package performance

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"
)

// Store 绩效聚合数据访问（只读，按需聚合）。
type Store struct{ db *sqlx.DB }

// NewStore 构造。
func NewStore(db *sqlx.DB) *Store { return &Store{db: db} }

// winClause 生成时间窗 AND 子句（?占位）+ 参数；from/to 零值=不加。
func winClause(col string, from, to time.Time) (string, []interface{}) {
	var clause string
	var args []interface{}
	if !from.IsZero() {
		clause += " AND " + col + " >= ?"
		args = append(args, from)
	}
	if !to.IsZero() {
		clause += " AND " + col + " <= ?"
		args = append(args, to)
	}
	return clause, args
}

// rebind 把 ? 占位转为 PG $N。
func rebind(q string) string { return sqlx.Rebind(sqlx.DOLLAR, q) }

// Summary 单人绩效：跨表按 user_id 聚合（[from,to] × project_space）。
func (s *Store) Summary(ctx context.Context, psID, userID string, from, to time.Time) (Profile, error) {
	p := Profile{UserID: userID}
	_ = s.db.GetContext(ctx, &p.UserName, `SELECT COALESCE(name,'') FROM "user" WHERE id=$1`, userID)

	// code_task 完成/失败
	wc, wca := winClause("created_at", from, to)
	var ct struct{ Done, Failed int }
	args := append([]interface{}{psID, userID}, wca...)
	_ = s.db.GetContext(ctx, &ct, rebind(
		`SELECT COUNT(*) FILTER (WHERE status='completed') done,
		        COUNT(*) FILTER (WHERE status='failed') failed
		 FROM code_task WHERE project_space_id=? AND user_id=?`+wc), args...)
	p.Metrics.CodeTaskDone, p.Metrics.CodeTaskFailed = ct.Done, ct.Failed

	// change_request 提交/通过/驳回（作者=user_id）
	var ch struct{ Sub, Appr, Rej int }
	args = append([]interface{}{psID, userID}, wca...)
	_ = s.db.GetContext(ctx, &ch, rebind(
		`SELECT COUNT(*) sub,
		        COUNT(*) FILTER (WHERE status='approved') appr,
		        COUNT(*) FILTER (WHERE status='rejected') rej
		 FROM change_request WHERE project_space_id=? AND user_id=?`+wc), args...)
	p.Metrics.ChangeSubmitted, p.Metrics.ChangeApproved, p.Metrics.ChangeRejected = ch.Sub, ch.Appr, ch.Rej

	// releases（join change 取作者）
	args = append([]interface{}{psID, userID}, wca...)
	_ = s.db.GetContext(ctx, &p.Metrics.Releases, rebind(
		`SELECT COUNT(*) FROM release_record r JOIN change_request c ON c.id=r.change_id
		 WHERE r.project_space_id=? AND c.user_id=?`+winClauseCol("r.created_at", from, to)), args...)

	// conversations 发起数
	args = append([]interface{}{psID, userID}, wca...)
	_ = s.db.GetContext(ctx, &p.Metrics.Conversations, rebind(
		`SELECT COUNT(*) FROM conversation WHERE project_space_id=? AND user_id=?`+wc), args...)

	// requirement 认领/完成（assignee 存 username，按 user 的 name 匹配）
	var rq struct{ Claimed, Done int }
	args = append([]interface{}{psID, userID}, wca...)
	_ = s.db.GetContext(ctx, &rq, rebind(
		`SELECT COUNT(*) claimed, COUNT(*) FILTER (WHERE status IN ('delivered','done')) done
		 FROM requirement WHERE project_space_id=?
		   AND assignee=(SELECT name FROM "user" WHERE id=?)`+winClauseCol("assigned_at", from, to)), args...)
	p.Metrics.ReqClaimed, p.Metrics.ReqCompleted = rq.Claimed, rq.Done

	// 编码工作台互动（codews_session）
	// 注：codews_session.user_id 存的是用户名（与 worktree 命名/requirement.assignee 一致），
	// 非 usr_xxx；故按 name 子查询匹配（与 requirement 认领同法）。
	ws, wsa := winClause("started_at", from, to)
	var wsm struct{ Sess, Prompts, Secs int }
	args = append([]interface{}{psID, userID}, wsa...)
	_ = s.db.GetContext(ctx, &wsm, rebind(
		`SELECT COUNT(*) sess,
		        COALESCE(SUM(prompt_count),0) prompts,
		        COALESCE(SUM(EXTRACT(EPOCH FROM (COALESCE(ended_at,NOW())-started_at))::int),0) secs
		 FROM codews_session WHERE project_space_id=? AND user_id=(SELECT name FROM "user" WHERE id=?)`+ws), args...)
	p.Metrics.WsSessions, p.Metrics.WsPrompts, p.Metrics.WsSeconds = wsm.Sess, wsm.Prompts, wsm.Secs

	// 最近会话列表（最近 20）
	args = []interface{}{psID, userID}
	_ = s.db.SelectContext(ctx, &p.Sessions, rebind(
		`SELECT id, tool, repo_dir, started_at, ended_at, prompt_count
		 FROM codews_session WHERE project_space_id=? AND user_id=(SELECT name FROM "user" WHERE id=?)
		 ORDER BY started_at DESC LIMIT 20`), args...)
	return p, nil
}

