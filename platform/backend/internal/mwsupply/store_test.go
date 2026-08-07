package mwsupply

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newTestStore 连 anp_test PG（迁移建表含 000028 + .28 种子）+ 清绑定/env/应用表隔离。
// 不清 appdeploy_service_instance：.28 种子（迁移插入）需保留供 LookupBindExisting。
func newTestStore(t *testing.T) (*Store, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_service_binding", "appdeploy_env", "appdeploy_application")
	return NewStore(db), db
}

// mkAppRow 建一条应用记录（绑定的 FK 父行）。
func mkAppRow(t *testing.T, db *sqlx.DB, name, ps string) string {
	t.Helper()
	a := &appdeploy.Application{ProjectSpaceID: ps, Name: name, RepoDir: "/data/repos/" + name, InternalPort: 8080}
	if err := appdeploy.NewStore(db).Create(context.Background(), a); err != nil {
		t.Fatalf("create app: %v", err)
	}
	return a.ID
}

// TestStore_LookupBindExisting_seed 迁移种子 svinst-redis-28 可被查到（平台级 NULL 实例）。
func TestStore_LookupBindExisting_seed(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.LookupBindExisting(context.Background(), "ps_1", "redis")
	if err != nil || got == nil {
		t.Fatalf("应命中 .28 redis 种子，err=%v got=%+v", err, got)
	}
	if got.Host != "10.10.0.28" || got.Port != 6381 || got.ID != "svinst-redis-28" {
		t.Fatalf("redis 种子不符: %+v", got)
	}
	// milvus 种子也在
	gotM, _ := s.LookupBindExisting(context.Background(), "ps_1", "milvus")
	if gotM == nil || gotM.Port != 19530 {
		t.Fatalf("milvus 种子应命中，得 %+v", gotM)
	}
	// 未注册 kind 返回 nil,nil（不报错）
	gotX, err := s.LookupBindExisting(context.Background(), "ps_1", "mongodb")
	if err != nil || gotX != nil {
		t.Fatalf("未注册 kind 应 nil,nil，得 %+v err=%v", gotX, err)
	}
}

// TestStore_RegisterBindExisting 注册一个新实例后 Lookup 命中;幂等不重复。
// 用平台级(ProjectSpaceID=nil)避开 project_space FK + kind=mongodb 避开 redis/milvus 种子。
func TestStore_RegisterBindExisting(t *testing.T) {
	s, db := newTestStore(t)
	// service_instance 不在 newTestStore 的 truncate 清单（要保留迁移种子 redis/milvus），
	// 故本测试注册的非种子 mongodb 实例须自行清理，否则按源序泄漏到 TestReconcile_missingInstanceKind
	// （该测试依赖 mongodb 无实例 → 应 StatusFailed），并清掉本地 .28 历史 mongodb 残留。
	t.Cleanup(func() { db.MustExec(`DELETE FROM appdeploy_service_instance WHERE kind='mongodb'`) })
	inst := &ServiceInstance{Kind: "mongodb", Name: "my-mongo", Host: "10.10.0.99", Port: 27017}
	if err := s.RegisterBindExisting(context.Background(), inst); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if inst.ID == "" {
		t.Fatal("应自动生成 ID")
	}
	got, err := s.LookupBindExisting(context.Background(), "ps_1", "mongodb")
	if err != nil || got == nil {
		t.Fatalf("注册后应 Lookup 到(平台级): %v %v", got, err)
	}
	if got.Host != "10.10.0.99" || got.Port != 27017 {
		t.Fatalf("应命中新注册实例,得 %+v", got)
	}
	// 幂等:同 kind+scope+host+port 再注不报错不重复
	if err := s.RegisterBindExisting(context.Background(), &ServiceInstance{Kind: "mongodb", Host: "10.10.0.99", Port: 27017}); err != nil {
		t.Fatalf("幂等注册应不报错: %v", err)
	}
}

