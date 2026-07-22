package pgsupply

import (
	"context"
	"fmt"
)

// QuotaChecker 配额检查接口（由 quota.Service 实现）。
// 仅依赖最小接口避免循环依赖：pgsupply 不导入 quota 包。
// 任意一项失败（库数超限 / 库大小超限）返回非 nil 错误，调用方决定如何反馈。
type QuotaChecker interface {
	CheckDatabases(ctx context.Context, psID string) error
	CheckDBSize(ctx context.Context, psID string) error
}

// EnvWriter 写应用环境变量（由 appdeploy.Store 实现，避免 pgsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool) error
}

// InstanceLookup 取/建项目 PG 实例（InstanceManager 实现）。
type InstanceLookup interface {
	GetOrCreate(ctx context.Context, psID string) (*PGInstance, error)
}

// Provisioner 为应用供给库：取项目实例 → 建库+role → 写库记录 + DATABASE_URL env。
type Provisioner struct {
	instances InstanceLookup
	store     *Store
	admin     PGAdmin
	env       EnvWriter
	quota     QuotaChecker // 可选；nil=不强制配额（开发环境/测试）
}

// NewProvisioner 构造。env 传 appdeploy.Store（满足 EnvWriter）。quota 可为 nil。
func NewProvisioner(instances InstanceLookup, store *Store, admin PGAdmin, env EnvWriter, quota QuotaChecker) *Provisioner {
	return &Provisioner{instances: instances, store: store, admin: admin, env: env, quota: quota}
}

// Provision 为应用建独立库 + 注入 DATABASE_URL。失败回滚已建的库/role。
// 配额超限（库数 / 库大小）→ 在最前面拦（不建 appdb 记录），返回 QuotaChecker 的错误。
func (p *Provisioner) Provision(ctx context.Context, psID, appID string) (*AppDatabase, error) {
	// 配额强制：建任何记录/容器前先查（超限不污染数据，便于前端精准提示）
	if p.quota != nil {
		if err := p.quota.CheckDatabases(ctx, psID); err != nil {
			return nil, err
		}
		if err := p.quota.CheckDBSize(ctx, psID); err != nil {
			return nil, err
		}
	}
	ins, err := p.instances.GetOrCreate(ctx, psID)
	if err != nil {
		return nil, fmt.Errorf("取/建项目 PG 实例: %w", err)
	}
	dbName := DBName(appID)
	role := RoleName(dbName)
	pwd := genPassword()

	ad := &AppDatabase{
		ID: "apdb_" + genShortID(), AppID: appID, ProjectSpaceID: psID,
		DBName: dbName, DBRole: role, PGInstanceID: ins.ID,
		DBHost: ins.Host, DBPort: ins.Port, Status: StatusProvisioning, BackupEnabled: true,
	}
	if err := p.store.CreateAppDB(ctx, ad); err != nil {
		return nil, fmt.Errorf("登记库记录: %w", err)
	}

	// 建库 + role + 授权（失败回滚）
	if err := p.admin.CreateDatabase(ctx, ins.AdminURLRef, dbName); err != nil {
		p.markFailed(ctx, ad, err)
		return nil, err
	}
	if err := p.admin.CreateRole(ctx, ins.AdminURLRef, role, pwd); err != nil {
		_ = p.admin.DropDatabase(ctx, ins.AdminURLRef, dbName)
		p.markFailed(ctx, ad, err)
		return nil, err
	}
	if err := p.admin.GrantAll(ctx, ins.AdminURLRef, dbName, role); err != nil {
		_ = p.admin.DropDatabase(ctx, ins.AdminURLRef, dbName)
		_ = p.admin.DropRole(ctx, ins.AdminURLRef, role)
		p.markFailed(ctx, ad, err)
		return nil, err
	}

	dsn := DSN(ins.Host, ins.Port, role, pwd, dbName)
	if err := p.env.UpsertEnv(ctx, appID, "DATABASE_URL", dsn, true); err != nil {
		_ = p.admin.DropDatabase(ctx, ins.AdminURLRef, dbName)
		_ = p.admin.DropRole(ctx, ins.AdminURLRef, role)
		p.markFailed(ctx, ad, err)
		return nil, fmt.Errorf("写 DATABASE_URL env: %w", err)
	}

	_ = p.store.SetAppDBStatus(ctx, ad.ID, StatusReady, "")
	ad.Status = StatusReady
	return ad, nil
}

// Cleanup 删库 + role（保留 PG 实例，项目可能还有其他应用）。
func (p *Provisioner) Cleanup(ctx context.Context, appID string) error {
	ad, err := p.store.GetAppDBByApp(ctx, appID)
	if err != nil || ad == nil {
		return nil // 无库记录，跳过
	}
	if ins, e := p.store.GetInstance(ctx, ad.PGInstanceID); e == nil && ins != nil {
		_ = p.admin.DropDatabase(ctx, ins.AdminURLRef, ad.DBName)
		_ = p.admin.DropRole(ctx, ins.AdminURLRef, ad.DBRole)
	}
	return p.store.DeleteAppDB(ctx, appID)
}

// markFailed 记录失败原因（不改 DB 状态语义，仅便于排障）。
func (p *Provisioner) markFailed(ctx context.Context, ad *AppDatabase, cause error) {
	_ = p.store.SetAppDBStatus(ctx, ad.ID, StatusFailed, cause.Error())
}
