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

// DB 暴露底层连接（跨模块装配/测试用，如 HealthReconciler 装配处复用同一 *sqlx.DB 建 ops.Store）。
func (s *Store) DB() *sqlx.DB { return s.db }

func appCols() string {
	return `id, project_space_id, name, COALESCE(repo_dir,'') AS repo_dir, internal_port, COALESCE(image,'') AS image, COALESCE(container_name,'') AS container_name, host_port, COALESCE(url,'') AS url, version, status, COALESCE(last_error,'') AS last_error, COALESCE(build_log,'') AS build_log, COALESCE(deploy_mode,'managed') AS deploy_mode, COALESCE(app_kind,'web') AS app_kind, COALESCE(external_url,'') AS external_url, COALESCE(import_source,'') AS import_source, COALESCE(import_ref,'') AS import_ref, imported_at, created_at, updated_at`
}

// Create 注册应用（默认 registered；导入流程传 StatusImporting）。
// 落 deploy_mode + app_kind + external_url：managed 默认走 registered + 空串；external 直接 running + external_url。
// app_kind 默认 web（与存量数据兼容）；非 web 形态由后续 Builder 出产物。
func (s *Store) Create(ctx context.Context, a *Application) error {
	a.ID = "app_" + uuid.NewString()[:20]
	if a.Status == "" {
		a.Status = "registered"
	}
	if a.DeployMode == "" {
		a.DeployMode = AppManaged
	}
	if a.AppKind == "" {
		a.AppKind = AppKindWeb
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_application (id, project_space_id, name, repo_dir, internal_port, status, deploy_mode, app_kind, external_url, import_source, import_ref)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		a.ID, a.ProjectSpaceID, a.Name, a.RepoDir, a.InternalPort, a.Status, a.DeployMode, a.AppKind, a.ExternalURL, a.ImportSource, a.ImportRef)
	return err
}

// List 列出项目空间下的应用。
func (s *Store) List(ctx context.Context, psID string) ([]Application, error) {
	var list []Application
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+appCols()+` FROM appdeploy_application WHERE project_space_id=$1 ORDER BY created_at DESC`, psID)
	return list, err
}

// Get 取单条。
func (s *Store) Get(ctx context.Context, psID, id string) (*Application, error) {
	var a Application
	err := s.db.GetContext(ctx, &a, `SELECT `+appCols()+` FROM appdeploy_application WHERE id=$1 AND project_space_id=$2`, id, psID)
	return &a, err
}

// GetByName 按名取（去重/查找）。
func (s *Store) GetByName(ctx context.Context, psID, name string) (*Application, error) {
	var a Application
	err := s.db.GetContext(ctx, &a, `SELECT `+appCols()+` FROM appdeploy_application WHERE project_space_id=$1 AND name=$2`, psID, name)
	return &a, err
}