// TestStore_ListBindExisting 列出注册的实例(平台级 elasticsearch,避开种子)。
func TestStore_ListBindExisting(t *testing.T) {
	s, _ := newTestStore(t)
	_ = s.RegisterBindExisting(context.Background(), &ServiceInstance{Kind: "elasticsearch", Host: "h2", Port: 9200})
	list, err := s.ListBindExisting(context.Background(), "ps_list")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, inst := range list {
		if inst.Kind == "elasticsearch" && inst.Host == "h2" {
			found = true
		}
	}
	if !found {
		t.Fatal("ListBindExisting 应含刚注册的 elasticsearch")
	}
}

// TestStore_UpsertBinding_upsert 绑定按 app+kind 幂等 upsert（ON CONFLICT 更新）。
func TestStore_UpsertBinding_upsert(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	appID := mkAppRow(t, db, "app_b1", "ps_1")

	b := &ServiceBinding{AppID: appID, ProjectSpaceID: "ps_1", ServiceKind: "redis",
		Strategy: ModeBindExisting, ServiceInstanceID: "svinst-redis-28", EnvKey: "REDIS_ADDR", Status: StatusBound}
	if err := s.UpsertBinding(ctx, b); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// 二次 upsert（同 app+kind）走 ON CONFLICT：状态 failed + last_error 更新
	b.Status = StatusFailed
	b.LastError = "x"
	if err := s.UpsertBinding(ctx, b); err != nil {
		t.Fatalf("upsert2: %v", err)
	}
	list, _ := s.ListBindingsByApp(ctx, appID)
	if len(list) != 1 || list[0].Status != StatusFailed || list[0].LastError != "x" {
		t.Fatalf("应 1 条 bound→failed，得 %+v", list)
	}
}

// TestMigration_000029_sharedSeedAndIndex 迁移后：shared redis 种子在 + 部分唯一索引在。
func TestMigration_000029_sharedSeedAndIndex(t *testing.T) {
	_, db := newTestStore(t)
	// 种子行 + isolation.db_range 解析正确
	var lo, hi int
	err := db.QueryRow(`SELECT (isolation->'db_range'->>0)::int, (isolation->'db_range'->>1)::int
		FROM appdeploy_service_instance
		WHERE id='svinst-redis-shared-28' AND supply_mode='shared' AND project_space_id IS NULL`).Scan(&lo, &hi)
	if err != nil {
		t.Fatalf("shared redis 种子缺失: %v", err)
	}
	if lo != 1 || hi != 15 {
		t.Fatalf("db_range 应 [1,15]，得 [%d,%d]", lo, hi)
	}
	// 部分唯一索引存在
	var idxExists bool
	if err := db.Get(&idxExists, `SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='uq_svbind_inst_token')`); err != nil {
		t.Fatalf("查索引: %v", err)
	}
	if !idxExists {
		t.Fatal("部分唯一索引 uq_svbind_inst_token 应存在")
	}
}

// TestStore_LookupShared_seed 平台级 shared redis 种子可查到。
func TestStore_LookupShared_seed(t *testing.T) {
	s, _ := newTestStore(t)
	got, err := s.LookupShared(context.Background(), "redis")
	if err != nil || got == nil {
		t.Fatalf("应命中 shared redis 种子，err=%v got=%+v", err, got)
	}
	if got.ID != "svinst-redis-shared-28" || got.SupplyMode != "shared" || got.Port != 6381 {
		t.Fatalf("shared 种子不符: %+v", got)
	}
	// shared milvus 种子也在（P5）
	gotM, err := s.LookupShared(context.Background(), "milvus")
	if err != nil || gotM == nil {
		t.Fatalf("应命中 shared milvus 种子，err=%v got=%+v", err, gotM)
	}
	if gotM.ID != "svinst-milvus-shared-28" || gotM.SupplyMode != "shared" || gotM.Port != 19530 {
		t.Fatalf("shared milvus 种子不符: %+v", gotM)
	}
	// 未注册 kind 仍 nil,nil
	gotX, err := s.LookupShared(context.Background(), "mongodb")
	if err != nil || gotX != nil {
		t.Fatalf("未注册 kind 应 nil,nil，得 %+v err=%v", gotX, err)
	}
}

