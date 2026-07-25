package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Route 路由策略（任务类型 → 模型）。
type Route struct {
	ID               string    `json:"id" db:"id"`
	TaskType         string    `json:"task_type" db:"task_type"`
	PrimaryModelID   string    `json:"primary_model_id" db:"primary_model_id"`
	FallbackModelID  *string   `json:"fallback_model_id,omitempty" db:"fallback_model_id"`
	Priority         int       `json:"priority" db:"priority"`
	Enabled          bool      `json:"enabled" db:"enabled"`
	UpdatedAt        time.Time `json:"updated_at" db:"updated_at"`
}

// --- Route Store ---

func (s *Store) ListRoutes(ctx context.Context) ([]Route, error) {
	var list []Route
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, task_type, primary_model_id, fallback_model_id, priority, enabled, updated_at
		 FROM compute_route ORDER BY task_type`)
	return list, err
}

func (s *Store) UpsertRoute(ctx context.Context, r *Route) error {
	if r.ID == "" {
		r.ID = "crt_" + uuid.NewString()[:19]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO compute_route (id, task_type, primary_model_id, fallback_model_id, priority, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT (task_type) DO UPDATE SET
		   primary_model_id = EXCLUDED.primary_model_id,
		   fallback_model_id = EXCLUDED.fallback_model_id,
		   priority = EXCLUDED.priority,
		   enabled = EXCLUDED.enabled,
		   updated_at = NOW()`,
		r.ID, r.TaskType, r.PrimaryModelID, r.FallbackModelID, r.Priority, r.Enabled)
	return err
}

func (s *Store) GetRoute(ctx context.Context, taskType string) (*Route, error) {
	var r Route
	err := s.db.GetContext(ctx, &r,
		`SELECT id, task_type, primary_model_id, fallback_model_id, priority, enabled, updated_at
		 FROM compute_route WHERE task_type = $1 AND enabled = TRUE`, taskType)
	return &r, err
}

// GetModelWithProvider 查模型 + 其供应商（网关路由用）。
func (s *Store) GetModelWithProvider(ctx context.Context, modelID string) (*Model, *Provider, error) {
	var m Model
	if err := s.db.GetContext(ctx, &m,
		`SELECT id, provider_id, name, display_name, modality, context_window, max_output, cost_input, cost_output, enabled
		 FROM compute_model WHERE id = $1 AND enabled = TRUE`, modelID); err != nil {
		return nil, nil, err
	}
	var p Provider
	if err := s.db.GetContext(ctx, &p,
		`SELECT id, name, type, base_url, api_key, enabled FROM compute_provider WHERE id = $1`, m.ProviderID); err != nil {
		return nil, nil, err
	}
	return &m, &p, nil
}

// SeedRoutes 若 compute_route 为空，seed 默认路由（需已有 model，由 main 调）。
func SeedRoutes(ctx context.Context, store *Store) error {
	var n int
	existing, err := store.ListRoutes(ctx)
	n = len(existing)
	if err != nil || n > 0 {
		return nil
	}
	models, err := store.ListModels(ctx, "")
	if err != nil || len(models) == 0 {
		return nil // 无模型则跳过
	}
	first := models[0].ID
	taskTypes := []string{"spec", "code", "test", "review", "chat", "general"}
	for _, tt := range taskTypes {
		_ = store.UpsertRoute(ctx, &Route{TaskType: tt, PrimaryModelID: first, Enabled: true})
	}
	return nil
}

// --- 统一网关 ---

// ChatRequest 统一对话请求。
type ChatRequest struct {
	TaskType       string                   `json:"task_type"`
	Model          string                   `json:"model,omitempty"`  // 直接指定模型 ID（绕过路由）
	Messages       []map[string]interface{} `json:"messages"`
	ProjectSpaceID string                   `json:"project_space_id,omitempty"`
}

// ChatResponse 统一对话响应。
type ChatResponse struct {
	Model       string                 `json:"model"`
	Content     string                 `json:"content"`
	Usage       map[string]interface{} `json:"usage,omitempty"`
	Provider    string                 `json:"provider"`
	Error       string                 `json:"error,omitempty"`
}

// Gateway 统一模型网关：路由 → 选模型 → 转发到 provider → 记账。
type Gateway struct {
	store *Store
}

// NewGateway 构造。
func NewGateway(store *Store) *Gateway { return &Gateway{store: store} }

