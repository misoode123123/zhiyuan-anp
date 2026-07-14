// Package conversation 是「对话式需求梳理」限界上下文。
// AI agent 与用户多轮对话梳理需求，确认后生成 requirement 规格接编码闭环。完整记录对话。
package conversation

import "time"

// Conversation 一次需求梳理会话。
type Conversation struct {
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	Status         string    `json:"status" db:"status"` // active/submitted
	Title          *string   `json:"title" db:"title"`
	RequirementID  *string   `json:"requirement_id" db:"requirement_id"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// Message 会话消息。
type Message struct {
	ID             string    `json:"id" db:"id"`
	ConversationID string    `json:"conversation_id" db:"conversation_id"`
	Role           string    `json:"role" db:"role"` // user/assistant
	Content        string    `json:"content" db:"content"` // JSON: {"text":...,"images":[...]}
	MediaKind      string    `json:"media_kind" db:"media_kind"` // text/image/audio(预留)
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}
