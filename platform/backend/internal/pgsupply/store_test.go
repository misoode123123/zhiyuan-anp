package pgsupply

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 内存 SQLite + pg_instance/appdeploy_database 两表（仿 appdeploy/store_test.go）。
// 类型映射：PG TIMESTAMP→DATETIME、BOOLEAN→INTEGER、INT/TEXT 原样。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE pg_instance (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  host             TEXT NOT NULL,
  port             INTEGER NOT NULL DEFAULT 5432,
  admin_url_ref    TEXT NOT NULL,
  deploy_mode      TEXT NOT NULL DEFAULT 'managed',
  status           TEXT NOT NULL DEFAULT 'active',
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	db.MustExec(`CREATE TABLE appdeploy_database (
  id               TEXT PRIMARY KEY,
  app_id           TEXT NOT NULL UNIQUE,
  project_space_id TEXT NOT NULL,
  db_name          TEXT NOT NULL UNIQUE,
  db_role          TEXT NOT NULL,
  pg_instance_id   TEXT NOT NULL,
  db_host          TEXT NOT NULL,
  db_port          INTEGER NOT NULL DEFAULT 5432,
  status           TEXT NOT NULL DEFAULT 'provisioning',
  last_error       TEXT,
  backup_enabled   INTEGER NOT NULL DEFAULT 1,
  last_backup_at   DATETIME,
  schema_version   TEXT,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewStore(db)
}

func mkInstance(ps string) *PGInstance {
	return &PGInstance{ID: "pgi_abc", ProjectSpaceID: ps, Host: "h", Port: 9500,
		AdminURLRef: "postgres://postgres:p@h:9500/postgres?sslmode=disable", DeployMode: DeployManaged, Status: StatusActive}
}

func TestStore_CreateInstanceAndGetByProject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	if err := s.CreateInstance(ctx, mkInstance("ps_1")); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	got, err := s.GetInstanceByProject(ctx, "ps_1")
	if err != nil {
		t.Fatalf("get by project: %v", err)
	}
	if got.ID != "pgi_abc" {
		t.Fatalf("应返回实例，得到 %+v", got)
	}
	// 不存在的项目 → 返回 err（sql.ErrNoRows）
	if _, err := s.GetInstanceByProject(ctx, "ps_none"); err == nil {
		t.Fatal("不存在项目应返回 err")
	}
}

func TestStore_AppDBLifecycle(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	_ = s.CreateInstance(ctx, mkInstance("ps_1"))
	ad := &AppDatabase{ID: "apdb_1", AppID: "app_1", ProjectSpaceID: "ps_1",
		DBName: "app_x", DBRole: "app_x_role", PGInstanceID: "pgi_abc",
		DBHost: "h", DBPort: 9500, Status: StatusReady, BackupEnabled: true}
	if err := s.CreateAppDB(ctx, ad); err != nil {
		t.Fatalf("create appdb: %v", err)
	}
	got, err := s.GetAppDBByApp(ctx, "app_1")
	if err != nil || got == nil || got.DBName != "app_x" {
		t.Fatalf("get appdb 失败: got=%+v err=%v", got, err)
	}
	if err := s.SetAppDBStatus(ctx, "apdb_1", StatusFailed, "boom"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ = s.GetAppDBByApp(ctx, "app_1")
	if got.Status != StatusFailed || got.LastError != "boom" {
		t.Fatalf("状态未更新: %+v", got)
	}
	if err := s.DeleteAppDB(ctx, "app_1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetAppDBByApp(ctx, "app_1"); err == nil {
		t.Fatal("删除后应查不到")
	}
}
