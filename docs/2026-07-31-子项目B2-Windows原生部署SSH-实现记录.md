# 子项目 B2 —— Windows 原生部署 SSH 端到端 实现记录

| 日期       | 作者     | 关联设计/计划                                                                                              | 状态    |
| ---------- | -------- | ---------------------------------------------------------------------------------------------------------- | ------- |
| 2026-07-31 | ANP 团队 | `specs/2026-07-31-子项目B2-Windows原生部署SSH-设计.md` / `plans/2026-07-31-子项目B2-Windows原生部署SSH.md` | ✅ 闭环 |

## 1. 目标回顾

让 NativeDeployer 的 SSH 链路在 **Windows 目标机**上真正跑通,并用最小 PowerShell 脚本产物在 `.31` 上验证「上传 → 执行 → 健康检查」三环。打包方式留 B3。

## 2. 缺口(读码 + E2E 发现)

### 2.1 读码发现的三处硬伤(设计文档已修正「已支持 SSH」的误判)

| 位置                         | 问题                                                                                               |
| ---------------------------- | -------------------------------------------------------------------------------------------------- |
| `SSHExecutor.PutFile`        | `echo <b64> \| base64 -d > '<path>'` 纯 bash;Windows OpenSSH 默认 shell 无 `base64` → PutFile 失败 |
| `SSHExecutor.Run`            | 把 `renderPowerShell` 生成的 PS 脚本原样 `sess.Run`;目标默认 shell 若是 cmd.exe,PS cmdlet 全不认   |
| `NativeDeployer.Deploy` 路径 | `s.Transfer.To + filepath.Base(f)`;构建机是 Linux,`filepath` 走 `/`,拼 Windows `C:\...` 漏分隔符   |

### 2.2 E2E 新发现的第四处:PutFile 早于建目录

`Deploy` 先逐 transfer 步 PutFile,再跑含 `New-Item`/`mkdir -p` 的脚本。目标目录不存在时:Windows `WriteAllBytes` 抛「路径一部分不存在」(Linux `base64 -d > path` 同理)。单测用假执行器不碰真 FS 没抓到,**.28→.31 E2E 才暴露**(PutFile exit 1)。修法:PutFile 前加 `ensureTransferDirs` 预建目录。

> 设计文档原写「不动 NativeDeployer.Deploy 主流程」,该假设在 E2E 下不成立;此修复属 B2「让链路真正跑通」的必要范围。

## 3. 实现(均在 `platform/backend/internal/appdeploy`)

### 3.1 SSHExecutor OS 感知(`remote_executor.go`)

Windows 侧统一用 `powershell -NoProfile -ExecutionPolicy Bypass -EncodedCommand <base64(UTF-16LE)>` 包裹(跨 cmd/powershell 默认 shell 都稳;绕开引号/换行地狱;`-ExecutionPolicy Bypass` 防 Restricted 拦上传的 .ps1)。命令构造抽成纯函数 + 方法:

