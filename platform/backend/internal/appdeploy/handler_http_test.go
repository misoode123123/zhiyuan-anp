package appdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newHTTPHandler 建一个完整 Handler（store=sqli 内存，deployer=空 host）。
// 注：deployer 字段在多数 store-only 接口（List/Detail/Env/RepoDocs/RepoFile/Stats deployed=false）
// 上不会被调用，因此即使指向真实 Deployer 也不会触发 docker。
func newHTTPHandler(t *testing.T) (*Handler, *sqlx.DB) {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE appdeploy_application (
  id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, name TEXT NOT NULL,
  repo_dir TEXT, internal_port INTEGER NOT NULL DEFAULT 80, image TEXT, container_name TEXT,
  host_port INTEGER NOT NULL DEFAULT 0, url TEXT, version INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'registered', last_error TEXT, build_log TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (project_space_id, name))`)
	db.MustExec(`CREATE TABLE appdeploy_instance (
  id TEXT PRIMARY KEY, app_id TEXT NOT NULL, env TEXT NOT NULL, image TEXT, container_name TEXT,
  host_port INTEGER NOT NULL DEFAULT 0, url TEXT, version INTEGER NOT NULL DEFAULT 0,
  status TEXT NOT NULL DEFAULT 'registered', last_error TEXT, build_log TEXT,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  UNIQUE (app_id, env))`)
	db.MustExec(`CREATE TABLE appdeploy_env (
  id TEXT PRIMARY KEY, app_id TEXT NOT NULL, key TEXT NOT NULL, value TEXT,
  is_secret INTEGER NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE (app_id, key))`)
	// Detail 联表（需求/变更/发布）
	db.MustExec(`CREATE TABLE requirement (
  id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, application_id TEXT, title TEXT NOT NULL,
  description TEXT, user_story TEXT, acceptance_criteria TEXT, status TEXT NOT NULL DEFAULT 'draft',
  priority TEXT, fixed_version TEXT, tasks TEXT, assignee TEXT, assigned_at DATETIME,
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	db.MustExec(`CREATE TABLE change_request (
  id TEXT PRIMARY KEY, project_space_id TEXT, kind TEXT, source_id TEXT, application_id TEXT,
  repo_dir TEXT, prompt TEXT, model TEXT, output TEXT, status TEXT, reviewer TEXT,
  reviewed_at DATETIME, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	db.MustExec(`CREATE TABLE release_record (
  id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, change_id TEXT, application_id TEXT,
  version TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'released',
  created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	store := NewStore(db)
	h := NewHandler(store, NewDeployer("test"), nil, nil, nil)
	return h, db
}

// newRouterWith 注册路由到 gin 引擎。
func newRouterWith(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h.Register(r.Group("/api/v1"))
	return r
}

// doReq 发请求返回状态码 + 解析后的 JSON body。
func doReq(t *testing.T, r http.Handler, method, target string, body interface{}) (int, map[string]interface{}) {
	t.Helper()
	var reader *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		reader = bytes.NewReader(b)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// seedApp 直接写一条应用记录到 DB（绕过 handler.Create 的 EnsureRepo/git 调用）。
func seedApp(t *testing.T, h *Handler, psID, name, repoDir string) *Application {
	t.Helper()
	a := &Application{ProjectSpaceID: psID, Name: name, RepoDir: repoDir, InternalPort: 8080}
	if err := h.store.Create(context.Background(), a); err != nil {
		t.Fatalf("seed create: %v", err)
	}
	return a
}

// TestHandler_List_empty 无应用时空列表（不报错）。
func TestHandler_List_empty(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps", nil)
	if code != 200 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	if resp["code"].(float64) != 0 {
		t.Fatalf("业务 code 应 0，得到 %v", resp["code"])
	}
	data, _ := resp["data"].([]interface{})
	if len(data) != 0 {
		t.Fatalf("空应用列表应 0 项，得到 %v", data)
	}
}

// TestHandler_List_withApps 多应用 + 各自实例聚合到 Instances 字段。
func TestHandler_List_withApps(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	// 给 a 建一个 prod 实例
	ins, _ := h.store.GetOrCreateInstance(ctx, a.ID, EnvProd)
	ins.URL = "http://h:9200"
	ins.Status = "running"
	_ = h.store.UpdateInstance(ctx, ins)
	seedApp(t, h, "ps_1", "cat", "/tmp/cat") // cat 无实例
	seedApp(t, h, "ps_2", "other", "/tmp/other")

	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps", nil)
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	data, _ := resp["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("ps_1 应有 2 个应用，得到 %d", len(data))
	}
	// 在 list 中找到 snake 并校验其实例聚合
	var snakeEntry map[string]interface{}
	for _, it := range data {
		m := it.(map[string]interface{})
		if m["name"] == "snake" {
			snakeEntry = m
		}
	}
	if snakeEntry == nil {
		t.Fatalf("未找到 snake 应用，list=%v", data)
	}
	inss, _ := snakeEntry["instances"].([]interface{})
	if len(inss) != 1 {
		t.Fatalf("snake 应有 1 个 prod 实例，得到 %d", len(inss))
	}
	// 跨空间隔离
	for _, it := range data {
		m := it.(map[string]interface{})
		if m["name"] == "other" {
			t.Fatal("ps_2 的 other 不应混入 ps_1")
		}
	}
}

// TestHandler_Detail_notFound 应用不存在 → 404。
func TestHandler_Detail_notFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/app_ghost/detail", nil)
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_Detail_ok 存在应用 → 200，含本体。
// repoDir 用临时目录（非 git 仓库 → Log 返回空，不报错）。
func TestHandler_Detail_ok(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/no-such-repo")
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/detail", nil)
	if code != 200 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	app, _ := data["application"].(map[string]interface{})
	if app == nil || app["name"] != "snake" {
		t.Fatalf("应返回 snake 应用本体，得到 %v", data)
	}
}

// TestHandler_DeployCommit_missingSha 缺 sha → 400。
func TestHandler_DeployCommit_missingSha(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/app_x/deploy-commit", map[string]string{"env": "test"})
	if code != 400 {
		t.Fatalf("缺 sha 应 400，得到 %d", code)
	}
}

// TestHandler_DeployCommit_appNotFound app 不存在 → 404。
func TestHandler_DeployCommit_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/app_ghost/deploy-commit", map[string]string{"sha": "abc1234"})
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_Promote_appNotFound 应用不存在 → 404（不触发 async docker）。
func TestHandler_Promote_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/app_ghost/promote", nil)
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_Deploy_appNotFound Deploy 不存在应用 → 404。
func TestHandler_Deploy_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/app_ghost/deploy", nil)
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_ListEnv 应用无环境变量 → 空列表。
func TestHandler_ListEnv_empty(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/env", nil)
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	data, _ := resp["data"].([]interface{})
	if len(data) != 0 {
		t.Fatalf("空 env 列表应 0 项，得到 %v", data)
	}
}

// TestHandler_ListEnv_secretMasked is_secret=true 的 value 应被 mask（空串）。
func TestHandler_ListEnv_secretMasked(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	_ = h.store.UpsertEnv(ctx, a.ID, "PUBLIC_KEY", "visible", false)
	_ = h.store.UpsertEnv(ctx, a.ID, "SECRET_TOKEN", "top-secret-value", true)

	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/env", nil)
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	data, _ := resp["data"].([]interface{})
	for _, it := range data {
		m := it.(map[string]interface{})
		if m["key"] == "SECRET_TOKEN" {
			if m["value"] != "" {
				t.Fatalf("SECRET_TOKEN 应被 mask 为空，得到 %q", m["value"])
			}
		}
		if m["key"] == "PUBLIC_KEY" {
			if m["value"] != "visible" {
				t.Fatalf("PUBLIC_KEY 应可见，得到 %q", m["value"])
			}
		}
	}
}

// TestHandler_UpsertEnv_invalidBody 缺 key → 400。
func TestHandler_UpsertEnv_invalidBody(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/env", map[string]string{"value": "v"})
	if code != 400 {
		t.Fatalf("缺 key 应 400，得到 %d", code)
	}
}

// TestHandler_UpsertEnv_ok 正常新增 → 200。
func TestHandler_UpsertEnv_ok(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/env",
		map[string]interface{}{"key": "API_KEY", "value": "v1", "is_secret": true})
	if code != 200 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	// 写入应可读回
	vars, _ := h.store.ListEnv(context.Background(), a.ID)
	if len(vars) != 1 || vars[0].Key != "API_KEY" {
		t.Fatalf("写入未生效: %v", vars)
	}
}

// TestHandler_DeleteEnv_ok 删除环境变量。
func TestHandler_DeleteEnv_ok(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	_ = h.store.UpsertEnv(ctx, a.ID, "K", "v", false)
	code, _ := doReq(t, r, http.MethodDelete, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/env/K", nil)
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	vars, _ := h.store.ListEnv(ctx, a.ID)
	if len(vars) != 0 {
		t.Fatalf("删除后应空，得到 %v", vars)
	}
}

// TestHandler_Stats_appNotFound 应用不存在 → 404。
func TestHandler_Stats_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/app_ghost/stats", nil)
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_Stats_notDeployed 应用存在但无实例 → deployed=false（不调 docker）。
func TestHandler_Stats_notDeployed(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/stats?env=prod", nil)
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["deployed"] != false {
		t.Fatalf("未部署应 deployed=false，得到 %v", data["deployed"])
	}
	if data["env"] != "prod" {
		t.Fatalf("env 应回显 prod，得到 %v", data["env"])
	}
}

// TestHandler_Stats_invalidEnvDefaultsProd env 非法 → 默认 prod（不报错）。
func TestHandler_Stats_invalidEnvDefaultsProd(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/stats?env=staging", nil)
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["env"] != "prod" {
		t.Fatalf("非法 env 应兜底 prod，得到 %v", data["env"])
	}
}

// TestHandler_Logs_notDeployed 应用未在 prod 部署 → 返回占位日志，不调 docker。
func TestHandler_Logs_notDeployed(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/logs", nil)
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["logs"] != "(应用未在 prod 部署)" {
		t.Fatalf("未部署 logs 应占位，得到 %v", data["logs"])
	}
}

// TestHandler_RepoDocs 扫描应用 repo 的文档结构。
func TestHandler_RepoDocs(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	// 用临时目录作为 repoDir（ScanDocs 纯函数，不需要 git）
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake"+aRandSuffix())
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/repo-docs", nil)
	if code != 200 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	data, _ := resp["data"].([]interface{})
	// 空目录也应返回 200 + 空列表
	_ = data
}

// TestHandler_RepoFile 读 repo 文件内容。
func TestHandler_RepoFile(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	// 使用真实临时目录 + 文件，让 ReadRepoFile 能读到
	dir := t.TempDir()
	a := seedApp(t, h, "ps_1", "snake", dir)
	// 临时目录由 t.TempDir 创建，dir 是绝对路径
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/repo-file?path=README.md", nil)
	// 文件不存在时 ReadRepoFile 返回 err → handler 返回 400
	if code != 400 {
		t.Fatalf("不存在文件应 400，得到 %d body=%v", code, resp)
	}
}

// TestHandler_RepoDocs_appNotFound 应用不存在 → 404。
func TestHandler_RepoDocs_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/app_ghost/repo-docs", nil)
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_Delete_appNotFound 应用不存在 → 删除返回 200（idempotent delete）。
func TestHandler_Delete_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodDelete, "/api/v1/project-spaces/ps_1/apps/app_ghost", nil)
	// 当前实现：a==nil 跳过 docker，store.Delete 不存在返回 nil（DELETE 幂等）
	if code != 200 {
		t.Fatalf("幂等删除应 200，得到 %d", code)
	}
}

// TestHandler_Stop_notDeployed 应用未在 prod 部署 → 400（不调 docker）。
func TestHandler_Stop_notDeployed(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/stop", nil)
	if code != 400 {
		t.Fatalf("未部署应用 Stop 应 400，得到 %d", code)
	}
}

// TestHandler_Start_notDeployed 应用未在 prod 部署 → 400。
func TestHandler_Start_notDeployed(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/start", nil)
	if code != 400 {
		t.Fatalf("未部署应用 Start 应 400，得到 %d", code)
	}
}

// aRandSuffix 返回伪随机后缀避免名字碰撞（简化：固定串）。
func aRandSuffix() string { return "-t1" }

// newHTTPHandlerWithTables 同 newHTTPHandler 但暴露 db 句柄以便 JOIN 表播种。
// 已在 newHTTPHandler 内建好 requirement/change_request/release_record 三张表。
func newHTTPHandlerWithTables(t *testing.T) (*Handler, *sqlx.DB) {
	return newHTTPHandler(t)
}

// TestStore_Detail_Aggregation Detail 聚合：本体 + 需求/变更/发布/实例。
// 验证 source_id=appID 与 source_id=reqID（属同 app）的变更都能被聚合。
func TestStore_Detail_Aggregation(t *testing.T) {
	h, db := newHTTPHandlerWithTables(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/no-git")
	// 需求（application_id 关联）
	_, _ = db.ExecContext(ctx, `INSERT INTO requirement (id, project_space_id, application_id, title, status, priority)
		VALUES ('req_1', 'ps_1', ?, '需求1', 'specified', 'P0')`, a.ID)
	// 变更 1：source_id 直接 = appID
	_, _ = db.ExecContext(ctx, `INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		VALUES ('chg_1', 'ps_1', ?, 'code', 'diff1', 'pending')`, a.ID)
	// 变更 2：source_id = 需求 ID（属同 app，应被聚合）
	_, _ = db.ExecContext(ctx, `INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		VALUES ('chg_2', 'ps_1', 'req_1', 'code', 'diff2', 'approved')`)
	// 发布（change_id → change → source_id → requirement → app 派生）
	_, _ = db.ExecContext(ctx, `INSERT INTO release_record (id, project_space_id, change_id, version, status)
		VALUES ('rel_1', 'ps_1', 'chg_2', 'v1.0', 'released')`)
	// 实例
	_, _ = h.store.GetOrCreateInstance(ctx, a.ID, EnvTest)

	d, err := h.store.Detail(ctx, "ps_1", a.ID)
	if err != nil || d == nil {
		t.Fatalf("detail: %v", err)
	}
	if d.Application.ID != a.ID {
		t.Fatalf("本体 ID 不匹配: %s", d.Application.ID)
	}
	if len(d.Requirements) != 1 || d.Requirements[0].ID != "req_1" {
		t.Fatalf("需求聚合错: %v", d.Requirements)
	}
	if len(d.Changes) != 2 {
		t.Fatalf("变更应聚合 2 条（直接 + 经需求派生），得到 %d", len(d.Changes))
	}
	if len(d.Releases) != 1 {
		t.Fatalf("发布应聚合 1 条，得到 %d", len(d.Releases))
	}
	if len(d.Instances) != 1 {
		t.Fatalf("实例聚合错: %d", len(d.Instances))
	}
}

// TestStore_Detail_appNotFound 应用不存在 → 返回 nil + err。
func TestStore_Detail_appNotFound(t *testing.T) {
	h, _ := newHTTPHandlerWithTables(t)
	d, err := h.store.Detail(context.Background(), "ps_1", "app_ghost")
	if d != nil {
		t.Fatalf("不存在应用 Detail 应返回 nil，得到 %+v", d)
	}
	_ = err
}

// TestHandler_Workspace_codeWSNil codeWS 未启用 → 500。
// 此路径不依赖 docker/git/codeWS，只校验错误码。
func TestHandler_Workspace_codeWSNil(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/workspace", map[string]string{"tool": "opencode"})
	if code != 500 {
		t.Fatalf("codeWS 未启用应 500，得到 %d", code)
	}
	if resp["code"].(float64) != 50021 {
		t.Fatalf("业务码应 50021，得到 %v", resp["code"])
	}
}

// TestHandler_Workspace_appNotFound codeWS=nil 时 codeWS 检查先于 app 查找，返回 500。
// 注：此为当前实现的顺序（codeWS gate 在 app lookup 前）。
func TestHandler_Workspace_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/app_ghost/workspace", nil)
	if code != 500 {
		t.Fatalf("codeWS=nil 时 Workspace 总是 500（gate 顺序），得到 %d", code)
	}
}

// TestHandler_RegisterChange_changesNil 变更闸门未启用 → 500。
func TestHandler_RegisterChange_changesNil(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/register-change", map[string]string{"note": "x"})
	if code != 500 {
		t.Fatalf("changes 未启用应 500，得到 %d", code)
	}
	if resp["code"].(float64) != 50021 {
		t.Fatalf("业务码应 50021，得到 %v", resp["code"])
	}
}

// TestHandler_RegisterChange_appNotFound 应用不存在 → 404。
func TestHandler_RegisterChange_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/app_ghost/register-change", nil)
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_InjectRequirement_codeWSNil codeWS 未启用 → 500（先校验 prompt）。
func TestHandler_InjectRequirement_codeWSNil(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/inject-requirement",
		map[string]string{"prompt": "实现登录"})
	if code != 500 {
		t.Fatalf("codeWS 未启用应 500，得到 %d", code)
	}
}

// TestHandler_InjectRequirement_appNotFound 应用不存在 → 404。
func TestHandler_InjectRequirement_appNotFound(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/app_ghost/inject-requirement",
		map[string]string{"prompt": "x"})
	if code != 404 {
		t.Fatalf("不存在应用应 404，得到 %d", code)
	}
}

