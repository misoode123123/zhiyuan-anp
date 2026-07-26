// Package performance 绩效记录：按人聚合开发工作流产出 + 开发者↔编码工具互动统计。
// 只读、按需聚合（MVP 不物化）；admin 看本空间全员，本人看自己。
package performance

import "time"

// Metrics 单人单周期的产出计数。
type Metrics struct {
	ReqClaimed, ReqCompleted                                int    // 需求认领/已完成
	CodeTaskDone, CodeTaskFailed                            int    // 编码任务完成/失败
	ChangeSubmitted, ChangeApproved, ChangeRejected         int    // 变更提交/通过/驳回
	Releases                                                int    // 发布次数
	Conversations                                           int    // 需求梳理会话数
	WsSessions, WsPrompts, WsSeconds                        int    // 编码工作台互动（置顶维度）
}

// SessionSummary 互动会话摘要（绩效页下钻列表）。
type SessionSummary struct {
	ID          string     `json:"id" db:"id"`
	Tool        string     `json:"tool" db:"tool"`
	RepoDir     string     `json:"repo_dir" db:"repo_dir"`
	StartedAt   time.Time  `json:"started_at" db:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty" db:"ended_at"`
	PromptCount int        `json:"prompt_count" db:"prompt_count"`
}

// SessionRow 单条 codews_session（handler 下钻聊天记录用）。
type SessionRow struct {
	ID        string `json:"id" db:"id"`
	AppID     string `json:"app_id" db:"app_id"`
	UserID    string `json:"user_id,omitempty" db:"user_id"`
	Tool      string `json:"tool" db:"tool"`
	RepoDir   string `json:"repo_dir" db:"repo_dir"`
	SessionID string `json:"session_id,omitempty" db:"session_id"`
}

// Profile 单人绩效（含指标 + 最近会话）。
type Profile struct {
	UserID      string           `json:"user_id"`
	UserName    string           `json:"user_name"`
	IsUnassigned bool            `json:"is_unassigned,omitempty"` // user_id 为空的历史行桶
	Metrics     Metrics          `json:"metrics"`
	Sessions    []SessionSummary `json:"sessions,omitempty"`
}
