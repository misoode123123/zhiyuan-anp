package auth

// 角色常量。
const (
	RoleBusiness      = "business"       // 业务部门：提需求/验收
	RoleDev           = "dev"            // 研发：派编码/代码评审
	RoleRuleArchitect = "rule_architect" // 规则架构师：规则管理
	RoleGatekeeper    = "gatekeeper"     // 闸门负责人：审批
	RoleAdmin         = "admin"          // 管理员：全部
)

// OpRoles 操作 → 允许的角色集合（权限矩阵）。
var OpRoles = map[string][]string{
	"requirement.create":   {RoleBusiness, RoleAdmin},
	"requirement.dispatch": {RoleDev, RoleAdmin},
	"code.run":             {RoleDev, RoleAdmin},
	"change.approve":       {RoleDev, RoleGatekeeper, RoleAdmin},
	"release.create":       {RoleGatekeeper, RoleAdmin},
	"rule.manage":          {RoleRuleArchitect, RoleAdmin},
	"config.manage":        {RoleAdmin},
}

// Allowed 判断角色集合是否可执行某操作（未定义操作默认允许）。
func Allowed(op string, roles []string) bool {
	need, ok := OpRoles[op]
	if !ok {
		return true
	}
	for _, r := range roles {
		if r == RoleAdmin {
			return true
		}
		for _, n := range need {
			if r == n {
				return true
			}
		}
	}
	return false
}
