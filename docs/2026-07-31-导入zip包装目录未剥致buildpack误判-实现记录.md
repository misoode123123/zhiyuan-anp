# appdeploy bug 修复：导入 zip 未剥"包装目录"→ 源码嵌套 → buildpack 误判 static

| 日期   | 2026-07-31                                                                                          |
| ------ | --------------------------------------------------------------------------------------------------- |
| 类型   | bug 修复（导入/构建链路）                                                                           |
| 现象   | 导入的「客服机器人」应用在 ANP 构建出 nginx 空壳容器，而非真实 Go 后端                              |
| 影响面 | 任何"zip 顶层带单一包装目录"的导入项目，源码会被嵌进子目录，buildpack 检测不到语言标记而误判 static |

## 1. 现象与根因

客服机器人点"部署"后，ANP 构建出的镜像 `appdeploy/app-3b18080424ff-test:vN` 是 nginx 静态站（仓库根的 Dockerfile 是 `FROM nginx:alpine` + `COPY . /usr/share/nginx/html`），不是 Go 后端。

**根因不是 buildpack 检测逻辑错**——`buildpack.go:61 detectType` 本来就认 `go.mod`→go。问题是导入的 zip 顶层带了一个包装目录 `yxt_eino_v2 - 客服机器人/`，`ImportFromZip` 解压后没剥掉，导致：

```
/data/repos/-----/                         ← 仓库根（buildpack 只看这里）
├── Dockerfile                              ← buildpack 兜底生成的 nginx（根没 go.mod → static）
└── yxt_eino_v2 - 客服机器人/              ← 包装目录（没剥）
    ├── go.mod  cmd/  internal/  config.yaml  Dockerfile(真正的 Go 多阶段)
    └── ...
```

仓库根没有 `go.mod` → `detectType` 兜底 `static` → 生成 nginx Dockerfile → 构建出空壳。真正的 Go 源码（含正确的多阶段 Dockerfile）全嵌在子目录里，构建上下文（仓库根）用不到。

> 对照：`ImportFromDir`（`cp -r src target`，target 新建）和 `ImportFromGit`（`git clone`）都把内容直接放仓库根，不嵌套；**只有 `ImportFromZip` 会保留 zip 顶层包装目录**。

## 2. 方案

在 `ImportFromZip` 解压后、`git init` 前，剥单一包装目录：若仓库根下**恰好只有一个顶层子目录**（zip 常见的 `项目名/` 包装），把它的内容上提到仓库根并移除空壳。多个顶层条目或顶层有散落文件则不动（避免破坏合法结构）。`.git` 不计入条目数（zip 可能把 `.git` 放在包装目录内，展平后正好落到根被识别）。

剥掉后仓库根直接是 `go.mod`/`cmd/`/...→ `detectType` 命中 go（或自带 Dockerfile 被原样使用）→ 正确构建。

## 3. 改动

- `platform/backend/internal/appdeploy/repo.go`
  - 新增 `flattenSingleWrapper(target)`：读顶层条目（排除 `.git`），恰一个目录则 `os.Rename` 其内容上提、移除空壳；否则 no-op。
  - `ImportFromZip` 解压循环后、`.git` 检测前调用，失败则清 target 报错。
- `platform/backend/internal/appdeploy/repo_flatten_test.go`（新）
  - `TestFlattenSingleWrapper`：单一包装目录(含中文+空格名、.git、子目录)上提到根 / 多顶层条目不动 / 扁平仓不动 / 仅 `.git` 不当作包装。

## 4. 验证

| 项                                                               | 结果          |
| ---------------------------------------------------------------- | ------------- |
| `go test ./internal/appdeploy/ -run TestFlattenSingleWrapper -v` | 4 子用例 PASS |
| `go build ./...`（platform/backend）                             | exit 0        |

（纯函数 + 表驱动单测覆盖；.28 端到端可在后续重新导入一个带包装目录的 zip 验证展平。）

## 5. 经验

- 导入要保证"仓库根 = 项目根"。zip 常带顶层包装目录，必须剥掉，否则后续所有"按仓库根探测"的逻辑（buildpack 语言检测、Dockerfile 查找、构建上下文）全失准。
- "buildpack 误判"类问题先看**仓库根实际有什么**，别急着改检测逻辑——检测往往没错，是输入（嵌套）错了。本次 `detectType` 对根级 `go.mod` 本就正确，无需改。
- 关联：本次诊断中还发现客服机器人是**企微长连接外发机器人**（不监听 HTTP 端口），其依赖 Redis/Milvus/PG 已在 .28 以 `0.0.0.0:端口` 运行；它本就用自己的 docker-compose（`yxt-bot`，host 网络）正常跑着，ANP 那条单容器 bridge 部署是重复且不搭的副本（已停）。
