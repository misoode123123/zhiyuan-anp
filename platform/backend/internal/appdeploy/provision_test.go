package appdeploy

import (
	"context"
	"strings"
	"testing"
)

func TestProvisioner_LinuxRunsDockerInstall(t *testing.T) {
	var cmds []string
	fake := &recordingExecutor{onRun: func(cmd string) (string, error) { cmds = append(cmds, cmd); return "ok", nil }}
	p := &Provisioner{}
	_, err := p.Provision(context.Background(), &DeployNode{OSType: "linux"}, fake)
	if err != nil {
		t.Fatal(err)
	}
	// 应该跑了装 Docker 的命令（含 docker 关键字或 apt/yum）
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "docker") {
		t.Fatalf("linux provision 应含装 docker 的命令，实际: %s", joined)
	}
}

func TestProvisioner_WindowsOnlyProbes(t *testing.T) {
	var cmds []string
	fake := &recordingExecutor{onRun: func(cmd string) (string, error) { cmds = append(cmds, cmd); return "WINPC", nil }}
	p := &Provisioner{}
	_, err := p.Provision(context.Background(), &DeployNode{OSType: "windows"}, fake)
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cmds, "\n")
	if strings.Contains(joined, "docker") {
		t.Fatalf("windows provision 不应装 docker，实际: %s", joined)
	}
	if !strings.Contains(joined, "COMPUTERNAME") {
		t.Fatalf("windows 应验连通，实际: %s", joined)
	}
}

// recordingExecutor 记录 Run 的命令
type recordingExecutor struct {
	onRun func(cmd string) (string, error)
}

func (r *recordingExecutor) Run(_ context.Context, cmd string) (string, string, int, error) {
	out, err := r.onRun(cmd)
	return out, "", 0, err
}
func (r *recordingExecutor) PutFile(_ context.Context, _, _ string) error { return nil }
func (r *recordingExecutor) TestConnection(_ context.Context) error { return nil }
