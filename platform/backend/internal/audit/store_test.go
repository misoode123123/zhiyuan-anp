package audit

import (
	"context"
	"strings"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestStore_CreateAndQuery 写一条审计 + 按 actor/action 查回。
func TestStore_CreateAndQuery(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "operation_log")
	s := NewStore(db)
	ctx := context.Background()

	err := s.CreateDetail(ctx, "user", "u1", "app.deploy", "app", "app_x", "ps1", "tr1", "success", "",
		map[string]interface{}{"env": "test", "version": 3})
	if err != nil {
		t.Fatalf("CreateDetail: %v", err)
	}

	// 按 actor_id 查
	list, err := s.Query(ctx, "", "u1", "", "", "", 10, 0)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("应查到 1 条，得到 %d", len(list))
	}
	got := list[0]
	if got.Action != "app.deploy" || got.ResourceID != "app_x" || got.Status != "success" {
		t.Fatalf("字段不符: %+v", got)
	}
	if got.Detail == nil || !strings.Contains(*got.Detail, `"env"`) {
		t.Fatalf("detail 应含 env，得到 %v", got.Detail)
	}

	// 按 action 过滤
	if list2, _ := s.Query(ctx, "", "", "app.deploy", "", "", 10, 0); len(list2) != 1 {
		t.Fatalf("action=app.deploy 应 1 条，得到 %d", len(list2))
	}
	// 不匹配 action 应 0 条
	if list3, _ := s.Query(ctx, "", "", "app.delete", "", "", 10, 0); len(list3) != 0 {
		t.Fatalf("action=app.delete 应 0 条，得到 %d", len(list3))
	}
}
