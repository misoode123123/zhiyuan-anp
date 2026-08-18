package notif

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

func newTestStore(t *testing.T) (*Store, *sqlx.DB) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "notification")
	return NewStore(db), db
}

// TestCreate_EmptyUserNormalizedAsNull 空 UserID 必须归一为 NULL（广播语义）。
// 空串行既不进 List 也不进 MarkAllRead，会变成永不可读的幽灵未读（.28 实况：
// Emit("") 落了 16 行空串，UnreadCount 不计但也不可见、清不掉）。
func TestCreate_EmptyUserNormalizedAsNull(t *testing.T) {
	s, _ := newTestStore(t)
	empty := ""
	n := &Notification{UserID: &empty, Type: "system", Title: "广播"}
	if err := s.Create(context.Background(), n); err != nil {
		t.Fatal(err)
	}
	var nulls int
	if err := s.db.Get(&nulls, `SELECT COUNT(*) FROM notification WHERE user_id IS NULL`); err != nil {
		t.Fatal(err)
	}
	if nulls != 1 {
		t.Fatalf("空 UserID 应归一为 NULL 落库, got %d 行", nulls)
	}
	// 归一后广播通知对该用户可见
	list, err := s.List(context.Background(), "admin", false, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("广播通知应可见: len=%d err=%v", len(list), err)
	}
}

// TestMarkAllRead_CoversBroadcast 全部已读必须覆盖广播行（user_id IS NULL）——
// List/UnreadCount 都把 NULL 行算给用户，漏掉则广播通知永远无法批量已读。
func TestMarkAllRead_CoversBroadcast(t *testing.T) {
	s, _ := newTestStore(t)
	uid := "admin"
	for i := 0; i < 2; i++ {
		if err := s.Create(context.Background(), &Notification{UserID: &uid, Type: "system", Title: "点对点"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Create(context.Background(), &Notification{Type: "system", Title: "广播"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(context.Background(), uid); n != 3 {
		t.Fatalf("未读应 3(点对点2+广播1), got %d", n)
	}
	if err := s.MarkAllRead(context.Background(), uid); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(context.Background(), uid); n != 0 {
		t.Fatalf("全部已读后未读应 0(广播行也被覆盖), got %d", n)
	}
}
