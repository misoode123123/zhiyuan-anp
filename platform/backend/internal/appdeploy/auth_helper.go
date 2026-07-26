package appdeploy

import "github.com/gin-gonic/gin"

// rolesFromCtx 从 gin context 取 AutoRequire 注入的角色列表。
// 供 env 敏感的部署操作（Deploy/Stop/Start 等）按 env 自行调 auth.Allowed 鉴权。
func rolesFromCtx(c *gin.Context) []string {
	if v, ok := c.Get("roles"); ok {
		if r, ok := v.([]string); ok {
			return r
		}
	}
	return nil
}
