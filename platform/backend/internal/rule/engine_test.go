package rule

import (
	"context"
	"testing"
)

// ===================== matchCondition 纯逻辑测试 =====================
// matchCondition 是引擎的核心判定：正则匹配优先，非法正则退化为大小写不敏感的包含匹配。

// TestMatchCondition_EmptyCondition 空条件永不命中（防御默认值）。
func TestMatchCondition_EmptyCondition(t *testing.T) {
	if matchCondition("", "anything") {
		t.Fatal("空条件应返回 false")
	}
	if matchCondition("", "") {
		t.Fatal("空条件 + 空内容也应返回 false")
	}
}

// TestMatchCondition_RegexMatch 合法正则命中。
func TestMatchCondition_RegexMatch(t *testing.T) {
	cases := []struct {
		name, cond, content string
		want                bool
	}{
		{"正则或表达式命中左", "foo|bar", "there is foo here", true},
		{"正则或表达式命中右", "foo|bar", "there is bar here", true},
		{"正则字符类命中", "[0-9]+", "abc123def", true},
		{"正则锚定完全匹配", "^hello$", "hello", true},
		{"正则锚定不匹配", "^hello$", "hello world", false},
		{"合法正则但不命中不走包含退化", "xyz\\d+", "xyz abc", false},
		{"正则点号匹配任意字符", "a.c", "axc", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchCondition(c.cond, c.content); got != c.want {
				t.Fatalf("matchCondition(%q,%q)=%v, want %v", c.cond, c.content, got, c.want)
			}
		})
	}
}

// TestMatchCondition_CaseInsensitive 正则与包含都应大小写不敏感。
func TestMatchCondition_CaseInsensitive(t *testing.T) {
	// 正则路径：内部已拼 (?i) 前缀
	if !matchCondition("ERROR", "something errored") {
		t.Fatal("正则路径应大小写不敏感：ERROR vs errored")
	}
	if !matchCondition("error", "ERROR: boom") {
		t.Fatal("正则路径应大小写不敏感：error vs ERROR")
	}
}

// TestMatchCondition_FallbackToContains 非法正则退化为包含匹配（大小写不敏感）。
func TestMatchCondition_FallbackToContains(t *testing.T) {
	// 未闭合括号 → 非法正则 → 走 strings.Contains
	cases := []struct {
		name, cond, content string
		want                bool
	}{
		{"非法正则退化命中", "(unclosed", "text (unclosed here", true},
		{"非法正则退化不命中", "(unclosed", "no match here", false},
		{"非法正则退化大小写不敏感", "(UNCLOSED", "see (unclosed", true},
		{"单左方括号非法正则命中", "[abc", "raw [abc token", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := matchCondition(c.cond, c.content); got != c.want {
				t.Fatalf("matchCondition(%q,%q)=%v, want %v（应退化包含匹配）",
					c.cond, c.content, got, c.want)
			}
		})
	}
}

// TestMatchCondition_EmptyContent 空内容：除非正则匹配空串，否则不命中。
func TestMatchCondition_EmptyContent(t *testing.T) {
	if matchCondition("foo", "") {
		t.Fatal("空内容对非空正则应不命中")
	}
	// 合法正则 a* 可匹配空串 → 命中（注意：这是 Go 正则的行为）
	if !matchCondition("a*", "") {
		t.Fatal("正则 a* 应匹配空串")
	}
}

// TestMatchCondition_SpecialChars 内容/条件含正则元字符时正则路径行为正确。
func TestMatchCondition_SpecialChars(t *testing.T) {
	// 条件作为正则会把 . 当作「任意字符」；想匹配字面量需转义
	if matchCondition("a.c", "abc") {
		// 这是正则行为（. 匹配 b），符合预期
	} else {
		t.Fatal("正则 a.c 应匹配 abc（. 匹配任意字符）")
	}
	// 字面量点号需转义
	if !matchCondition(`a\.c`, "a.c") {
		t.Fatal("转义 a\\.c 应匹配字面量 a.c")
	}
	if matchCondition(`a\.c`, "abc") {
		t.Fatal("转义 a\\.c 不应匹配 abc")
	}
}

// ===================== HasBlock 纯逻辑测试 =====================

