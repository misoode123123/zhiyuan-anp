package conversation

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestStore 建内存 SQLite + conversation/message 两表（自包含，仿 change/store_test.go 模式）。
// 类型映射：PG TIMESTAMP→SQLite DATETIME，TEXT→TEXT；默认值与 PG schema 对齐。
// 两张表 DDL 对齐 internal/db/migrations/pg/000001_init.up.sql。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	db, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	db.MustExec(`CREATE TABLE conversation (
  id               TEXT PRIMARY KEY,
  project_space_id TEXT NOT NULL,
  status           TEXT NOT NULL DEFAULT 'active',
  title            TEXT,
  requirement_id   TEXT,
  created_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at       DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	db.MustExec(`CREATE TABLE message (
  id              TEXT PRIMARY KEY,
  conversation_id TEXT NOT NULL REFERENCES conversation(id),
  role            TEXT NOT NULL,
  content         TEXT NOT NULL,
  media_kind      TEXT NOT NULL DEFAULT 'text',
  created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP)`)
	return NewStore(db)
}

// mustCreateConv 创建一个会话，失败即 t.Fatal。
func mustCreateConv(t *testing.T, s *Store, psID string) *Conversation {
	t.Helper()
	c := &Conversation{ProjectSpaceID: psID}
	if err := s.CreateConv(context.Background(), c); err != nil {
		t.Fatalf("create conv: %v", err)
	}
	return c
}

// mustAddMsg 追加一条消息，失败即 t.Fatal。
func mustAddMsg(t *testing.T, s *Store, cid, role, content string) *Message {
	t.Helper()
	m := &Message{ConversationID: cid, Role: role, Content: content, MediaKind: "text"}
	if err := s.AddMessage(context.Background(), m); err != nil {
		t.Fatalf("add msg: %v", err)
	}
	return m
}

// setCreatedAt 显式覆盖 created_at。
// 原因：sqlite CURRENT_TIMESTAMP 精度到秒，连续 INSERT 会拿到同值，
// 导致 ORDER BY created_at 顺序不确定；测试中需要稳定的排序断言。
func setCreatedAt(t *testing.T, s *Store, table, id string, ts time.Time) {
	t.Helper()
	res, err := s.db.ExecContext(context.Background(),
		`UPDATE `+table+` SET created_at=? WHERE id=?`,
		ts.Format("2006-01-02 15:04:05"), id)
	if err != nil {
		t.Fatalf("set created_at: %v", err)
	}
	n, _ := res.RowsAffected()
	if n != 1 {
		t.Fatalf("set created_at 影响行数异常：%d（id=%s）", n, id)
	}
}

// TestCreateConvAndGetAndDefaults 建会话→读回，校验默认 status=active、ID 前缀、
// 可空字段为 nil、时间戳被填充。
func TestCreateConvAndGetAndDefaults(t *testing.T) {
	s := newTestStore(t)
	c := mustCreateConv(t, s, "ps_1")

	if !strings.HasPrefix(c.ID, "conv_") {
		t.Fatalf("ID 应有 conv_ 前缀，得到 %s", c.ID)
	}
	got, err := s.GetConv(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("get conv: %v", err)
	}
	if got.Status != "active" {
		t.Fatalf("新建会话 status 应为 active，得到 %s", got.Status)
	}
	if got.ProjectSpaceID != "ps_1" {
		t.Fatalf("ProjectSpaceID 不匹配：%s", got.ProjectSpaceID)
	}
	if got.Title != nil || got.RequirementID != nil {
		t.Fatalf("新建会话 Title/RequirementID 应为 nil，得到 title=%v reqID=%v", got.Title, got.RequirementID)
	}
	if got.CreatedAt.IsZero() {
		t.Fatal("CreatedAt 应被 DEFAULT 填充")
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt 应被 DEFAULT 填充")
	}
}

