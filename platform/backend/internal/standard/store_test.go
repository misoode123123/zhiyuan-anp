package standard

import (
	"context"
	"strings"
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

// createScoped 建一条分层规范（scope/module 可空）。
func createScoped(t *testing.T, s *Store, scope, module, name string, prio int, enabled bool) Standard {
	t.Helper()
	st := &Standard{
		Scope: scope, Module: module, Name: name, Category: "general",
		Content: name, Priority: prio, Enabled: enabled,
	}
	if err := s.Create(context.Background(), st); err != nil {
		t.Fatalf("create scoped: %v", err)
	}
	return *st
}

func TestListByScope_Module(t *testing.T) {
	s := newTestStore(t)
	createScoped(t, s, ScopePlatform, "", "P1", 100, true)
	createScoped(t, s, ScopeApp, "", "A1", 100, true)
	createScoped(t, s, ScopeModule, ModuleAPI, "M-api", 100, true)
	createScoped(t, s, ScopeModule, ModuleForm, "M-form", 100, true)

	got, err := s.ListByScope(context.Background(), ScopeModule, ModuleAPI)
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(got) != 1 || got[0].Name != "M-api" {
		t.Fatalf("scope=module&module=api 应只返回 M-api，得到 %+v", got)
	}
}

func TestListByScope_AllKeepsScopeOrder(t *testing.T) {
	s := newTestStore(t)
	// 故意乱序插：module → app → platform，期望按 platform>app>module 排
	createScoped(t, s, ScopeModule, ModuleAPI, "M1", 1, true)
	createScoped(t, s, ScopeApp, "", "A1", 1, true)
	createScoped(t, s, ScopePlatform, "", "P1", 1, true)

	got, err := s.ListByScope(context.Background(), "", "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("应返回 3 条，得到 %d", len(got))
	}
	if got[0].Name != "P1" || got[1].Name != "A1" || got[2].Name != "M1" {
		t.Fatalf("应按 platform>app>module 排序，得到 %s,%s,%s",
			got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestAggregate_PlusAppPlusModule(t *testing.T) {
	s := newTestStore(t)
	createScoped(t, s, ScopePlatform, "", "P1", 100, true)
	createScoped(t, s, ScopeApp, "", "A1", 100, true)
	createScoped(t, s, ScopeModule, ModuleAPI, "M-api", 100, true)
	createScoped(t, s, ScopeModule, ModuleForm, "M-form", 100, true) // 不属于 api → 不出现

	got, err := s.Aggregate(context.Background(), ModuleAPI)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("聚合(platform+app+api)应 3 条，得到 %d: %+v", len(got), got)
	}
	// scope 顺序：P1 → A1 → M-api
	if got[0].Name != "P1" || got[1].Name != "A1" || got[2].Name != "M-api" {
		t.Fatalf("聚合顺序错误，得到 %s,%s,%s", got[0].Name, got[1].Name, got[2].Name)
	}
}

func TestAggregate_EmptyModuleOmitsModuleScope(t *testing.T) {
	s := newTestStore(t)
	createScoped(t, s, ScopePlatform, "", "P1", 100, true)
	createScoped(t, s, ScopeApp, "", "A1", 100, true)
	createScoped(t, s, ScopeModule, ModuleAPI, "M-api", 100, true)

	got, err := s.Aggregate(context.Background(), "")
	if err != nil {
		t.Fatalf("Aggregate empty: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("module 为空时应只 platform+app = 2 条，得到 %d: %+v", len(got), got)
	}
}

func TestBuildAgentsMarkdown_Sections(t *testing.T) {
	list := []Standard{
		{Scope: ScopePlatform, Name: "P1", Content: "p-body"},
		{Scope: ScopeApp, Name: "A1", Content: "a-body"},
		{Scope: ScopeModule, Module: ModuleAPI, Name: "M1", Content: "m-body"},
	}
	md := BuildAgentsMarkdown(list, ModuleAPI)
	for _, want := range []string{"# AGENTS.md", "## 平台规范（Platform）", "### P1", "p-body",
		"## 应用规范（App）", "### A1", "a-body", "## 模块规范（Module: api）", "### M1", "m-body"} {
		if !strings.Contains(md, want) {
			t.Fatalf("AGENTS.md 缺少 %q, 输出:\n%s", want, md)
		}
	}
}
