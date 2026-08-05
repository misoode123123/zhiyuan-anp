package mwsupply

import (
	"context"
	"database/sql"
	"testing"

	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
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

// TestPgDedicated_reReconcilePreservesDSN C1 回归：pg dedicated 二次供给（reuse 分支）不重写 DATABASE_URL。
// spec.SupplyDedicated != nil 时 reuse 分支须跳过 writeDedicatedEnvSpec——否则 ConnStr(inst)=host:port
// 会覆盖首次供给写入的有效 app-role DSN。同时验证 token（dbName）不被清空、不再起容器。
func TestPgDedicated_reReconcilePreservesDSN(t *testing.T) {
	ded := &fakePgDedicated{
		container: "pg-ded-reuse-1",
		dbName:    "app_reuse",
		dsn:       "postgres://app_role:secret@testdeploy:9550/app_reuse?sslmode=disable",
		adminURL:  "postgres://postgres:pw@testdeploy:9550/postgres",
		port:      9550,
	}
	r, appStore, _, _, _ := newReconcilerTestWithPgDed(t, ded)
	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "pgdedreuse", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}

	deps := []DepService{{Kind: "pg", Strategy: ModeDedicated}}
	// 第一次供给：DATABASE_URL=dsn，token=dbName
	r.supplyAll(ctx, a.ID, "ps_1", deps)
	firstDSN, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL")
	if firstDSN != ded.dsn {
		t.Fatalf("首次 DATABASE_URL 应 %q，得 %q", ded.dsn, firstDSN)
	}
	b1, _ := r.store.GetBinding(ctx, a.ID, "pg")
	if b1 == nil || b1.IsolationToken != ded.dbName {
		t.Fatalf("首次 token 应 %q，得 %+v", ded.dbName, b1)
	}

	// 第二次供给（走 reuse 分支）
	ded.provCalls = 0 // 重置；reuse 不应再调 ProvisionDedicated
	r.supplyAll(ctx, a.ID, "ps_1", deps)

	// DATABASE_URL 不变（仍是 dsn，非 host:port）
	secondDSN, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL")
	if secondDSN != ded.dsn {
		t.Fatalf("二次供给 DATABASE_URL 应不变 %q，得 %q", ded.dsn, secondDSN)
	}
	if secondDSN == "testdeploy:9550" {
		t.Fatalf("DATABASE_URL 被降级为 host:port（C1 回归）：%q", secondDSN)
	}
	// reuse 不应再起容器
	if ded.provCalls != 0 {
		t.Fatalf("reuse 不应再调 ProvisionDedicated，得 %d 次", ded.provCalls)
	}
	// token 保留（不被清空）
	b2, _ := r.store.GetBinding(ctx, a.ID, "pg")
	if b2 == nil || b2.Status != StatusBound {
		t.Fatalf("二次应仍 bound，得 %+v", b2)
	}
	if b2.IsolationToken != ded.dbName {
		t.Fatalf("二次 token 应保留 %q，得 %q", ded.dbName, b2.IsolationToken)
	}
}

// fakeEnvWriter 记录 UpsertEnv/DeleteEnv 调用；upsertErr 非 nil 时 UpsertEnv 返错（测 env 写失败路径）。
// 满足 EnvWriter 接口，用于 I1 测试注入（harness 默认用真实 appdeploy.Store 无法模拟写失败）。
type fakeEnvWriter struct {
	upsertErr   error
	upsertCalls []fakeEnvUpsert
	deleteCalls []fakeEnvDelete
}

type fakeEnvUpsert struct {
	appID, key, value string
	isSecret          bool
	source            string
}
type fakeEnvDelete struct {
	appID, key string
}

func (f *fakeEnvWriter) UpsertEnv(_ context.Context, appID, key, value string, isSecret bool, source string) error {
	f.upsertCalls = append(f.upsertCalls, fakeEnvUpsert{appID, key, value, isSecret, source})
	return f.upsertErr
}
func (f *fakeEnvWriter) DeleteEnv(_ context.Context, appID, key string) error {
	f.deleteCalls = append(f.deleteCalls, fakeEnvDelete{appID, key})
	return nil
}

