package audit

import (
	"strings"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/auth"
)

// whitelist 白名单路由（method + FullPath 模板）→ 审计 action。
// 只记关键操作（部署/删除/审批/发布/配额/配置/登录），避免普通 CRUD 噪音。
var whitelist = map[string]string{
	"POST /api/v1/project-spaces/:id/apps/:aid/deploy":                "app.deploy",
	"POST /api/v1/project-spaces/:id/apps/:aid/promote":               "app.promote",
	"POST /api/v1/project-spaces/:id/apps/:aid/deploy-commit":         "app.deploy-commit",
	"DELETE /api/v1/project-spaces/:id/apps/:aid":                     "app.delete",
	"POST /api/v1/changes/:id/approve":                                "change.approve",
	"POST /api/v1/changes/:id/reject":                                 "change.reject",
	"POST /api/v1/project-spaces/:id/releases":                        "release.create",
	"PUT /api/v1/project-spaces/:id/quota":                            "quota.update",
	"PUT /api/v1/config/:key":                                         "config.set",
	"POST /api/v1/auth/login":                                         "auth.login",
	"POST /api/v1/project-spaces/:id/test-cases/:tcid/manual-verdict": "qa.manual-verdict",
}

// Middleware 审计中间件：白名单路由在 c.Next() 后记一条 operation_log。
// actor 取 user_id（或 actor_type=agent）；trace_id/project_space_id 从中间件链注入。
// 挂在 v1 组（覆盖所有业务路由），内部按 method+FullPath 判断是否白名单。
func Middleware(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		key := c.Request.Method + " " + c.FullPath()
		action, ok := whitelist[key]
		if !ok {
			return
		}
		resourceType, resourceID := deriveResource(action, c)
		status := "success"
		if c.Writer.Status() >= 400 {
			status = "failed"
		}
		actorType := c.GetString("actor_type")
		if actorType == "" {
			actorType = "user"
		}
		// actor 优先取 user id（usr_xxx，AuthUser 注入的 CtxUserDBID）；缺省回退 username（旧键 user_id）。
		actor := c.GetString(auth.CtxUserDBID)
		if actor == "" {
			actor = c.GetString("user_id")
		}
		_ = store.CreateDetail(c.Request.Context(), actorType, actor, action,
			resourceType, resourceID, c.GetString("project_space_id"), c.GetString("trace_id"),
			status, "",
			map[string]interface{}{
				"method": c.Request.Method,
				"path":   c.Request.URL.Path,
				"http":   c.Writer.Status(),
			})
	}
}

// deriveResource 按 action 前缀从 path param 派生 resource_type/resource_id。
func deriveResource(action string, c *gin.Context) (rtype, rid string) {
	switch {
	case strings.HasPrefix(action, "app."):
		return "app", c.Param("aid")
	case strings.HasPrefix(action, "change."):
		return "change", c.Param("id")
	case strings.HasPrefix(action, "release."):
		return "release", ""
	case strings.HasPrefix(action, "quota."):
		return "project_space", c.Param("id")
	case strings.HasPrefix(action, "config."):
		return "config", c.Param("key")
	case strings.HasPrefix(action, "auth."):
		return "user", c.GetString("user_id")
	case strings.HasPrefix(action, "qa."):
		return "test_case", c.Param("tcid")
	}
	return "", ""
}
