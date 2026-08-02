package mwsupply

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// fakeFlusher 记录 FlushDB 调用；err 非 nil 时返错（测 flush 失败路径）。
// 同时实现 ReadyChecker（Ping 返 pingErr，默认 nil=就绪）——NewReconciler 的 flusher+ready 双用。
type fakeFlusher struct {
	calls   int
	err     error
	pingErr error // dedicated 就绪检测返错（默认 nil=就绪）
}

func (f *fakeFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	f.calls++
	return f.err
}

// Ping 满足 ReadyChecker（dedicated 就绪检测）。默认 pingErr=nil → 立即就绪。
func (f *fakeFlusher) Ping(ctx context.Context, host string, port int, password string) error {
	return f.pingErr
}

// fakeDocker 记 RunRedisContainer/RmForce/RunMilvusStack/MilvusReady/RmMilvusStack 调用；
// usedPorts 控制端口池；runErr/stackErr/readyErr 模拟失败。
type fakeDocker struct {
	usedPorts    map[int]struct{}
	runCalls     []fakeDockerRun
	runErr       error
	rmCalls      []string
	stackCalls   []fakeMilvusStack // milvus：RunMilvusStack 调用
	stackErr     error
	rmStackCalls []string // milvus：RmMilvusStack 调用（base）
	readyErr     error   // milvus：MilvusReady 返错（默认 nil=就绪）
	readyCalls   int
}

type fakeDockerRun struct {
	name, password string
	port           int
}

type fakeMilvusStack struct {
	base string
	port int
}

func (f *fakeDocker) UsedPorts(_ context.Context) map[int]struct{} { return f.usedPorts }
func (f *fakeDocker) RunRedisContainer(_ context.Context, name, password string, port int) error {
	f.runCalls = append(f.runCalls, fakeDockerRun{name, password, port})
	return f.runErr
}
func (f *fakeDocker) RmForce(_ context.Context, name string) error {
	f.rmCalls = append(f.rmCalls, name)
	return nil
}
func (f *fakeDocker) RunMilvusStack(_ context.Context, base string, port int) error {
	f.stackCalls = append(f.stackCalls, fakeMilvusStack{base, port})
	return f.stackErr
}
func (f *fakeDocker) MilvusReady(_ context.Context, base string, _ time.Duration) error {
	f.readyCalls++
	return f.readyErr
}
func (f *fakeDocker) RmMilvusStack(_ context.Context, base string) error {
	f.rmStackCalls = append(f.rmStackCalls, base)
	return nil
}

// newReconcilerTest 起 Reconciler（env 用真实 appdeploy.Store；flusher+ready 用同一 fakeFlusher；
// docker 用 fakeDocker）+ 清表 + 保 .28 种子（含 shared）。host=testdeploy（REDIS_ADDR 测试值）。
func newReconcilerTest(t *testing.T) (*Reconciler, *appdeploy.Store, *sqlx.DB, *fakeFlusher, *fakeDocker) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	ensureSeed(t, db)
	appStore := appdeploy.NewStore(db)
	f := &fakeFlusher{}                                       // 同时作 DBFlusher + ReadyChecker
	dk := &fakeDocker{usedPorts: map[int]struct{}{}}
	return NewReconciler(NewStore(db), appStore, f, f, dk, "testdeploy"), appStore, db, f, dk
}

// ensureSeed 确保 .28 redis/milvus bind_existing 种子 + shared redis 种子在（Truncate 不动 service_instance；幂等再插）。
func ensureSeed(t *testing.T, db *sqlx.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO appdeploy_service_instance (id, project_space_id, kind, name, supply_mode, host, port, isolation, status) VALUES
	  ('svinst-redis-28',NULL,'redis','yxt-redis','bind_existing','10.10.0.28',6381,'{"default_db":0}'::jsonb,'active'),
	  ('svinst-milvus-28',NULL,'milvus','yxt-milvus','bind_existing','10.10.0.28',19530,NULL,'active'),
	  ('svinst-redis-shared-28',NULL,'redis','yxt-redis-shared','shared','10.10.0.28',6381,'{"db_range":[1,15]}'::jsonb,'active')
	  ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		t.Fatalf("ensureSeed: %v", err)
	}
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte(body), 0o644)
	return dir
}

// —— 既有 bind_existing 用例（newReconcilerTest 返回值变 4 元，末位 _ 接收 flusher）——

