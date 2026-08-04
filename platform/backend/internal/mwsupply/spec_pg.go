package mwsupply

import (
	"context"

	"zhiyuan-anp/platform/backend/internal/pgsupply"
)

// pgSpec 构造 pg 的 KindSpec（P2a：仅 shared；bind_existing/dedicated 留 P2b）。
// shared 走自管路径 SupplyShared：调 pgsupply.Provisioner.Provision（全套：项目空间 PG 实例 + 建库/role
// + 写 DATABASE_URL env + 写 appdeploy_database），返回 (instanceID="", token=库名)。
// binding 不存 service_instance_id（FK→service_instance，而 pg 实例在 pg_instance 表）；明细在 appdeploy_database。
// pg 自管 env（pgsupply.Provision 写 DATABASE_URL）：不设 SharedEnv/DedicatedEnv/AllocSharedToken。
func pgSpec(prov *pgsupply.Provisioner) KindSpec {
	return KindSpec{
		Kind: "pg", DisplayName: "PostgreSQL", AddrEnv: "DATABASE_URL", Token: TokenDatabaseName,
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			appDB, err := prov.Provision(ctx, psID, appID)
			if err != nil {
				return "", "", err
			}
			return "", appDB.DBName, nil // instanceID 空（pg binding 不存）；token=库名
		},
	}
}
