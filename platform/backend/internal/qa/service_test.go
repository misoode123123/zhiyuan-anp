package qa

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------- extractObjects 纯逻辑（容错解析 AI 输出） ----------

// TestExtractObjects_SingleArray 标准单数组：[{"a":1},{"b":2}] → 2 对象。
func TestExtractObjects_SingleArray(t *testing.T) {
	got := extractObjects(`[{"title":"a"},{"title":"b"}]`)
	if len(got) != 2 {
		t.Fatalf("应提取 2 个对象，得到 %d", len(got))
	}
	if !strings.Contains(got[0], `"title":"a"`) {
		t.Fatalf("首对象内容异常：%s", got[0])
	}
}

// TestExtractObjects_MultiArray 多数组容错：[{..}][{..}] → 2 对象。
// 这是 AI 输出的真实异常格式，被解析逻辑专门兼容。
func TestExtractObjects_MultiArray(t *testing.T) {
	got := extractObjects(`[{"a":1}][{"b":2}]`)
	if len(got) != 2 {
		t.Fatalf("多数组应提取 2 个对象，得到 %d", len(got))
	}
}

// TestExtractObjects_Scattered 散落对象（无 [ 包装），夹杂噪声文本。
func TestExtractObjects_Scattered(t *testing.T) {
	got := extractObjects(`好的，以下是测试用例：{"title":"x"} 后续说明 {"title":"y"}`)
	if len(got) != 2 {
		t.Fatalf("散落对象应提取 2 个，得到 %d", len(got))
	}
}

// TestExtractObjects_Empty 边界：空串/无对象 → 0。
func TestExtractObjects_Empty(t *testing.T) {
	for _, in := range []string{"", "hello world", "[]", "没有花括号"} {
		if got := extractObjects(in); len(got) != 0 {
			t.Fatalf("输入 %q 应返回 0 个对象，得到 %d", in, len(got))
		}
	}
}

// TestExtractObjects_Nested 嵌套对象：只返回最外层 1 个，内层 { 不切分。
func TestExtractObjects_Nested(t *testing.T) {
	got := extractObjects(`{"a":{"b":1},"c":2}`)
	if len(got) != 1 {
		t.Fatalf("嵌套对象应整体返回 1 个，得到 %d", len(got))
	}
	if !strings.Contains(got[0], `"b":1`) {
		t.Fatalf("应包含内层字段：%s", got[0])
	}
}

// TestExtractObjects_BraceInString 字符串内的 } 不能算闭合（inStr 状态机保护）。
func TestExtractObjects_BraceInString(t *testing.T) {
	got := extractObjects(`{"a":"}扰动{"}`)
	if len(got) != 1 {
		t.Fatalf("字符串内花括号不应打断配平，得到 %d 个", len(got))
	}
}

// TestExtractObjects_EscapedQuote 转义引号 \" 后仍正确判断字符串边界。
// 关键回归：prev != '\\' 判断，遇 \\" 应已出字符串。
func TestExtractObjects_EscapedQuote(t *testing.T) {
	got := extractObjects(`{"a":"he\"llo","b":2}`)
	if len(got) != 1 {
		t.Fatalf("转义引号对象应整体返回 1 个，得到 %d", len(got))
	}
}

// TestExtractObjects_Unclosed 边界：未闭合对象不应计入结果。
func TestExtractObjects_Unclosed(t *testing.T) {
	got := extractObjects(`{"a":1`)
	if len(got) != 0 {
		t.Fatalf("未闭合对象应返回 0，得到 %d", len(got))
	}
}

// ---------- truncate 纯逻辑 ----------

// TestTruncate_Short 短于 n：原样返回。
func TestTruncate_Short(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Fatalf("短串应原样返回，得到 %q", got)
	}
}

// TestTruncate_Exact 边界：恰好等于 n 不截断（len > n 判定）。
func TestTruncate_Exact(t *testing.T) {
	if got := truncate("hello", 5); got != "hello" {
		t.Fatalf("恰好等长应原样返回，得到 %q", got)
	}
}

// TestTruncate_Long 长于 n：截断前 n 字节并加省略号。
// 注意：truncate 按 byte 计数，"…" 是 3 字节 UTF-8 字符，故总长度为 n+3。
func TestTruncate_Long(t *testing.T) {
	got := truncate("hello world", 5)
	if !strings.HasPrefix(got, "hello") || !strings.HasSuffix(got, "…") {
		t.Fatalf("超长应截断+…，得到 %q", got)
	}
	if len(got) != 8 { // 5 + 3（… 占 3 字节）
		t.Fatalf("截断长度异常：%d", len(got))
	}
}

