package appdeploy

import (
	"strings"
	"testing"
)

// TestValidateAppName 应用名必须人工起名:合法名通过,空/过短/ID 前缀/纯数字被拒。
func TestValidateAppName(t *testing.T) {
	legal := []string{"hello-go", "聊天应用", "web_portal2", "中文"}
	for _, n := range legal {
		if msg := validateAppName(n); msg != "" {
			t.Errorf("合法名 %q 应通过,得到 %q", n, msg)
		}
	}
	illegal := []struct{ name, want string }{
		{"", "至少 2 个字符"},
		{"   ", "至少 2 个字符"},
		{"a", "至少 2 个字符"},
		{"app_abc123", "ID 前缀"},
		{"CHG_xyz", "ID 前缀"}, // 大小写不敏感
		{"req_9", "ID 前缀"},
		{"12345", "纯数字"},
		{"007", "纯数字"},
	}
	for _, c := range illegal {
		msg := validateAppName(c.name)
		if msg == "" {
			t.Errorf("非法名 %q 应被拒,却放行", c.name)
			continue
		}
		if !strings.Contains(msg, c.want) {
			t.Errorf("非法名 %q 应提示含 %q,得到 %q", c.name, c.want, msg)
		}
	}
}
