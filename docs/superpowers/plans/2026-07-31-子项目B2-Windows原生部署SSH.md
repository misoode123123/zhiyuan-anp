# 子项目 B2 Windows 原生部署 SSH 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 NativeDeployer 的 SSH 链路在 Windows 目标机上真正跑通(PutFile/Run 走 PowerShell),并用最小 PowerShell 脚本产物在 `.31` 上端到端验证「上传→执行→健康检查」。

**Architecture:** 不改 NativeDeployer 主流程,只在 `SSHExecutor` 内按 `node.OSType` 分流:Windows 侧把命令包成 `powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand <base64(UTF-16LE)>`(跨 cmd/powershell 默认 shell 都稳)。命令构造抽成 3 个纯函数 + 2 个 SSHExecutor 方法,单测不打真实服务器。顺带修 `NativeDeployer.Deploy` 的远程路径拼接(Linux 机构建 Windows 路径漏分隔符)。

**Tech Stack:** Go 1.x(`platform/backend/internal/appdeploy` 包)、`golang.org/x/crypto/ssh`、`encoding/binary`+`unicode/utf16`(UTF-16LE)、`encoding/base64`。验证产物 PowerShell。

## Global Constraints

- **禁 SQLite,只用 PG**:本计划单测全是纯函数(不触 DB);任何会触 DB 的测试不在本计划范围。
- **Go 命令前缀**:`GOPATH=C:/Users/yxt/go`(本机 GOPATH 被污染,见 memory)。所有 `go test`/`go build` 命令必须加此前缀。
- **单测可本机跑**:纯函数单测 OS 无关、不连服务器、不触 DB,直接本机 `go test`。
- **功能/E2E 走 .28**:本机不跑功能测试;E2E 在 `.28` 后端重建后对 `.31` 验证(见 Task 6)。
- **文档随代码进 git**:实现完成后写实现记录文档并 commit(见 Task 7)。
- 包路径:`platform/backend/internal/appdeploy`;命令在 `platform/backend/` 下执行。

## File Structure

| 文件                                                                     | 责任                                                     | 动作   |
| ------------------------------------------------------------------------ | -------------------------------------------------------- | ------ |
| `platform/backend/internal/appdeploy/remote_executor.go`                 | SSH/WinRM 远程执行器;新增 OS 感知命令构造                | Modify |
| `platform/backend/internal/appdeploy/remote_executor_powershell_test.go` | 3 纯函数 + 2 方法的纯单测(不连服务器)                    | Create |
| `platform/backend/internal/appdeploy/deployer_native.go`                 | 原生部署主流程;修远程路径拼接                            | Modify |
| `platform/backend/internal/appdeploy/deployer_native_test.go`            | 补 Windows 路径拼接测试(复用既有 `recordingPutExecutor`) | Modify |

**不动**:`RenderScript`/`renderPowerShell`、`WinRMExecutor`、`NativeDeployer.Deploy` 主流程、handler `deployNative`、`DeployNode` 模型、前端。

---

### Task 1: 纯函数 `joinRemotePath`(目标 OS 感知路径拼接)

**Files:**

- Modify: `platform/backend/internal/appdeploy/remote_executor.go`(文件末尾追加函数)
- Test: `platform/backend/internal/appdeploy/remote_executor_powershell_test.go`(新建)

**Interfaces:**

- Produces: `func joinRemotePath(to, base, osType string) string` —— windows 补 `\`、linux 补 `/`;`to` 已带尾分隔符(`\` 或 `/`)则不双补。

- [ ] **Step 1: 写失败测试(新建测试文件)**

新建 `platform/backend/internal/appdeploy/remote_executor_powershell_test.go`:

```go
package appdeploy

