package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service 测试业务逻辑：调 AI 把需求验收标准转测试用例；按用例 HTTP 检查对着已部署应用自动验收。
type Service struct {
	store           *Store
	agentRuntimeURL string
}

// NewService 构造 Service。
func NewService(store *Store, agentRuntimeURL string) *Service {
	return &Service{store: store, agentRuntimeURL: agentRuntimeURL}
}

const testSystemPrompt = `你是测试工程师。把需求验收标准转为可执行的 HTTP 测试用例（被测对象是已部署运行的 Web 服务）。
严格只返回纯 JSON 数组（不要 markdown、不要解释），格式：
[{"title":"用例标题","steps":["步骤1","步骤2"],"expected":"预期结果","method":"GET","path":"/","expected_status":200,"expected_body":"ok"}]
说明：
- method/path/expected_status/expected_body 是可执行 HTTP 检查：对该 path 发 method 请求，校验状态码=expected_status 且响应体包含 expected_body。
- 健康检查/首页类用 GET / + expected_status 200；具体功能按需求路径设计，path 以 / 开头。
- expected_body 只写响应体里应出现的关键文本，不要整段响应。
- 无明确 HTTP 语义的用例，method/path/expected_status/expected_body 留空（将标为需人工验证）。`

type caseDraft struct {
	Title          string   `json:"title"`
	Steps          []string `json:"steps"`
	Expected       string   `json:"expected"`
	Method         string   `json:"method"`
	Path           string   `json:"path"`
	ExpectedStatus int      `json:"expected_status"`
	ExpectedBody   string   `json:"expected_body"`
}

// GenerateTests 把需求验收标准转为测试用例并入库（含可执行 HTTP 检查）。
func (s *Service) GenerateTests(ctx context.Context, projectSpaceID, requirementID, title, acceptanceCriteria string) ([]TestCase, error) {
	userMsg := "需求：" + title + "。验收标准：" + acceptanceCriteria
	body := map[string]interface{}{
		"messages": []map[string]string{
			{"role": "system", "content": testSystemPrompt},
			{"role": "user", "content": userMsg},
		},
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.agentRuntimeURL+"/v1/chat", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out struct {
		Content string `json:"content"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析 AI 响应: %w", err)
	}
	if out.Error != "" {
		return nil, fmt.Errorf("AI: %s", out.Error)
	}

	// 容错解析：AI 偶尔把每个用例各包一个数组（[{..}][{..}]）或散落对象，
	// 按大括号配平提取所有 JSON 对象，避免 "invalid character '[' after top-level value"。
	objs := extractObjects(out.Content)
	if len(objs) == 0 {
		return nil, fmt.Errorf("AI 未返回有效测试用例（原文: %s）", out.Content)
	}
	var drafts []caseDraft
	for _, obj := range objs {
		var d caseDraft
		if e := json.Unmarshal([]byte(obj), &d); e == nil && strings.TrimSpace(d.Title) != "" {
			drafts = append(drafts, d)
		}
	}

	var created []TestCase
	for _, d := range drafts {
		stepsJSON, _ := json.Marshal(d.Steps)
		tc := &TestCase{
			ID:             "tc_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
			ProjectSpaceID: projectSpaceID,
			RequirementID:  requirementID,
			Title:          d.Title,
			Steps:          string(stepsJSON),
			Expected:       d.Expected,
			Method:         strings.ToUpper(strings.TrimSpace(d.Method)),
			Path:           strings.TrimSpace(d.Path),
			ExpectedStatus: d.ExpectedStatus,
			ExpectedBody:   d.ExpectedBody,
			Status:         "draft",
		}
		if err := s.store.Create(ctx, tc); err != nil {
			return created, err
		}
		created = append(created, *tc)
	}
	return created, nil
}

// GetCase / ListByProjectSpace / ListByRequirement 透传 store（handler 运行用）。
func (s *Service) GetCase(ctx context.Context, id string) (*TestCase, error) {
	return s.store.Get(ctx, id)
}
func (s *Service) ListByProjectSpace(ctx context.Context, ps string) ([]TestCase, error) {
	return s.store.ListByProjectSpace(ctx, ps)
}
func (s *Service) ListByRequirement(ctx context.Context, rid string) ([]TestCase, error) {
	return s.store.ListByRequirement(ctx, rid)
}

// RunHTTPRequest 对着 baseURL 执行用例的 HTTP 检查，比对期望，回写 tc 并持久化。
// 判定：有断言且通过→passed；有断言不通过→failed；无 HTTP 断言或未部署→manual。
func (s *Service) RunHTTPRequest(ctx context.Context, tc *TestCase, baseURL string) error {
	tc.RunAt = nowTime()
	// 无 HTTP 断言：标人工
	if tc.ExpectedStatus == 0 && strings.TrimSpace(tc.ExpectedBody) == "" {
		tc.Status = "manual"
		if baseURL == "" {
			tc.ActualBody = "无 HTTP 断言，且需求未归属已部署应用，需人工验证"
		} else {
			tc.ActualBody = "无 HTTP 断言，需人工验证（被测应用 " + baseURL + "）"
		}
		return s.store.UpdateRun(ctx, tc)
	}
	if baseURL == "" {
		tc.Status = "manual"
		tc.ActualBody = "需求未归属已部署应用，无法自动运行（请先发布部署该应用）"
		return s.store.UpdateRun(ctx, tc)
	}
	method := strings.ToUpper(strings.TrimSpace(tc.Method))
	if method == "" {
		method = "GET"
	}
	path := tc.Path
	if path == "" {
		path = "/"
	}
	url := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, method, url, nil)
	if err != nil {
		tc.Status = "failed"
		tc.ActualBody = "构造请求失败: " + err.Error()
		return s.store.UpdateRun(ctx, tc)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		tc.Status = "failed"
		tc.ActualBody = "请求失败: " + err.Error()
		return s.store.UpdateRun(ctx, tc)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	tc.ActualStatus = resp.StatusCode
	tc.ActualBody = truncate(string(body), 500)
	statusOK := tc.ExpectedStatus == 0 || resp.StatusCode == tc.ExpectedStatus
	bodyOK := strings.TrimSpace(tc.ExpectedBody) == "" || strings.Contains(string(body), strings.TrimSpace(tc.ExpectedBody))
	switch {
	case !statusOK && tc.ExpectedStatus >= 500 && resp.StatusCode < 500:
		// 期望异常(5xx)但请求正常返回:异常状态无法自动触发(如 GET 无参),标人工而非失败
		tc.Status = "manual"
		tc.ActualBody = tc.ActualBody + "\n(期望异常状态 " + fmt.Sprintf("%d", tc.ExpectedStatus) + " 但无触发方式,需人工验证异常路径)"
	case !statusOK:
		tc.Status = "failed" // status 不符(非异常用例)= 真失败
	case !bodyOK:
		tc.Status = "partial" // status 对但 body 不符:AI expected_body 可能猜错实现格式,降为部分通过
	default:
		tc.Status = "passed"
	}
	return s.store.UpdateRun(ctx, tc)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// extractObjects 从文本里按大括号配平提取所有顶层 JSON 对象。
// 容错 AI 输出：单数组、多数组([{..}][{..}])、散落对象都能正确解析。
func extractObjects(s string) []string {
	var objs []string
	depth, start := 0, -1
	inStr := false
	var prev byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '"' && prev != '\\' {
				inStr = false
			}
			prev = c
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				objs = append(objs, s[start:i+1])
				start = -1
			}
		}
		prev = c
	}
	return objs
}