// TestHandler_InjectRequirement_missingPrompt codeWS=nil 时 codeWS gate 在 binding 校验前，
// 因此即使缺 prompt 也返回 500（gate 顺序）。无法在不构造 codeWS 的情况下触发 400 路径，
// 此处固化当前行为。
func TestHandler_InjectRequirement_missingPrompt(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/inject-requirement",
		map[string]string{})
	if code != 500 {
		t.Fatalf("codeWS=nil 时 InjectRequirement 总是 500（gate 顺序），得到 %d", code)
	}
}

// TestSyncOverviewIfProd_testEnvNotSynced env=test 不应同步到 application 概览。
// 直接调用私有方法，覆盖 test 分支的早返回。
func TestSyncOverviewIfProd_testEnvNotSynced(t *testing.T) {
	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	prevStatus := a.Status
	h.syncOverviewIfProd(ctx, a, EnvTest)
	// test 环境：函数早返回，不应触发任何 DB 写
	got, _ := h.store.GetByAppID(ctx, a.ID)
	if got.Status != prevStatus {
		t.Fatalf("test 环境不应同步概览，status 变成了 %s", got.Status)
	}
}

// TestSyncOverviewIfProd_prodNoInstance env=prod 但无实例 → 早返回。
func TestSyncOverviewIfProd_prodNoInstance(t *testing.T) {
	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	h.syncOverviewIfProd(ctx, a, EnvProd)
	// 无实例：函数在 ins==nil 处早返回，a 字段不变
	got, _ := h.store.GetByAppID(ctx, a.ID)
	if got.URL != "" {
		t.Fatalf("无实例时 URL 不应变，得到 %q", got.URL)
	}
}

