package dev

import (
	"regexp"
	"strings"
)

// ansiRe 匹配 ANSI/VT100 转义序列：
//   - OSC：ESC ] ... （以 ST=ESC \ 或 BEL=\x07 终止，如终端标题/超链接）
//   - CSI：ESC [ 参数 中间字节 final（SGR 颜色 \x1b[31m / 光标 \x1b[H / 清屏 \x1b[2J 等）
//
// opencode 在 run --auto 下仍向 stdout 写 TUI 转义码；若原样存进
// code_task.output / change_request.output 会出现 \x1b[0m 类乱码。存库前剥离。
var ansiRe = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x1b\\\\|\x07)?|\x1b\\[[0-9;?]*[ -/]*[@-~]")

// stripANSI 移除 s 中的 ANSI/VT100 转义序列，返回纯文本。
// 末尾再移除残留的裸 ESC（截断/不完整序列），确保存库文本无控制码。
func stripANSI(s string) string {
	return strings.ReplaceAll(ansiRe.ReplaceAllString(s, ""), "\x1b", "")
}
