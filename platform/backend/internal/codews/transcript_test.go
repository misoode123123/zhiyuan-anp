package codews

import "testing"

// TestFileReader_ClaudeMessages 按 cwd 过滤 + content 双形态归一化 + 噪音行(mode/snapshot)跳过。
func TestFileReader_ClaudeMessages(t *testing.T) {
	r := fileReader{root: "testdata/claude", tool: "claude"}
	msgs, err := r.Messages("/repo/app", "s1")
	if err != nil {
		t.Fatalf("messages: %v", err)
	}
	if len(msgs) != 3 { // u1 + a1 + u2（/other 的 s2 排除；mode/snapshot 跳过）
		t.Fatalf("期望 3 条, 得 %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "实现登录" {
		t.Fatalf("首条: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "好的，已加登录页" {
		t.Fatalf("assistant 数组 content 未归一化: %+v", msgs[1])
	}
}

func TestFileReader_ClaudeSessions(t *testing.T) {
	r := fileReader{root: "testdata/claude", tool: "claude"}
	metas, err := r.Sessions("/repo/app")
	if err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if len(metas) != 1 || metas[0].SessionID != "s1" {
		t.Fatalf("/repo/app 应只 s1, 得 %+v", metas)
	}
	if metas[0].PromptCount != 2 {
		t.Fatalf("s1 prompt_count 应 2, 得 %d", metas[0].PromptCount)
	}
}

// TestFileReader_CodexMessages codex 顶层 role/content(input_text/output_text) 形态。
// 注：codex 工具暂未接入(③遗留)，reader 用 fixture 验证解析；真实 schema 待 codex 接入后核对。
func TestFileReader_CodexMessages(t *testing.T) {
	r := fileReader{root: "testdata/codex", tool: "codex"}
	msgs, _ := r.Messages("/repo/app", "r1")
	if len(msgs) != 2 {
		t.Fatalf("codex 期望 2 条, 得 %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "写脚本" {
		t.Fatalf("codex 首条: %+v", msgs[0])
	}
	if msgs[1].Role != "assistant" || msgs[1].Content != "已写" {
		t.Fatalf("codex assistant: %+v", msgs[1])
	}
}

func TestReaderFor_AllToolsAndUnknown(t *testing.T) {
	// 三工具均走文件读取：opencode 直读 opencode.db，claude/codex 读磁盘 .jsonl
	if ReaderFor("opencode") == nil {
		t.Fatal("opencode 应返回 reader（直读 opencode.db）")
	}
	if ReaderFor("claude") == nil || ReaderFor("codex") == nil {
		t.Fatal("claude/codex 应返回 reader")
	}
	if ReaderFor("unknown-tool") != nil {
		t.Fatal("未知工具应 nil")
	}
}
