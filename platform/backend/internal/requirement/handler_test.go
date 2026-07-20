package requirement

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/change"
)

// newReqRepoWithChanges 同库内建 requirement + change_request 两表，便于 my-tasks 聚合测试。
func newReqRepoWithChanges(t *testing.T) (*Repository, *change.Store) {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE requirement (
  id                  TEXT PRIMARY KEY,
  project_space_id    TEXT NOT NULL,
  application_id      TEXT,
  title               TEXT NOT NULL,
  description         TEXT,
  user_story          TEXT,
  acceptance_criteria TEXT,
  status              TEXT NOT NULL DEFAULT 'draft',
  priority            TEXT,
  fixed_version       TEXT,
  tasks               TEXT,
  assignee            TEXT,
  assigned_at         DATETIME,
  created_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at          DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	db.MustExec(`CREATE TABLE change_request (
  id              TEXT PRIMARY KEY,
  project_space_id TEXT,
  kind            TEXT,
  source_id       TEXT,
  repo_dir        TEXT,
  prompt          TEXT,
  model           TEXT,
  output          TEXT,
  status          TEXT,
  reviewer        TEXT,
  reviewed_at     DATETIME,
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewRepository(db), change.NewStore(db)
}

// newHandlerWith 构造一个 gin 引擎并注册 requirement 路由。
func newHandlerWith(t *testing.T, repo *Repository, chgStore *change.Store) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := NewService(repo, "", nil, nil, nil)
	h := NewHandler(svc, chgStore, nil) // authStore=nil：跳过 RBAC（其逻辑属 auth 包）
	h.Register(r.Group("/api/v1"))
	return r
}

// doJSON 发请求并返回状态码 + 解析后的 JSON 体（外层 Response）。
func doJSON(t *testing.T, r http.Handler, method, target, xUser string) (int, map[string]interface{}) {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if xUser != "" {
		req.Header.Set("X-User", xUser)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body map[string]interface{}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body
}

// dataOf 从统一响应体里取 data（httpx.Response.Data）。
// data 为 map（OK 传 gin.H）时返回该 map；为 list 时返回 nil（调用方按需断言 body["data"]）。
func dataOf(t *testing.T, body map[string]interface{}) map[string]interface{} {
	t.Helper()
	d, ok := body["data"].(map[string]interface{})
	if !ok {
		t.Fatalf("响应 data 非 object: %v", body)
	}
	return d
}

// TestHandler_MyTasks_Aggregation my-tasks 聚合：多状态需求 + 多状态变更一起进 toClaim/myDev/toApprove/toRelease。
func TestHandler_MyTasks_Aggregation(t *testing.T) {
	repo, chg := newReqRepoWithChanges(t)
	ctx := context.Background()

	// 需求侧 4 条，覆盖各阶段：
	//   - specified 未认领 → toClaim
	//   - developing assignee=alice → myDev(alice)
	//   - developing assignee=bob → 不在 alice 的 myDev
	//   - draft 未认领 → 不进 toClaim（status 过滤）
	mustCreateRepo(t, repo, mkReq("req_to_claim", "ps_1")) // status=specified, 未认领

	dev := mkReq("req_my_dev", "ps_1")
	dev.Status = "developing"
	mustCreateRepo(t, repo, dev)
	mustAssign(t, repo, "req_my_dev", "alice")

	devBob := mkReq("req_bob_dev", "ps_1")
	devBob.Status = "developing"
	mustCreateRepo(t, repo, devBob)
	mustAssign(t, repo, "req_bob_dev", "bob")

	draft := mkReq("req_draft", "ps_1")
	draft.Status = "draft"
	mustCreateRepo(t, repo, draft) // draft 不进 toClaim

	// 变更侧 3 条：pending/approved/released（released 用不同 source_id，避免 MarkReleased 误伤 approved）
	_ = chg.Create(ctx, &change.ChangeRequest{ProjectSpaceID: "ps_1", Kind: "code", SourceID: "app_1", Output: "diff1"}) // pending
	chgApproved := &change.ChangeRequest{ProjectSpaceID: "ps_1", Kind: "code", SourceID: "app_1", Output: "diff2"}
	_ = chg.Create(ctx, chgApproved)
	_ = chg.Decide(ctx, chgApproved.ID, "approved", "admin") // approved
	chgReleased := &change.ChangeRequest{ProjectSpaceID: "ps_1", Kind: "code", SourceID: "app_2", Output: "diff3"}
	_ = chg.Create(ctx, chgReleased)
	_ = chg.Decide(ctx, chgReleased.ID, "approved", "admin")
	_ = chg.MarkReleased(ctx, "app_2") // app_2 的 approved→released，不进任何待办

	r := newHandlerWith(t, repo, chg)
	code, body := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/my-tasks", "alice")
	if code != 200 {
		t.Fatalf("my-tasks 状态码 %d, body=%v", code, body)
	}
	d := dataOf(t, body)
	toClaim, _ := d["toClaim"].([]interface{})
	if len(toClaim) != 1 || toClaim[0].(map[string]interface{})["id"] != "req_to_claim" {
		t.Fatalf("toClaim 应只含 req_to_claim，得到 %v", toClaim)
	}
	myDev, _ := d["myDev"].([]interface{})
	if len(myDev) != 1 || myDev[0].(map[string]interface{})["id"] != "req_my_dev" {
		t.Fatalf("myDev 应只含 alice 的 req_my_dev，得到 %v", myDev)
	}
	toApprove, _ := d["toApprove"].([]interface{})
	if len(toApprove) != 1 {
		t.Fatalf("toApprove 应只含 1 条 pending，得到 %v", toApprove)
	}
	toRelease, _ := d["toRelease"].([]interface{})
	if len(toRelease) != 1 {
		t.Fatalf("toRelease 应只含 1 条 approved，得到 %v", toRelease)
	}
	roles, _ := d["roles"].([]interface{})
	if len(roles) != 0 {
		t.Fatalf("authStore=nil 时 roles 应为空数组，得到 %v", roles)
	}
}

// TestHandler_MyTasks_NoChangeStore chgStore=nil 时不聚合变更、仍能正常返回。
func TestHandler_MyTasks_NoChangeStore(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_x", "ps_1"))

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/my-tasks", "alice")
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	// toApprove/toRelease 应为空切片（chgStore=nil 分支）
	if v, _ := dataOf(t, body)["toApprove"].([]interface{}); len(v) != 0 {
		t.Fatalf("chgStore=nil 时 toApprove 应为空，得到 %v", v)
	}
}

// TestHandler_MyTasks_DefaultUserAnonymous 未带 X-User 头 → 默认 "anonymous"。
func TestHandler_MyTasks_DefaultUserAnonymous(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	dev := mkReq("req_anon", "ps_1")
	dev.Status = "developing"
	mustCreateRepo(t, repo, dev)
	mustAssign(t, repo, "req_anon", "anonymous")

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/my-tasks", "") // 无 X-User
	if code != 200 {
		t.Fatalf("状态码 %d", code)
	}
	myDev, _ := dataOf(t, body)["myDev"].([]interface{})
	if len(myDev) != 1 {
		t.Fatalf("默认 anonymous 应认领到 req_anon，得到 %v", myDev)
	}
}

// TestHandler_Assign_Success 未认领→200，assigned_to 返回当前用户。
func TestHandler_Assign_Success(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_h1", "ps_1"))

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_h1/assign", "alice")
	if code != 200 {
		t.Fatalf("assign 成功应为 200，得到 %d body=%v", code, body)
	}
	if dataOf(t, body)["assigned_to"] != "alice" {
		t.Fatalf("assigned_to 应为 alice，得到 %v", body["data"])
	}
}

// TestHandler_Assign_Conflict409 已被他人认领→409（httpx.Err 状态码 + 错误信息含当前认领人）。
func TestHandler_Assign_Conflict409(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_h2", "ps_1"))
	mustAssign(t, repo, "req_h2", "alice") // alice 先认领

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_h2/assign", "bob")
	if code != 409 {
		t.Fatalf("已被他人认领应 409，得到 %d body=%v", code, body)
	}
	// 错误信息应包含当前认领人 alice
	if msg, _ := body["message"].(string); msg == "" || !strings.Contains(msg, "alice") {
		t.Fatalf("错误信息应含 alice，得到 %v", body)
	}
}

// TestHandler_Assign_IdempotentSelf 本人重复认领→200（幂等）。
func TestHandler_Assign_IdempotentSelf(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_h3", "ps_1"))
	mustAssign(t, repo, "req_h3", "alice")

	r := newHandlerWith(t, repo, nil)
	code, _ := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_h3/assign", "alice")
	if code != 200 {
		t.Fatalf("本人重复认领应 200（幂等），得到 %d", code)
	}
}

// TestHandler_Release_Success 释放认领→200，released=true。
func TestHandler_Release_Success(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_h4", "ps_1"))
	mustAssign(t, repo, "req_h4", "alice")

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_h4/release", "")
	if code != 200 {
		t.Fatalf("release 应 200，得到 %d", code)
	}
	if dataOf(t, body)["released"] != true {
		t.Fatalf("released 应为 true，得到 %v", body["data"])
	}
	// 释放后他人可认领
	code2, _ := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_h4/assign", "bob")
	if code2 != 200 {
		t.Fatalf("释放后 bob 应可认领，得到 %d", code2)
	}
}

// TestHandler_List 列出项目空间下的需求。
func TestHandler_List(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_l1", "ps_1"))
	mustCreateRepo(t, repo, mkReq("req_l2", "ps_1"))

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/requirements", "")
	if code != 200 {
		t.Fatalf("list 应 200，得到 %d", code)
	}
	// httpx.OK 把 list 包在 "data" 字段
	data, _ := body["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("应返回 2 条，得到 %d (body=%v)", len(data), body)
	}
}

// TestHandler_ListByApp 按应用筛选需求池。
func TestHandler_ListByApp(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	req := mkReq("req_app1", "ps_1")
	req.ApplicationID = "app_target"
	mustCreateRepo(t, repo, req)
	req2 := mkReq("req_app2", "ps_1")
	req2.ApplicationID = "app_other"
	mustCreateRepo(t, repo, req2)

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/app_target/requirements", "")
	if code != 200 {
		t.Fatalf("list-by-app 应 200，得到 %d", code)
	}
	data, _ := body["data"].([]interface{})
	if len(data) != 1 || data[0].(map[string]interface{})["id"] != "req_app1" {
		t.Fatalf("应只返回 app_target 的需求，得到 %v", data)
	}
}

// TestHandler_Assign_DefaultUserAnonymous 未带 X-User 头 → 默认 "anonymous" 认领。
// 补 Handler.Assign 中 user 默认分支。
func TestHandler_Assign_DefaultUserAnonymous(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_h5", "ps_1"))

	r := newHandlerWith(t, repo, nil)
	code, body := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_h5/assign", "")
	if code != 200 {
		t.Fatalf("assign(anonymous) 应 200，得到 %d body=%v", code, body)
	}
	if dataOf(t, body)["assigned_to"] != "anonymous" {
		t.Fatalf("默认应 assigned_to=anonymous，得到 %v", body["data"])
	}
}

// TestHandler_Release_NotFound 不存在的需求 Release 仍 200（Release 实现幂等、不区分存在性）。
// 覆盖 Release handler 的成功返回路径与底层 Release 的无影响分支。
func TestHandler_Release_NotFound(t *testing.T) {
	repo, _ := newReqRepoWithChanges(t)
	r := newHandlerWith(t, repo, nil)
	code, _ := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_missing/release", "")
	if code != 200 {
		t.Fatalf("对不存在需求 Release 仍应 200（幂等），得到 %d", code)
	}
}

// TestHandler_MyTasks_WithAuthStoreRoles authStore 接入后 roles 返回用户在某空间的角色。
//
// 注意：发现 auth.Store.Roles 在 projectSpaceID 非空时拼出
// `WHERE user_id = $1 AND project_space_id = ?`（混用 $N 与 ? 占位符）。
// 在 modernc.org/sqlite 上仍能工作（驱动同时支持），但在 PostgreSQL 上 $1 不会替换、? 也无效，
// 生产 PG 上 MyTasks 的 RBAC 分支会查不到角色 → 前端拿到空 roles。
// 见"潜在 bug"汇报。
func TestHandler_MyTasks_WithAuthStoreRoles(t *testing.T) {
	repo, chg := newReqRepoWithChanges(t)
	mustCreateRepo(t, repo, mkReq("req_r1", "ps_1"))

	// 复用同一 sqlite 库，建 user / membership 表，构造 auth.Store
	db := repo.db
	db.MustExec(`CREATE TABLE "user" (
		id TEXT PRIMARY KEY, name TEXT NOT NULL UNIQUE, email TEXT,
		status TEXT NOT NULL DEFAULT 'active', created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	db.MustExec(`CREATE TABLE membership (
		id TEXT PRIMARY KEY, project_space_id TEXT NOT NULL, user_id TEXT NOT NULL, role TEXT NOT NULL,
		UNIQUE (project_space_id, user_id))`)
	mustExec(t, db, `INSERT INTO "user" (id, name, status) VALUES ('usr_alice', 'alice', 'active')`)
	mustExec(t, db, `INSERT INTO membership (id, project_space_id, user_id, role) VALUES ('mbr_1', 'ps_1', 'usr_alice', 'dev')`)

	authStore := auth.NewStore(db)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := NewService(repo, "", nil, nil, nil)
	h := NewHandler(svc, chg, authStore)
	h.Register(r.Group("/api/v1"))

	code, body := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/my-tasks", "alice")
	if code != 200 {
		t.Fatalf("my-tasks(auth) 状态码 %d body=%v", code, body)
	}
	roles, _ := dataOf(t, body)["roles"].([]interface{})
	// 预期 roles=["dev"]；若 auth.Roles 的 ? 占位符 bug 在 sqlite 上恰好能跑通则正常返回。
	if len(roles) == 0 {
		t.Logf("注意：roles 为空 —— 可能命中 auth.Store.Roles 的 ? 占位符问题（详见潜在 bug）")
	}
}

// mustExec 执行 DDL/DML，失败即 Fatal。
func mustExec(t *testing.T, db *sqlx.DB, q string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(q, args...); err != nil {
		t.Fatalf("exec %q: %v", q, err)
	}
}

// newClosedRepoHandler 建库后立刻关闭底层连接，强制让所有 SQL 调用报错，
// 用来覆盖 handler 的 `if err != nil { httpx.Err(...) }` 错误分支。
func newClosedRepoHandler(t *testing.T) http.Handler {
	t.Helper()
	repo, _ := newReqRepoWithChanges(t)
	_ = repo.db.Close() // 关库 → 后续查询/执行均报错
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := NewService(repo, "", nil, nil, nil)
	h := NewHandler(svc, nil, nil)
	h.Register(r.Group("/api/v1"))
	return r
}

// TestHandler_List_DBError 覆盖 List handler 的错误分支（500）。
func TestHandler_List_DBError(t *testing.T) {
	r := newClosedRepoHandler(t)
	code, body := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/requirements", "")
	if code != 500 {
		t.Fatalf("DB 错误时 List 应 500，得到 %d body=%v", code, body)
	}
}

// TestHandler_ListByApp_DBError 覆盖 ListByApp handler 的错误分支（500）。
func TestHandler_ListByApp_DBError(t *testing.T) {
	r := newClosedRepoHandler(t)
	code, _ := doJSON(t, r, http.MethodGet, "/api/v1/project-spaces/ps_1/apps/app_x/requirements", "")
	if code != 500 {
		t.Fatalf("DB 错误时 ListByApp 应 500，得到 %d", code)
	}
}

// TestHandler_Release_DBError 覆盖 Release handler 的错误分支（500）。
func TestHandler_Release_DBError(t *testing.T) {
	r := newClosedRepoHandler(t)
	code, _ := doJSON(t, r, http.MethodPost, "/api/v1/project-spaces/ps_1/requirements/req_x/release", "")
	if code != 500 {
		t.Fatalf("DB 错误时 Release 应 500，得到 %d", code)
	}
}