// Chat 统一对话入口。
func (g *Gateway) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	var modelID string

	// 1. 路由选模型
	if req.Model != "" {
		modelID = req.Model // 直接指定
	} else {
		rt, err := g.store.GetRoute(ctx, req.TaskType)
		if err != nil || rt == nil || rt.PrimaryModelID == "" {
			// 无路由 → 取第一个 enabled 模型
			models, _ := g.store.ListModels(ctx, "")
			if len(models) == 0 {
				return nil, fmt.Errorf("无可用模型（请先在算力中心添加供应商+模型）")
			}
			modelID = models[0].ID
		} else {
			modelID = rt.PrimaryModelID
		}
	}

	// 2. 查模型 + 供应商
	m, p, err := g.store.GetModelWithProvider(ctx, modelID)
	if err != nil {
		return nil, fmt.Errorf("查模型/供应商失败: %w", err)
	}
	if !p.Enabled {
		return nil, fmt.Errorf("供应商 %s 已禁用", p.Name)
	}

	// 3. 转发到 provider（OpenAI-compatible 格式）
	resp, err := g.forward(ctx, p, m, req.Messages)
	if err != nil {
		// fallback
		rt, _ := g.store.GetRoute(ctx, req.TaskType)
		if rt != nil && rt.FallbackModelID != nil && *rt.FallbackModelID != "" && *rt.FallbackModelID != modelID {
			m2, p2, err2 := g.store.GetModelWithProvider(ctx, *rt.FallbackModelID)
			if err2 == nil && p2.Enabled {
				resp, err = g.forward(ctx, p2, m2, req.Messages)
				if err == nil {
					m, p = m2, p2
				}
			}
		}
		if err != nil {
			return nil, fmt.Errorf("主模型+fallback 均失败: %w", err)
		}
	}

	// 4. 记账（usage_record）
	if resp.Usage != nil {
		g.recordUsage(ctx, req.ProjectSpaceID, p.ID, m.ID, resp.Usage, m)
	}

	resp.Provider = p.Name
	return resp, nil
}

// forward 转发到 provider（OpenAI-compatible /v1/chat/completions），含 retry。
func (g *Gateway) forward(ctx context.Context, p *Provider, m *Model, messages []map[string]interface{}) (*ChatResponse, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ { // 最多 2 次（1 次正常 + 1 次 retry）
		resp, err := g.doForward(ctx, p, m, messages)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		// 仅对网络/5xx 错误 retry，4xx（参数/key 错误）不 retry
		if !isRetryable(err) {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Second):
		}
	}
	return nil, lastErr
}

// isRetryable 判断错误是否值得 retry（网络超时/5xx）。
func isRetryable(err error) bool {
	s := err.Error()
	return strings.Contains(s, "timeout") || strings.Contains(s, "EOF") ||
		strings.Contains(s, "connection refused") || strings.Contains(s, "返回 5")
}

// doForward 实际转发逻辑（单次，无 retry）。
func (g *Gateway) doForward(ctx context.Context, p *Provider, m *Model, messages []map[string]interface{}) (*ChatResponse, error) {
	body := map[string]interface{}{
		"model":    m.Name,
		"messages": messages,
	}
	buf, _ := json.Marshal(body)

	url := strings.TrimRight(p.BaseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(string(buf)))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if p.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+p.APIKey)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("provider 返回 %d: %s", resp.StatusCode, string(raw[:min(len(raw), 200)]))
		// Go 1.25 内置 min(int, int) int
	}

	// 解析 OpenAI 格式响应
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return &ChatResponse{Model: m.Name, Content: string(raw)}, nil
	}

	content := ""
	if len(result.Choices) > 0 {
		content = result.Choices[0].Message.Content
	}
	var usage map[string]interface{}
	if result.Usage != nil {
		usage = map[string]interface{}{
			"prompt_tokens":     result.Usage.PromptTokens,
			"completion_tokens": result.Usage.CompletionTokens,
			"total_tokens":      result.Usage.TotalTokens,
		}
	}
	return &ChatResponse{
		Model:   result.Model,
		Content: content,
		Usage:   usage,
	}, nil
}

// recordUsage 记账（含 cost 入库）。
func (g *Gateway) recordUsage(ctx context.Context, psID, providerID, modelID string, usage map[string]interface{}, m *Model) {
	prompt, _ := usage["prompt_tokens"].(int)
	completion, _ := usage["completion_tokens"].(int)
	total, _ := usage["total_tokens"].(int)
	cost := float64(prompt)/1000*m.CostInput + float64(completion)/1000*m.CostOutput
	_ = g.store.Create(ctx, &UsageRecord{
		ProjectSpaceID:   psID,
		Model:            modelID,
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      total,
	})
	// cost 入 usage_record（UPDATE，因为 Create 时没传 cost 列）
	if cost > 0 {
		_, _ = g.store.db.ExecContext(ctx,
			`UPDATE usage_record SET cost = $1 WHERE model = $2 AND total_tokens = $3 AND project_space_id = $4 ORDER BY id DESC LIMIT 1`,
			cost, modelID, total, psID)
	}
}

// min 在 Go 1.21+ 是内置函数，此处无需定义。
