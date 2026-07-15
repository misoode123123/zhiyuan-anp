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

// AuthUser 解析当前用户注入 user_id。
// 优先 Authorization: Bearer <token>（真实登录）；无 token/无效则回退 X-User 头（兼容调试/旧前端）。
// store 为 nil 时纯走 X-User（测试用）。
func AuthUser(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store != nil {
			if auth := c.GetHeader("Authorization"); strings.HasPrefix(auth, "Bearer ") {
				if name, ok := store.ValidToken(c.Request.Context(), strings.TrimPrefix(auth, "Bearer ")); ok {
					c.Set(CtxUserID, name)
					c.Next()
					return
				}
			}
		}
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
