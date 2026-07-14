package ops

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + ops 表 + 看板聚合依赖的最小列。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	schema := `
CREATE TABLE ops_alert (
	id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, source TEXT NOT NULL DEFAULT 'custom',
	severity TEXT NOT NULL DEFAULT 'warning', status TEXT NOT NULL DEFAULT 'firing',
	fingerprint TEXT NOT NULL, title TEXT NOT NULL, description TEXT,
	fired_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, resolved_at DATETIME,
	created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE ops_sop (
	id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, code TEXT NOT NULL, name TEXT NOT NULL,
	description TEXT, category TEXT NOT NULL DEFAULT 'restart', risk_level TEXT NOT NULL DEFAULT 'low',
	steps TEXT, rollback TEXT, requires_approval INTEGER NOT NULL DEFAULT 0,
	status TEXT NOT NULL DEFAULT 'draft', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE (project_space_id, code));
CREATE TABLE requirement (id TEXT PRIMARY KEY, project_space_id TEXT, status TEXT, title TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE code_task (id TEXT PRIMARY KEY, project_space_id TEXT, status TEXT, prompt TEXT, updated_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE change_request (id TEXT PRIMARY KEY, project_space_id TEXT, status TEXT, prompt TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE release_record (id TEXT PRIMARY KEY, project_space_id TEXT, version TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE usage_record (id TEXT PRIMARY KEY, project_space_id TEXT, total_tokens INTEGER NOT NULL DEFAULT 0);
CREATE INDEX idx_ops_alert_ps ON ops_alert(project_space_id, status);`
	db.MustExec(schema)
	return NewStore(db)
}

func TestAlert_FingerprintDedupAndList(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a1 := &Alert{ProjectSpaceID: "ps1", Source: "patrol", Severity: "critical", Title: "组件异常: db", Description: "down"}
	a2 := &Alert{ProjectSpaceID: "ps1", Source: "patrol", Severity: "critical", Title: "组件异常: db"} // 同指纹
	if err := s.CreateAlert(ctx, a1); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	if err := s.CreateAlert(ctx, a2); err != nil {
		t.Fatalf("create a2: %v", err)
	}
	if a1.Fingerprint == "" || a1.Fingerprint != a2.Fingerprint {
		t.Fatalf("同源同标题应同指纹: %q vs %q", a1.Fingerprint, a2.Fingerprint)
	}
	list, err := s.ListAlerts(ctx, "ps1", "", "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("应有 2 条告警，得到 %d", len(list))
	}
	// severity 过滤
	crit, _ := s.ListAlerts(ctx, "ps1", "critical", "")
	if len(crit) != 2 {
		t.Fatalf("critical 过滤应 2 条，得到 %d", len(crit))
	}
	open, _ := s.CountOpenAlerts(ctx, "ps1")
	if open != 2 {
		t.Fatalf("firing 告警应 2 条，得到 %d", open)
	}
	// resolve 后计入去重判定
	if err := s.ResolveAlert(ctx, "ps1", a1.ID); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	exist, _ := s.HasFiringFingerprint(ctx, a1.Fingerprint)
	if !exist {
		t.Fatal("a2 仍 firing，同指纹应判存在")
	}
}

func TestSOP_CRUD(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	sop := &SOP{ProjectSpaceID: "ps1", Code: "RESTART", Name: "重启", Category: "restart", RiskLevel: "low", Status: "active", Steps: "步骤"}
	if err := s.CreateSOP(ctx, sop); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := s.GetSOP(ctx, "ps1", sop.ID)
	if err != nil || got.Name != "重启" {
		t.Fatalf("get 失败: %v %v", got, err)
	}
	list, _ := s.ListSOPs(ctx, "ps1", "")
	if len(list) != 1 {
		t.Fatalf("list 应 1 条，得到 %d", len(list))
	}
	act, _ := s.ListSOPs(ctx, "ps1", "active")
	if len(act) != 1 {
		t.Fatalf("active 过滤应 1 条")
	}
	sop.Name = "重启更新"
	if err := s.UpdateSOP(ctx, sop); err != nil {
		t.Fatalf("update: %v", err)
	}
	if n, _ := s.CountActiveSOPs(ctx, "ps1"); n != 1 {
		t.Fatalf("active 计数应 1，得到 %d", n)
	}
	// 同 project_space 重复 code 应失败（UNIQUE）
	if err := s.CreateSOP(ctx, &SOP{ProjectSpaceID: "ps1", Code: "RESTART", Name: "dup"}); err == nil {
		t.Fatal("重复 code 应违反唯一约束")
	}
	if err := s.DeleteSOP(ctx, "ps1", sop.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestDashboard_Aggregation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	db := s.db
	ps := "ps1"
	// 造数据：2 需求（draft/delivered）、1 编码任务（completed）、1 变更（pending）、1 发布、用量 1500
	db.MustExec(`INSERT INTO requirement (id,project_space_id,status,title) VALUES ('r1',?,'draft','A'),('r2',?,'delivered','B')`, ps, ps)
	db.MustExec(`INSERT INTO code_task (id,project_space_id,status,prompt) VALUES ('c1',?,'completed','p')`, ps)
	db.MustExec(`INSERT INTO change_request (id,project_space_id,status,prompt) VALUES ('ch1',?,'pending','ch')`, ps)
	db.MustExec(`INSERT INTO release_record (id,project_space_id,version) VALUES ('rl1',?,'v1')`, ps)
	db.MustExec(`INSERT INTO usage_record (id,project_space_id,total_tokens) VALUES ('u1',?,1500)`, ps)

	st, err := s.Stats(ctx, ps)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if st.Requirements["draft"] != 1 || st.Requirements["delivered"] != 1 {
		t.Fatalf("requirements 统计错误: %+v", st.Requirements)
	}
	if st.CodeTasks["completed"] != 1 {
		t.Fatalf("code_tasks 统计错误: %+v", st.CodeTasks)
	}
	if st.Changes["pending"] != 1 {
		t.Fatalf("changes 统计错误: %+v", st.Changes)
	}
	if st.Releases != 1 {
		t.Fatalf("releases 应 1，得到 %d", st.Releases)
	}
	u, err := s.Usage(ctx, ps)
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	if u.TotalTokens != 1500 || u.TotalCalls != 1 {
		t.Fatalf("usage 错误: %+v", u)
	}
	act, err := s.Activity(ctx, ps)
	if err != nil {
		t.Fatalf("activity: %v", err)
	}
	if len(act) != 5 {
		t.Fatalf("活动流应 5 条（需求 2 + 编码 1 + 变更 1 + 发布 1），得到 %d", len(act))
	}
}

func TestOverallHealth(t *testing.T) {
	if got := OverallHealth([]ComponentHealth{{Status: "healthy"}}); got != "healthy" {
		t.Fatalf("全 healthy 应 healthy，得到 %s", got)
	}
	if got := OverallHealth([]ComponentHealth{{Status: "healthy"}, {Status: "degraded"}}); got != "degraded" {
		t.Fatalf("有 degraded 应 degraded，得到 %s", got)
	}
	if got := OverallHealth([]ComponentHealth{{Status: "down"}}); got != "down" {
		t.Fatalf("有 down 应 down，得到 %s", got)
	}
}
