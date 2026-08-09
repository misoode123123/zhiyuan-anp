package compute

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
)

// OpenCodeConfig opencode.json 的 Go 表示。
type OpenCodeConfig struct {
	Schema     string                       `json:"$schema"`
	Provider   map[string]*OpenCodeProvider `json:"provider"`
	Model      string                       `json:"model,omitempty"`       // 顶层默认模型(provider/model)；空→opencode 回退内置免费模型
	SmallModel string                       `json:"small_model,omitempty"` // 后台任务(标题/摘要)用的小模型；空→opencode 内置免费 big-pickle(易 429)
}

// OpenCodeProvider opencode 的 provider 配置。
type OpenCodeProvider struct {
	NPM     string                    `json:"npm"`
	Name    string                    `json:"name"`
	Options OpenCodeProviderOptions   `json:"options"`
	Models  map[string]*OpenCodeModel `json:"models,omitempty"`
}

// OpenCodeProviderOptions provider 选项。
type OpenCodeProviderOptions struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey,omitempty"`
}

// OpenCodeModel opencode 的 model 配置。
type OpenCodeModel struct {
	Name      string         `json:"name"`
	Reasoning bool           `json:"reasoning,omitempty"`
	Limit     *OpenCodeLimit `json:"limit,omitempty"`
}

// OpenCodeLimit 上下文/输出限制。
type OpenCodeLimit struct {
	Context int `json:"context,omitempty"`
	Output  int `json:"output,omitempty"`
}

// GenerateOpenCodeConfig 从 DB 读 enabled providers+models，生成 opencode.json 结构。
func (s *Store) GenerateOpenCodeConfig(ctx context.Context) (*OpenCodeConfig, error) {
	providers, err := s.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	cfg := &OpenCodeConfig{
		Schema:   "https://opencode.ai/config.json",
		Provider: map[string]*OpenCodeProvider{},
	}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		// 无 key provider 不进 config：opencode 会用空 key 调用 → 401 空白（#30）。
		// 跳过该 provider，用户在 opencode 里看不到/用不了其模型，从源头杜绝 401。
		if p.APIKey == "" {
			log.Printf("[compute] 跳过无 APIKey 的 provider(不进 opencode config): id=%s name=%s", p.ID, p.Name)
			continue
		}
		// provider key：取 name 的 slug 化（去空格、小写、- 分隔）
		key := slugify(p.Name)
		provider := &OpenCodeProvider{
			NPM:  "@ai-sdk/openai-compatible",
			Name: p.Name,
			Options: OpenCodeProviderOptions{
				BaseURL: p.BaseURL,
				APIKey:  p.APIKey,
			},
			Models: map[string]*OpenCodeModel{},
		}
		// 加载该 provider 的 enabled models
		models, err := s.ListModels(ctx, p.ID)
		if err != nil {
			continue
		}
		for _, m := range models {
			if !m.Enabled {
				continue
			}
			om := &OpenCodeModel{
				Name:      firstNonEmpty(m.DisplayName, m.Name),
				Reasoning: m.Modality == "code" || m.Modality == "text",
			}
			if m.ContextWindow > 0 || m.MaxOutput > 0 {
				// opencode 1.18+ 校验 limit.output 必须存在；DB 漏填某项时补默认，
				// 否则生成的 opencode.json 缺 output → serve 启动校验失败、工作台打不开。
				ctx := m.ContextWindow
				if ctx == 0 {
					ctx = 131072
				}
				out := m.MaxOutput
				if out == 0 {
					out = 8192
				}
				om.Limit = &OpenCodeLimit{Context: ctx, Output: out}
			}
			provider.Models[m.Name] = om
		}
		// 至少有 1 个 enabled model 才加入
		if len(provider.Models) > 0 {
			cfg.Provider[key] = provider
		}
	}
	return cfg, nil
}

// WriteOpenCodeConfig 生成 opencode.json 并写到指定路径。
// path 为空时取 system_config 的 opencode_config_path（调用方传入）。
func (s *Store) WriteOpenCodeConfig(ctx context.Context, path string) error {
	cfg, err := s.GenerateOpenCodeConfig(ctx)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(abs, buf, 0644)
}