import (
	"testing"
)

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
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestJoinRemotePath -v
```

Expected: FAIL(`joinRemotePath undefined`)。

- [ ] **Step 3: 写最小实现**

在 `remote_executor.go` 末尾(`psQuote` 函数后)追加:

```go
// joinRemotePath 按目标 OS 拼接远程路径:windows 用反斜杠、linux 用正斜杠。
// to 已带尾分隔符(\ 或 /)则直接接 base,不双补。构建机是 Linux,filepath.Join
// 会用 / 拼 Windows 路径出错,故这里手动按目标 OS 分隔符拼。
func joinRemotePath(to, base, osType string) string {
	if osType == "windows" {
		if strings.HasSuffix(to, `\`) || strings.HasSuffix(to, `/`) {
			return to + base
		}
		return to + `\` + base
	}
	if strings.HasSuffix(to, `/`) {
		return to + base
	}
	return to + `/` + base
}
```

- [ ] **Step 4: 跑测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestJoinRemotePath -v
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/appdeploy/remote_executor.go platform/backend/internal/appdeploy/remote_executor_powershell_test.go
git commit -m "feat(appdeploy): joinRemotePath 目标OS感知路径拼接"
```

---

### Task 2: 纯函数 `wrapPowerShellScript`(EncodedCommand 包裹)

**Files:**

- Modify: `platform/backend/internal/appdeploy/remote_executor.go`(加 import + 函数)
- Test: `platform/backend/internal/appdeploy/remote_executor_powershell_test.go`(追加测试 + 解码helper)

**Interfaces:**

- Produces: `func wrapPowerShellScript(script string) string` —— 返回 `powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand <b64>`,b64 = `base64(UTF-16LE(script))`。

- [ ] **Step 1: 写失败测试**

在 `remote_executor_powershell_test.go` 顶部 import 块加 `"encoding/binary"`、`"encoding/base64"`、`"strings"`、`"unicode/utf16"`,并追加:

```go
// decodeEncodedCommand 测试helper:把 wrapPowerShellScript 的输出解回原脚本。
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
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestWrapPowerShellScript -v
```

Expected: FAIL(`wrapPowerShellScript undefined`)。

- [ ] **Step 3: 写最小实现**

在 `remote_executor.go` 的 import 块加两行(import 顺序按字母序;`encoding/base64` 已在,补 `encoding/binary` 和 `unicode/utf16`):

```go
import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"

	"github.com/masterzen/winrm"
	"golang.org/x/crypto/ssh"
)
```

在文件末尾追加函数:

```go
// wrapPowerShellScript 把任意 PowerShell 脚本包成
// `powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand <b64>`,
// b64 = base64(UTF-16LE(script))。-EncodedCommand 是跨 cmd/powershell 默认 shell
// 都稳的执行方式(绕开引号/换行地狱);-ExecutionPolicy Bypass 防 Restricted 策略拦上传的 .ps1。
func wrapPowerShellScript(script string) string {
	u16 := utf16.Encode([]rune(script))
	buf := make([]byte, len(u16)*2)
	for i, v := range u16 {
		binary.LittleEndian.PutUint16(buf[i*2:], v)
	}
	return "powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand " + base64.StdEncoding.EncodeToString(buf)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestWrapPowerShellScript -v
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/appdeploy/remote_executor.go platform/backend/internal/appdeploy/remote_executor_powershell_test.go
git commit -m "feat(appdeploy): wrapPowerShellScript EncodedCommand 包裹"
```

---

### Task 3: 纯函数 `psWriteFileCommand`(Windows PutFile 命令)

**Files:**

- Modify: `platform/backend/internal/appdeploy/remote_executor.go`(追加函数)
- Test: `platform/backend/internal/appdeploy/remote_executor_powershell_test.go`(追加测试)

**Interfaces:**

- Produces: `func psWriteFileCommand(remotePath, b64 string) string` —— 内层 `[IO.File]::WriteAllBytes('<psQuote(remotePath)>', [Convert]::FromBase64String('<b64>'))`,外层经 `wrapPowerShellScript` 包裹。复用既有 `psQuote`(单引号转义防注入)。

- [ ] **Step 1: 写失败测试**

在 `remote_executor_powershell_test.go` 追加:

```go
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
	// 内含原 b64(base64 字母表无单引号,安全内联)
	if !strings.Contains(inner, b64) {
		t.Errorf("inner %q 不含 b64", inner)
	}
	// 含 WriteAllBytes 调用
	if !strings.Contains(inner, "[IO.File]::WriteAllBytes(") {
		t.Errorf("inner %q 不含 WriteAllBytes", inner)
	}

	// 路径含单引号时被 psQuote 转义(' -> '')
	got2 := psWriteFileCommand(`C:\a'b\x`, b64)
	inner2 := decodeEncodedCommand(t, got2)
	if !strings.Contains(inner2, `'C:\a''b\x'`) {
		t.Errorf("单引号未转义: %q", inner2)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestPsWriteFileCommand -v
```

Expected: FAIL(`psWriteFileCommand undefined`)。

- [ ] **Step 3: 写最小实现**

在 `remote_executor.go` 末尾追加(`psQuote` 已存在,直接复用):

```go
// psWriteFileCommand 构造 Windows PutFile 命令:用 PowerShell 把 b64 解码写盘,
// 整体经 wrapPowerShellScript 包成 EncodedCommand。remotePath 用 psQuote 转义防注入。
// b64 字母表([A-Za-z0-9+/=])无单引号,内联进 PS 单引号串安全。
func psWriteFileCommand(remotePath, b64 string) string {
	ps := fmt.Sprintf("[IO.File]::WriteAllBytes('%s', [Convert]::FromBase64String('%s'))", psQuote(remotePath), b64)
	return wrapPowerShellScript(ps)
}
```

- [ ] **Step 4: 跑测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestPsWriteFileCommand -v
```

Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/appdeploy/remote_executor.go platform/backend/internal/appdeploy/remote_executor_powershell_test.go
git commit -m "feat(appdeploy): psWriteFileCommand Windows PutFile 命令"
```

---

### Task 4: `runShell`/`putShell` 方法 + 接入 SSHExecutor.Run/PutFile

**Files:**

- Modify: `platform/backend/internal/appdeploy/remote_executor.go`(`SSHExecutor.Run`、`SSHExecutor.PutFile` 改调新方法 + 新增两方法)
- Test: `platform/backend/internal/appdeploy/remote_executor_powershell_test.go`(追加方法测试)

**Interfaces:**

- Produces:
  - `func (e *SSHExecutor) runShell(cmd string) string` —— windows 返回 `wrapPowerShellScript(cmd)`,否则原样。
  - `func (e *SSHExecutor) putShell(remotePath, b64 string) string` —— windows 返回 `psWriteFileCommand(remotePath, b64)`,否则返回既有 linux 命令串(逐字不变,避免回归)。

- [ ] **Step 1: 写失败测试**

在 `remote_executor_powershell_test.go` 追加:

```go
func TestSSHExecutorRunShell(t *testing.T) {
	win := &SSHExecutor{node: &DeployNode{OSType: "windows"}}
	lin := &SSHExecutor{node: &DeployNode{OSType: "linux"}}

	// windows:包成 EncodedCommand,解回等于原 cmd
	got := win.runShell("Get-Process")
	if decodeEncodedCommand(t, got) != "Get-Process" {
		t.Errorf("windows runShell 未正确包裹: %q", got)
	}
	// linux:原样
	if lin.runShell("ls -la") != "ls -la" {
		t.Errorf("linux runShell 应原样: %q", lin.runShell("ls -la"))
	}
}

func TestSSHExecutorPutShell(t *testing.T) {
	b64 := "AAAA" // 任意合法 b64
	win := &SSHExecutor{node: &DeployNode{OSType: "windows"}}
	lin := &SSHExecutor{node: &DeployNode{OSType: "linux"}}

	// windows:解出 WriteAllBytes
	got := win.putShell(`C:\x\f.bin`, b64)
	if !strings.Contains(decodeEncodedCommand(t, got), "[IO.File]::WriteAllBytes(") {
		t.Errorf("windows putShell 未生成 WriteAllBytes: %q", got)
	}
	// linux:既有 base64 -d 串(逐字)
	wantLin := fmt.Sprintf("echo %s | base64 -d > %s", b64, sshQuote(`C:\x\f.bin`))
	if lin.putShell(`C:\x\f.bin`, b64) != wantLin {
		t.Errorf("linux putShell 应逐字保留:\n got: %q\nwant: %q", lin.putShell(`C:\x\f.bin`, b64), wantLin)
	}
}
```

注意:测试文件 import 块需补 `"fmt"`(Step 1 一并加进 import)。最终 `remote_executor_powershell_test.go` 的 import 块为:

```go
import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"
)
```

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run 'TestSSHExecutor(Run|Put)Shell' -v
```

Expected: FAIL(`runShell`/`putShell undefined`)。

- [ ] **Step 3: 写最小实现**

在 `remote_executor.go` 的 `SSHExecutor` 内,把 `Run` 和 `PutFile` 改为调新方法,并新增两方法。

改 `SSHExecutor.Run` 的执行行(原 `err = sess.Run(cmd)`,约 remote_executor.go:122):

```go
	err = sess.Run(e.runShell(cmd))
```

改 `SSHExecutor.PutFile` 的返回行(原 `return sess.Run(fmt.Sprintf("echo %s | base64 -d > '%s'", b64, sshQuote(remotePath)))`,约 remote_executor.go:155):

```go
	return sess.Run(e.putShell(remotePath, b64))
```

在 `SSHExecutor.TestConnection` 后、`sshPort` 辅助函数前,新增两方法:

```go
// runShell 按 node.OS 决定 Run 的最终命令:windows 包成 powershell -EncodedCommand,
// linux 原样。纯逻辑(无 IO),便于单测。
func (e *SSHExecutor) runShell(cmd string) string {
	if e.node != nil && e.node.OSType == "windows" {
		return wrapPowerShellScript(cmd)
	}
	return cmd
}

// putShell 按 node.OS 决定 PutFile 的最终命令:windows 用 PowerShell 写盘,
// linux 逐字保留既有 base64 -d 串(不改 linux 行为,避免回归)。纯逻辑(无 IO)。
func (e *SSHExecutor) putShell(remotePath, b64 string) string {
	if e.node != nil && e.node.OSType == "windows" {
		return psWriteFileCommand(remotePath, b64)
	}
	return fmt.Sprintf("echo %s | base64 -d > '%s'", b64, sshQuote(remotePath))
}
```

- [ ] **Step 4: 跑测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run 'TestSSHExecutor(Run|Put)Shell' -v
```

Expected: PASS。

- [ ] **Step 5: 跑包内全测确认无回归**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -v
```

Expected: 全 PASS(含既有测试;若有触 sqlite 的 node_test 等仍应通过,本机无 PG 依赖)。若个别 DB 用例因环境失败,记录但不阻塞(它们与本次改动无关,且功能验证走 .28)。

- [ ] **Step 6: Commit**

```bash
git add platform/backend/internal/appdeploy/remote_executor.go platform/backend/internal/appdeploy/remote_executor_powershell_test.go
git commit -m "feat(appdeploy): SSHExecutor Run/PutFile OS感知(Windows 走 EncodedCommand)"
```

---

### Task 5: `NativeDeployer.Deploy` 远程路径拼接修正

**Files:**

- Modify: `platform/backend/internal/appdeploy/deployer_native.go:36`
- Test: `platform/backend/internal/appdeploy/deployer_native_test.go`(追加 windows 路径用例)

**Interfaces:**

- Consumes: `joinRemotePath(to, base, osType string) string`(Task 1)。
- `NativeDeployer.Deploy` 已持有 `node *DeployNode`,直接取 `node.OSType`。

- [ ] **Step 1: 写失败测试**

在 `deployer_native_test.go` 末尾追加(复用既有 `recordingPutExecutor`):

```go
func TestNativeDeployer_Deploy_WindowsPathJoin(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.ps1"), []byte("write marker"), 0644); err != nil {
		t.Fatal(err)
	}
	desc := &DeployDesc{
		Target: TargetDesc{OS: "windows", Dir: `C:\anp\app`},
		Steps: []StepDesc{
			// To 不带尾分隔符,验证 joinRemotePath 补 \
			{Transfer: &TransferStep{From: dir + "/*", To: `C:\anp\app`}},
		},
	}
	fake := &recordingPutExecutor{ran: new([]string)}
	_, err := (&NativeDeployer{}).Deploy(
		context.Background(),
		&Application{ID: "app_1", RepoDir: dir},
		&DeployNode{OSType: "windows"},
		fake, desc,
	)
	if err != nil {
		t.Fatalf("Deploy err: %v", err)
	}
	// 远程路径应为 C:\anp\app\hello.ps1(补了 \)
	want := `->C:\anp\app\hello.ps1`
	found := false
	for _, p := range fake.puts {
		if strings.HasSuffix(p, want) {
			found = true
		}
	}
	if !found {
		t.Errorf("未拼出 Windows 远程路径 %q,puts=%v", want, fake.puts)
	}
}
```

`deployer_native_test.go` 的 import 块已含 `context`/`os`/`path/filepath`/`testing`;需补 `"strings"`(若未有)。检查既有 import,缺则加。

- [ ] **Step 2: 跑测试确认失败**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestNativeDeployer_Deploy_WindowsPathJoin -v
```

Expected: FAIL(远程路径是 `C:\anp\apphello.ps1`,漏分隔符,不匹配 `C:\anp\app\hello.ps1`)。

- [ ] **Step 3: 写最小实现**

改 `deployer_native.go:36`:

```go
		for _, f := range files {
			remote := joinRemotePath(s.Transfer.To, filepath.Base(f), node.OSType)
			if err := exec.PutFile(ctx, f, remote); err != nil {
```

(原:`remote := s.Transfer.To + filepath.Base(f)`)

- [ ] **Step 4: 跑测试确认通过**

Run:

```bash
cd platform/backend && GOPATH=C:/Users/yxt/go go test ./internal/appdeploy/ -run TestNativeDeployer_Deploy -v
```

Expected: PASS(含既有 `TestNativeDeployer_Deploy_PutFileAndRun` 与新 windows 用例)。

- [ ] **Step 5: Commit**

```bash
git add platform/backend/internal/appdeploy/deployer_native.go platform/backend/internal/appdeploy/deployer_native_test.go
git commit -m "fix(appdeploy): NativeDeployer 远程路径按目标OS拼接(Windows 漏分隔符)"
```

---

### Task 6: E2E 验证(.28 后端 → .31 Windows 节点)

**Files:** 无代码改动;在 `.28` / `.31` 上执行验证。

**前置**:

- `.31` 已是 `connect_type=ssh` + `os_type=windows` + `ssh_password` 的节点(B1 配好)。
- `.28` 后端源码在 `/opt/anp`,keyless SSH 到 `root@10.10.0.28`(见 memory `deploy-prod-10.10.0.28`)。

- [ ] **Step 1: 推代码到 .28**

```bash
# 仓库已 commit(Task1-5);推到 origin 后 .28 拉,或直接 scp 改动文件
scp platform/backend/internal/appdeploy/remote_executor.go \
    platform/backend/internal/appdeploy/deployer_native.go \
    root@10.10.0.28:/opt/anp/platform/backend/internal/appdeploy/
```

(若 .28 走 git pull,则在 .28 执行 `cd /opt/anp && git pull`。)

- [ ] **Step 2: .28 重建后端**

```bash
ssh root@10.10.0.28 'cd /opt/anp && docker-compose build backend && docker-compose up -d backend'
```

Expected: backend 容器重建启动,日志无编译错误。

- [ ] **Step 3: 在目标应用 RepoDir 放验证产物**

选一个应用(或新建),其 RepoDir(`/data/repos/<appname>/`)放两个文件:

`/data/repos/<appname>/deploy.yaml`(把 `<appname>` 换成实际应用名):

```yaml
target:
  os: windows
  dir: C:\anp\<appname>\
steps:
  - transfer: { from: ./hello.ps1, to: C:\anp\<appname>\ }
  - run: { cmd: "& 'C:\\anp\\<appname>\\hello.ps1'" }
  - healthcheck:
      cmd: "if(-not (Test-Path 'C:\\anp\\<appname>\\marker.txt')){exit 1}"
      timeout: 30s
```

`/data/repos/<appname>/hello.ps1`:

```powershell
# B2 链路验证:写 marker 后正常返回(不用 exit,避免退出宿主跳过 healthcheck)
$app = '<appname>'
$content = "$app deployed at $(Get-Date -Format 'o')"
[IO.File]::WriteAllText("C:\anp\$app\marker.txt", $content)
```

可用 heredoc 在 .28 上写:

```bash
ssh root@10.10.0.28 "cat > /data/repos/<appname>/hello.ps1" <<'PS'
$app = '<appname>'
[IO.File]::WriteAllText("C:\anp\$app\marker.txt", "$app deployed at $(Get-Date -Format 'o')")
PS
```

- [ ] **Step 4: 前端触发部署到 .31**

浏览器 `http://10.10.0.28:8088`(Ctrl+F5 硬刷),应用页对该应用「部署」,`node_id` 选 `.31` 的 windows+ssh 节点,env=test。

Expected: 接口返回 `building`,前端进度条出现。

- [ ] **Step 5: 验证 instance 成功**

轮询应用详情/实例,期望:

- `instance.status = running`
- `instance.last_error` 空
- `build_log` 含「上传 → 执行 → healthcheck」过程且 exit 0

- [ ] **Step 6: 验证 .31 上 marker 实际生成**

从 .28(或本机能直连 .31)SSH 到 .31 检查:

```bash
ssh <winuser>@<.31-host> 'powershell -Command "Get-Content C:\anp\<appname>\marker.txt"'
```

Expected: 输出 `<appname> deployed at <时间戳>`。

若失败:看 `.28` backend 日志(`docker logs anp-backend`)的 `build_log`,定位是 PutFile 失败(EncodedCommand 构造)、Run 失败(默认 shell)、还是路径拼接。回单测定位修复。

- [ ] **Step 7: 记录 E2E 结果**

把验证结果(成功/失败 + 关键日志摘要)记入 Task 7 的实现记录文档。无需 commit(本步无代码改动)。

---

### Task 7: 实现记录文档 + commit

**Files:**

- Create: `docs/2026-07-31-子项目B2-Windows原生部署SSH-实现记录.md`

- [ ] **Step 1: 写实现记录**

记录:背景缺口(三处)、实现(3 纯函数 + 2 方法 + 路径修复)、单测结果、.28→.31 E2E 结果(instance running + marker 生成)、遗留(B3 打包方式 / 常驻服务)。模板见既有 `docs/2026-07-31-子项目B1-SSH密码字段前端-实现记录.md`。

- [ ] **Step 2: Commit**

```bash
git add docs/2026-07-31-子项目B2-Windows原生部署SSH-实现记录.md
git commit -m "docs(server-mgmt): 子项目B2 Windows原生部署SSH 实现记录"
```

---

## Self-Review(已自检)

**Spec coverage:**

- §3.2 SSHExecutor Run/PutFile windows 分支 + 3 纯函数 → Task 1/2/3/4 ✅
- §3.3 NativeDeployer 路径拼接修正 → Task 5 ✅
- §3.4 验证产物 deploy.yaml + hello.ps1 → Task 6 Step 3 ✅
- §6.1 单测(3 纯函数)→ Task 1/2/3 ✅(另加 Task 4 方法测试 + Task 5 路径测试)
- §6.2 E2E → Task 6 ✅
- §4 改动点清单 / §7 范围(B3 留待后续)→ Task 7 记录 + 不涉代码 ✅

**Placeholder scan:** 无 TBD/TODO;所有代码块完整;`<appname>` 在 Task 6 是运行时变量(应用名),Step 3 已注明替换。

**Type consistency:** `joinRemotePath(to, base, osType string) string` / `wrapPowerShellScript(script string) string` / `psWriteFileCommand(remotePath, b64 string) string` / `(e *SSHExecutor) runShell(cmd string) string` / `(e *SSHExecutor) putShell(remotePath, b64 string) string` —— 各 Task 定义与调用签名一致;`psQuote`、`sshQuote`、`recordingPutExecutor` 为既有符号,直接复用。
