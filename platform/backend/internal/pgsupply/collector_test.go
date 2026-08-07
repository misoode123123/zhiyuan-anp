package pgsupply

import (
	"context"
	"testing"

	"go.uber.org/zap"
)

// fakePGAdmin 内存 PGAdmin：按 adminURL+dbNames 返回预设 size map。
// DatabaseSizes 用 fakeSizes[adminURL] 查；adminURL 不存在 → 返回 error 模拟连不上。
type fakePGAdmin struct {
	fakeSizes map[string]map[string]int64 // adminURL → dbName → bytes
	failURL   string                      // 模拟连不上的 adminURL
}

func (f *fakePGAdmin) CreateDatabase(context.Context, string, string) error     { return nil }
func (f *fakePGAdmin) CreateRole(context.Context, string, string, string) error { return nil }
func (f *fakePGAdmin) GrantAll(context.Context, string, string, string) error   { return nil }
func (f *fakePGAdmin) DropDatabase(context.Context, string, string) error       { return nil }
func (f *fakePGAdmin) DropRole(context.Context, string, string) error           { return nil }
func (f *fakePGAdmin) Ping(context.Context, string) error                       { return nil }

func (f *fakePGAdmin) DatabaseSizes(_ context.Context, adminURL string, dbNames []string) (map[string]int64, error) {
	if adminURL == f.failURL {
		return nil, errFakePGUnreachable
	}
	out := make(map[string]int64, len(dbNames))
	m := f.fakeSizes[adminURL]
	for _, n := range dbNames {
		if v, ok := m[n]; ok {
			out[n] = v
		}
	}
	return out, nil
}

// errFakePGUnreachable fake 错误，识别用。
var errFakePGUnreachable = &fakeErr{"fake: PG unreachable"}

type fakeErr struct{ s string }

func (e *fakeErr) Error() string { return e.s }

// 准备测试数据：1 实例 + 2 库（ps_1）+ 配额。
func setupCollectorData(t *testing.T) (*Store, *PGInstance) {
	t.Helper()
	s := newTestStore(t)
	ctx := context.Background()
	ins := mkInstance("ps_1")
	if err := s.CreateInstance(ctx, ins); err != nil {
		t.Fatalf("create instance: %v", err)
	}
	// 再建一个 app_2 供第二个库引用
	s.db.MustExec(`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status)
		VALUES ('app_2','ps_1','t2',8080,'registered') ON CONFLICT DO NOTHING`)
	// 两库（同实例、同项目）
	for _, ad := range []*AppDatabase{
		{ID: "apdb_a", AppID: "app_1", ProjectSpaceID: "ps_1", DBName: "app_a", DBRole: "r_a",
			PGInstanceID: "pgi_abc", DBHost: "h", DBPort: 9500, Status: StatusReady, BackupEnabled: true},
		{ID: "apdb_b", AppID: "app_2", ProjectSpaceID: "ps_1", DBName: "app_b", DBRole: "r_b",
			PGInstanceID: "pgi_abc", DBHost: "h", DBPort: 9500, Status: StatusReady, BackupEnabled: true},
	} {
		if err := s.CreateAppDB(ctx, ad); err != nil {
			t.Fatalf("create appdb %s: %v", ad.ID, err)
		}
	}
	return s, ins
}

// CollectDBSizes 基本流程：2 库 → 2 条 size_bytes 更新；无配额 → 不告警。
func TestCollector_CollectDBSizes(t *testing.T) {
	s, ins := setupCollectorData(t)
	ctx := context.Background()

	pg := &fakePGAdmin{
		fakeSizes: map[string]map[string]int64{
			ins.AdminURLRef: {"app_a": 5 * 1024 * 1024, "app_b": 7 * 1024 * 1024},
		},
	}
	c := NewCollector(s, pg, zap.NewNop())

	r := c.CollectDBSizes(ctx)
	if r.Instances != 1 || r.Total != 2 || r.Updated != 2 || r.Failed != 0 {
		t.Fatalf("结果不符: %+v", r)
	}
	if len(r.Alerts) != 0 {
		t.Fatalf("无配额应不告警，得到 %+v", r.Alerts)
	}

	// 验证 size_bytes 已更新
	a, _ := s.GetAppDBByApp(ctx, "app_1")
	if a.SizeBytes != 5*1024*1024 {
		t.Fatalf("app_a size 应 5MB，得到 %d", a.SizeBytes)
	}
	b, _ := s.GetAppDBByApp(ctx, "app_2")
	if b.SizeBytes != 7*1024*1024 {
		t.Fatalf("app_b size 应 7MB，得到 %d", b.SizeBytes)
	}
}

