package standard

import (
	"strings"
	"testing"
)

func TestBuildPromptSection_Empty(t *testing.T) {
	if got := BuildPromptSection(nil); got != "" {
		t.Fatalf("空列表应返回空串，得到 %q", got)
	}
}

func TestBuildPromptSection_Mix(t *testing.T) {
	ps := "ps_1"
	list := []Standard{
		{ProjectSpaceID: nil, Category: "general", Content: "五约束"},
		{ProjectSpaceID: &ps, Category: "language", Content: "用 FastAPI"},
	}
	got := BuildPromptSection(list)
	want := "\n\n【编码规范·必须遵循】\n[全局][general] 五约束\n[项目][language] 用 FastAPI"
	if got != want {
		t.Fatalf("\n got: %q\nwant: %q", got, want)
	}
}

// TestBuildAgentsMarkdown_DeploySpecSection 部署适配规范固定段须含 .anp/deploy.yaml
// 回写指引（needs/actual 两段 + config.yaml 挂载声明要求），opencode 据此维护部署清单。
func TestBuildAgentsMarkdown_DeploySpecSection(t *testing.T) {
	got := BuildAgentsMarkdown(nil, "")
	for _, want := range []string{".anp/deploy.yaml", "needs", "actual", "mounts", "config.yaml"} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildAgentsMarkdown 固定段应含 %q\n输出:\n%s", want, got)
		}
	}
}
