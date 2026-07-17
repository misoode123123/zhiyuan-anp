package requirement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/mozillazg/go-pinyin"

	"zhiyuan-anp/platform/backend/internal/codetask"
	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/dev"
)

// AppResolver 按应用解析其托管仓库路径+端口（由 appdeploy.Store 实现）。
// 派发编码时，若需求已归属应用，自动用该应用仓库，无需手填 repo_dir；
// 若未归属应用，兜底自动创建一个托管应用并绑定（EnsureAppForRequirement）。
type AppResolver interface {
	ResolveApp(ctx context.Context, appID string) (repoDir string, port int, err error)
	// EnsureAppForRequirement 为需求兜底创建托管应用，返回 appID+repoDir+port。
	EnsureAppForRequirement(ctx context.Context, psID, appName string) (appID, repoDir string, port int, err error)
}

// Service 需求业务逻辑：调 AI 生成规格并入库（支持多模态：文字+图片→GLM-4V）。
type Service struct {
	repo            *Repository
	agentRuntimeURL string
	coder           *dev.CodingAgent
	compute         *compute.Store
	apps            AppResolver
}

// NewService 构造 Service。apps 可为 nil（不启用按应用派发）。
func NewService(repo *Repository, agentRuntimeURL string, coder *dev.CodingAgent, computeStore *compute.Store, apps AppResolver) *Service {
	return &Service{repo: repo, agentRuntimeURL: agentRuntimeURL, coder: coder, compute: computeStore, apps: apps}
}

// CreateInput 创建需求入参。
type CreateInput struct {
	ProjectSpaceID string
	ApplicationID  string // 可选：归属应用（应用一等公民）
	Description    string
	Images         []string // 图片 data URL（data:image/...;base64,...）或 http URL
}

type specResult struct {
	Title              string   `json:"title"`
	UserStory          string   `json:"user_story"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

type usageInfo struct {
	Model            string
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

const specSystemPrompt = `你是资深需求分析师。把用户的业务描述（可能附带界面截图/原型图）转为结构化需求规格。
严格只返回纯 JSON（不要 markdown、不要解释），格式：
{"title":"简洁需求标题","user_story":"作为<角色>，我希望<功能>，以便<价值>","acceptance_criteria":["可验证的验收点1","验收点2","验收点3"]}`

// Create：AI 生成规格 → 记录用量 → 入库。
func (s *Service) Create(ctx context.Context, in CreateInput) (*Requirement, error) {
	spec, usage, err := s.generateSpec(ctx, in.Description, in.Images)
	if err != nil {
		return nil, fmt.Errorf("生成需求规格: %w", err)
	}
	if s.compute != nil && usage != nil {
		_ = s.compute.Create(ctx, &compute.UsageRecord{
			ProjectSpaceID:   in.ProjectSpaceID,
			Model:            usage.Model,
			Kind:             "spec",
			PromptTokens:     usage.PromptTokens,
			CompletionTokens: usage.CompletionTokens,
			TotalTokens:      usage.TotalTokens,
		})
	}
	acJSON, _ := json.Marshal(spec.AcceptanceCriteria)
	req := &Requirement{
		ID:                 "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
		ProjectSpaceID:     in.ProjectSpaceID,
		ApplicationID:      in.ApplicationID,
		Title:              spec.Title,
		Description:        in.Description,
		UserStory:          spec.UserStory,
		AcceptanceCriteria: string(acJSON),
		Status:             "specified",
		Priority:           "P1", // 默认常规;后续可按紧急度调整
	}
	if err := s.repo.Create(ctx, req); err != nil {
		return nil, err
	}
	return req, nil
}

// Breakdown 调 AI 把需求拆成可执行子任务清单(JSON 数组 [{text,done}]),存 tasks 字段并返回。
func (s *Service) Breakdown(ctx context.Context, reqID string) (string, error) {
	req, err := s.repo.Get(ctx, reqID)
	if err != nil || req == nil || req.ID == "" {
		return "", fmt.Errorf("需求不存在")
	}
	prompt := fmt.Sprintf("把下面的需求拆成 3-6 个可执行的开发子任务,每项一个简短动作。严格只返回 JSON 数组(不要 markdown/解释),格式 [{\"text\":\"子任务\",\"done\":false}]:\n标题:%s\n用户故事:%s\n验收标准:%s", req.Title, req.UserStory, req.AcceptanceCriteria)
	buf, _ := json.Marshal(map[string]interface{}{
		"messages": []map[string]interface{}{{"role": "user", "content": prompt}},
	})
	hr, _ := http.NewRequestWithContext(ctx, "POST", s.agentRuntimeURL+"/v1/chat", bytes.NewReader(buf))
	hr.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(hr)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Content string `json:"content"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Error != "" {
		return "", fmt.Errorf(out.Error)
	}
	tasks := strings.TrimSpace(extractJSON(out.Content))
	if !strings.HasPrefix(tasks, "[") {
		tasks = "[" + tasks + "]" // GLM 偶尔漏掉数组外层括号,补上保证合法 JSON 数组
	}
	_ = s.repo.UpdateTasks(ctx, reqID, tasks)
	return tasks, nil
}

