# 非 web 应用（桌面/移动/CLI/服务）开发及全流程管理

> 日期:2026-07-29 ｜ 状态:待评审

## 1. 背景与目标

ANP 平台当前只支持 web/容器化 HTTP 应用的开发：`Application` 实体强绑 Docker（`RepoDir`/`InternalPort`/`Image`/`HostPort`/`URL`），构建链是 `docker build → docker run → http://<host>:<port>`，`DeployMode` 只有 `managed`(托管)/`external`(纳管)，发布产物绑定镜像 + HTTP 灰度。总体方案 `企业AI原生研发平台方案.md:301,657` 提到产出应用可为 Web/App/小程序，但未展开非 web 形态的开发流程。

**目标**：让平台支持桌面、移动、CLI、后端服务四种非 web 应用形态的"编码 → 构建产物"链路——AI 能针对各形态生成正确代码并打包出真实产物（exe/dmg/apk/二进制），产物存平台对象存储供下载。

**分期边界**：本期打通"编码 → 构建产物"，发布/分发（签名、上架、灰度、外部 CI）留下期。务实落地，先出可用 MVP。

## 2. 已确认的需求边界

| 维度       | 选择                                                   |
| ---------- | ------------------------------------------------------ |
| 支持形态   | 桌面 / 移动 / CLI / 后端服务（四种全支持）             |
| 全流程深度 | 先打通"编码 → 构建产物"，发布分发暂占位                |
| 构建方式   | 平台预置构建容器（每形态一个镜像）                     |
| 产物存储   | 平台对象存储（MinIO）+ 应用详情页下载                  |
| 编码适配   | 模板驱动脚手架（新增 desktop/mobile/cli/service 模板） |

## 3. 整体架构与形态模型

把"应用形态"做成 `appdeploy` 内的一个正交维度，与现有"接入模式"并列，web 链路零改动。

```
Application 现有两个维度:
  DeployMode: managed(托管) / external(纳管)     ← 已有，不动
  AppKind:    web / desktop / mobile / cli / service  ← 新增

两者正交组合(本期实际用到的):
  managed × web       = 现有链路(docker build→容器→URL)，完全不变
  managed × desktop   = 新链路(脚手架→编码→DesktopBuilder→exe/dmg→MinIO→下载)
  managed × mobile    = 新链路(...→MobileBuilder→apk→MinIO→下载)
  managed × cli       = 新链路(...→CLIBuilder→二进制→MinIO→下载)
  managed × service   = 近 web(可能非 HTTP: gRPC/MQ)，本期按 web 构建器复用
  external × *        = 纳管，不构建(已有，不变)
```

**分层**：

- **模板层**：新增 `desktop`/`mobile`/`cli`/`service` 项目模板，含脚手架种子 + 形态编码规范 + 构建配置。
- **编码层**：opencode 不改，靠模板脚手架 + 注入形态规范让 AI 写出形态正确的代码（沿用现有 `coding_standard` 注入机制）。
- **构建层**：新增 `Builder` 接口 + 按 `AppKind` 分派；web 走原 `WebBuilder`，其余走对应预置容器构建器。
- **产物层**：新增 `artifact` 表 + MinIO 存储 + 应用详情页下载。
- **发布层**：本期占位（产物可下载即算"发布到可获取"），完整分发下期。

**关键约束**：

- `AppKind` 默认 `web`，存量应用迁移加默认值，老数据零影响。
- web 链路（`WebBuilder`）把现有 `docker build→run` 逻辑原样搬进 `WebBuilder.Build()`，行为不变，只多接口壳。
- 本期不做：iOS 签名/上架、桌面自动更新、移动灰度、外部 CI 对接——下期。

## 4. 数据模型

### 4.1 Application 加 AppKind 字段

`appdeploy_application` 表加一列（迁移 `000022`，幂等）：

```sql
ALTER TABLE appdeploy_application
    ADD COLUMN IF NOT EXISTS app_kind TEXT NOT NULL DEFAULT 'web';
-- 值: web / desktop / mobile / cli / service
```

`Application` struct 加 `AppKind string`。常量：

```go
const (
    AppKindWeb     = "web"      // 现有链路
    AppKindDesktop = "desktop"  // 桌面安装包
    AppKindMobile  = "mobile"   // 移动 apk/ipa
    AppKindCLI     = "cli"      // 命令行二进制
    AppKindService = "service"  // 后端服务(可能非HTTP)
)
```

