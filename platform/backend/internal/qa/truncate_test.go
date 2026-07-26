package qa

import (
	"testing"
	"unicode/utf8"
)

// TestTruncate_RuneBoundary 截断不得从多字节字符中间切断（同 appdeploy.truncateStr 的 bug）。
func TestTruncate_RuneBoundary(t *testing.T) {
	s := "中文测试字符串" // 7 汉字 = 21 字节
	for _, n := range []int{1, 4, 7, 10} {
		got := truncate(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("n=%d 截断结果须是合法 UTF-8，得到 %q", n, got)
		}
	}
	if got := truncate("短", 100); got != "短" {
		t.Fatalf("短串应原样返回，得到 %q", got)
	}
}
