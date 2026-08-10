package codews

import (
	"context"
	"strings"
	"testing"
	"time"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestPGSessionStore_Lifecycle StartSession 落行（生成 id）→ FinishSession 回填 ended_at + 计数。
func TestPGSessionStore_Lifecycle(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "codews_session")
	store := NewPGSessionStore(db)
	ctx := context.Background()

	rec := &SessionRecord{ProjectSpaceID: "ps1", AppID: "app1", UserID: "usr_a", Tool: "claude", RepoDir: "/r"}
	if err := store.StartSession(ctx, rec); err != nil {
		t.Fatalf("start: %v", err)
	}
	if rec.ID == "" {
		t.Fatal("应生成 id")
	}
	if err := store.FinishSession(ctx, rec.ID, SessionCounts{PromptCount: 3, MessageCount: 7}); err != nil {
		t.Fatalf("finish: %v", err)
	}
	var ended *time.Time
	var pc int
	if err := db.QueryRowx(`SELECT ended_at, prompt_count FROM codews_session WHERE id=$1`, rec.ID).Scan(&ended, &pc); err != nil {
		t.Fatalf("query: %v", err)
	}
	if ended == nil {
		t.Fatal("ended_at 应已回填")
	}
	if pc != 3 {
		t.Fatalf("prompt_count 应=3, 得 %d", pc)
	}
}

// TestStartSession_PersistsRequirementID 绑定需求：StartSession 把 requirement_id 落库。
func TestStartSession_PersistsRequirementID(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "codews_session")
	store := NewPGSessionStore(db)
	ctx := context.Background()

	rec := &SessionRecord{
		ProjectSpaceID: "ps_t", AppID: "app_t", UserID: "usr_alice",
		Tool: "opencode", RepoDir: "/repo", Port: 9401,
		RequirementID: "req_123",
	}
	if err := store.StartSession(ctx, rec); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	var got string
	if err := db.GetContext(ctx, &got,
		`SELECT requirement_id FROM codews_session WHERE id=$1`, rec.ID); err != nil {
		t.Fatalf("query: %v", err)
	}
	if got != "req_123" {
		t.Fatalf("requirement_id want req_123 got %q", got)
	}
}

func TestPGSessionStore_LastSession(t *testing.T) {
	db := testutil.TestDB(t)
	store := NewPGSessionStore(db)
	ctx := context.Background()
	const ps, app, user = "ps_l", "app_l", "u_l"

	// 插两条同 (ps,app,user)、不同 requirement_id 的会话；把第一条 started_at 调早，
	// 保证 ORDER BY started_at DESC 确定性地返回第二条（避免微秒级同戳歧义）。
	rA := &SessionRecord{ProjectSpaceID: ps, AppID: app, UserID: user, Tool: "opencode", RepoDir: "/r", Port: 1, RequirementID: "reqA"}
	if err := store.StartSession(ctx, rA); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `UPDATE codews_session SET started_at = started_at - INTERVAL '1 hour' WHERE id=$1`, rA.ID); err != nil {
		t.Fatal(err)
	}
	rB := &SessionRecord{ProjectSpaceID: ps, AppID: app, UserID: user, Tool: "opencode", RepoDir: "/r", Port: 2, RequirementID: "reqB"}
	if err := store.StartSession(ctx, rB); err != nil {
		t.Fatal(err)
	}

	got, err := store.LastSession(ctx, ps, app, user)
	if err != nil || got == nil {
		t.Fatalf("LastSession 欲返回最近会话，got=%v err=%v", got, err)
	}
	if got.RequirementID != "reqB" {
		t.Fatalf("LastSession 应返回最新(reqB)，实得 %s", got.RequirementID)
	}

	// 无历史 → (nil, nil)，非错误
	got2, err := store.LastSession(ctx, ps, app, "nobody")
	if err != nil || got2 != nil {
		t.Fatalf("无历史应返回 (nil,nil)，got=%v err=%v", got2, err)
	}
}

