package appdeploy

import "strings"

// redactOut 把输出中已知密钥值替换为 ***（Go 层权威脱敏，spec §3 精化第 2 条）。
// aiDeploy 把 opencode 输出 / shim 日志落 build_log 前必须过此函数。纯函数。
// 空串 secret 跳过（否则把整个输出清空）。
func redactOut(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec == "" {
			continue
		}
		s = strings.ReplaceAll(s, sec, "***")
	}
	return s
}
