package change

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestCreate_PersistsUserID Create 写入 user_id；Get 经 chgCols 的 COALESCE 回读。
func TestCreate_PersistsUserID(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "change_request")
	s := NewStore(db)
	ctx := context.Background()

	c1 := &ChangeRequest{ProjectSpaceID: "ps1", UserID: "usr_bob", Kind: "code", Output: "diff"}
	if err := s.Create(ctx, c1); err != nil {
		t.Fatalf("create: %v", err)
	}
	var uid string
	if err := db.Get(&uid, `SELECT user_id FROM change_request WHERE id=$1`, c1.ID); err != nil || uid != "usr_bob" {
		t.Fatalf("user_id 应 usr_bob, 得 %q err=%v", uid, err)
	}
	got, err := s.Get(ctx, c1.ID)
	if err != nil || got.UserID != "usr_bob" {
		t.Fatalf("Get 回读 user_id 应 usr_bob, 得 %q err=%v", got.UserID, err)
	}
}
