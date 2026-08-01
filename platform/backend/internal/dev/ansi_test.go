package dev

import "testing"

// TestStripANSI 验证从 opencode stdout 剥离 ANSI/VT100 转义序列（SGR/光标/OSC），
// 保留正文。防止 code_task.output / change_request.output 存进乱码转义码。
func TestStripANSI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"纯文本不变", "hello world", "hello world"},
		{"空串不变", "", ""},
		{"SGR颜色+重置", "hello \x1b[31mred\x1b[0m world", "hello red world"},
		{"重置后接内容(opencode实测模式)", "\x1b[0m\n\x1b[0m# \x1b[0mTodos", "\n# Todos"},
		{"隐藏光标+清屏+归位", "\x1b[?25l\x1b[2J\x1b[Hclear", "clear"},
		{"多参数光标定位", "\x1b[1;1Hstart", "start"},
		{"OSC超链接(ST终止)", "see \x1b]8;;https://x\x1b\\link\x1b]8;;\x1b\\ now", "see link now"},
		{"OSC标题(BEL终止)", "\x1b]0;title\x07body", "body"},
		{"混排转义+正文保留", "\x1b[32m✓\x1b[0m \x1b[1mbold\x1b[22m done", "✓ bold done"},
		{"裸ESC结尾兜底移除", "text\x1b", "text"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := stripANSI(tt.in); got != tt.want {
				t.Errorf("stripANSI(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
