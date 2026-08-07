package compute

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// OpenCodeConfig opencode.json 的 Go 表示。
type OpenCodeConfig struct {
	Schema   string                       `json:"$schema"`
	Provider map[string]*OpenCodeProvider `json:"provider"`
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
