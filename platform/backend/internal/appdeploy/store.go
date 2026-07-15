package appdeploy

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// Store 应用部署数据访问。
type Store struct {
	db *sqlx.DB
}

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

func appCols() string {
	return `id, project_space_id, name, COALESCE(repo_dir,'') AS repo_dir, internal_port, COALESCE(image,'') AS image, COALESCE(container_name,'') AS container_name, host_port, COALESCE(url,'') AS url, version, status, COALESCE(last_error,'') AS last_error, COALESCE(build_log,'') AS build_log, created_at, updated_at`
}

// Create 注册应用（registered 状态）。
func (s *Store) Create(ctx context.Context, a *Application) error {
	a.ID = "app_" + uuid.NewString()[:20]
	if a.Status == "" {
		a.Status = "registered"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_application (id, project_space_id, name, repo_dir, internal_port, status)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		a.ID, a.ProjectSpaceID, a.Name, a.RepoDir, a.InternalPort, a.Status)
	return err
}

// List 列出项目空间下的应用。
func (s *Store) List(ctx context.Context, psID string) ([]Application, error) {
	var list []Application
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+appCols()+` FROM appdeploy_application WHERE project_space_id=? ORDER BY created_at DESC`, psID)
	return list, err
}

// Get 取单条。
func (s *Store) Get(ctx context.Context, psID, id string) (*Application, error) {
	var a Application
	err := s.db.GetContext(ctx, &a, `SELECT `+appCols()+` FROM appdeploy_application WHERE id=? AND project_space_id=?`, id, psID)
	return &a, err
}

// GetByName 按名取（去重/查找）。
func (s *Store) GetByName(ctx context.Context, psID, name string) (*Application, error) {
	var a Application
	err := s.db.GetContext(ctx, &a, `SELECT `+appCols()+` FROM appdeploy_application WHERE project_space_id=? AND name=?`, psID, name)
	return &a, err
}

// GetByAppID 按应用 id 取（跨空间，id 全局唯一）。
func (s *Store) GetByAppID(ctx context.Context, appID string) (*Application, error) {
	var a Application
	err := s.db.GetContext(ctx, &a, `SELECT `+appCols()+` FROM appdeploy_application WHERE id=?`, appID)
	return &a, err
}

// ResolveApp 供需求派发/发布按应用解析其托管仓库路径 + 内部端口。
func (s *Store) ResolveApp(ctx context.Context, appID string) (repoDir string, port int, err error) {
	a, err := s.GetByAppID(ctx, appID)
	if err != nil || a == nil || a.ID == "" {
		return "", 0, fmt.Errorf("应用 %s 不存在", appID)
	}
	return a.RepoDir, a.InternalPort, nil
}

// AppURLByAppID 按应用 id 取其 test 环境 URL（测试中心验最新发布的 test 实例）。
// 无 test 实例时回退 application 表 URL（兼容旧数据）；都没有则报错。
func (s *Store) AppURLByAppID(ctx context.Context, appID string) (string, error) {
	if ins, _ := s.GetInstance(ctx, appID, EnvTest); ins != nil && ins.URL != "" {
		return ins.URL, nil
	}
	a, err := s.GetByAppID(ctx, appID)
	if err != nil || a == nil || a.ID == "" {
		return "", fmt.Errorf("应用 %s 不存在", appID)
	}
	if a.URL == "" {
		return "", fmt.Errorf("应用 %s 尚未部署到 test 环境", appID)
	}
	return a.URL, nil
}

// insCols 实例显式列（可空字段 COALESCE）。
const insCols = `id, app_id, env, COALESCE(image,'') AS image, COALESCE(container_name,'') AS container_name, host_port, COALESCE(url,'') AS url, version, status, COALESCE(last_error,'') AS last_error, COALESCE(build_log,'') AS build_log, created_at, updated_at`

// GetInstance 取某应用某环境实例（不存在返回 nil,nil）。
func (s *Store) GetInstance(ctx context.Context, appID, env string) (*AppInstance, error) {
	var list []AppInstance
	if err := s.db.SelectContext(ctx, &list, `SELECT `+insCols+` FROM appdeploy_instance WHERE app_id=? AND env=?`, appID, env); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return &list[0], nil
}

// GetOrCreateInstance 取或建某环境实例（首次部署到该环境用）。
func (s *Store) GetOrCreateInstance(ctx context.Context, appID, env string) (*AppInstance, error) {
	ins, err := s.GetInstance(ctx, appID, env)
	if err != nil {
		return nil, err
	}
	if ins != nil {
		return ins, nil
	}
	ins = &AppInstance{ID: "ins_" + uuid.NewString()[:20], AppID: appID, Env: env, Status: "registered"}
	_, err = s.db.ExecContext(ctx, `INSERT INTO appdeploy_instance (id, app_id, env, status) VALUES (?, ?, ?, 'registered')`, ins.ID, appID, env)
	return ins, err
}

