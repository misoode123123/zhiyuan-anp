package pgsupply

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newTestStore 连 anp_test PG（testutil 跑迁移建平台全表）+ 清表隔离。
// 替代 sqlite :memory:（sqlite 单测漏 PG 类型 bug，见 memory sqlite-test-pg-type-trap）。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	// FK 前置：pg_instance.project_space_id → project_space（建测试用到的所有 psID，ON CONFLICT 幂等）
	for _, ps := range []string{"ps_1", "ps_new", "ps_x"} {
		db.MustExec(`INSERT INTO project_space (id, name, slug, status) VALUES ('` + ps + `','测试空间','` + ps + `','active') ON CONFLICT (id) DO NOTHING`)
	}
	testutil.Truncate(t, db, "appdeploy_database", "pg_instance", "appdeploy_application")
	// FK 前置：appdeploy_database.app_id → appdeploy_application（Truncate 后重建 app_1）
	db.MustExec(`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status) VALUES ('app_1','ps_1','t',8080,'registered') ON CONFLICT DO NOTHING`)
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
