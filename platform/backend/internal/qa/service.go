package qa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Service 测试业务逻辑：调 AI 把需求验收标准转测试用例。
type Service struct {
	store           *Store
	agentRuntimeURL string
}

// NewService 构造 Service。
func NewService(store *Store, agentRuntimeURL string) *Service {
	return &Service{store: store, agentRuntimeURL: agentRuntimeURL}
}

const testSystemPrompt = `你是测试工程师。把需求验收标准转为可执行的测试用例。
严格只返回纯 JSON 数组（不要 markdown、不要解释），格式：
[{"title":"用例标题","steps":["步骤1","步骤2"],"expected":"预期结果"}]`

type caseDraft struct {
	Title    string   `json:"title"`
	Steps    []string `json:"steps"`
	Expected string   `json:"expected"`
}

// GenerateTests 把需求验收标准转为测试用例并入库。
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

	raw := extractArray(out.Content)
	var drafts []caseDraft
	if err := json.Unmarshal([]byte(raw), &drafts); err != nil {
		return nil, fmt.Errorf("解析测试用例 JSON 失败: %w（原文: %s）", err, out.Content)
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
			Status:         "draft",
		}
		if err := s.store.Create(ctx, tc); err != nil {
			return created, err
		}
		created = append(created, *tc)
	}
	return created, nil
}

// extractArray 提取首个 JSON 数组。
func extractArray(s string) string {
	start := strings.Index(s, "[")
	end := strings.LastIndex(s, "]")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
