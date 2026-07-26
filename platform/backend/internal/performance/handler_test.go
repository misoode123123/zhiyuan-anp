package performance

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestRBAC_MembersAdminOnly admin token → 200；dev token → 403（performance.view.all 仅 admin）。
func TestRBAC_MembersAdminOnly(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "auth_session", "membership", `"user"`)
	authStore := auth.NewStore(db)
	ctx := context.Background()

	// 用户（原生 SQL 插固定 id，因 CreateUser 会覆盖 id）+ 密码 + 成员角色
	// 用不碰撞的用户名（perf_*），避免留库污染 auth guard_test 等依赖 "admin" 的测试。
	db.MustExec(`INSERT INTO "user"(id,name) VALUES('usr_perfadm','perf_admin')`)
	db.MustExec(`INSERT INTO "user"(id,name) VALUES('usr_perfdev','perf_dev')`)
	if err := authStore.SetPasswordByName(ctx, "perf_admin", "pw"); err != nil {
		t.Fatalf("set admin pw: %v", err)
	}
	if err := authStore.SetPasswordByName(ctx, "perf_dev", "pw"); err != nil {
		t.Fatalf("set dev pw: %v", err)
	}
	db.MustExec(`INSERT INTO project_space(id,name,slug) VALUES('ps_perf','p','ps_perf') ON CONFLICT (id) DO NOTHING`)
	db.MustExec(`INSERT INTO membership(id,project_space_id,user_id,role) VALUES('m1','ps_perf','usr_perfadm','admin')`)
	db.MustExec(`INSERT INTO membership(id,project_space_id,user_id,role) VALUES('m2','ps_perf','usr_perfdev','dev')`)

	adminToken, _, err := authStore.Login(ctx, "perf_admin", "pw")
	if err != nil {
		t.Fatalf("admin login: %v", err)
	}
	devToken, _, err := authStore.Login(ctx, "perf_dev", "pw")
	if err != nil {
		t.Fatalf("dev login: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/api/v1")
	v.Use(auth.AuthUser(authStore), auth.AutoRequire(authStore))
	NewHandler(NewStore(db), nil).Register(v)

	hit := func(token string) int {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/project-spaces/ps_perf/performance/members", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if c := hit(adminToken); c != 200 {
		t.Fatalf("admin 应 200, 得 %d", c)
	}
	if c := hit(devToken); c != 403 {
		t.Fatalf("dev 应 403, 得 %d", c)
	}
}
