package appdeploy

import (
	"context"
	"errors"
	"testing"
	"time"

	"zhiyuan-anp/platform/backend/internal/ops"
)

func TestAggregateHealth(t *testing.T) {
	cases := []struct {
		name       string
		h          ContainerHealth
		stored     int
		wantStatus string
		wantCount  int
	}{
		{"exited→failed", ContainerHealth{Running: false, RestartCount: 5}, 5, "failed", 5},
		{"running 稳定→running", ContainerHealth{Running: true, RestartCount: 2}, 2, "running", 2},
		{"冷启动基线(有历史重启)→running 不告警", ContainerHealth{Running: true, RestartCount: 9}, 0, "running", 9},
		{"crash-loop(本周期新增≥3)→degraded", ContainerHealth{Running: true, RestartCount: 8}, 5, "degraded", 8},
		{"曾 degraded,无新增→running(非粘恢复)", ContainerHealth{Running: true, RestartCount: 8}, 8, "running", 8},
	}
	for _, c := range cases {
		got, cnt := aggregateHealth(c.h, c.stored, 3)
		if got != c.wantStatus || cnt != c.wantCount {
			t.Fatalf("%s: got (%s,%d) want (%s,%d)", c.name, got, cnt, c.wantStatus, c.wantCount)
		}
	}
}

type fakeInspector struct{ h ContainerHealth; err error }

func (f *fakeInspector) InspectHealth(_ context.Context, _ string) (ContainerHealth, error) {
	return f.h, f.err
}

type fakeAlerter struct {
	unhealthy []string // 记 appID+env
	recovered []string
}

func (a *fakeAlerter) OnUnhealthy(_ context.Context, _, appID, _, env, _, _ string) error {
	a.unhealthy = append(a.unhealthy, appID+":"+env)
	return nil
}
func (a *fakeAlerter) OnRecovered(_ context.Context, _, appID, _, env string) error {
	a.recovered = append(a.recovered, appID+":"+env)
	return nil
}

// 直接测 checkOne(绕过 ticker):崩溃翻 failed+告警;恢复翻 running+resolved;restart_count 回写。
func TestCheckOne_CrashAndRecover(t *testing.T) {
	store := newTestStore(t)
	ps := "ps_rec"
	a := &Application{ProjectSpaceID: ps, Name: "bot", AppKind: AppKindHeadless, InternalPort: 0}
	store.Create(context.Background(), a)
	store.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status, container_name, restart_count)
		 VALUES ('ins1',$1,$2,'running','bot-ctr',0)`, a.ID, EnvTest)

	al := &fakeAlerter{}
	r := &HealthReconciler{store: store, deployer: &fakeInspector{h: ContainerHealth{Running: false, ExitCode: 137}}, alerter: al, interval: time.Second, burst: 3}

	// 崩溃 → failed + 告警
	r.checkOne(context.Background(), headlessInstance{AppID: a.ID, Env: EnvTest, ContainerName: "bot-ctr", Status: "running", RestartCount: 0, ProjectSpaceID: ps, Name: "bot"})
	ins, _ := store.GetInstance(context.Background(), a.ID, EnvTest)
	if ins.Status != "failed" || len(al.unhealthy) != 1 {
		t.Fatalf("崩溃后应 failed+告警,得 status=%s alerts=%v", ins.Status, al.unhealthy)
	}

	// 恢复(running) → running + recovered
	r.deployer = &fakeInspector{h: ContainerHealth{Running: true, RestartCount: 0}}
	r.checkOne(context.Background(), headlessInstance{AppID: a.ID, Env: EnvTest, ContainerName: "bot-ctr", Status: "failed", RestartCount: 0, ProjectSpaceID: ps, Name: "bot"})
	ins, _ = store.GetInstance(context.Background(), a.ID, EnvTest)
	if ins.Status != "running" || len(al.recovered) != 1 {
		t.Fatalf("恢复后应 running+resolved,得 status=%s recovered=%v", ins.Status, al.recovered)
	}

	// crash-loop → degraded + 告警 + restart_count 回写
	r.deployer = &fakeInspector{h: ContainerHealth{Running: true, RestartCount: 5}}
	r.checkOne(context.Background(), headlessInstance{AppID: a.ID, Env: EnvTest, ContainerName: "bot-ctr", Status: "running", RestartCount: 1, ProjectSpaceID: ps, Name: "bot"})
	ins, _ = store.GetInstance(context.Background(), a.ID, EnvTest)
	if ins.Status != "degraded" || ins.RestartCount != 5 {
		t.Fatalf("crash-loop 应 degraded 且 restart_count=5,得 %+v", ins)
	}

	// 稳定(无新增) → 恢复 running(非粘)
	r.deployer = &fakeInspector{h: ContainerHealth{Running: true, RestartCount: 5}}
	r.checkOne(context.Background(), headlessInstance{AppID: a.ID, Env: EnvTest, ContainerName: "bot-ctr", Status: "degraded", RestartCount: 5, ProjectSpaceID: ps, Name: "bot"})
	ins, _ = store.GetInstance(context.Background(), a.ID, EnvTest)
	if ins.Status != "running" {
		t.Fatalf("crash-loop 停止后应非粘恢复 running,得 %s", ins.Status)
	}
}

// inspect 失败:只记 last_error,不改 status,不告警。
func TestCheckOne_InspectFail_NoFlip(t *testing.T) {
	store := newTestStore(t)
	ps := "ps_if"
	a := &Application{ProjectSpaceID: ps, Name: "bot3", AppKind: AppKindHeadless, InternalPort: 0}
	store.Create(context.Background(), a)
	store.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status) VALUES ('ins_if',$1,$2,'running')`, a.ID, EnvTest)
	al := &fakeAlerter{}
	r := &HealthReconciler{store: store, deployer: &fakeInspector{err: errors.New("docker unreachable")}, alerter: al, burst: 3}
	r.checkOne(context.Background(), headlessInstance{AppID: a.ID, Env: EnvTest, ContainerName: "x", Status: "running", RestartCount: 0, ProjectSpaceID: ps, Name: "bot3"})
	ins, _ := store.GetInstance(context.Background(), a.ID, EnvTest)
	if ins.Status != "running" || len(al.unhealthy) != 0 {
		t.Fatalf("inspect 失败应保留 running 不告警,得 status=%s alerts=%d", ins.Status, len(al.unhealthy))
	}
}

