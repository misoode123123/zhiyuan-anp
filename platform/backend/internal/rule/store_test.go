package rule

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + 仅 rule 表（自包含，仿 change/store_test.go 模式）。
// 类型映射对齐 pg 迁移：TIMESTAMP→DATETIME、BOOLEAN→INTEGER。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE rule (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL,
  category        TEXT NOT NULL DEFAULT 'general',
  type            TEXT NOT NULL DEFAULT 'mandatory',
  condition       TEXT NOT NULL,
  condition_field TEXT NOT NULL DEFAULT 'prompt',
  action          TEXT NOT NULL DEFAULT 'block',
  scope           TEXT NOT NULL DEFAULT 'all',
  enabled         INTEGER NOT NULL DEFAULT 1,
  description     TEXT,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewStore(db)
}

// mustCreate 包装 Create，失败即 t.Fatal；返回创建后的 Rule（含生成的 ID）。
func mustCreate(t *testing.T, s *Store, r *Rule) *Rule {
	t.Helper()
	if err := s.Create(context.Background(), r); err != nil {
		t.Fatalf("create: %v", err)
	}
	return r
}

// newRule 构造一条最小可用的 Rule，便于测试微调字段。
func newRule(name, cond, field, action, scope string, enabled bool) *Rule {
	return &Rule{
		Name: name, Category: "general", Type: "mandatory",
		Condition: cond, ConditionField: field, Action: action,
		Scope: scope, Enabled: enabled,
	}
}

// TestCreate_AssignsIDPrefix Create 后 ID 应以 "rule_" 前缀填充（store 内部用 uuid 截断）。
func TestCreate_AssignsIDPrefix(t *testing.T) {
	s := newTestStore(t)
	r := mustCreate(t, s, newRule("R1", "foo", "prompt", "block", "all", true))
	if len(r.ID) < 5 || r.ID[:5] != "rule_" {
		t.Fatalf("ID 应以 'rule_' 开头，得到 %q", r.ID)
	}
}

// TestList_EmptyAndPopulated 空表返回空切片；写入后能读回且字段完整。
func TestList_EmptyAndPopulated(t *testing.T) {
	s := newTestStore(t)

	got, err := s.List(context.Background())
	if err != nil {
		t.Fatalf("List 空表: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空表 List 应返回 0 条，得到 %d", len(got))
	}

	mustCreate(t, s, newRule("B-rule", "foo", "prompt", "block", "all", true))
	mustCreate(t, s, newRule("A-rule", "bar", "output", "warn", "dev", true))

	got, err = s.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应返回 2 条，得到 %d", len(got))
	}
	// 排序：ORDER BY category, name —— category 相同则按 name 升序
	if got[0].Name != "A-rule" || got[1].Name != "B-rule" {
		t.Fatalf("排序错误：期望 A-rule,B-rule，得到 %s,%s", got[0].Name, got[1].Name)
	}
	// 字段穿透校验
	if got[1].Condition != "foo" || got[1].Action != "block" || !got[1].Enabled {
		t.Fatalf("字段读回不匹配： %+v", got[1])
	}
}

// TestListEnabled_FiltersByScopeAndEnabled ListEnabled 必须同时满足：
// ① enabled=true；② scope='all' 或 scope=入参。
func TestListEnabled_FiltersByScopeAndEnabled(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, newRule("G", "g", "prompt", "block", "all", true))    // 全局启用 → 命中
	mustCreate(t, s, newRule("D", "d", "prompt", "block", "dev", true))    // dev 启用 → 命中
	mustCreate(t, s, newRule("R", "r", "prompt", "block", "req", true))    // 别的 scope → 不命中
	mustCreate(t, s, newRule("X", "x", "prompt", "block", "all", false))   // 全局但禁用 → 不命中
	mustCreate(t, s, newRule("XD", "xd", "prompt", "block", "dev", false)) // dev 但禁用 → 不命中

	got, err := s.ListEnabled(context.Background(), "dev")
	if err != nil {
		t.Fatalf("ListEnabled: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("dev scope 应返回 2 条(G+D)，得到 %d: %+v", len(got), got)
	}
	// ListEnabled 不保证顺序（SQL 无 ORDER BY），按名字集合校验
	names := map[string]bool{got[0].Name: true, got[1].Name: true}
	if !names["G"] || !names["D"] {
		t.Fatalf("应包含 G 和 D，得到 %+v", got)
	}
}

// TestListEnabled_NoMatchForOtherScope 入参 scope 在表中不存在时，只返回 scope='all' 的规则。
func TestListEnabled_NoMatchForOtherScope(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, newRule("G", "g", "prompt", "block", "all", true))
	mustCreate(t, s, newRule("D", "d", "prompt", "block", "dev", true))

	got, _ := s.ListEnabled(context.Background(), "nonexistent")
	if len(got) != 1 || got[0].Name != "G" {
		t.Fatalf("无 scope 匹配时只应返回 all 规则，得到 %+v", got)
	}
}