// GetByAppID 按应用 id 取（跨空间，id 全局唯一）。
func (s *Store) GetByAppID(ctx context.Context, appID string) (*Application, error) {
	var a Application
	err := s.db.GetContext(ctx, &a, `SELECT `+appCols()+` FROM appdeploy_application WHERE id=$1`, appID)
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
const insCols = `id, app_id, env, COALESCE(image,'') AS image, COALESCE(container_name,'') AS container_name, host_port, COALESCE(url,'') AS url, version, status, COALESCE(last_error,'') AS last_error, COALESCE(build_log,'') AS build_log, restart_count, created_at, updated_at`

// GetInstance 取某应用某环境实例（不存在返回 nil,nil）。
func (s *Store) GetInstance(ctx context.Context, appID, env string) (*AppInstance, error) {
	var list []AppInstance
	if err := s.db.SelectContext(ctx, &list, `SELECT `+insCols+` FROM appdeploy_instance WHERE app_id=$1 AND env=$2`, appID, env); err != nil {
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO appdeploy_instance (id, app_id, env, status) VALUES ($1, $2, $3, 'registered')`, ins.ID, appID, env)
	return ins, err
}

// UpdateInstance 更新实例部署态字段。
func (s *Store) UpdateInstance(ctx context.Context, ins *AppInstance) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_instance SET image=$1, container_name=$2, host_port=$3, url=$4, version=$5, status=$6, last_error=$7, build_log=$8, updated_at=CURRENT_TIMESTAMP WHERE app_id=$9 AND env=$10`,
		ins.Image, ins.ContainerName, ins.HostPort, ins.URL, ins.Version, ins.Status, ins.LastError, ins.BuildLog, ins.AppID, ins.Env)
	return err
}

// SetInstanceStatus 更新实例状态 + 错误/日志。
func (s *Store) SetInstanceStatus(ctx context.Context, appID, env, status, lastErr, buildLog string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_instance SET status=$1, last_error=$2, build_log=$3, updated_at=CURRENT_TIMESTAMP WHERE app_id=$4 AND env=$5`,
		status, lastErr, buildLog, appID, env)
	return err
}

// ListInstancesByApp 列出应用的所有环境实例。
func (s *Store) ListInstancesByApp(ctx context.Context, appID string) ([]AppInstance, error) {
	var list []AppInstance
	err := s.db.SelectContext(ctx, &list, `SELECT `+insCols+` FROM appdeploy_instance WHERE app_id=$1 ORDER BY env`, appID)
	return list, err
}

// headlessInstance HealthReconciler 巡检目标行(appdeploy_instance JOIN appdeploy_application)。
type headlessInstance struct {
	AppID          string `db:"app_id"`
	Env            string `db:"env"`
	ContainerName  string `db:"container_name"`
	Status         string `db:"status"`
	RestartCount   int    `db:"restart_count"`
	ProjectSpaceID string `db:"project_space_id"`
	Name           string `db:"name"`
}

// ListHeadlessActiveInstances 列出需健康巡检的实例:headless 应用 且 status∈{running,degraded,failed}。
// 含 failed 是为让 reconcile 能捕获"崩溃后又被 docker restart 拉起"的实例并翻回 running+resolve 告警；
// stopped(用户主动停) 不纳入,registered/building/built 亦同。
func (s *Store) ListHeadlessActiveInstances(ctx context.Context) ([]headlessInstance, error) {
	var list []headlessInstance
	err := s.db.SelectContext(ctx, &list,
		`SELECT i.app_id, i.env, COALESCE(i.container_name,'') AS container_name,
		        i.status, i.restart_count, a.project_space_id, a.name
		 FROM appdeploy_instance i
		 JOIN appdeploy_application a ON a.id = i.app_id
		 WHERE a.app_kind='headless' AND i.status IN ('running','degraded','failed')`)
	return list, err
}

// UpdateInstanceHealth 更新实例 status + last_error + restart_count(reconcile 翻转时用)。
func (s *Store) UpdateInstanceHealth(ctx context.Context, appID, env, status, lastErr string, restartCount int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_instance SET status=$1, last_error=$2, restart_count=$3, updated_at=CURRENT_TIMESTAMP
		 WHERE app_id=$4 AND env=$5`, status, lastErr, restartCount, appID, env)
	return err
}

// UpdateRestartCount 仅更新 restart_count 基线(status 未翻转时用)。
func (s *Store) UpdateRestartCount(ctx context.Context, appID, env string, restartCount int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_instance SET restart_count=$1 WHERE app_id=$2 AND env=$3`, restartCount, appID, env)
	return err
}

// envCols 环境变量显式列。
const envCols = `id, app_id, key, COALESCE(value,'') AS value, is_secret, COALESCE(source,'user') AS source, created_at`

// ListEnv 列出应用的环境变量（部署注入用；接口层对 is_secret 的 value 做 mask）。
func (s *Store) ListEnv(ctx context.Context, appID string) ([]EnvVar, error) {
	var list []EnvVar
	err := s.db.SelectContext(ctx, &list, `SELECT `+envCols+` FROM appdeploy_env WHERE app_id=$1 ORDER BY key`, appID)
	return list, err
}

// UpsertEnv 新增或更新环境变量（按 app_id+key 唯一）。
// is_secret 直接传 bool（PG BOOLEAN 列不接受 int；sqlite 驱动自动 bool↔INTEGER）。
// source: "user"(用户面板) / "platform"(平台注入，部署 reconcile 保障，前端只读)。
func (s *Store) UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error {
	if source == "" {
		source = "user"
	}
	id := "env_" + uuid.NewString()[:20]
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_env (id, app_id, key, value, is_secret, source) VALUES ($1, $2, $3, $4, $5, $6)
		 ON CONFLICT(app_id, key) DO UPDATE SET value=excluded.value, is_secret=excluded.is_secret, source=excluded.source`,
		id, appID, key, value, isSecret, source)
	return err
}

