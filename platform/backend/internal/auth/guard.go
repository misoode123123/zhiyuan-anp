package auth

import (
	"strings"

	"github.com/gin-gonic/gin"

	"zhiyuan-anp/platform/backend/internal/httpx"
)

// routeOps 把「HTTP方法 + 路由模板（c.FullPath()）」映射到权限操作。
// 仅登记需强制 RBAC 的写/危险操作；未登记者（读取类及其余）默认放行。
// 集中维护，避免逐 handler 注入中间件。
var routeOps = map[string]string{
	// 需求工作台
	"POST /api/v1/project-spaces/:id/requirements":                    "requirement.create",
	"POST /api/v1/project-spaces/:id/requirements/:rid/dispatch-code": "requirement.dispatch",
	// 对话式需求梳理（需求采集路径，复用 requirement.create —— business 角色）
	"POST /api/v1/project-spaces/:id/conversations": "requirement.create",
	"POST /api/v1/conversations/:cid/messages":      "requirement.create",
	"POST /api/v1/conversations/:cid/generate-spec": "requirement.create",
	"POST /api/v1/conversations/:cid/commit":        "requirement.create",
	// 研发工作台
	"POST /api/v1/code": "code.run",
	// 变更闸门（批准/拒绝同属闸门决策）
	"POST /api/v1/changes/:id/approve": "change.approve",
	"POST /api/v1/changes/:id/reject":  "change.approve",
	// 发布中心
	"POST /api/v1/project-spaces/:id/releases": "release.create",
	// 规则治理中心（规则 + 编码规范同属 rule_architect 治理）
	"POST /api/v1/rules":                    "rule.manage",
	"PUT /api/v1/rules/:id":                 "rule.manage",
	"PATCH /api/v1/rules/:id/enabled":       "rule.manage",
	"DELETE /api/v1/rules/:id":              "rule.manage",
	"POST /api/v1/rules/check":              "rule.manage",
	"POST /api/v1/standards":                "rule.manage",
	"PUT /api/v1/standards/:id":             "rule.manage",
	"PATCH /api/v1/standards/:id/enabled":   "rule.manage",
	"DELETE /api/v1/standards/:id":          "rule.manage",
	"POST /api/v1/project-spaces/:id/standards": "rule.manage",
	// 系统配置 + 成员管理（admin）
	"PUT /api/v1/config/:key":               "config.manage",
	"POST /api/v1/project-spaces/:id/members": "config.manage",
}

// RouteOp 返回某「方法+路由模板」对应的操作；未登记返回空串（不强制）。
func RouteOp(method, fullpath string) string {
	return routeOps[method+" "+fullpath]
}

// RegisteredOps 返回所有已登记操作（测试/诊断用）。
func RegisteredOps() map[string]string {
	out := make(map[string]string, len(routeOps))
	for k, v := range routeOps {
		out[k] = v
	}
	return out
}

// AutoRequire 集中式 RBAC 中间件：按 c.FullPath()+方法 查操作 → 校验当前用户角色。
// 挂到 /api/v1 组一次即可覆盖全部登记路由，无需改动各 handler 的 Register。
//
// 项目空间维度（ABAC）：优先用请求头注入的 project_space_id 过滤角色；
// 缺失时取用户在全部空间的并集角色（M1 简化，跨空间收紧留待后续）。
func AutoRequire(store *Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		op := RouteOp(c.Request.Method, c.FullPath())
		if op == "" {
			c.Next()
			return
		}
		user := c.GetString(CtxUserID)
		psID := c.GetString("project_space_id")
		roles, err := store.Roles(c.Request.Context(), user, psID)
		if err != nil {
			httpx.Err(c, 500, 50012, "查询用户角色失败: "+err.Error())
			c.Abort()
			return
		}
		if !Allowed(op, roles) {
			httpx.Err(c, 403, 40301, "无权限执行「"+op+"」（用户 "+user+"，角色: "+strings.Join(roles, ",")+"）")
			c.Abort()
			return
		}
		c.Next()
	}
}
