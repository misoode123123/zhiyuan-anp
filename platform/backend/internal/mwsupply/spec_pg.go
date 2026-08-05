package mwsupply

import (
	"context"

	"zhiyuan-anp/platform/backend/internal/pgsupply"
)

// PgDedicatedRunner 起 per-app 独立 PG 容器 + 建库/role（pg dedicated 专用）。
// 由 *pgsupply.InstanceManager 实现（持 docker+admin+host）。抽接口便于 mwsupply 单测用 fake
// （InstanceManager 需 docker 无法单测）。返回 container/dbName/dsn/adminURL/port（不写 env、不登记实例）。
type PgDedicatedRunner interface {
	// ProvisionDedicated 起 per-app 独立 PG 容器 + 建库/role，返回 container/dbName/dsn/adminURL/port。
	ProvisionDedicated(ctx context.Context, appID, psID string) (container, dbName, dsn, adminURL string, port int, err error)
	// CleanupDedicated docker rm per-app 独立容器。
	CleanupDedicated(ctx context.Context, container string) error
}

// pgSpec 构造 pg 的 KindSpec（P2a shared + P2b bind_existing + P2b T3 dedicated）。
// shared 走自管路径 SupplyShared：调 pgsupply.Provisioner.Provision（全套：项目空间 PG 实例 + 建库/role
// + 写 DATABASE_URL env + 写 appdeploy_database），返回 (instanceID="", token=库名)。
// bind_existing 走默认路径：LookupBindExisting → ConnValue=inst.AuthRef（运维登记的完整 DSN）→ UpsertEnv DATABASE_URL。
// dedicated 走自管路径 SupplyDedicated：调 ded.ProvisionDedicated（起 per-app 容器+建库/role）→
// 写 DATABASE_URL env(dsn) → 登记 service_instance(kind=pg, dedicated, container_name, host, port, auth_ref=adminURL)
// → 返回 (instID, token=dbName)。
// pg 自管 env（shared 由 pgsupply.Provision 写；dedicated/bind_existing 由闭包/主路径写）：
// 不设 SharedEnv/DedicatedEnv/AllocSharedToken。
func pgSpec(prov *pgsupply.Provisioner, ded PgDedicatedRunner, store *Store, env EnvWriter) KindSpec {
	return KindSpec{
		Kind: "pg", DisplayName: "PostgreSQL", AddrEnv: "DATABASE_URL", Token: TokenDatabaseName,
		// bind_existing 注入运维登记的完整 DSN（service_instance.auth_ref），不建库/role —— 用现成 PG+库+凭据。
		ConnValue: func(inst *ServiceInstance) string { return inst.AuthRef },
		SupplyShared: func(ctx context.Context, appID, psID string) (string, string, error) {
			appDB, err := prov.Provision(ctx, psID, appID)
			if err != nil {
				return "", "", err
			}
			return "", appDB.DBName, nil // instanceID 空（pg binding 不存）；token=库名
		},
		// dedicated：起 per-app 独立 PG 容器 + 建库/role → 写 DATABASE_URL → 登记 service_instance。
		// host 由 supplyDedicated 闭包传入（=r.host）；ProvisionDedicated 内部用 InstanceManager.host 起容器，二者同值。
		SupplyDedicated: func(ctx context.Context, appID, psID, host string) (string, string, error) {
			container, dbName, dsn, adminURL, port, err := ded.ProvisionDedicated(ctx, appID, psID)
			if err != nil {
				return "", "", err
			}
			_ = env.UpsertEnv(ctx, appID, "DATABASE_URL", dsn, true, "platform")
			inst := &ServiceInstance{
				ID:            "svinst-pg-ded-" + genShortID(),
				Kind:          "pg",
				Name:          container,
				SupplyMode:    ModeDedicated,
				Host:          host,
				Port:          port,
				AuthRef:       adminURL,
				ContainerName: container,
				Status:        "active",
			}
			if err := store.CreateInstance(ctx, inst); err != nil {
				// 登记失败回收容器（best-effort），返回错让 binding failed。
				_ = ded.CleanupDedicated(ctx, container)
				return "", "", err
			}
			return inst.ID, dbName, nil
		},
		CleanupDedicated: func(ctx context.Context, name string) error {
			return ded.CleanupDedicated(ctx, name)
		},
	}
}
