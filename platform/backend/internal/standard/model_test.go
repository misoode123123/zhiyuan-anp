package standard

import (
	"fmt"
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

// TestBuildAgentsMarkdown_DeployFacts 断言「ANP 部署适配规范」固定段含与引擎实现一致的事实：
//   - 自动注入 PORT / CONFIG_PATH=/app/config.yaml
//   - 端口段（从 PortTestMin/Max · PortProdMin/Max 常量渲染，改常量→文本随之变→此处即漂移告警）
//   - needs 消费现状真相（P1-b 起四字段全消费）
//
// 防规则源（AGENTS.md）与引擎实现脱节。
func TestBuildAgentsMarkdown_DeployFacts(t *testing.T) {
	got := BuildAgentsMarkdown(nil, "")
	for _, want := range []string{
		"`PORT`",                         // 自动注入 PORT（带反引号，定位到 bullet 标题）
		"`CONFIG_PATH=/app/config.yaml`", // 自动注入 CONFIG_PATH
		fmt.Sprintf("%d-%d", PortTestMin, PortTestMax), // test 端口段（常量渲染）
		fmt.Sprintf("%d-%d", PortProdMin, PortProdMax), // prod 端口段（常量渲染）
		"消费 `needs` 四字段",                               // needs 消费现状真相（P1-b 全消费）
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("BuildAgentsMarkdown 固定段应含 %q\n输出:\n%s", want, got)
		}
	}
}