// List 列出项目空间下的需求。
func (s *Service) List(ctx context.Context, projectSpaceID string) ([]Requirement, error) {
	return s.repo.List(ctx, projectSpaceID)
}

// ListByApp 列出某应用下的需求。
func (s *Service) ListByApp(ctx context.Context, appID string) ([]Requirement, error) {
	return s.repo.ListByApp(ctx, appID)
}

// Dispatch 把需求规格异步派发给编码引擎，返回异步任务。
// repo_dir 优先级：显式传入 > 需求归属应用的托管仓库（应用一等公民：代码归属确定）。
func (s *Service) Dispatch(ctx context.Context, projectSpaceID, reqID, repoDir, model string) (*codetask.Task, error) {
	if s.coder == nil {
		return nil, fmt.Errorf("编码引擎未配置")
	}
	req, err := s.repo.Get(ctx, reqID)
	if err != nil {
		return nil, fmt.Errorf("读取需求: %w", err)
	}
	if req == nil || req.ID == "" {
		return nil, fmt.Errorf("需求 %s 不存在", reqID)
	}
	// repo_dir 优先级：显式传入 > 需求归属应用的托管仓库 > 兜底为需求自动创建托管应用。
	// 应用一等公民：代码位置始终确定，派发永不因"未归属应用"阻塞。
	if repoDir == "" && s.apps != nil {
		if req.ApplicationID == "" {
			// 未归属应用：兜底创建托管应用（req-<短id>，ASCII 确定名）并绑定到需求。
			appID, rd, _, e := s.apps.EnsureAppForRequirement(ctx, projectSpaceID, deriveAppName(req.Title, req.ID))
			if e != nil {
				return nil, fmt.Errorf("为需求兜底创建托管应用失败: %w", e)
			}
			req.ApplicationID = appID
			repoDir = rd
			_ = s.repo.SetApplication(ctx, req.ID, appID) // 绑定，后续派发/发布自动归属
		} else if rd, _, e := s.apps.ResolveApp(ctx, req.ApplicationID); e == nil {
			repoDir = rd
		}
	}
	if repoDir == "" {
		return nil, fmt.Errorf("无法确定代码位置：需求未归属应用且自动创建托管应用失败")
	}
	_ = s.repo.UpdateStatus(ctx, reqID, "developing") // 需求进入开发(specified→developing→delivered)
	return s.coder.Submit(ctx, projectSpaceID, "dispatch", reqID, repoDir, buildCodePrompt(req), model)
}

// deriveAppName 为未归属应用的需求派生一个友好的托管应用名。
// 用需求标题的拼音做主名（中文→ASCII，可读），截断 + reqID 短后缀保证唯一与稳定（同需求重派发复用）。
func deriveAppName(title, reqID string) string {
	base := pinyinSlug(title)
	if base == "" {
		base = "app"
	}
	if len(base) > 24 {
		base = base[:24]
	}
	return base + "-" + shortSuffix(reqID)
}