// TestPGSessionStore_SaveMessages 对话快照落库：seed→按 seq 读回→重复 upsert 幂等不翻倍→
// 追加新 seq 只增量→超长 content 截断。模拟后台 ticker 周期性全量重拉 LiveTranscript 的场景。
func TestPGSessionStore_SaveMessages(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "codews_session")
	testutil.Truncate(t, db, "codews_message")
	store := NewPGSessionStore(db)
	ctx := context.Background()

	rec := &SessionRecord{ProjectSpaceID: "ps_m", AppID: "app_m", UserID: "usr_m", Tool: "opencode", RepoDir: "/r"}
	if err := store.StartSession(ctx, rec); err != nil {
		t.Fatalf("StartSession: %v", err)
	}

	// 首轮快照：两条消息（seq=0,1）→ 按 seq 升序读回内容/角色一致
	msgs := []TranscriptMsg{
		{Role: "user", Content: "帮我加登录页"},
		{Role: "assistant", Content: "已加 login.tsx"},
	}
	if err := store.SaveMessages(ctx, rec.ID, msgs); err != nil {
		t.Fatalf("SaveMessages: %v", err)
	}
	got, err := store.Messages(ctx, rec.ID)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(got) != 2 || got[0].Role != "user" || got[0].Content != "帮我加登录页" || got[1].Role != "assistant" {
		t.Fatalf("读回不符: %+v", got)
	}

	// 幂等：重复存同两条（全量重拉）→ ON CONFLICT (session_id,seq) DO NOTHING，不应翻倍
	if err := store.SaveMessages(ctx, rec.ID, msgs); err != nil {
		t.Fatalf("SaveMessages 重存: %v", err)
	}
	if got2, _ := store.Messages(ctx, rec.ID); len(got2) != 2 {
		t.Fatalf("幂等失败: 重复存后应仍 2 条，得 %d", len(got2))
	}

	// 追加：次轮快照多一条（seq=2）→ 前两条冲突跳过、仅新增 1 条，共 3 条且顺序正确
	msgs3 := append(msgs, TranscriptMsg{Role: "user", Content: "再加注册页"})
	if err := store.SaveMessages(ctx, rec.ID, msgs3); err != nil {
		t.Fatalf("SaveMessages 追加: %v", err)
	}
	if got3, _ := store.Messages(ctx, rec.ID); len(got3) != 3 || got3[2].Content != "再加注册页" {
		t.Fatalf("追加不符: %+v", got3)
	}

	// 截断：超 maxMessageContent 的 content 被裁为 maxMessageContent + 截断标记。
	// 用独立 session（seq=0 不与上面冲突）验证。
	recBig := &SessionRecord{ProjectSpaceID: "ps_m", AppID: "app_big", UserID: "usr_m", Tool: "opencode", RepoDir: "/rb"}
	if err := store.StartSession(ctx, recBig); err != nil {
		t.Fatalf("StartSession big: %v", err)
	}
	big := strings.Repeat("x", maxMessageContent+500)
	if err := store.SaveMessages(ctx, recBig.ID, []TranscriptMsg{{Role: "user", Content: big}}); err != nil {
		t.Fatalf("SaveMessages 截断: %v", err)
	}
	var stored string
	if err := db.GetContext(ctx, &stored,
		`SELECT content FROM codews_message WHERE session_id=$1 ORDER BY seq LIMIT 1`, recBig.ID); err != nil {
		t.Fatalf("query big: %v", err)
	}
	if len(stored) >= len(big) {
		t.Fatalf("应被截断: stored=%d >= big=%d", len(stored), len(big))
	}
	if !strings.HasSuffix(stored, "已截断)") {
		t.Fatalf("应以截断标记结尾: 末尾=%q", stored[len(stored)-16:])
	}

	// 无消息会话 → Messages 返空切片（非错误），handler 据此降级 live/磁盘兜底
	empty, err := store.Messages(ctx, "cws_nope")
	if err != nil || len(empty) != 0 {
		t.Fatalf("无消息应返空切片, got=%v err=%v", empty, err)
	}
	// 空 msgs 入参 → SaveMessages 直接 nil 返回（不报错、不落行）
	if err := store.SaveMessages(ctx, rec.ID, nil); err != nil {
		t.Fatalf("空入参 SaveMessages 应 nil: %v", err)
	}
}