// 无翻转但基线推进:冷启动观测到历史重启(stored=0 观测 9),status 不变不告警,但 restart_count 0→9 回写。
// 回归保护:若有人删掉 checkOne 的 else-if 分支,aggregateHealth 纯函数测试仍通过,但存储基线永不前进 →
// 冷启动分支每周期重入 → 真实 crash-loop 永远检测不到。此测试强制 UpdateRestartCount 在 checkOne 层执行。
func TestCheckOne_NoFlip_WritesBaseline(t *testing.T) {
	store := newTestStore(t)
	ps := "ps_nf"
	a := &Application{ProjectSpaceID: ps, Name: "bot4", AppKind: AppKindHeadless, InternalPort: 0}
	store.Create(context.Background(), a)
	store.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status, container_name, restart_count)
		 VALUES ('ins_nf',$1,$2,'running','bot-ctr',0)`, a.ID, EnvTest)

	al := &fakeAlerter{}
	r := &HealthReconciler{store: store, deployer: &fakeInspector{h: ContainerHealth{Running: true, RestartCount: 9}}, alerter: al, interval: time.Second, burst: 3}

	// 冷启动基线:stored=0 观测 9 → status=running 不翻转,不告警,但基线 0→9 回写
	r.checkOne(context.Background(), headlessInstance{AppID: a.ID, Env: EnvTest, ContainerName: "bot-ctr", Status: "running", RestartCount: 0, ProjectSpaceID: ps, Name: "bot4"})
	ins, _ := store.GetInstance(context.Background(), a.ID, EnvTest)
	if ins.Status != "running" {
		t.Fatalf("无翻转应保留 running,得 %s", ins.Status)
	}
	if len(al.unhealthy) != 0 || len(al.recovered) != 0 {
		t.Fatalf("无翻转不应告警,得 unhealthy=%d recovered=%d", len(al.unhealthy), len(al.recovered))
	}
	if ins.RestartCount != 9 {
		t.Fatalf("基线应回写为 9(证明 UpdateRestartCount 执行),得 %d", ins.RestartCount)
	}
}

// TestOpsHealthAlerter_Dedup 已有 firing 时 OnUnhealthy 跳过(去重)；OnRecovered 按 fingerprint resolve。
// 桥接：appdeploy.Store.DB() → ops.NewStore 同库，OpsHealthAlerter 写 ops_alert 表。
func TestOpsHealthAlerter_Dedup(t *testing.T) {
	s := newTestStore(t)
	odb := ops.NewStore(s.DB())
	alerter := NewOpsHealthAlerter(odb)
	ctx := context.Background()
	ps := "ps_dedup"

	// 两次 OnUnhealthy 同源同标题 → 去重，只留 1 条 firing
	if err := alerter.OnUnhealthy(ctx, ps, "app1", "bot", EnvTest, "critical", "exit 1"); err != nil {
		t.Fatalf("OnUnhealthy 1: %v", err)
	}
	if err := alerter.OnUnhealthy(ctx, ps, "app1", "bot", EnvTest, "critical", "exit 1"); err != nil {
		t.Fatalf("OnUnhealthy 2: %v", err)
	}
	list, _ := odb.ListAlerts(ctx, ps, "", "firing")
	if len(list) != 1 {
		t.Fatalf("去重后期望 1 条 firing,得到 %d", len(list))
	}

	// OnRecovered → resolve 该 fingerprint，firing 归 0
	if err := alerter.OnRecovered(ctx, ps, "app1", "bot", EnvTest); err != nil {
		t.Fatalf("OnRecovered: %v", err)
	}
	list, _ = odb.ListAlerts(ctx, ps, "", "firing")
	if len(list) != 0 {
		t.Fatalf("recover 后期望 0 条 firing,得到 %d", len(list))
	}
}

// TestOpsHealthAlerter_SatisfiesInterface 编译期断言：OpsHealthAlerter 实现 HealthAlerter。
func TestOpsHealthAlerter_SatisfiesInterface(t *testing.T) {
	var _ HealthAlerter = (*OpsHealthAlerter)(nil)
}
