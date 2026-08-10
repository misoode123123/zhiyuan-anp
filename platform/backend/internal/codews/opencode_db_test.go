package codews

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// seedOpenCodeDB 建一个最小 opencode.db（message+part 表）灌入测试对话，返回其路径。
func seedOpenCodeDB(t *testing.T, sid string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "opencode.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer db.Close()
	for _, s := range []string{
		`CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
		`CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT NOT NULL, session_id TEXT NOT NULL, time_created INTEGER NOT NULL, time_updated INTEGER NOT NULL, data TEXT NOT NULL)`,
	} {
		if _, err := db.Exec(s); err != nil {
			t.Fatalf("create table: %v", err)
		}
	}
	mustExec := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed insert: %v", err)
		}
	}
	// user 消息：1 个 text part
	mustExec(`INSERT INTO message (id,session_id,time_created,time_updated,data) VALUES ('m1',?,1000,1000,'{"role":"user"}')`, sid)
	mustExec(`INSERT INTO part (id,message_id,session_id,time_created,time_updated,data) VALUES ('p1','m1',?,1000,1000,'{"type":"text","text":"帮我加登录页"}')`, sid)
	// assistant 消息：reasoning + text + text 三 part（验证 reasoning 跳过、多 text 按时序拼接）
	mustExec(`INSERT INTO message (id,session_id,time_created,time_updated,data) VALUES ('m2',?,2000,2000,'{"role":"assistant"}')`, sid)
	mustExec(`INSERT INTO part (id,message_id,session_id,time_created,time_updated,data) VALUES ('p2','m2',?,2001,2001,'{"type":"reasoning","text":"思考中"}')`, sid)
	mustExec(`INSERT INTO part (id,message_id,session_id,time_created,time_updated,data) VALUES ('p3','m2',?,2002,2002,'{"type":"text","text":"已创建 login.tsx"}')`, sid)
	mustExec(`INSERT INTO part (id,message_id,session_id,time_created,time_updated,data) VALUES ('p4','m2',?,2003,2003,'{"type":"text","text":"并加了路由"}')`, sid)
	return path
}

// TestOpenCodeDBReader_Messages 直读 opencode.db 还原对话：role 取自 message.data，
// content 聚合 part.data.type=='text' 的 text（跳过 reasoning/tool），按 message 时序。
func TestOpenCodeDBReader_Messages(t *testing.T) {
	const sid = "ses_test123"
	t.Setenv("OPENCODE_DB_PATH", seedOpenCodeDB(t, sid))

	msgs, err := opencodeDBReader{}.Messages("", sid)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("want 2 msgs, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "user" || msgs[0].Content != "帮我加登录页" {
		t.Fatalf("user msg 不符: %+v", msgs[0])
	}
	// assistant：reasoning 跳过，两个 text 按 \n 拼
	if msgs[1].Role != "assistant" || msgs[1].Content != "已创建 login.tsx\n并加了路由" {
		t.Fatalf("assistant msg 不符（reasoning 应跳过、多 text 拼接）: %+v", msgs[1])
	}

	// 不存在的 session → 空切片（非错误）
	empty, err := opencodeDBReader{}.Messages("", "ses_nope")
	if err != nil || len(empty) != 0 {
		t.Fatalf("不存在 session 应返空切片, got=%v err=%v", empty, err)
	}
	// 空 sessionID → 直接 nil（不查库）
	gotNil, _ := opencodeDBReader{}.Messages("", "")
	if gotNil != nil {
		t.Fatalf("空 sessionID 应 nil, got %v", gotNil)
	}
}

// TestOpenCodeDBReader_DBNotFound 库文件缺失时返错误（handler 据此降级 live 兜底）。
func TestOpenCodeDBReader_DBNotFound(t *testing.T) {
	t.Setenv("OPENCODE_DB_PATH", filepath.Join(t.TempDir(), "nope.db"))
	_, err := opencodeDBReader{}.Messages("", "ses_x")
	if err == nil {
		t.Fatal("库缺失应返错误")
	}
}