// UpdateInstance 更新实例部署态字段。
func (s *Store) UpdateInstance(ctx context.Context, ins *AppInstance) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_instance SET image=?, container_name=?, host_port=?, url=?, version=?, status=?, last_error=?, build_log=?, updated_at=CURRENT_TIMESTAMP WHERE app_id=? AND env=?`,
		ins.Image, ins.ContainerName, ins.HostPort, ins.URL, ins.Version, ins.Status, ins.LastError, ins.BuildLog, ins.AppID, ins.Env)
	return err
}

// SetInstanceStatus 更新实例状态 + 错误/日志。
func (s *Store) SetInstanceStatus(ctx context.Context, appID, env, status, lastErr, buildLog string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_instance SET status=?, last_error=?, build_log=?, updated_at=CURRENT_TIMESTAMP WHERE app_id=? AND env=?`,
		status, lastErr, buildLog, appID, env)
	return err
}

// ListInstancesByApp 列出应用的所有环境实例。
func (s *Store) ListInstancesByApp(ctx context.Context, appID string) ([]AppInstance, error) {
	var list []AppInstance
	err := s.db.SelectContext(ctx, &list, `SELECT `+insCols+` FROM appdeploy_instance WHERE app_id=? ORDER BY env`, appID)
	return list, err
}

// envCols 环境变量显式列。
const envCols = `id, app_id, key, COALESCE(value,'') AS value, is_secret, created_at`

// ListEnv 列出应用的环境变量（部署注入用；接口层对 is_secret 的 value 做 mask）。
func (s *Store) ListEnv(ctx context.Context, appID string) ([]EnvVar, error) {
	var list []EnvVar
	err := s.db.SelectContext(ctx, &list, `SELECT `+envCols+` FROM appdeploy_env WHERE app_id=? ORDER BY key`, appID)
	return list, err
}

// UpsertEnv 新增或更新环境变量（按 app_id+key 唯一）。
func (s *Store) UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool) error {
	id := "env_" + uuid.NewString()[:20]
	sec := 0
	if isSecret {
		sec = 1
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_env (id, app_id, key, value, is_secret) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(app_id, key) DO UPDATE SET value=excluded.value, is_secret=excluded.is_secret`,
		id, appID, key, value, sec)
	return err
}

// DeleteEnv 删除环境变量。
func (s *Store) DeleteEnv(ctx context.Context, appID, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_env WHERE app_id=? AND key=?`, appID, key)
	return err
}

// EnvPairs 返回 ["KEY=VALUE", ...] 供 docker run -e 注入（含 secret 实际值）。
func (s *Store) EnvPairs(ctx context.Context, appID string) ([]string, error) {
	vars, err := s.ListEnv(ctx, appID)
	if err != nil {
		return nil, err
	}
	pairs := make([]string, 0, len(vars))
	for _, v := range vars {
		pairs = append(pairs, v.Key+"="+v.Value)
	}
	return pairs, nil
}

// EnsureAppForRequirement 为需求兜底创建托管应用：同名则复用，否则建仓 + 建记录。
// 用于"需求未归属应用"时自动确立代码归属（应用 = 托管 git 仓库），使派发永不阻塞。
// 返回 appID + repoDir + port（默认 8080，buildpack 后续可按源码类型校正）。
func (s *Store) EnsureAppForRequirement(ctx context.Context, psID, appName string) (appID, repoDir string, port int, err error) {
	if a, e := s.GetByName(ctx, psID, appName); e == nil && a != nil && a.ID != "" {
		return a.ID, a.RepoDir, a.InternalPort, nil
	}
	repoDir = ManagedRepoDir(appName)
	if e := EnsureRepo(ctx, repoDir); e != nil {
		return "", "", 0, fmt.Errorf("初始化托管仓库: %w", e)
	}
	a := &Application{ProjectSpaceID: psID, Name: appName, RepoDir: repoDir, InternalPort: 8080}
	if e := s.Create(ctx, a); e != nil {
		return "", "", 0, e
	}
	return a.ID, a.RepoDir, a.InternalPort, nil
}

// UpdateDeploy 更新部署态字段（镜像/容器/端口/URL/版本/状态）。
func (s *Store) UpdateDeploy(ctx context.Context, a *Application) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_application SET image=?, container_name=?, host_port=?, url=?, version=?, status=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		a.Image, a.ContainerName, a.HostPort, a.URL, a.Version, a.Status, a.ID)
	return err
}

// SetStatus 更新状态 + 最近错误/构建日志。
func (s *Store) SetStatus(ctx context.Context, psID, id, status, lastErr, buildLog string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_application SET status=?, last_error=?, build_log=?, updated_at=CURRENT_TIMESTAMP WHERE id=? AND project_space_id=?`,
		status, lastErr, buildLog, id, psID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("应用 %s 不存在", id)
	}
	return nil
}

// Delete 删除记录。
func (s *Store) Delete(ctx context.Context, psID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_application WHERE id=? AND project_space_id=?`, id, psID)
	return err
}
