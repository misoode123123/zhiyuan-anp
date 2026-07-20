package auth

import (
	"strings"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

const (
	// HeaderUserID 用户标识请求头（M1 模拟登录；后续换 OIDC/SSO token）。
	HeaderUserID = "X-User"
	// CtxUserID context key。
	CtxUserID = "user_id"
)

// publicPaths 无需登录的白名单（登录接口本身；healthz 不在 /api/v1 下,不受 AuthUser 管）。
var publicPaths = map[string]bool{
	"/api/v1/auth/login": true,
}

// AuthUser 强制真实登录:Bearer token 校验通过才放行,否则 401。
// 撤 X-User 模拟回退(2026-07-20 真实鉴权)——任何人不再能靠 X-User 头伪装身份。
// store 为 nil(未启用鉴权)→ 500;登录接口走白名单放行。
func AuthUser(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if publicPaths[c.Request.URL.Path] {
			c.Next()
			return
		}
		if store == nil {
			httpx.Err(c, 500, 50001, "鉴权未配置")
			c.Abort()
			return
		}
		auth := c.GetHeader("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			httpx.Err(c, 401, 40101, "未登录")
			c.Abort()
			return
		}
		name, ok := store.ValidToken(c.Request.Context(), strings.TrimPrefix(auth, "Bearer "))
		if !ok {
			httpx.Err(c, 401, 40101, "登录已过期,请重新登录")
			c.Abort()
			return
		}
		c.Set(CtxUserID, name)
		c.Next()
	}
}

// Require 权限校验中间件：查用户角色（当前项目空间）→ 按矩阵校验操作。
func Require(store *Store, op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := c.GetString(CtxUserID)
		psID := c.GetString("project_space_id")
		roles, _ := store.Roles(c.Request.Context(), user, psID)
		if !Allowed(op, roles) {
			httpx.Err(c, 403, 40301, "无权限执行「"+op+"」（用户 "+user+" 角色: "+strings.Join(roles, ",")+"）")
			c.Abort()
			return
		}
		c.Next()
	}
}
