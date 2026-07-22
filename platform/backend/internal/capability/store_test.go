package capability

import (
	"context"
	"strings"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newTestStore 连 anp_test PG（testutil 跑迁移建平台全表）+ 清本模块 4 表隔离。
// 替代 sqlite :memory:（sqlite 漏 PG 类型 bug，如 BOOLEAN→INTEGER 掩盖；见 memory sqlite-test-pg-type-trap）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "capability_skill", "capability_api_key", "capability_usage", "capability_domain_agent")
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