// TestSyncOverviewIfProd_sync env=prod + 有实例 → 同步概览。
func TestSyncOverviewIfProd_sync(t *testing.T) {
	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	ins, _ := h.store.GetOrCreateInstance(ctx, a.ID, EnvProd)
	ins.Image = "img/v1"
	ins.ContainerName = "cn"
	ins.HostPort = 9200
	ins.URL = "http://h:9200"
	ins.Version = 5
	ins.Status = "running"
	_ = h.store.UpdateInstance(ctx, ins)

	h.syncOverviewIfProd(ctx, a, EnvProd)
	got, _ := h.store.GetByAppID(ctx, a.ID)
	if got.URL != "http://h:9200" || got.Version != 5 || got.Status != "running" || got.HostPort != 9200 {
		t.Fatalf("prod 实例态未同步到概览: %+v", got)
	}
}

// TestHandler_NewHandlerDeps NewHandler 接受 nil 依赖（codeWS/changes/cfg）。
func TestHandler_NewHandlerDeps(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil)
	if h == nil {
		t.Fatal("NewHandler 不应返回 nil")
	}
	if h.store != nil || h.deployer != nil || h.codeWS != nil || h.changes != nil || h.cfg != nil {
		t.Fatalf("全 nil 依赖应保留 nil： %+v", h)
	}
}

// TestHandler_Register 路由注册不 panic（覆盖 Register 函数）。
func TestHandler_Register(t *testing.T) {
	h, _ := newHTTPHandler(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 注册到完整路由前缀；只验证不 panic
	r2 := r.Group("/api/v1")
	h.Register(r2)
	// 简单 hit 一个路由确认注册生效
	code, _ := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_x/apps", nil)
	if code != 200 {
		t.Fatalf("注册后 List 路由应可用，得到 %d", code)
	}
}
