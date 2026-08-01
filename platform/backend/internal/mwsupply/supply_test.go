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

// newReconcilerTest 起 Reconciler（env 用真实 appdeploy.Store，满足 EnvWriter）+ 清表 + 保 .28 种子。
func newReconcilerTest(t *testing.T) (*Reconciler, *appdeploy.Store, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	ensureSeed(t, db)
	appStore := appdeploy.NewStore(db)
	return NewReconciler(NewStore(db), appStore), appStore, db
}

// ensureSeed 确保 .28 redis/milvus 种子在（Truncate 不动 service_instance，迁移已种；幂等再插）。
func ensureSeed(t *testing.T, db *sqlx.DB) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO appdeploy_service_instance (id, project_space_id, kind, name, supply_mode, host, port, isolation, status) VALUES
	  ('svinst-redis-28',NULL,'redis','yxt-redis','bind_existing','10.10.0.28',6381,'{"default_db":0}'::jsonb,'active'),
	  ('svinst-milvus-28',NULL,'milvus','yxt-milvus','bind_existing','10.10.0.28',19530,NULL,'active')
	  ON CONFLICT (id) DO NOTHING`)
	if err != nil {
		t.Fatalf("ensureSeed: %v", err)
	}
}

// writeManifest 在临时 repo dir 写 .anp/deps.yaml。
func writeManifest(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	_ = os.MkdirAll(filepath.Join(dir, ".anp"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, ".anp", "deps.yaml"), []byte(body), 0o644)
	return dir
}

// TestReconcile_bindExisting 写了 deps.yaml → 注入 REDIS_ADDR/MILVUS_ADDR(source=platform) + binding bound。
func TestReconcile_bindExisting(t *testing.T) {
	r, appStore, db := newReconcilerTest(t)
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
	src, _ := appStore.GetEnvSource(ctx, a.ID, "REDIS_ADDR")
	if src != "platform" {
		t.Fatalf("REDIS_ADDR source 应 platform，得 %q", src)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 2 {
		t.Fatalf("应 2 binding，得 %d", len(binds))
	}
	for _, b := range binds {
		if b.Status != StatusBound {
			t.Fatalf("binding %s 应 bound，得 %s", b.ServiceKind, b.Status)
		}
	}
}

// TestReconcile_idempotent 再跑一次不报错、env/binding 仍正确。
func TestReconcile_idempotent(t *testing.T) {
	r, appStore, _ := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp2", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	dir := writeManifest(t, "services:\n  - kind: redis\n")
	_ = r.Reconcile(ctx, a.ID, "ps_1", dir)
	if err := r.Reconcile(ctx, a.ID, "ps_1", dir); err != nil {
		t.Fatalf("二次 reconcile 不应报错: %v", err)
	}
	ra, _ := appStore.GetEnvValue(ctx, a.ID, "REDIS_ADDR")
	if ra != "10.10.0.28:6381" {
		t.Fatalf("二次后 REDIS_ADDR 仍应正确，得 %q", ra)
	}
}

// TestReconcile_missingInstanceKind 未注册的 kind → binding failed，不 panic、不报错。
func TestReconcile_missingInstanceKind(t *testing.T) {
	r, appStore, db := newReconcilerTest(t)
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

// TestReconcile_noManifest 无清单 → 不写任何 env（应用仅 DATABASE_URL 由 pgsupply 管）。
func TestReconcile_noManifest(t *testing.T) {
	r, appStore, db := newReconcilerTest(t)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "rcapp4", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)
	if err := r.Reconcile(ctx, a.ID, "ps_1", t.TempDir()); err != nil { // 无 .anp/deps.yaml
		t.Fatalf("无清单不应报错: %v", err)
	}
	binds, _ := NewStore(db).ListBindingsByApp(ctx, a.ID)
	if len(binds) != 0 {
		t.Fatalf("无清单应 0 binding，得 %d", len(binds))
	}
}