// ---------- RunHTTPRequest 状态迁移分支（本地 httptest，非外部应用） ----------

// newSvcWithStore 复用 store_test 的内存 SQLite 建表逻辑，封装 Service。
func newSvcWithStore(t *testing.T) *Service {
	return NewService(newTestStore(t), "http://unused-agent-runtime")
}

// TestRunHTTPRequest_ManualNoAssertion 无 HTTP 断言（ExpectedStatus=0 且 ExpectedBody 空）
// 必然标 manual，且 store 中状态被回写。
func TestRunHTTPRequest_ManualNoAssertion(t *testing.T) {
	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := &TestCase{
		ID:             "tc_manual",
		ProjectSpaceID: "ps_1",
		Title:          "纯人工验证用例",
		Status:         "draft",
		// ExpectedStatus=0, ExpectedBody="" → 无 HTTP 断言
	}
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, "http://example.com"); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "manual" {
		t.Fatalf("无断言应标 manual，得到 %s", tc.Status)
	}
	if !strings.Contains(tc.ActualBody, "人工验证") {
		t.Fatalf("ActualBody 应提示人工验证：%s", tc.ActualBody)
	}
	if tc.RunAt == nil {
		t.Fatal("RunAt 应被赋值")
	}
	// 库内状态已持久化。
	got, _ := svc.store.Get(ctx, tc.ID)
	if got.Status != "manual" {
		t.Fatalf("store 中 status 应为 manual，得到 %s", got.Status)
	}
}

// TestRunHTTPRequest_ManualNoAssertionNoBaseURL 无断言 + baseURL 空：
// ActualBody 文案与有 baseURL 的分支不同（提示"未归属已部署应用"）。
func TestRunHTTPRequest_ManualNoAssertionNoBaseURL(t *testing.T) {
	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := &TestCase{ID: "tc_m2", ProjectSpaceID: "ps_1", Title: "x", Status: "draft"}
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, ""); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "manual" {
		t.Fatalf("应标 manual，得到 %s", tc.Status)
	}
	if !strings.Contains(tc.ActualBody, "未归属已部署应用") {
		t.Fatalf("baseURL 空应提示未归属已部署应用：%s", tc.ActualBody)
	}
}

// TestRunHTTPRequest_ManualAssertionButNoBaseURL 有断言但 baseURL 空：
// 无法自动运行，标 manual，提示先发布部署。
func TestRunHTTPRequest_ManualAssertionButNoBaseURL(t *testing.T) {
	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "case_no_base")
	tc.ID = "tc_no_base"
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, ""); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "manual" {
		t.Fatalf("baseURL 空 + 有断言应标 manual，得到 %s", tc.Status)
	}
	if !strings.Contains(tc.ActualBody, "无法自动运行") {
		t.Fatalf("应提示无法自动运行：%s", tc.ActualBody)
	}
}

// TestRunHTTPRequest_Pass httptest 起 200 返回 "ok"：状态码与 body 都匹配 → passed。
func TestRunHTTPRequest_Pass(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "pass_case")
	tc.ID = "tc_pass"
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, srv.URL); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "passed" {
		t.Fatalf("断言通过应标 passed，得到 %s（actual=%d body=%q）",
			tc.Status, tc.ActualStatus, tc.ActualBody)
	}
	if tc.ActualStatus != 200 {
		t.Fatalf("ActualStatus 应为 200，得到 %d", tc.ActualStatus)
	}
}

// TestRunHTTPRequest_FailStatus 状态码不符 → failed。
func TestRunHTTPRequest_FailStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "fail_status")
	tc.ID = "tc_fail_status"
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, srv.URL); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "failed" {
		t.Fatalf("状态码不符应标 failed，得到 %s", tc.Status)
	}
	if tc.ActualStatus != 500 {
		t.Fatalf("ActualStatus 应为 500，得到 %d", tc.ActualStatus)
	}
}

// TestRunHTTPRequest_FailBody 状态码对但 body 不含期望文本 → failed。
func TestRunHTTPRequest_FailBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("hello world"))
	}))
	defer srv.Close()

	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "fail_body")
	tc.ID = "tc_fail_body"
	tc.ExpectedBody = "MISSING-TOKEN" // body 不含
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, srv.URL); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "failed" {
		t.Fatalf("body 不匹配应标 failed，得到 %s", tc.Status)
	}
}