校验：建应用时 `app_kind` 必须 `oneof=web desktop mobile cli service`。存量数据默认 `web`，零影响。

### 4.2 新增 artifact 表（产物模型）

一个应用一次构建可产出多个产物（桌面同时出 Windows exe + macOS dmg + Linux AppImage；移动出多 ABI apk）。一对多：

```sql
CREATE TABLE IF NOT EXISTS appdeploy_artifact (
    id            TEXT PRIMARY KEY,          -- art_xxx
    application_id TEXT NOT NULL,            -- 关联应用
    build_version INT NOT NULL,              -- 对应 Application.Version
    app_kind      TEXT NOT NULL,             -- 冗余,便于按形态查询
    platform      TEXT NOT NULL,             -- windows / macos / linux / android / ios / multi
    arch          TEXT NOT NULL,             -- x64 / arm64 / x86 / universal / multi
    filename      TEXT NOT NULL,             -- myapp-1.0.0-win-x64.exe
    size_bytes    BIGINT NOT NULL,
    sha256        TEXT NOT NULL,             -- 完整性校验
    storage_key   TEXT NOT NULL,             -- MinIO 对象 key(如 artifacts/<app>/<ver>/<filename>)
    content_type  TEXT,                       -- application/octet-stream 等
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    FOREIGN KEY (application_id) REFERENCES appdeploy_application(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_artifact_app_ver ON appdeploy_artifact(application_id, build_version);
```

### 4.3 构建配置（轻量，本期不建完整模板系统）

代码层项目模板系统（`ProjectTemplate` 表 + 规则包/闸门/配额联动）尚不存在，本期不建完整模板系统（那是独立子系统，留下期）。改用轻量方案：

- 建应用时直接指定 `app_kind`（web/desktop/mobile/cli/service）。
- 各形态的构建配置（构建镜像、构建命令、产物目录、脚手架标识）存一张轻量表 `appdeploy_build_config`，按 `app_kind` 唯一，seed 4 条非 web 默认配置（web 不需要，走自带 Dockerfile）。
- 脚手架种子按 `app_kind` 从静态目录 `deploy/scaffolds/<scaffold>/` 选择（见 §6.1）。
- 形态编码规范 seed 到现有 `coding_standard` 表（见 §6.2）。

```sql
CREATE TABLE IF NOT EXISTS appdeploy_build_config (
    app_kind      TEXT PRIMARY KEY,            -- desktop/mobile/cli/service
    build_image   TEXT NOT NULL,               -- anp/builder-electron:latest
    build_command TEXT NOT NULL,               -- cd /src && npm ci && npx electron-builder --win --mac --linux
    artifact_dir  TEXT NOT NULL,               -- /src/dist
    scaffold      TEXT NOT NULL,               -- electron-react-ts
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

**关键点**：

- `artifact` 与 `application` 一对多，一次构建多产物（多平台/多架构）都能存。
- `storage_key` 是 MinIO 对象 key，下载时平台按 key 拉流，不暴露物理路径。
- 构建配置是数据，不是代码——加新形态只加 `appdeploy_build_config` 记录，不改后端逻辑（Builder 按 `app_kind` 分派读对应配置）。
- 不引入新租户/权限维度，沿用现有 `project_space_id` 隔离。

## 5. 构建器抽象与构建流程

### 5.1 Builder 接口

```go
// Builder 把应用源码构建为产物。按 AppKind 分派，web 走原有逻辑，其余走预置容器。
type Builder interface {
    // Build 在构建环境里跑构建,返回产出的产物描述列表(可能多个平台/架构)。
    Build(ctx context.Context, app *Application) ([]ArtifactOutput, error)
}

