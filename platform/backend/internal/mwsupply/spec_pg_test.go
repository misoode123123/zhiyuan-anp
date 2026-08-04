package mwsupply

import "testing"

func TestPgSpec_Fields(t *testing.T) {
	s := pgSpec(nil) // provisioner 传 nil（本测只验静态字段）
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
	// Token 语义常量（TokenDatabaseName，非裸字面量）
	if s.Token != TokenDatabaseName {
		t.Fatalf("pg Token 应为 TokenDatabaseName，得 %q", s.Token)
	}
}
