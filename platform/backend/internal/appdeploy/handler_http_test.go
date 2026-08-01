package appdeploy

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/requirement"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newHTTPHandler 建一个完整 Handler（store=anp_test PG，deployer=空 host）。
// 注：deployer 字段在多数 store-only 接口（List/Detail/Env/RepoDocs/RepoFile/Stats deployed=false）
// 上不会被调用，因此即使指向真实 Deployer 也不会触发 docker。
// 替代 sqlite :memory:（sqlite 漏 PG 类型 bug，如 is_secret BOOLEAN→INTEGER；见 memory sqlite-test-pg-type-trap）。
func newHTTPHandler(t *testing.T) (*Handler, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"release_record", "change_request", "requirement",
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
	)
	store := NewStore(db)
	h := NewHandler(store, NewDeployer("test"), nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	return h, db
}

// newRouterWith 注册路由到 gin 引擎。
// 注入 admin 角色：env 敏感的部署操作（Deploy/Stop/Start/Delete）按 roles 自鉴权，
// 这些测试本意是验 404/400（应用不存在/未部署）等业务逻辑而非 RBAC 拒绝，故给足权限直达目标路径
// （补 deploy 权限分离工作漏更 test fixture 的预存缺口——否则一律 403，到不了被测分支）。
func newRouterWith(h *Handler) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("roles", []string{"admin"}); c.Next() })
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

// TestHandler_Promote_forbidden 非 gatekeeper/admin → 403(部署权限分离)。
// newRouterWith 固定注入 admin,此处 inline 一个空 roles 的 router 覆盖。
func TestHandler_Promote_forbidden(t *testing.T) {
	h, _ := newHTTPHandlerWithGates(t)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("roles", []string{}); c.Next() })
	h.Register(r.Group("/api/v1"))
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/promote", nil)
	if code != 403 {
		t.Fatalf("非 admin 应 403,得到 %d body=%v", code, resp)
	}
}

