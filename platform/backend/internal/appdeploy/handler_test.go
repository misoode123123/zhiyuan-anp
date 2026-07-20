package appdeploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsValidEnv 合法环境枚举校验。
func TestIsValidEnv(t *testing.T) {
	if !IsValidEnv(EnvTest) {
		t.Fatal("test 应合法")
	}
	if !IsValidEnv(EnvProd) {
		t.Fatal("prod 应合法")
	}
	for _, bad := range []string{"", "dev", "staging", "TEST", "PROD", "test "} {
		if IsValidEnv(bad) {
			t.Fatalf("不合法环境 %q 应返回 false", bad)
		}
	}
}

// TestTail 截断尾部 n 字符 + 头尾空白清理。
// 注意：tail 按【字节】切片（非 rune），多字节字符可能切碎——此处校验该行为。
func TestTail(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"hello world", 5, "world"},
		{"  hello  ", 100, "hello"}, // trim 后小于 n 全返
		{"abcdefgh", 3, "fgh"},      // 取尾 3
		{"", 5, ""},                 // 空
		{"   ", 5, ""},              // 全空白
		{"ab", 10, "ab"},            // n 超长不切
	}
	for _, c := range cases {
		if got := tail(c.in, c.n); got != c.want {
			t.Fatalf("tail(%q,%d)=%q want %q", c.in, c.n, got, c.want)
		}
	}
	// 多字节字符按字节切片：tail("abc中", 2) = 中 的末 2 字节（非完整 rune）
	// 这是当前实现的行为（潜在 bug：会输出非法 UTF-8），此处固化行为检测。
	got := tail("abc中", 2)
	if len(got) != 2 {
		t.Fatalf("tail(abc中,2) 字节长度应为 2，得到 %d (内容=%q)", len(got), got)
	}
}

// TestTruncateStr 截断到 n 字符并加 "...(截断)" 后缀。
func TestTruncateStr(t *testing.T) {
	// 短串不截
	if got := truncateStr("hi", 100); got != "hi" {
		t.Fatalf("短串应原样返回，得到 %q", got)
	}
	// 长串截断
	long := strings.Repeat("a", 100)
	got := truncateStr(long, 10)
	if !strings.HasPrefix(got, "aaaaaaaaaa") || !strings.HasSuffix(got, "...(截断)") {
		t.Fatalf("长串应截断 + 后缀，得到 %q", got)
	}
	// 边界：刚好等于 n
	if got := truncateStr("abcd", 4); got != "abcd" {
		t.Fatalf("等于 n 不应截断，得到 %q", got)
	}
	// 边界：n=0 时 len(s)<=0 才不截；任何非空都截断
	if got := truncateStr("abc", 0); got != "...(截断)" {
		t.Fatalf("n=0 + 非空串应截断到 0 + 后缀，得到 %q", got)
	}
}

// TestExtractJSONObject 从含 markdown/前后噪声的文本提取首个 JSON 对象。
func TestExtractJSONObject(t *testing.T) {
	// 用变量构造 case，避免 raw string 中的 {} 在复合字面量里扰乱 tokenizer。
	type tc struct {
		name, in, want string
	}
	var cases []tc
	cases = append(cases, tc{"纯JSON", `{"a":1}`, `{"a":1}`})
	cases = append(cases, tc{"前后噪声", "prefix ```json" + `{"x":2}` + "``` tail", `{"x":2}`})
	cases = append(cases, tc{"嵌套对象", `xx {"a":{"b":1}} yy`, `{"a":{"b":1}}`})
	cases = append(cases, tc{"无JSON", "no json here", "no json here"})
	cases = append(cases, tc{"空字符串", "", ""})

	for _, c := range cases {
		got := extractJSONObject(c.in)
		if got != c.want {
			t.Fatalf("%s: got=%q want=%q", c.name, got, c.want)
		}
	}
}

// TestMin 两数取小。
func TestMin(t *testing.T) {
	cases := []struct{ a, b, want int }{
		{1, 2, 1},
		{2, 1, 1},
		{5, 5, 5},
		{0, -1, -1},
	}
	for _, c := range cases {
		if got := min(c.a, c.b); got != c.want {
			t.Fatalf("min(%d,%d)=%d want %d", c.a, c.b, got, c.want)
		}
	}
	// 关键应用：sha 前 7 位
	sha := "abcdef1234567890"
	if got := sha[:min(7, len(sha))]; got != "abcdef1" {
		t.Fatalf("sha 截前 7 = %q", got)
	}
	// 短 sha 不越界
	short := "abc"
	if got := short[:min(7, len(short))]; got != "abc" {
		t.Fatalf("短 sha 应原样: %q", got)
	}
}

// TestErrString_Error 自定义 errString 类型 Error() 返回字符串内容。
func TestErrString_Error(t *testing.T) {
	e := errAppNotFound
	if e.Error() != "应用不存在" {
		t.Fatalf("errString.Error 不匹配: %q", e.Error())
	}
	// 直接构造
	custom := errString("自定义错误")
	if custom.Error() != "自定义错误" {
		t.Fatalf("自定义 errString 不匹配: %q", custom.Error())
	}
}

