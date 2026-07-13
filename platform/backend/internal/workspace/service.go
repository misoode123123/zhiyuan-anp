package workspace

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Service 项目空间业务逻辑。
type Service struct {
	repo *Repository
}

// NewService 构造 Service。
func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// CreateProjectSpaceInput 创建项目空间入参。
type CreateProjectSpaceInput struct {
	Name string `json:"name" validate:"required,min=1,max=128"`
	Slug string `json:"slug" validate:"required,min=1,max=64"`
}

// CreateProjectInput 创建项目入参。
type CreateProjectInput struct {
	Name string `json:"name" validate:"required,min=1,max=128"`
	Slug string `json:"slug" validate:"required,min=1,max=64"`
}

func newID(prefix string) string {
	return prefix + strings.ReplaceAll(uuid.NewString(), "-", "")[:20]
}

func (s *Service) CreateProjectSpace(ctx context.Context, in CreateProjectSpaceInput) (*ProjectSpace, error) {
	ps := &ProjectSpace{
		ID: newID("ps_"), Name: in.Name, Slug: in.Slug, Status: "active",
	}
	if err := s.repo.CreateProjectSpace(ctx, ps); err != nil {
		return nil, err
	}
	return ps, nil
}

func (s *Service) GetProjectSpace(ctx context.Context, id string) (*ProjectSpace, error) {
	return s.repo.GetProjectSpace(ctx, id)
}

func (s *Service) ListProjectSpaces(ctx context.Context) ([]ProjectSpace, error) {
	return s.repo.ListProjectSpaces(ctx)
}

// CreateProject 强制绑定路径中的 projectSpaceID（多租户隔离：不允许跨空间写入）。
func (s *Service) CreateProject(ctx context.Context, projectSpaceID string, in CreateProjectInput) (*Project, error) {
	p := &Project{
		ID: newID("prj_"), ProjectSpaceID: projectSpaceID,
		Name: in.Name, Slug: in.Slug, Status: "active",
	}
	if err := s.repo.CreateProject(ctx, p); err != nil {
		return nil, err
	}
	return p, nil
}

func (s *Service) ListProjects(ctx context.Context, projectSpaceID string) ([]Project, error) {
	return s.repo.ListProjects(ctx, projectSpaceID)
}