// TestHandler_Promote_changeGateRejected 登记变更未审批 → 409/40920(变更闸门)。
func TestHandler_Promote_changeGateRejected(t *testing.T) {
	h, db := newHTTPHandlerWithGates(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		 VALUES ('chg_1', 'ps_1', $1, 'code', 'diff', 'pending')`, a.ID); err != nil {
		t.Fatalf("seed change: %v", err)
	}
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/promote", nil)
	if code != 409 {
		t.Fatalf("未审批变更应 409,得到 %d", code)
	}
	if resp["code"].(float64) != 40920 {
		t.Fatalf("业务码应 40920(变更闸门),得到 %v", resp["code"])
	}
}

// TestHandler_Promote_deliveredGateRejected approved 变更 + 来源需求未 delivered → 409/40921(AC7 核心)。
func TestHandler_Promote_deliveredGateRejected(t *testing.T) {
	h, db := newHTTPHandlerWithGates(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO requirement (id, project_space_id, application_id, title, status)
		 VALUES ('req_1', 'ps_1', $1, 't', 'developing')`, a.ID); err != nil {
		t.Fatalf("seed req: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		 VALUES ('chg_1', 'ps_1', 'req_1', 'code', 'diff', 'approved')`); err != nil {
		t.Fatalf("seed change: %v", err)
	}
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/promote", nil)
	if code != 409 {
		t.Fatalf("approved+未delivered 应 409,得到 %d body=%v", code, resp)
	}
	if resp["code"].(float64) != 40921 {
		t.Fatalf("业务码应 40921(AC7 delivered),得到 %v", resp["code"])
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

// TestHandler_DeployProd_forbidden 非 gatekeeper/admin → 403(app.deploy.prod 拒绝)。
// newRouterWith 固定注入 admin,此处 inline 一个空 roles 的 router 覆盖。
// 对称 TestHandler_Promote_forbidden,确认 Deploy env=prod 同样按部署权限分离鉴权。
func TestHandler_DeployProd_forbidden(t *testing.T) {
	h, _ := newHTTPHandlerWithGates(t)
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("roles", []string{}); c.Next() })
	h.Register(r.Group("/api/v1"))
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/deploy", map[string]string{"env": "prod"})
	if code != 403 {
		t.Fatalf("非 admin 应 403,得到 %d body=%v", code, resp)
	}
}

// TestHandler_DeployProd_changeGateRejected 登记变更未审批 → 409/40920(变更闸门)。
// 对称 TestHandler_Promote_changeGateRejected,确认 /deploy env=prod 同走变更闸门。
func TestHandler_DeployProd_changeGateRejected(t *testing.T) {
	h, db := newHTTPHandlerWithGates(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		 VALUES ('chg_1', 'ps_1', $1, 'code', 'diff', 'pending')`, a.ID); err != nil {
		t.Fatalf("seed change: %v", err)
	}
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/deploy", map[string]string{"env": "prod"})
	if code != 409 {
		t.Fatalf("未审批变更应 409,得到 %d", code)
	}
	if resp["code"].(float64) != 40920 {
		t.Fatalf("业务码应 40920(变更闸门),得到 %v", resp["code"])
	}
}

// TestHandler_DeployProd_deliveredGateRejected approved 变更 + 来源需求未 delivered → 409/40921。
// 对称 TestHandler_Promote_deliveredGateRejected(AC7):堵 /deploy env=prod 绕过 /promote 的 delivered 漏检。
func TestHandler_DeployProd_deliveredGateRejected(t *testing.T) {
	h, db := newHTTPHandlerWithGates(t)
	r := newRouterWith(h)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	if _, err := db.ExecContext(ctx,
		`INSERT INTO requirement (id, project_space_id, application_id, title, status)
		 VALUES ('req_1', 'ps_1', $1, 't', 'developing')`, a.ID); err != nil {
		t.Fatalf("seed req: %v", err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		 VALUES ('chg_1', 'ps_1', 'req_1', 'code', 'diff', 'approved')`); err != nil {
		t.Fatalf("seed change: %v", err)
	}
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/deploy", map[string]string{"env": "prod"})
	if code != 409 {
		t.Fatalf("approved+未delivered 应 409,得到 %d body=%v", code, resp)
	}
	if resp["code"].(float64) != 40921 {
		t.Fatalf("业务码应 40921(对称 AC7 delivered),得到 %v", resp["code"])
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
	_ = h.store.UpsertEnv(ctx, a.ID, "PUBLIC_KEY", "visible", false, "user")
	_ = h.store.UpsertEnv(ctx, a.ID, "SECRET_TOKEN", "top-secret-value", true, "user")

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
	_ = h.store.UpsertEnv(ctx, a.ID, "K", "v", false, "user")
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

// newHTTPHandlerWithGates 同 newHTTPHandler,但注入 changes+reqRepo,
// 供 Promote 变更闸门(40920)/AC7 delivered 前置(40921)测试——这两个依赖真实 Store/Repository。
// 不复用 newHTTPHandler:后者 changes=nil 被 TestHandler_RegisterChange_changesNil 的 nil-gate 测试依赖。
func newHTTPHandlerWithGates(t *testing.T) (*Handler, *sqlx.DB) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"release_record", "change_request", "requirement",
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
	)
	store := NewStore(db)
	changes := change.NewStore(db)
	reqRepo := requirement.NewRepository(db)
	h := NewHandler(store, NewDeployer("test"), nil, changes, nil, reqRepo, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	return h, db
}

// TestStore_Detail_Aggregation Detail 聚合：本体 + 需求/变更/发布/实例。
// 验证 source_id=appID 与 source_id=reqID（属同 app）的变更都能被聚合。
func TestStore_Detail_Aggregation(t *testing.T) {
	h, db := newHTTPHandlerWithTables(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/no-git")
	// 需求（application_id 关联）
	_, _ = db.ExecContext(ctx, `INSERT INTO requirement (id, project_space_id, application_id, title, status, priority)
		VALUES ('req_1', 'ps_1', $1, '需求1', 'specified', 'P0')`, a.ID)
	// 变更 1：source_id 直接 = appID
	_, _ = db.ExecContext(ctx, `INSERT INTO change_request (id, project_space_id, source_id, kind, output, status)
		VALUES ('chg_1', 'ps_1', $1, 'code', 'diff1', 'pending')`, a.ID)
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

// TestHandler_NewHandlerDeps NewHandler 接受 nil 依赖（codeWS/changes/cfg/provisioner/standards）。
func TestHandler_NewHandlerDeps(t *testing.T) {
	h := NewHandler(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	if h == nil {
		t.Fatal("NewHandler 不应返回 nil")
	}
	if h.store != nil || h.deployer != nil || h.codeWS != nil || h.changes != nil || h.cfg != nil || h.standards != nil {
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

// extRouteStore 采集 Create external 时 routeWriter.UpsertExternalRoute 的入参，
// 验证 appdeploy handler 调对了 external 分支（而不是 managed 的 UpsertRoute）。
type extRouteStore struct {
	called    bool
	gotAppID  string
	gotEnv    string
	gotExtURL string
}

func (s *extRouteStore) UpsertRoute(_ context.Context, _, _, _ string, _ string, _ int) error {
	return nil
}
func (s *extRouteStore) UpsertExternalRoute(_ context.Context, appID, _, env, extURL string) error {
	s.called = true
	s.gotAppID = appID
	s.gotEnv = env
	s.gotExtURL = extURL
	return nil
}
func (s *extRouteStore) DeleteRouteByApp(_ context.Context, _ string) error { return nil }

// newHTTPHandlerWithExtRoute 同 newHTTPHandler，但注入 extRouteStore 以观测 Create external 调用。
func newHTTPHandlerWithExtRoute(t *testing.T) (*Handler, *extRouteStore) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"release_record", "change_request", "requirement",
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
	)
	store := NewStore(db)
	rw := &extRouteStore{}
	h := NewHandler(store, NewDeployer("test"), nil, nil, nil, nil, nil, rw, nil, nil, nil, nil, nil, "", nil, nil)
	return h, rw
}

// TestHandler_CreateExternal external 模式注册：落库 + 调 UpsertExternalRoute + 不调 EnsureRepo。
// 不走 managed 的 EnsureRepo/git/provision，验证 external 分支独立工作。
func TestHandler_CreateExternal(t *testing.T) {
	h, rw := newHTTPHandlerWithExtRoute(t)
	r := newRouterWith(h)
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name":         "存量ERP",
		"deploy_mode":  "external",
		"external_url": "http://10.10.0.28:8088",
	})
	if code != 201 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("应返回应用本体，得到 %v", resp)
	}
	if data["deploy_mode"] != "external" {
		t.Fatalf("deploy_mode 应 external，得到 %v", data["deploy_mode"])
	}
	if data["external_url"] != "http://10.10.0.28:8088" {
		t.Fatalf("external_url 不匹配: %v", data["external_url"])
	}
	if data["status"] != "running" {
		t.Fatalf("external 应用应 running，得到 %v", data["status"])
	}
	appID, _ := data["id"].(string)
	if appID == "" {
		t.Fatal("应自动生成 app id")
	}
	// UpsertExternalRoute 应被调一次（external_url + env=prod）
	if !rw.called {
		t.Fatal("Create external 应调 routeWriter.UpsertExternalRoute")
	}
	if rw.gotAppID != appID || rw.gotEnv != "prod" || rw.gotExtURL != "http://10.10.0.28:8088" {
		t.Fatalf("UpsertExternalRoute 入参不符: %+v", rw)
	}
	// 落库回读验证（不带 managed 的 repo_dir/internal_port）
	got, _ := h.store.GetByAppID(context.Background(), appID)
	if got == nil || got.DeployMode != AppExternal {
		t.Fatalf("落库 external 应用读不到或字段错: %+v", got)
	}
	if got.RepoDir != "" || got.InternalPort != 0 {
		t.Fatalf("external 应用不应建 repo_dir/internal_port: %+v", got)
	}
}

// TestHandler_CreateExternal_BadURL external_url 非法 → 400（不落库、不调 route）。
func TestHandler_CreateExternal_BadURL(t *testing.T) {
	h, rw := newHTTPHandlerWithExtRoute(t)
	r := newRouterWith(h)
	cases := []struct {
		url  string
		desc string
	}{
		{"", "空串（必填）"},
		{"not-a-url", "无 scheme"},
		{"ftp://h/p", "非 http(s) scheme"},
		{"http://", "缺 host"},
	}
	for _, c := range cases {
		code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
			"name": "bad-app", "deploy_mode": "external", "external_url": c.url,
		})
		if code != 400 {
			t.Fatalf("%s: 应 400，得到 %d", c.desc, code)
		}
	}
	if rw.called {
		t.Fatal("非法 URL 时不应调 UpsertExternalRoute")
	}
}

