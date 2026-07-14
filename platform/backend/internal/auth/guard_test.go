package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newGuardTestStore 建内存 SQLite + 仅 membership 表（自包含）。
func newGuardTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE membership (
		id TEXT PRIMARY KEY,
		project_space_id TEXT NOT NULL,
		user_id TEXT NOT NULL,
		role TEXT NOT NULL,
		UNIQUE (project_space_id, user_id))`)
	return NewStore(db)
}

func TestRouteOp_KnownAndUnknown(t *testing.T) {
	cases := map[string]string{
		"POST /api/v1/code":                 "code.run",
		"POST /api/v1/project-spaces/:id/requirements": "requirement.create",
		"PUT /api/v1/config/:key":           "config.manage",
		"GET /api/v1/project-spaces/:id/usage": "", // 读取类未登记 → 空
		"GET /api/v1/rules":                 "",   // 读取类未登记 → 空
		"DELETE /api/v1/standards/:id":      "rule.manage",
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
	// 覆盖默认 anonymous：用一个能读头的 AuthUser
	r2 := gin.New()
	r2.Use(AuthUser())
	v2 := r2.Group("/api/v1")
	v2.Use(AutoRequire(store))
	v2.POST("/code", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	r2.ServeHTTP(w2, req2)
	if w2.Code != 200 {
		t.Fatalf("admin 应通过(200)，得到 %d body=%s", w2.Code, w2.Body.String())
	}

	// 读取类路由（未登记）对 anonymous 放行
	r3 := gin.New()
	r3.Use(AuthUser())
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