// TestUpdate_SuccessAndNotFound Update 成功改字段；不存在的 ID 返回错误。
func TestUpdate_SuccessAndNotFound(t *testing.T) {
	s := newTestStore(t)
	r := mustCreate(t, s, newRule("Orig", "foo", "prompt", "block", "all", true))

	r.Name = "Updated"
	r.Condition = "bar"
	r.Action = "warn"
	r.Enabled = false
	if err := s.Update(context.Background(), r); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := s.List(context.Background())
	if len(got) != 1 {
		t.Fatalf("List 应返回 1 条，得到 %d", len(got))
	}
	if got[0].Name != "Updated" || got[0].Condition != "bar" || got[0].Action != "warn" || got[0].Enabled {
		t.Fatalf("更新未生效： %+v", got[0])
	}

	// 不存在的 ID → 报错
	if err := s.Update(context.Background(), &Rule{ID: "rule_nope", Name: "x", Condition: "y"}); err == nil {
		t.Fatal("Update 不存在的 ID 应报错")
	}
}

// TestSetEnabled_Toggle SetEnabled 切换 enabled 状态，后续 List 能观察到。
func TestSetEnabled_Toggle(t *testing.T) {
	s := newTestStore(t)
	r := mustCreate(t, s, newRule("R", "foo", "prompt", "block", "all", true))

	// 关闭
	if err := s.SetEnabled(context.Background(), r.ID, false); err != nil {
		t.Fatalf("SetEnabled false: %v", err)
	}
	got, _ := s.List(context.Background())
	if got[0].Enabled {
		t.Fatal("SetEnabled(false) 后 Enabled 应为 false")
	}
	// 关闭后被 ListEnabled 排除
	enabled, _ := s.ListEnabled(context.Background(), "all")
	if len(enabled) != 0 {
		t.Fatalf("禁用后 ListEnabled 应为空，得到 %d 条", len(enabled))
	}
	// 重新开启
	if err := s.SetEnabled(context.Background(), r.ID, true); err != nil {
		t.Fatalf("SetEnabled true: %v", err)
	}
	got, _ = s.List(context.Background())
	if !got[0].Enabled {
		t.Fatal("SetEnabled(true) 后 Enabled 应为 true")
	}
}

// TestDelete_RemoveAndIdempotent Delete 后 List 看不到；再次删除不报错（受影响 0 行不视作错误）。
func TestDelete_RemoveAndIdempotent(t *testing.T) {
	s := newTestStore(t)
	r := mustCreate(t, s, newRule("R", "foo", "prompt", "block", "all", true))

	if err := s.Delete(context.Background(), r.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, _ := s.List(context.Background())
	if len(got) != 0 {
		t.Fatalf("删除后 List 应为空，得到 %d 条", len(got))
	}
	// 再删一次（ID 已不存在）不应报错 —— 当前实现未做 RowsAffected 校验
	if err := s.Delete(context.Background(), r.ID); err != nil {
		t.Fatalf("重复删除不应报错，得到 %v", err)
	}
}

// TestSeedDemoRules_SeedsWhenEmpty 空表播种 3 条演示规则，覆盖 block/warn/require_approval 三类 action。
func TestSeedDemoRules_SeedsWhenEmpty(t *testing.T) {
	s := newTestStore(t)
	if err := s.SeedDemoRules(context.Background()); err != nil {
		t.Fatalf("SeedDemoRules 首次: %v", err)
	}
	got, _ := s.List(context.Background())
	if len(got) != 3 {
		t.Fatalf("应播种 3 条演示规则，得到 %d", len(got))
	}
	// 校验三类 action 都存在
	actions := map[string]bool{}
	for _, r := range got {
		actions[r.Action] = true
	}
	for _, want := range []string{"block", "warn", "require_approval"} {
		if !actions[want] {
			t.Errorf("演示规则缺少 action=%s", want)
		}
	}
}

// TestSeedDemoRules_NoopWhenPopulated 已有数据时再次调用应是无操作。
func TestSeedDemoRules_NoopWhenPopulated(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, newRule("X", "foo", "prompt", "block", "all", true))

	if err := s.SeedDemoRules(context.Background()); err != nil {
		t.Fatalf("SeedDemoRules 非空表: %v", err)
	}
	got, _ := s.List(context.Background())
	if len(got) != 1 {
		t.Fatalf("非空表 SeedDemoRules 应是 no-op，仍应只有 1 条，得到 %d", len(got))
	}
}
