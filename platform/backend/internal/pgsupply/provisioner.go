package pgsupply

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
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
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
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
	// 幂等：同 app 已有 Ready 库 → 复用，不重建。
	// 只复用 Ready（保证 DATABASE_URL env 已写）；Provisioning/Failed 不早返回，落续供路径推进到 Ready
	// （修 P2a #2：Provisioning 行 env 未写，早返回会造成 binding bound 但无 DATABASE_URL 的 lag）。
	if existing, err := p.store.GetAppDBByApp(ctx, appID); err == nil && existing != nil && existing.Status == StatusReady {
		return existing, nil
	}
	ins, err := p.instances.GetOrCreate(ctx, psID)
	if err != nil {
		return nil, fmt.Errorf("取/建项目 PG 实例: %w", err)
	}
	dbName := DBName(appID)
	role := RoleName(dbName)
	pwd := genPassword()

	var ad *AppDatabase
	if existing, err := p.store.GetAppDBByApp(ctx, appID); err == nil && existing != nil {
		// 续供：复用既有 Provisioning/Failed 行（不新建，避免 UNIQUE 冲突），推进到 Ready。
		ad = existing
		_ = p.store.SetAppDBStatus(ctx, ad.ID, StatusProvisioning, "")
		ad.Status = StatusProvisioning
		// 复用既有 DBName/DBRole：库早按此名建过（续供 CreateDatabase 撞 already-exists 被吞），
		// 重建 role/DSN 也用此名，避免新建另一个库名造成孤儿库 + DSN 与行记录不一致。
		dbName = ad.DBName
		role = ad.DBRole
	} else {
		ad = &AppDatabase{
			ID: "apdb_" + genShortID(), AppID: appID, ProjectSpaceID: psID,
			DBName: dbName, DBRole: role, PGInstanceID: ins.ID,
			DBHost: ins.Host, DBPort: ins.Port, Status: StatusProvisioning, BackupEnabled: true,
		}
		if err := p.store.CreateAppDB(ctx, ad); err != nil {
			return nil, fmt.Errorf("登记库记录: %w", err)
		}
	}

	// 建库（续供时库可能已存在 → 吞 already-exists；其余失败回滚）。
	if err := p.admin.CreateDatabase(ctx, ins.AdminURLRef, dbName); err != nil {
		if !isDuplicateDB(err) {
			p.markFailed(ctx, ad, err)
			return nil, err
		}
	}
	// role：Drop(IF EXISTS) + Create。续供时 role 可能半建 → 重建以新密码；全新则 Drop 为 no-op。
	_ = p.admin.DropRole(ctx, ins.AdminURLRef, role)
	if err := p.admin.CreateRole(ctx, ins.AdminURLRef, role, pwd); err != nil {
		p.markFailed(ctx, ad, err)
		return nil, err
	}
	if err := p.admin.GrantAll(ctx, ins.AdminURLRef, dbName, role); err != nil {
		_ = p.admin.DropRole(ctx, ins.AdminURLRef, role)
		p.markFailed(ctx, ad, err)
		return nil, err
	}

	dsn := DSN(ins.Host, ins.Port, role, pwd, dbName)
	if err := p.env.UpsertEnv(ctx, appID, "DATABASE_URL", dsn, true, "platform"); err != nil {
		_ = p.admin.DropRole(ctx, ins.AdminURLRef, role)
		p.markFailed(ctx, ad, err)
		return nil, fmt.Errorf("写 DATABASE_URL env: %w", err)
	}

	_ = p.store.SetAppDBStatus(ctx, ad.ID, StatusReady, "")
	ad.Status = StatusReady
	return ad, nil
}

// isDuplicateDB 判 CreateDatabase 错误是否"库已存在"（续供容忍）。
// PG duplicate_database 错误码 42P04；pgx 字符串化含 "already exists"。
func isDuplicateDB(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P04" {
		return true
	}
	return strings.Contains(err.Error(), "already exists")
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
