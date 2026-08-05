package mwsupply

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
)

func TestPgSpec_Fields(t *testing.T) {
	s := pgSpec(nil, nil, nil, nil) // provisioner/ded/store/env 传 nil（本测只验静态字段）
	if s.Kind != "pg" || s.AddrEnv != "DATABASE_URL" {
		t.Fatalf("pg 基本字段错: %+v", s)
	}
	// pg 自管 env：SharedEnv/DedicatedEnv 应为 nil（DATABASE_URL 由 pgsupply.Provision 写）
	if s.SharedEnv != nil || s.DedicatedEnv != nil {
		t.Fatalf("pg SharedEnv/DedicatedEnv 应 nil（自管 env），得非 nil: SharedEnv=%T DedicatedEnv=%T", s.SharedEnv, s.DedicatedEnv)
	}
	// SupplyShared 必须设（pg 走自管路径）
	if s.SupplyShared == nil {
		t.Fatal("pg spec 必须设 SupplyShared")
	}
	// AllocSharedToken 应为 nil（pg 不走默认 token 路径）
	if s.AllocSharedToken != nil {
		t.Fatal("pg AllocSharedToken 应 nil（走 SupplyShared 自管路径）")
	}
	// ConnValue 必须设（bind_existing 注入 inst.AuthRef=完整 DSN，不走默认 host:port）。
	if s.ConnValue == nil {
		t.Fatal("pg spec 必须设 ConnValue（bind_existing 注入登记的 DSN）")
	}
	// Token 语义常量（TokenDatabaseName，非裸字面量）
	if s.Token != TokenDatabaseName {
		t.Fatalf("pg Token 应为 TokenDatabaseName，得 %q", s.Token)
	}
}

// TestPgSpec_ConnValue_returnsAuthRef 验证 pg ConnValue 闭包返回 inst.AuthRef（登记的完整 DSN），
// 而非 host:port —— bind_existing 据此注入 DATABASE_URL。
func TestPgSpec_ConnValue_returnsAuthRef(t *testing.T) {
	s := pgSpec(nil, nil, nil, nil)
	inst := &ServiceInstance{Host: "10.10.0.28", Port: 5432, AuthRef: "postgres://u:p@h:5432/db"}
	if got := s.ConnValue(inst); got != inst.AuthRef {
		t.Fatalf("ConnValue 应返回 inst.AuthRef %q，得 %q", inst.AuthRef, got)
	}
	if got := s.ConnValue(inst); got == "10.10.0.28:5432" {
		t.Fatal("ConnValue 不应返回 host:port（应返回 AuthRef DSN）")
	}
}

// TestSupplyOne_pgBindExisting_InjectsDSN 验证 pg bind_existing 注入运维登记的完整 DSN，不建库/role。
//
// 登记一个 service_instance(kind=pg, bind_existing, auth_ref="postgres://...") →
// supplyOne bind_existing → DATABASE_URL=该 DSN，binding bound + service_instance_id=该实例 +
// IsolationToken 空，且无 appdeploy_database 行（bind_existing 不建库，用现成 PG+库+凭据）。
//
// 用 newReconcilerTest harness：BuildSpecs 以 nil pgProv 注册 pg（bind_existing 不触 provisioner）。
func TestSupplyOne_pgBindExisting_InjectsDSN(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()

	// 登记一个 pg bind_existing 实例（auth_ref 存完整 DSN，模拟运维把现成 PG+库+凭据登记给 ANP）。
	const dsn = "postgres://opuser:oppwd@10.10.0.28:5432/opdb"
	inst := &ServiceInstance{
		Kind: "pg", Name: "yxt-pg", Host: "10.10.0.28", Port: 5432,
		AuthRef: dsn,
	}
	if err := r.store.RegisterBindExisting(ctx, inst); err != nil {
		t.Fatalf("register bind_existing: %v", err)
	}
	// RegisterBindExisting 幂等：若行已存在（host+port+ps 命中）则不重复插、也不回填 inst.ID。
	// 以 LookupBindExisting 取实际入库实例为准（同 supplyOne 的真源），避免跨用例残留导致的 ID 漂移。
	inst, lookErr := r.store.LookupBindExisting(ctx, "ps_1", "pg")
	if lookErr != nil || inst == nil {
		t.Fatalf("登记后 LookupBindExisting 取不到 pg 实例: %v", lookErr)
	}
	// 清理本用例登记的 pg 实例（newReconcilerTest 不 truncate service_instance，避免跨用例/跨运行残留）。
	instID := inst.ID
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM appdeploy_service_instance WHERE id=$1`, instID) })

	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "pgbeapp", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}

	// 显式 bind_existing 策略（默认 "" 也走 bind_existing）。
	r.supplyOne(ctx, a.ID, "ps_1", DepService{Kind: "pg", Strategy: ModeBindExisting})

	// env DATABASE_URL = 登记的完整 DSN（非 host:port，说明走了 ConnValue=AuthRef 分支）。
	got, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL")
	if got != dsn {
		t.Fatalf("DATABASE_URL 应注入登记的 DSN %q，得 %q", dsn, got)
	}
	if got == "10.10.0.28:5432" {
		t.Fatalf("不应走默认 ConnStr(host:port)，说明 pg ConnValue 未生效")
	}
	// source=platform + secret（auth_ref 非空 → isSecret=true）。
	src, _ := appStore.GetEnvSource(ctx, a.ID, "DATABASE_URL")
	if src != "platform" {
		t.Fatalf("source 应 platform，得 %q", src)
	}
	// binding bound + service_instance_id=登记实例 + token 空。
	b, _ := NewStore(db).GetBinding(ctx, a.ID, "pg")
	if b == nil || b.Status != StatusBound {
		t.Fatalf("pg binding 应 bound，得 %+v", b)
	}
	if b.ServiceInstanceID != inst.ID {
		t.Fatalf("service_instance_id 应登记实例 %q，得 %q", inst.ID, b.ServiceInstanceID)
	}
	if b.IsolationToken != "" {
		t.Fatalf("bind_existing 不应有 isolation token，得 %q", b.IsolationToken)
	}
	// 不建库（无 appdeploy_database 行）—— bind_existing 用现成 PG+库+凭据，无 provisioning。
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM appdeploy_database WHERE app_id=$1`, a.ID); err != nil {
		t.Fatalf("count appdeploy_database: %v", err)
	}
	if n != 0 {
		t.Fatalf("bind_existing 不应建 appdeploy_database 行（用现成库），得 %d", n)
	}
}

