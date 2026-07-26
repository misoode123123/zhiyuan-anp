package appdeploy

import (
	"testing"
	"unicode/utf8"
)

// TestTruncateStr_RuneBoundary 截断不得从多字节字符中间切断，否则产生无效 UTF-8，
// 写入 PG（UTF8 列）会被拒收（SQLSTATE 22021）—— register-change 在含中文 git 内容的应用上必 500。
// 修法：按字节预算但退到完整 rune 边界。
func TestTruncateStr_RuneBoundary(t *testing.T) {
	s := "中文测试字符串123" // 前 7 个汉字 = 21 字节，n 落在字符中间会切坏
	for _, n := range []int{1, 4, 7, 10, 20} {
		got := truncateStr(s, n)
		if !utf8.ValidString(got) {
			t.Fatalf("n=%d 截断结果须是合法 UTF-8，得到无效字节序列 %q", n, got)
		}
	}
	// 短串原样返回（不附加截断标记）
	if got := truncateStr("短", 100); got != "短" {
		t.Fatalf("短串应原样返回，得到 %q", got)
	}
	// 长串截断后仍是合法 UTF-8 且带截断标记
	got := truncateStr(s, 5)
	if !utf8.ValidString(got) {
		t.Fatalf("截断后须合法 UTF-8，得到 %q", got)
	}
}