// TestPgDedicated_envWriteFail I1：env 写失败 → CleanupDedicated 回收容器 + binding failed + 不登记 service_instance。
// 构造 Reconciler 注入 fakeEnvWriter（UpsertEnv 返错）：ProvisionDedicated 成功后写 DATABASE_URL 失败 →
// 回收容器（ded.CleanupDedicated）→ 返错 → supplyDedicated mkBind(failed)。容器不留、实例不登记。
func TestPgDedicated_envWriteFail(t *testing.T) {
	ded := &fakePgDedicated{
		container: "pg-ded-envfail-1",
		dbName:    "app_envfail",
		dsn:       "postgres://app_role:secret@testdeploy:9551/app_envfail?sslmode=disable",
		adminURL:  "postgres://postgres:pw@testdeploy:9551/postgres",
		port:      9551,
	}
	// 构造 Reconciler：env 用 fakeEnvWriter（UpsertEnv 返错），store/docker/flusher 同 harness。
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	ensureSeed(t, db)
	store := NewStore(db)
	appStore := appdeploy.NewStore(db)
	fl := &fakeFlusher{}
	dk := &fakeDocker{usedPorts: map[int]struct{}{}}
	failEnv := &fakeEnvWriter{upsertErr: errStr("env 写失败")}
	r := NewReconciler(store, failEnv, fl, fl, dk, "testdeploy", nil, ded)

	ctx := context.Background()
	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "pgenvfail", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}

	r.supplyAll(ctx, a.ID, "ps_1", []DepService{{Kind: "pg", Strategy: ModeDedicated}})

	// ProvisionDedicated 被调（容器确实起了）
	if ded.provCalls != 1 {
		t.Fatalf("ProvisionDedicated 应调 1 次，得 %d", ded.provCalls)
	}
	// CleanupDedicated 被调（容器回收）
	if len(ded.cleanCalls) != 1 || ded.cleanCalls[0] != ded.container {
		t.Fatalf("CleanupDedicated 应以 %q 调一次，得 %v", ded.container, ded.cleanCalls)
	}
	// binding failed（非 bound）
	b, _ := store.GetBinding(ctx, a.ID, "pg")
	if b == nil || b.Status != StatusFailed {
		t.Fatalf("env 写失败应 binding failed，得 %+v", b)
	}
	// 不登记 service_instance（binding 无 instance id）
	if b.ServiceInstanceID != "" {
		t.Fatalf("env 写失败不应登记 service_instance，得 %q", b.ServiceInstanceID)
	}
	// DATABASE_URL 未落库（fake env 不写真 DB）
	if v, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL"); v != "" {
		t.Fatalf("env 写失败不应落 DATABASE_URL，得 %q", v)
	}
}

// TestPgDedicated_createInstanceFail M-1：env 写成功后 store.CreateInstance 失败 →
// 须 DeleteEnv(DATABASE_URL) 删掉刚写的 env 行（防 stale 残留至下次 reconcile）+ 回收容器 + 返错。
// pgSpec 闭包捕获 *Store 具体类型无法注入 fake store，故：env 用 fakeEnvWriter（记录 upsert/delete，
// env 侧不碰 DB），store 用 closed db 句柄让 CreateInstance 必失败（ExecContext → sql: database is closed），
// 直接驱动 spec.SupplyDedicated 闭包。
func TestPgDedicated_createInstanceFail(t *testing.T) {
	ded := &fakePgDedicated{
		container: "pg-ded-cifail-1",
		dbName:    "app_cifail",
		dsn:       "postgres://app_role:secret@testdeploy:9552/app_cifail?sslmode=disable",
		adminURL:  "postgres://postgres:pw@testdeploy:9552/postgres",
		port:      9552,
	}
	// closed db 句柄：CreateInstance（INSERT）必失败，不依赖网络。
	sqlDB, _ := sql.Open("pgx", "")
	sqlDB.Close()
	store := NewStore(sqlx.NewDb(sqlDB, "pgx"))
	recEnv := &fakeEnvWriter{} // UpsertEnv 成功；记录 DeleteEnv（M-1 验证点）

	// pgSpec(nil prov)：dedicated 路径不碰 SupplyShared，prov 留 nil。
	spec := pgSpec(nil, ded, store, recEnv)
	instID, token, err := spec.SupplyDedicated(context.Background(), "app_cifail", "ps_1", "testdeploy")

	// 返错 + 空返回
	if err == nil {
		t.Fatal("CreateInstance 失败应返错")
	}
	if instID != "" || token != "" {
		t.Fatalf("失败应空返回 instID/token，得 %q/%q", instID, token)
	}
	// ProvisionDedicated 被调（容器确实起了）
	if ded.provCalls != 1 {
		t.Fatalf("ProvisionDedicated 应调 1 次，得 %d", ded.provCalls)
	}
	// DATABASE_URL 先写（env.UpsertEnv 成功）
	if len(recEnv.upsertCalls) != 1 || recEnv.upsertCalls[0].key != "DATABASE_URL" {
		t.Fatalf("应先 UpsertEnv DATABASE_URL，得 %+v", recEnv.upsertCalls)
	}
	if recEnv.upsertCalls[0].value != ded.dsn {
		t.Fatalf("UpsertEnv 值应 dsn %q，得 %q", ded.dsn, recEnv.upsertCalls[0].value)
	}
	// M-1 核心：DATABASE_URL 已被删除（不残留 stale 行）
	if len(recEnv.deleteCalls) != 1 || recEnv.deleteCalls[0].key != "DATABASE_URL" {
		t.Fatalf("CreateInstance 失败应 DeleteEnv DATABASE_URL（M-1），得 %+v", recEnv.deleteCalls)
	}
	if recEnv.deleteCalls[0].appID != "app_cifail" {
		t.Fatalf("DeleteEnv appID 应 app_cifail，得 %q", recEnv.deleteCalls[0].appID)
	}
	// CleanupDedicated 被调（容器回收）
	if len(ded.cleanCalls) != 1 || ded.cleanCalls[0] != ded.container {
		t.Fatalf("CleanupDedicated 应以 %q 调一次，得 %v", ded.container, ded.cleanCalls)
	}
}
