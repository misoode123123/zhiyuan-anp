package pgsupply

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

// InstanceManager 每个项目空间取/建一个独立 PG 实例（managed: docker 起容器）。
type InstanceManager struct {
	store  *Store
	docker DockerRunner
	admin  PGAdmin
	host   string // 宿主对外地址（AppDeployHost）
}

// NewInstanceManager 构造。
func NewInstanceManager(store *Store, docker DockerRunner, admin PGAdmin, host string) *InstanceManager {
	return &InstanceManager{store: store, docker: docker, admin: admin, host: host}
}

// GetOrCreate 项目有 active 实例则复用；无（sql.ErrNoRows）则 managed 起容器；DB 错误则返回。
func (m *InstanceManager) GetOrCreate(ctx context.Context, psID string) (*PGInstance, error) {
	ins, err := m.store.GetInstanceByProject(ctx, psID)
	if err == nil && ins != nil {
		return ins, nil
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("查项目 PG 实例: %w", err)
	}
	return m.provision(ctx, psID)
}

// provision 起新 PG 容器并登记。
//
// 并发兜底（I5）：同项目并发建应用时，两 goroutine 都跑过 GetOrCreate 的「查无」分支，
// 双方各起一个 PG 容器，CreateInstance 时 partial unique index（迁移 000005）让后到者冲突。
// 本方法捕获 unique_violation → 清理自己刚起的容器 → 重查复用先到者建的实例；
// 重查仍无（理论 race：先到者已删）→ 返回原冲突错误让上层决策。
func (m *InstanceManager) provision(ctx context.Context, psID string) (*PGInstance, error) {
	name := InstanceName(psID) + "-" + genShortID()
	pwd := genPassword()
	port := allocPort(m.docker.UsedPorts(ctx), pgPortMin, pgPortMax)
	if port == 0 {
		return nil, fmt.Errorf("无可用 PG 端口（%d-%d 已满）", pgPortMin, pgPortMax)
	}
	if err := m.docker.RunPGContainer(ctx, name, pwd, port); err != nil {
		return nil, fmt.Errorf("起 PG 容器: %w", err)
	}
	adminURL := DSN(m.host, port, "postgres", pwd, "postgres")
	if err := waitForReady(ctx, m.admin, adminURL); err != nil {
		_ = m.docker.RmForce(ctx, name) // 清理半成品容器
		return nil, fmt.Errorf("PG 未就绪: %w", err)
	}
	ins := &PGInstance{
		ID:             "pgi_" + genShortID(),
		ProjectSpaceID: psID,
		Host:           m.host,
		Port:           port,
		AdminURLRef:    adminURL,
		DeployMode:     DeployManaged,
		Status:         StatusActive,
		ContainerName:  name,
	}
	if err := m.store.CreateInstance(ctx, ins); err != nil {
		_ = m.docker.RmForce(ctx, name) // 登记失败/冲突，回收自己的容器
		// 并发兜底：partial unique 冲突 → 另一 goroutine 已建 active 实例 → 重查复用
		if isUniqueViolation(err) {
			if existing, e := m.store.GetInstanceByProject(ctx, psID); e == nil && existing != nil {
				return existing, nil
			}
		}
		return nil, fmt.Errorf("登记实例: %w", err)
	}
	return ins, nil
}

