package logsvc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// dispatchWith 构造挂 DualLoggerMiddleware 的 gin engine，handler 返回指定 status。
func dispatchWith(t *testing.T, dl *DualLogger, status int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(func(c *gin.Context) { c.Set("trace_id", "t_mid"); c.Next() })
	r.Use(DualLoggerMiddleware(dl))
	r.GET("/x", func(c *gin.Context) { c.Status(status) })
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	r.ServeHTTP(httptest.NewRecorder(), req)
}

// TestDualLoggerMiddleware_4xxEntersWarn 4xx 响应应入一条 WARN。
func TestDualLoggerMiddleware_4xxEntersWarn(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "platform_log")
	dl := NewDualLogger(zap.NewNop(), NewStore(db))

	dispatchWith(t, dl, 404)

	var n int
	_ = db.GetContext(context.Background(), &n,
		`SELECT COUNT(*) FROM platform_log WHERE level='WARN' AND trace_id='t_mid'`)
	if n != 1 {
		t.Fatalf("4xx 应入 1 条 WARN，得到 %d", n)
	}
}

// TestDualLoggerMiddleware_2xxNoEntry 2xx 响应不入库。
func TestDualLoggerMiddleware_2xxNoEntry(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "platform_log")
	dl := NewDualLogger(zap.NewNop(), NewStore(db))

	dispatchWith(t, dl, 200)

	var n int
	_ = db.GetContext(context.Background(), &n, `SELECT COUNT(*) FROM platform_log WHERE trace_id='t_mid'`)
	if n != 0 {
		t.Fatalf("2xx 不应入库，得到 %d", n)
	}
}
