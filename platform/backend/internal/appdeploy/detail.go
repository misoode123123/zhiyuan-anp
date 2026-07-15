package appdeploy

import (
	"context"
	"time"
)

// AppDetail 应用详情聚合：应用本体 + 其归属的需求/变更/发布 + 仓库版本历史（应用一等公民视图）。
// 变更/发布通过 requirement.application_id 派生关联（source_id→requirement→app），无需冗余列。
type AppDetail struct {
	Application  Application     `json:"application"`
	Requirements []AppReqItem    `json:"requirements"`
	Changes      []AppChangeItem `json:"changes"`
	Releases     []AppRelItem    `json:"releases"`
	Commits      []CommitInfo    `json:"commits"` // 托管 git 仓库的版本历史（= 应用代码版本）
}

// AppReqItem 需求条目（详情用精简字段）。
type AppReqItem struct {
	ID     string `json:"id" db:"id"`
	Title  string `json:"title" db:"title"`
	Status string `json:"status" db:"status"`
}

// AppChangeItem 变更条目。
type AppChangeItem struct {
	ID       string    `json:"id" db:"id"`
	Status   string    `json:"status" db:"status"`
	SourceID string    `json:"source_id" db:"source_id"`
	Kind     string    `json:"kind" db:"kind"`
	CreateAt time.Time `json:"created_at" db:"created_at"`
}

// AppRelItem 发布条目。
type AppRelItem struct {
	ID        string    `json:"id" db:"id"`
	Version   string    `json:"version" db:"version"`
	Status    string    `json:"status" db:"status"`
	ChangeID  string    `json:"change_id" db:"change_id"`
	CreateAt  time.Time `json:"created_at" db:"created_at"`
}

// Detail 聚合某应用的研发全链路（需求→变更→发布）+ 应用本体。
func (s *Store) Detail(ctx context.Context, psID, appID string) (*AppDetail, error) {
	a, err := s.Get(ctx, psID, appID)
	if err != nil || a == nil || a.ID == "" {
		return nil, err
	}
	d := &AppDetail{Application: *a}

	// 需求（直接归属）
	if err := s.db.SelectContext(ctx, &d.Requirements,
		`SELECT id, COALESCE(title,'') AS title, status FROM requirement WHERE application_id=? ORDER BY created_at DESC`, appID); err != nil {
		return nil, err
	}
	// 变更（经 source_id→requirement→app 派生）
	if err := s.db.SelectContext(ctx, &d.Changes,
		`SELECT id, status, COALESCE(source_id,'') AS source_id, COALESCE(kind,'') AS kind, created_at
		 FROM change_request
		 WHERE source_id IN (SELECT id FROM requirement WHERE application_id=?)
		 ORDER BY created_at DESC`, appID); err != nil {
		return nil, err
	}
	// 发布（经 change_id→change→source_id→requirement→app 派生）
	if err := s.db.SelectContext(ctx, &d.Releases,
		`SELECT id, version, status, COALESCE(change_id,'') AS change_id, created_at
		 FROM release_record
		 WHERE change_id IN (SELECT id FROM change_request WHERE source_id IN (SELECT id FROM requirement WHERE application_id=?))
		 ORDER BY created_at DESC`, appID); err != nil {
		return nil, err
	}
	// 托管仓库版本历史（git log = 应用代码版本）
	d.Commits, _ = Log(ctx, a.RepoDir, 10)
	return d, nil
}