// TestStore_AllocatedTokens 占用集正确。
func TestStore_AllocatedTokens(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	appA := mkAppRow(t, db, "sh_a", "ps_1")
	appB := mkAppRow(t, db, "sh_b", "ps_1")
	_ = s.ClaimSharedToken(ctx, appA, "ps_1", "redis", "svinst-redis-shared-28", "1", "REDIS_ADDR")
	_ = s.ClaimSharedToken(ctx, appB, "ps_1", "redis", "svinst-redis-shared-28", "3", "REDIS_ADDR")
	toks, err := s.AllocatedTokens(ctx, "svinst-redis-shared-28")
	if err != nil {
		t.Fatalf("AllocatedTokens: %v", err)
	}
	if len(toks) != 2 || !contains(toks, "1") || !contains(toks, "3") {
		t.Fatalf("占用集应 {1,3}，得 %v", toks)
	}
}

// TestStore_ClaimSharedToken_uniqueViolation 不同 app 抢同 (inst,token) → 第二个 23505。
func TestStore_ClaimSharedToken_uniqueViolation(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	appA := mkAppRow(t, db, "cu_a", "ps_1")
	appB := mkAppRow(t, db, "cu_b", "ps_1")
	if err := s.ClaimSharedToken(ctx, appA, "ps_1", "redis", "svinst-redis-shared-28", "5", "REDIS_ADDR"); err != nil {
		t.Fatalf("首次 claim: %v", err)
	}
	err := s.ClaimSharedToken(ctx, appB, "ps_1", "redis", "svinst-redis-shared-28", "5", "REDIS_ADDR")
	if !isUniqueViolation(err) {
		t.Fatalf("撞号应 23505，得 %v", err)
	}
}

// TestStore_GetBinding 取/无。
func TestStore_GetBinding(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	app := mkAppRow(t, db, "gb_a", "ps_1")
	if b, err := s.GetBinding(ctx, app, "redis"); err != nil || b != nil {
		t.Fatalf("无 binding 应 nil,nil，得 %+v err=%v", b, err)
	}
	_ = s.ClaimSharedToken(ctx, app, "ps_1", "redis", "svinst-redis-shared-28", "2", "REDIS_ADDR")
	b, err := s.GetBinding(ctx, app, "redis")
	if err != nil || b == nil || b.IsolationToken != "2" || b.Status != StatusBound {
		t.Fatalf("应取到 bound token=2，得 %+v err=%v", b, err)
	}
}

// TestStore_shared_recycle 删 binding → token 回收（AllocatedTokens 不再含）。
func TestStore_shared_recycle(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	app := mkAppRow(t, db, "rc_a", "ps_1")
	_ = s.ClaimSharedToken(ctx, app, "ps_1", "redis", "svinst-redis-shared-28", "4", "REDIS_ADDR")
	if toks, _ := s.AllocatedTokens(ctx, "svinst-redis-shared-28"); !contains(toks, "4") {
		t.Fatalf("应含 4，得 %v", toks)
	}
	if _, err := db.Exec(`DELETE FROM appdeploy_service_binding WHERE app_id=$1`, app); err != nil {
		t.Fatalf("delete binding: %v", err)
	}
	if toks, _ := s.AllocatedTokens(ctx, "svinst-redis-shared-28"); contains(toks, "4") {
		t.Fatalf("删 binding 后 4 应回收，得 %v", toks)
	}
}

