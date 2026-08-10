// Package standard 是「编码规范」限界上下文 —— 注入式生成指导（全局+项目级）。
// 与 rule（硬约束/正则 block）互补：规范告诉 AI「该怎么写」，规则约束「不能怎么写」。
//
// 分层（scope，上层优先级高，AI 编码注入时合并）：
//   - platform：平台级，全平台生效
//   - app：应用级，所有应用通用
//   - module：模块级，module 子字段指明 api/form/db/code/ui
package standard

import (
	"fmt"
	"strings"
	"time"
)

// scope 取值常量。
const (
	ScopePlatform = "platform" // L1 平台级
	ScopeApp      = "app"      // L2 应用级
	ScopeModule   = "module"   // L3 模块级（看 Module 字段）
)

// module 子字段取值常量（scope=module 时）。
const (
	ModuleAPI  = "api"
	ModuleForm = "form"
	ModuleDB   = "db"
	ModuleCode = "code"
	ModuleUI   = "ui"
)

// ANP 部署端口段（宿主端口分配区间）—— 单一源。
// BuildAgentsMarkdown 固定段据此渲染；appdeploy/deployer.go 的端口常量别名引用同一组值，
// 改一处 → AGENTS.md 文本与引擎端口分配同步（防规则源与实现脱节）。
// 依赖方向 appdeploy→standard 单向，常量源放本包零循环风险。
const (
	PortTestMin = 9100 // test 环境宿主端口下限
	PortTestMax = 9199 // test 环境宿主端口上限
	PortProdMin = 9200 // prod 环境宿主端口下限
	PortProdMax = 9300 // prod 环境宿主端口上限
)

// Standard 编码规范条文。
type Standard struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID *string   `json:"project_space_id" db:"project_space_id"` // NULL=全局；非空=该项目空间（旧字段，保留兼容）
	Name           string    `json:"name" db:"name"`
	Category       string    `json:"category" db:"category"` // general/language/framework/security/testing（补充分类）
	Content        string    `json:"content" db:"content"`
	Priority       int       `json:"priority" db:"priority"`
	Enabled        bool      `json:"enabled" db:"enabled"`
	Scope          string    `json:"scope" db:"scope"`   // platform / app / module
	Module         string    `json:"module" db:"module"` // scope=module 时：api/form/db/code/ui；其余空
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// BuildPromptSection 把生效规范拼成注入 prompt 的段落。
// 每行前缀 [全局]/[项目] + [category]，便于 AI 区分来源与类型。空列表返回空串。
func BuildPromptSection(list []Standard) string {
	if len(list) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n【编码规范·必须遵循】")
	for _, s := range list {
		scope := "全局"
		if s.ProjectSpaceID != nil {
			scope = "项目"
		}
		fmt.Fprintf(&b, "\n[%s][%s] %s", scope, s.Category, s.Content)
	}
	return b.String()
}

