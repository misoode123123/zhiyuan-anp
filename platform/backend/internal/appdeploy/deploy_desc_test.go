package appdeploy

import (
	"strings"
	"testing"
)

func TestParseDeployDesc(t *testing.T) {
	yml := `
target: {os: linux, dir: /opt/myapp}
steps:
  - run: {cmd: ./start.sh, cwd: /opt/myapp}
  - healthcheck: {cmd: "curl -sf http://localhost:8080/health", timeout: 30s}
`
	desc, err := ParseDeployDesc([]byte(yml))
	if err != nil {
		t.Fatal(err)
	}
	if desc.Target.OS != "linux" || desc.Target.Dir != "/opt/myapp" {
		t.Fatalf("target: %+v", desc.Target)
	}
	if len(desc.Steps) != 2 || desc.Steps[0].Run.Cmd != "./start.sh" {
		t.Fatalf("steps: %+v", desc.Steps)
	}
}

func TestRenderScript_LinuxBash(t *testing.T) {
	desc := &DeployDesc{
		Target: TargetDesc{OS: "linux", Dir: "/opt/myapp"},
		Steps: []StepDesc{
			{Run: &RunStep{Cmd: "./myapp --daemon", Cwd: "/opt/myapp"}},
			{Healthcheck: &HealthcheckStep{Cmd: "curl -sf http://localhost:8080/health || exit 1", Timeout: "30s"}},
		},
	}
	script, err := RenderScript(&DeployNode{OSType: "linux"}, desc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "./myapp --daemon") || !strings.Contains(script, "curl") {
		t.Fatalf("script 缺命令: %s", script)
	}
}

func TestRenderScript_WindowsPowerShell(t *testing.T) {
	desc := &DeployDesc{
		Target: TargetDesc{OS: "windows", Dir: "C:\\app"},
		Steps: []StepDesc{
			{Run: &RunStep{Cmd: "Start-Process myapp.exe", Cwd: "C:\\app"}},
		},
	}
	script, err := RenderScript(&DeployNode{OSType: "windows"}, desc)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(script, "Start-Process") {
		t.Fatalf("powerShell 脚本缺命令: %s", script)
	}
}

func TestRenderScript_RejectsTraversal(t *testing.T) {
	desc := &DeployDesc{
		Target: TargetDesc{OS: "linux", Dir: "../escape"},
		Steps:  []StepDesc{},
	}
	if _, err := RenderScript(&DeployNode{OSType: "linux"}, desc); err == nil {
		t.Fatal("应拒绝 ../ 越界 dir")
	}
}
