package conversation

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"zhiyuan-anp/platform/backend/internal/requirement"
)

// analystSystemPrompt 需求梳理员人设（混合模式）。
const analystSystemPrompt = `你是资深需求分析师，与用户对话梳理需求。先听用户自由描述，
识别缺失要素（用户角色 / 目标价值 / 使用场景 / 约束 / 验收标准）并针对性追问。
要素齐全时，主动询问「是否要我生成结构化需求规格？」。
回复简洁，一次只问 1-2 个关键问题。`

// specSystemPrompt 把对话总结为结构化规格。
const specSystemPrompt = `你是资深需求分析师。把以下对话梳理出的需求，总结为结构化需求规格。
严格只返回纯 JSON（不要 markdown、不要解释），格式：
{"title":"简洁需求标题","user_story":"作为<角色>，我希望<功能>，以便<价值>","acceptance_criteria":["可验证的验收点1","验收点2"]}`

// Service 对话式需求梳理业务逻辑。
type Service struct {
	store   *Store
	reqRepo *requirement.Repository
	agent   string // agent-runtime URL
}

// NewService 构造。
func NewService(store *Store, reqRepo *requirement.Repository, agentURL string) *Service {
	return &Service{store: store, reqRepo: reqRepo, agent: agentURL}
}

type msgContent struct {
	Text   string   `json:"text"`
	Images []string `json:"images,omitempty"`
}

func parseMsgContent(s string) msgContent {
	var c msgContent
	if s == "" {
		return c
	}
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		return msgContent{Text: s} // 兼容纯文本
	}
	return c
}

// toChatContent 转成 /v1/chat 的 content（纯文本或多模态数组）。
func toChatContent(c msgContent) interface{} {
	if len(c.Images) == 0 {
		return c.Text
	}
	parts := []map[string]interface{}{{"type": "text", "text": c.Text}}
	for _, img := range c.Images {
		parts = append(parts, map[string]interface{}{"type": "image_url", "image_url": map[string]string{"url": img}})
	}
	return parts
}

// CreateConversation 建会话。
func (s *Service) CreateConversation(ctx context.Context, psID string) (*Conversation, error) {
	c := &Conversation{ProjectSpaceID: psID}
	if err := s.store.CreateConv(ctx, c); err != nil {
		return nil, err
	}
	return c, nil
}

// ListConversations 列会话。
func (s *Service) ListConversations(ctx context.Context, psID string) ([]Conversation, error) {
	return s.store.ListConvByPS(ctx, psID)
}

// GetConversation 会话 + 全部消息。
func (s *Service) GetConversation(ctx context.Context, cid string) (*Conversation, []Message, error) {
	c, err := s.store.GetConv(ctx, cid)
	if err != nil || c.ID == "" {
		return nil, nil, fmt.Errorf("会话不存在")
	}
	msgs, err := s.store.ListMessages(ctx, cid)
	if err != nil {
		return nil, nil, err
	}
	return c, msgs, nil
}

// SendMessage 用户发消息 → agent 回复（多模态：text + images）。
func (s *Service) SendMessage(ctx context.Context, cid, text string, images []string) (*Message, error) {
	conv, err := s.store.GetConv(ctx, cid)
	if err != nil || conv.ID == "" {
		return nil, fmt.Errorf("会话不存在")
	}
	// 存 user message
	uc, _ := json.Marshal(msgContent{Text: text, Images: images})
	mediaKind := "text"
	if len(images) > 0 {
		mediaKind = "image"
	}
	if err := s.store.AddMessage(ctx, &Message{ConversationID: cid, Role: "user", Content: string(uc), MediaKind: mediaKind}); err != nil {
		return nil, err
	}
	// 拼 messages（system + 历史 + 新 user 已在历史中）
	history, err := s.store.ListMessages(ctx, cid)
	if err != nil {
		return nil, err
	}
	msgs := []map[string]interface{}{{"role": "system", "content": analystSystemPrompt}}
	for _, m := range history {
		msgs = append(msgs, map[string]interface{}{"role": m.Role, "content": toChatContent(parseMsgContent(m.Content))})
	}
	reply, err := s.chat(ctx, msgs, "")
	if err != nil {
		return nil, err
	}
	// 存 assistant message
	ac, _ := json.Marshal(msgContent{Text: reply})
	if err := s.store.AddMessage(ctx, &Message{ConversationID: cid, Role: "assistant", Content: string(ac), MediaKind: "text"}); err != nil {
		return nil, err
	}
	return &Message{ConversationID: cid, Role: "assistant", Content: string(ac), MediaKind: "text"}, nil
}