// TestMigration_000030_containerNameColumn 迁移后：service_instance 有 container_name 列；
// 既有 bind_existing/shared 种子行该列为 NULL → LookupBindExisting 取回 ContainerName==""（instCols 回归）。
func TestMigration_000030_containerNameColumn(t *testing.T) {
	s, db := newTestStore(t)
	// 列存在
	var hasCol bool
	if err := db.Get(&hasCol, `SELECT EXISTS(SELECT 1 FROM information_schema.columns
		WHERE table_name='appdeploy_service_instance' AND column_name='container_name')`); err != nil {
		t.Fatalf("查列: %v", err)
	}
	if !hasCol {
		t.Fatal("container_name 列应存在（迁移 000030）")
	}
	// instCols 含 container_name：LookupBindExisting 取回的种子行 ContainerName 为空（NULL→COALESCE '')
	got, err := s.LookupBindExisting(context.Background(), "ps_1", "redis")
	if err != nil || got == nil {
		t.Fatalf("应命中 redis 种子，err=%v got=%+v", err, got)
	}
	if got.ContainerName != "" {
		t.Fatalf("bind_existing 种子 ContainerName 应空，得 %q", got.ContainerName)
	}
}

// TestStore_CreateGetInstance dedicated 实例行落库 + 取回（含 container_name）。
func TestStore_CreateGetInstance(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	inst := &ServiceInstance{
		ID: "svinst-redis-ded-test1", Kind: "redis", Name: "mwredis-test1",
		SupplyMode: ModeDedicated, Host: "10.10.0.28", Port: 9600,
		AuthRef: "pwd123", ContainerName: "mwredis-test1", Status: "active",
	}
	if err := s.CreateInstance(ctx, inst); err != nil {
		t.Fatalf("CreateInstance: %v", err)
	}
	got, err := s.GetInstance(ctx, inst.ID)
	if err != nil || got == nil {
		t.Fatalf("GetInstance 应取回，err=%v got=%+v", err, got)
	}
	if got.ContainerName != "mwredis-test1" || got.Port != 9600 || got.AuthRef != "pwd123" || got.SupplyMode != ModeDedicated {
		t.Fatalf("dedicated 实例行不符: %+v", got)
	}
	// 无 → nil,nil
	gotNil, err := s.GetInstance(ctx, "nope")
	if err != nil || gotNil != nil {
		t.Fatalf("无实例应 nil,nil，得 %+v err=%v", gotNil, err)
	}
}

// TestStore_DeleteInstance 删实例行。
func TestStore_DeleteInstance(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	inst := &ServiceInstance{
		ID: "svinst-redis-ded-test2", Kind: "redis", Name: "mwredis-test2",
		SupplyMode: ModeDedicated, Host: "h", Port: 9601, ContainerName: "mwredis-test2", Status: "active",
	}
	_ = s.CreateInstance(ctx, inst)
	if err := s.DeleteInstance(ctx, inst.ID); err != nil {
		t.Fatalf("DeleteInstance: %v", err)
	}
	got, _ := s.GetInstance(ctx, inst.ID)
	if got != nil {
		t.Fatalf("删后应取不到，得 %+v", got)
	}
}

// TestStore_CreateInstance_idempotent 同 id 再 Create 不报错（DO NOTHING）。
func TestStore_CreateInstance_idempotent(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	inst := &ServiceInstance{ID: "svinst-ded-idem", Kind: "redis", Name: "n", SupplyMode: ModeDedicated, Host: "h", Port: 9602, ContainerName: "n", Status: "active"}
	if err := s.CreateInstance(ctx, inst); err != nil {
		t.Fatalf("首次 Create: %v", err)
	}
	if err := s.CreateInstance(ctx, inst); err != nil {
		t.Fatalf("二次 Create 应幂等不报错: %v", err)
	}
}

