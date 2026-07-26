package codetask

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestCreate_PersistsUserID Create 写入 user_id；空则落 NULL；Get 经 COALESCE 回读。
func TestCreate_PersistsUserID(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "code_task")
	s := NewStore(db)
	ctx := context.Background()

	t1 := &Task{ID: "ct_a", ProjectSpaceID: "ps1", UserID: "usr_alice", Kind: "code", Prompt: "p"}
	if err := s.Create(ctx, t1); err != nil {
		t.Fatalf("create t1: %v", err)
	}
	var uid string
	if err := db.Get(&uid, `SELECT user_id FROM code_task WHERE id='ct_a'`); err != nil || uid != "usr_alice" {
		t.Fatalf("user_id 应 usr_alice, 得 %q err=%v", uid, err)
	}

	// 空 user_id → NULL（绩效"未归属"桶按 IS NULL 判定）
	t2 := &Task{ID: "ct_b", ProjectSpaceID: "ps1", Kind: "code", Prompt: "p"}
	if err := s.Create(ctx, t2); err != nil {
		t.Fatalf("create t2: %v", err)
	}
	var nullable *string
	if err := db.Get(&nullable, `SELECT user_id FROM code_task WHERE id='ct_b'`); err != nil || nullable != nil {
		t.Fatalf("空 user_id 应 NULL, 得 %v err=%v", nullable, err)
	}

	got, err := s.Get(ctx, "ct_a")
	if err != nil || got.UserID != "usr_alice" {
		t.Fatalf("Get 回读 user_id 应 usr_alice, 得 %q err=%v", got.UserID, err)
	}
}