// GenerateOpenCodeConfigForModels 生成只含指定 modelIDs（及其 provider）的 opencode config。
// 未命中的模型跳过；provider 仅当有 ≥1 命中模型才加入。
// provider key（slugify(p.Name)）与 model key（m.Name）推导与 GenerateOpenCodeConfig 完全一致，
// 确保与全局 config 同构（codews per-user XDG config 注入用）。
func (s *Store) GenerateOpenCodeConfigForModels(ctx context.Context, modelIDs []string) (*OpenCodeConfig, error) {
	wanted := make(map[string]struct{}, len(modelIDs))
	for _, id := range modelIDs {
		wanted[id] = struct{}{}
	}
	providers, err := s.ListProviders(ctx)
	if err != nil {
		return nil, err
	}
	cfg := &OpenCodeConfig{
		Schema:   "https://opencode.ai/config.json",
		Provider: map[string]*OpenCodeProvider{},
	}
	for _, p := range providers {
		if !p.Enabled {
			continue
		}
		// 无 key provider 不进 config：opencode 会用空 key 调用 → 401 空白（#30）。
		// 跳过该 provider，用户在 opencode 里看不到/用不了其模型，从源头杜绝 401。
		if p.APIKey == "" {
			log.Printf("[compute] 跳过无 APIKey 的 provider(不进 opencode config): id=%s name=%s", p.ID, p.Name)
			continue
		}
		key := slugify(p.Name)
		provider := &OpenCodeProvider{
			NPM:  "@ai-sdk/openai-compatible",
			Name: p.Name,
			Options: OpenCodeProviderOptions{
				BaseURL: p.BaseURL,
				APIKey:  p.APIKey,
			},
			Models: map[string]*OpenCodeModel{},
		}
		models, err := s.ListModels(ctx, p.ID)
		if err != nil {
			continue
		}
		for _, m := range models {
			if !m.Enabled {
				continue
			}
			// 仅含授权模型：不在 modelIDs 集合内则跳过
			if _, ok := wanted[m.ID]; !ok {
				continue
			}
			om := &OpenCodeModel{
				Name:      firstNonEmpty(m.DisplayName, m.Name),
				Reasoning: m.Modality == "code" || m.Modality == "text",
			}
			if m.ContextWindow > 0 || m.MaxOutput > 0 {
				// 与 GenerateOpenCodeConfig 一致：DB 漏填某项时补默认，否则缺 output
				// → opencode serve 启动校验失败。逻辑 verbatim（131072/8192）。
				cw := m.ContextWindow
				if cw == 0 {
					cw = 131072
				}
				out := m.MaxOutput
				if out == 0 {
					out = 8192
				}
				om.Limit = &OpenCodeLimit{Context: cw, Output: out}
			}
			provider.Models[m.Name] = om
			// 默认模型：首个授权模型作为 opencode 顶层 model + small_model 默认。不设则
			// opencode 回退其内置免费模型(如 big-pickle)当默认/后台小模型 → 界面默认显示免费
			// 模型 + 后台任务 429 FreeUsageLimitError，用户以为"模型不对/没反应"。ref 格式与
			// ResolveOpencodeModelID 一致：slugify(providerName)/modelName。
			if cfg.Model == "" {
				ref := key + "/" + m.Name
				cfg.Model = ref
				cfg.SmallModel = ref
			}
		}
		// 至少有 1 个命中 model 才加入
		if len(provider.Models) > 0 {
			cfg.Provider[key] = provider
		}
	}
	return cfg, nil
}

// WriteOpenCodeConfigForModels 生成 per-model opencode.json 并写到 path（按需建父目录）。
// path 为空时直接返回（与 WriteOpenCodeConfig 一致的兜底）。
func (s *Store) WriteOpenCodeConfigForModels(ctx context.Context, modelIDs []string, path string) error {
	cfg, err := s.GenerateOpenCodeConfigForModels(ctx, modelIDs)
	if err != nil {
		return err
	}
	if path == "" {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(abs, buf, 0644)
}

// ResolveOpencodeModelID 把 compute_model.id（cmd_xxx）解析为 opencode 的 "provider/name"。
// 未命中返 ("", nil)；孤儿 model（provider 不存在，FK 上不应出现）亦返 ("", nil)。
// provider 段用 slugify(provider.Name)，与 GenerateOpenCodeConfig 的 provider key 一致。
func (s *Store) ResolveOpencodeModelID(ctx context.Context, modelID string) (string, error) {
	m, err := s.getModelByID(ctx, modelID)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil
	}
	p, err := s.GetProvider(ctx, m.ProviderID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	// 无 key 不解析为可用模型 ref：建会话时 modelRef="" → initSession 不指定该模型，
	// 避免 opencode 用空 key 调用 → 401 空白（#30）。改用 config 顶层默认（首个有 key 模型）。
	if p.APIKey == "" {
		log.Printf("[compute] 模型 %s 的 provider %s 无 APIKey，不解析为 opencode 模型(建会话不注入)", modelID, p.Name)
		return "", nil
	}
	return slugify(p.Name) + "/" + m.Name, nil
}

// ModelName 返回 compute_model.id 对应的 model.Name（claude 工具的 ANTHROPIC_MODEL 用）。
// 未命中返 ("", nil)。
func (s *Store) ModelName(ctx context.Context, modelID string) (string, error) {
	m, err := s.getModelByID(ctx, modelID)
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", nil
	}
	return m.Name, nil
}

// getModelByID 按 id 加载单个 model（不论 enabled）。未命中返回 (nil, nil)，
// 便于上层 Resolve*/ModelName 区分「不存在」与「真实 DB 错」。
func (s *Store) getModelByID(ctx context.Context, id string) (*Model, error) {
	var m Model
	err := s.db.GetContext(ctx, &m,
		`SELECT id, provider_id, name, display_name, modality, context_window, max_output, cost_input, cost_output, capabilities, enabled, created_at
		 FROM compute_model WHERE id = $1`, id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

// slugify 简单 slug 化（去空格/特殊字符，小写，- 分隔）。
func slugify(s string) string {
	out := make([]byte, 0, len(s))
	prevDash := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			out = append(out, c+32)
			prevDash = false
		} else if c >= 'a' && c <= 'z' || c >= '0' && c <= '9' {
			out = append(out, c)
			prevDash = false
		} else if !prevDash && len(out) > 0 {
			out = append(out, '-')
			prevDash = true
		}
	}
	// 去尾 -
	if len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return "provider"
	}
	return string(out)
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