// CollectDBSizes 超配额 → Alerts 含一条；ProjectTotalSizeMb 读 size_bytes 求和正确。
func TestCollector_AlertOnQuotaExceeded(t *testing.T) {
	s, ins := setupCollectorData(t)
	ctx := context.Background()

	// 设配额 max_total_db_mb=8 → 5+7=12MB 超限
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO project_quota (project_space_id, max_total_db_mb) VALUES ('ps_1', 8)`); err != nil {
		t.Fatalf("set quota: %v", err)
	}

	pg := &fakePGAdmin{
		fakeSizes: map[string]map[string]int64{
			ins.AdminURLRef: {"app_a": 5 * 1024 * 1024, "app_b": 7 * 1024 * 1024},
		},
	}
	c := NewCollector(s, pg, zap.NewNop())

	r := c.CollectDBSizes(ctx)
	if len(r.Alerts) != 1 {
		t.Fatalf("应 1 条告警，得到 %d", len(r.Alerts))
	}
	al := r.Alerts[0]
	if al.ProjectSpaceID != "ps_1" || al.UsedMB != 12 || al.LimitMB != 8 {
		t.Fatalf("告警字段不符: %+v", al)
	}

	// ProjectTotalSizeMb 读 size_bytes 列求和 → 12MB
	mb, err := s.ProjectTotalSizeMb(ctx, "ps_1")
	if err != nil {
		t.Fatalf("ProjectTotalSizeMb: %v", err)
	}
	if mb != 12 {
		t.Fatalf("项目总 size 应 12MB，得到 %d", mb)
	}
}

// 实例连不上 → 跳过该实例，记 failed；不 panic、不中断。
func TestCollector_InstanceUnreachable(t *testing.T) {
	s, ins := setupCollectorData(t)
	ctx := context.Background()

	pg := &fakePGAdmin{
		failURL:   ins.AdminURLRef, // 模拟连不上
		fakeSizes: map[string]map[string]int64{},
	}
	c := NewCollector(s, pg, zap.NewNop())

	r := c.CollectDBSizes(ctx)
	if r.Instances != 1 || r.Total != 2 || r.Updated != 0 || r.Failed != 2 {
		t.Fatalf("实例连不上应 Failed=2，得到 %+v", r)
	}
}

// ListAppDBsByInstance + UpdateAppDBSize + ListActiveInstances 直接验证。
func TestStore_DBSizeAccessors(t *testing.T) {
	s, _ := setupCollectorData(t)
	ctx := context.Background()

	// ListActiveInstances
	list, err := s.ListActiveInstances(ctx)
	if err != nil || len(list) != 1 || list[0].ID != "pgi_abc" {
		t.Fatalf("ListActiveInstances 失败: %v / %+v", err, list)
	}

	// ListAppDBsByInstance
	dbs, err := s.ListAppDBsByInstance(ctx, "pgi_abc")
	if err != nil || len(dbs) != 2 {
		t.Fatalf("ListAppDBsByInstance 失败: %v / %+v", err, dbs)
	}

	// UpdateAppDBSize
	if err := s.UpdateAppDBSize(ctx, "apdb_a", 12345); err != nil {
		t.Fatalf("UpdateAppDBSize: %v", err)
	}
	a, _ := s.GetAppDBByApp(ctx, "app_1")
	if a.SizeBytes != 12345 {
		t.Fatalf("size_bytes 应 12345，得到 %d", a.SizeBytes)
	}

	// MaxTotalDBMb 行不存在 → 0
	mb, err := s.MaxTotalDBMb(ctx, "ps_1")
	if err != nil || mb != 0 {
		t.Fatalf("无配额行应返回 0,nil，得到 %d,%v", mb, err)
	}
}