// winClauseCol 同 winClause（用于不同列名的查询，避免复用同一 wc 字符串误拼）。
func winClauseCol(col string, from, to time.Time) string {
	c, _ := winClause(col, from, to)
	return c
}

// Members 全员绩效 + 未归属桶（admin 用）。
func (s *Store) Members(ctx context.Context, psID string, from, to time.Time) ([]Profile, error) {
	// 空间内出现过的 user_id 并集（成员 + 各工作表）
	var ids []struct {
		UID string `db:"uid"`
	}
	// 空间内出现过的 user_id 并集（成员 + 各工作表；codews_session.user_id 是用户名不在此并集，
	// 工作台用户必为空间成员，经 membership 覆盖，其会话由 Summary 按 name 匹配挂载）
	q := `SELECT user_id uid FROM membership WHERE project_space_id=? AND user_id IS NOT NULL AND user_id <> ''
	      UNION SELECT user_id FROM code_task WHERE project_space_id=? AND user_id IS NOT NULL AND user_id <> ''
	      UNION SELECT user_id FROM change_request WHERE project_space_id=? AND user_id IS NOT NULL AND user_id <> ''
	      UNION SELECT user_id FROM conversation WHERE project_space_id=? AND user_id IS NOT NULL AND user_id <> ''`
	if err := s.db.SelectContext(ctx, &ids, rebind(q), psID, psID, psID, psID); err != nil {
		return nil, err
	}
	out := make([]Profile, 0, len(ids)+1)
	for _, id := range ids {
		p, _ := s.Summary(ctx, psID, id.UID, from, to)
		out = append(out, p)
	}
	// 未归属桶（历史 user_id 为 NULL 的行）
	if u, _ := s.unassignedSummary(ctx, psID, from, to); u.Metrics.CodeTaskDone+u.Metrics.WsSessions+u.Metrics.ChangeSubmitted+u.Metrics.Conversations > 0 {
		u.IsUnassigned = true
		out = append(out, u)
	}
	return out, nil
}

// unassignedSummary 历史 user_id 为空的行桶。
func (s *Store) unassignedSummary(ctx context.Context, psID string, from, to time.Time) (Profile, error) {
	p := Profile{UserName: "未归属（历史无 user_id）"}
	wc, wca := winClause("created_at", from, to)
	var ct struct{ Done, Failed int }
	args := append([]interface{}{psID}, wca...)
	_ = s.db.GetContext(ctx, &ct, rebind(
		`SELECT COUNT(*) FILTER (WHERE status='completed') done, COUNT(*) FILTER (WHERE status='failed') failed
		 FROM code_task WHERE project_space_id=? AND user_id IS NULL`+wc), args...)
	p.Metrics.CodeTaskDone, p.Metrics.CodeTaskFailed = ct.Done, ct.Failed

	var ch struct{ Sub, Appr, Rej int }
	args = append([]interface{}{psID}, wca...)
	_ = s.db.GetContext(ctx, &ch, rebind(
		`SELECT COUNT(*) sub, COUNT(*) FILTER (WHERE status='approved') appr, COUNT(*) FILTER (WHERE status='rejected') rej
		 FROM change_request WHERE project_space_id=? AND user_id IS NULL`+wc), args...)
	p.Metrics.ChangeSubmitted, p.Metrics.ChangeApproved, p.Metrics.ChangeRejected = ch.Sub, ch.Appr, ch.Rej

	args = append([]interface{}{psID}, wca...)
	_ = s.db.GetContext(ctx, &p.Metrics.Conversations, rebind(
		`SELECT COUNT(*) FROM conversation WHERE project_space_id=? AND user_id IS NULL`+wc), args...)

	ws, wsa := winClause("started_at", from, to)
	var wsm struct{ Sess, Prompts int }
	args = append([]interface{}{psID}, wsa...)
	_ = s.db.GetContext(ctx, &wsm, rebind(
		`SELECT COUNT(*) sess, COALESCE(SUM(prompt_count),0) FROM codews_session WHERE project_space_id=? AND user_id IS NULL`+ws), args...)
	p.Metrics.WsSessions, p.Metrics.WsPrompts = wsm.Sess, wsm.Prompts
	return p, nil
}

// SessionByID 取单条 codews_session（handler 下钻聊天记录：拿 tool/repo_dir/session_id）。
func (s *Store) SessionByID(ctx context.Context, id string) (SessionRow, error) {
	var r SessionRow
	err := s.db.GetContext(ctx, &r,
		`SELECT id, app_id, COALESCE(user_id,'') AS user_id, tool, repo_dir, COALESCE(session_id,'') AS session_id
		 FROM codews_session WHERE id=$1`, id)
	return r, err
}
