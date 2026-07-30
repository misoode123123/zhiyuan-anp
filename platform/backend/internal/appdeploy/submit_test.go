package appdeploy

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/requirement"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newSubmitDB 连 anp_test PG（testutil 跑迁移建平台全表）+ 清 submit/merge 测试涉及的 3 表。
// 替代 sqlite :memory:（sqlite SetMaxOpenConns(1) 保同库；PG 连接池天然共享库，无需限制）。
func newSubmitDB(t *testing.T) *sqlx.DB {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "change_request", "requirement", "appdeploy_application")
	return db
}

// setupSubmit 建 app + requirement,返回 Handler(mock checkFn)。
// ac=验收标准(空=需求无标准);makeWorktree=是否建 .worktrees/<user>/ 目录。
func setupSubmit(t *testing.T, ac, user string, makeWorktree bool, check checkFunc) (*Handler, *sqlx.DB) {
	t.Helper()
	db := newSubmitDB(t)
	repoDir := t.TempDir()
	if makeWorktree {
		wt := filepath.Join(repoDir, ".worktrees", sanitizeID(user))
		if err := os.MkdirAll(wt, 0o755); err != nil {
			t.Fatalf("mkdir worktree: %v", err)
		}
		_ = os.WriteFile(filepath.Join(wt, "main.go"), []byte("package main\n"), 0o644)
	}
	if _, err := db.Exec(`INSERT INTO appdeploy_application (id, project_space_id, name, repo_dir, status) VALUES ('app_1', 'ps_1', 'demo', $1, 'registered')`, repoDir); err != nil {
		t.Fatalf("insert app: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO requirement (id, project_space_id, application_id, title, description, user_story, acceptance_criteria, status) VALUES ('req_1', 'ps_1', 'app_1', '登录页', '', '', $1, 'developing')`, ac); err != nil {
		t.Fatalf("insert req: %v", err)
	}
	h := NewHandler(NewStore(db), nil, nil, change.NewStore(db), nil, requirement.NewRepository(db), nil, nil, nil, nil, nil, nil, nil, "", nil, nil)
	h.checkFn = check
	return h, db
}

func doSubmit(h *Handler, body, user string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟 AuthUser:X-User 头 → CtxUserID(撤回退后 handler 读 CtxUserID)
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-User"); u != "" {
			c.Set("user_id", u)
		}
		c.Next()
	})
	r.POST("/p/:id/a/:aid/submit", h.Submit)
	req := httptest.NewRequest("POST", "/p/ps_1/a/app_1/submit", strings.NewReader(body))
	if user != "" {
		req.Header.Set("X-User", user)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestSubmit_NoReqID 缺 req_id → 400(必须关联需求)。
func TestSubmit_NoReqID(t *testing.T) {
	h, _ := setupSubmit(t, `["标准"]`, "alice", true, nil)
	w := doSubmit(h, `{}`, "alice")
	if w.Code != 400 {
		t.Fatalf("无 req_id 应 400,得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestSubmit_NoAcceptanceCriteria 需求无验收标准 → 400(不跳过核对)。
func TestSubmit_NoAcceptanceCriteria(t *testing.T) {
	h, _ := setupSubmit(t, "", "alice", true, nil)
	w := doSubmit(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 400 {
		t.Fatalf("无验收标准应 400,得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestSubmit_NoWorktree 开发者工作分支不存在 → 400(提示先认领/打开工作台)。
func TestSubmit_NoWorktree(t *testing.T) {
	h, _ := setupSubmit(t, `["标准"]`, "alice", false, nil)
	w := doSubmit(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 400 {
		t.Fatalf("无 worktree 应 400,得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestSubmit_AIFail AI 核对失败 → 503(不放行)。
func TestSubmit_AIFail(t *testing.T) {
	fail := func(context.Context, string, string, string, string) (bool, error, string) {
		return false, fmt.Errorf("AI 挂了"), ""
	}
	h, _ := setupSubmit(t, `["标准"]`, "alice", true, fail)
	w := doSubmit(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 503 {
		t.Fatalf("AI 失败应 503(不放行),得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestSubmit_CheckFailed 核对未通过(有❌) → 409(拦截)。
func TestSubmit_CheckFailed(t *testing.T) {
	fail := func(context.Context, string, string, string, string) (bool, error, string) {
		return false, nil, "❌ 标准1 未实现"
	}
	h, _ := setupSubmit(t, `["标准"]`, "alice", true, fail)
	w := doSubmit(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 409 {
		t.Fatalf("核对未通过应 409,得到 %d: %s", w.Code, w.Body.String())
	}
}

// TestSubmit_PassAndRegister 全✅ → 200 + 自动登记 pending change。
func TestSubmit_PassAndRegister(t *testing.T) {
	pass := func(context.Context, string, string, string, string) (bool, error, string) {
		return true, nil, "✅ 全部实现"
	}
	h, db := setupSubmit(t, `["标准"]`, "alice", true, pass)
	w := doSubmit(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 200 {
		t.Fatalf("核对通过应 200,得到 %d: %s", w.Code, w.Body.String())
	}
	// 闭环收敛（PRD 2026-07-26）：Submit 现把 source_id 写成 requirement_id（发布中心据此回写 delivered），
	// 故按 source_id='req_1' 计数；application_id=app_1（应用闸门走它）。
	var n int
	if err := db.Get(&n, "SELECT COUNT(*) FROM change_request WHERE source_id='req_1' AND status='pending'"); err != nil {
		t.Fatalf("查 change: %v", err)
	}
	if n != 1 {
		t.Fatalf("应自动登记 1 条 pending change,得到 %d", n)
	}
	var appID string
	if err := db.Get(&appID, "SELECT COALESCE(application_id,'') FROM change_request WHERE source_id='req_1' AND status='pending' LIMIT 1"); err != nil {
		t.Fatalf("查 change application_id: %v", err)
	}
	if appID != "app_1" {
		t.Fatalf("change.application_id 应为 app_1，得到 %q", appID)
	}
	if !strings.Contains(w.Body.String(), "change_id") {
		t.Fatalf("响应应含 change_id: %s", w.Body.String())
	}
}

// TestSubmit_ReadsWorktreeCode 全✅时 readRepoCode 读的是 worktree 代码(间接验证:worktree 存在才放行)。
func TestSubmit_ReadsWorktreeCode(t *testing.T) {
	var gotCode string
	pass := func(_ context.Context, _ string, code, _, _ string) (bool, error, string) {
		gotCode = code
		return true, nil, "ok"
	}
	h, _ := setupSubmit(t, `["标准"]`, "alice", true, pass)
	w := doSubmit(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 200 {
		t.Fatalf("应 200,得到 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(gotCode, "package main") {
		t.Fatalf("应读到 worktree 里的 main.go 内容,得到 code=%q", gotCode)
	}
}
