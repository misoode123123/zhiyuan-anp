// Package standard 是「编码规范」限界上下文 —— 注入式生成指导（全局+项目级）。
// 与 rule（硬约束/正则 block）互补：规范告诉 AI「该怎么写」，规则约束「不能怎么写」。
package standard

import (
	"fmt"
	"strings"
	"time"
)

// Standard 编码规范条文。
type Standard struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID *string   `json:"project_space_id" db:"project_space_id"` // NULL=全局；非空=该项目空间
	Name           string    `json:"name" db:"name"`
	Category       string    `json:"category" db:"category"` // general/language/framework/security/testing
	Content        string    `json:"content" db:"content"`
	Priority       int       `json:"priority" db:"priority"`
	Enabled        bool      `json:"enabled" db:"enabled"`
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
