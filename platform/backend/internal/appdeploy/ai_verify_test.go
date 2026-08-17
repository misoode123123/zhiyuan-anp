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
	if p := parseHostPortInspect("0.0.0.0:9101"); p != 9101 {
		t.Errorf("parseHostPortInspect 应得 9101, 得 %d", p)
	}
	if p := parseHostPortInspect(""); p != 0 {
		t.Errorf("空输出应 0, 得 %d", p)
	}
}
