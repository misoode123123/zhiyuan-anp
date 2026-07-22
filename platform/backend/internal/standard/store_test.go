package standard

import (
	"context"
	"os"
	"path/filepath"
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

// createScopedPS 建一条带 psID 的分层规范（scope/module/ps 可空）。
func createScopedPS(t *testing.T, s *Store, scope, module, name string, ps *string, prio int) Standard {
	t.Helper()
	st := &Standard{
		Scope: scope, Module: module, ProjectSpaceID: ps,
		Name: name, Category: "general", Content: name, Priority: prio, Enabled: true,
	}
	if err := s.Create(context.Background(), st); err != nil {
		t.Fatalf("create scoped ps: %v", err)
	}
	return *st
}

func TestAggregateFor_FiltersByProjectSpace(t *testing.T) {
	s := newTestStore(t)
	ps1 := "ps_1"
	ps2 := "ps_2"
	// 全局 platform
	createScopedPS(t, s, ScopePlatform, "", "G-plat", nil, 100)
	// 全局 module api
	createScopedPS(t, s, ScopeModule, ModuleAPI, "G-api", nil, 100)
	// ps1 项目级 platform
	createScopedPS(t, s, ScopePlatform, "", "P1-plat", &ps1, 50)
	// ps1 项目级 module form
	createScopedPS(t, s, ScopeModule, ModuleForm, "P1-form", &ps1, 50)
	// ps2 项目级（不该出现）
	createScopedPS(t, s, ScopePlatform, "", "P2-plat", &ps2, 10)

	got, err := s.AggregateFor(context.Background(), ps1, "")
	if err != nil {
		t.Fatalf("AggregateFor: %v", err)
	}
	// 期望：G-plat, P1-plat (platform) + G-api (module api) + P1-form (module form)
	// 不含 P2-plat
	names := namesOf(got)
	if len(got) != 4 {
		t.Fatalf("应返回 4 条(全局plat+ps1plat+全局api+ps1form)，得到 %d: %+v", len(got), got)
	}
	for _, n := range []string{"G-plat", "P1-plat", "G-api", "P1-form"} {
		if !contains(names, n) {
			t.Fatalf("期望包含 %s，得到 %v", n, names)
		}
	}
	if contains(names, "P2-plat") {
		t.Fatalf("不应包含其他项目空间规范 P2-plat，得到 %v", names)
	}
}

func TestAggregateFor_ModuleNonEmptyOnlyThatModule(t *testing.T) {
	s := newTestStore(t)
	createScopedPS(t, s, ScopePlatform, "", "P1", nil, 100)
	createScopedPS(t, s, ScopeApp, "", "A1", nil, 100)
	createScopedPS(t, s, ScopeModule, ModuleAPI, "M-api", nil, 100)
	createScopedPS(t, s, ScopeModule, ModuleForm, "M-form", nil, 100)

	got, err := s.AggregateFor(context.Background(), "", ModuleAPI)
	if err != nil {
		t.Fatalf("AggregateFor: %v", err)
	}
	names := namesOf(got)
	if len(got) != 3 {
		t.Fatalf("module=api 应只 platform+app+api=3 条，得到 %d: %+v", len(got), got)
	}
	if !contains(names, "M-api") || contains(names, "M-form") {
		t.Fatalf("module=api 应含 M-api 不含 M-form，得到 %v", names)
	}
}

func TestRefreshAgentsMD_WritesAggregateMarkdown(t *testing.T) {
	s := newTestStore(t)
	ps1 := "ps_1"
	createScopedPS(t, s, ScopePlatform, "", "G-plat", nil, 100)
	createScopedPS(t, s, ScopeModule, ModuleAPI, "G-api", nil, 100)
	createScopedPS(t, s, ScopePlatform, "", "P1-plat", &ps1, 50)

	repoDir := t.TempDir()
	if err := s.RefreshAgentsMD(context.Background(), repoDir, ps1, ""); err != nil {
		t.Fatalf("RefreshAgentsMD: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repoDir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("读回 AGENTS.md: %v", err)
	}
	md := string(data)
	for _, want := range []string{"# AGENTS.md", "### G-plat", "### P1-plat", "### G-api"} {
		if !strings.Contains(md, want) {
			t.Fatalf("AGENTS.md 缺少 %q, 输出:\n%s", want, md)
		}
	}
}

func TestRefreshAgentsMD_CreatesRepoDirIfMissing(t *testing.T) {
	s := newTestStore(t)
	createScopedPS(t, s, ScopePlatform, "", "P1", nil, 100)

	repoDir := filepath.Join(t.TempDir(), "nested", "repo")
	if err := s.RefreshAgentsMD(context.Background(), repoDir, "", ""); err != nil {
		t.Fatalf("RefreshAgentsMD 嵌套目录应自动创建: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repoDir, "AGENTS.md")); err != nil {
		t.Fatalf("AGENTS.md 未写入: %v", err)
	}
}

// namesOf 收集规范名（断言用）。
func namesOf(list []Standard) []string {
	out := make([]string, 0, len(list))
	for _, s := range list {
		out = append(out, s.Name)
	}
	return out
}

// contains 字符串切片包含判定（断言用）。
func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
