package appgw

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// TokenVerifier 平台 JWT 验签端口（auth.Store 实现此接口）。
// 抽接口而非直接依赖 *auth.Store：appgw 单测可注入 fake，且与 auth 包解耦。
type TokenVerifier interface {
	// ValidToken 校验 token，返回用户名 + 是否有效（与 auth.Store.ValidToken 同签名）。
	ValidToken(ctx context.Context, token string) (string, bool)
}

// RouteStore 路由查询端口（*Store 实现此接口；测试可注入 fake）。
type RouteStore interface {
	GetRoute(ctx context.Context, appCode, env string) (*Route, error)
}

// AccessLogger 调用日志写入端口（*Store 实现此接口）。
// gateway.ReverseProxy 反代时调 LogAccess 记一笔；nil = 不记日志（向后兼容）。
type AccessLogger interface {
	LogAccess(ctx context.Context, al *AccessLog) error
}

// Gateway 应用反向代理网关：把 /apps/<app_code>/ 统一前缀的请求按 appdeploy_route
// 转发到对应应用容器，并注入平台身份头。
type Gateway struct {
	store     RouteStore
	auth      TokenVerifier // nil = 不强制鉴权（仅极少数内部场景；默认应传非 nil）
	logger    *zap.Logger
	accessLog AccessLogger // nil = 不记 access_log（向后兼容；3b 起默认注入 *Store）
}

// NewGateway 构造。auth/accessLog 可为 nil（鉴权关闭或日志未启用，仅内部测试用）。
func NewGateway(store RouteStore, auth TokenVerifier, logger *zap.Logger, accessLog AccessLogger) *Gateway {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Gateway{store: store, auth: auth, logger: logger, accessLog: accessLog}
}

// ReverseProxy gin handler：解析 /apps/<code>~<env>/<rest> → 查路由 → 鉴权 → 注入头 → 反代。
//
// URL 形态：
//
//	/apps/<app_code>/              → env 默认 prod
//	/apps/<app_code>~test/         → test 环境
//	/apps/<app_code>/<rest>        → rest 透传到应用容器 /<rest>
//
// 鉴权：route.auth_required=true 时验 Authorization: Bearer <token>，无效 → 401。
// 身份头：注入 X-User（鉴权通过的用户）/ X-Project-Space-Id（路由所属项目）/ X-Trace-Id（链路）。
func (g *Gateway) ReverseProxy(c *gin.Context) {
	appCode, env, rest, ok := parseAppsPath(c.Request.URL.Path)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"code": 40401, "message": "无效的 /apps/ 路径，需为 /apps/<app_code>/..."})
		return
	}
	if env == "" {
		env = DefaultEnv
	}

	route, err := g.store.GetRoute(c.Request.Context(), appCode, env)
	if err != nil || route == nil {
		c.JSON(http.StatusNotFound, gin.H{"code": 40402, "message": fmt.Sprintf("应用路由不存在: app_code=%s env=%s", appCode, env)})
		return
	}
	if route.Status != StatusActive {
		c.JSON(http.StatusServiceUnavailable, gin.H{"code": 50301, "message": "应用路由未激活: status=" + route.Status})
		return
	}

	// 鉴权：route.auth_required 时验平台 JWT。
	user := ""
	if route.AuthRequired {
		if g.auth == nil {
			c.JSON(http.StatusInternalServerError, gin.H{"code": 50001, "message": "鉴权未配置"})
			return
		}
		u, ok := verifyBearer(c, g.auth)
		if !ok {
			c.JSON(http.StatusUnauthorized, gin.H{"code": 40101, "message": "未登录或登录已过期"})
			return
		}
		user = u
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", route.UpstreamHost, route.UpstreamPort),
	}
	proxy := httputil.NewSingleHostReverseProxy(target)

	// 自定义 Director：去掉 /apps/<code>~<env>/ 前缀 + 注入身份头。
	// 保留原始 NewSingleHostReverseProxy 的行为（Scheme/Host/Path 合并），覆写 Path 与头。
	traceID := c.GetString("trace_id")
	projectSpaceID := route.ProjectSpaceID
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.URL.Path = "/" + rest
		req.URL.RawPath = "" // 已重新规范化，丢弃原始 escaped path
		// 身份头注入（应用侧可选校验；X-User 仅在鉴权通过时填）
		if user != "" {
			req.Header.Set("X-User", user)
		}
		if projectSpaceID != "" {
			req.Header.Set("X-Project-Space-Id", projectSpaceID)
		}
		if traceID != "" {
			req.Header.Set("X-Trace-Id", traceID)
		}
	}

	// 计量采集（阶段3b）：反代前记起始时间，ModifyResponse/ErrorHandler 落 access_log。
	// 记日志失败/慢都不能阻塞请求 —— recordAccess 用独立 ctx + goroutine 异步写。
	start := time.Now()
	caller := resolveCaller(c, user)

	// ModifyResponse 捕获 upstream 正常响应（含 4xx/5xx），记一笔 status + latency。
	proxy.ModifyResponse = func(resp *http.Response) error {
		g.recordAccess(c, route, appCode, env, caller, traceID, resp.StatusCode, start)
		return nil // 不改 response，仅观察
	}
	// 自定义错误响应：upstream 不可达时返回 502 而非默认 500。
	// ErrorHandler 与 ModifyResponse 互斥（反代失败走这，不调 ModifyResponse）→ 此处也记一笔。
	proxy.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		g.logger.Warn("appgw upstream error",
			zap.String("app_code", appCode), zap.String("env", env),
			zap.String("upstream", target.Host), zap.Error(err))
		g.recordAccess(c, route, appCode, env, caller, traceID, http.StatusBadGateway, start)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`{"code":50201,"message":"应用网关转发失败（upstream 不可达）"}`))
	}

	proxy.ServeHTTP(c.Writer, c.Request)
}

