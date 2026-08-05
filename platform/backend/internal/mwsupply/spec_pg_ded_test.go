package mwsupply

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
)

// —— P2b Task 3: pg dedicated（独立 pgvector 容器 + 库/role）——
//
// pg dedicated 走自管路径：mwsupply 闭包调 pgsupply.InstanceManager.ProvisionDedicated
// （起 per-app 容器 + 建库/role，返回 container/dbName/dsn/adminURL/port）→ 写 DATABASE_URL env
// → 登记 service_instance(kind=pg, dedicated, container_name, host, port, auth_ref=adminURL)
// → 返回 (instID, token=dbName)。Cleanup/ReleaseDep → spec.CleanupDedicated → docker rm container。
//
// InstanceManager 需 docker 无法单测，故 PgDedicatedRunner 接口抽象，用 fake 覆盖 mwsupply 侧闭包逻辑。

// fakePgDedicated 记录 ProvisionDedicated/CleanupDedicated 调用；返 canned 值（或 err 模拟失败）。
type fakePgDedicated struct {
	container  string
	dbName     string
	dsn        string
	adminURL   string
	port       int
	err        error
	provCalls  int
	cleanCalls []string
}

func (f *fakePgDedicated) ProvisionDedicated(_ context.Context, _, _ string) (container, dbName, dsn, adminURL string, port int, err error) {
	f.provCalls++
	return f.container, f.dbName, f.dsn, f.adminURL, f.port, f.err
}

func (f *fakePgDedicated) CleanupDedicated(_ context.Context, container string) error {
	f.cleanCalls = append(f.cleanCalls, container)
	return nil
}

// TestPgDedicated 声明 pg=dedicated → supplyDedicated 调 ProvisionDedicated →
// DATABASE_URL 写入(=dsn)、service_instance 登记(container_name + host + port + auth_ref=adminURL)、
// binding bound(instID, token=dbName)。然后 ReleaseDep → CleanupDedicated(docker rm) 以 container 被调。
func TestPgDedicated(t *testing.T) {
	ded := &fakePgDedicated{
		container: "pg-ded-deadbeef-1",
		dbName:    "app_deadbeef",
		dsn:       "postgres://app_role:secret@testdeploy:9550/app_deadbeef?sslmode=disable",
		adminURL:  "postgres://postgres:pw@testdeploy:9550/postgres",
		port:      9550,
	}
	r, appStore, _, _, _ := newReconcilerTestWithPgDed(t, ded)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "pgdedapp", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}

	r.supplyAll(ctx, a.ID, "ps_1", []DepService{{Kind: "pg", Strategy: ModeDedicated}})

	// ProvisionDedicated 被调一次
	if ded.provCalls != 1 {
		t.Fatalf("ProvisionDedicated 应调 1 次，得 %d", ded.provCalls)
	}
	// DATABASE_URL 写入 = dsn
	gotDSN, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL")
	if gotDSN != ded.dsn {
		t.Fatalf("DATABASE_URL 应 %q，得 %q", ded.dsn, gotDSN)
	}
	src, _ := appStore.GetEnvSource(ctx, a.ID, "DATABASE_URL")
	if src != "platform" {
		t.Fatalf("DATABASE_URL source 应 platform，得 %q", src)
	}
	// binding bound + instID 非空 + token=dbName
	b, _ := r.store.GetBinding(ctx, a.ID, "pg")
	if b == nil || b.Status != StatusBound || b.Strategy != ModeDedicated {
		t.Fatalf("pg binding 应 dedicated/bound，得 %+v", b)
	}
	if b.IsolationToken != ded.dbName {
		t.Fatalf("token 应 dbName %q，得 %q", ded.dbName, b.IsolationToken)
	}
	if b.ServiceInstanceID == "" {
		t.Fatal("dedicated binding 应有 service_instance_id")
	}
	// service_instance 登记：container_name + host + port + auth_ref=adminURL + kind=pg + dedicated
	inst, _ := r.store.GetInstance(ctx, b.ServiceInstanceID)
	if inst == nil {
		t.Fatalf("service_instance 应登记，id=%s", b.ServiceInstanceID)
	}
	if inst.Kind != "pg" || inst.SupplyMode != ModeDedicated {
		t.Fatalf("实例 kind/pg + dedicated，得 %+v", inst)
	}
	if inst.ContainerName != ded.container {
		t.Fatalf("container_name 应 %q，得 %q", ded.container, inst.ContainerName)
	}
	if inst.Port != ded.port {
		t.Fatalf("port 应 %d，得 %d", ded.port, inst.Port)
	}
	if inst.AuthRef != ded.adminURL {
		t.Fatalf("auth_ref 应 adminURL %q，得 %q", ded.adminURL, inst.AuthRef)
	}
	if inst.Status != "active" {
		t.Fatalf("status 应 active，得 %q", inst.Status)
	}

	// ReleaseDep → CleanupDedicated 以 container 被调（docker rm per-app 容器）
	ded.cleanCalls = nil
	r.ReleaseDep(ctx, b)
	if len(ded.cleanCalls) != 1 || ded.cleanCalls[0] != ded.container {
		t.Fatalf("CleanupDedicated 应以 %q 调一次，得 %v", ded.container, ded.cleanCalls)
	}
	// instance 行 + binding 行已删
	if got, _ := r.store.GetInstance(ctx, b.ServiceInstanceID); got != nil {
		t.Fatalf("ReleaseDep 后 service_instance 应删，得 %+v", got)
	}
	// DATABASE_URL 已删
	if v, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL"); v != "" {
		t.Fatalf("ReleaseDep 后 DATABASE_URL 应删，得 %q", v)
	}
}

// TestPgDedicated_provisionError ProvisionDedicated 返错 → binding failed，不写 env、不登记实例。
func TestPgDedicated_provisionError(t *testing.T) {
	ded := &fakePgDedicated{err: errStr("起 PG 容器失败")}
	r, appStore, _, _, _ := newReconcilerTestWithPgDed(t, ded)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "pgdedfail", RepoDir: "/x", InternalPort: 8080}
	_ = appStore.Create(ctx, a)

	r.supplyAll(ctx, a.ID, "ps_1", []DepService{{Kind: "pg", Strategy: ModeDedicated}})
	b, _ := r.store.GetBinding(ctx, a.ID, "pg")
	if b == nil || b.Status != StatusFailed {
		t.Fatalf("应 failed，得 %+v", b)
	}
	if v, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL"); v != "" {
		t.Fatalf("失败不应写 DATABASE_URL，得 %q", v)
	}
}