type ArtifactOutput struct {
    Platform    string // windows/macos/linux/android/ios/multi
    Arch        string // x64/arm64/x86/universal/multi
    Filename    string
    ContentType string
    // 构建容器内产物文件的绝对路径,由 Builder 收集后交给产物层上传 MinIO
    SrcPath     string
}
```

### 5.2 五个 Builder 实现

| Builder          | AppKind | 构建镜像                                      | 构建做什么                                                                                     |
| ---------------- | ------- | --------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `WebBuilder`     | web     | 现有(应用自带 Dockerfile)                     | 原样搬现有 `docker build→run`，行为不变；不产出 artifact，部署即"产物"                         |
| `ServiceBuilder` | service | 复用 web(自带 Dockerfile)                     | 本期等同 web（可能非 HTTP 但构建链相同），后续按需分异                                         |
| `DesktopBuilder` | desktop | `anp/builder-electron` 或 `anp/builder-tauri` | `docker run` 挂载源码 → 跑 `electron-builder`/`tauri build` → 扫 `dist/` 收集 exe/dmg/AppImage |
| `MobileBuilder`  | mobile  | `anp/builder-android`                         | `docker run` 挂载源码 → `gradle assembleRelease` → 收集 apk(多 ABI)                            |
| `CLIBuilder`     | cli     | `anp/builder-go-cross` 或 `anp/builder-rust`  | `docker run` → 交叉编译多 OS/Arch → 收集二进制                                                 |

**分派**：`Build(app)` 按 `app.AppKind` 选 Builder，从该应用所用模板的 `preset_pipeline_def.build` 读构建镜像/命令/产物目录。Builder 本身不硬编码镜像，配置在模板数据里。

### 5.3 构建流程（非 web 形态）

```
触发构建(app 详情页"构建"按钮, 或编码后手动)
  │
  ▼
1. 解析 app.AppKind → 选 Builder
2. 从 app 所用模板读 build.image / build.command / build.artifact_dir
3. docker run --rm \
       -v <app.RepoDir>:/src \
       <build.image> \
       sh -c "<build.command>"
   (挂载源码到 /src,构建产物落到 artifact_dir)
  │
  ▼
4. Builder 扫描 artifact_dir,按文件名规则识别平台/架构
   (如 *-win-x64.exe → windows/x64; *.dmg → macos/universal; *.apk → android/multi)
   生成 []ArtifactOutput
  │
  ▼
5. 逐个产物:算 sha256 + size → 上传 MinIO(storage_key=artifacts/<app>/<ver>/<filename>)
   → 写 appdeploy_artifact 记录(build_version = app.Version)
  │
  ▼
6. 更新 app.Status = built(新状态) + Version 自增, 清理构建容器
   (web 仍走原 building→running;非 web 用 built 表示"产物已就绪可下载")
```

### 5.4 与现有构建链的关系

- 现有 `dockerBuild`/`dockerRun` 逻辑（管镜像构建+容器起停+端口分配）整体收进 `WebBuilder.Build()`，对外通过 `Builder` 接口调用。调用方只认接口，web 行为不变。
- `Application.Status` 现有值 `registered/building/running/stopped/failed`，新增 `built`（非 web 产物就绪）。web 链路不出现 `built`。
- 构建超时/recover/markFailed（"构建卡 building"修复）复用现有机制，Builder 一样受保护。
- 错误处理：构建容器非 0 退出 → 捕获容器日志写 `app.LastError` + `Status=failed`，产物不落库（与现有 web 构建失败一致）。

### 5.5 关键约束

- 预置构建镜像由平台维护（Dockerfile 放 `deploy/builders/`，随部署构建）。本期先各出一个最小可用镜像，不追求全平台覆盖。
- 产物扫描靠文件名规则匹配，规则收敛到 `lib` 里一个纯函数（可单测，呼应 workspace 那套 node 环境测试做法）。
- 同一构建版本可重跑，重跑覆盖同 `storage_key`（幂等），`artifact` 记录按 `(application_id, build_version, platform, arch)` 唯一，重跑 upsert。

## 6. 编码适配与模板脚手架

### 6.1 模板脚手架种子（scaffold）

每个非 web 模板内置一份最小可构建的脚手架，建项目时克隆到 `RepoDir` 作为初始代码（沿用现有 `EnsureRepo` 建空仓的机制，区别是带种子）：

| 模板      | 脚手架                       | 种子内容（最小可构建）                                                                          |
| --------- | ---------------------------- | ----------------------------------------------------------------------------------------------- |
| `desktop` | `electron-react-ts`          | Electron + React + TS 主进程/渲染进程骨架 + `package.json`(含 electron-builder 配置) + 构建脚本 |
| `mobile`  | `android-flutter` 或 `rn-ts` | Flutter 工程（或 RN）+ Android `build.gradle` + 一个空 Activity/Screen + 签名占位               |
| `cli`     | `go-cli` 或 `rust-cli`       | main + 命令框架(cobra/clap) + 跨平台构建脚本 + README                                           |
| `service` | 复用 web 脚手架              | 现有 web 种子（本期 service 等同 web）                                                          |

脚手架存仓库 `deploy/scaffolds/<name>/`，建项目时 `cp -r` 到 `RepoDir` 再 git init（与导入已有项目的目录复制同机制，复用 `ImportFromDir` 的安全加固：防 `../` 逃逸、跳 symlink）。

### 6.2 形态编码规范注入

复用现有 `coding_standard` 注入机制（`dev.CodingAgent` 的 `BuildPromptSection` 把全局+项目级规范注入 opencode prompt）。每个模板 seed 预置该形态的编码规范到 `coding_standard` 表：

- `desktop`：Electron 主进程/渲染进程分离、IPC 边界、禁用 Node 集成风险项、打包配置规范
- `mobile`：Android 权限最小化、生命周期、避免主线程阻塞、apk 构建变体规范
- `cli`：跨平台路径处理、退出码语义、参数解析规范、无外部运行时依赖
- `service`：复用 web 规范

AI 编码时 prompt 里自带形态约束，不用改 opencode 引擎本身。

### 6.3 建项目流程（轻量，本期）

本期无完整模板系统，建非 web 应用的流程简化为：

```
建应用(指定 app_kind=desktop/mobile/cli/service, 复用现有 handler.Create)
  │
  ▼
