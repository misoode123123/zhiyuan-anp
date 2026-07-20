package conversation

import (
	"context"
	"strings"
	"testing"
)

// TestParseMsgContent 消息内容解析：空串、纯文本、JSON 文本、含图片多模态。
// 该函数是 user/assistant content 入库后的反序列化入口，覆盖 fallback 路径。
func TestParseMsgContent(t *testing.T) {
	cases := []struct {
		name     string
		in       string
		wantText string
		wantImgs []string
	}{
		{"空字符串", "", "", nil},
		{"纯文本（非 JSON）", "随便一句话", "随便一句话", nil},
		{"纯 JSON 文本", `{"text":"你好"}`, "你好", nil},
		{"含图片", `{"text":"看图","images":["http://x/a.png"]}`,
			"看图", []string{"http://x/a.png"}},
		{"多图", `{"text":"多图","images":["http://x/a","http://x/b"]}`,
			"多图", []string{"http://x/a", "http://x/b"}},
		{"非法 JSON 退化纯文本", "{not json", "{not json", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseMsgContent(tc.in)
			if got.Text != tc.wantText {
				t.Fatalf("Text 不匹配：got=%q want=%q", got.Text, tc.wantText)
			}
			if len(got.Images) != len(tc.wantImgs) {
				t.Fatalf("Images 数量不匹配：got=%d want=%d (%v)", len(got.Images), len(tc.wantImgs), got.Images)
			}
			for i := range got.Images {
				if got.Images[i] != tc.wantImgs[i] {
					t.Fatalf("Images[%d] 不匹配：got=%q want=%q", i, got.Images[i], tc.wantImgs[i])
				}
			}
		})
	}
}

// TestToChatContent_PlainText 无图片时 content 是纯字符串（高效路径）。
func TestToChatContent_PlainText(t *testing.T) {
	got := toChatContent(msgContent{Text: "hello"})
	s, ok := got.(string)
	if !ok {
		t.Fatalf("无图片时 content 应为 string 类型，得到 %T", got)
	}
	if s != "hello" {
		t.Fatalf("content 应为 'hello'，得到 %q", s)
	}
}

// TestToChatContent_EmptyTextNoImage 空文本 + 无图：仍是空字符串（保持纯文本路径）。
func TestToChatContent_EmptyTextNoImage(t *testing.T) {
	got := toChatContent(msgContent{})
	if _, ok := got.(string); !ok {
		t.Fatalf("应回退到 string 类型，得到 %T", got)
	}
}

