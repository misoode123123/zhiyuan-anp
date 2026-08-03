package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newGuardTestStore 连 anp_test PG + 清 membership 表。
// FK 前置：membership.project_space_id REFERENCES project_space(id)（建 ps_default 供 AddMember）。
// 替代 sqlite :memory:（sqlite FK 默认不强制掩盖真实约束；见 memory sqlite-test-pg-type-trap）。
func newGuardTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	// FK 前置：测试用 AddMember(project_space_id='ps_default')，需 ps_default 存在。
	db.MustExec(`INSERT INTO project_space (id, name, slug, status) VALUES ('ps_default','默认空间','ps_default','active') ON CONFLICT (id) DO NOTHING`)
	testutil.Truncate(t, db, "membership")
	return NewStore(db)
}

func TestRouteOp_KnownAndUnknown(t *testing.T) {
	cases := map[string]string{
		"POST /api/v1/code":                            "code.run",
		"POST /api/v1/project-spaces/:id/requirements": "requirement.create",
		"PUT /api/v1/config/:key":                      "config.manage",
		"GET /api/v1/project-spaces/:id/usage":         "", // 读取类未登记 → 空
		"GET /api/v1/rules":                            "", // 读取类未登记 → 空
		"DELETE /api/v1/standards/:id":                 "rule.manage",
	}
	for k, want := range cases {
		if got := RouteOp(parseMethod(k), parsePath(k)); got != want {
			t.Fatalf("RouteOp(%q)=%q, want %q", k, got, want)
		}
	}
}

func parseMethod(k string) string {
	for i := 0; i < len(k); i++ {
		if k[i] == ' ' {
			return k[:i]
		}
	}
	return ""
}

func parsePath(k string) string {
	for i := 0; i < len(k); i++ {
		if k[i] == ' ' {
			return k[i+1:]
		}
	}
	return ""
}

func TestAllowed_Matrix(t *testing.T) {
	if !Allowed("code.run", []string{RoleDev}) {
		t.Fatal("dev 应可 code.run")
	}
	if !Allowed("code.run", []string{RoleAdmin}) {
		t.Fatal("admin 应可 code.run")
	}
	if Allowed("code.run", []string{RoleBusiness}) {
		t.Fatal("business 不可 code.run")
	}
	if !Allowed("requirement.create", []string{RoleBusiness}) {
		t.Fatal("business 应可 requirement.create")
	}
	// 未登记操作默认允许
	if !Allowed("anything.undefined", []string{RoleBusiness}) {
		t.Fatal("未登记操作应默认允许")
	}
	// 无角色被拒（已登记操作）
	if Allowed("config.manage", nil) {
		t.Fatal("无角色不应通过 config.manage")
	}
}

// TestAutoRequire_AllowsAdminDeniesAnonymous 端到端校验中间件行为。
func TestAutoRequire_AllowsAdminDeniesAnonymous(t *testing.T) {
	store := newGuardTestStore(t)
	// 种子 admin（默认空间）
	if err := store.AddMember(context.Background(), &Member{
		UserID: "admin", ProjectSpaceID: "ps_default", Role: RoleAdmin,
	}); err != nil {
		t.Fatalf("seed admin: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(CtxUserID, "anonymous"); c.Next() }) // 模拟 AuthUser 默认
	v1 := r.Group("/api/v1")
	v1.Use(AutoRequire(store))
	v1.POST("/code", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })

	// anonymous → 403
	req := httptest.NewRequest(http.MethodPost, "/api/v1/code", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("anonymous 应被拒(403)，得到 %d", w.Code)
	}

	// admin → 200
	req2 := httptest.NewRequest(http.MethodPost, "/api/v1/code", nil)
	req2.Header.Set(HeaderUserID, "admin")
	w2 := httptest.NewRecorder()
	// admin 已认证(直接注入 CtxUserID,模拟 AuthUser 通过;撤 X-User 回退后不再用 AuthUser(nil))
	r2 := gin.New()
	r2.Use(func(c *gin.Context) { c.Set(CtxUserID, "admin"); c.Next() })
	v2 := r2.Group("/api/v1")
	v2.Use(AutoRequire(store))
	v2.POST("/code", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r2.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("admin 应通过(200)，得到 %d body=%s", w2.Code, w2.Body.String())
	}

	// 读取类路由（未登记）对已认证但无角色用户放行
	r3 := gin.New()
	r3.Use(func(c *gin.Context) { c.Set(CtxUserID, "anonymous"); c.Next() })
	v3 := r3.Group("/api/v1")
	v3.Use(AutoRequire(store))
	v3.GET("/rules", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	req3 := httptest.NewRequest(http.MethodGet, "/api/v1/rules", nil)
	w3 := httptest.NewRecorder()
	r3.ServeHTTP(w3, req3)
	if w3.Code != 200 {
		t.Fatalf("读取类对 anonymous 应放行(200)，得到 %d", w3.Code)
	}
}

// TestRouteOp_NetworkModeRegistered 防回归：PUT /network-mode 必须登记在 routeOps，
// 否则 AutoRequire 不注入 roles → PutNetworkMode 的 rolesFromCtx 返 nil → 所有人 403（含 admin）。
func TestRouteOp_NetworkModeRegistered(t *testing.T) {
	got := RouteOp("PUT", "/api/v1/project-spaces/:id/apps/:aid/network-mode")
	if got != "app.net.host" {
		t.Fatalf("PUT /network-mode 应登记 op app.net.host（否则 AutoRequire 不注入 roles，生产全员 403），得 %q", got)
	}
}
