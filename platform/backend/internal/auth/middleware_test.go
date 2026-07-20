package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newAuthTestStore 内存 SQLite + user + auth_session 表(AuthUser 的 ValidToken 查 auth_session)。
func newAuthTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE "user" (id TEXT PRIMARY KEY, name TEXT, email TEXT, status TEXT, password_hash TEXT, created_at DATETIME)`)
	db.MustExec(`CREATE TABLE auth_session (token TEXT PRIMARY KEY, user_id TEXT, user_name TEXT, expires_at DATETIME)`)
	return NewStore(db)
}

func authEngine(store *Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	v := r.Group("/api/v1")
	v.Use(AuthUser(store))
	v.POST("/auth/login", func(c *gin.Context) { c.String(200, "login") })
	v.GET("/secret", func(c *gin.Context) { c.String(200, c.GetString(CtxUserID)) })
	return r
}

// TestAuthUser_NoToken_401 无 token → 401(撤 X-User 回退后的核心安全行为)。
func TestAuthUser_NoToken_401(t *testing.T) {
	r := authEngine(newAuthTestStore(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/secret", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("无 token 应 401,得到 %d", w.Code)
	}
}

// TestAuthUser_XUserIgnored X-User 头不再放行(撤回退;任何人不能靠 X-User 伪装)。
func TestAuthUser_XUserIgnored(t *testing.T) {
	r := authEngine(newAuthTestStore(t))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/secret", nil)
	req.Header.Set("X-User", "admin")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("X-User 头不应放行(撤回退),得到 %d", w.Code)
	}
}

// TestAuthUser_PublicLoginWhitelist /auth/login 无 token 白名单放行(登录接口本身)。
func TestAuthUser_PublicLoginWhitelist(t *testing.T) {
	r := authEngine(newAuthTestStore(t))
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("/auth/login 应白名单放行,得到 %d", w.Code)
	}
}

// TestAuthUser_ValidTokenPass 有效 Bearer token → 放行并注入 user_id。
func TestAuthUser_ValidTokenPass(t *testing.T) {
	store := newAuthTestStore(t)
	store.db.MustExec(`INSERT INTO auth_session (token, user_id, user_name, expires_at) VALUES ('tok_test', 'usr_1', 'alice', datetime('now','+1 day'))`)
	r := authEngine(store)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/secret", nil)
	req.Header.Set("Authorization", "Bearer tok_test")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("有效 token 应放行,得到 %d body=%s", w.Code, w.Body.String())
	}
	if w.Body.String() != "alice" {
		t.Fatalf("应注入 user_id=alice,得到 %s", w.Body.String())
	}
}

// TestAuthUser_ExpiredToken_401 过期 token → 401。
func TestAuthUser_ExpiredToken_401(t *testing.T) {
	store := newAuthTestStore(t)
	store.db.MustExec(`INSERT INTO auth_session (token, user_id, user_name, expires_at) VALUES ('tok_old', 'usr_1', 'alice', datetime('now','-1 day'))`)
	r := authEngine(store)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/secret", nil)
	req.Header.Set("Authorization", "Bearer tok_old")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("过期 token 应 401,得到 %d", w.Code)
	}
}