func TestReconcile_bindExisting(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp", RepoDir: "/data/repos/rcapp", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	dir := writeManifest(t, "services:\n  - kind: redis\n  - kind: milvus\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra != "10.10.0.28:6381" {
		t.Fatalf("REDIS_ADDR 应 10.10.0.28:6381，得 %q", ra)
	}
	ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	if ma != "10.10.0.28:19530" {
		t.Fatalf("MILVUS_ADDR 应 10.10.0.28:19530，得 %q", ma)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 2 {
		t.Fatalf("应 2 binding，得 %d", len(binds))
	}
}

func TestReconcile_idempotent(t *testing.T) {
	r, appStore, _, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp2", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("二次 reconcile 不应报错: %v", err)
	}
}

func TestReconcile_missingInstanceKind(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp3", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: mongodb\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("未注册 kind 不应报错: %v", err)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("未注册 kind 应 binding failed，得 %+v", binds)
	}
}

func TestReconcile_noManifest(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp4", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	if err := r.Reconcile(ctx, a.ID, "ps_1", t.TempDir()); err != nil {
		t.Fatalf("无清单不应报错: %v", err)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 0 {
		t.Fatalf("无清单应 0 binding，得 %d", len(binds))
	}
}

// —— shared 用例 ——

// TestReconcile_sharedRedis 两个 shared app 分到不同 db 号；双 env + flush 各 1 次。
func TestReconcile_sharedRedis(t *testing.T) {
	r, appStore, db, fl, _ := newReconcilerTest(t)
	ctx := context.Background()
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")

	mk := func(name string) string {
		a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: name, RepoDir: "/x", InternalPort: 8080}
		_ = appStore.Create(ctx, a)
		_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
		return a.ID
	}
	a1 := mk("sh1")
	a2 := mk("sh2")

	db1, _ := appStore.GetEnvValue(ctx, a1, "REDIS_DB")
	db2, _ := appStore.GetEnvValue(ctx, a2, "REDIS_DB")
	if db1 == "" || db2 == "" || db1 == db2 {
		t.Fatalf("两 app REDIS_DB 应不同且非空，得 %q / %q", db1, db2)
	}
	for _, aid := range []string{a1, a2} {
		ra, _ := appStore.GetEnvValue(ctx, aid, "REDIS_ADDR")
		if ra != "10.10.0.28:6381" {
			t.Fatalf("REDIS_ADDR 应 10.10.0.28:6381，得 %q", ra)
		}
		src, _ := appStore.GetEnvSource(ctx, aid, "REDIS_DB")
		if src != "platform" {
			t.Fatalf("REDIS_DB source 应 platform，得 %q", src)
		}
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a1)
	if len(binds) != 1 || binds[0].Status != StatusBound || binds[0].IsolationToken != db1 {
		t.Fatalf("a1 binding 应 bound token=%s，得 %+v", db1, binds)
	}
	if fl.calls != 2 {
		t.Fatalf("flush 应调 2 次（每 app 新分配 1 次），得 %d", fl.calls)
	}
}

// TestReconcile_shared_idempotent 同 app 重部署：号不变、不再 flush、env 仍在。
func TestReconcile_shared_idempotent(t *testing.T) {
	r, appStore, _, fl, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "shidem", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	db1, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB")
	fl.calls = 0 // 重置计数
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir) // 重部署
	db2, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB")
	if db1 != db2 {
		t.Fatalf("重部署 db 号应不变，%q → %q", db1, db2)
	}
	if fl.calls != 0 {
		t.Fatalf("重部署复用不应 flush，得 %d 次", fl.calls)
	}
}

// TestReconcile_shared_flushFailBestEffort flush 失败(best-effort) → 仍 claim + 注入 env，binding bound。
// 后端可能无 redis 网络访问（.28 即如此）；flush 是重分配卫生，非首次分配正确性所需 → 不阻塞。
func TestReconcile_shared_flushFailBestEffort(t *testing.T) {
	r, appStore, db, fl, _ := newReconcilerTest(t)
	fl.err = errStr("redis 不可达")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "shfail", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound {
		t.Fatalf("flush best-effort 失败应仍 bound，得 %+v", binds)
	}
	rdb, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB")
	if rdb == "" {
		t.Fatalf("flush best-effort 失败仍应写 REDIS_DB，得空")
	}
}

