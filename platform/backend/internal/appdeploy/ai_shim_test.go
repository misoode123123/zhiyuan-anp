package appdeploy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestShimAllow(t *testing.T) {
	// 放行组
	for _, args := range [][]string{
		{"docker", "build", "-t", "x", "."},
		{"docker", "run", "-d", "--name", "appdeploy-x-test-v11", "img"},
		{"docker", "inspect", "appdeploy-x-test-v11"},
		{"docker", "logs", "--tail", "50", "appdeploy-x-test-v11"},
		{"docker", "ps"},
		{"docker", "stop", "appdeploy-x-test-v11"},
		{"docker", "rm", "-f", "appdeploy-x-test-v10"},
	} {
		if err := shimAllow(args, "x"); err != nil {
			t.Errorf("应放行 %v: %v", args, err)
		}
	}
	// 拒绝组
	for _, args := range [][]string{
		{"docker", "stop", "deploy_backend"},               // 他人容器
		{"docker", "rm", "-f", "deploy_postgres_1"},        // 平台容器
		{"docker", "rm"},                                   // 无目标（rm 无名 → 拒绝）
		{"docker", "system", "prune", "-a"},                // 危险子命令
		{"docker", "volume", "rm", "x"},                    // volume 域
		{"docker", "network", "disconnect", "a", "b"},      // network 域
		{"docker", "exec", "appdeploy-x-test-v11", "sh"},   // exec 逃逸
		{"docker", "stop", "stop", "appdeploy-x-test-v11"}, // "stop" 作为首个非 flag 参数（钉死 Go 语义，sh 侧靠 shift 对齐）
	} {
		if err := shimAllow(args, "x"); err == nil {
			t.Errorf("应拒绝 %v", args)
		}
	}
}

func TestInstallShim(t *testing.T) {
	dir := t.TempDir()
	got, err := InstallShim(dir)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(got, "docker"))
	if err != nil {
		t.Fatalf("shim/docker 应存在: %v", err)
	}
	if !strings.Contains(string(b), "ANP_CONTAINER_PREFIX") {
		t.Error("shim 脚本应读 ANP_CONTAINER_PREFIX")
	}
	// Unix exec 位在 Windows 上不可表示（os.Stat 恒报 0666），仅 Linux 断言。
	if runtime.GOOS != "windows" {
		fi, _ := os.Stat(filepath.Join(got, "docker"))
		if fi.Mode().Perm()&0o111 == 0 {
			t.Error("shim 应可执行")
		}
	}
}

func TestRestrictedEnv(t *testing.T) {
	base := []string{"PATH=/usr/bin", "HOME=/root", "ZHIPUAI_API_KEY=sk-x"}
	env := restrictedEnv(base, "/usr/local/bin/anp-docker-shim", "x")
	joined := strings.Join(env, "\n")
	if !strings.HasPrefix(firstPath(env), "/usr/local/bin/anp-docker-shim") {
		t.Errorf("PATH 应前置 shim 目录: %v", env)
	}
	if !strings.Contains(joined, "ANP_CONTAINER_PREFIX=appdeploy-x-") {
		t.Errorf("应注入 ANP_CONTAINER_PREFIX: %v", env)
	}
	if !strings.Contains(joined, "ZHIPUAI_API_KEY=sk-x") {
		t.Errorf("应保留原 env: %v", env)
	}

	// base 已含旧 ANP_CONTAINER_PREFIX → 输出只能有一条，且值为注入的 appdeploy-x-
	env2 := restrictedEnv(
		[]string{"PATH=/usr/bin", "HOME=/root", "ANP_CONTAINER_PREFIX=evil-"},
		"/usr/local/bin/anp-docker-shim", "x")
	count2 := 0
	for _, e := range env2 {
		if strings.HasPrefix(e, "ANP_CONTAINER_PREFIX=") {
			count2++
			if e != "ANP_CONTAINER_PREFIX=appdeploy-x-" {
				t.Errorf("ANP_CONTAINER_PREFIX 应为注入值 appdeploy-x-，实际 %q", e)
			}
		}
	}
	if count2 != 1 {
		t.Errorf("ANP_CONTAINER_PREFIX 应只有一条（预置 evil- 须剔除），实际 %d 条: %v", count2, env2)
	}

	// base 无 PATH → 兜底注入 PATH=shimDir（防白名单静默绕过）
	env3 := restrictedEnv(
		[]string{"HOME=/root"},
		"/usr/local/bin/anp-docker-shim", "x")
	p3 := firstPath(env3)
	if !strings.HasPrefix(p3, "/usr/local/bin/anp-docker-shim") {
		t.Errorf("base 无 PATH 时应兜底注入 PATH=shimDir，实际 firstPath=%q: %v", p3, env3)
	}
}

func firstPath(env []string) string {
	for _, e := range env {
		if strings.HasPrefix(e, "PATH=") {
			return strings.TrimPrefix(e, "PATH=")
		}
	}
	return ""
}