// TestSupplyOne_pgBindExisting_EmptyAuthRefFails 验证登记的 pg bind_existing 实例 AuthRef(DSN) 为空时
// 不静默写空 DATABASE_URL，而是 binding failed（M-3 守卫）。通用：任何 kind connVal 空 → failed。
//
// 偏离 brief 逐字代码两处（为保证用例可独立运行 / 不污染共享 .28 库）：
//   - 实例挂项目空间 ps_1（ProjectSpaceID）：与 InjectsDSN 的平台级(NULL) pg 实例区分 RegisterBindExisting
//     幂等键 (kind,host,port,ps)，避免登记被幂等吞掉；LookupBindExisting 项目级优先于平台级，
//     确保取回本例空 AuthRef 实例（不受 InjectsDSN 残留的平台级非空 AuthRef 实例干扰）。
//   - 清理先删引用该实例的 binding（FK RESTRICT）再删实例：InjectsDSN 的 t.Cleanup 仅删实例，
//     会被自身 supplyOne 建的 binding FK 拦截而静默失败（残留根因）；本例修正之以免污染后续运行。
func TestSupplyOne_pgBindExisting_EmptyAuthRefFails(t *testing.T) {
	r, appStore, db, _, _ := newReconcilerTest(t)
	ctx := context.Background()

	// 项目级挂 ps_1（idempotency 键与平台级 InjectsDSN 区分；Lookup 项目级优先）。
	psID := "ps_1"
	inst := &ServiceInstance{Kind: "pg", Name: "pg-noauth", Host: "10.10.0.28", Port: 5432, AuthRef: "", ProjectSpaceID: &psID}
	if err := r.store.RegisterBindExisting(ctx, inst); err != nil {
		t.Fatalf("register: %v", err)
	}
	got, lookErr := r.store.LookupBindExisting(ctx, "ps_1", "pg")
	if lookErr != nil || got == nil {
		t.Fatalf("登记后 LookupBindExisting 取不到 pg 实例: %v", lookErr)
	}
	instID := got.ID
	// FK 安全清理：先删引用该实例的 binding（binding.service_instance_id RESTRICT instance 删除），再删实例。
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM appdeploy_service_binding WHERE service_instance_id=$1`, instID)
		_, _ = db.Exec(`DELETE FROM appdeploy_service_instance WHERE id=$1`, instID)
	})

	a := &appdeploy.Application{ProjectSpaceID: "ps_1", Name: "pgemptyapp", RepoDir: "/x", InternalPort: 8080}
	if err := appStore.Create(ctx, a); err != nil {
		t.Fatalf("create app: %v", err)
	}

	r.supplyOne(ctx, a.ID, "ps_1", DepService{Kind: "pg", Strategy: ModeBindExisting})

	b, _ := NewStore(db).GetBinding(ctx, a.ID, "pg")
	if b == nil || b.Status != StatusFailed {
		t.Fatalf("空 AuthRef 应 binding failed，得 %+v", b)
	}
	if v, _ := appStore.GetEnvValue(ctx, a.ID, "DATABASE_URL"); v != "" {
		t.Fatalf("空 AuthRef 不应写 DATABASE_URL，得 %q", v)
	}
}