// TestReconcile_shared_poolExhaust 占满 1-15 后第 16 个 → failed。
func TestReconcile_shared_poolExhaust(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n")
	for i := 0; i < 15; i++ {
		a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "ex" + string(rune('a'+i)), RepoDir: "/x", InternalPort: 8080}
		_ = appStore.Create(ctx, a)
		if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
			t.Fatalf("前 15 不应错: %v", err)
		}
	}
	a16 := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "ex16", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a16)
	_ = r.Reconcile(ctx, a16.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a16.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("第 16 个应 failed（池满），得 %+v", binds)
	}
}

// —— dedicated 用例 ——

// TestReconcile_dedicatedRedis 新供给：起容器 + 就绪 + 登记 + 双 env。
func TestReconcile_dedicatedRedis(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "ded1", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 起了一次容器，端口 9600（空池最小号）
	if len(dk.runCalls) != 1 || dk.runCalls[0].port != mwPortMin {
		t.Fatalf("应起 1 容器 port=%d，得 %+v", mwPortMin, dk.runCalls)
	}
	// env：REDIS_ADDR=testdeploy:9600 + REDIS_PASSWORD 非空（secret/platform）
	ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra != "testdeploy:9600" {
		t.Fatalf("REDIS_ADDR 应 testdeploy:9600，得 %q", ra)
	}
	rp, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_PASSWORD")
	if rp == "" {
		t.Fatal("REDIS_PASSWORD 应非空")
	}
	rsrc, _ := appStore.GetEnvSource(ctx, a.ID, "REDIS_PASSWORD")
	if rsrc != "platform" {
		t.Fatalf("REDIS_PASSWORD source 应 platform，得 %q", rsrc)
	}
	// 不注入 REDIS_DB
	if rdb, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_DB"); rdb != "" {
		t.Fatalf("dedicated 不应注入 REDIS_DB，得 %q", rdb)
	}
	// binding bound + 实例行带 container_name
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound || binds[0].Strategy != ModeDedicated {
		t.Fatalf("binding 应 dedicated/bound，得 %+v", binds)
	}
	inst, _ := NewStore(db).GetInstance(ctx, binds[0].ServiceInstanceID)
	if inst == nil || inst.ContainerName == "" || inst.Port != mwPortMin {
		t.Fatalf("实例行应带 container_name + port=%d，得 %+v", mwPortMin, inst)
	}
}

// TestReconcile_dedicated_idempotent 同 app 重部署：不重启容器、port 不变、env 仍在。
func TestReconcile_dedicated_idempotent(t *testing.T) {
	r, appStore, _, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedidem", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	ra1, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	dk.runCalls = nil // 重置
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir) // 重部署
	ra2, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra1 != ra2 {
		t.Fatalf("重部署 REDIS_ADDR 应不变，%q → %q", ra1, ra2)
	}
	if len(dk.runCalls) != 0 {
		t.Fatalf("重部署复用不应再起容器，得 %d 次", len(dk.runCalls))
	}
}

// TestReconcile_dedicated_poolExhaust 端口池满 → failed、不起容器、不写 env。
func TestReconcile_dedicated_poolExhaust(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	// 占满 9600-9699
	full := map[int]struct{}{}
	for p := mwPortMin; p <= mwPortMax; p++ {
		full[p] = struct{}{}
	}
	dk.usedPorts = full
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedex", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("池满应 failed，得 %+v", binds)
	}
	if len(dk.runCalls) != 0 {
		t.Fatalf("池满不应起容器，得 %d", len(dk.runCalls))
	}
	if ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR"); ra != "" {
		t.Fatalf("池满不应写 REDIS_ADDR，得 %q", ra)
	}
}

// TestReconcile_dedicated_runFail 起容器失败 → failed、不登记实例。
func TestReconcile_dedicated_runFail(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	dk.runErr = errStr("docker run 失败")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedfail", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed || binds[0].ServiceInstanceID != "" {
		t.Fatalf("起容器失败应 failed + 无实例，得 %+v", binds)
	}
}