// --- Import 端点（JSON：git/dir）测试 ---
// importMultipart 不需要；本任务测 JSON 端点。helper: 覆盖 ManagedRepoBase 到临时目录 + 建本地源仓。
func withImportRepoBase(t *testing.T) (string, func()) {
	t.Helper()
	old := ManagedRepoBase
	base := t.TempDir()
	ManagedRepoBase = base
	// 白名单放开到 base（dir 来源测试用）
	oldRoots := AllowedDirRoots
	AllowedDirRoots = []string{base + "/"}
	return base, func() { ManagedRepoBase = old; AllowedDirRoots = oldRoots }
}

// waitImport 轮询 app 至非 importing（registered/failed），最多 8s。
func waitImport(t *testing.T, h *Handler, appID string) *Application {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := h.store.GetByAppID(context.Background(), appID)
		if got != nil && got.Status != StatusImporting {
			return got
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("导入超时未完成 app=%s", appID)
	return nil
}

// TestHandler_Import_Git git 来源：占位 importing → 异步 clone 本地源仓 → registered。
func TestHandler_Import_Git(t *testing.T) {
	h, _ := newHTTPHandler(t)
	base, restore := withImportRepoBase(t)
	defer restore()
	// 建本地源仓作 git_url；源仓须在 base 下（白名单已放开到 base，本地 git_url 走 isUnderAllowedRoot）
	src := filepath.Join(base, "src")
	makeLocalGitRepo(t, src)

	r := newRouterWith(h)
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/import/apps",
		map[string]interface{}{"source": "git", "name": "legacy-api", "git_url": src})
	if code != 201 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	appID, _ := data["id"].(string)
	if appID == "" {
		t.Fatalf("未返回 app id: %v", data)
	}
	got := waitImport(t, h, appID)
	if got.Status != "registered" {
		t.Fatalf("git 导入应 registered，得到 %q err=%q", got.Status, got.LastError)
	}
	if got.ImportSource != "git" {
		t.Fatalf("import_source 应 git，得到 %q", got.ImportSource)
	}
}

