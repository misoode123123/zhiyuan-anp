package standard

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newTestStore 连 anp_test PG（testutil 跑迁移建平台全表）+ 清 coding_standard 表隔离。
// 替代 sqlite :memory:（sqlite 漏 PG 类型 bug，见 memory sqlite-test-pg-type-trap）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "coding_standard")
	return NewStore(db)
}

func mustCreate(t *testing.T, s *Store, ps *string, name string, prio int, enabled bool) Standard {
	t.Helper()
	st := &Standard{ProjectSpaceID: ps, Name: name, Category: "general", Content: name, Priority: prio, Enabled: enabled}
	if err := s.Create(context.Background(), st); err != nil {
		t.Fatalf("create: %v", err)
	}
	return *st
}

func TestListEffective_MergesGlobalAndProject(t *testing.T) {
	s := newTestStore(t)
	ps := "ps_1"
	mustCreate(t, s, nil, "G1", 100, true)          // 全局
	mustCreate(t, s, &ps, "P1", 50, true)           // 项目级 ps_1
	mustCreate(t, s, &ps, "P2-disabled", 10, false) // 项目级但禁用 → 不出现
	other := "ps_2"
	mustCreate(t, s, &other, "P-other", 1, true) // 别的空间 → 不出现

	got, err := s.ListEffective(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("ListEffective: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应返回 2 条(全局G1+项目P1)，得到 %d: %+v", len(got), got)
	}
	// priority 升序：P1(50) 在 G1(100) 前
	if got[0].Name != "P1" || got[1].Name != "G1" {
		t.Fatalf("顺序应为 P1,G1，得到 %s,%s", got[0].Name, got[1].Name)
	}
}

func TestListEffective_GlobalOnlyWhenNoProject(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, nil, "G1", 100, true)
	got, err := s.ListEffective(context.Background(), "ps_no_proj")
	if err != nil {
		t.Fatalf("ListEffective: %v", err)
	}
	if len(got) != 1 || got[0].Name != "G1" {
		t.Fatalf("无项目级时应只返回全局，得到 %+v", got)
	}
}
