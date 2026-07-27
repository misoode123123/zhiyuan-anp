package codews

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// liveClient 读 opencode 工作台内置 API（带超时，防卡死）。
var liveClient = &http.Client{Timeout: 3 * time.Second}

// LiveTranscript 读 opencode 活跃会话的归一化消息（HTTP 127.0.0.1:port/api/session/<id>/message）。
// 仅 opencode（claude/codex 走 ReaderFor 读磁盘 transcript）。无会话/读失败返回空（非致命）。
func LiveTranscript(port int, sessionID string) ([]TranscriptMsg, error) {
	if sessionID == "" {
		return nil, nil
	}
	resp, err := liveClient.Get(fmt.Sprintf("http://127.0.0.1:%d/api/session/%s/message", port, sessionID))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var r struct {
		Data []struct {
			Type  string `json:"type"`
			Parts []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, err
	}
	var out []TranscriptMsg
	for _, msg := range r.Data {
		var sb strings.Builder
		for _, p := range msg.Parts {
			if p.Type == "text" && strings.TrimSpace(p.Text) != "" {
				sb.WriteString(p.Text)
			}
		}
		if sb.Len() == 0 {
			continue
		}
		out = append(out, TranscriptMsg{Role: msg.Type, Content: sb.String()})
	}
	return out, nil
}
