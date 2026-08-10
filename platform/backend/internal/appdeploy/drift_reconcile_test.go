package appdeploy

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeImageInspector struct {
	img string
	err error
}

func (f *fakeImageInspector) InspectImage(_ context.Context, _ string) (string, error) {
	return f.img, f.err
}

type fakeDriftAlerter struct {
	drift    []string // 记 name:env
	resolved []string
}

func (a *fakeDriftAlerter) OnDrift(_ context.Context, _, _, name, env, _ string) error {
	a.drift = append(a.drift, name+":"+env)
	return nil
}
func (a *fakeDriftAlerter) OnDriftResolved(_ context.Context, _, _, name, env string) error {
	a.resolved = append(a.resolved, name+":"+env)
	return nil
}

// seedDriftInstance 建应用 + 实例（指定 image/version/container_name），返回 driftInstance 巡检参数。
func seedDriftInstance(t *testing.T, store *Store, ps, name, image string, version int) driftInstance {
	t.Helper()
	a := &Application{ProjectSpaceID: ps, Name: name, AppKind: AppKindWeb, InternalPort: 8080, RepoDir: t.TempDir()}
	if err := store.Create(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	ctr := name + "-ctr"
	if _, err := store.db.ExecContext(context.Background(),
		`INSERT INTO appdeploy_instance (id, app_id, env, status, image, version, container_name)
		 VALUES ($1,$2,$3,'running',$4,$5,$6)`,
		"ins_"+name, a.ID, EnvTest, image, version, ctr); err != nil {
		t.Fatal(err)
	}
	return driftInstance{
		AppID: a.ID, Env: EnvTest, Image: image, Version: version,
		ContainerName: ctr, RepoDir: a.RepoDir, ProjectSpaceID: ps, Name: name,
	}
}

// 自愈升：DB v8 / 容器 v10 → DB 升到 v10 + resolved（无 drift 告警）。
func TestDriftCheckOne_SelfHealUp(t *testing.T) {
	store := newTestStore(t)
	target := seedDriftInstance(t, store, "ps_du", "svc", "appdeploy/svc-test:v8", 8)
	al := &fakeDriftAlerter{}
	r := &DriftReconciler{store: store, deployer: &fakeImageInspector{img: "appdeploy/svc-test:v10"}, alerter: al, interval: time.Second}

	r.checkOne(context.Background(), target)

	ins, _ := store.GetInstance(context.Background(), target.AppID, EnvTest)
	if ins.Version != 10 || ins.Image != "appdeploy/svc-test:v10" {
		t.Fatalf("自愈升后 DB 应 v10/v10tag got version=%d image=%q", ins.Version, ins.Image)
	}
	if len(al.drift) != 0 || len(al.resolved) != 1 {
		t.Fatalf("自愈后应 resolved 无 drift got drift=%d resolved=%d", len(al.drift), len(al.resolved))
	}
}

// 向下不降：DB v10 / 容器 v8 → DB 不变 + drift 告警（疑似回滚）。
func TestDriftCheckOne_Down_NoDecrease(t *testing.T) {
	store := newTestStore(t)
	target := seedDriftInstance(t, store, "ps_dd", "svc2", "appdeploy/svc2-test:v10", 10)
	al := &fakeDriftAlerter{}
	r := &DriftReconciler{store: store, deployer: &fakeImageInspector{img: "appdeploy/svc2-test:v8"}, alerter: al}

	r.checkOne(context.Background(), target)

	ins, _ := store.GetInstance(context.Background(), target.AppID, EnvTest)
	if ins.Version != 10 || ins.Image != "appdeploy/svc2-test:v10" {
		t.Fatalf("向下应不降 DB 保持 v10 got version=%d image=%q", ins.Version, ins.Image)
	}
	if len(al.drift) != 1 || len(al.resolved) != 0 {
		t.Fatalf("向下应 drift 告警 got drift=%d resolved=%d", len(al.drift), len(al.resolved))
	}
}

// inspect 失败：早退，无 DB 写、无告警（不误报）。
func TestDriftCheckOne_InspectFail(t *testing.T) {
	store := newTestStore(t)
	target := seedDriftInstance(t, store, "ps_df", "svc3", "appdeploy/svc3-test:v5", 5)
	al := &fakeDriftAlerter{}
	r := &DriftReconciler{store: store, deployer: &fakeImageInspector{err: errors.New("docker unreachable")}, alerter: al}

	r.checkOne(context.Background(), target)

	ins, _ := store.GetInstance(context.Background(), target.AppID, EnvTest)
	if ins.Version != 5 || len(al.drift) != 0 || len(al.resolved) != 0 {
		t.Fatalf("inspect 失败应不变不告警 got version=%d drift=%d resolved=%d", ins.Version, len(al.drift), len(al.resolved))
	}
}

// 一致：DB=容器=v7 无 manifest → OK，resolved 清旧告警，无 drift、无 DB 改动。
func TestDriftCheckOne_Consistent(t *testing.T) {
	store := newTestStore(t)
	target := seedDriftInstance(t, store, "ps_dc", "svc4", "appdeploy/svc4-test:v7", 7)
	al := &fakeDriftAlerter{}
	r := &DriftReconciler{store: store, deployer: &fakeImageInspector{img: "appdeploy/svc4-test:v7"}, alerter: al}

	r.checkOne(context.Background(), target)

	ins, _ := store.GetInstance(context.Background(), target.AppID, EnvTest)
	if ins.Version != 7 {
		t.Fatalf("一致时不应改 DB got version=%d", ins.Version)
	}
	if len(al.drift) != 0 || len(al.resolved) != 1 {
		t.Fatalf("一致应 resolved 无 drift got drift=%d resolved=%d", len(al.drift), len(al.resolved))
	}
}

// 编译期断言：OpsHealthAlerter 实现 DriftAlerter。
func TestOpsHealthAlerter_SatisfiesDriftAlerter(t *testing.T) {
	var _ DriftAlerter = (*OpsHealthAlerter)(nil)
}
