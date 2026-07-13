package rule

import (
	"context"
	"regexp"
	"strings"
)

// Violation 规则违反。
type Violation struct {
	Rule *Rule `json:"rule"`
}

// HasBlock 是否存在阻断级违反。
func HasBlock(vs []Violation) bool {
	for i := range vs {
		if vs[i].Rule != nil && vs[i].Rule.Action == "block" {
			return true
		}
	}
	return false
}

// Engine 规则引擎：对 AI 产出/输入做规则校验。
type Engine struct {
	store *Store
}

// NewEngine 构造 Engine。
func NewEngine(store *Store) *Engine { return &Engine{store: store} }

// Check 检查 content 是否违反 scope 下、匹配 field 的启用规则。
func (e *Engine) Check(ctx context.Context, scope, field, content string) ([]Violation, error) {
	rules, err := e.store.ListEnabled(ctx, scope)
	if err != nil {
		return nil, err
	}
	var vs []Violation
	for i := range rules {
		r := rules[i]
		if r.ConditionField != field {
			continue
		}
		if matchCondition(r.Condition, content) {
			v := r // 取地址
			vs = append(vs, Violation{Rule: &v})
		}
	}
	return vs, nil
}

// matchCondition 先按正则匹配，正则非法则退化为包含匹配（均大小写不敏感）。
func matchCondition(condition, content string) bool {
	if condition == "" {
		return false
	}
	if re, err := regexp.Compile("(?i)" + condition); err == nil {
		if re.MatchString(content) {
			return true
		}
		// 正则合法但未命中，不再走包含
		return false
	}
	return strings.Contains(strings.ToLower(content), strings.ToLower(condition))
}
