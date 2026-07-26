package conversation

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestCreateConv_PersistsUserID CreateConv 写入 user_id；GetConv 经 COALESCE 回读。
func TestCreateConv_PersistsUserID(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "conversation")
	// conversation.project_space_id 有 FK → 先建 project_space（幂等）。
	db.MustExec(`INSERT INTO project_space(id,name,slug) VALUES('ps1','p','ps1') ON CONFLICT (id) DO NOTHING`)

	s := NewStore(db)
	ctx := context.Background()

	c := &Conversation{ProjectSpaceID: "ps1", UserID: "usr_carol"}
	if err := s.CreateConv(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}
	var uid string
	if err := db.Get(&uid, `SELECT user_id FROM conversation WHERE id=$1`, c.ID); err != nil || uid != "usr_carol" {
		t.Fatalf("user_id 应 usr_carol, 得 %q err=%v", uid, err)
	}
	got, err := s.GetConv(ctx, c.ID)
	if err != nil || got.UserID != "usr_carol" {
		t.Fatalf("GetConv 回读 user_id 应 usr_carol, 得 %q err=%v", got.UserID, err)
	}
}
