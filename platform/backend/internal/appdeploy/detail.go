package appdeploy

import (
	"context"
	"time"
)

// AppFullView 应用全景聚合：应用本体 + 需求/变更/发布/git历史/实例（原 AppDetail）
// + 编码会话/异步任务/路由/依赖/部署需求 needs（P1-c 全景扩展）。单一信息源，前端详情看板据此渲染。
// 变更/发布通过 requirement.application_id 派生关联（source_id→requirement→app），无需冗余列。
type AppFullView struct {
	Application  Application     `json:"application"`
	Requirements []AppReqItem    `json:"requirements"`
	Changes      []AppChangeItem `json:"changes"`
	Releases     []AppRelItem    `json:"releases"`
	Commits      []CommitInfo    `json:"commits"`   // 托管 git 仓库的版本历史（= 应用代码版本）
	Instances    []AppInstance   `json:"instances"` // 各环境部署实例（test/prod）
	// P1-c 全景维度
	Sessions    []AppSession     `json:"sessions"`     // 编码会话（codews_session by app_id）
	Tasks       []AppTask        `json:"tasks"`        // 异步编码任务（code_task 经 change→app 派生）
	Routes      []AppRoute       `json:"routes"`       // 路由（appdeploy_route by app_id）
	Deps        []DepDeclaration `json:"deps"`         // 中间件依赖（handler 经 mwReconciler 填，Store 不填）
	DeployNeeds *NeedsSpec       `json:"deploy_needs"` // .anp/deploy.yaml needs（权威输入，只读展示；无 manifest=nil）
}