// TestToChatContent_Multimodal 有图片时 content 变为多模态数组：
// 首元素是 text part，其余为 image_url part，顺序与传入图片顺序一致。
func TestToChatContent_Multimodal(t *testing.T) {
	got := toChatContent(msgContent{
		Text:   "看图",
		Images: []string{"http://x/a.png", "http://x/b.png"},
	})
	parts, ok := got.([]map[string]interface{})
	if !ok {
		t.Fatalf("有图片时应为 []map[string]interface{}，得到 %T", got)
	}
	if len(parts) != 3 { // 1 text + 2 image
		t.Fatalf("应有 3 个 part（1 text + 2 image），得到 %d", len(parts))
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "看图" {
		t.Fatalf("首 part 应为 {type:text text:看图}，得到 %+v", parts[0])
	}
	// 图片顺序保持
	for i, want := range []string{"http://x/a.png", "http://x/b.png"} {
		idx := i + 1
		if parts[idx]["type"] != "image_url" {
			t.Fatalf("part[%d] type 应为 image_url，得到 %v", idx, parts[idx]["type"])
		}
		urlMap, ok := parts[idx]["image_url"].(map[string]string)
		if !ok {
			t.Fatalf("part[%d] image_url 应为 map[string]string，得到 %T", idx, parts[idx]["image_url"])
		}
		if urlMap["url"] != want {
			t.Fatalf("part[%d] url 应为 %s，得到 %s", idx, want, urlMap["url"])
		}
	}
}

// TestExtractJSON 从可能含 markdown/解释性文本中提取首个 JSON 对象。
// 覆盖：纯 JSON、markdown fence、前后有杂文、无 JSON 回退原文。
func TestExtractJSON(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"纯 JSON", `{"a":1,"b":2}`, `{"a":1,"b":2}`},
		{"markdown fence 包裹", "```json\n{\"a\":1}\n```", `{"a":1}`},
		{"前后有解释文字", "回复：\n{\"a\":1}\n（结束）", `{"a":1}`},
		{"无 JSON 回退原文", "纯文本无括号", "纯文本无括号"},
		{"嵌套对象", `prefix {"a":{"b":1}} suffix`, `{"a":{"b":1}}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractJSON(tc.in)
			if got != tc.want {
				t.Fatalf("extractJSON(%q)=%q want=%q", tc.in, got, tc.want)
			}
		})
	}
}

// --- Service 边界测试：不依赖 AI/HTTP 的分支 ---

// TestService_CreateAndList Service 层串起 CreateConversation→ListConversations：
// 建后能从 List 查到、ID 前缀正确；status 的 DB 默认值 'active' 由 ListConversations 透出。
//
// 注意：Service.CreateConversation 当前只回填 ID/ProjectSpaceID，
// 返回对象的 Status/CreatedAt 等字段为空（DB DEFAULT 未回读）——这是一处潜在缺陷，
// handler.Create 把该对象直接返给前端会导致前端看到 status=""。
// 这里以 ListConversations（走 SELECT）的结果为准断言 status，避开该缺陷。
func TestService_CreateAndList(t *testing.T) {
	svc := NewService(newTestStore(t), nil, "http://unused")
	c, err := svc.CreateConversation(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(c.ID, "conv_") {
		t.Fatalf("ID 应有 conv_ 前缀，得到 %s", c.ID)
	}
	// 潜在缺陷：返回对象的 status 未回读，期望保持空（即 buggy 行为）；
	// 修复后改为断言 "active"。
	if c.Status != "" {
		t.Fatalf("当前实现 status 未回读，期望空串；若此断言失败说明已修复（应同步更新断言为 active），得到 %q", c.Status)
	}

	list, err := svc.ListConversations(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].ID != c.ID {
		t.Fatalf("ListConversations 应只含新建 c，得到 %+v", list)
	}
	// 走 SELECT 回读时 status 应为 DB 默认值 active
	if list[0].Status != "active" {
		t.Fatalf("ListConversations 返回的会话 status 应为 active，得到 %s", list[0].Status)
	}
}

// TestService_GetConversation_NotFound 会话不存在 → 直接返回 err（不查消息）。
func TestService_GetConversation_NotFound(t *testing.T) {
	svc := NewService(newTestStore(t), nil, "http://unused")
	conv, msgs, err := svc.GetConversation(context.Background(), "conv_nope")
	if err == nil {
		t.Fatal("会话不存在应返回 err")
	}
	if conv != nil || msgs != nil {
		t.Fatalf("err 时 conv/msgs 都应为 nil，得到 conv=%v msgs=%v", conv, msgs)
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("err 文案应包含 '不存在'，得到 %v", err)
	}
}

// TestService_GetConversation_FullLifecycle 完整生命周期：
// 建会话 → 追加 user/assistant 消息 → GetConversation 返回会话+消息（角色顺序正确）。
// 关键：覆盖 store 层组合 + Service 的 happy path（不调 AI）。
func TestService_GetConversation_FullLifecycle(t *testing.T) {
	store := newTestStore(t)
	svc := NewService(store, nil, "http://unused")

	conv, _ := svc.CreateConversation(context.Background(), "ps_1")
	// 直接通过 store 追加消息，绕开 SendMessage（它会调 AI）
	mustAddMsg(t, store, conv.ID, "user", `{"text":"你好"}`)
	mustAddMsg(t, store, conv.ID, "assistant", `{"text":"请补充"}`)

	got, msgs, err := svc.GetConversation(context.Background(), conv.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != conv.ID {
		t.Fatalf("会话 ID 不匹配：%s", got.ID)
	}
	if len(msgs) != 2 {
		t.Fatalf("应有 2 条消息，得到 %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[1].Role != "assistant" {
		t.Fatalf("角色顺序应为 user,assistant，得到 %s,%s", msgs[0].Role, msgs[1].Role)
	}
}

// TestService_SendMessage_ConvNotFound 会话不存在 → SendMessage 直接返回 err，
// 不会发起 HTTP 调用（cid 不存在分支保护了 agent URL 不可达场景）。
func TestService_SendMessage_ConvNotFound(t *testing.T) {
	svc := NewService(newTestStore(t), nil, "http://unused")
	_, err := svc.SendMessage(context.Background(), "conv_nope", "hi", nil)
	if err == nil {
		t.Fatal("会话不存在应返回 err")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("err 应包含 '不存在'，得到 %v", err)
	}
}

// TestService_SendMessageStream_ConvNotFound 流式变体：
// 会话不存在同样直接返回 err，不建立 HTTP 流。
func TestService_SendMessageStream_ConvNotFound(t *testing.T) {
	svc := NewService(newTestStore(t), nil, "http://unused")
	called := 0
	_, err := svc.SendMessageStream(context.Background(), "conv_nope", "hi", nil,
		func(string) { called++ })
	if err == nil {
		t.Fatal("会话不存在应返回 err")
	}
	if called != 0 {
		t.Fatalf("err 路径不应回调 onChunk，被回调 %d 次", called)
	}
}

// TestService_GenerateSpec_EmptyHistory 会话无消息时 GenerateSpec 直接报错，
// 不会调 AI（避免空历史白调一次 chat）。
func TestService_GenerateSpec_EmptyHistory(t *testing.T) {
	store := newTestStore(t)
	c := mustCreateConv(t, store, "ps_1")
	svc := NewService(store, nil, "http://unused")

	_, err := svc.GenerateSpec(context.Background(), c.ID)
	if err == nil {
		t.Fatal("空历史应返回 err")
	}
	if !strings.Contains(err.Error(), "尚无消息") {
		t.Fatalf("err 应包含 '尚无消息'，得到 %v", err)
	}
}

// TestService_GenerateSpec_ConvWithoutMessages 验证：即便 cid 完全不存在，
// ListMessages 也返回空切片（而非 err），仍会走到「尚无消息」分支。
// 覆盖 store.ListMessages 的 nil-safe 特性。
func TestService_GenerateSpec_ConvWithoutMessages(t *testing.T) {
	svc := NewService(newTestStore(t), nil, "http://unused")
	_, err := svc.GenerateSpec(context.Background(), "conv_never")
	if err == nil {
		t.Fatal("空历史应返回 err")
	}
	if !strings.Contains(err.Error(), "尚无消息") {
		t.Fatalf("err 应包含 '尚无消息'，得到 %v", err)
	}
}

// TestService_Commit_ConvNotFound 会话不存在时 Commit 直接返回 err，
// 不会触及 reqRepo（传 nil 也不会 nil-panic）。
func TestService_Commit_ConvNotFound(t *testing.T) {
	svc := NewService(newTestStore(t), nil, "http://unused")
	_, err := svc.Commit(context.Background(), "conv_nope", &SpecResult{
		Title:              "x",
		UserStory:          "y",
		AcceptanceCriteria: []string{"a"},
	})
	if err == nil {
		t.Fatal("会话不存在应返回 err")
	}
	if !strings.Contains(err.Error(), "不存在") {
		t.Fatalf("err 应包含 '不存在'，得到 %v", err)
	}
}
