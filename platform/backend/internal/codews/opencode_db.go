package codews

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 纯 Go SQLite 驱动：只读 opencode.db，与业务库 PG 互不影响
)

// opencode 把全部会话/消息写入本地 SQLite 库 opencode.db（message+part 表，WAL 实时落盘）。
// 直读此库还原对话，优于 live HTTP 与后台快照：数据实时落盘、进程死后文件仍在（bind mount 持久），
// 无需 ticker 搬运、不丢消息（除容器卷被删）。session.id 全局唯一，多用户共享同一库按它区分。

// opencodeDBPath 返回 opencode 本地库路径。
// 默认 $XDG_DATA_HOME/opencode/opencode.db 或 $HOME/.local/share/opencode/opencode.db；
// OPENCODE_DB_PATH env 覆盖（测试/自定义）。codews 仅隔离 config（XDG_CONFIG_HOME）不隔离 DATA，
// 故多用户共享同一库。
func opencodeDBPath() string {
	if p := os.Getenv("OPENCODE_DB_PATH"); p != "" {
		return p
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "opencode", "opencode.db")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".local", "share", "opencode", "opencode.db")
	}
	return "opencode.db"
}

// opencodeDBReader 只读打开 opencode.db 还原对话（实现 TranscriptReader）。
type opencodeDBReader struct{}

// openOpenCodeDB 只读打开 opencode.db（mode=ro 自动合并 -wal 最新数据，不阻塞 opencode 写）。
func openOpenCodeDB() (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+opencodeDBPath()+"?mode=ro")
}

// Messages 按 session_id 还原某会话全部对话。repoDir 忽略（同库按全局唯一 session_id 查）。
// 聚合：message.data.role 定角色；该 message 下 part.data.type=='text' 的 text 拼正文；
// 按 message.time_created 时序。跳过 reasoning/tool/step-start 等非文本块。
func (opencodeDBReader) Messages(repoDir, sessionID string) ([]TranscriptMsg, error) {
	if sessionID == "" {
		return nil, nil
	}
	db, err := openOpenCodeDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(
		`SELECT m.id, m.time_created, json_extract(m.data,'$.role') AS role,
		        json_extract(p.data,'$.type') AS ptype, json_extract(p.data,'$.text') AS ptext
		 FROM message m JOIN part p ON p.message_id = m.id
		 WHERE m.session_id = ?
		 ORDER BY m.time_created, m.id, p.time_created`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		role string
		ts   int64
		sb   strings.Builder
	}
	var order []string
	cur := map[string]*acc{}
	for rows.Next() {
		var id, role, ptype, ptext sql.NullString
		var ts sql.NullInt64
		if err := rows.Scan(&id, &ts, &role, &ptype, &ptext); err != nil {
			return nil, err
		}
		if ptype.String != "text" || ptext.String == "" {
			continue // 仅聚合 text 块
		}
		a, ok := cur[id.String]
		if !ok {
			a = &acc{role: role.String, ts: ts.Int64}
			cur[id.String] = a
			order = append(order, id.String)
		}
		if a.sb.Len() > 0 {
			a.sb.WriteByte('\n')
		}
		a.sb.WriteString(ptext.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]TranscriptMsg, 0, len(order))
	for _, id := range order {
		a := cur[id]
		out = append(out, TranscriptMsg{Role: a.role, Content: a.sb.String(), CreatedAt: msToTime(a.ts)})
	}
	return out, nil
}

// Sessions 按 repoDir(directory) 列 opencode 会话。performance 会话列表走 codews_session，
// 此方法供其他按 repo 维度的入口扩展用。
func (opencodeDBReader) Sessions(repoDir string) ([]TranscriptMeta, error) {
	if repoDir == "" {
		return nil, nil
	}
	db, err := openOpenCodeDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(
		`SELECT id, COALESCE(directory,''), time_updated, COALESCE(title,'')
		 FROM session WHERE directory = ? ORDER BY time_updated DESC`, repoDir)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TranscriptMeta
	for rows.Next() {
		var id, dir, title sql.NullString
		var ts sql.NullInt64
		if err := rows.Scan(&id, &dir, &ts, &title); err != nil {
			return nil, err
		}
		out = append(out, TranscriptMeta{
			SessionID: id.String, Tool: "opencode", Cwd: dir.String, UpdatedAt: msToTime(ts.Int64),
		})
	}
	return out, nil
}

// msToTime opencode 的 time_created/time_updated 为 epoch 毫秒。
func msToTime(ms int64) time.Time {
	if ms <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms).UTC()
}
