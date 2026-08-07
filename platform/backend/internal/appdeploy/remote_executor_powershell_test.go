package appdeploy

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)

// decodeEncodedCommand 测试 helper：把 wrapPowerShellScript 的输出解回原脚本。
func decodeEncodedCommand(t *testing.T, wrapped string) string {
	t.Helper()
	const prefix = "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand "
	if !strings.HasPrefix(wrapped, prefix) {
		t.Fatalf("missing prefix: %q", wrapped)
	}
	enc := strings.TrimPrefix(wrapped, prefix)
	dec, err := base64.StdEncoding.DecodeString(enc)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	u16 := make([]uint16, len(dec)/2)
	for i := range u16 {
		u16[i] = binary.LittleEndian.Uint16(dec[i*2:])
	}
	return string(utf16.Decode(u16))
}

func TestWrapPowerShellScript(t *testing.T) {
	script := "Get-Date\nWrite-Host '中文' \"quoted\"" // 多行 + 中文 + 引号
	got := wrapPowerShellScript(script)
	if !strings.HasPrefix(got, "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ") {
		t.Fatalf("missing prefix: %q", got)
	}
	if decodeEncodedCommand(t, got) != script {
		t.Fatalf("round-trip mismatch: %q", got)
	}
}

func TestPsWriteFileCommand(t *testing.T) {
	data := []byte("hello\nworld")
	b64 := base64.StdEncoding.EncodeToString(data)
	got := psWriteFileCommand(`C:\anp\app\hello.ps1`, b64)
	inner := decodeEncodedCommand(t, got)
	// 路径被 psQuote 包成单引号串
	wantPath := `'C:\anp\app\hello.ps1'`
	if !strings.Contains(inner, wantPath) {
		t.Errorf("inner %q 不含路径 %q", inner, wantPath)
	}
	// 内含原 b64（base64 字母表无单引号，安全内联）
	if !strings.Contains(inner, b64) {
		t.Errorf("inner %q 不含 b64", inner)
	}
	// 含 WriteAllBytes 调用
	if !strings.Contains(inner, "[IO.File]::WriteAllBytes(") {
		t.Errorf("inner %q 不含 WriteAllBytes", inner)
	}

	// 路径含单引号时被 psQuote 转义（' -> ''）
	got2 := psWriteFileCommand(`C:\a'b\x`, b64)
	inner2 := decodeEncodedCommand(t, got2)
	if !strings.Contains(inner2, `'C:\a''b\x'`) {
		t.Errorf("单引号未转义: %q", inner2)
	}
}

func TestSSHExecutorRunShell(t *testing.T) {
	win := &SSHExecutor{node: &DeployNode{OSType: "windows"}}
	lin := &SSHExecutor{node: &DeployNode{OSType: "linux"}}

	// windows: 包成 EncodedCommand，解回等于原 cmd
	got := win.runShell("Get-Process")
	if decodeEncodedCommand(t, got) != "Get-Process" {
		t.Errorf("windows runShell 未正确包裹: %q", got)
	}
	// linux: 原样
	if lin.runShell("ls -la") != "ls -la" {
		t.Errorf("linux runShell 应原样: %q", lin.runShell("ls -la"))
	}
}

func TestSSHExecutorPutShell(t *testing.T) {
	b64 := "AAAA" // 任意合法 b64
	win := &SSHExecutor{node: &DeployNode{OSType: "windows"}}
	lin := &SSHExecutor{node: &DeployNode{OSType: "linux"}}

	// windows: 解出 WriteAllBytes
	got := win.putShell(`C:\x\f.bin`, b64)
	if !strings.Contains(decodeEncodedCommand(t, got), "[IO.File]::WriteAllBytes(") {
		t.Errorf("windows putShell 未生成 WriteAllBytes: %q", got)
	}
	// linux: 既有 base64 -d 串（逐字）
	wantLin := fmt.Sprintf("echo %s | base64 -d > '%s'", b64, sshQuote(`C:\x\f.bin`))
	if lin.putShell(`C:\x\f.bin`, b64) != wantLin {
		t.Errorf("linux putShell 应逐字保留:\n got: %q\nwant: %q", lin.putShell(`C:\x\f.bin`, b64), wantLin)
	}
}

func TestJoinRemotePath(t *testing.T) {
	cases := []struct {
		to, base, osType, want string
	}{
		{`C:\a\b`, "x.ps1", "windows", `C:\a\b\x.ps1`},
		{`C:\a\b\`, "x.ps1", "windows", `C:\a\b\x.ps1`}, // 已带尾分隔符不双补
		{`C:\a\b/`, "x.ps1", "windows", `C:\a\b/x.ps1`}, // 混用 / 也算尾分隔符
		{"/opt/a", "x", "linux", "/opt/a/x"},
		{"/opt/a/", "x", "linux", "/opt/a/x"},
		{"", "x", "linux", "/x"}, // to 空:补分隔符 + base(边界)
	}
	for _, c := range cases {
		if got := joinRemotePath(c.to, c.base, c.osType); got != c.want {
			t.Errorf("joinRemotePath(%q,%q,%q) = %q, want %q", c.to, c.base, c.osType, got, c.want)
		}
	}
}
