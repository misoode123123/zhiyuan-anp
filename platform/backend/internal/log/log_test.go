package log

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// tempLogDir 建临时目录；cleanup 删除失败（Windows 下 lumberjack 仍占用文件）不标记测试失败。
// 用 os.MkdirTemp 而非 t.TempDir：后者的 cleanup 在 RemoveAll 失败时会 t.Errorf 标记 FAIL。
func tempLogDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "logtest")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// TestNew_ReplacesGlobal New(Config) 后全局 zap.L() 被替换为真实 logger（enable 对应 level）。
func TestNew_ReplacesGlobal(t *testing.T) {
	zap.ReplaceGlobals(zap.NewNop())
	t.Cleanup(func() { zap.ReplaceGlobals(zap.NewNop()) })

	_ = New(Config{Level: "warn"})

	if !zap.L().Core().Enabled(zapcore.WarnLevel) {
		t.Fatal("New 后 zap.L() 应 enable Warn（真实 logger），仍是 nop")
	}
}

// TestNew_FileOutput output=file 时日志写入指定文件。
func TestNew_FileOutput(t *testing.T) {
	dir := tempLogDir(t)
	f := filepath.Join(dir, "app.log")

	_ = New(Config{Level: "info", Output: "file", File: f, MaxSizeMB: 1, MaxBackups: 1, MaxAgeDays: 1})
	zap.L().Info("hello-file")

	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("读日志文件: %v", err)
	}
	if !strings.Contains(string(data), "hello-file") {
		t.Fatalf("日志文件应含 hello-file，得到 %q", string(data))
	}
}

// TestNew_ErrorFileOnlyErrors ErrorFile 只含 ERROR+，不含 INFO。
func TestNew_ErrorFileOnlyErrors(t *testing.T) {
	dir := tempLogDir(t)
	appF := filepath.Join(dir, "app.log")
	errF := filepath.Join(dir, "error.log")

	_ = New(Config{Level: "info", Output: "file", File: appF, ErrorFile: errF, MaxSizeMB: 1, MaxBackups: 1, MaxAgeDays: 1})
	zap.L().Info("info-msg")
	zap.L().Error("error-msg")

	errData, _ := os.ReadFile(errF)
	if !strings.Contains(string(errData), "error-msg") {
		t.Fatalf("error.log 应含 error-msg，得到 %q", string(errData))
	}
	if strings.Contains(string(errData), "info-msg") {
		t.Fatalf("error.log 不应含 info-msg（只 Error+），得到 %q", string(errData))
	}
}

// TestNew_FormatJSONFile format=json 时日志文件为 JSON 行（含 "msg" 字段）。
func TestNew_FormatJSONFile(t *testing.T) {
	dir := tempLogDir(t)
	f := filepath.Join(dir, "app.log")

	_ = New(Config{Level: "info", Format: "json", Output: "file", File: f, MaxSizeMB: 1, MaxBackups: 1, MaxAgeDays: 1})
	zap.L().Info("json-msg")

	data, _ := os.ReadFile(f)
	if !strings.Contains(string(data), `"msg":"json-msg"`) {
		t.Fatalf("json 格式应含 \"msg\":\"json-msg\"，得到 %q", string(data))
	}
}
