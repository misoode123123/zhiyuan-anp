package appdeploy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestEngineFor(t *testing.T) {
	h, _ := newHTTPHandler(t)
	// 默认 fixed（cfg nil）
	if e := h.engineFor("", EnvTest); e != "fixed" {
		t.Errorf("无配置默认 fixed，得到 %s", e)
	}
	if e := h.engineFor("fixed", EnvTest); e != "fixed" {
		t.Errorf("显式 fixed 应 fixed，得到 %s", e)
	}
	if e := h.engineFor("ai", EnvProd); e != "fixed" {
		t.Errorf("prod 恒 fixed，得到 %s", e)
	}
}

// fakeOpencode 造一个可注入的 aiOpencodeRun：往 buildDir 写合法 deploy-result。
func fakeOpencode(t *testing.T, valid bool) func(context.Context, string, string, string, []string) (string, error) {
	return func(ctx context.Context, dir, prompt, model string, env []string) (string, error) {
		// 简报应已落盘
		brief, err := os.ReadFile(filepath.Join(dir, ".anp", "deploy-brief.md"))
		if err != nil {
			return "", fmt.Errorf("简报未落盘: %w", err)
		}
		if len(brief) == 0 {
			return "", fmt.Errorf("简报为空")
		}
		if !valid {
			return "AI 拒绝执行", fmt.Errorf("模拟 AI 失败")
		}
		// 从简报提取规定的容器名/镜像 tag（简报里有硬规则行）——这里用固定 slug=ai-demo
		res := "container: appdeploy-ai-demo-test-v1\nimage: appdeploy/ai-demo-test:v1\nlisten_port: 8080\n"
		if err := os.MkdirAll(filepath.Join(dir, ".anp"), 0o755); err != nil {
			return "", err
		}
		return "AI 部署完成", os.WriteFile(filepath.Join(dir, deployResultRelPath), []byte(res), 0o644)
	}
}

// TestAiDeploy_FullChain 集成（PG anp_test）：fake opencode 成功 →
// 状态到 running、build_log 含简报与验证、instance 实态字段被回填。
// 注：docker inspect 三读数在 CI 无容器 → InspectRunning=false → 验证必挂。
// 故本测注入 fake：把 hostPortOf/InspectHealth/InspectImage 三个读数经包级 var 注入。
func TestAiDeploy_FullChain(t *testing.T) {
	// fake 读数注入点（ai_engine.go 须提供这三个包级 var）。
	// 宿主端口须与平台实际预分配一致：无 docker 环境 usedPortsOn 恒空 →
	// AllocFreePort(∅, 9100, 9199) 确定性取 test 段首个 9100。
	aiInspect = func(ctx context.Context, h *Handler, container string) (bool, int, string) {
		return true, 9100, "appdeploy/ai-demo-test:v1"
	}
	defer func() { aiInspect = nil }()
	origRun := aiOpencodeRun
	aiOpencodeRun = fakeOpencode(t, true)
	defer func() { aiOpencodeRun = origRun }()

	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "ai-demo", t.TempDir())
	h.aiDeploy("ps_1", a.ID, EnvTest, "")

	ins, _ := h.store.GetInstance(ctx, a.ID, EnvTest)
	if ins == nil || ins.Status != "running" {
		t.Fatalf("成功链后应 running，得到 %+v", ins)
	}
	if ins.ContainerName != "appdeploy-ai-demo-test-v1" {
		t.Fatalf("容器名应按平台规则，得到 %q", ins.ContainerName)
	}
	if !contains(ins.BuildLog, "五步验证全过") {
		t.Fatalf("build_log 应含验证记录:\n%s", ins.BuildLog)
	}
	// 简报/result 临时文件应被清理
	if _, err := os.Stat(filepath.Join(a.RepoDir, ".anp", "deploy-brief.md")); !os.IsNotExist(err) {
		t.Error("deploy-brief.md 验证后应删除")
	}
	if _, err := os.Stat(filepath.Join(a.RepoDir, deployResultRelPath)); !os.IsNotExist(err) {
		t.Error("deploy-result.yaml 验证后应删除")
	}
}

// TestAiDeploy_FailMarksFailedAndLogsRedacted 集成：AI 失败 → failed + 脱敏日志。
func TestAiDeploy_FailMarksFailedAndLogsRedacted(t *testing.T) {
	origRun := aiOpencodeRun
	aiOpencodeRun = fakeOpencode(t, false)
	defer func() { aiOpencodeRun = origRun }()
	aiInspect = func(ctx context.Context, h *Handler, container string) (bool, int, string) {
		return false, 0, ""
	}
	defer func() { aiInspect = nil }()

	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "ai-demo2", t.TempDir())
	_ = h.store.UpsertEnv(ctx, a.ID, "SECRET_KEY", "sk-secret-value-123", true, "test")
	h.aiDeploy("ps_1", a.ID, EnvTest, "")

	ins, _ := h.store.GetInstance(ctx, a.ID, EnvTest)
	if ins == nil || ins.Status != "failed" {
		t.Fatalf("失败链后应 failed，得到 %+v", ins)
	}
	if !contains(ins.BuildLog, "AI 执行失败") {
		t.Fatalf("build_log 应含失败原因:\n%s", ins.BuildLog)
	}
	if contains(ins.BuildLog, "sk-secret-value-123") {
		t.Fatal("build_log 不得含密钥明文")
	}
	app, _ := h.store.GetByAppID(ctx, a.ID)
	if app.Status != "failed" {
		t.Fatalf("app.status 应 failed（前端红条），得到 %s", app.Status)
	}
}
