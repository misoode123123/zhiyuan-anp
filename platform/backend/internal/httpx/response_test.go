package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newObsContext 建一个带 trace_id/user_id 的 gin test context + 全局 logger observer。
func newObsContext(t *testing.T, method, path string) (*gin.Context, *observer.ObservedLogs) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(method, path, nil)
	c.Set("trace_id", "tr_test_1")
	c.Set("user_id", "u_test")
	core, recorded := observer.New(zapcore.DebugLevel)
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(zap.NewNop()) })
	return c, recorded
}

// TestErr_LogsWarnFor4xx 4xx 应记一条 WARN，带 trace_id/path。
func TestErr_LogsWarnFor4xx(t *testing.T) {
	c, recorded := newObsContext(t, http.MethodGet, "/api/v1/x")

	Err(c, 404, 40420, "应用不存在")

	if recorded.FilterMessage("应用不存在").Len() != 1 {
		t.Fatalf("应记 1 条 WARN，得到 %d", recorded.Len())
	}
	if recorded.FilterLevelExact(zapcore.WarnLevel).Len() != 1 {
		t.Fatalf("4xx 应为 WARN 级")
	}
	ctx := recorded.All()[0].ContextMap()
	if ctx["trace_id"] != "tr_test_1" || ctx["path"] != "/api/v1/x" {
		t.Fatalf("日志字段缺 trace_id/path：%v", ctx)
	}
}

// TestErr_LogsErrorFor5xx 5xx 应记 ERROR。
func TestErr_LogsErrorFor5xx(t *testing.T) {
	c, recorded := newObsContext(t, http.MethodPost, "/api/v1/y")

	Err(c, 500, 50012, "内部错误")

	if recorded.FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
		t.Fatalf("5xx 应为 ERROR 级，得到 %v", recorded.All())
	}
}