- `wrapPowerShellScript(script)` → EncodedCommand 包裹(UTF-16LE + base64)。
- `psWriteFileCommand(remotePath, b64)` → 内层 `[IO.File]::WriteAllBytes('<psQuote(path)>', [Convert]::FromBase64String('<b64>'))`,外层 EncodedCommand;复用既有 `psQuote` 防注入。
- `joinRemotePath(to, base, osType)` → 目标 OS 感知路径拼接(windows `\` / linux `/`,带尾分隔符不双补)。
- `(e *SSHExecutor) runShell(cmd)` / `putShell(remotePath, b64)` → 按 `node.OSType` 分流;linux 分支逐字保留既有 `base64 -d` 串(不改 linux 行为,避免回归)。
- `Run` 改 `sess.Run(e.runShell(cmd))`;`PutFile` 改 `sess.Run(e.putShell(remotePath, b64))`。

### 3.2 路径拼接修正(`deployer_native.go`)

`remote := joinRemotePath(s.Transfer.To, filepath.Base(f), node.OSType)`(原 `To + Base(f)`)。

### 3.3 PutFile 前预建目录(`deployer_native.go`)

新增 `ensureTransferDirs`:收集所有 transfer.To,Windows 发 `New-Item -ItemType Directory -Force`、Linux 发 `mkdir -p`,`Deploy` 入口 PutFile 循环前调用。

## 4. 单测(`remote_executor_powershell_test.go` 新建 + `deployer_native_test.go` 增补)

纯函数 / 假执行器,不连服务器、不触 DB(避 sqlite/PG 类型陷阱):

- `TestJoinRemotePath`:windows/linux/尾分隔符/混用斜杠/空 to 边界。
- `TestWrapPowerShellScript` + `decodeEncodedCommand` helper:多行+中文+引号 round-trip。
- `TestPsWriteFileCommand`:WriteAllBytes 调用、b64 内联、单引号转义。
- `TestSSHExecutorRunShell` / `TestSSHExecutorPutShell`:windows 包裹 / linux 原样逐字。
- `TestNativeDeployer_Deploy_WindowsPathJoin`:远程路径补 `\`。
- `TestNativeDeployer_Deploy_CreatesTransferDirBeforePut`:timeline 断言「先 RUN 建目录再 PUT」。

包内全测 PASS(22.9s)。

## 5. E2E 验证(.28 后端 → .31 Windows 节点)

**拓扑摸清**:.28 后端是 `deploy_backend_1` 容器(`deploy/docker-compose.prod.yml`,镜像 baked、源码未挂载 → 改码须 scp + 重建镜像)。`node_31`(10.10.0.31, windows, ssh, Administrator, 密码已设)已在 DB。入口 nginx:8088。

**链路**:scp 改动文件 → `docker-compose build backend && up -d backend` → 给 `admin` 直接插 `auth_session` 伪造 token(不透明 token,非 JWT,无需密码)→ 建 `b2-winssh-e2e` 应用 → RepoDir 放 deploy.yaml + hello.ps1 → API 触发部署到 node_31 → 直连 .31 验 marker。

**过程中两个 fixture/顺序坑(E2E 才暴露)**:

1. **deploy.yaml 双引号 + Windows 路径 = YAML 非法转义**:原写 `cmd: "& 'C:\anp\...\hello.ps1'"`,YAML 双引号串里 `\h`/`\m` 是非法转义 → `yaml.Unmarshal` 报错 → `loadDeployDesc` 返 `(nil,err)` → native 条件不满足 → **落回 docker 路径**(第一次部署误走 nginx docker 构建)。改 YAML 全用**单引号串**(`\` 字面量,内 `'` → `''`)后正确走 native。
2. **PutFile 早于建目录**(见 §2.2)。

**最终结果**(修 fixture + ensureTransferDirs 后):

| 验证点                      | 结果                                                                                                                   |
| --------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| instance.status             | **running**                                                                                                            |
| last_error                  | 空                                                                                                                     |
| native 分流                 | ✅(非 docker)                                                                                                          |
| .31 `C:\anp\b2-winssh-e2e\` | `hello.ps1`(PutFile 上传)+ `marker.txt`(`b2-winssh-e2e deployed at 2026-07-31T21:42:24+08:00`,Run 执行 hello.ps1 所写) |
| healthcheck                 | `Test-Path marker.txt` → exit 0(部署成功判据)                                                                          |

.PutFile(EncodedCommand 上传)→ Run(EncodedCommand 执行)→ healthcheck 三环全过。验证后已清一次性产物(应用 DB 行 / docker 容器 / 伪造 token / .31 目录 / .28 repo 目录)。

## 6. 验证产物 deploy.yaml(YAML 正确写法)

Windows 路径在 YAML 里必须避免双引号串的 `\` 转义——用单引号串,内部 `'` 用 `''` 转义:

```yaml
target:
  os: windows
  dir: 'C:\anp\<app>\'
steps:
  - transfer: { from: ./hello.ps1, to: 'C:\anp\<app>\' }
  - run: { cmd: '& ''C:\anp\<app>\hello.ps1''' }
  - healthcheck:
      cmd: 'if(-not (Test-Path ''C:\anp\<app>\marker.txt'')){exit 1}'
      timeout: 30s
```

`hello.ps1` 写 marker 后**正常返回,不用 `exit`**(避免退出宿主进程跳过 healthcheck)。

## 7. 提交(均在 main)

- `ff6350d` joinRemotePath 目标OS感知路径拼接
- `b0cab1d` wrapPowerShellScript EncodedCommand 包裹(UTF-16LE)
- `91dc095` psWriteFileCommand Windows PutFile 命令
- `4dbc1cb` SSHExecutor Run/PutFile OS感知(Windows 走 EncodedCommand)
- `039069e` NativeDeployer 远程路径按目标OS拼接
- (E2E 发现)`fix: NativeDeployer PutFile前预创建transfer目标目录`
- 本实现记录

## 8. 范围 / 遗留

- **B2 闭环**:SSHExecutor OS 感知 + 路径拼接 + PutFile 前建目录 + .31 端到端全过。
- **留 B3**:Windows 应用正式打包方式(二进制交叉编译 / 框架依赖 / 常驻服务化)。B2 的「自退出 PS 脚本 + marker」是验证链路的最小形态,不代表生产打包。
- **常驻服务**受 `sess.Run` 阻塞约束未解(B3 命题,届时考虑后台启动 / Windows 服务注册)。
- `.28` 后端已重建带新代码;生产 PG/其他环境未涉及。
