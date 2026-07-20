package capability

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 能力市场数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// ---------------- 技能 ----------------

func skillCols() string {
	return `id, project_space_id, code, name, COALESCE(description,'') AS description, category, COALESCE(prompt_template,'') AS prompt_template, version, status, risk_level, is_public, COALESCE(data_access_scope,'') AS data_access_scope, created_at, updated_at`
}

// CreateSkill 新建技能（draft）。
func (s *Store) CreateSkill(ctx context.Context, sk *Skill) error {
	sk.ID = "skl_" + uuid.NewString()[:20]
	if sk.Status == "" {
		sk.Status = "draft"
	}
	if sk.Version == "" {
		sk.Version = "0.1.0"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO capability_skill (id, project_space_id, code, name, description, category, prompt_template, version, status, risk_level, is_public, data_access_scope)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		sk.ID, sk.ProjectSpaceID, sk.Code, sk.Name, sk.Description, sk.Category,
		sk.PromptTemplate, sk.Version, sk.Status, sk.RiskLevel, sk.IsPublic, sk.DataAccessScope)
	return err
}

// ListSkills 技能列表（status 可选；publicOnly 仅上架）。
func (s *Store) ListSkills(ctx context.Context, psID, status string, publicOnly bool) ([]Skill, error) {
	q := `SELECT ` + skillCols() + ` FROM capability_skill`
	var args []interface{}
	where := []string{}
	if psID != "" {
		where = append(where, `project_space_id = ?`)
		args = append(args, psID)
	}
	if status != "" {
		where = append(where, `status = ?`)
		args = append(args, status)
	}
	if publicOnly {
		where = append(where, `status = 'active'`)
	}
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	q += ` ORDER BY created_at DESC`
	q = sqlx.Rebind(sqlx.DOLLAR, q) // 动态拼 WHERE，?→$N（PG 兼容）
	var list []Skill
	err := s.db.SelectContext(ctx, &list, q, args...)
	return list, err
}

// GetSkill 取单条（任一空间）。
func (s *Store) GetSkill(ctx context.Context, id string) (*Skill, error) {
	var sk Skill
	err := s.db.GetContext(ctx, &sk, `SELECT `+skillCols()+` FROM capability_skill WHERE id=$1`, id)
	return &sk, err
}

// GetSkillByCode 按 code 取（invoke 路由用）。
func (s *Store) GetSkillByCode(ctx context.Context, code string) (*Skill, error) {
	var sk Skill
	err := s.db.GetContext(ctx, &sk, `SELECT `+skillCols()+` FROM capability_skill WHERE code=$1 AND status='active'`, code)
	return &sk, err
}

// UpdateSkill 更新技能。
func (s *Store) UpdateSkill(ctx context.Context, sk *Skill) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE capability_skill SET code=$1, name=$2, description=$3, category=$4, prompt_template=$5, version=$6, risk_level=$7, is_public=$8, data_access_scope=$9, updated_at=CURRENT_TIMESTAMP
		 WHERE id=$10`,
		sk.Code, sk.Name, sk.Description, sk.Category, sk.PromptTemplate, sk.Version, sk.RiskLevel, sk.IsPublic, sk.DataAccessScope, sk.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("技能 %s 不存在", sk.ID)
	}
	return nil
}

// SetSkillStatus 切换技能生命周期状态（submit→pending_review / approve→active / offline）。
func (s *Store) SetSkillStatus(ctx context.Context, id, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE capability_skill SET status=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("技能 %s 不存在", id)
	}
	return nil
}

// DeleteSkill 删除技能。
func (s *Store) DeleteSkill(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM capability_skill WHERE id=$1`, id)
	return err
}

// ---------------- APIKey ----------------

// generateKey 生成 sk_anp_<32hex> 明文 + 其 SHA256 哈希 + 展示前缀。
func generateKey() (plain, hash, prefix string, err error) {
	b := make([]byte, 16)
	if _, err = rand.Read(b); err != nil {
		return
	}
	plain = "sk_anp_" + hex.EncodeToString(b)
	sum := sha256.Sum256([]byte(plain))
	hash = hex.EncodeToString(sum[:])
	prefix = plain[:14] + "…" // sk_anp_xxxxxx…
	return
}

