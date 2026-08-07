package mwsupply

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const instCols = `id, project_space_id, kind, name, supply_mode, host, port,
 COALESCE(auth_ref,'') AS auth_ref, COALESCE(isolation::text,'') AS isolation,
 COALESCE(container_name,'') AS container_name, status, created_at, updated_at`

const bindCols = `id, app_id, project_space_id, service_kind, strategy,
 COALESCE(service_instance_id,'') AS service_instance_id, COALESCE(isolation_token,'') AS isolation_token,
 env_key, status, COALESCE(last_error,'') AS last_error, created_at, updated_at`

// Store 中间件实例注册表 + 绑定记录数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// LookupBindExisting 取某 kind 的 bind_existing 实例（项目级优先 → 平台级 NULL）。
// 无则返回 nil,nil。
func (s *Store) LookupBindExisting(ctx context.Context, psID, kind string) (*ServiceInstance, error) {
	var inst ServiceInstance
	err := s.db.GetContext(ctx, &inst,
		`SELECT `+instCols+` FROM appdeploy_service_instance
		 WHERE kind=$1 AND supply_mode='bind_existing' AND status='active'
		   AND (project_space_id=$2 OR project_space_id IS NULL)
		 ORDER BY (project_space_id IS NOT NULL) DESC
		 LIMIT 1`, kind, psID)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

// UpsertBinding 新增或更新绑定（按 app_id+service_kind 唯一）。
func (s *Store) UpsertBinding(ctx context.Context, b *ServiceBinding) error {
	if b.ID == "" {
		b.ID = "svb_" + uuid.NewString()[:20]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_service_binding (id, app_id, project_space_id, service_kind, strategy, service_instance_id, isolation_token, env_key, status, last_error)
		 VALUES ($1,$2,$3,$4,$5,NULLIF($6,''),NULLIF($7,''),$8,$9,NULLIF($10,''))
		 ON CONFLICT(app_id, service_kind) DO UPDATE SET strategy=excluded.strategy,
		   service_instance_id=excluded.service_instance_id, isolation_token=excluded.isolation_token,
		   env_key=excluded.env_key, status=excluded.status, last_error=excluded.last_error,
		   updated_at=CURRENT_TIMESTAMP`,
		b.ID, b.AppID, b.ProjectSpaceID, b.ServiceKind, b.Strategy, b.ServiceInstanceID, b.IsolationToken, b.EnvKey, b.Status, b.LastError)
	return err
}

// ListBindingsByApp 列应用全部绑定。
func (s *Store) ListBindingsByApp(ctx context.Context, appID string) ([]ServiceBinding, error) {
	var list []ServiceBinding
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+bindCols+` FROM appdeploy_service_binding WHERE app_id=$1 ORDER BY service_kind`, appID)
	return list, err
}

// LookupShared 取某 kind 的平台级 shared 实例（project_space_id IS NULL）。无则 nil,nil。
func (s *Store) LookupShared(ctx context.Context, kind string) (*ServiceInstance, error) {
	var inst ServiceInstance
	err := s.db.GetContext(ctx, &inst,
		`SELECT `+instCols+` FROM appdeploy_service_instance
		 WHERE kind=$1 AND supply_mode='shared' AND status='active' AND project_space_id IS NULL
		 LIMIT 1`, kind)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

// GetBinding 取某 app 某 kind 的绑定。无则 nil,nil。
func (s *Store) GetBinding(ctx context.Context, appID, kind string) (*ServiceBinding, error) {
	var b ServiceBinding
	err := s.db.GetContext(ctx, &b,
		`SELECT `+bindCols+` FROM appdeploy_service_binding WHERE app_id=$1 AND service_kind=$2`, appID, kind)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &b, nil
}

// AllocatedTokens 列某实例所有已分配 token（isolation_token IS NOT NULL）。
// shared redis db 号分配的占用集来源。
func (s *Store) AllocatedTokens(ctx context.Context, instID string) ([]string, error) {
	var toks []string
	err := s.db.SelectContext(ctx, &toks,
		`SELECT isolation_token FROM appdeploy_service_binding
		 WHERE service_instance_id=$1 AND isolation_token IS NOT NULL`, instID)
	return toks, err
}

// ClaimSharedToken 原子登记 shared 绑定（strategy=shared,status=bound）。
// 复用 UpsertBinding 的 ON CONFLICT(app_id,service_kind)。
// 撞 (service_instance_id,isolation_token) 唯一索引 → DB 抛 23505，调用方 isUniqueViolation 捕获后换号重试。
func (s *Store) ClaimSharedToken(ctx context.Context, appID, psID, kind, instID, token, envKey string) error {
	return s.UpsertBinding(ctx, &ServiceBinding{
		AppID: appID, ProjectSpaceID: psID, ServiceKind: kind,
		Strategy: ModeShared, ServiceInstanceID: instID, IsolationToken: token,
		EnvKey: envKey, Status: StatusBound,
	})
}

// CreateInstance 登记 dedicated 实例行（supply_mode=dedicated，含 container_name）。
// id PK 冲突 → DO NOTHING（幂等）。project_space_id 传 *string（dedicated 实例不挂项目，靠 binding 关联 app）。
func (s *Store) CreateInstance(ctx context.Context, inst *ServiceInstance) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_service_instance
		   (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, isolation, container_name, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,NULLIF($8,''),NULLIF($9,'')::jsonb,NULLIF($10,''),$11)
		 ON CONFLICT (id) DO NOTHING`,
		inst.ID, inst.ProjectSpaceID, inst.Kind, inst.Name, inst.SupplyMode,
		inst.Host, inst.Port, inst.AuthRef, inst.Isolation, inst.ContainerName, inst.Status)
	return err
}

// RegisterBindExisting 注册一个 bind_existing 实例(运维把部署机已有服务登记给 ANP)。
// 幂等:同 kind + project_space_id + host + port 已存在则不重复插(spec ②)。
// project_space_id 传 *string:NULL=平台全局,&psID=项目级。
func (s *Store) RegisterBindExisting(ctx context.Context, inst *ServiceInstance) error {
	var exist string
	_ = s.db.GetContext(ctx, &exist,
		`SELECT id FROM appdeploy_service_instance
		 WHERE kind=$1 AND supply_mode='bind_existing' AND host=$2 AND port=$3
		   AND (project_space_id IS NOT DISTINCT FROM $4)`,
		inst.Kind, inst.Host, inst.Port, inst.ProjectSpaceID)
	if exist != "" {
		return nil
	}
	if inst.ID == "" {
		inst.ID = "svinst-" + uuid.NewString()[:12]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_service_instance
		   (id, project_space_id, kind, name, supply_mode, host, port, auth_ref, status)
		 VALUES ($1,$2,$3,$4,'bind_existing',$5,$6,NULLIF($7,''),'active')
		 ON CONFLICT (id) DO NOTHING`,
		inst.ID, inst.ProjectSpaceID, inst.Kind, inst.Name, inst.Host, inst.Port, inst.AuthRef)
	return err
}

// ListBindExisting 列出某项目空间可见的 bind_existing 实例(项目级 + 平台级 NULL)。
func (s *Store) ListBindExisting(ctx context.Context, psID string) ([]ServiceInstance, error) {
	var out []ServiceInstance
	err := s.db.SelectContext(ctx, &out,
		`SELECT `+instCols+` FROM appdeploy_service_instance
		 WHERE supply_mode='bind_existing' AND status='active'
		   AND (project_space_id=$1 OR project_space_id IS NULL)
		 ORDER BY (project_space_id IS NOT NULL) DESC, kind`, psID)
	return out, err
}

// GetInstance 按 id 取实例。无则 nil,nil。
func (s *Store) GetInstance(ctx context.Context, id string) (*ServiceInstance, error) {
	var inst ServiceInstance
	err := s.db.GetContext(ctx, &inst, `SELECT `+instCols+` FROM appdeploy_service_instance WHERE id=$1`, id)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, err
	}
	return &inst, nil
}

// DeleteInstance 按 id 删实例行（binding/env 由 FK CASCADE 兜底；dedicated 容器由调用方先 docker rm）。
func (s *Store) DeleteInstance(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_service_instance WHERE id=$1`, id)
	return err
}

// DeleteBinding 按 id 删绑定行。dedicated Cleanup 时先删 binding 解 FK 引用
// （binding.service_instance_id REFERENCES instance(id) 默认 RESTRICT，binding 在则 instance 删不掉）。
func (s *Store) DeleteBinding(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_service_binding WHERE id=$1`, id)
	return err
}

// DeclareIfAbsent 种声明（仅当该 (app,kind) 无 binding 时）；ON CONFLICT DO NOTHING。
// 导入种子用：不覆盖用户已 UI 设的声明。
func (s *Store) DeclareIfAbsent(ctx context.Context, appID, psID, kind, strategy string) error {
	if strategy == "" {
		strategy = ModeBindExisting
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_service_binding (id, app_id, project_space_id, service_kind, strategy, env_key, status)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)
		 ON CONFLICT (app_id, service_kind) DO NOTHING`,
		"svb_"+uuid.NewString()[:20], appID, psID, kind, strategy, EnvKeyFor(kind), StatusDeclared)
	return err
}

// ListActiveInstances 列某 ps 可见的 active 实例（项目级 + 平台级 NULL）。
func (s *Store) ListActiveInstances(ctx context.Context, psID string) ([]ServiceInstance, error) {
	var list []ServiceInstance
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+instCols+` FROM appdeploy_service_instance
		 WHERE status='active' AND (project_space_id=$1 OR project_space_id IS NULL)
		 ORDER BY kind, supply_mode`, psID)
	return list, err
}

// DeclareBinding upsert 声明（status=declared，重置 instance/token/env_key）。
// PutDeps 的 added/changed 用：结果字段清空，待下次部署供给。
func (s *Store) DeclareBinding(ctx context.Context, appID, psID, kind, strategy string) error {
	if strategy == "" {
		strategy = ModeBindExisting
	}
	return s.UpsertBinding(ctx, &ServiceBinding{
		AppID: appID, ProjectSpaceID: psID, ServiceKind: kind,
		Strategy: strategy, EnvKey: EnvKeyFor(kind), Status: StatusDeclared,
	})
}
