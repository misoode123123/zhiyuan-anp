package compute

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Provider 模型供应商（智谱/OpenAI/Claude/Ollama/vLLM）。
type Provider struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Type        string    `json:"type" db:"type"` // api / local
	BaseURL     string    `json:"base_url" db:"base_url"`
	APIKey      string    `json:"api_key,omitempty" db:"api_key"`
	Enabled     bool      `json:"enabled" db:"enabled"`
	Config      *string   `json:"config,omitempty" db:"config"` // JSONB
	Description string    `json:"description,omitempty" db:"description"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
	UpdatedAt   time.Time `json:"updated_at" db:"updated_at"`
}

// Model 模型（属于某 Provider）。
type Model struct {
	ID            string    `json:"id" db:"id"`
	ProviderID    string    `json:"provider_id" db:"provider_id"`
	Name          string    `json:"name" db:"name"`
	DisplayName   string    `json:"display_name,omitempty" db:"display_name"`
	Modality      string    `json:"modality" db:"modality"` // text/vision/code
	ContextWindow int       `json:"context_window,omitempty" db:"context_window"`
	MaxOutput     int       `json:"max_output,omitempty" db:"max_output"`
	CostInput     float64   `json:"cost_input" db:"cost_input"`
	CostOutput    float64   `json:"cost_output" db:"cost_output"`
	Capabilities  *string   `json:"capabilities,omitempty" db:"capabilities"` // JSONB
	Enabled       bool      `json:"enabled" db:"enabled"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// --- Provider Store ---

func (s *Store) ListProviders(ctx context.Context) ([]Provider, error) {
	var list []Provider
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, name, type, base_url, api_key, enabled, config, description, created_at, updated_at
		 FROM compute_provider ORDER BY created_at`)
	return list, err
}

func (s *Store) GetProvider(ctx context.Context, id string) (*Provider, error) {
	var p Provider
	err := s.db.GetContext(ctx, &p,
		`SELECT id, name, type, base_url, api_key, enabled, config, description, created_at, updated_at
		 FROM compute_provider WHERE id = $1`, id)
	return &p, err
}

func (s *Store) CreateProvider(ctx context.Context, p *Provider) error {
	p.ID = "cpv_" + uuid.NewString()[:19]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO compute_provider (id, name, type, base_url, api_key, enabled, config, description)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		p.ID, p.Name, p.Type, p.BaseURL, p.APIKey, p.Enabled, p.Config, p.Description)
	return err
}

func (s *Store) UpdateProvider(ctx context.Context, p *Provider) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE compute_provider SET name=$1, type=$2, base_url=$3, api_key=$4, enabled=$5, config=$6, description=$7, updated_at=NOW()
		 WHERE id=$8`,
		p.Name, p.Type, p.BaseURL, p.APIKey, p.Enabled, p.Config, p.Description, p.ID)
	return err
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM compute_provider WHERE id=$1`, id)
	return err
}

// --- Model Store ---

func (s *Store) ListModels(ctx context.Context, providerID string) ([]Model, error) {
	var list []Model
	q := `SELECT id, provider_id, name, display_name, modality, context_window, max_output, cost_input, cost_output, capabilities, enabled, created_at
	      FROM compute_model`
	args := []interface{}{}
	if providerID != "" {
		q += ` WHERE provider_id = $1`
		args = append(args, providerID)
	}
	q += ` ORDER BY created_at`
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

func (s *Store) CreateModel(ctx context.Context, m *Model) error {
	m.ID = "cmd_" + uuid.NewString()[:19]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO compute_model (id, provider_id, name, display_name, modality, context_window, max_output, cost_input, cost_output, capabilities, enabled)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		m.ID, m.ProviderID, m.Name, m.DisplayName, m.Modality, m.ContextWindow, m.MaxOutput,
		m.CostInput, m.CostOutput, m.Capabilities, m.Enabled)
	return err
}

func (s *Store) UpdateModel(ctx context.Context, m *Model) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE compute_model SET name=$1, display_name=$2, modality=$3, context_window=$4, max_output=$5, cost_input=$6, cost_output=$7, capabilities=$8, enabled=$9
		 WHERE id=$10`,
		m.Name, m.DisplayName, m.Modality, m.ContextWindow, m.MaxOutput,
		m.CostInput, m.CostOutput, m.Capabilities, m.Enabled, m.ID)
	return err
}

func (s *Store) DeleteModel(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM compute_model WHERE id=$1`, id)
	return err
}
