package mwsupply

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jmoiron/sqlx"
	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// fakeFlusher 记录 FlushDB 调用；err 非 nil 时返错（测 flush 失败路径）。
type fakeFlusher struct {
	calls int
	err   error
}

func (f *fakeFlusher) FlushDB(ctx context.Context, host string, port int, password string, db int) error {
	f.calls++
	return f.err
}

// newReconcilerTest 起 Reconciler（env 用真实 appdeploy.Store；flusher 用 fake）+ 清表 + 保 .28 种子（含 shared）。
func newReconcilerTest(t *testing.T) (*Reconciler, *appdeploy.Store, *sqlx.DB, *fakeFlusher) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	ensureSeed(t, db)
	appStore := appdeploy.NewStore(db)
	f := &fakeFlusher{}
	return NewReconciler(NewStore(db), appStore, f), appStore, db, f
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
	r, appStore, db, _ := newReconcilerTest(t)
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
	r, appStore, _, _ := newReconcilerTest(t)
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
	r, appStore, db, _ := newReconcilerTest(t)
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
	r, appStore, db, _ := newReconcilerTest(t)
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
	r, appStore, db, fl := newReconcilerTest(t)
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
	r, appStore, _, fl := newReconcilerTest(t)
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
	r, appStore, db, fl := newReconcilerTest(t)
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
	r, appStore, db, _ := newReconcilerTest(t)
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

// errStr 造个简单 error。
type errStr string

func (e errStr) Error() string { return string(e) }