// TestRunHTTPRequest_DefaultMethodPath 空 method/path 默认 GET /。
func TestRunHTTPRequest_DefaultMethodPath(t *testing.T) {
	var sawMethod, sawPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawMethod = r.Method
		sawPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := &TestCase{
		ID:             "tc_defaults",
		ProjectSpaceID: "ps_1",
		Title:          "默认 method/path",
		Status:         "draft",
		ExpectedStatus: 200,
		ExpectedBody:   "ok",
		// Method/Path 留空
	}
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, srv.URL); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if sawMethod != "GET" || sawPath != "/" {
		t.Fatalf("默认应为 GET /，实际 %s %s", sawMethod, sawPath)
	}
	if tc.Status != "passed" {
		t.Fatalf("默认 method/path 通过应 passed，得到 %s", tc.Status)
	}
}

// TestRunHTTPRequest_BadURL 构造请求失败（非法 URL）→ failed，ActualBody 提示构造失败。
func TestRunHTTPRequest_BadURL(t *testing.T) {
	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "bad_url")
	tc.ID = "tc_bad"
	_ = svc.store.Create(ctx, tc)
	// baseURL 含控制字符使 http.NewRequest 解析失败。
	if err := svc.RunHTTPRequest(ctx, tc, "http://example.com\x7f"); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "failed" {
		t.Fatalf("构造请求失败应标 failed，得到 %s", tc.Status)
	}
	if !strings.Contains(tc.ActualBody, "构造请求失败") && !strings.Contains(tc.ActualBody, "请求失败") {
		t.Fatalf("ActualBody 应提示请求失败：%s", tc.ActualBody)
	}
}

// TestRunHTTPRequest_BodyTruncation 响应超 500 字节时 ActualBody 被截断（验证 truncate 落地）。
func TestRunHTTPRequest_BodyTruncation(t *testing.T) {
	long := strings.Repeat("a", 1000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(long))
	}))
	defer srv.Close()

	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "long_body")
	tc.ID = "tc_long"
	tc.ExpectedBody = "" // 不校验 body 内容，避免内容包含
	tc.ExpectedStatus = 200
	_ = svc.store.Create(ctx, tc)

	if err := svc.RunHTTPRequest(ctx, tc, srv.URL); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	// 500 字节 + "…" = 501 字符。
	if len(tc.ActualBody) > 510 {
		t.Fatalf("ActualBody 应被截断到 ~500 字节，得到长度 %d", len(tc.ActualBody))
	}
}

// TestRunHTTPRequest_UnreachableHost 不可达主机 → failed，ActualBody 提示请求失败。
func TestRunHTTPRequest_UnreachableHost(t *testing.T) {
	svc := newSvcWithStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tc := mkTC("ps_1", "req_1", "unreach")
	tc.ID = "tc_unreach"
	_ = svc.store.Create(ctx, tc)
	// 用一个几乎一定不可达的地址：保留地址 + 高位端口。
	if err := svc.RunHTTPRequest(ctx, tc, "http://127.0.0.1:1"); err != nil {
		t.Fatalf("RunHTTPRequest: %v", err)
	}
	if tc.Status != "failed" {
		t.Fatalf("不可达应标 failed，得到 %s", tc.Status)
	}
	if !strings.Contains(tc.ActualBody, "请求失败") {
		t.Fatalf("ActualBody 应提示请求失败：%s", tc.ActualBody)
	}
}

// TestNewService 构造器透传参数。
func TestNewService(t *testing.T) {
	svc := NewService(&Store{}, "http://x")
	if svc.agentRuntimeURL != "http://x" {
		t.Fatalf("agentRuntimeURL 应透传，得到 %s", svc.agentRuntimeURL)
	}
}

// TestService_GetListPassthrough Service.GetCase/ListByProjectSpace/ListByRequirement
// 是对 store 的薄透传：在内存库建几条，验证读回一致。
func TestService_GetListPassthrough(t *testing.T) {
	svc := newSvcWithStore(t)
	ctx := context.Background()
	tc := mkTC("ps_1", "req_1", "p1")
	_ = svc.store.Create(ctx, tc)

	got, err := svc.GetCase(ctx, tc.ID)
	if err != nil || got.ID != tc.ID {
		t.Fatalf("GetCase 透传错误：err=%v got=%+v", err, got)
	}
	list, err := svc.ListByProjectSpace(ctx, "ps_1")
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByProjectSpace 透传错误：err=%v len=%d", err, len(list))
	}
	rl, err := svc.ListByRequirement(ctx, "req_1")
	if err != nil || len(rl) != 1 {
		t.Fatalf("ListByRequirement 透传错误：err=%v len=%d", err, len(rl))
	}
}
