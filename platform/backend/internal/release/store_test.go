package release

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + 仅 release_record 表（自包含，仿 change/store_test.go 模式）。
// schema 字段对齐 internal/db/migrations/pg/000001_init.up.sql 的 release_record，
// 仅做 PG→SQLite 类型映射：TIMESTAMP→DATETIME，其余 TEXT 保持原样。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE release_record (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  change_id        TEXT,
  application_id   TEXT,
  version          TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'released',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	// List JOIN 依赖:release→change→app(双路径)
	db.MustExec(`CREATE TABLE change_request (id TEXT PRIMARY KEY, source_id TEXT, reviewer TEXT, prompt TEXT, output TEXT)`)
	db.MustExec(`CREATE TABLE requirement (id TEXT PRIMARY KEY, application_id TEXT)`)
	db.MustExec(`CREATE TABLE appdeploy_application (id TEXT PRIMARY KEY, name TEXT)`)
	return NewStore(db)
}

// mkRelease 构造一条发布记录入参（状态默认 released，仿生产路径）。
func mkRelease(ps, chg, ver string) *Release {
	return &Release{ProjectSpaceID: ps, ChangeID: chg, Version: ver, Status: "released"}
}

// TestCreate_PopulatesIDAndPersists 新建后 ID 应以 "rel_" 前缀填充、字段被持久化。
func TestCreate_PopulatesIDAndPersists(t *testing.T) {
	s := newTestStore(t)
	r := mkRelease("ps_1", "chg_a", "v1")
	if err := s.Create(context.Background(), r); err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(r.ID, "rel_") {
		t.Fatalf("create 后 ID 应以 rel_ 前缀填充，得到 %q", r.ID)
	}
	// 读回校验：List 应能查到刚写入的记录且字段一致。
	list, err := s.List(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应返回 1 条，得到 %d", len(list))
	}
	got := list[0]
	if got.ID != r.ID {
		t.Fatalf("ID 不匹配：入库 %s / 读回 %s", r.ID, got.ID)
	}
	if got.ChangeID != "chg_a" || got.Version != "v1" || got.Status != "released" {
		t.Fatalf("字段未正确持久化： %+v", got)
	}
	// created_at 由 DB 默认值生成，应非零。
	if got.CreatedAt.IsZero() {
		t.Fatal("created_at 应由 DB 默认值填充")
	}
}

// TestList_FilterByProjectSpace 多空间共存时只返回当前空间的记录。
func TestList_FilterByProjectSpace(t *testing.T) {
	s := newTestStore(t)
	_ = s.Create(context.Background(), mkRelease("ps_1", "chg_a", "v1"))
	_ = s.Create(context.Background(), mkRelease("ps_1", "chg_b", "v2"))
	_ = s.Create(context.Background(), mkRelease("ps_2", "chg_c", "v1"))

	got, err := s.List(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list ps_1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ps_1 应有 2 条，得到 %d", len(got))
	}
	for _, r := range got {
		if r.ProjectSpaceID != "ps_1" {
			t.Fatalf("过滤泄漏：在 ps_1 结果中看到 %s", r.ProjectSpaceID)
		}
	}
}

// TestList_EmptyForUnknownPS 边界：未发布过的空间返回空切片、无错误。
func TestList_EmptyForUnknownPS(t *testing.T) {
	s := newTestStore(t)
	got, err := s.List(context.Background(), "ps_ghost")
	if err != nil {
		t.Fatalf("list 空 space 不应报错: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("未知空间应返回 0 条，得到 %d", len(got))
	}
}

// TestCount_VersionIncrementFlow 模拟 handler.Create 的版本号自增逻辑：
// 发布前 Count → 写入 "v{n+1}" → 下一轮 Count 自增。覆盖核心读写联动。
func TestCount_VersionIncrementFlow(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	for i := 1; i <= 3; i++ {
		n, err := s.Count(ctx, "ps_1")
		if err != nil {
			t.Fatalf("count #%d: %v", i, err)
		}
		if n != i-1 {
			t.Fatalf("第 %d 轮 Count 应为 %d，得到 %d", i, i-1, n)
		}
		r := mkRelease("ps_1", fmt.Sprintf("chg_%d", i), fmt.Sprintf("v%d", n+1))
		if err := s.Create(ctx, r); err != nil {
			t.Fatalf("create #%d: %v", i, err)
		}
	}
	// 最终落库 3 条，且版本号依次为 v1/v2/v3。
	list, _ := s.List(ctx, "ps_1")
	if len(list) != 3 {
		t.Fatalf("最终应有 3 条，得到 %d", len(list))
	}
	versions := map[string]bool{}
	for _, r := range list {
		versions[r.Version] = true
	}
	for _, want := range []string{"v1", "v2", "v3"} {
		if !versions[want] {
			t.Fatalf("缺少版本 %s，实际集合 %+v", want, versions)
		}
	}
}

// TestCount_ZeroForUnknownPS 边界：未发布过的空间 Count 返回 0（用于首次版本号 v1 自增）。
func TestCount_ZeroForUnknownPS(t *testing.T) {
	s := newTestStore(t)
	n, err := s.Count(context.Background(), "ps_ghost")
	if err != nil {
		t.Fatalf("count 未知空间不应报错: %v", err)
	}
	if n != 0 {
		t.Fatalf("未知空间 Count 应为 0，得到 %d", n)
	}
}

// TestCount_IsolatesByProjectSpace 边界：相邻空间的发布不应干扰当前空间的计数。
func TestCount_IsolatesByProjectSpace(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.Create(ctx, mkRelease("ps_1", "chg_a", "v1"))
	_ = s.Create(ctx, mkRelease("ps_1", "chg_b", "v2"))
	_ = s.Create(ctx, mkRelease("ps_2", "chg_c", "v1"))

	if n, _ := s.Count(ctx, "ps_1"); n != 2 {
		t.Fatalf("ps_1 Count 应为 2，得到 %d", n)
	}
	if n, _ := s.Count(ctx, "ps_2"); n != 1 {
		t.Fatalf("ps_2 Count 应为 1，得到 %d", n)
	}
}

// TestList_AppNameAndChangeInfo List JOIN 出 app_name/reviewer/prompt/output
// (release→change→source→app 双路径),供发布历史显示应用名+内容+提交人。
func TestList_AppNameAndChangeInfo(t *testing.T) {
	s := newTestStore(t)
	s.db.MustExec(`INSERT INTO appdeploy_application (id, name) VALUES ('app_1', 'hello-go')`)
	s.db.MustExec(`INSERT INTO requirement (id, application_id) VALUES ('req_1', 'app_1')`)
	s.db.MustExec(`INSERT INTO change_request (id, source_id, reviewer, prompt, output) VALUES ('chg_1', 'req_1', 'alice', '实现登录', '【总结】登录页完成')`)
	_ = s.Create(context.Background(), mkRelease("ps_1", "chg_1", "v1"))

	list, err := s.List(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应 1 条,得到 %d", len(list))
	}
	got := list[0]
	if got.AppName != "hello-go" {
		t.Fatalf("app_name 应 JOIN 出 hello-go(经 change→requirement→app),得到 %q", got.AppName)
	}
	if got.Reviewer != "alice" {
		t.Fatalf("reviewer 应 alice,得到 %q", got.Reviewer)
	}
	if got.Prompt != "实现登录" {
		t.Fatalf("prompt 应实现登录,得到 %q", got.Prompt)
	}
}
