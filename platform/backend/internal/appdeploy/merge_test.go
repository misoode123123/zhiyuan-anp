package appdeploy

import (
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"

	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/requirement"
)

// gitInit 建 t.TempDir git repo(main 有提交),可选建 dev-alice 分支 + .worktrees/alice。
func gitInit(t *testing.T, makeWorktree bool) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.co")
	run("config", "user.name", "t")
	run("commit", "-q", "--allow-empty", "-m", "init")
	run("branch", "dev-alice") // 开发者分支(merge 目标)
	if makeWorktree {
		run("worktree", "add", ".worktrees/"+sanitizeID("alice"), "dev-alice")
	}
	return dir
}

// setupMerge 建 app + change(可选 approved) + requirement + git repo。
func setupMerge(t *testing.T, approved, makeWorktree bool) (*Handler, *sqlx.DB) {
	t.Helper()
	db := newSubmitDB(t) // 复用三表(appdeploy_application/requirement/change_request)
	db.SetMaxOpenConns(1)
	repoDir := gitInit(t, makeWorktree)
	if _, err := db.Exec(`INSERT INTO appdeploy_application (id, project_space_id, name, repo_dir, status) VALUES ('app_1', 'ps_1', 'demo', ?, 'registered')`, repoDir); err != nil {
		t.Fatalf("insert app: %v", err)
	}
	chgStatus := "pending"
	if approved {
		chgStatus = "approved"
	}
	if _, err := db.Exec(`INSERT INTO change_request (id, project_space_id, kind, source_id, status) VALUES ('chg_1', 'ps_1', 'code', 'app_1', ?)`, chgStatus); err != nil {
		t.Fatalf("insert change: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO requirement (id, project_space_id, application_id, title, description, user_story, acceptance_criteria, status, assignee) VALUES ('req_1', 'ps_1', 'app_1', '登录页', '', '', '["标准"]', 'developing', 'alice')`); err != nil {
		t.Fatalf("insert req: %v", err)
	}
	h := NewHandler(NewStore(db), nil, nil, change.NewStore(db), nil, requirement.NewRepository(db))
	return h, db
}

func doMerge(h *Handler, body, user string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// 模拟 AuthUser:X-User 头 → CtxUserID(撤回退后 handler 读 CtxUserID)
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-User"); u != "" {
			c.Set("user_id", u)
		}
		c.Next()
	})
	r.POST("/p/:id/a/:aid/merge", h.Merge)
	req := httptest.NewRequest("POST", "/p/ps_1/a/app_1/merge", strings.NewReader(body))
	req.Header.Set("X-User", user)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestMerge_NoApproval 无 approved 变更 → 409(G3 前置)。
func TestMerge_NoApproval(t *testing.T) {
	h, _ := setupMerge(t, false, true)
	w := doMerge(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 409 {
		t.Fatalf("无 approved 变更应 409,得到 %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "审批") {
		t.Fatalf("应提示需审批,得到 %s", w.Body.String())
	}
}

// TestMerge_ApprovedConverge approved + worktree → 合并 + 收敛(释放认领/delivered/清worktree)。
func TestMerge_ApprovedConverge(t *testing.T) {
	h, db := setupMerge(t, true, true)
	w := doMerge(h, `{"req_id":"req_1"}`, "alice")
	if w.Code != 200 {
		t.Fatalf("approved 合并应 200,得到 %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "delivered") {
		t.Fatalf("应 delivered: %s", body)
	}
	// 验证需求收敛:assignee 清空 + status=delivered
	var assignee, status string
	if err := db.Get(&assignee, `SELECT COALESCE(assignee,'') FROM requirement WHERE id='req_1'`); err != nil {
		t.Fatal(err)
	}
	if assignee != "" {
		t.Fatalf("合并后 assignee 应清空,得到 %q", assignee)
	}
	if err := db.Get(&status, `SELECT status FROM requirement WHERE id='req_1'`); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" {
		t.Fatalf("合并后需求应 delivered,得到 %s", status)
	}
	// 验证 worktree 清理:查 repo_dir 的 .worktrees/alice 应不存在
	var repoDir2 string
	_ = db.Get(&repoDir2, `SELECT repo_dir FROM appdeploy_application WHERE id='app_1'`)
	if _, err := exec.Command("git", "-C", filepath.Join(repoDir2, ".worktrees", sanitizeID("alice")), "status").CombinedOutput(); err == nil {
		t.Fatal("worktree .worktrees/alice 应已清理")
	}
}
