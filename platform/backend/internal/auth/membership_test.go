package auth

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestListMembers_NameJoin 成员列表 JOIN user 返回 name（供派发选人显示/指派）。
func TestListMembers_NameJoin(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "membership", `"user"`)
	// FK 前置：membership 引用 project_space；migrate 已建表，补一个 ps_default。
	db.MustExec(`INSERT INTO project_space (id, name, slug) VALUES ('ps_m', 'm', 'm')
		ON CONFLICT (id) DO NOTHING`)
	db.MustExec(`INSERT INTO "user" (id, name, email, status) VALUES ('usr_alice', 'alice', '', 'active')
		ON CONFLICT (id) DO NOTHING`)
	db.MustExec(`INSERT INTO membership (id, project_space_id, user_id, role)
		VALUES ('mbr_1', 'ps_m', 'usr_alice', 'dev')`)

	s := NewStore(db)
	list, err := s.ListMembers(context.Background(), "ps_m")
	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(list) != 1 || list[0].Name != "alice" {
		t.Fatalf("期望 1 个成员 name=alice，得到: %#v", list)
	}
}