// TestHandler_Import_BadURL git_url 非法 → 400。
func TestHandler_Import_BadURL(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/import/apps",
		map[string]interface{}{"source": "git", "name": "bad", "git_url": "not-a-url"})
	if code != 400 {
		t.Fatalf("非法 git_url 应 400，得到 %d body=%v", code, resp)
	}
}

// TestHandler_Import_NameConflict 同空间同名 → 409。
func TestHandler_Import_NameConflict(t *testing.T) {
	h, _ := newHTTPHandler(t)
	_, restore := withImportRepoBase(t)
	defer restore()
	seedApp(t, h, "ps_1", "dup", "/tmp/dup") // 已存在
	r := newRouterWith(h)
	code, _ := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/import/apps",
		map[string]interface{}{"source": "git", "name": "dup", "git_url": "/tmp/x"})
	if code != 409 {
		t.Fatalf("同名应 409，得到 %d", code)
	}
}

// TestHandler_Import_TokenRedaction clone 失败时 last_error 须脱敏不含 auth_token（PRD §8 安全路径）。
// 用一个存在但非 git 仓的目录作 git_url 强制 clone 失败；token 经 runImport 闭包不落库，
// 失败信息写入前应 ReplaceAll 脱敏。验空 token 不乱码 + 非空 token 不泄露两条路径。
func TestHandler_Import_TokenRedaction(t *testing.T) {
	h, _ := newHTTPHandler(t)
	base, restore := withImportRepoBase(t)
	defer restore()
	// 存在但非 git 仓的目录（须在白名单 base 下）→ clone 必失败
	notGit := filepath.Join(base, "notgit")
	if err := os.MkdirAll(notGit, 0755); err != nil {
		t.Fatalf("mkdir notgit: %v", err)
	}
	r := newRouterWith(h)
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/import/apps",
		map[string]interface{}{"source": "git", "name": "redact", "git_url": notGit, "auth_token": "SECRET-TOKEN"})
	if code != 201 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	appID, _ := data["id"].(string)
	got := waitImport(t, h, appID)
	if got.Status != "failed" {
		t.Fatalf("应 failed（非 git 仓 clone 失败），得到 %q", got.Status)
	}
	if strings.Contains(got.LastError, "SECRET-TOKEN") {
		t.Fatalf("last_error 须脱敏不含 token，得到 %q", got.LastError)
	}
}

