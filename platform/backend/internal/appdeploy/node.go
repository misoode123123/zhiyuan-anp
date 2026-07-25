package appdeploy

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// DeployNode 部署节点（.28 本地 / .30 远程）。
type DeployNode struct {
	ID          string    `json:"id" db:"id"`
	Name        string    `json:"name" db:"name"`
	Host        string    `json:"host" db:"host"`
	DockerURL   string    `json:"docker_url" db:"docker_url"`
	SSHUser     string    `json:"ssh_user" db:"ssh_user"`
	Status      string    `json:"status" db:"status"`
	MaxApps     int       `json:"max_apps" db:"max_apps"`
	Description string    `json:"description,omitempty" db:"description"`
	CreatedAt   time.Time `json:"created_at" db:"created_at"`
}

// NodeStore 节点数据访问。
type NodeStore struct {
	db *sqlx.DB
}

func NewNodeStore(db *sqlx.DB) *NodeStore { return &NodeStore{db: db} }

func (s *NodeStore) List(ctx context.Context) ([]DeployNode, error) {
	var list []DeployNode
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, name, host, docker_url, ssh_user, status, max_apps, description, created_at
		 FROM deploy_node ORDER BY created_at`)
	return list, err
}

func (s *NodeStore) Get(ctx context.Context, id string) (*DeployNode, error) {
	var n DeployNode
	err := s.db.GetContext(ctx, &n,
		`SELECT id, name, host, docker_url, ssh_user, status, max_apps, description, created_at
		 FROM deploy_node WHERE id = $1`, id)
	return &n, err
}

func (s *NodeStore) Create(ctx context.Context, n *DeployNode) error {
	n.ID = "node_" + uuid.NewString()[:18]
	if n.Status == "" {
		n.Status = "active"
	}
	if n.MaxApps == 0 {
		n.MaxApps = 20
	}
	if n.SSHUser == "" {
		n.SSHUser = "root"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deploy_node (id, name, host, docker_url, ssh_user, status, max_apps, description)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		n.ID, n.Name, n.Host, n.DockerURL, n.SSHUser, n.Status, n.MaxApps, n.Description)
	return err
}

func (s *NodeStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deploy_node WHERE id = $1 AND id != 'node_local'`, id)
	return err
}

// TestDocker 测试节点连通性（docker version）。
func (s *NodeStore) TestDocker(ctx context.Context, dockerURL string) error {
	_, err := runDockerOn(ctx, dockerURL, "version", "--format", "{{.Server.Version}}")
	return err
}

// AppCount 节点上运行的应用数。
func (s *NodeStore) AppCount(ctx context.Context, nodeID string) (int, error) {
	var n int
	err := s.db.GetContext(ctx, &n,
		`SELECT COUNT(*) FROM appdeploy_application WHERE deploy_node_id = $1 AND status = 'running'`, nodeID)
	return n, err
}
