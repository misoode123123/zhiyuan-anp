package appdeploy

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// ArtifactStore 产物数据访问。
type ArtifactStore struct{ db *sqlx.DB }

func NewArtifactStore(db *sqlx.DB) *ArtifactStore { return &ArtifactStore{db: db} }

const artifactCols = `id, application_id, build_version, app_kind, platform, arch, filename, size_bytes, sha256, storage_key, content_type, created_at`

// Create 写一条产物记录。id 空则生成。
func (s *ArtifactStore) Create(ctx context.Context, a *Artifact) error {
	if a.ID == "" {
		a.ID = "art_" + uuid.NewString()[:20]
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO appdeploy_artifact (`+artifactCols+`)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,COALESCE($12,CURRENT_TIMESTAMP))`,
		a.ID, a.ApplicationID, a.BuildVersion, a.AppKind, a.Platform, a.Arch, a.Filename,
		a.SizeBytes, a.SHA256, a.StorageKey, a.ContentType, a.CreatedAt)
	return err
}

// ListByApp 列出应用全部产物（按 build_version 倒序）。
func (s *ArtifactStore) ListByApp(ctx context.Context, appID string) ([]Artifact, error) {
	var list []Artifact
	err := s.db.SelectContext(ctx, &list,
		`SELECT `+artifactCols+` FROM appdeploy_artifact WHERE application_id=$1 ORDER BY build_version DESC, created_at DESC`, appID)
	return list, err
}

// Get 取单条产物（用于下载鉴权校验）。
func (s *ArtifactStore) Get(ctx context.Context, id string) (*Artifact, error) {
	var a Artifact
	err := s.db.GetContext(ctx, &a, `SELECT `+artifactCols+` FROM appdeploy_artifact WHERE id=$1`, id)
	if err != nil {
		return nil, fmt.Errorf("artifact %s: %w", id, err)
	}
	return &a, nil
}

// DeleteByApp 删除应用全部产物记录（MinIO 实体由调用方清理）。
func (s *ArtifactStore) DeleteByApp(ctx context.Context, appID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM appdeploy_artifact WHERE application_id=$1`, appID)
	return err
}

// BuildConfigStore 构建配置数据访问。
type BuildConfigStore struct{ db *sqlx.DB }

func NewBuildConfigStore(db *sqlx.DB) *BuildConfigStore { return &BuildConfigStore{db: db} }

// Get 按 app_kind 取构建配置。
func (s *BuildConfigStore) Get(ctx context.Context, appKind string) (*BuildConfig, error) {
	var c BuildConfig
	err := s.db.GetContext(ctx, &c,
		`SELECT app_kind, build_image, build_command, artifact_dir, scaffold, created_at
		 FROM appdeploy_build_config WHERE app_kind=$1`, appKind)
	if err != nil {
		return nil, fmt.Errorf("build_config %s: %w", appKind, err)
	}
	return &c, nil
}