// TestHandler_Stats_External external 应用 Stats 返回 deployed=true + health（按 external_url 探活）。
// 不查实例、不调 docker；external_url 指向 httptest 假上游以测 up 状态。
func TestHandler_Stats_External(t *testing.T) {
	// 启 httptest 假外部应用（200 → health=up）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	h, _ := newHTTPHandlerWithExtRoute(t)
	r := newRouterWith(h)
	// 通过 HTTP 创建 external 应用（走完整 Create 分支，含 route 写入）
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name":         "外部应用",
		"deploy_mode":  "external",
		"external_url": srv.URL,
	})
	if code != 201 {
		t.Fatalf("create external: 状态码 %d body=%v", code, resp)
	}
	appID, _ := resp["data"].(map[string]interface{})["id"].(string)

	// Stats：应返回 deployed=true + external=true + health=up
	code, resp = doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+appID+"/stats?env=prod", nil)
	if code != 200 {
		t.Fatalf("stats 状态码 %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["deployed"] != true {
		t.Fatalf("external 应用应 deployed=true，得到 %v", data["deployed"])
	}
	if data["external"] != true {
		t.Fatalf("应返回 external=true 标识，得到 %v", data["external"])
	}
	if data["url"] != srv.URL {
		t.Fatalf("应回显 external_url，得到 %v", data["url"])
	}
	if data["health"] != "up" {
		t.Fatalf("健康应 up，得到 %v", data["health"])
	}
}

// --- ImportUpload 端点（multipart zip）测试 ---

// doMultipart 构造 multipart/form-data 请求（file 字段 + 表单字段），返回状态码与 body。
func doMultipart(t *testing.T, r http.Handler, target, fileField, fileName string, fileContent []byte, fields map[string]string) (int, map[string]interface{}) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = mw.WriteField(k, v)
	}
	fw, err := mw.CreateFormFile(fileField, fileName)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = fw.Write(fileContent)
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, target, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var resp map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	return w.Code, resp
}

// TestHandler_ImportUpload zip 上传 → 异步解压 → registered。
func TestHandler_ImportUpload(t *testing.T) {
	h, _ := newHTTPHandler(t)
	_, restore := withImportRepoBase(t)
	defer restore()
	zipBytes, _ := writeZip(t, map[string]string{"index.html": "<h1>hi</h1>"})

	r := newRouterWith(h)
	code, resp := doMultipart(t, r, "/api/v1/project-spaces/ps_1/import/apps/upload",
		"file", "app.zip", zipBytes, map[string]string{"name": "zipapp"})
	if code != 201 {
		t.Fatalf("状态码 %d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	appID, _ := data["id"].(string)
	got := waitImport(t, h, appID)
	if got.Status != "registered" {
		t.Fatalf("zip 导入应 registered，得到 %q err=%q", got.Status, got.LastError)
	}
	if got.ImportSource != "dir" {
		t.Fatalf("zip 归 import_source=dir，得到 %q", got.ImportSource)
	}
}

// TestHandler_ImportUpload_NoFile 未带 file → 400。
func TestHandler_ImportUpload_NoFile(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWith(h)
	// 空表单（无 file）
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("name", "nofile")
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/project-spaces/ps_1/import/apps/upload", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("未带 file 应 400，得到 %d", w.Code)
	}
}

// newRouterWithUser 同 newRouterWith 但注入 CtxUserID（git 接口按 user 定位 worktree）。
func newRouterWithUser(h *Handler, user string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("roles", []string{"admin"})
		c.Set(auth.CtxUserID, user)
		c.Next()
	})
	h.Register(r.Group("/api/v1"))
	return r
}

