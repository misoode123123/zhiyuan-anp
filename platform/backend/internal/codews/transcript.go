package codews

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TranscriptMeta 工具原生会话的元信息（绩效互动区列表/下钻用）。
type TranscriptMeta struct {
	SessionID   string    `json:"session_id"`
	Tool        string    `json:"tool"`
	Cwd         string    `json:"cwd"`
	UpdatedAt   time.Time `json:"updated_at"`
	PromptCount int       `json:"prompt_count"`
}

// TranscriptMsg 归一化后的单条对话消息（跨工具统一）。
type TranscriptMsg struct {
	Role      string    `json:"role"` // user / assistant
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

// TranscriptReader 读取某 repo 的工具原生 transcript（claude/codex 读磁盘；opencode 走 live HTTP，不实现此接口）。
type TranscriptReader interface {
	Sessions(repoDir string) ([]TranscriptMeta, error)           // 该 repo 的会话列表（最近在前）
	Messages(repoDir, sessionID string) ([]TranscriptMsg, error) // 单会话归一化消息
}

// ReaderFor 按工具名返回 transcript reader。claude/codex 读磁盘；其他(nil)。
func ReaderFor(tool string) TranscriptReader {
	switch tool {
	case "claude":
		return fileReader{root: claudeHome(), tool: "claude"}
	case "codex":
		return fileReader{root: codexHome(), tool: "codex"}
	}
	return nil
}

func claudeHome() string {
	if h := os.Getenv("CLAUDE_HOME"); h != "" {
		return filepath.Join(h, "projects")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".claude", "projects")
	}
	return ".claude/projects"
}

func codexHome() string {
	if h := os.Getenv("CODEX_HOME"); h != "" {
		return filepath.Join(h, "sessions")
	}
	if h, err := os.UserHomeDir(); err == nil {
		return filepath.Join(h, ".codex", "sessions")
	}
	return ".codex/sessions"
}

// fileReader 通用磁盘 transcript 读法：递归扫 root 下 *.jsonl，按记录内 cwd 字段匹配 repoDir
// （不靠目录名编码，规避中文/特殊字符编码差异；与 claude/codex 自身 resume 的目录命名无关）。
type fileReader struct {
	root, tool string
}

// transcriptRecord 兼容 claude(message 嵌套) 与 codex(顶层 role/content) 两种记录形态。
type transcriptRecord struct {
	Type      string          `json:"type"`
	Role      string          `json:"role"`       // 顶层 role（codex）
	Message   json.RawMessage `json:"message"`    // 嵌套 {role,content}（claude/codex）
	Content   json.RawMessage `json:"content"`    // 顶层 content（codex）
	Cwd       string          `json:"cwd"`
	SessionID string          `json:"sessionId"`  // claude
	SessID2   string          `json:"session_id"` // codex
	Timestamp string          `json:"timestamp"`
}

// roleContent 取记录的 role+content（优先 message 嵌套，回退顶层）。role 空=非对话行。
func (r transcriptRecord) roleContent() (string, string) {
	if len(r.Message) > 0 {
		var m struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if json.Unmarshal(r.Message, &m) == nil && m.Role != "" {
			return m.Role, flattenContent(m.Content)
		}
	}
	if r.Role != "" {
		return r.Role, flattenContent(r.Content)
	}
	return "", ""
}

func (r transcriptRecord) session() string {
	if r.SessionID != "" {
		return r.SessionID
	}
	return r.SessID2
}

func (f fileReader) Messages(repoDir, sessionID string) ([]TranscriptMsg, error) {
	var out []TranscriptMsg
	for _, p := range globJSONL(f.root) {
		for _, r := range scanJSONL(p) {
			if r.Cwd != repoDir {
				continue
			}
			if sessionID != "" && r.session() != sessionID {
				continue
			}
			role, content := r.roleContent()
			if role == "" {
				continue
			}
			out = append(out, TranscriptMsg{Role: role, Content: content, CreatedAt: parseTS(r.Timestamp)})
		}
	}
	return out, nil
}

func (f fileReader) Sessions(repoDir string) ([]TranscriptMeta, error) {
	byID := map[string]*TranscriptMeta{}
	for _, p := range globJSONL(f.root) {
		for _, r := range scanJSONL(p) {
			if r.Cwd != repoDir {
				continue
			}
			sid := r.session()
			if sid == "" {
				continue
			}
			m := byID[sid]
			if m == nil {
				m = &TranscriptMeta{SessionID: sid, Tool: f.tool, Cwd: repoDir}
				byID[sid] = m
			}
			if t := parseTS(r.Timestamp); t.After(m.UpdatedAt) {
				m.UpdatedAt = t
			}
			if role, _ := r.roleContent(); role == "user" {
				m.PromptCount++
			}
		}
	}
	out := make([]TranscriptMeta, 0, len(byID))
	for _, m := range byID {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out, nil
}

// ---- 辅助 ----

func globJSONL(root string) []string {
	var files []string
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	return files
}

// scanJSONL 逐行解析 jsonl，跳过无法解析的行（容错：未知字段忽略）。
func scanJSONL(path string) []transcriptRecord {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []transcriptRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // 单行最大 4MB（含工具调用/大段代码）
	for sc.Scan() {
		var r transcriptRecord
		if json.Unmarshal(sc.Bytes(), &r) == nil {
			out = append(out, r)
		}
	}
	return out
}

// flattenContent 把 content（string 或 [{type,text}/{type,output_text}/{type,input_text}]）归一化为纯文本。
func flattenContent(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var arr []map[string]json.RawMessage
	if json.Unmarshal(raw, &arr) == nil {
		var sb strings.Builder
		for _, p := range arr {
			var txt string
			if v, ok := p["text"]; ok {
				_ = json.Unmarshal(v, &txt)
			} else if v, ok := p["output_text"]; ok {
				_ = json.Unmarshal(v, &txt)
			} else if v, ok := p["input_text"]; ok {
				_ = json.Unmarshal(v, &txt)
			}
			if txt != "" {
				sb.WriteString(txt)
			}
		}
		return sb.String()
	}
	return ""
}

func parseTS(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}
