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
