# opencode 输出 ANSI 转义码污染 code_task / change_request.output

- 日期：2026-08-01
- 模块：`internal/dev`（CodingAgent 编码任务）
- 严重度：轻微（数据可读性，非功能阻断）
- 状态：已修（待 .28 重建验证）

## 现象

导入应用触发 opencode 适配（`adapt` 任务）后，`code_task.output` 与自动登记的
`change_request.output` 里混入 opencode TUI 的 ANSI 转义码，例如：

```
\x1b[0m
\x1b[0m# \x1b[0mTodos
[✓] Explore repository structure ...
```

审批 UI 直接渲染会把 `\x1b[0m` 当文本显示，产出不可读。不仅影响 `adapt` 任务，
所有走 `CodingAgent`（`code` / `dispatch` / `adapt`）的异步编码任务都有此问题——
`adapt` 只是第一次被肉眼检查到。

## 根因

数据流：

```
opencode stdout（含 SGR \x1b[31m / 重置 \x1b[0m / 光标 \x1b[H / OSC 超链接等 TUI 转义）
  → CodingAgent.opencodeRun (coding.go) 捕获进 out buffer，return out.String() 原样不剥   ← 根因
    → run() 三处存库全是原样：
       ├ MarkFailed(out+err)            → code_task.output
       ├ MarkCompleted(out)             → code_task.output
       └ change_request.Output = out    → change_request.output
```

`opencodeRun` 是 `CodingAgent.run` 的唯一调用点（全后端唯一），是单一咽喉——
opencode 在 `run --auto` 非交互模式下仍向 stdout 写 TUI 转义码，而该函数把
stdout+stderr 原样返回并存库。

## 修法

在 `opencodeRun` 返回前剥离 ANSI/VT100 转义序列，两个返回分支（成功/失败）共用：

- 新增 `internal/dev/ansi.go`：`stripANSI(s)` 用正则剥 CSI（`ESC [ params final`，
  覆盖 SGR/光标/清屏）+ OSC（`ESC ] ... ST(ESC\) | BEL`，覆盖标题/超链接），末尾
  兜底移除残留裸 ESC（截断/不完整序列）。
- `coding.go` opencodeRun：`result := stripANSI(out.String())` 后再返回。

一处净化即覆盖全部 3 个存储点（MarkCompleted/MarkFailed/change_request.Output）。

### 关键避坑

OSC 正则初版用 `[^\x1b]*`（只在 ESC 停）→ 遇 **BEL(`\x07`)终止**的 OSC（如
`\x1b]0;title\x07body`）时贪婪吞掉 BEL 之后的正文 `body`，整段被删。修正为
`[^\x07\x1b]*`（在 BEL 或 ESC 停）+ 终结符匹配 `\x07 | ESC\`。TDD 测试用例
「OSC标题(BEL终止)」抓住了这个回归。

## 验证

- 单测 `internal/dev/ansi_test.go`：10 用例（SGR/重置/光标/OSC-ST/OSC-BEL/混排/裸ESC）全绿。
- `go build ./cmd/server` 通过；`go test ./internal/dev/` 通过。
- .28 端到端：重建 backend 后重新导入小应用触发 adapt，查 `code_task.output` /
  `change_request.output` 无 `\x1b` 残留（见 commit 后部署验证）。

## 影响 / 回归面

- 行为变化：所有异步编码任务的 output 不再含 ANSI 码（纯文本）。无负向影响（转义码
  本就不该入库）。
- 交互编码工作台（codews）走 opencode serve 的 session API，不经 opencodeRun，不受影响。
