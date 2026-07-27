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