// DeleteEnv 删除环境变量。
func (s *Store) DeleteEnv(ctx context.Context, appID, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_env WHERE app_id=$1 AND key=$2`, appID, key)
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
		`UPDATE appdeploy_application SET image=$1, container_name=$2, host_port=$3, url=$4, version=$5, status=$6, updated_at=CURRENT_TIMESTAMP WHERE id=$7`,
		a.Image, a.ContainerName, a.HostPort, a.URL, a.Version, a.Status, a.ID)
	return err
}

// UpdateVersion 持久化构建版本号（BuildArtifacts 成功上传产物后调）。
// 与 SetStatus 分离：SetStatus 只改 status/last_error/build_log，不写 version；
// 非 web 构建产物的版本号递增需单独落库，否则 BuildArtifacts 里 a.Version++ 只改内存。
// psID 可空（跨空间按 id 更新时传空串，SQL 不带 project_space_id 谓词）。
func (s *Store) UpdateVersion(ctx context.Context, id string, version int) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_application SET version=$1, updated_at=now() WHERE id=$2`, version, id)
	return err
}

// SetStatus 更新状态 + 最近错误/构建日志。
func (s *Store) SetStatus(ctx context.Context, psID, id, status, lastErr, buildLog string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_application SET status=$1, last_error=$2, build_log=$3, updated_at=CURRENT_TIMESTAMP WHERE id=$4 AND project_space_id=$5`,
		status, lastErr, buildLog, id, psID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("应用 %s 不存在", id)
	}
	return nil
}

// UpdateImportDone 导入完成：置 registered + imported_at=now + repo_dir，清 last_error。
func (s *Store) UpdateImportDone(ctx context.Context, psID, id, repoDir string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_application SET status='registered', imported_at=CURRENT_TIMESTAMP, repo_dir=$1, last_error='', updated_at=CURRENT_TIMESTAMP WHERE id=$2 AND project_space_id=$3`,
		repoDir, id, psID)
	return err
}

// UpdateAppStatus 只更新 app 状态（部署进度用，跨环境）。
func (s *Store) UpdateAppStatus(ctx context.Context, appID, status string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE appdeploy_application SET status=$1, updated_at=CURRENT_TIMESTAMP WHERE id=$2`,
		status, appID)
	return err
}

// Delete 删除记录。
func (s *Store) Delete(ctx context.Context, psID, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_application WHERE id=$1 AND project_space_id=$2`, id, psID)
	return err
}

// GetEnvValue 取应用某 env key 的明文值（不存在返回空串，不报错）。
// 供 pgsupply 读 DATABASE_URL 做 mask 展示（跨模块只读 appdeploy_env）。
func (s *Store) GetEnvValue(ctx context.Context, appID, key string) (string, error) {
	var v string
	err := s.db.GetContext(ctx, &v,
		`SELECT COALESCE((SELECT value FROM appdeploy_env WHERE app_id=$1 AND key=$2),'')`,
		appID, key)
	return v, err
}

// GetEnvSource 取应用某 env key 的 source（不存在返回 'user'，不报错）。
// 供 HTTP 面板判断是否平台托管（platform 不可手改/删，部署 reconcile 保障）。
func (s *Store) GetEnvSource(ctx context.Context, appID, key string) (string, error) {
	var src string
	err := s.db.GetContext(ctx, &src,
		`SELECT COALESCE((SELECT source FROM appdeploy_env WHERE app_id=$1 AND key=$2),'user')`,
		appID, key)
	return src, err
}