// TestHasBlock_EmptyAndNil 空集合或全 nil Rule 都不算阻断。
func TestHasBlock_EmptyAndNil(t *testing.T) {
	if HasBlock(nil) {
		t.Fatal("nil 入参应返回 false")
	}
	if HasBlock([]Violation{}) {
		t.Fatal("空切片应返回 false")
	}
	if HasBlock([]Violation{{Rule: nil}}) {
		t.Fatal("nil Rule 的 violation 应被跳过，返回 false")
	}
}

// TestHasBlock_OnlyWarn 全是 warn/非 block → false。
func TestHasBlock_OnlyWarn(t *testing.T) {
	warn := &Rule{Action: "warn"}
	approval := &Rule{Action: "require_approval"}
	if HasBlock([]Violation{{Rule: warn}, {Rule: approval}}) {
		t.Fatal("全是 warn/require_approval 应返回 false")
	}
}

// TestHasBlock_ContainsBlock 只要有一条 block → true。
func TestHasBlock_ContainsBlock(t *testing.T) {
	warn := &Rule{Action: "warn"}
	block := &Rule{Action: "block"}
	if !HasBlock([]Violation{{Rule: warn}, {Rule: block}}) {
		t.Fatal("含 block 应返回 true")
	}
	if !HasBlock([]Violation{{Rule: nil}, {Rule: block}}) {
		t.Fatal("含 block（即使夹 nil）应返回 true")
	}
}

// ===================== Engine.Check 集成测试（经 Store + sqlite） =====================
// Engine.Check 依赖 Store.ListEnabled，故借助 newTestStore 做端到端覆盖。

// TestEngine_Check_HitAndMiss 同 scope 下，匹配 field+condition 的规则命中；不匹配 field 的跳过。
func TestEngine_Check_HitAndMiss(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, newRule("R-prompt", "delete.*migrate", "prompt", "block", "dev", true))
	mustCreate(t, s, newRule("R-output", "api[_-]?key", "output", "warn", "dev", true))

	e := NewEngine(s)
	ctx := context.Background()

	// prompt 命中 block
	vs, err := e.Check(ctx, "dev", "prompt", "please delete the migrate file")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if len(vs) != 1 {
		t.Fatalf("prompt 应命中 1 条，得到 %d", len(vs))
	}
	if vs[0].Rule.Name != "R-prompt" || vs[0].Rule.Action != "block" {
		t.Fatalf("命中规则错误： %+v", vs[0].Rule)
	}
	if !HasBlock(vs) {
		t.Fatal("含 block violation，HasBlock 应为 true")
	}

	// output 命中 warn（非 block）
	vs, _ = e.Check(ctx, "dev", "output", "API_KEY=sk-xxx")
	if len(vs) != 1 || vs[0].Rule.Action != "warn" {
		t.Fatalf("output 应命中 1 条 warn，得到 %+v", vs)
	}
	if HasBlock(vs) {
		t.Fatal("仅 warn 不应判定为阻断")
	}

	// 不匹配的 field 不命中
	vs, _ = e.Check(ctx, "dev", "code_path", "delete migrate")
	if len(vs) != 0 {
		t.Fatalf("不匹配 field 应 0 命中，得到 %d", len(vs))
	}

	// 匹配 field 但内容不命中正则
	vs, _ = e.Check(ctx, "dev", "prompt", "hello world")
	if len(vs) != 0 {
		t.Fatalf("内容不命中应 0 violation，得到 %d", len(vs))
	}
}

// TestEngine_Check_ScopeFilter scope='all' 规则被任意 scope 命中；其它 scope 不串。
func TestEngine_Check_ScopeFilter(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, newRule("global", "secret", "prompt", "block", "all", true))
	mustCreate(t, s, newRule("dev-only", "secret", "prompt", "warn", "dev", true))
	mustCreate(t, s, newRule("req-only", "secret", "prompt", "warn", "requirement", true))

	e := NewEngine(s)

	// scope=dev：全局 + dev 命中（2 条）；req-only 被过滤
	vs, _ := e.Check(context.Background(), "dev", "prompt", "contains secret here")
	if len(vs) != 2 {
		t.Fatalf("dev scope 应命中 2 条(global+dev)，得到 %d", len(vs))
	}

	// scope=requirement：全局 + req 命中（2 条）
	vs, _ = e.Check(context.Background(), "requirement", "prompt", "contains secret here")
	if len(vs) != 2 {
		t.Fatalf("requirement scope 应命中 2 条(global+req)，得到 %d", len(vs))
	}
}

