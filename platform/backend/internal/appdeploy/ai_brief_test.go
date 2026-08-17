package appdeploy

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuildDeployBrief_ContainsHardRulesAndNoSecretValues(t *testing.T) {
	in := BriefInput{
		AppName: "客服机器人", Slug: dockerSlug("客服机器人"), Env: EnvTest,
		RepoDir: "/data/repos/app_1", BuildDir: "/data/repos/app_1", Version: "11",
		Port: 9101, EnvKeys: []string{"ZHIPUAI_API_KEY", "DB_PASSWORD"},
		Needs:  &NeedsSpec{Ports: []int{8080}, Command: "python -m http.server 8080"},
		Actual: &ActualSpec{ImageDigest: "appdeploy/x-test:v10", HostPort: 9101},
	}
	b := BuildDeployBrief(in)
	for _, want := range []string{
		"appdeploy-" + in.Slug + "-test-v11", // 容器名硬规则
		"9101",                               // 预分配端口
		"ZHIPUAI_API_KEY", "DB_PASSWORD",     // key 名（供 AI 知道有哪些）
		"appdeploy-" + in.Slug + "-", // 前缀清理规则
		"deploy-result.yaml",         // 回报文件名
	} {
		if !strings.Contains(b, want) {
			t.Errorf("简报应含 %q:\n%s", want, b)
		}
	}
	// 简报绝不出现密钥值（key 名可出现）
	if strings.Contains(b, "sk-") {
		t.Errorf("简报疑似泄漏密钥值:\n%s", b)
	}
}

// TestBuildDeployBrief_HeadlessNoPort headless 分支（I-1）：Port<=0 时端口行写 headless
// 提示而非「宿主端口必须用: 0」——AI 看到 0 会误当合法端口加 -p 0。
func TestBuildDeployBrief_HeadlessNoPort(t *testing.T) {
	in := BriefInput{
		AppName: "定时任务机器人", Slug: dockerSlug("定时任务机器人"), Env: EnvTest,
		RepoDir: "/data/repos/app_2", BuildDir: "/data/repos/app_2", Version: "3",
		Port: 0,
	}
	b := BuildDeployBrief(in)
	if !strings.Contains(b, "headless 应用：无端口发布") {
		t.Errorf("简报应含 headless 提示行:\n%s", b)
	}
	if strings.Contains(b, "端口必须用: 0") || strings.Contains(b, "端口 0") {
		t.Errorf("简报不应出现「端口 0」（AI 会误当合法端口）:\n%s", b)
	}
	// 硬性规则的其余部分照常输出（容器名/镜像 tag 不受 headless 影响）
	for _, want := range []string{
		"appdeploy-" + in.Slug + "-test-v3",
		"appdeploy/" + in.Slug + "-test:v3",
	} {
		if !strings.Contains(b, want) {
			t.Errorf("简报应含 %q:\n%s", want, b)
		}
	}
}

func TestLoadDeployResult_RoundTripAndMissing(t *testing.T) {
	dir := t.TempDir()
	if r, err := LoadDeployResult(dir); r != nil || err != nil {
		t.Fatalf("不存在时应 (nil,nil)，得到 %v %v", r, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".anp"), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "container: appdeploy-x-test-v11\nimage: appdeploy/x-test:v11\nlisten_port: 8080\nnotes: ok\n"
	if err := os.WriteFile(filepath.Join(dir, deployResultRelPath), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := LoadDeployResult(dir)
	if err != nil || r == nil {
		t.Fatalf("读取失败: %v %v", r, err)
	}
	if r.Container != "appdeploy-x-test-v11" || r.ListenPort != 8080 {
		t.Fatalf("解析不符: %+v", r)
	}
}

func TestValidateResult(t *testing.T) {
	good := &DeployResult{Container: "appdeploy-x-test-v11", Image: "appdeploy/x-test:v11"}
	// 容器名合规
	if err := ValidateResult(good, "x", "test", "11", 9101); err != nil {
		t.Fatalf("合法结果不应报错: %v", err)
	}
	// 容器名违规（别人的前缀）
	bad := &DeployResult{Container: "appdeploy-other-test-v11", Image: "appdeploy/x-test:v11"}
	if err := ValidateResult(bad, "x", "test", "11", 9101); err == nil {
		t.Fatal("容器名前缀违规应报错")
	}
	// 端口不符（result 没有宿主端口字段；listen_port 与 needs 一致性由验证器查容器实态，这里只验结构必要字段）
	empty := &DeployResult{}
	if err := ValidateResult(empty, "x", "test", "11", 9101); err == nil {
		t.Fatal("空 container/image 应报错")
	}
}
