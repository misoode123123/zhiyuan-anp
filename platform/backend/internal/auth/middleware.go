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

// AuthUser 解析 X-User 头注入 user_id（缺失则 anonymous）。
func AuthUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := c.GetHeader(HeaderUserID)
		if u == "" {
			u = "anonymous"
		}
		c.Set(CtxUserID, u)
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
