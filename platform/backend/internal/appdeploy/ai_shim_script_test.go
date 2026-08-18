package appdeploy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestShimScript_ExecutesWhitelist 经真实 sh 执行 shimScript 本体（.28 e2e 曾抓到
// 放行分支 shift 后丢子命令、docker ps 降级为裸 docker 的 Critical——Go 镜像
// shimAllow 测不到 sh 层，此测试钉死脚本可执行行为）。
// Windows 无 sh 时跳过（CI/Linux/.28 全覆盖）。
func TestShimScript_ExecutesWhitelist(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("无 sh（裸 Windows），CI/Linux/.28 覆盖")
	}
	dir := t.TempDir()
	// fake docker：记录收到的参数，可探测前缀拒绝路径
	fake := filepath.Join(dir, "fake-docker")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"FAKE:$*\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	sh, err := InstallShim(filepath.Join(dir, "shim"))
	if err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(sh, "docker")
	// 脚本 REAL 指向 /usr/bin/docker；用 sed 换成 fake 再执行（不依赖 .28 上真 docker）
	fixed := filepath.Join(dir, "docker-fixed")
	b, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixed, []byte(strings.Replace(string(b), "/usr/bin/docker", fake, 1)), 0o755); err != nil {
		t.Fatal(err)
	}

	env := []string{"ANP_CONTAINER_PREFIX=appdeploy-x-test-", "PATH=/usr/bin:/bin"}

	run := func(args ...string) (string, error) {
		cmd := exec.Command("sh", append([]string{fixed}, args...)...)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		return string(out), err
	}

	// 放行命令：子命令必须透传给真 docker（Windows 经 sh 调用时 $* 首参是脚本名，
	// 断言用后缀匹配兼容两种调用形态）
	if out, err := run("ps", "-a"); err != nil || !strings.Contains(out, "ps -a") {
		t.Fatalf("docker ps -a 应透传子命令: out=%q err=%v", out, err)
	}
	if out, err := run("build", "-t", "img", "."); err != nil || !strings.Contains(out, "build -t img .") {
		t.Fatalf("docker build 应透传: out=%q err=%v", out, err)
	}
	// stop 合规前缀：透传（含 flags）
	if out, err := run("stop", "-t", "5", "appdeploy-x-test-v1"); err != nil || !strings.Contains(out, "stop -t 5 appdeploy-x-test-v1") {
		t.Fatalf("stop 前缀容器应透传: out=%q err=%v", out, err)
	}
	// stop 越权：拒绝 exit 127
	if out, err := run("stop", "deploy_backend_1"); err == nil || !strings.Contains(out, "拒绝") {
		t.Fatalf("stop 平台容器应拒绝: out=%q err=%v", out, err)
	}
	// 白名单外：拒绝
	if out, err := run("version"); err == nil || !strings.Contains(out, "拒绝") {
		t.Fatalf("version 应拒绝: out=%q err=%v", out, err)
	}
	// 无前缀 env：stop 拒绝
	cmd := exec.Command("sh", fixed, "stop", "appdeploy-x-test-v1")
	cmd.Env = []string{"PATH=/usr/bin:/bin"}
	out, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(out), "ANP_CONTAINER_PREFIX") {
		t.Fatalf("无前缀 stop 应拒绝: out=%q err=%v", out, err)
	}
}