// TestEngine_Check_DisabledRuleExcluded 被禁用的规则即使内容匹配也不应被引擎命中。
func TestEngine_Check_DisabledRuleExcluded(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, newRule("off", "secret", "prompt", "block", "all", false))
	mustCreate(t, s, newRule("on", "secret", "prompt", "warn", "all", true))

	e := NewEngine(s)
	vs, _ := e.Check(context.Background(), "dev", "prompt", "has secret")
	if len(vs) != 1 || vs[0].Rule.Name != "on" {
		t.Fatalf("禁用规则应被排除，期望仅命中 on，得到 %+v", vs)
	}
}

// TestEngine_Check_MultipleViolationsAndBlockPriority 多条命中时，只要存在 block 即阻断。
func TestEngine_Check_MultipleViolationsAndBlockPriority(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, newRule("warn-1", "foo", "prompt", "warn", "all", true))
	mustCreate(t, s, newRule("warn-2", "bar", "prompt", "warn", "all", true))
	mustCreate(t, s, newRule("block-1", "baz", "prompt", "block", "all", true))

	e := NewEngine(s)
	vs, _ := e.Check(context.Background(), "dev", "prompt", "foo bar baz all matched")
	if len(vs) != 3 {
		t.Fatalf("应命中 3 条，得到 %d", len(vs))
	}
	if !HasBlock(vs) {
		t.Fatal("含 block-1，HasBlock 必须为 true")
	}
}

// TestEngine_Check_NoRulesEmptyTable 空表时返回空 violations，无错误。
func TestEngine_Check_NoRulesEmptyTable(t *testing.T) {
	s := newTestStore(t)
	e := NewEngine(s)
	vs, err := e.Check(context.Background(), "dev", "prompt", "anything")
	if err != nil {
		t.Fatalf("空表 Check 不应报错: %v", err)
	}
	if vs != nil && len(vs) != 0 {
		t.Fatalf("空表应 0 violation，得到 %+v", vs)
	}
}

// TestEngine_Check_RealDemoRules 用 SeedDemoRules 播种真实演示规则，
// 验证端到端 RaC 判定（中英文正则、跨字段、block/warn/require_approval）。
func TestEngine_Check_RealDemoRules(t *testing.T) {
	s := newTestStore(t)
	if err := s.SeedDemoRules(context.Background()); err != nil {
		t.Fatalf("SeedDemoRules: %v", err)
	}
	e := NewEngine(s)
	ctx := context.Background()

	// ① 中文「删除迁移」命中 block
	// 注意：演示规则正则要求字面量「删除」与「migrate/迁移」同时出现，
	// 用「删了」「migrations」均不会命中（前者非「删除」，后者第 7 字符 i≠e）。
	vs, _ := e.Check(ctx, "dev", "prompt", "请帮我删除 migrate 迁移文件")
	if !HasBlock(vs) {
		t.Fatalf("「删除迁移」应触发 block，得到 %+v", vs)
	}

	// ② 英文「production deploy」命中 require_approval（非 block）
	// 注意：演示规则正则要求 (生产|prod|...) 出现在 (部署|deploy|...) 之前，
	// 顺序反了（如 "deploy ... production"）不会命中。
	vs, _ = e.Check(ctx, "dev", "prompt", "production deploy please")
	if len(vs) != 1 || vs[0].Rule.Action != "require_approval" {
		t.Fatalf("production deploy 应命中 require_approval，得到 %+v", vs)
	}
	if HasBlock(vs) {
		t.Fatal("require_approval 不应被判定为 block")
	}

	// ③ output 字段含 api_key 命中 warn
	vs, _ = e.Check(ctx, "dev", "output", "API_KEY=sk-12345")
	if len(vs) != 1 || vs[0].Rule.Action != "warn" {
		t.Fatalf("output 含 api_key 应命中 warn，得到 %+v", vs)
	}

	// ④ 不相关内容无命中
	vs, _ = e.Check(ctx, "dev", "prompt", "今天天气真好")
	if len(vs) != 0 {
		t.Fatalf("无关内容应 0 命中，得到 %d", len(vs))
	}
}
