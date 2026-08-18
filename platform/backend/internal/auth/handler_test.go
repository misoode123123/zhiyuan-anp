package auth

import (
	"strings"
	"testing"
)

// TestValidUsername 用户名同时是 codews 隔离键（worktree/分支/XDG 经 sanitizeID 清洗）。
// 约束到 slug 形式让 sanitizeID 成为恒等映射，从源头杜绝 "a.b" 与 "a_b" 清洗后撞串
// 导致跨用户改同一份代码——故碰撞型字符必须拒绝。
func TestValidUsername(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		// 合法 slug（sanitizeID 恒等，零碰撞风险）
		{"admin", true},
		{"yxt", true},
		{"zhang-san", true},
		{"dev01", true},
		{"a1", true},
		// 长度边界：2-64 合法
		{"a", false},                     // 太短(<2)
		{"", false},                      // 空
		{strings.Repeat("a", 64), true},  // 上限合法
		{strings.Repeat("a", 65), false}, // 超长(>64)
		// 碰撞型字符——这正是要防的坑（sanitize 会把它们都变成 '-'）
		{"alice.bob", false}, // '.' → 与 alice-bob 撞串
		{"alice_bob", false}, // '_' → 与 alice-bob 撞串
		{"alice bob", false}, // 空格 → 撞串
		{"Alice", false},     // 大写（sanitize 会小写化，与 alice 撞）
		{"张三", false},        // 非 ASCII
		{"a@b", false},       // 特殊字符
		// 首尾连字符（git 分支名/目录名不规范）
		{"-abc", false},
		{"abc-", false},
		{"a--b", true}, // 中间多个连字符合法（sanitize 恒等，不与他人撞）
	}
	for _, c := range cases {
		if got := validUsername(c.name); got != c.want {
			t.Errorf("validUsername(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}