// TestMigration_000033_sharedMilvusSeed 迁移后：shared milvus 种子在（isolation mode=prefix）。
func TestMigration_000033_sharedMilvusSeed(t *testing.T) {
	_, db := newTestStore(t)
	var mode, supplyMode string
	var port int
	err := db.QueryRow(`SELECT isolation->>'mode', supply_mode, port
		FROM appdeploy_service_instance
		WHERE id='svinst-milvus-shared-28' AND kind='milvus' AND project_space_id IS NULL`).
		Scan(&mode, &supplyMode, &port)
	if err != nil {
		t.Fatalf("shared milvus 种子缺失: %v", err)
	}
	if mode != "prefix" || supplyMode != "shared" || port != 19530 {
		t.Fatalf("shared milvus 种子不符: mode=%s supply_mode=%s port=%d", mode, supplyMode, port)
	}
}

// TestStore_DeclareIfAbsent_absent 无 binding → 种 declared；已有 → 不覆盖。
func TestStore_DeclareIfAbsent(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	app := mkAppRow(t, db, "dia_a", "ps_1")

	if err := s.DeclareIfAbsent(ctx, app, "ps_1", "redis", ModeShared); err != nil {
		t.Fatalf("DeclareIfAbsent: %v", err)
	}
	b, _ := s.GetBinding(ctx, app, "redis")
	if b == nil || b.Status != StatusDeclared || b.Strategy != ModeShared || b.EnvKey != "REDIS_ADDR" {
		t.Fatalf("应种 declared/shared/REDIS_ADDR，得 %+v", b)
	}
	// 已存在（用户 UI 改成 dedicated+bound）→ DeclareIfAbsent 不覆盖
	if err := s.UpsertBinding(ctx, &ServiceBinding{AppID: app, ProjectSpaceID: "ps_1",
		ServiceKind: "redis", Strategy: ModeDedicated, EnvKey: "REDIS_ADDR", Status: StatusBound}); err != nil {
		t.Fatalf("upsert bound: %v", err)
	}
	if err := s.DeclareIfAbsent(ctx, app, "ps_1", "redis", ModeShared); err != nil {
		t.Fatalf("DeclareIfAbsent 二次: %v", err)
	}
	b2, _ := s.GetBinding(ctx, app, "redis")
	if b2.Strategy != ModeDedicated || b2.Status != StatusBound {
		t.Fatalf("DeclareIfAbsent 不应覆盖已存在声明，得 %+v", b2)
	}
}

// TestStore_DeclareBinding_upsertDeclared upsert declared，重置 instance/token；幂等。
func TestStore_DeclareBinding(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()
	app := mkAppRow(t, db, "dcb_a", "ps_1")

	if err := s.DeclareBinding(ctx, app, "ps_1", "milvus", ModeShared); err != nil {
		t.Fatalf("DeclareBinding: %v", err)
	}
	b, _ := s.GetBinding(ctx, app, "milvus")
	if b == nil || b.Status != StatusDeclared || b.Strategy != ModeShared ||
		b.EnvKey != "MILVUS_ADDR" || b.ServiceInstanceID != "" || b.IsolationToken != "" {
		t.Fatalf("应 declared/shared/MILVUS_ADDR + 空结果字段，得 %+v", b)
	}
	// 模拟已供给（bound + instance + token）后重新声明 → 重置回 declared、清结果
	if err := s.UpsertBinding(ctx, &ServiceBinding{AppID: app, ProjectSpaceID: "ps_1",
		ServiceKind: "milvus", Strategy: ModeShared, ServiceInstanceID: "svinst-milvus-shared-28",
		IsolationToken: "appabc123def_", EnvKey: "MILVUS_ADDR", Status: StatusBound}); err != nil {
		t.Fatalf("upsert bound: %v", err)
	}
	if err := s.DeclareBinding(ctx, app, "ps_1", "milvus", ModeDedicated); err != nil {
		t.Fatalf("DeclareBinding 改策略: %v", err)
	}
	b2, _ := s.GetBinding(ctx, app, "milvus")
	if b2.Status != StatusDeclared || b2.Strategy != ModeDedicated ||
		b2.ServiceInstanceID != "" || b2.IsolationToken != "" {
		t.Fatalf("重新声明应 declared+新策略+清结果，得 %+v", b2)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
