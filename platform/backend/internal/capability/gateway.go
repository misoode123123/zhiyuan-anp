package capability

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Gateway 统一调用网关：APIKey 鉴权 → 技能校验 → 调 agent-runtime → 记用量。
type Gateway struct {
	store           *Store
	agentRuntimeURL string
	defaultModel    string
	client          *http.Client
}

// NewGateway 构造。
func NewGateway(store *Store, agentRuntimeURL, defaultModel string) *Gateway {
	return &Gateway{
		store: store, agentRuntimeURL: agentRuntimeURL, defaultModel: defaultModel,
		client: &http.Client{Timeout: 60 * time.Second},
	}
}

// InvokeResult 一次调用的结果。
type InvokeResult struct {
	Content      string `json:"content"`
	RenderHint   string `json:"render_hint"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TraceID      string `json:"trace_id"`
}

// Invoke 执行技能调用：鉴权 → 校验技能 → 调模型 → 记用量。
// 错误以业务语义返回（鉴权失败/技能不可用/模型错误）。
func (g *Gateway) Invoke(ctx context.Context, apiKey, skillCode, input, renderHint, traceID string) (*InvokeResult, error) {
	// 1. APIKey 鉴权
	key, err := g.store.LookupAPIKey(ctx, apiKey)
	if err != nil {
		return nil, ErrAuth
	}
	if key.ExpiresAt != nil && time.Now().After(*key.ExpiresAt) {
		return nil, ErrAuth
	}

	// 2. 技能校验（必须 active）
	skill, err := g.store.GetSkillByCode(ctx, skillCode)
	if err != nil {
		return nil, ErrSkillUnavailable
	}
	// allowed_skills 限制
	if key.AllowedSkills != "" {
		allowed := splitCSV(key.AllowedSkills)
		if !contains(allowed, skillCode) {
			return nil, ErrNotAllowed
		}
	}

	// 3. 构造 prompt（模板 {input} 占位，无模板则按描述组装）
	prompt := skill.PromptTemplate
	if prompt == "" {
		prompt = "你是「" + skill.Name + "」技能。" + skill.Description + "\n\n用户输入：" + input
	} else {
		prompt = strings.ReplaceAll(prompt, "{input}", input)
	}

	// 4. 调 agent-runtime /v1/chat
	start := time.Now()
	content, inTok, outTok, callErr := g.callChat(ctx, prompt)
	latency := int(time.Since(start).Milliseconds())

	// 5. 记用量（成功/失败均记录，便于监控）
	_ = g.store.RecordUsage(ctx, &CapabilityUsage{
		ProjectSpaceID: key.ProjectSpaceID, APIKeyID: key.ID, CallerApp: key.AppName,
		SkillID: skill.ID, InputTokens: inTok, OutputTokens: outTok,
		Success: callErr == nil, LatencyMS: latency, RenderHint: renderHint, TraceID: traceID,
	})
	if callErr != nil {
		return nil, fmt.Errorf("模型调用失败: %w", callErr)
	}
	return &InvokeResult{
		Content: content, RenderHint: renderHint,
		InputTokens: inTok, OutputTokens: outTok, TraceID: traceID,
	}, nil
}

// callChat 调 agent-runtime /v1/chat，返回 (content, input_tokens, output_tokens, err)。
func (g *Gateway) callChat(ctx context.Context, prompt string) (string, int, int, error) {
	model := g.defaultModel
	if model == "" {
		model = "glm-4-flash"
	}
	body := map[string]interface{}{
		"model":    model,
		"messages": []map[string]string{{"role": "user", "content": prompt}},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, g.agentRuntimeURL+"/v1/chat", bytes.NewReader(buf))
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := g.client.Do(req)
	if err != nil {
		return "", 0, 0, err
	}
	defer resp.Body.Close()
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", 0, 0, err
	}
	content := ""
	if len(out.Choices) > 0 {
		content = out.Choices[0].Message.Content
	}
	return content, out.Usage.PromptTokens, out.Usage.CompletionTokens, nil
}

// 业务错误哨兵值。
var (
	ErrAuth             = fmt.Errorf("APIKey 无效或已失效")
	ErrSkillUnavailable = fmt.Errorf("技能不存在或未上架")
	ErrNotAllowed       = fmt.Errorf("该 APIKey 未被授权调用此技能")
)

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
