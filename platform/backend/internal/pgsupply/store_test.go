package pgsupply

import (
	"context"
	"strings"
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
	testutil.Truncate(t, db, "db_action_log", "appdeploy_database", "pg_instance", "appdeploy_application", "project_quota")
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

// newActionLog 构造测试用审计日志。
func newActionLog(id, ps, app, db, actor, actionType, stmt, status string) *ActionLog {
	return &ActionLog{
		ID: id, ProjectSpaceID: ps, AppID: app, DBName: db,
		Actor: actor, ActionType: actionType, Statement: stmt, Status: status,
	}
}

func TestStore_ActionLogCRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// CreateActionLog：写 success + failed 两条
	if err := s.CreateActionLog(ctx, newActionLog("dal_1", "ps_1", "app_1", "app_x", "alice", "SELECT", "SELECT 1", "success")); err != nil {
		t.Fatalf("create action log: %v", err)
	}
	failLog := newActionLog("dal_2", "ps_1", "app_1", "app_x", "bob", "DDL", "DROP TABLE t", "failed")
	failLog.Error = "permission denied"
	if err := s.CreateActionLog(ctx, failLog); err != nil {
		t.Fatalf("create failed log: %v", err)
	}

	// ListActionLogs：按时间倒序，最新在前 → dal_2 在前
	list, err := s.ListActionLogs(ctx, "app_1", 50)
	if err != nil {
		t.Fatalf("list action logs: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应返回 2 条，得到 %d", len(list))
	}
	if list[0].ID != "dal_2" {
		t.Fatalf("倒序首条应为 dal_2，得到 %s", list[0].ID)
	}
	// COALESCE：failed 条 error 应读回，success 条 error 空串
	if list[0].Error != "permission denied" {
		t.Fatalf("failed error 应读回，得到 %q", list[0].Error)
	}
	if list[1].Error != "" {
		t.Fatalf("success error 应为空，得到 %q", list[1].Error)
	}

	// 按 appID 过滤
	other, _ := s.ListActionLogs(ctx, "app_other", 50)
	if len(other) != 0 {
		t.Fatalf("app_other 应无日志，得到 %d", len(other))
	}
}

func TestStore_ActionLogLimit(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 写 5 条
	for i := 0; i < 5; i++ {
		// 用不同 id 避免主键冲突
		al := newActionLog("dal_l"+itoa(i), "ps_1", "app_1", "app_x", "u", "SELECT", "SELECT 1", "success")
		if err := s.CreateActionLog(ctx, al); err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
	}
	// limit=3 → 只 3 条
	list, _ := s.ListActionLogs(ctx, "app_1", 3)
	if len(list) != 3 {
		t.Fatalf("limit=3 应返回 3 条，得到 %d", len(list))
	}
	// limit=0 → 默认 50（>5 全取）
	all, _ := s.ListActionLogs(ctx, "app_1", 0)
	if len(all) != 5 {
		t.Fatalf("limit=0 默认 50 应返回全部 5 条，得到 %d", len(all))
	}
	// limit>200 → 默认 50
	big, _ := s.ListActionLogs(ctx, "app_1", 9999)
	if len(big) != 5 {
		t.Fatalf("limit>200 取默认 50 应返回全部 5 条，得到 %d", len(big))
	}
}

func TestStore_ActionLogStatementTruncate(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	// 超 5000 字符 → 截断到 5000
	long := strings.Repeat("a", 6000)
	al := newActionLog("dal_long", "ps_1", "app_1", "app_x", "u", "OTHER", long, "success")
	if err := s.CreateActionLog(ctx, al); err != nil {
		t.Fatalf("create long stmt: %v", err)
	}
	list, _ := s.ListActionLogs(ctx, "app_1", 50)
	var got *ActionLog
	for i := range list {
		if list[i].ID == "dal_long" {
			got = &list[i]
			break
		}
	}
	if got == nil {
		t.Fatal("找不到 dal_long")
	}
	if len(got.Statement) != actionLogStmtMax {
		t.Fatalf("statement 应截断到 %d，得到 %d", actionLogStmtMax, len(got.Statement))
	}
}

// itoa 简易整数转字符串（避免引入 strconv 仅此一处）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := []byte{}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	return string(buf)
}