// TestReconcile_dedicated_readyFail_bestEffort 就绪检测失败(best-effort) → 仍 bound、不 RmForce、env 已写。
// .28 backend(deploy_default) 拨不到 host 发布端口会超时，但 app(默认 bridge) 能到 → ping 失败不阻塞。
func TestReconcile_dedicated_readyFail_bestEffort(t *testing.T) {
	r, appStore, db, fl, dk := newReconcilerTest(t)
	fl.pingErr = errStr("redis 不可达")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedready", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	// best-effort：ping 失败仍 bound、容器保留（不 RmForce）、env 已写
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound {
		t.Fatalf("ping best-effort 失败应仍 bound，得 %+v", binds)
	}
	if len(dk.rmCalls) != 0 {
		t.Fatalf("best-effort 不应 RmForce 容器，得 %v", dk.rmCalls)
	}
	if ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR"); ra == "" {
		t.Fatal("best-effort 应已写 REDIS_ADDR")
	}
}

// TestReconcile_cleanup_dedicated 删 dedicated app → docker rm 容器 + 删 instance 行。
func TestReconcile_cleanup_dedicated(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "dedclean", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	instID := binds[0].ServiceInstanceID
	inst, _ := NewStore(db).GetInstance(ctx, instID)
	cname := inst.ContainerName
	dk.rmCalls = nil

	if err := r.Cleanup(ctx, a.ID); err != nil {
		t.Fatalf("Cleanup 不应报错: %v", err)
	}
	// docker rm 了 dedicated 容器
	if len(dk.rmCalls) != 1 || dk.rmCalls[0] != cname {
		t.Fatalf("应 RmForce %q，得 %v", cname, dk.rmCalls)
	}
	// instance 行已删
	if got, _ := NewStore(db).GetInstance(ctx, instID); got != nil {
		t.Fatalf("Cleanup 后实例行应删，得 %+v", got)
	}
}

// TestReconcile_cleanup_skipsSharedAndBindExisting Cleanup 只动 dedicated，不碰 shared/bind_existing（靠 CASCADE）。
func TestReconcile_cleanup_skipsSharedAndBindExisting(t *testing.T) {
	r, _, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	// shared app
	as := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "shclean", RepoDir: "/x", InternalPort: 8080}
	_ = appdeploy.NewStore(db).Create(ctx, as)
	_ = r.Reconcile(ctx, as.ID, "ps_1", writeManifest(t, "services:\n  - kind: redis\n    strategy: shared\n"))
	// bind_existing app
	ab := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "beclean", RepoDir: "/x", InternalPort: 8080}
	_ = appdeploy.NewStore(db).Create(ctx, ab)
	_ = r.Reconcile(ctx, ab.ID, "ps_1", writeManifest(t, "services:\n  - kind: redis\n"))
	dk.rmCalls = nil

	_ = r.Cleanup(ctx, as.ID)
	_ = r.Cleanup(ctx, ab.ID)
	if len(dk.rmCalls) != 0 {
		t.Fatalf("shared/bind_existing 不应触发 RmForce，得 %v", dk.rmCalls)
	}
}

// errStr 造个简单 error。
type errStr string

func (e errStr) Error() string { return string(e) }

// —— milvus dedicated 用例 ——

// TestReconcile_dedicatedMilvus 新供给：起栈 + 登记 + MILVUS_ADDR（无 password/db）。
func TestReconcile_dedicatedMilvus(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "milded1", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	// 起了一次栈，端口 9700（空池最小号）
	if len(dk.stackCalls) != 1 || dk.stackCalls[0].port != milvusPortMin {
		t.Fatalf("应起 1 栈 port=%d，得 %+v", milvusPortMin, dk.stackCalls)
	}
	// env：MILVUS_ADDR=testdeploy:9700；无 MILVUS_PASSWORD；无 MILVUS_DB
	ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	if ma != "testdeploy:9700" {
		t.Fatalf("MILVUS_ADDR 应 testdeploy:9700，得 %q", ma)
	}
	if mp, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_PASSWORD"); mp != "" {
		t.Fatalf("milvus v1 无 auth，不应写 MILVUS_PASSWORD，得 %q", mp)
	}
	if mdb, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_DB"); mdb != "" {
		t.Fatalf("milvus dedicated 不应注入 MILVUS_DB，得 %q", mdb)
	}
	// binding bound + 实例行 kind=milvus / container_name=mwmilvus-<short> / port=9700
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound || binds[0].Strategy != ModeDedicated {
		t.Fatalf("binding 应 dedicated/bound，得 %+v", binds)
	}
	inst, _ := NewStore(db).GetInstance(ctx, binds[0].ServiceInstanceID)
	if inst == nil || inst.Kind != "milvus" || inst.ContainerName == "" || !strings.HasPrefix(inst.ContainerName, "mwmilvus-") || inst.Port != milvusPortMin {
		t.Fatalf("实例行应 kind=milvus + mwmilvus-<short> + port=%d，得 %+v", milvusPortMin, inst)
	}
}

