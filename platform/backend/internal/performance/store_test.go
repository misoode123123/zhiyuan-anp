package performance

import (
	"context"
	"testing"
	"time"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "code_task", "change_request", "release_record", "conversation",
		"codews_session", "membership", `"user"`)
	return NewStore(db)
}

func TestSummary_AggregatesByUser(t *testing.T) {
	s := newTestStore(t)
	db := s.db
	db.MustExec(`INSERT INTO "user"(id,name) VALUES('usr_a','alice')`)
	db.MustExec(`INSERT INTO code_task(id,project_space_id,user_id,status) VALUES('ct1','ps1','usr_a','completed')`)
	db.MustExec(`INSERT INTO code_task(id,project_space_id,user_id,status) VALUES('ct2','ps1','usr_a','failed')`)
	db.MustExec(`INSERT INTO code_task(id,project_space_id,user_id,status) VALUES('ct3','ps1',NULL,'completed')`)
	db.MustExec(`INSERT INTO change_request(id,project_space_id,user_id,status) VALUES('ch1','ps1','usr_a','approved')`)
	db.MustExec(`INSERT INTO change_request(id,project_space_id,user_id,status) VALUES('ch2','ps1','usr_a','rejected')`)
	// codews_session.user_id 存用户名（与 worktree/requirement.assignee 一致），非 usr_xxx
	db.MustExec(`INSERT INTO codews_session(id,project_space_id,app_id,user_id,tool,repo_dir,prompt_count,ended_at)
	            VALUES('cws1','ps1','app1','alice','claude','/r',3,NOW())`)

	p, err := s.Summary(context.Background(), "ps1", "usr_a", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	if p.UserName != "alice" {
		t.Fatalf("UserName 应 alice, 得 %q", p.UserName)
	}
	if p.Metrics.CodeTaskDone != 1 || p.Metrics.CodeTaskFailed != 1 {
		t.Fatalf("code_task 计数错: %+v", p.Metrics)
	}
	if p.Metrics.ChangeSubmitted != 2 || p.Metrics.ChangeApproved != 1 || p.Metrics.ChangeRejected != 1 {
		t.Fatalf("change 计数错: %+v", p.Metrics)
	}
	if p.Metrics.WsSessions != 1 || p.Metrics.WsPrompts != 3 {
		t.Fatalf("互动计数错: %+v", p.Metrics)
	}
}

func TestMembers_UnassignedBucket(t *testing.T) {
	s := newTestStore(t)
	s.db.MustExec(`INSERT INTO code_task(id,project_space_id,user_id,status) VALUES('ct_x','ps1',NULL,'completed')`)
	list, err := s.Members(context.Background(), "ps1", time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	var hasUnassigned bool
	for _, p := range list {
		if p.IsUnassigned {
			hasUnassigned = true
			if p.Metrics.CodeTaskDone != 1 {
				t.Fatalf("未归属桶 code_task done 应 1, 得 %d", p.Metrics.CodeTaskDone)
			}
		}
	}
	if !hasUnassigned {
		t.Fatal("应含未归属桶")
	}
}