// makeWorktree 在 dir 下建 dev-<user> worktree（供 git 接口 happy path）。
func makeWorktree(t *testing.T, dir, user string) string {
	t.Helper()
	wt := filepath.Join(dir, ".worktrees", user)
	runGit(context.Background(), dir, "worktree", "add", "-b", "dev-"+user, wt, "HEAD")
	return wt
}

// TestHandler_GitStatus_NoWorktree 未认领需求（无 worktree）→ worktree_exists=false 不报错。
func TestHandler_GitStatus_NoWorktree(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWithUser(h, "alice")
	dir := t.TempDir()
	makeLocalGitRepo(t, dir)
	a := seedApp(t, h, "ps_1", "gw", dir)

	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/git-status", nil)
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["worktree_exists"] != false {
		t.Fatalf("无 worktree 应 worktree_exists=false，得到 %v", data["worktree_exists"])
	}
}

// TestHandler_GitStatus_Worktree worktree 有改动 → changes 含修改文件、commits 非空、branch=dev-alice。
func TestHandler_GitStatus_Worktree(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWithUser(h, "alice")
	dir := t.TempDir()
	makeLocalGitRepo(t, dir)
	wt := makeWorktree(t, dir, "alice")
	_ = os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("changed"), 0o644)
	a := seedApp(t, h, "ps_1", "gw2", dir)

	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/git-status", nil)
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["worktree_exists"] != true {
		t.Fatalf("应 worktree_exists=true，得到 %v", data["worktree_exists"])
	}
	if data["branch"] != "dev-alice" {
		t.Fatalf("branch 应 dev-alice，得到 %v", data["branch"])
	}
	changes, _ := data["changes"].([]interface{})
	if len(changes) == 0 {
		t.Fatalf("changes 应含 hello.txt 修改，得到 %v", changes)
	}
	first, _ := changes[0].(map[string]interface{})
	if first["path"] != "hello.txt" || first["status"] != "M" {
		t.Fatalf("首条应 hello.txt/M，得到 %v", first)
	}
	commits, _ := data["commits"].([]interface{})
	if len(commits) == 0 {
		t.Fatalf("commits 不应空，得到 %v", commits)
	}
}

// TestHandler_FileDiff 工作区文件 diff 含 +changed。
func TestHandler_FileDiff(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWithUser(h, "alice")
	dir := t.TempDir()
	makeLocalGitRepo(t, dir)
	wt := makeWorktree(t, dir, "alice")
	_ = os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("changed"), 0o644)
	a := seedApp(t, h, "ps_1", "fd", dir)

	target := "/api/v1/project-spaces/ps_1/apps/" + a.ID + "/file-diff?path=hello.txt"
	code, resp := doReq(t, r, http.MethodGet, target, nil)
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if !strings.Contains(data["diff"].(string), "+changed") {
		t.Fatalf("diff 应含 +changed，得到 %q", data["diff"])
	}
}

// TestHandler_Commit 留空 message → 走 AI 兜底文案提交，返回 sha。
func TestHandler_Commit(t *testing.T) {
	h, _ := newHTTPHandler(t)
	r := newRouterWithUser(h, "alice")
	dir := t.TempDir()
	makeLocalGitRepo(t, dir)
	wt := makeWorktree(t, dir, "alice")
	_ = os.WriteFile(filepath.Join(wt, "hello.txt"), []byte("changed"), 0o644)
	a := seedApp(t, h, "ps_1", "ct", dir)

	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+a.ID+"/commit", map[string]interface{}{})
	if code != 200 {
		t.Fatalf("code=%d body=%v", code, resp)
	}
	// 提交后 worktree 应无未提交改动
	changes, _ := StatusFiles(context.Background(), wt)
	if len(changes) != 0 {
		t.Fatalf("提交后应无未提交改动，得到 %v", changes)
	}
}

// --- Task 10: 非 web 应用构建产物链路测试 ---

