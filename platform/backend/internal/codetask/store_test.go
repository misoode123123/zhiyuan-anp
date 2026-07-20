package codetask

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 内存 SQLite + code_task/requirement/change_request/appdeploy_application 四表(ListByProjectSpace JOIN 依赖)。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, ddl := range []string{
		`CREATE TABLE code_task (id TEXT PRIMARY KEY, project_space_id TEXT, kind TEXT, source_id TEXT, repo_dir TEXT, prompt TEXT, model TEXT, status TEXT, output TEXT, change_id TEXT, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
		`CREATE TABLE requirement (id TEXT PRIMARY KEY, application_id TEXT, title TEXT)`,
		`CREATE TABLE change_request (id TEXT PRIMARY KEY, source_id TEXT)`,
		`CREATE TABLE appdeploy_application (id TEXT PRIMARY KEY, name TEXT)`,
	} {
		db.MustExec(ddl)
	}
	return NewStore(db)
}

// TestListByProjectSpace_Join dispatch 任务应 JOIN 出 req_title(需求标题) + app_name(变更所属应用)。
// dev 页据此显示"来自需求:登录页"而非 source_id 随机串。
func TestListByProjectSpace_Join(t *testing.T) {
	s := newTestStore(t)
	s.db.MustExec(`INSERT INTO appdeploy_application (id, name) VALUES ('app_1', 'hello-go')`)
	s.db.MustExec(`INSERT INTO requirement (id, application_id, title) VALUES ('req_1', 'app_1', '登录页')`)
	s.db.MustExec(`INSERT INTO change_request (id, source_id) VALUES ('chg_1', 'app_1')`)

	task := &Task{ID: "t_1", ProjectSpaceID: "ps_1", Kind: "dispatch", SourceID: "req_1", Prompt: "实现登录", Model: "glm-5.1"}
	if err := s.Create(context.Background(), task); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.SetChangeID(context.Background(), "t_1", "chg_1"); err != nil {
		t.Fatalf("set change_id: %v", err)
	}

	list, err := s.ListByProjectSpace(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应返回 1 条,得到 %d", len(list))
	}
	got := list[0]
	if got.ReqTitle != "登录页" {
		t.Fatalf("req_title 应 JOIN 出 '登录页',得到 %q", got.ReqTitle)
	}
	if got.AppName != "hello-go" {
		t.Fatalf("app_name 应经 change→app JOIN 出 'hello-go',得到 %q", got.AppName)
	}
}

// TestListByProjectSpace_ManualTask 手动派发(kind=code,无 source_id/change_id)时 req_title/app_name 为空,不报错。
func TestListByProjectSpace_ManualTask(t *testing.T) {
	s := newTestStore(t)
	task := &Task{ID: "t_2", ProjectSpaceID: "ps_1", Kind: "code", Prompt: "写脚本", Model: "glm-5.1"}
	if err := s.Create(context.Background(), task); err != nil {
		t.Fatalf("create: %v", err)
	}
	list, err := s.ListByProjectSpace(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ReqTitle != "" || list[0].AppName != "" {
		t.Fatalf("手动任务 req_title/app_name 应为空,得到 %+v", list)
	}
}
