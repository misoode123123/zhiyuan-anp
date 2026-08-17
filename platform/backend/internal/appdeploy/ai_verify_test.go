package appdeploy

import (
	"strings"
	"testing"
)

func vInput(res *DeployResult, running bool, hostPort int, img string) verifyInput {
	return verifyInput{Result: res, Slug: "x", Env: "test", Version: "11", Port: 9101,
		InspectRunning: running, InspectHostPort: hostPort, InspectImage: img}
}

func goodResult() *DeployResult {
	return &DeployResult{Container: "appdeploy-x-test-v11", Image: "appdeploy/x-test:v11", ListenPort: 8080}
}

func TestVerifyAIResult_AllPass(t *testing.T) {
	if fails := verifyAIResult(vInput(goodResult(), true, 9101, "appdeploy/x-test:v11")); len(fails) != 0 {
		t.Fatalf("全过场景应无失败项: %v", fails)
	}
}

func TestVerifyAIResult_EachFailure(t *testing.T) {
	cases := []struct {
		name string
		in   verifyInput
		want string // 期望失败信息包含的关键词
	}{
		{"result缺失", vInput(nil, true, 9101, "appdeploy/x-test:v11"), "deploy-result"},
		{"result不合规", vInput(&DeployResult{Container: "evil", Image: "x"}, true, 9101, "appdeploy/x-test:v11"), "不合规"},
		{"容器没跑", vInput(goodResult(), false, 9101, "appdeploy/x-test:v11"), "Running"},
		{"端口错", vInput(goodResult(), true, 9200, "appdeploy/x-test:v11"), "端口"},
		{"镜像不符", vInput(goodResult(), true, 9101, "appdeploy/x-test:v10"), "镜像"},
	}
	for _, c := range cases {
		fails := verifyAIResult(c.in)
		if len(fails) == 0 {
			t.Errorf("%s: 应有失败项", c.name)
			continue
		}
		if !strings.Contains(strings.Join(fails, "; "), c.want) {
			t.Errorf("%s: 失败项 %v 应含 %q", c.name, fails, c.want)
		}
	}
}

func TestHostPortOf(t *testing.T) {
	// C-1：docker inspect 模板 {{...HostPort}}{{end}} 输出裸端口号（无冒号），
	// 原实现要求含 ":" 才解析 → 恒 0 → 验证3 恒失败。两种形状都必须兼容。
	cases := []struct {
		in   string
		want int
		desc string
	}{
		{"9101", 9101, "模板直出裸端口号（无冒号，C-1 主形状）"},
		{"0.0.0.0:9101", 9101, "带绑定地址"},
		{"[::]:9101", 9101, "IPv6 绑定地址"},
		{" 9101\n", 9101, "首尾空白容忍"},
		{"", 0, "空输出"},
		{"91019102", 0, "多端口拼接 >65535 判 0（fail-closed）"},
		{"abc", 0, "非数字"},
		{"0.0.0.0:abc", 0, "冒号后非数字"},
	}
	for _, c := range cases {
		if p := parseHostPortInspect(c.in); p != c.want {
			t.Errorf("parseHostPortInspect(%q)=%d, 期望 %d（%s）", c.in, p, c.want, c.desc)
		}
	}
}

func TestVersionOfImageTag(t *testing.T) {
	// R-3：tag 反解纯函数钉契约——LastIndex(":v") 取尾段 Atoi，0=不可回滚（fail-closed）。
	cases := []struct {
		in   string
		want int
		desc string
	}{
		{"appdeploy/x-test:v3", 3, "本平台标准命名"},
		{"reg:5000/a:v2", 2, "带仓库前缀（LastIndex 取仓库地址后的 :v）"},
		{":v0", 0, "v0 非法（n<1）"},
		{"a:b:vabc", 0, "尾段非数字"},
		{"no-colon-tag", 0, "无 :v 前缀"},
		{"", 0, "空串"},
	}
	for _, c := range cases {
		if n := versionOfImageTag(c.in); n != c.want {
			t.Errorf("versionOfImageTag(%q)=%d, 期望 %d（%s）", c.in, n, c.want, c.desc)
		}
	}
}
