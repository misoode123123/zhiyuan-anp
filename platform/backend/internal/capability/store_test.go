package capability

import (
	"context"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`
CREATE TABLE capability_skill (
	id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, code TEXT NOT NULL, name TEXT NOT NULL,
	description TEXT, category TEXT NOT NULL DEFAULT 'assistant', prompt_template TEXT,
	version TEXT NOT NULL DEFAULT '0.1.0', status TEXT NOT NULL DEFAULT 'draft', risk_level TEXT NOT NULL DEFAULT 'low',
	is_public INTEGER NOT NULL DEFAULT 0, data_access_scope TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE (project_space_id, code));
CREATE TABLE capability_api_key (
	id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, app_name TEXT NOT NULL, key_hash TEXT NOT NULL,
	key_prefix TEXT NOT NULL, allowed_skills TEXT, scope TEXT NOT NULL DEFAULT 'write', status TEXT NOT NULL DEFAULT 'active',
	expires_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE capability_usage (
	id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, api_key_id TEXT, caller_app TEXT, skill_id TEXT,
	input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0, success INTEGER NOT NULL DEFAULT 0,
	latency_ms INTEGER NOT NULL DEFAULT 0, render_hint TEXT, trace_id TEXT,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE capability_domain_agent (
	id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, code TEXT NOT NULL, name TEXT NOT NULL,
	domain TEXT NOT NULL DEFAULT 'custom', composed_skills TEXT, status TEXT NOT NULL DEFAULT 'draft',
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	UNIQUE (project_space_id, code));`)
	return NewStore(db)
}

func TestAPIKey_CreateLookupRevoke(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	k := &APIKey{ProjectSpaceID: "ps1", AppName: "财务系统"}
	plain, err := s.CreateAPIKey(ctx, k)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(plain, "sk_anp_") {
		t.Fatalf("key 应 sk_anp_ 前缀，得到 %s", plain)
	}
	if k.KeyHash == "" || k.ID == "" {
		t.Fatal("应回填 hash 与 id")
	}
	// 明文不应等于 hash（哈希不可逆）
	if plain == k.KeyHash {
		t.Fatal("明文不应等于哈希")
	}
	// lookup 成功
	got, err := s.LookupAPIKey(ctx, plain)
	if err != nil || got == nil || got.AppName != "财务系统" {
		t.Fatalf("lookup 失败: %v %+v", err, got)
	}
	// 错误 key 查不到
	if _, err := s.LookupAPIKey(ctx, "sk_anp_wrong"); err == nil {
		t.Fatal("错误 key 应查不到")
	}
	// 吊销后查不到
	if err := s.RevokeAPIKey(ctx, "ps1", k.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, err := s.LookupAPIKey(ctx, plain); err == nil {
		t.Fatal("吊销后应查不到")
	}
}

func TestSkill_LifecycleAndActiveOnly(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sk := &Skill{ProjectSpaceID: "ps1", Code: "data-qa", Name: "数据问答", Category: "data_qa", Status: "draft"}
	if err := s.CreateSkill(ctx, sk); err != nil {
		t.Fatalf("create: %v", err)
	}
	// draft 状态按 code 查不到（只查 active）
	if _, err := s.GetSkillByCode(ctx, "data-qa"); err == nil {
		t.Fatal("draft 状态不应可被 invoke 查到")
	}
	if err := s.SetSkillStatus(ctx, sk.ID, "active"); err != nil {
		t.Fatalf("activate: %v", err)
	}
	got, err := s.GetSkillByCode(ctx, "data-qa")
	if err != nil || got == nil || got.Status != "active" {
		t.Fatalf("active 后应可查: %v %+v", err, got)
	}
	// 下线后 invoke 不可用
	_ = s.SetSkillStatus(ctx, sk.ID, "offline")
	if _, err := s.GetSkillByCode(ctx, "data-qa"); err == nil {
		t.Fatal("offline 后不应可被 invoke 查到")
	}
}

func TestUsage_Aggregation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = s.RecordUsage(ctx, &CapabilityUsage{
			ProjectSpaceID: "ps1", APIKeyID: "k1", CallerApp: "app", SkillID: "skl_1",
			InputTokens: 100, OutputTokens: 50, Success: i < 2, LatencyMS: 200,
		})
	}
	stats, err := s.UsageBySkill(ctx, "ps1")
	if err != nil {
		t.Fatalf("by-skill: %v", err)
	}
	if len(stats) != 1 || stats[0].Calls != 3 || stats[0].InputTokens != 300 || stats[0].SuccessCount != 2 {
		t.Fatalf("聚合错误: %+v", stats)
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" data-qa , doc-gen ,, ")
	if len(got) != 2 || got[0] != "data-qa" || got[1] != "doc-gen" {
		t.Fatalf("splitCSV 错误: %+v", got)
	}
}
