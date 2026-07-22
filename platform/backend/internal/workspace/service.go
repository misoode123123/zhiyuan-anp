package workspace

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

// Service 项目空间业务逻辑。
type Service struct {
	repo    *Repository
	teardowns []TeardownHook // 删项目前的级联清理钩子（pgsupply 清 PG 容器等）
}

// TeardownHook 删 project_space 前的清理钩子。失败应仅记日志不阻塞删除（数据级联由 FK CASCADE 兜底）。
// 例如：pgsupply.InstanceManager.TeardownForProject 清理项目下运行中的 PG 容器（资源泄漏修复 I2）。
type TeardownHook func(ctx context.Context, psID string) error

// NewService 构造 Service。
func NewService(repo *Repository) *Service { return &Service{repo: repo} }

// AddTeardownHook 注册删项目前的清理钩子（main 装配时调，注入 pgsupply 等模块的清理逻辑）。
func (s *Service) AddTeardownHook(h TeardownHook) {
	if h != nil {
		s.teardowns = append(s.teardowns, h)
	}
}

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

// Overview 空间概览（成员/应用/需求/变更/发布计数）。
func (s *Service) Overview(ctx context.Context, psID string) (*Overview, error) {
	return s.repo.Overview(ctx, psID)
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

// DeleteProjectSpace 删项目空间：先跑已注册的 teardown 钩子（清 PG 容器等运行时资源），
// 失败仅记日志不阻塞（数据库行由 FK ON DELETE CASCADE 清）；钩子跑完删 project_space 行。
// 不存在 → ErrNotFound。
func (s *Service) DeleteProjectSpace(ctx context.Context, id string) error {
	if _, err := s.repo.GetProjectSpace(ctx, id); err != nil {
		return err
	}
	for _, h := range s.teardowns {
		_ = h(ctx, id) // 钩子失败不阻塞删除（数据级联由 FK CASCADE）
	}
	return s.repo.DeleteProjectSpace(ctx, id)
}