EnsureRepo 建 git 仓后,按 app_kind 从 appdeploy_build_config 读 scaffold
  → 克隆 deploy/scaffolds/<scaffold>/ 到 RepoDir 作为初始代码 → git init + 首次提交
  │
  ▼
按 app_kind seed 形态编码规范到 coding_standard(若尚未存在)
  │
  ▼
空间就绪,AI 在脚手架基础上编码(opencode 注入形态规范)
```

完整模板机制（规则包/闸门/配额/Agent 配置联动）留下期。

### 6.4 编码工作台的形态感知

- 研发工作台左面板（已 IDE 化）不变，git 变更/diff/提交逻辑与形态无关，天然通用。
- 顶部工具栏"构建部署"按钮：web 形态是"构建并部署到 test"（现有）；非 web 形态改成"构建产物"——点了跑 Builder 出产物，产物列表出现在应用详情页，不部署容器。
- 编码引擎（opencode）零改动：只管在 `RepoDir` 写代码，形态正确性由脚手架 + 规范注入保证。

### 6.5 关键约束

- 脚手架种子必须"最小可构建"——能直接被对应 Builder 打出产物。每个脚手架入库前要在对应构建镜像里验证一次。
- `service` 本期完全等同 web（脚手架/规范/Builder 都复用），只为后续非 HTTP 服务预留 `AppKind` 值，不额外开发。
- 不为本期做形态专用 Agent，靠脚手架+规范注入达成形态正确性。

## 7. 产物存储与下载

### 7.1 MinIO 存储模型

构建产物实体文件存 MinIO，bucket `anp-artifacts`，对象 key 规范：

```
artifacts/<application_id>/<build_version>/<filename>
例: artifacts/app_67e8.../3/myapp-1.0.0-win-x64.exe
```

- 同一构建版本重跑覆盖同 key（幂等）。
- 应用删除级联清理：`application` 删 → `artifact` 记录删（FK CASCADE）→ 异步清理对应 MinIO 对象。
- MinIO 连接复用研发工作台设计里已有的 MinIO 配置，不引入新中间件。若现网 MinIO 未部署，本期降级到本地文件存储（`data/artifacts/`），`storage_key` 仍统一抽象，存储后端可切换。

### 7.2 产物上传流程（构建第 5 步细化）

```
Builder 产出 ArtifactOutput{SrcPath, Platform, Arch, Filename, ContentType}
  │
  ▼
1. 读 SrcPath 文件 → 算 sha256 + size_bytes
2. 生成 storage_key = artifacts/<app_id>/<version>/<filename>
3. 上传 MinIO(或降级写本地),设 Content-Type + Content-Disposition
4. 写 appdeploy_artifact 记录(所有字段)
5. 删除构建容器内 SrcPath 临时文件
```

上传失败：单产物失败不阻塞其他产物（多平台构建部分成功也保留成功的），失败产物记入 `app.LastError` 但 `Status` 仍算 `built`（产物已部分就绪）。

### 7.3 下载接口与鉴权

新增两个端点（复用现有 `AuthUser` + `Require` 权限框架）：

```
GET /api/v1/project-spaces/:id/apps/:aid/artifacts
  → 列出该应用全部产物(按 build_version 倒序),返回 [{id,platform,arch,filename,size,sha256,build_version,created_at}]
  鉴权: 需该空间读权限