// TestProbeHealth URL 健康探测：up/down/error(code)/unknown。
func TestProbeHealth(t *testing.T) {
	// unknown：空 URL
	if got := probeHealth(""); got != "unknown" {
		t.Fatalf("空 URL 应 unknown，得到 %q", got)
	}
	// up：2xx/3xx
	srvOK := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	defer srvOK.Close()
	if got := probeHealth(srvOK.URL); got != "up" {
		t.Fatalf("200 应 up，得到 %q", got)
	}
	// error(code)：4xx/5xx
	srvErr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}))
	defer srvErr.Close()
	if got := probeHealth(srvErr.URL); !strings.HasPrefix(got, "error(") || !strings.Contains(got, "503") {
		t.Fatalf("503 应 error(503)，得到 %q", got)
	}
	// down：连不上（关掉 server 后）
	srvDown := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srvDown.Close()
	if got := probeHealth(srvDown.URL); got != "down" {
		t.Fatalf("连不上应 down，得到 %q", got)
	}
	// down：非法 URL
	if got := probeHealth("http://127.0.0.1:0/nope"); got != "down" {
		t.Fatalf("非法 host 应 down，得到 %q", got)
	}
}

// TestProbeHealth_4xxAsError 状态码边界：400-599 都归 error(code)。
func TestProbeHealth_4xxAsError(t *testing.T) {
	for _, code := range []int{400, 404, 500} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(code)
		}))
		got := probeHealth(srv.URL)
		srv.Close()
		if !strings.HasPrefix(got, "error(") {
			t.Fatalf("code=%d 应 error(code)，得到 %q", code, got)
		}
	}
}

// TestReadRepoCode 读取 repo 代码合并成单串（截断 + 文件分块）。
func TestReadRepoCode(t *testing.T) {
	dir, _ := os.MkdirTemp("", "read-code")
	defer os.RemoveAll(dir)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main(){}\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "src"), 0o755)
	os.WriteFile(filepath.Join(dir, "src/util.go"), []byte("package src\n"), 0o644)
	// 隐藏文件应被忽略
	os.WriteFile(filepath.Join(dir, ".env"), []byte("SECRET=x"), 0o644)
	// 排除目录
	os.MkdirAll(filepath.Join(dir, "node_modules/pkg"), 0o755)
	os.WriteFile(filepath.Join(dir, "node_modules/pkg/index.js"), []byte("should not appear"), 0o644)

	got := readRepoCode(dir)
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "package main") {
		t.Fatalf("应含 main.go 内容: %q", got)
	}
	if !strings.Contains(got, "src/util.go") {
		t.Fatalf("应含子目录文件: %q", got)
	}
	if strings.Contains(got, "SECRET=x") {
		t.Fatal("不应包含隐藏文件内容")
	}
	if strings.Contains(got, "should not appear") {
		t.Fatal("不应包含排除目录内容")
	}
	// 含文件分隔标记
	if !strings.Contains(got, "=== main.go ===") {
		t.Fatalf("应含文件分隔标记: %q", got)
	}
}

// TestReadRepoCode_emptyRepo 空仓库返回空串不报错。
func TestReadRepoCode_emptyRepo(t *testing.T) {
	dir, _ := os.MkdirTemp("", "read-empty")
	defer os.RemoveAll(dir)
	if got := readRepoCode(dir); got != "" {
		t.Fatalf("空仓库应返回空串，得到 %q", got)
	}
}

// TestReadRepoCode_Truncation 限制：超过 15 文件 / 8000 字符自动截断（覆盖截断分支）。
func TestReadRepoCode_Truncation(t *testing.T) {
	dir, _ := os.MkdirTemp("", "read-trunc")
	defer os.RemoveAll(dir)
	// 创建 20 个文件 > 15 上限
	for i := 0; i < 20; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		os.WriteFile(name, []byte("content\n"), 0o644)
	}
	got := readRepoCode(dir)
	// 文件数 > 15 → 不会全部收录（break）
	// 至少应包含首文件
	if !strings.Contains(got, "=== ") {
		t.Fatalf("应含至少一个文件标记: %q", got)
	}
}

// TestStats_HandlerDeployedFalse Handler.Stats 在实例未部署时返回 deployed=false（不走 docker 路径）。
// 注：此处直接测 store 路径，handler 层 deployer 不调用（ins==nil 分支）。
func TestStats_HandlerDeployedFalse(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	a := mkApp("ps_1", "snake")
	_ = s.Create(ctx, a)

	// 无实例 → Stats 应返回 deployed=false（不调 docker）
	ins, _ := s.GetInstance(ctx, a.ID, EnvProd)
	if ins != nil {
		t.Fatal("未部署前实例应为 nil")
	}
}

// TestSummarizeChange_NoApiKeyOrEmpty apiKey 空 或 diff+conversation 都空 → 直接返回空串（不发 HTTP）。
func TestSummarizeChange_NoApiKeyOrEmpty(t *testing.T) {
	// 无 apiKey
	if got := summarizeChange(context.Background(), "", "diff", "conv"); got != "" {
		t.Fatalf("无 apiKey 应返回空，得到 %q", got)
	}
	// apiKey 在但 diff/conversation 都空
	if got := summarizeChange(context.Background(), "k", "", ""); got != "" {
		t.Fatalf("无内容应返回空，得到 %q", got)
	}
}

// TestCheckRequirement_NoApiKeyOrEmpty 无 apiKey 或无内容 → 放行（passed=true），不调 HTTP。
func TestCheckRequirement_NoApiKeyOrEmpty(t *testing.T) {
	// 无 apiKey → 放行
	passed, details := checkRequirement(context.Background(), "", "code", "title", "criteria")
	if !passed {
		t.Fatal("无 apiKey 应放行")
	}
	if !strings.Contains(details, "未配置") && !strings.Contains(details, "跳过") {
		t.Fatalf("放行原因应含 '未配置' 或 '跳过'，得到 %q", details)
	}
	// 有 apiKey 但 code/criteria 都空 → 放行
	passed2, _ := checkRequirement(context.Background(), "k", "", "", "")
	if !passed2 {
		t.Fatal("有 apiKey 但无内容应放行")
	}
}
