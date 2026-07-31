# 设计:子项目 B2 —— Windows 原生部署 SSH 端到端

| 版本 | 日期       | 作者     | 状态   |
| ---- | ---------- | -------- | ------ |
| v0.1 | 2026-07-31 | ANP 团队 | 待审核 |

> 上游设计:`docs/superpowers/specs/2026-07-31-服务器节点纳管统一-设计.md`(子项目 B)。
> 本文聚焦 B 的第 2 步:NativeDeployer 走 SSH 把应用部署到 Windows 节点,端到端跑通。

## 1. 背景与目标

统一设计子项目 B 分三步:

- B1. Windows 节点 connect_type=ssh + ssh_password,验证 OS 指标采集(**已完成**,前端补了 ssh_password 字段)。
- **B2. Windows 原生部署端到端:NativeDeployer 经 SSH 上传产物 + 远程执行 + 健康检查(本文)。**
- B3. Windows 应用打包方式(无 Docker,二进制/脚本)——单独需求,本文不定。

B2 目标:让 NativeDeployer 的 SSH 链路在 **Windows 目标机**上真正跑通,并用一个最小 PowerShell 脚本产物在 `.31` Windows 节点上验证「上传 → 执行 → 健康检查」三环全过。

## 2. 现状与缺口

上游设计写「NativeDeployer 已支持 SSH(PutFile+Run),未端到端验」。读码后发现 **Windows 上实际跑不通**,存在三处硬伤:

| 位置                         | 现状(bash 专用)                                     | Windows 上的问题                                                      |
| ---------------------------- | --------------------------------------------------- | --------------------------------------------------------------------- |
| `SSHExecutor.PutFile`        | `echo <b64> \| base64 -d > '<path>'`                | Windows OpenSSH 默认 shell 是 cmd.exe,无 `base64` 命令 → PutFile 失败 |
| `SSHExecutor.Run`            | 把 `renderPowerShell` 生成的 PS 脚本原样 `sess.Run` | 目标默认 shell 若是 cmd.exe,PowerShell cmdlet 全不认 → Run 失败       |
| `NativeDeployer.Deploy` 路径 | `s.Transfer.To + filepath.Base(f)` 拼远程路径       | 构建机是 Linux(.28),`filepath` 走 `/`,拼 `C:\...` 漏分隔符/混斜杠     |

`RenderScript` 本身已 OS 分流(windows→`renderPowerShell`,linux→`renderBash`),这部分不动。

### 2.1 关键约束:`sess.Run` 阻塞

`SSHExecutor.Run` 用 `sess.Run(cmd)`,**会阻塞到命令退出**。因此部署产物不能是前台常驻服务(会卡到 `deployNative` 的 10min 超时再判失败)。产物必须是 **自退出脚本**:跑完即 exit,健康检查靠标记文件存在性判定。

## 3. 设计

### 3.1 方案选型

让 `SSHExecutor` OS 感知,三种形态对比:

| 方案        | 形态                                              | 取舍                                                       |
| ----------- | ------------------------------------------------- | ---------------------------------------------------------- |
| **A(采用)** | `SSHExecutor` 内按 `e.node.OSType` 分流           | 改动最小、localized;SSHExecutor 已持有 `e.node`,不新增类型 |
| B           | 拆 `SSHLinuxExecutor`/`SSHWindowsExecutor` 两类型 | 干净但 dial/认证逻辑重复,为「命令包裹不同」开两类型偏重    |
| C           | 注入 wrap 策略函数                                | 可测但多一层间接,两 case 不值当                            |

**采用 A**。Windows 侧统一用 `powershell -NoProfile -EncodedCommand <base64(UTF-16LE)>` 包裹——跨 cmd/powershell 默认 shell 都稳,绕开引号/换行地狱。命令构造抽成纯函数,单测不打真实服务器即可验。

### 3.2 SSHExecutor 改动(均在 `remote_executor.go`)

```
Run(ctx, cmd):
  if e.node.OSType == "windows":
      cmd = wrapPowerShellScript(cmd)   // powershell -NoProfile -EncodedCommand <b64>
  sess.Run(cmd)                          // 其余不变(退出码语义复用)

PutFile(ctx, local, remote):
  data = read(local); b64 = base64(data)
  if e.node.OSType == "windows":
      cmd = psWriteFileCommand(remote, b64)  // 同样走 EncodedCommand,解出 WriteAllBytes
  else:
      cmd = "echo %s | base64 -d > '%s'"      // linux 不变
  sess.Run(cmd)
```

新增三个纯函数(无副作用、可单测):