GET /api/v1/project-spaces/:id/apps/:aid/artifacts/:artid/download
  → 302 重定向到 MinIO 预签名 URL(有效期 15min),或直接流式返回文件
  鉴权: 需该空间读权限; 预签名 URL 不带鉴权(时效短)
```

- 路径风格与现有 appdeploy 接口一致（`/project-spaces/:id/apps/:aid/...`）。
- 下载走预签名 URL：平台不中转大文件流量，MinIO 直出；客户端凭短时效 URL 拉取。
- 防 traversal：`artid` 必须命中库里记录，`storage_key` 由后端按规范拼，不接受前端传入路径。

### 7.4 前端：应用详情页产物区

应用详情页加"构建产物"区（仅 `AppKind != web` 显示，web 仍显示部署实例/URL）：

```
┌─ 构建产物 ────────────────────────────┐
│ 构建版本 3 · 2026-07-29 14:20        │
│  📦 myapp-1.0.0-win-x64.exe  42MB    │
│     Windows · x64 · sha256: a3f…     │
│     [⬇ 下载]                         │
│  📦 myapp-1.0.0-mac-universal.dmg 51MB│
│     macOS · universal · [⬇ 下载]      │
│  📦 myapp-1.0.0-linux-x64.AppImage 39MB│
│     Linux · x64 · [⬇ 下载]            │
│ [🔄 重新构建]                         │
└──────────────────────────────────────┘
```

- 列表调 `/artifacts`，下载按钮调 `/artifacts/:artid/download`（浏览器直接触发下载，跟随 302）。
- 显示 sha256 前缀供用户校验完整性。
- "重新构建"触发构建流程，新版本号 + 新产物覆盖展示。

### 7.5 关键约束

- web 应用完全不走产物区（`AppKind=web` 时 `/artifacts` 返回空，前端不渲染产物区），web 体验零变化。
- 产物不进版本历史/回滚机制（下期发布分发再做），本期"下载最新产物"即满足"先打通编码→产物"。
- 不做产物签名/校验签名（下期），但存 sha256 供人工核对。

## 8. 与现有九大板块的关系

| 板块             | 本期改动                                                                                |
| ---------------- | --------------------------------------------------------------------------------------- |
| 1 需求工作台     | 无改动（需求与形态无关）                                                                |
| 2 研发工作台     | 工作台左面板通用不变；顶部"构建部署"按钮按 `AppKind` 分异（web 部署 / 非 web 构建产物） |
| 3 测试与质量中心 | 无改动（本期不涉及形态特定的验收）                                                      |
| 4 规则治理中心   | 无改动（`coding_standard` 复用，只加形态规范 seed）                                     |
| 5 安全与合规中心 | 无改动                                                                                  |
| 6 发布中心       | 本期占位（产物可下载即"可获取"）；完整分发下期                                          |
| 7 运维中心       | 非 web 无运行实例，不纳入探活；web/external 不变                                        |
| 8 算力与资源中心 | 无改动                                                                                  |
| 9 AI 能力市场    | 无改动                                                                                  |
| 横切·项目空间    | 新增 4 个内置模板（含脚手架种子 + 形态规范 + 构建配置）                                 |
| 横切·appdeploy   | 加 `AppKind` + `Builder` 接口 + `artifact` 表 + 下载接口（核心改动）                    |

## 9. 本期不做（下期）

- iOS 签名/上架、桌面自动更新、移动灰度分发
- 外部 CI（GitHub Actions / GitLab CI / Jenkins）对接
- 产物签名与签名校验
- 产物版本历史与回滚
- `service` 形态与 web 的差异化（非 HTTP 协议适配）
- 形态专用编码 Agent

## 10. 验收标准

1. 建 `desktop`/`mobile`/`cli`/`service` 项目（选对应模板）→ 自动克隆脚手架种子到 RepoDir。
2. AI 编码产出形态正确的代码（脚手架 + 规范注入保证）。
3. 点"构建产物"→ 平台在预置构建容器里打包 → 产出真实产物（desktop 出 exe/dmg/AppImage，mobile 出 apk，cli 出多平台二进制）。
4. 产物存 MinIO（或本地降级），应用详情页"构建产物"区列出各平台产物 + 大小 + sha256。
5. 点"下载"→ 浏览器下载到真实可运行的产物文件。
6. `web` 应用全链路行为不变（回归现有部署流程）。
7. 存量应用 `app_kind` 默认 `web`，老数据无影响。
