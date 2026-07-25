package log

import (
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TestNew_ReplacesGlobal New 应 ReplaceGlobals，使 zap.L() 变成真实 logger（enable 对应 level）。
// 业务 handler（httpx.Err / appdeploy 等）直接用 zap.L()，依赖此全局已就绪。
// 先把全局置为 nop（不 enable Warn），调 New("warn") 后全局应被替换为 warn logger。
func TestNew_ReplacesGlobal(t *testing.T) {
	zap.ReplaceGlobals(zap.NewNop()) // 起始：全局为 nop
	t.Cleanup(func() { zap.ReplaceGlobals(zap.NewNop()) })

	_ = New("warn")

	// 若 New 未调 ReplaceGlobals，zap.L() 仍是 nop（Core 不 enable WarnLevel）
	if !zap.L().Core().Enabled(zapcore.WarnLevel) {
		t.Fatal("New 后 zap.L() 应被替换为真实 logger（enable Warn），仍是 nop——New 未调 ReplaceGlobals")
	}
}