// setupHandlerWithAppKind 建 Handler（store=anp_test PG）+ 装配构建产物链路
// （ArtifactStore/BuildConfigStore/LocalArtifactStorage），路由注册到 gin。
// 建表 SQL 由 testutil.TestDB 跑迁移 000022 提供（app_kind 列 + appdeploy_artifact + appdeploy_build_config）。
func setupHandlerWithAppKind(t *testing.T) (*Handler, *gin.Engine) {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db,
		"release_record", "change_request", "requirement",
		"appdeploy_env", "appdeploy_instance", "appdeploy_application",
		"appdeploy_artifact", "appdeploy_build_config",
	)
	store := NewStore(db)
	h := NewHandler(store, NewDeployer("test"), nil, nil, nil, nil, nil, nil, nil, nil,
		NewBuildConfigStore(db), NewArtifactStore(db), NewLocalArtifactStorage(t.TempDir()), "", nil, nil)
	return h, newRouterWith(h)
}

// withManagedRepoBaseTmp 临时把 ManagedRepoBase 指向 t.TempDir()，让 Create managed 的
// EnsureRepo 在测试可写目录建仓（默认 /data/repos 在本机/CI 可能不存在）。
func withManagedRepoBaseTmp(t *testing.T) func() {
	t.Helper()
	old := ManagedRepoBase
	ManagedRepoBase = t.TempDir()
	return func() { ManagedRepoBase = old }
}

// TestHandler_Create_WithAppKind Create 带 app_kind=desktop 落库后回读 app_kind=desktop。
// managed 模式触发 EnsureRepo，故覆盖 ManagedRepoBase 到临时目录。
func TestHandler_Create_WithAppKind(t *testing.T) {
	h, r := setupHandlerWithAppKind(t)
	restore := withManagedRepoBaseTmp(t)
	defer restore()
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name": "deskapp", "deploy_mode": "managed", "app_kind": "desktop",
	})
	if code != 201 {
		t.Fatalf("code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data == nil {
		t.Fatalf("应返回应用本体，得到 %v", resp)
	}
	if data["app_kind"] != "desktop" {
		t.Fatalf("app_kind 应 desktop，得到 %v", data["app_kind"])
	}
	appID, _ := data["id"].(string)
	got, _ := h.store.GetByAppID(context.Background(), appID)
	if got == nil || got.AppKind != AppKindDesktop {
		t.Fatalf("落库 app_kind 不匹配: %+v", got)
	}
}

// TestHandler_Create_DefaultAppKindWeb 空 app_kind 默认 web（向后兼容存量应用）。
func TestHandler_Create_DefaultAppKindWeb(t *testing.T) {
	_, r := setupHandlerWithAppKind(t)
	restore := withManagedRepoBaseTmp(t)
	defer restore()
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name": "webapp", "deploy_mode": "managed",
	})
	if code != 201 {
		t.Fatalf("code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	if data["app_kind"] != AppKindWeb {
		t.Fatalf("空 app_kind 应默认 web，得到 %v", data["app_kind"])
	}
}

// TestHandler_Create_InvalidAppKind 非法 app_kind → 400（不落库）。
func TestHandler_Create_InvalidAppKind(t *testing.T) {
	_, r := setupHandlerWithAppKind(t)
	restore := withManagedRepoBaseTmp(t)
	defer restore()
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name": "badapp", "deploy_mode": "managed", "app_kind": "game",
	})
	if code != 400 {
		t.Fatalf("非法 app_kind 应 400，得到 %d body=%v", code, resp)
	}
}