// TestReconcile_dedicatedMilvus_idempotent 同 app 重部署：不重启栈、port 不变、env 仍在。
func TestReconcile_dedicatedMilvus_idempotent(t *testing.T) {
	r, appStore, _, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildidem", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	ma1, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	dk.stackCalls = nil
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir) // 重部署
	ma2, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR")
	if ma1 != ma2 {
		t.Fatalf("重部署 MILVUS_ADDR 应不变，%q → %q", ma1, ma2)
	}
	if len(dk.stackCalls) != 0 {
		t.Fatalf("重部署复用不应再起栈，得 %d 次", len(dk.stackCalls))
	}
}

// TestReconcile_dedicatedMilvus_poolExhaust milvus 端口池满 → failed、不起栈、不写 env。
func TestReconcile_dedicatedMilvus_poolExhaust(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	full := map[int]struct{}{}
	for p := milvusPortMin; p <= milvusPortMax; p++ {
		full[p] = struct{}{}
	}
	dk.usedPorts = full
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildex", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed {
		t.Fatalf("池满应 failed，得 %+v", binds)
	}
	if len(dk.stackCalls) != 0 {
		t.Fatalf("池满不应起栈，得 %d", len(dk.stackCalls))
	}
	if ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR"); ma != "" {
		t.Fatalf("池满不应写 MILVUS_ADDR，得 %q", ma)
	}
}

// TestReconcile_dedicatedMilvus_stackFail 起栈失败 → failed、不登记实例。
func TestReconcile_dedicatedMilvus_stackFail(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	dk.stackErr = errStr("docker run milvus 失败")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildfail", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusFailed || binds[0].ServiceInstanceID != "" {
		t.Fatalf("起栈失败应 failed + 无实例，得 %+v", binds)
	}
}

// TestReconcile_dedicatedMilvus_readyTimeout_bestEffort 就绪探针超时(best-effort) → 仍 bound、不 RmMilvusStack、env 已写。
func TestReconcile_dedicatedMilvus_readyTimeout_bestEffort(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	dk.readyErr = errStr("milvus 就绪超时")
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildready", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 1 || binds[0].Status != StatusBound {
		t.Fatalf("就绪 best-effort 失败应仍 bound，得 %+v", binds)
	}
	if len(dk.rmStackCalls) != 0 {
		t.Fatalf("best-effort 不应 RmMilvusStack，得 %v", dk.rmStackCalls)
	}
	if ma, _ := appStore.GetEnvValue(ctx, a.ID, "MILVUS_ADDR"); ma == "" {
		t.Fatal("best-effort 应已写 MILVUS_ADDR")
	}
}

// TestReconcile_cleanup_dedicatedMilvus 删 milvus dedicated app → RmMilvusStack(base) + 删 instance 行。
func TestReconcile_cleanup_dedicatedMilvus(t *testing.T) {
	r, appStore, db, _, dk := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "mildclean", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: milvus\n    strategy: dedicated\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	instID := binds[0].ServiceInstanceID
	inst, _ := NewStore(db).GetInstance(ctx, instID)
	base := inst.ContainerName
	dk.rmStackCalls = nil

	if err := r.Cleanup(ctx, a.ID); err != nil {
		t.Fatalf("Cleanup 不应报错: %v", err)
	}
	// docker rm 了 milvus 栈（按 base）
	if len(dk.rmStackCalls) != 1 || dk.rmStackCalls[0] != base {
		t.Fatalf("应 RmMilvusStack %q，得 %v", base, dk.rmStackCalls)
	}
	// redis 的 RmForce 不应被调（此 app 只有 milvus dedicated）
	if len(dk.rmCalls) != 0 {
		t.Fatalf("milvus cleanup 不应触发 redis RmForce，得 %v", dk.rmCalls)
	}
	// instance 行已删
	if got, _ := NewStore(db).GetInstance(ctx, instID); got != nil {
		t.Fatalf("Cleanup 后实例行应删，得 %+v", got)
	}
}
