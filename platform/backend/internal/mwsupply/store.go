package mwsupply

import (
	"context"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

const instCols = `id, project_space_id, kind, name, supply_mode, host, port,
 COALESCE(auth_ref,'') AS auth_ref, COALESCE(isolation::text,'') AS isolation, status, created_at, updated_at`

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