// TestHandler_ListArtifacts_Empty 已建应用无产物时 ListArtifacts 返回空列表（不报错）。
// 验证路由注册 + artifactStore 装配后空查询正常返回。
func TestHandler_ListArtifacts_Empty(t *testing.T) {
	_, r := setupHandlerWithAppKind(t)
	restore := withManagedRepoBaseTmp(t)
	defer restore()
	// 建一个 desktop 应用（name 须 ≥2 字符过 validateAppName）
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name": "desk", "app_kind": "desktop",
	})
	if code != 201 {
		t.Fatalf("建应用 code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	appID, _ := data["id"].(string)
	if appID == "" {
		t.Fatalf("未返回 app id: %v", data)
	}
	// 列产物：空列表
	code, resp = doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/"+appID+"/artifacts", nil)
	if code != 200 {
		t.Fatalf("ListArtifacts code=%d body=%v", code, resp)
	}
	d, _ := resp["data"].(map[string]interface{})
	arts, _ := d["artifacts"].([]interface{})
	if len(arts) != 0 {
		t.Fatalf("无产物应空列表，得到 %v", arts)
	}
}

// TestHandler_ListArtifacts_NotConfigured artifactStore 未注入（nil）→ 500 "功能未配置"。
// 验证 nil-safe：Task 13 前未装配不应 panic。
func TestHandler_ListArtifacts_NotConfigured(t *testing.T) {
	h, _ := newHTTPHandler(t) // artifactStore=nil
	r := newRouterWith(h)
	code, resp := doReq(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/app_ghost/artifacts", nil)
	if code != 500 {
		t.Fatalf("未装配应 500，得到 %d body=%v", code, resp)
	}
}

// TestHandler_BuildArtifacts_WebRejected web 应用调构建产物接口 → 400（web 走部署流程）。
func TestHandler_BuildArtifacts_WebRejected(t *testing.T) {
	_, r := setupHandlerWithAppKind(t)
	restore := withManagedRepoBaseTmp(t)
	defer restore()
	// 建 web 应用（默认）
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name": "webapp",
	})
	if code != 201 {
		t.Fatalf("建应用 code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	appID, _ := data["id"].(string)
	code, resp = doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+appID+"/build-artifacts", nil)
	if code != 400 {
		t.Fatalf("web 应用构建产物应 400，得到 %d body=%v", code, resp)
	}
}

// TestHandler_BuildArtifacts_ExternalRejected external 纳管应用调构建产物 → 400（I-5）。
// external 应用无 RepoDir/托管源码，构建会失败翻转状态；须在 BuildArtifacts 开头拒绝。
// app_kind 与 deploy_mode 正交：external + desktop 仍走此拒绝分支。
func TestHandler_BuildArtifacts_ExternalRejected(t *testing.T) {
	h, r := setupHandlerWithAppKind(t)
	// 建 external + desktop 应用（external 不触发 EnsureRepo/ManagedRepo，无需覆盖 repo base）
	code, resp := doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps", map[string]interface{}{
		"name":         "extdesk",
		"deploy_mode":  "external",
		"app_kind":     "desktop",
		"external_url": "http://ext.example.com/app",
	})
	if code != 201 {
		t.Fatalf("建应用 code=%d body=%v", code, resp)
	}
	data, _ := resp["data"].(map[string]interface{})
	appID, _ := data["id"].(string)
	if appID == "" {
		t.Fatalf("未返回 app id: %v", data)
	}
	if data["app_kind"] != "desktop" {
		t.Fatalf("app_kind 应 desktop，得到 %v", data["app_kind"])
	}
	// 调构建产物接口：须 400 拒绝，不进入 building/dispatch 分支（状态保持 running 不翻转）
	code, resp = doReq(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/apps/"+appID+"/build-artifacts", nil)
	if code != 400 {
		t.Fatalf("external 应用构建产物应 400，得到 %d body=%v", code, resp)
	}
	// 验证状态未被翻转为 building/failed（external 创建即 running）
	got, _ := h.store.GetByAppID(context.Background(), appID)
	if got == nil || got.Status != "running" {
		t.Fatalf("external 应用状态应保持 running，得到 %+v", got)
	}
}

// TestValidAppKind 应用形态合法性校验纯函数。
func TestValidAppKind(t *testing.T) {
	for _, k := range []string{AppKindWeb, AppKindDesktop, AppKindMobile, AppKindCLI, AppKindService} {
		if !validAppKind(k) {
			t.Fatalf("合法 app_kind %q 应 true", k)
		}
	}
	for _, bad := range []string{"", "game", "WEB", "desktop ", "cli/app"} {
		if validAppKind(bad) {
			t.Fatalf("非法 app_kind %q 应 false", bad)
		}
	}
}