// TestGetConv_NotFound 未存在的 ID：返回 err 且 Conversation.ID 为空。
// 这是 Service.GetConversation 判定「会话不存在」的依据。
func TestGetConv_NotFound(t *testing.T) {
	s := newTestStore(t)
	got, err := s.GetConv(context.Background(), "conv_nope")
	if err == nil {
		t.Fatal("未找到应返回 err（sql.ErrNoRows）")
	}
	if got == nil {
		t.Fatal("应返回非 nil 的零值指针供调用方判 ID")
	}
	if got.ID != "" {
		t.Fatalf("未找到时 ID 应为空字符串，得到 %s", got.ID)
	}
}

// TestListConvByPS_OrderedAndScoped 列表按 created_at DESC，
// 且只返回本 project_space_id 的会话（隔离性）。
func TestListConvByPS_OrderedAndScoped(t *testing.T) {
	s := newTestStore(t)
	c1 := mustCreateConv(t, s, "ps_1")
	c2 := mustCreateConv(t, s, "ps_1")
	c3 := mustCreateConv(t, s, "ps_2")

	// 显式错开时间戳：c1 早、c2 晚（DESC 期望 c2 在前）
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	setCreatedAt(t, s, "conversation", c1.ID, base)
	setCreatedAt(t, s, "conversation", c2.ID, base.Add(time.Minute))

	got, err := s.ListConvByPS(context.Background(), "ps_1")
	if err != nil {
		t.Fatalf("list ps_1: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ps_1 应有 2 个会话，得到 %d", len(got))
	}
	if got[0].ID != c2.ID || got[1].ID != c1.ID {
		t.Fatalf("顺序应为 c2,c1（created_at DESC），得到 %s,%s", got[0].ID, got[1].ID)
	}

	// 跨空间隔离
	other, _ := s.ListConvByPS(context.Background(), "ps_2")
	if len(other) != 1 || other[0].ID != c3.ID {
		t.Fatalf("ps_2 应只含 c3，得到 %+v", other)
	}
}