// ProvisionDedicated 起 per-app 独立 PG 容器 + 建库/role，返回 container/dbName/dsn/adminURL/host/port。
//
// 与 provision()（per-project）的区别：本方法建 per-app 容器，**不**登记 pg_instance 表
// （避免与 per-project partial unique index 交互），也**不**写 env、**不**登记 service_instance
// （由 mwsupply 闭包做：写 DATABASE_URL + 登记 service_instance 带 container_name 供清理）。
// 复用 InstanceManager 已有的 docker（RunPGContainer/RmForce）+ admin（CreateDatabase/Role/GrantAll）
// + waitForReady，端口段同 per-project（9500-9599）。
//
// 返回的 host = m.host（实际起容器用的 AppDeployHost），供 mwsupply 登记到 service_instance.Host
// 作为单一来源（消除 r.host 与 m.host 双 host 耦合）。
//
// 失败回滚：RunPGContainer 成功后任何失败（waitForReady/建库/建 role/授权）都 RmForce 容器后返错 ——
// dedicated 容器无 pg_instance 记录（mwsupply 出错时不登记 service_instance），若不回收则泄漏宿主端口+内存。
func (m *InstanceManager) ProvisionDedicated(ctx context.Context, appID, psID string) (container, dbName, dsn, adminURL, host string, port int, err error) {
	container = InstanceName(psID) + "-ded-" + genShortID()
	pwd := genPassword()
	port = allocPort(m.docker.UsedPorts(ctx), pgPortMin, pgPortMax)
	if port == 0 {
		return "", "", "", "", "", 0, fmt.Errorf("无可用 PG 端口（%d-%d 已满）", pgPortMin, pgPortMax)
	}
	if err := m.docker.RunPGContainer(ctx, container, pwd, port); err != nil {
		return "", "", "", "", "", 0, fmt.Errorf("起 PG 容器: %w", err)
	}
	adminURL = DSN(m.host, port, "postgres", pwd, "postgres")
	if err := waitForReady(ctx, m.admin, adminURL); err != nil {
		_ = m.docker.RmForce(ctx, container) // 清理半成品容器
		return "", "", "", "", "", 0, fmt.Errorf("PG 未就绪: %w", err)
	}
	dbName = DBName(appID)
	role := RoleName(dbName)
	if err := m.admin.CreateDatabase(ctx, adminURL, dbName); err != nil {
		_ = m.docker.RmForce(ctx, container) // 建库失败回收容器（无实例记录，不回收则泄漏）
		return "", "", "", "", "", 0, fmt.Errorf("建库 %s: %w", dbName, err)
	}
	if err := m.admin.CreateRole(ctx, adminURL, role, pwd); err != nil {
		_ = m.admin.DropDatabase(ctx, adminURL, dbName)
		_ = m.docker.RmForce(ctx, container)
		return "", "", "", "", "", 0, fmt.Errorf("建 role %s: %w", role, err)
	}
	if err := m.admin.GrantAll(ctx, adminURL, dbName, role); err != nil {
		_ = m.admin.DropDatabase(ctx, adminURL, dbName)
		_ = m.admin.DropRole(ctx, adminURL, role)
		_ = m.docker.RmForce(ctx, container)
		return "", "", "", "", "", 0, fmt.Errorf("授权 %s/%s: %w", dbName, role, err)
	}
	dsn = DSN(m.host, port, role, pwd, dbName)
	host = m.host
	return container, dbName, dsn, adminURL, host, port, nil
}

// CleanupDedicated docker rm per-app 独立容器（mwsupply Cleanup/ReleaseDedicated 调）。
// 与 per-project TeardownForProject 区别：per-app 容器无 pg_instance 记录，仅 docker rm。
func (m *InstanceManager) CleanupDedicated(ctx context.Context, container string) error {
	return m.docker.RmForce(ctx, container)
}

// isUniqueViolation 判断是否 PG 唯一约束冲突（错误码 23505）。
// 用于 provision 并发兜底：partial unique index 命中时重查复用先到者建的实例。
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "23505"
	}
	return false
}

// waitForReady 轮询 Ping 直到成功或 ctx 取消（由调用方传超时 ctx 控制时长）。
func waitForReady(ctx context.Context, admin PGAdmin, adminURL string) error {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	// 首次立即试
	if err := admin.Ping(ctx, adminURL); err == nil {
		return nil
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := admin.Ping(ctx, adminURL); err == nil {
				return nil
			}
		}
	}
}

// TeardownResult TeardownForProject 的累计结果（删项目级联清理用）。
type TeardownResult struct {
	Total       int      `json:"total"`         // 项目下 PG 实例数
	Removed     int      `json:"removed"`       // 成功 docker rm 的容器数
	NoContainer int      `json:"no_container"`  // 无 container_name（external 或老实例），仅删记录
	Failed      int      `json:"failed"`        // 容器清理失败数
	FailedIDs   []string `json:"failed_ids,omitempty"`
}

// TeardownForProject 删项目级联清理 PG 容器（I2 资源泄漏修复）。
//
// 场景：删 project_space 时，FK ON DELETE CASCADE 只删 pg_instance/appdeploy_database 记录，
// 运行中的 PG 容器仍占着端口/内存（资源泄漏）。本方法在删 project_space 前调用，
// 遍历该项目所有实例：managed+有 container_name → docker rm -f；external/无容器名 → 仅删记录。
// 失败不中断（个别实例清理失败不阻塞删项目，failed 字段记录便于排障）。
func (m *InstanceManager) TeardownForProject(ctx context.Context, psID string) TeardownResult {
	list, err := m.store.ListInstancesByProject(ctx, psID)
	if err != nil {
		return TeardownResult{}
	}
	r := TeardownResult{Total: len(list)}
	for _, ins := range list {
		if ins.ContainerName == "" {
			// external 模式或迁移 000005 前的老实例（无 container_name）：容器非平台纳管，仅删记录
			r.NoContainer++
			_ = m.store.DeleteInstance(ctx, ins.ID)
			continue
		}
		if err := m.docker.RmForce(ctx, ins.ContainerName); err != nil {
			r.Failed++
			r.FailedIDs = append(r.FailedIDs, ins.ID)
			continue // 容器没清掉保留记录，便于人工排查
		}
		// 容器清掉后再删实例记录（appdeploy_database 由 FK CASCADE）
		_ = m.store.DeleteInstance(ctx, ins.ID)
		r.Removed++
	}
	return r
}