// resolveCaller 推断调用方标识（用于 access_log.caller）：
//   - 鉴权通过 → 平台用户名
//   - 否则带 X-Api-Key → "apikey:" + <key 前 8 字符>（避免落全 key）
//   - 否则 → "anonymous"
func resolveCaller(c *gin.Context, user string) string {
	if user != "" {
		return user
	}
	if k := c.GetHeader("X-Api-Key"); k != "" {
		if len(k) > 8 {
			k = k[:8]
		}
		return "apikey:" + k
	}
	return "anonymous"
}

// recordAccess 异步写一笔 access_log。latency_ms 自 start 计算。
// 用独立 context（不复用 request ctx —— 请求结束后 request ctx 会被 cancel，INSERT 会失败）。
// 记失败只 zap.Warn，不影响业务。
func (g *Gateway) recordAccess(c *gin.Context, route *Route, appCode, env, caller, traceID string, status int, start time.Time) {
	if g.accessLog == nil {
		return // 未启用 access_log
	}
	al := &AccessLog{
		ProjectSpaceID: route.ProjectSpaceID,
		AppID:          route.AppID,
		AppCode:        appCode,
		Env:            env,
		Caller:         caller,
		Method:         c.Request.Method,
		Path:           c.Request.URL.Path,
		Status:         status,
		LatencyMs:      int(time.Since(start).Milliseconds()),
		TraceID:        traceID,
	}
	logger := g.accessLog
	go func() {
		// 独立 ctx：请求 ctx 在 ServeHTTP 返回后会被 cancel，异步写需脱离请求生命周期。
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := logger.LogAccess(ctx, al); err != nil {
			// 仅在 access logger 本身有 zap 能力时尝试；否则吞掉（不阻塞业务）。
			g.logger.Warn("appgw access_log 写入失败",
				zap.String("app_code", appCode), zap.Int("status", status), zap.Error(err))
		}
	}()
}

// parseAppsPath 从 /apps/<code>~<env>/<rest> 解析出 code/env/rest。
// 返回 (code, env, rest, ok)；ok=false 表示路径不是 /apps/<非空>/ 形态。
// env 默认 ""（调用方按 DefaultEnv 补默认）；rest 不含前导 "/"（拼接时 "/" + rest）。
//
//	parseAppsPath("/apps/app_x/api/q")    → ("app_x", "", "api/q", true)
//	parseAppsPath("/apps/app_x~test/")    → ("app_x", "test", "", true)
//	parseAppsPath("/apps/app_x")          → ("app_x", "", "", true)
//	parseAppsPath("/apps/")               → ("", "", "", false)
//	parseAppsPath("/api/v1/foo")          → ("", "", "", false)
func parseAppsPath(fullPath string) (code, env, rest string, ok bool) {
	if !strings.HasPrefix(fullPath, "/apps/") {
		return "", "", "", false
	}
	trimmed := strings.TrimPrefix(fullPath, "/apps/")
	if trimmed == "" {
		return "", "", "", false
	}
	// 分离首段（<code> 或 <code>~<env>）与 rest
	first := trimmed
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		first = trimmed[:idx]
		rest = trimmed[idx+1:]
	}
	if first == "" {
		return "", "", "", false
	}
	// 处理 ~test 后缀（仅首段；URL 路径里其他段不解析）
	if t := strings.Index(first, "~"); t >= 0 {
		code = first[:t]
		env = first[t+1:]
	} else {
		code = first
	}
	if code == "" {
		return "", "", "", false
	}
	return code, env, rest, true
}

// verifyBearer 从 Authorization 头取 Bearer token，调 verifier 校验。
func verifyBearer(c *gin.Context, v TokenVerifier) (string, bool) {
	a := c.GetHeader("Authorization")
	if !strings.HasPrefix(a, "Bearer ") {
		return "", false
	}
	return v.ValidToken(c.Request.Context(), strings.TrimPrefix(a, "Bearer "))
}