// SendMessageStream 流式发消息：逐 chunk 回调 onChunk，流结束存完整 assistant message。
func (s *Service) SendMessageStream(ctx context.Context, cid, text string, images []string, onChunk func(string)) (*Message, error) {
	conv, err := s.store.GetConv(ctx, cid)
	if err != nil || conv.ID == "" {
		return nil, fmt.Errorf("会话不存在")
	}
	uc, _ := json.Marshal(msgContent{Text: text, Images: images})
	mediaKind := "text"
	if len(images) > 0 {
		mediaKind = "image"
	}
	if err := s.store.AddMessage(ctx, &Message{ConversationID: cid, Role: "user", Content: string(uc), MediaKind: mediaKind}); err != nil {
		return nil, err
	}
	history, err := s.store.ListMessages(ctx, cid)
	if err != nil {
		return nil, err
	}
	msgs := []map[string]interface{}{{"role": "system", "content": analystSystemPrompt}}
	for _, m := range history {
		msgs = append(msgs, map[string]interface{}{"role": m.Role, "content": toChatContent(parseMsgContent(m.Content))})
	}
	body := map[string]interface{}{"messages": msgs}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.agent+"/v1/chat/stream", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 3 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var full strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}
		var d struct {
			Delta string `json:"delta"`
		}
		if json.Unmarshal([]byte(payload), &d) == nil && d.Delta != "" {
			full.WriteString(d.Delta)
			if onChunk != nil {
				onChunk(d.Delta)
			}
		}
	}
	reply := full.String()
	if reply == "" {
		reply = "(AI 无回复)"
	}
	ac, _ := json.Marshal(msgContent{Text: reply})
	if err := s.store.AddMessage(ctx, &Message{ConversationID: cid, Role: "assistant", Content: string(ac), MediaKind: "text"}); err != nil {
		return nil, err
	}
	return &Message{ConversationID: cid, Role: "assistant", Content: string(ac), MediaKind: "text"}, nil
}

// SpecResult 规格草稿。
type SpecResult struct {
	Title              string   `json:"title"`
	UserStory          string   `json:"user_story"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
}

// GenerateSpec 全历史总结成规格草稿（不入库）。
func (s *Service) GenerateSpec(ctx context.Context, cid string) (*SpecResult, error) {
	history, err := s.store.ListMessages(ctx, cid)
	if err != nil {
		return nil, err
	}
	if len(history) == 0 {
		return nil, fmt.Errorf("会话尚无消息")
	}
	var dialog bytes.Buffer
	for _, m := range history {
		c := parseMsgContent(m.Content)
		who := "用户"
		if m.Role == "assistant" {
			who = "AI"
		}
		fmt.Fprintf(&dialog, "%s：%s\n", who, c.Text)
	}
	msgs := []map[string]interface{}{
		{"role": "system", "content": specSystemPrompt},
		{"role": "user", "content": "以下是对话梳理出的需求，请总结成结构化规格：\n" + dialog.String()},
	}
	reply, err := s.chat(ctx, msgs, "zhipu/glm-4-flash")
	if err != nil {
		return nil, err
	}
	var spec SpecResult
	if err := json.Unmarshal([]byte(extractJSON(reply)), &spec); err != nil {
		return nil, fmt.Errorf("解析规格失败: %w（原文: %s）", err, reply)
	}
	return &spec, nil
}

// Commit 确认入库：写 requirement + 会话 submitted + 回填。
func (s *Service) Commit(ctx context.Context, cid string, spec *SpecResult) (*requirement.Requirement, error) {
	conv, err := s.store.GetConv(ctx, cid)
	if err != nil || conv.ID == "" {
		return nil, fmt.Errorf("会话不存在")
	}
	acJSON, _ := json.Marshal(spec.AcceptanceCriteria)
	req := &requirement.Requirement{
		ID:                 "req_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:20],
		ProjectSpaceID:     conv.ProjectSpaceID,
		Title:              spec.Title,
		Description:        "(由对话梳理生成)",
		UserStory:          spec.UserStory,
		AcceptanceCriteria: string(acJSON),
		Status:             "specified",
	}
	if err := s.reqRepo.Create(ctx, req); err != nil {
		return nil, err
	}
	if err := s.store.SubmitConv(ctx, cid, spec.Title, req.ID); err != nil {
		return nil, err
	}
	return req, nil
}

// chat 调 agent-runtime /v1/chat（无状态多轮），返回 assistant 文本。
func (s *Service) chat(ctx context.Context, msgs []map[string]interface{}, model string) (string, error) {
	body := map[string]interface{}{"messages": msgs}
	if model != "" {
		body["model"] = model
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", s.agent+"/v1/chat", bytes.NewReader(buf))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Content string `json:"content"`
		Error   string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析 AI 响应: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("AI 返回错误: %s", out.Error)
	}
	return out.Content, nil
}

// ASR 语音识别：转发 agent-runtime /v1/asr（base64 音频 → 文字）。
func (s *Service) ASR(ctx context.Context, audioB64, filename string) (string, error) {
	body, _ := json.Marshal(map[string]string{"audio": audioB64, "filename": filename})
	req, err := http.NewRequestWithContext(ctx, "POST", s.agent+"/v1/asr", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Text  string `json:"text"`
		Error string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("解析 ASR 响应: %w", err)
	}
	if out.Error != "" {
		return "", fmt.Errorf("ASR: %s", out.Error)
	}
	return out.Text, nil
}

// extractJSON 从可能含 markdown 的文本提取首个 JSON 对象。
func extractJSON(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start >= 0 && end > start {
		return s[start : end+1]
	}
	return s
}
