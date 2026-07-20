package config

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + 仅 system_config 表（自包含，仿 change/store_test.go 模式）。
// DDL 来自 internal/db/migrations/pg/000001_init.up.sql，按方言映射 TIMESTAMP→DATETIME。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE system_config (
  key         TEXT PRIMARY KEY,
  value       TEXT,
  category    TEXT NOT NULL DEFAULT 'general',
  description TEXT,
  updated_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewStore(db)
}

// TestSetAndGet 往返一致：未 Set 的 key 返回 fallback；Set 后直写缓存，Get 立即命中。
func TestSetAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 未命中返回 fallback
	if got := s.Get("missing", "fb"); got != "fb" {
		t.Fatalf("未 Set 的 key 应返回 fallback，得到 %q", got)
	}

	if err := s.Set(ctx, "k1", "v1", "cat_a", "desc1"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// Set 后无需 Load，缓存即生效（热生效语义）
	if got := s.Get("k1", "fb"); got != "v1" {
		t.Fatalf("Set 后 Get 应返回写入值，得到 %q", got)
	}
}

// TestSet_Overwrite 同 key 再次 Set 应覆盖 value/category/description（ON CONFLICT 路径）。
func TestSet_Overwrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "k", "v1", "cat_a", "d1"); err != nil {
		t.Fatalf("first set: %v", err)
	}
	if err := s.Set(ctx, "k", "v2", "cat_b", "d2"); err != nil {
		t.Fatalf("overwrite set: %v", err)
	}
	if got := s.Get("k", ""); got != "v2" {
		t.Fatalf("覆盖后 Get 应返回新值，得到 %q", got)
	}
	all := s.All()
	if len(all) != 1 {
		t.Fatalf("覆盖后仍应只有 1 条（upsert 不新增），得到 %d", len(all))
	}
	if all[0].Category != "cat_b" || all[0].Description != "d2" {
		t.Fatalf("覆盖未更新 category/description: %+v", all[0])
	}
}

// TestLoad 绕过缓存直接写 DB 后，必须 Load 才能让 Get 命中。
func TestLoad(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 直接写 DB（绕过缓存）
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO system_config (key, value, category, description) VALUES ($1,$2,$3,$4)`,
		"ext", "extv", "general", ""); err != nil {
		t.Fatalf("seed insert: %v", err)
	}
	if got := s.Get("ext", "fb"); got != "fb" {
		t.Fatalf("Load 前缓存未命中应返回 fallback，得到 %q", got)
	}
	if err := s.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := s.Get("ext", "fb"); got != "extv" {
		t.Fatalf("Load 后应返回 DB 值，得到 %q", got)
	}
}

// TestLoad_ClearsStale Load 应整体重建缓存，已删除的 DB 行不应残留于缓存。
// 覆盖“删除后读不到”这一要求（store 本身无 Delete 方法，故在 DB 层模拟删除）。
func TestLoad_ClearsStale(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "tmp", "v", "general", ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	// DB 层删除（store 无 Delete API）
	if _, err := s.db.ExecContext(ctx, `DELETE FROM system_config WHERE key = $1`, "tmp"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.Load(ctx); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := s.Get("tmp", "fb"); got != "fb" {
		t.Fatalf("Load 后已删除项不应留在缓存，得到 %q", got)
	}
}

// TestAll_Ordering All 按 category, key 排序返回（多 category 交叉验证）。
func TestAll_Ordering(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "b_key", "1", "cat_b", ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.Set(ctx, "a_key2", "1", "cat_a", ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := s.Set(ctx, "a_key1", "1", "cat_a", ""); err != nil {
		t.Fatalf("set: %v", err)
	}

	all := s.All()
	if len(all) != 3 {
		t.Fatalf("应返回 3 条，得到 %d", len(all))
	}
	want := []string{"a_key1", "a_key2", "b_key"} // 先按 category，再按 key
	for i, w := range want {
		if all[i].Key != w {
			t.Fatalf("第 %d 条应为 %s，得到 %s（全量 %+v）", i, w, all[i].Key, all)
		}
	}
}

// TestAll_Empty 空表 All 返回 0 条（不 panic）。
func TestAll_Empty(t *testing.T) {
	s := newTestStore(t)
	if got := len(s.All()); got != 0 {
		t.Fatalf("空表 All 应返回 0 条，得到 %d", got)
	}
}

// TestSeedIfEmpty_Seed 空表时按 defaults 播种，播种后缓存可读。
func TestSeedIfEmpty_Seed(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	defaults := map[string][2]string{
		"model":   {"glm-5.1", "ai"},
		"timeout": {"30", "system"},
	}
	if err := s.SeedIfEmpty(ctx, defaults); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := s.Get("model", ""); got != "glm-5.1" {
		t.Fatalf("播种后应读到默认值 model，得到 %q", got)
	}
	if got := s.Get("timeout", ""); got != "30" {
		t.Fatalf("播种后应读到默认值 timeout，得到 %q", got)
	}
	// DB 实际落库 2 条
	if got := len(s.All()); got != 2 {
		t.Fatalf("DB 应落库 2 条，得到 %d", got)
	}
}

// TestSeedIfEmpty_NonEmptySkip 非空表不播种，仅 Load 已有项。
func TestSeedIfEmpty_NonEmptySkip(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "existed", "keep", "general", ""); err != nil {
		t.Fatalf("set: %v", err)
	}
	defaults := map[string][2]string{
		"model": {"glm-5.1", "ai"},
	}
	if err := s.SeedIfEmpty(ctx, defaults); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if got := s.Get("model", "fb"); got != "fb" {
		t.Fatalf("非空表不应播种新 key，得到 %q", got)
	}
	if got := s.Get("existed", ""); got != "keep" {
		t.Fatalf("已有项应保留，得到 %q", got)
	}
}