- `wrapPowerShellScript(script string) string` —— 返回 `powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand <b64>`,b64 = `base64(utf16le(script))`。加 `-ExecutionPolicy Bypass` 防 .31 执行策略(Restricted)拦上传的 .ps1。
- `psWriteFileCommand(remotePath string, b64 string) string` —— 返回经 EncodedCommand 包裹的 `[IO.File]::WriteAllBytes('<quoted-path>', [Convert]::FromBase64String('<b64>'))`。路径用 `psQuote`(已有,单引号转义)防注入。
- `joinRemotePath(to, base, osType string) string` —— 目标 OS 感知的路径拼接:windows 补 `\`、linux 补 `/`;`to` 已带尾分隔符则不双补。

### 3.3 NativeDeployer 路径拼接修正(`deployer_native.go`)

```go
// before
remote := s.Transfer.To + filepath.Base(f)
// after
remote := joinRemotePath(s.Transfer.To, filepath.Base(f), node.OSType)
```

`NativeDeployer.Deploy` 已持有 `node *DeployNode`,直接取 `node.OSType`。

### 3.4 验证产物 deploy.yaml(最小,自退出)

部署到 `C:\anp\<app>\`,产物 `hello.ps1`(写标记文件后**正常返回,不用 `exit`**——因 `Run` 已把整段渲染脚本包进 `powershell -EncodedCommand`,`& script.ps1` 内若 `exit` 会退出宿主进程、跳过后续 healthcheck)。`run.cmd`/`healthcheck.cmd` 写**裸 PowerShell 表达式**(不要再包 `powershell -Command`,否则双层嵌套):

```yaml
target:
  os: windows
  dir: C:\anp\<app>\
steps:
  - transfer: { from: ./hello.ps1, to: C:\anp\<app>\ }
  - run: { cmd: "& 'C:\\anp\\<app>\\hello.ps1'" }
  - healthcheck:
      cmd: "if(-not (Test-Path 'C:\\anp\\<app>\\marker.txt')){exit 1}"
      timeout: 30s
```

`hello.ps1`:写 `C:\anp\<app>\marker.txt`(内容 = 应用名 + `Get-Date` 时间戳)后正常返回。healthcheck 仅在标记**缺失**时 `exit 1`(失败);存在则落到脚本尾隐式 `exit 0` → Deploy 成功。

## 4. 改动点清单

| 文件                 | 改动                                                                                                                 |
| -------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `remote_executor.go` | `SSHExecutor.Run`/`PutFile` 加 windows 分支;新增 `wrapPowerShellScript`/`psWriteFileCommand`/`joinRemotePath` 纯函数 |
| `deployer_native.go` | 远程路径改用 `joinRemotePath(to, base, node.OSType)`                                                                 |
| 单测(新)             | 三个纯函数的纯单测(不连服务器)                                                                                       |

**不动**:`RenderScript`/`renderPowerShell`、`WinRMExecutor`、`NativeDeployer.Deploy` 主流程、handler `deployNative`、DeployNode 模型、前端。

## 5. 错误处理

- 非零退出语义复用现有链路:`SSHExecutor.Run` 已把 `*ssh.ExitError` 转 error(handler `deployNative` 回写 instance=failed + build_log)。Windows 分支不新增错误类型。
- `EncodedCommand` 构造是纯内存操作,理论上不失败;UTF-16LE 编码/base64 为标准库,无兜底需要。

## 6. 测试

### 6.1 单测(纯函数,不连服务器,避 sqlite/PG 类型陷阱)

- `wrapPowerShellScript`:输入多行 PS → 输出以 `powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand ` 开头;尾部 b64 解码为 UTF-16LE 后等于原脚本(含换行/中文/引号)。
- `psWriteFileCommand`:输出可解出 `[IO.File]::WriteAllBytes('<path>',[Convert]::FromBase64String('<b64>'))`;其 b64 解码 = 原文件字节;路径含单引号时被 `psQuote` 转义。
- `joinRemotePath`:`C:\a\b` + `x.ps1` → `C:\a\b\x.ps1`;`C:\a\b\` + `x.ps1` → `C:\a\b\x.ps1`(不双补);`/opt/a` + `x` → `/opt/a/x`。

### 6.2 E2E(.28 后端 → .31 Windows 节点,按 deploy-28-no-local-test)

1. `.31` 已是 connect_type=ssh + ssh_password 的 windows 节点(B1 配好)。
2. 选一个应用,其 RepoDir 放 `deploy.yaml` + `hello.ps1`(§3.4)。
3. 前端 `/servers` 触发部署到 `.31`。
4. 期望:instance.status=running;.31 上 `C:\anp\<app>\marker.txt` 实际生成。

## 7. 范围

- **B2 做**:修 SSHExecutor OS 感知(PutFile/Run 走 PowerShell EncodedCommand)+ 修路径拼接 + 最小 PS 脚本产物 .31 端到端验证。
- **B2 不做(留 B3)**:Windows 应用正式打包方式(二进制交叉编译 / 框架依赖 / 常驻服务化)。B2 的「自退出脚本 + marker」是验证链路用的最小形态,不代表生产打包方案。
- 常驻服务部署受 `sess.Run` 阻塞约束,B2 不解(属 B3 命题,届时考虑后台启动 / Windows 服务注册)。