// pinyinSlug 把字符串转为 ASCII slug：中文→拼音音节(每字一词)，连续 ASCII 字母数字合并为一词，其余作分隔。
// 例："贪吃蛇H5游戏" → "tan-chi-she-h5-you-xi"。
func pinyinSlug(s string) string {
	a := pinyin.NewArgs()
	var words []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			words = append(words, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r >= 0x4e00 && r <= 0x9fff: // CJK 基本区：每字一音节
			flush()
			if ps := pinyin.Pinyin(string(r), a); len(ps) > 0 && len(ps[0]) > 0 {
				words = append(words, ps[0][0])
			}
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			cur.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			cur.WriteRune(r + 32) // 小写
		default:
			flush() // 标点/空格 → 词界
		}
	}
	flush()
	return strings.Join(words, "-")
}

// shortSuffix 取需求 id 末段（去 _、小写）做唯一后缀。
func shortSuffix(reqID string) string {
	s := strings.ToLower(strings.ReplaceAll(reqID, "_", ""))
	if len(s) > 8 {
		s = s[len(s)-8:]
	}
	return s
}

// buildCodePrompt 把需求规格拼装为编码 prompt（单行）。
// 要求产出"完整可独立运行的 Web 服务"，使其可被平台应用部署引擎构建部署。
func buildCodePrompt(r *Requirement) string {
	var ac []string
	_ = json.Unmarshal([]byte(r.AcceptanceCriteria), &ac)
	var b strings.Builder
	b.WriteString("请实现以下需求规格。")
	b.WriteString(" 标题：" + r.Title + "。")
	b.WriteString(" 用户故事：" + r.UserStory + "。")
	b.WriteString(" 验收标准：")
	for i, c := range ac {
		fmt.Fprintf(&b, "%d. %s；", i+1, c)
	}
	if r.Description != "" {
		b.WriteString(" 补充描述：" + r.Description + "。")
	}
	// 可部署性约束：产出必须是完整可独立运行的 Web 服务（非库/模块），便于平台自动构建部署。
	b.WriteString(deployableServiceHint)
	return b.String()
}

// deployableServiceHint 派发编码的可部署性约束：产出须为完整可独立运行的 Web 服务。
const deployableServiceHint = ` 【交付要求】产出必须是完整可独立运行的 Web 服务（含 main 入口，不是库/模块）：用一个 HTTP 服务监听 0.0.0.0:${PORT:-8080}，实现上述核心功能，并提供 GET / 返回 200 的健康检查；自包含可运行，依赖写入 go.mod/requirements.txt/package.json 之一；无需写 Dockerfile（平台按类型自动生成）。`

// generateSpec 调 agent-runtime 让 GLM 生成规格（有图片时走 GLM-4V 多模态）。
func (s *Service) generateSpec(ctx context.Context, description string, images []string) (*specResult, *usageInfo, error) {
	// 构造 user content：有图片则多模态（content 数组），否则纯文本
	var userContent interface{}
	model := ""
	if len(images) > 0 {
		parts := []map[string]interface{}{{"type": "text", "text": description}}
		for _, img := range images {
			parts = append(parts, map[string]interface{}{
				"type":      "image_url",
				"image_url": map[string]string{"url": img},
			})
		}
		userContent = parts
		model = "zhipu/glm-4v-flash" // 视觉模型
	} else {
		userContent = description
	}

	body := map[string]interface{}{
		"messages": []map[string]interface{}{
			{"role": "system", "content": specSystemPrompt},
			{"role": "user", "content": userContent},
		},
	}
	if model != "" {
		body["model"] = model
	}

	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.agentRuntimeURL+"/v1/chat", bytes.NewReader(buf))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	var out struct {
		Content string `json:"content"`
		Error   string `json:"error"`
		Model   string `json:"model"`
		Usage   *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, nil, fmt.Errorf("解析 AI 响应: %w", err)
	}
	if out.Error != "" {
		return nil, nil, fmt.Errorf("AI 返回错误: %s", out.Error)
	}

	raw := extractJSON(out.Content)
	var spec specResult
	if err := json.Unmarshal([]byte(raw), &spec); err != nil {
		return nil, nil, fmt.Errorf("解析需求规格 JSON 失败: %w（AI 原文: %s）", err, out.Content)
	}
	var u *usageInfo
	if out.Usage != nil {
		u = &usageInfo{
			Model: out.Model, PromptTokens: out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens, TotalTokens: out.Usage.TotalTokens,
		}
	}
	return &spec, u, nil
}

// extractJSON 从可能含 markdown 的文本中提取首个 JSON 对象。
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