// CreateAPIKey 新建 APIKey（返回时 KeyHash/KeyPrefix 已填，明文由调用方返回给用户一次）。
func (s *Store) CreateAPIKey(ctx context.Context, k *APIKey) (plain string, err error) {
	plain, k.KeyHash, k.KeyPrefix, err = generateKey()
	if err != nil {
		return
	}
	k.ID = "key_" + uuid.NewString()[:20]
	if k.Status == "" {
		k.Status = "active"
	}
	if k.Scope == "" {
		k.Scope = "write"
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO capability_api_key (id, project_space_id, app_name, key_hash, key_prefix, allowed_skills, scope, status, expires_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		k.ID, k.ProjectSpaceID, k.AppName, k.KeyHash, k.KeyPrefix, k.AllowedSkills, k.Scope, k.Status, k.ExpiresAt)
	return plain, err
}

// ListAPIKeys APIKey 列表（脱敏，不含 hash）。
func (s *Store) ListAPIKeys(ctx context.Context, psID string) ([]APIKey, error) {
	var list []APIKey
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, app_name, key_prefix, allowed_skills, scope, status, expires_at, created_at
		 FROM capability_api_key WHERE project_space_id=$1 ORDER BY created_at DESC`, psID)
	return list, err
}

// LookupAPIKey 按明文 key 查询（invoke 鉴权用）。
func (s *Store) LookupAPIKey(ctx context.Context, plain string) (*APIKey, error) {
	sum := sha256.Sum256([]byte(plain))
	hash := hex.EncodeToString(sum[:])
	var k APIKey
	err := s.db.GetContext(ctx, &k,
		`SELECT id, project_space_id, app_name, key_hash, key_prefix, allowed_skills, scope, status, expires_at, created_at
		 FROM capability_api_key WHERE key_hash=$1 AND status='active'`, hash)
	if err != nil {
		return nil, err
	}
	return &k, nil
}

// RevokeAPIKey 吊销。
func (s *Store) RevokeAPIKey(ctx context.Context, psID, id string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE capability_api_key SET status='revoked' WHERE id=$1 AND project_space_id=$2`, id, psID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("APIKey %s 不存在", id)
	}
	return nil
}

// ---------------- 用量 ----------------

// RecordUsage 记录一次调用用量。
func (s *Store) RecordUsage(ctx context.Context, u *CapabilityUsage) error {
	u.ID = "usg_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO capability_usage (id, project_space_id, api_key_id, caller_app, skill_id, input_tokens, output_tokens, success, latency_ms, render_hint, trace_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		u.ID, u.ProjectSpaceID, u.APIKeyID, u.CallerApp, u.SkillID, u.InputTokens, u.OutputTokens, u.Success, u.LatencyMS, u.RenderHint, u.TraceID)
	return err
}

// UsageBySkill 按技能聚合用量。
func (s *Store) UsageBySkill(ctx context.Context, psID string) ([]SkillUsageStat, error) {
	var list []SkillUsageStat
	err := s.db.SelectContext(ctx, &list,
		`SELECT skill_id, COUNT(*) calls, COALESCE(SUM(input_tokens),0) input_tokens, COALESCE(SUM(output_tokens),0) output_tokens,
		        SUM(CASE WHEN success THEN 1 ELSE 0 END) success_count
		 FROM capability_usage WHERE project_space_id=$1 GROUP BY skill_id ORDER BY calls DESC`, psID)
	return list, err
}

// UsageList 最近用量明细。
func (s *Store) UsageList(ctx context.Context, psID string) ([]CapabilityUsage, error) {
	var list []CapabilityUsage
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, api_key_id, caller_app, skill_id, input_tokens, output_tokens, success, latency_ms, render_hint, trace_id, created_at
		 FROM capability_usage WHERE project_space_id=$1 ORDER BY created_at DESC LIMIT 200`, psID)
	return list, err
}

// ---------------- 领域 Agent ----------------

// ListDomainAgents 领域 Agent 列表。
func (s *Store) ListDomainAgents(ctx context.Context, psID string) ([]DomainAgent, error) {
	var list []DomainAgent
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, project_space_id, code, name, domain, composed_skills, status, created_at, updated_at
		 FROM capability_domain_agent WHERE project_space_id=$1 ORDER BY created_at DESC`, psID)
	return list, err
}

// CreateDomainAgent 新建领域 Agent。
func (s *Store) CreateDomainAgent(ctx context.Context, d *DomainAgent) error {
	d.ID = "dag_" + uuid.NewString()[:20]
	if d.Status == "" {
		d.Status = "draft"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO capability_domain_agent (id, project_space_id, code, name, domain, composed_skills, status)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		d.ID, d.ProjectSpaceID, d.Code, d.Name, d.Domain, d.ComposedSkills, d.Status)
	return err
}

// DeleteDomainAgent 删除。
func (s *Store) DeleteDomainAgent(ctx context.Context, psID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM capability_domain_agent WHERE id=$1 AND project_space_id=$2`, id, psID)
	return err
}