// TestListConvByPS_Empty 没有会话的空间应返回 0 条（非 err）。
func TestListConvByPS_Empty(t *testing.T) {
	s := newTestStore(t)
	got, err := s.ListConvByPS(context.Background(), "ps_empty")
	if err != nil {
		t.Fatalf("list empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("空空间应返回 0 条，得到 %d: %+v", len(got), got)
	}
}

// TestSubmitConv_FlipStatusAndBackfill 提交后 status=submitted，
// title 与 requirement_id 被回填（指针非 nil），updated_at 被刷新。
func TestSubmitConv_FlipStatusAndBackfill(t *testing.T) {
	s := newTestStore(t)
	c := mustCreateConv(t, s, "ps_1")
	before, _ := s.GetConv(context.Background(), c.ID)

	if err := s.SubmitConv(context.Background(), c.ID, "登录改造", "req_xyz"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	got, _ := s.GetConv(context.Background(), c.ID)
	if got.Status != "submitted" {
		t.Fatalf("status 应翻转未 submitted，得到 %s", got.Status)
	}
	if got.Title == nil || *got.Title != "登录改造" {
		t.Fatalf("Title 应回填为 '登录改造'，得到 %v", got.Title)
	}
	if got.RequirementID == nil || *got.RequirementID != "req_xyz" {
		t.Fatalf("RequirementID 应回填为 'req_xyz'，得到 %v", got.RequirementID)
	}
	// updated_at 应被 CURRENT_TIMESTAMP 刷新（允许 >=）
	if got.UpdatedAt.Before(before.UpdatedAt) {
		t.Fatalf("updated_at 应被刷新，before=%v after=%v", before.UpdatedAt, got.UpdatedAt)
	}
}

// TestAddMessage_AndListByConv 追加多条消息→ListMessages 按时间正序读回；
// 校验角色 user/assistant 顺序、ID 前缀、media_kind 默认值。
func TestAddMessage_AndListByConv(t *testing.T) {
	s := newTestStore(t)
	c := mustCreateConv(t, s, "ps_1")

	m1 := mustAddMsg(t, s, c.ID, "user", `{"text":"你好"}`)
	m2 := mustAddMsg(t, s, c.ID, "assistant", `{"text":"请描述场景"}`)
	m3 := mustAddMsg(t, s, c.ID, "user", `{"text":"移动端"}`)

	if !strings.HasPrefix(m1.ID, "msg_") {
		t.Fatalf("消息 ID 应有 msg_ 前缀，得到 %s", m1.ID)
	}
	// 显式错开时间戳保证排序稳定（user→assistant→user）
	base := time.Date(2024, 1, 1, 10, 0, 0, 0, time.UTC)
	setCreatedAt(t, s, "message", m1.ID, base)
	setCreatedAt(t, s, "message", m2.ID, base.Add(time.Minute))
	setCreatedAt(t, s, "message", m3.ID, base.Add(2*time.Minute))

	got, err := s.ListMessages(context.Background(), c.ID)
	if err != nil {
		t.Fatalf("list msgs: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("应有 3 条消息，得到 %d", len(got))
	}
	if got[0].ID != m1.ID || got[1].ID != m2.ID || got[2].ID != m3.ID {
		t.Fatalf("顺序应为 m1,m2,m3（created_at ASC），得到 %s,%s,%s",
			got[0].ID, got[1].ID, got[2].ID)
	}
	if got[0].Role != "user" || got[1].Role != "assistant" || got[2].Role != "user" {
		t.Fatalf("角色序列应为 user,assistant,user，得到 %s,%s,%s",
			got[0].Role, got[1].Role, got[2].Role)
	}
	for i, m := range got {
		if m.MediaKind != "text" {
			t.Fatalf("msg[%d] MediaKind 应为 text，得到 %s", i, m.MediaKind)
		}
		if m.ConversationID != c.ID {
			t.Fatalf("msg[%d] ConversationID 不匹配：%s", i, m.ConversationID)
		}
	}
}

// TestListMessages_EmptyAndIsolated 空会话返回 0 条；
// 同空间两个会话的消息互不串台（conversation_id 过滤生效）。
func TestListMessages_EmptyAndIsolated(t *testing.T) {
	s := newTestStore(t)
	c1 := mustCreateConv(t, s, "ps_1")
	c2 := mustCreateConv(t, s, "ps_1")

	// 空会话
	if got, _ := s.ListMessages(context.Background(), c1.ID); len(got) != 0 {
		t.Fatalf("空会话应有 0 条消息，得到 %d", len(got))
	}

	mustAddMsg(t, s, c2.ID, "user", `{"text":"hi"}`)
	mustAddMsg(t, s, c2.ID, "assistant", `{"text":"hello"}`)

	// c1 仍然为空（不串）
	if got, _ := s.ListMessages(context.Background(), c1.ID); len(got) != 0 {
		t.Fatalf("c1 不应看到 c2 的消息，得到 %d", len(got))
	}
	// c2 有 2 条
	got, _ := s.ListMessages(context.Background(), c2.ID)
	if len(got) != 2 {
		t.Fatalf("c2 应有 2 条消息，得到 %d", len(got))
	}
}

// TestAddMessage_ConversationNotFound message→conversation 的关联：
// 由于 sqlite 默认 PRAGMA foreign_keys=OFF，这里不期望 FK 报错，
// 但语义上 AddMessage 一个不存在的 cid 会写入孤儿行。
// 用例仅断言：插入不报错、能被 ListMessages 查到（覆盖 INSERT 路径）。
func TestAddMessage_ConversationNotFound(t *testing.T) {
	s := newTestStore(t)
	m := &Message{ConversationID: "conv_orphan", Role: "user", Content: "{}", MediaKind: "text"}
	if err := s.AddMessage(context.Background(), m); err != nil {
		t.Fatalf("AddMessage 不应因 cid 不存在而报错（默认 FK 关闭），得到 %v", err)
	}
	got, _ := s.ListMessages(context.Background(), "conv_orphan")
	if len(got) != 1 || got[0].ID != m.ID {
		t.Fatalf("应能查到孤儿消息，得到 %+v", got)
	}
}
