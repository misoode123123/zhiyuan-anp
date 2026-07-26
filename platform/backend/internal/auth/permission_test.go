package auth

import "testing"

// TestDeployRBAC 部署权限分离矩阵：dev 可 test，prod 仅 gatekeeper/admin（spec 2026-07-26-部署权限分离）。
func TestDeployRBAC(t *testing.T) {
	cases := []struct {
		op    string
		roles []string
		want  bool
	}{
		{"app.deploy.test", []string{RoleDev}, true},
		{"app.deploy.prod", []string{RoleDev}, false},
		{"app.deploy.prod", []string{RoleGatekeeper}, true},
		{"app.deploy.prod", []string{RoleAdmin}, true},
		{"app.deploy-commit.test", []string{RoleDev}, true},
		{"app.deploy-commit.prod", []string{RoleDev}, false},
		{"app.stop.test", []string{RoleDev}, true},
		{"app.stop.prod", []string{RoleDev}, false},
		{"app.start.prod", []string{RoleGatekeeper}, true},
		{"app.delete", []string{RoleDev}, false},
		{"app.delete", []string{RoleAdmin}, true},
		{"app.deploy.test", []string{RoleBusiness}, false}, // 业务方不可部署
		{"app.delete", []string{RoleBusiness}, false},
	}
	for _, c := range cases {
		if got := Allowed(c.op, c.roles); got != c.want {
			t.Errorf("Allowed(%q,%v)=%v want %v", c.op, c.roles, got, c.want)
		}
	}
}
