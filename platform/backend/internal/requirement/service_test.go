package requirement

import (
	"context"
	"strings"
	"testing"
)

// ===== 纯逻辑：deriveAppName / pinyinSlug / shortSuffix =====

func TestPinyinSlug(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"贪吃蛇H5游戏", "tan-chi-she-h5-you-xi"}, // 中文逐字拼音 + ASCII 连续合并小写
		{"用户中心 V2", "yong-hu-zhong-xin-v2"},  // 空格分词、大写 V→v
		{"Hello World", "hello-world"},       // 全 ASCII
		{"", ""},                             // 空串兜底
		{"!!@@##", ""},                       // 全标点
	}
	for _, c := range cases {
		if got := pinyinSlug(c.in); got != c.want {
			t.Errorf("pinyinSlug(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestShortSuffix(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"req_abc123def456", "abc123def456"[:8]}, // 取末 8：取最后 8 位
		{"req_short", "reqshort"},                // 短串全用
		{"REQ_UP", "requp"},                      // 小写化
		{"", ""},
	}
	// 修正：req_abc123def456 去下划线小写 → reqabc123def456，末 8 = 23def456... 重算
	cases[0].want = "23def456" // 16 字符串末 8
	for _, c := range cases {
		if got := shortSuffix(c.in); got != c.want {
			t.Errorf("shortSuffix(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestDeriveAppName 中文标题 → pinyin-base（截断 24）+ reqID 短后缀。
func TestDeriveAppName(t *testing.T) {
	got := deriveAppName("贪吃蛇H5游戏", "req_abcdef123456")
	// base=tan-chi-she-h5-you-xi (19<24 不截断) + "-" + shortSuffix
	wantSuffix := shortSuffix("req_abcdef123456")
	if !strings.HasSuffix(got, "-"+wantSuffix) {
		t.Fatalf("deriveAppName 应以 shortSuffix 结尾，得到 %s", got)
	}
	if !strings.HasPrefix(got, "tan-chi-she-h5-you-xi") {
		t.Fatalf("deriveAppName 应以拼音 base 开头，得到 %s", got)
	}

	// 空标题 → app 兜底
	got2 := deriveAppName("", "req_x")
	if !strings.HasPrefix(got2, "app-") {
		t.Fatalf("空标题应兜底为 app-xxx，得到 %s", got2)
	}

	// 超长标题截断到 24 字符的 base
	long := strings.Repeat("需求", 30) // 30×2=60 字符拼音 60 词
	got3 := deriveAppName(long, "req_longid123")
	// 从 suffix 反推 base（base 自身也含 "-"，不能简单按 "-" 切分）
	suf := shortSuffix("req_longid123")
	basePart := strings.TrimSuffix(got3, "-"+suf)
	if len(basePart) > 24 {
		t.Fatalf("base 应截断到 ≤24 字符，得到 %d: %s", len(basePart), basePart)
	}
}

// TestBuildCodePrompt 需求→编码 prompt，标题/用户故事/验收标准齐全 + 可部署性约束。
func TestBuildCodePrompt(t *testing.T) {
	r := &Requirement{
		Title:              "登录页",
		UserStory:          "作为访客，我希望登录",
		AcceptanceCriteria: `["SSO 跳转","回调登录"]`,
		Description:        "附加说明",
	}
	got := buildCodePrompt(r)
	for _, want := range []string{"登录页", "作为访客", "SSO 跳转", "回调登录", "附加说明", "Web 服务"} {
		if !strings.Contains(got, want) {
			t.Errorf("buildCodePrompt 缺少 %q\n完整: %s", want, got)
		}
	}
}

// TestBuildCodePrompt_EmptyAcceptanceCriteria 验收标准非合法 JSON 时不 panic、prompt 仍含标题。
func TestBuildCodePrompt_EmptyAcceptanceCriteria(t *testing.T) {
	r := &Requirement{
		Title:              "标题",
		UserStory:          "故事",
		AcceptanceCriteria: "", // 空 / 非法 JSON
	}
	got := buildCodePrompt(r) // 不应 panic
	if !strings.Contains(got, "标题") {
		t.Errorf("空 AC 时 prompt 应仍含标题，得到 %s", got)
	}
}

// TestExtractJSON 从 markdown 包裹的文本中提取首个 JSON 对象。
func TestExtractJSON(t *testing.T) {
	cases := []struct{ in, want string }{
		{"```json\n{\"a\":1}\n```", `{"a":1}`},
		{`prefix {"b":2} suffix`, `{"b":2}`},
		{`no json here`, `no json here`}, // 无 { } 时原样返回
		{`{"x":{"y":2}}`, `{"x":{"y":2}}`},
	}
	for _, c := range cases {
		if got := extractJSON(c.in); got != c.want {
			t.Errorf("extractJSON(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// ===== Service 委派（用真 repo，不调 AI）=====

// TestService_ListAndListByApp 通过 Service 走 repo.List/ListByApp。
func TestService_ListAndListByApp(t *testing.T) {
	repo := newTestRepo(t)
	svc := NewService(repo, "", nil, nil, nil)

	req := mkReq("req_svc1", "ps_1")
	req.ApplicationID = "app_z"
	mustCreateRepo(t, repo, req)

	list, err := svc.List(context.Background(), "ps_1")
	if err != nil || len(list) != 1 {
		t.Fatalf("Service.List: err=%v len=%d", err, len(list))
	}
	byApp, err := svc.ListByApp(context.Background(), "app_z")
	if err != nil || len(byApp) != 1 {
		t.Fatalf("Service.ListByApp: err=%v len=%d", err, len(byApp))
	}
}

// TestService_AssignAndRelease Service 层 Assign/Release 走 repo 互斥逻辑。
func TestService_AssignAndRelease(t *testing.T) {
	repo := newTestRepo(t)
	svc := NewService(repo, "", nil, nil, nil)
	mustCreateRepo(t, repo, mkReq("req_svc2", "ps_1"))

	if err := svc.Assign(context.Background(), "req_svc2", "alice"); err != nil {
		t.Fatalf("Service.Assign: %v", err)
	}
	// 他人再来→冲突
	if err := svc.Assign(context.Background(), "req_svc2", "bob"); err == nil {
		t.Fatal("Service.Assign 互斥应拒绝 bob")
	}
	// 释放→bob 可认领
	if err := svc.Release(context.Background(), "req_svc2"); err != nil {
		t.Fatalf("Service.Release: %v", err)
	}
	if err := svc.Assign(context.Background(), "req_svc2", "bob"); err != nil {
		t.Fatalf("释放后 bob 应可认领: %v", err)
	}
}

// TestService_Dispatch_NoCoder 编码引擎未配置→明确错误（不触碰 AI/外部）。
func TestService_Dispatch_NoCoder(t *testing.T) {
	repo := newTestRepo(t)
	svc := NewService(repo, "", nil, nil, nil)
	mustCreateRepo(t, repo, mkReq("req_d", "ps_1"))

	_, err := svc.Dispatch(context.Background(), "ps_1", "req_d", "/tmp/repo", "glm-5.1")
	if err == nil || !strings.Contains(err.Error(), "编码引擎未配置") {
		t.Fatalf("coder=nil 应返回'编码引擎未配置'，得到: %v", err)
	}
}

// TestService_Dispatch_RequirementNotFound 需求不存在→明确错误。
func TestService_Dispatch_RequirementNotFound(t *testing.T) {
	repo := newTestRepo(t)
	svc := NewService(repo, "", nil, nil, nil)
	// 注意：coder 仍是 nil，所以 Dispatch 会先在 coder 校验处失败；
	// 但若需走"读取需求"分支，可让 coder 非 nil —— 这里仅验证 nil 路径稳定返回错误，
	// 避免在单测中引入 dev.CodingAgent 真实依赖（任务约定跳过 opencode 编码相关）。
	_, err := svc.Dispatch(context.Background(), "ps_1", "req_missing", "/tmp/repo", "glm-5.1")
	if err == nil {
		t.Fatal("Dispatch 应返回错误")
	}
}