// AppSession 编码会话摘要（codews_session 子集，前端研发列用）。
type AppSession struct {
	ID          string     `json:"id" db:"id"`
	Tool        string     `json:"tool" db:"tool"`
	StartedAt   time.Time  `json:"started_at" db:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	PromptCount int        `json:"prompt_count" db:"prompt_count"`
}

// AppTask 异步编码任务摘要（code_task 子集 + 派生 req_title，前端研发列用）。
type AppTask struct {
	ID        string    `json:"id" db:"id"`
	Kind      string    `json:"kind" db:"kind"`
	Status    string    `json:"status" db:"status"`
	ReqTitle  string    `json:"req_title,omitempty" db:"req_title"`
	ChangeID  string    `json:"change_id,omitempty" db:"change_id"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// AppRoute 路由摘要（appdeploy_route 子集，前端部署列用）。
type AppRoute struct {
	Env          string `json:"env" db:"env"`
	AppCode      string `json:"app_code" db:"app_code"`
	UpstreamHost string `json:"upstream_host" db:"upstream_host"`
	UpstreamPort int    `json:"upstream_port" db:"upstream_port"`
	Status       string `json:"status" db:"status"`
	ExternalURL  string `json:"external_url,omitempty" db:"external_url"`
}

// norm nil 切片归一化为空切片（Go nil 切片序列化 JSON null，前端 .length 崩；统一空数组）。
func norm[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// AppReqItem 需求条目（详情用，含展开所需的描述/用户故事/验收标准）。
type AppReqItem struct {
	ID                 string `json:"id" db:"id"`
	Title              string `json:"title" db:"title"`
	Status             string `json:"status" db:"status"`
	Priority           string `json:"priority" db:"priority"`
	FixedVersion       string `json:"fixed_version" db:"fixed_version"`
	Tasks              string `json:"tasks" db:"tasks"`
	Assignee           string `json:"assignee" db:"assignee"`
	Description        string `json:"description" db:"description"`
	UserStory          string `json:"user_story" db:"user_story"`
	AcceptanceCriteria string `json:"acceptance_criteria" db:"acceptance_criteria"`
}

// AppChangeItem 变更条目。
type AppChangeItem struct {
	ID       string    `json:"id" db:"id"`
	Status   string    `json:"status" db:"status"`
	SourceID string    `json:"source_id" db:"source_id"`
	Kind     string    `json:"kind" db:"kind"`
	Output   string    `json:"output" db:"output"`
	CreateAt time.Time `json:"created_at" db:"created_at"`
}

// AppRelItem 发布条目。
type AppRelItem struct {
	ID       string    `json:"id" db:"id"`
	Version  string    `json:"version" db:"version"`
	Status   string    `json:"status" db:"status"`
	ChangeID string    `json:"change_id" db:"change_id"`
	CreateAt time.Time `json:"created_at" db:"created_at"`
}

// Detail 聚合某应用的全景视图：需求→变更→发布 + 实例 + 编码会话/异步任务/路由/部署需求。
// Deps 不在此填（经 handler 的 mwReconciler.ListDeps 注入，避 appdeploy→mwsupply 依赖）。
func (s *Store) Detail(ctx context.Context, psID, appID string) (*AppFullView, error) {
	a, err := s.Get(ctx, psID, appID)
	if err != nil || a == nil || a.ID == "" {
		return nil, err
	}
	d := &AppFullView{Application: *a}

	// 需求（直接归属，含详情字段供前端展开；按等级 P0→P1→P2 排序）
	if err := s.db.SelectContext(ctx, &d.Requirements,
		`SELECT id, COALESCE(title,'') AS title, status,
		        COALESCE(priority,'') AS priority, COALESCE(fixed_version,'') AS fixed_version, COALESCE(tasks,'') AS tasks, COALESCE(assignee,'') AS assignee,
		        COALESCE(description,'') AS description, COALESCE(user_story,'') AS user_story,
		        COALESCE(acceptance_criteria,'') AS acceptance_criteria
		 FROM requirement WHERE application_id=$1 ORDER BY COALESCE(NULLIF(priority,''),'P1'), created_at DESC`, appID); err != nil {
		return nil, err
	}
	// 变更：source_id=应用ID（交互编码登记，期2）OR source_id=需求ID（AI 编码派生）
	if err := s.db.SelectContext(ctx, &d.Changes,
		`SELECT id, status, COALESCE(source_id,'') AS source_id, COALESCE(kind,'') AS kind, COALESCE(output,'') AS output, created_at
		 FROM change_request
		 WHERE source_id = $1 OR source_id IN (SELECT id FROM requirement WHERE application_id=$2)
		 ORDER BY created_at DESC`, appID, appID); err != nil {
		return nil, err
	}
	// 发布（经 change_id→change→source_id→requirement→app 派生）
	if err := s.db.SelectContext(ctx, &d.Releases,
		`SELECT id, version, status, COALESCE(change_id,'') AS change_id, created_at
		 FROM release_record
		 WHERE change_id IN (SELECT id FROM change_request WHERE source_id IN (SELECT id FROM requirement WHERE application_id=$1))
		 ORDER BY created_at DESC`, appID); err != nil {
		return nil, err
	}
	// 托管仓库版本历史（git log = 应用代码版本）
	d.Commits, _ = Log(ctx, a.RepoDir, 10)
	// 各环境部署实例（test/prod）
	d.Instances, _ = s.ListInstancesByApp(ctx, appID)
	// P1-c：编码会话（codews_session.app_id 直关联）
	d.Sessions, _ = s.ListSessionsByApp(ctx, appID)
	// P1-c：路由（appdeploy_route.app_id）
	d.Routes, _ = s.ListRoutesByApp(ctx, appID)
	// P1-c：异步编码任务（code_task 经 change_request→app 派生）
	d.Tasks, _ = s.ListTasksByApp(ctx, appID)
	// P1-c：部署需求 needs（.anp/deploy.yaml 权威输入，只读；best-effort，无 manifest=nil）
	if mf, _ := LoadDeployManifest(a.RepoDir); mf != nil {
		d.DeployNeeds = &mf.Needs
	}
	// nil→空切片归一化（Go nil 切片序列化 JSON null，前端 detail.*.length 崩；统一空数组）。
	d.Requirements = norm(d.Requirements)
	d.Changes = norm(d.Changes)
	d.Releases = norm(d.Releases)
	d.Commits = norm(d.Commits)
	d.Instances = norm(d.Instances)
	d.Sessions = norm(d.Sessions)
	d.Routes = norm(d.Routes)
	d.Tasks = norm(d.Tasks)
	d.Deps = norm(d.Deps)
	return d, nil
}

// ListSessionsByApp 列某应用的编码会话（codews_session by app_id，最近 20 条）。
func (s *Store) ListSessionsByApp(ctx context.Context, appID string) ([]AppSession, error) {
	var list []AppSession
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, COALESCE(tool,'') AS tool, started_at, ended_at, prompt_count
		 FROM codews_session WHERE app_id=$1 ORDER BY started_at DESC LIMIT 20`, appID)
	return list, err
}

// ListRoutesByApp 列某应用的路由（appdeploy_route by app_id，按 env 排）。
func (s *Store) ListRoutesByApp(ctx context.Context, appID string) ([]AppRoute, error) {
	var list []AppRoute
	err := s.db.SelectContext(ctx, &list,
		`SELECT env, COALESCE(app_code,'') AS app_code, COALESCE(upstream_host,'') AS upstream_host,
		        upstream_port, COALESCE(status,'') AS status, COALESCE(external_url,'') AS external_url
		 FROM appdeploy_route WHERE app_id=$1 ORDER BY env`, appID)
	return list, err
}

// ListTasksByApp 列某应用的异步编码任务（code_task 经 change_request 派生回 app，最近 20 条）。
// 派生路径镜像 codetask.ListByProjectSpace：task.change_id→change.id，change.source_id=appID（直登）
// 或 change.source_id∈requirement(appID)（AI 派发）。
func (s *Store) ListTasksByApp(ctx context.Context, appID string) ([]AppTask, error) {
	var list []AppTask
	err := s.db.SelectContext(ctx, &list,
		`SELECT t.id, COALESCE(t.kind,'') AS kind, t.status,
		        COALESCE((SELECT r.title FROM requirement r WHERE r.id = t.source_id),'') AS req_title,
		        COALESCE(t.change_id,'') AS change_id, t.created_at
		 FROM code_task t
		 JOIN change_request ch ON ch.id = t.change_id
		 WHERE ch.source_id = $1 OR ch.source_id IN (SELECT id FROM requirement WHERE application_id=$1)
		 ORDER BY t.created_at DESC LIMIT 20`, appID)
	return list, err
}
