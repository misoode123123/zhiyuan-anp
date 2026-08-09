package codews

import (
	"context"
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