// BuildAgentsMarkdown 把聚合规范渲染成 AGENTS.md 文本（按层级分节）。
// 顺序：平台级 → 应用级 → 指定模块级（调用方传入已按 scope 优先级排好的列表）。
func BuildAgentsMarkdown(list []Standard, module string) string {
	var b strings.Builder
	b.WriteString("# AGENTS.md\n\n")
	b.WriteString("> 本文件由 ANP 平台 /governance 自动导出（开发规范分层聚合）。")
	b.WriteString("层级：平台级 > 应用级 > 模块级；AI 编码时合并遵循，上层优先。\n\n")

	section := func(title string, items []Standard) {
		if len(items) == 0 {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n", title)
		for _, s := range items {
			fmt.Fprintf(&b, "### %s\n\n", s.Name)
			if s.Content != "" {
				b.WriteString(s.Content)
				b.WriteString("\n\n")
			}
		}
	}

	platformItems, appItems, moduleItems := groupByScope(list)
	section("平台规范（Platform）", platformItems)
	section("应用规范（App）", appItems)
	modTitle := "模块规范（Module"
	if module != "" {
		modTitle += ": " + module
	}
	modTitle += "）"
	section(modTitle, moduleItems)

	// ANP 部署适配规范（固定段，导入/部署适配用；opencode 据此把应用适配成可部署）。
	// 与引擎实现保持一致：端口段取自本包 PortTestMin/Max·PortProdMin/Max 常量；注入 env/消费现状
	// 据引擎实际（deployer.go ensurePortEnv、handler.go CONFIG_PATH 注入、deploy_manifest.go 消费子集）。
	b.WriteString("## ANP 部署适配规范（导入/部署适配用）\n\n")
	b.WriteString("导入或部署应用到 ANP 时按本规范适配代码（ANP 注入连接信息、容器化运行）：\n\n")
	b.WriteString("- **配置优先读环境变量（env-over-config）**：应用配置须优先读环境变量。**禁止硬编码 `127.0.0.1`/`localhost` 访问中间件**（容器内不可达）。\n")
	b.WriteString("- **运行时自动注入的 env（应用须读取，勿硬编码）**：\n")
	b.WriteString("  - `PORT`：容器监听端口（= 宿主映射的容器端口）。PORT-driven 应用（node `process.env.PORT` / python `int(os.getenv(\"PORT\"))`）据此监听；应用显式设了 `PORT` 则尊重不覆盖。\n")
	b.WriteString("  - `CONFIG_PATH=/app/config.yaml`：配置文件在容器内的挂载路径，应用据此加载配置（无 config 则忽略）。\n")
	b.WriteString("  - 中间件连接：`DATABASE_URL`(PG)、可注入 `REDIS_ADDR`/`MILVUS_ADDR`/`PG_*` 等，由 ANP 供给注入。\n")
	fmt.Fprintf(&b, "- **端口段**：宿主端口分配 test %d-%d / prod %d-%d（ANP 自动按环境从区间内分配首个空闲端口；容器内监听端口由 `PORT` 指定，`-p` 映射宿主:容器）。\n", PortTestMin, PortTestMax, PortProdMin, PortProdMax)
	b.WriteString("- **构建**：仓库根须有 Dockerfile（推荐多阶段）；`EXPOSE` 应用监听端口；构建上下文 = 仓库根。\n")
	b.WriteString("- **依赖**：中间件由 ANP 供给或绑定已有，连接信息经环境变量注入；应用读 env，不写死地址。\n")
	b.WriteString("- **依赖声明（回写 `.anp/deps.yaml`）**：若应用用到 redis/milvus 等中间件，在仓库根写 `.anp/deps.yaml` 声明依赖，ANP 据此注入连接 env（`REDIS_ADDR`/`MILVUS_ADDR`）。格式：`services: [{kind: redis}, {kind: milvus}]`（kind 必填；strategy 可选，不写走默认 `bind_existing`）。无中间件依赖则不写此文件。\n")
	b.WriteString("- **部署需求回写（`.anp/deploy.yaml`）**：声明应用的部署需求，ANP 据此确定性重放部署（每次升级保持原先部署方式，不因引擎变更而漂移）。文件分两段：`needs`（**你维护**：`mounts`/`env_keys`/`ports`/`command`）+ `actual`（**引擎成功后自动回填，你只读别改**：`image_digest`/`mounts_src`/`host_port`/`engine_version`）。`mounts` 用于密钥/配置文件挂载（`src` 仓库相对路径 → `dst` 容器内路径，不进镜像层）。**仓库根有 `config.yaml` 的应用务必声明** `mounts: [{src: config.yaml, dst: /app/config.yaml, readonly: true}]`，否则挂载缺失/错位。无特殊部署需求（普通 web 无 config）可不写此文件，引擎自动探测。\n")
	b.WriteString("  - **引擎消费现状（务必知晓，避免误判）**：当前引擎**消费 `needs` 四字段**：\n")
	b.WriteString("    - `mounts`（含 `dst=/app/config.yaml` 配置挂载）→ 逐条 `-v` 挂载；\n")
	b.WriteString("    - `ports[0]` → 容器监听端口（优先于 InternalPort，决定 `-p` 容器侧 + `PORT` 注入）；\n")
	b.WriteString("    - `command` → 覆盖镜像 CMD（按空白拆词，支持引号包裹含空格参数）；\n")
	b.WriteString("    - `env_keys` → 校验有值来源（声明但缺值仅 WARN，不阻断部署）。\n")
	b.WriteString("    **注**：`actual.mounts_src` 确定性重放当前仅记 config 挂载；extra 挂载每次重算（结果正确，确定性重放为 follow-up）。\n")
	b.WriteString("- **网络**：默认 bridge（隔离）；需 host 网络须审批，优先改配置走 env。\n")
	b.WriteString("- **形态**：web（HTTP，有端口+URL）/ headless（bot/worker 等长驻外发，无 URL，健康=进程或外连）。\n")
	b.WriteString("- **缺失服务**：若部署机缺某依赖，在变更里报明（kind/原因），由 ANP 经审批后受控安装（白名单内），连接回填注入 env。\n\n")
	return b.String()
}

// groupByScope 把扁平列表按 scope 拆三段（保持传入顺序）。
func groupByScope(list []Standard) (platform, app, module []Standard) {
	for _, s := range list {
		switch s.Scope {
		case ScopeApp:
			app = append(app, s)
		case ScopeModule:
			module = append(module, s)
		default:
			platform = append(platform, s)
		}
	}
	return
}
