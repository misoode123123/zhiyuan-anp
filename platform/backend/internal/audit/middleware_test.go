package audit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestMiddleware_Whitelisted 白名单路由（POST .../deploy）触发 → operation_log 记一条 app.deploy。
func TestMiddleware_Whitelisted(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "operation_log")
	store := NewStore(db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("user_id", "u_test"); c.Set("trace_id", "tr_test"); c.Next() })
	r.Use(Middleware(store))
	r.POST("/api/v1/project-spaces/:id/apps/:aid/deploy", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/project-spaces/ps1/apps/app_x/deploy", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	list, _ := store.Query(context.Background(), "", "u_test", "app.deploy", "", "", 10, 0)
	if len(list) != 1 {
		t.Fatalf("白名单 deploy 应记 1 条，得到 %d", len(list))
	}
	if list[0].ResourceID != "app_x" || list[0].Status != "success" || list[0].TraceID != "tr_test" {
		t.Fatalf("字段不符: %+v", list[0])
	}
}

// TestMiddleware_FailedStatus 4xx/5xx 响应 → status=failed。
func TestMiddleware_FailedStatus(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "operation_log")
	store := NewStore(db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware(store))
	r.DELETE("/api/v1/project-spaces/:id/apps/:aid", func(c *gin.Context) { c.Status(500) })

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/project-spaces/ps1/apps/app_y", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	list, _ := store.Query(context.Background(), "", "", "app.delete", "", "", 10, 0)
	if len(list) != 1 {
		t.Fatalf("app.delete 应记 1 条，得到 %d", len(list))
	}
	if list[0].Status != "failed" {
		t.Fatalf("5xx 应 status=failed，得到 %q", list[0].Status)
	}
}

// TestMiddleware_NonWhitelisted 非白名单路由（GET .../apps）不记审计。
func TestMiddleware_NonWhitelisted(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "operation_log")
	store := NewStore(db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(Middleware(store))
	r.GET("/api/v1/project-spaces/:id/apps", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodGet, "/api/v1/project-spaces/ps1/apps", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	list, _ := store.Query(context.Background(), "", "", "", "", "", 10, 0)
	if len(list) != 0 {
		t.Fatalf("非白名单不应记，得到 %d", len(list))
	}
}

// TestMiddleware_ActorIsUserID 白名单操作落 operation_log 时，actor_id 应为 user id（usr_xxx），不是 username。
// AuthUser 现同时注入 CtxUserID(name) 与 CtxUserDBID(usr_xxx)；审计优先取后者。
func TestMiddleware_ActorIsUserID(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "operation_log")
	store := NewStore(db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set(auth.CtxUserID, "alice")   // name（旧键 user_id，向后兼容）
		c.Set(auth.CtxUserDBID, "usr_1") // db id（新键 user_db_id）
		c.Set("trace_id", "tr_x")
		c.Next()
	})
	r.Use(Middleware(store))
	r.PUT("/api/v1/config/:key", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/foo", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	var actor string
	if err := db.Get(&actor, `SELECT actor_id FROM operation_log ORDER BY id DESC LIMIT 1`); err != nil {
		t.Fatalf("query operation_log: %v", err)
	}
	if actor != "usr_1" {
		t.Fatalf("actor_id 应为 usr_1(非 username alice), 得 %q", actor)
	}
}

// TestMiddleware_ManualVerdict 人工验收路由在白名单 → operation_log 记 qa.manual-verdict，
// resource_type=test_case、resource_id=被验收用例 id（:tcid）。
func TestMiddleware_ManualVerdict(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "operation_log")
	store := NewStore(db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set(auth.CtxUserDBID, "usr_qa"); c.Set("trace_id", "tr_qa"); c.Next() })
	r.Use(Middleware(store))
	r.POST("/api/v1/project-spaces/:id/test-cases/:tcid/manual-verdict", func(c *gin.Context) { c.Status(200) })

	req := httptest.NewRequest(http.MethodPost, "/api/v1/project-spaces/ps1/test-cases/tc_z/manual-verdict", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)

	list, _ := store.Query(context.Background(), "", "usr_qa", "qa.manual-verdict", "", "", 10, 0)
	if len(list) != 1 {
		t.Fatalf("manual-verdict 应记 1 条，得到 %d", len(list))
	}
	if list[0].ResourceType != "test_case" || list[0].ResourceID != "tc_z" {
		t.Fatalf("审计字段不符：type=%s id=%s", list[0].ResourceType, list[0].ResourceID)
	}
}
