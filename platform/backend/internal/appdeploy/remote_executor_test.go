package appdeploy

import (
	"testing"
)

func TestNewRemoteExecutor_Dispatch(t *testing.T) {
	cases := []struct{ ct, want string }{
		{"ssh", "*appdeploy.SSHExecutor"},
		{"winrm", "*appdeploy.WinRMExecutor"},
	}
	for _, c := range cases {
		n := &DeployNode{ConnectType: c.ct, Host: "x", SSHUser: "root"}
		e, err := NewRemoteExecutor(n)
		if err != nil {
			t.Fatalf("ct %s: %v", c.ct, err)
		}
		if got := typeName(e); got != c.want {
			t.Fatalf("ct %s: got %s want %s", c.ct, got, c.want)
		}
	}
}

func TestNewRemoteExecutor_DockerTCPNotSupported(t *testing.T) {
	n := &DeployNode{ConnectType: "docker_tcp"}
	if _, err := NewRemoteExecutor(n); err == nil {
		t.Fatal("docker_tcp 不走 RemoteExecutor，应报错")
	}
}

func typeName(e RemoteExecutor) string {
	// 简单类型名（避免 reflect 包路径前缀）
	switch e.(type) {
	case *SSHExecutor:
		return "*appdeploy.SSHExecutor"
	case *WinRMExecutor:
		return "*appdeploy.WinRMExecutor"
	}
	return "unknown"
}
