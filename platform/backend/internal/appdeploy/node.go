package appdeploy

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// DeployNode 部署节点（.28 本地 / .30 远程）。
type DeployNode struct {
	ID            string     `json:"id" db:"id"`
	Name          string     `json:"name" db:"name"`
	Host          string     `json:"host" db:"host"`
	DockerURL     string     `json:"docker_url" db:"docker_url"`
	SSHUser       string     `json:"ssh_user" db:"ssh_user"`
	Status        string     `json:"status" db:"status"`
	MaxApps       int        `json:"max_apps" db:"max_apps"`
	Description   string     `json:"description,omitempty" db:"description"`
	CreatedAt     time.Time  `json:"created_at" db:"created_at"`
	OSType        string     `json:"os_type" db:"os_type"`
	Env           string     `json:"env" db:"env"`
	ConnectType   string     `json:"connect_type" db:"connect_type"`
	SSHPort       int        `json:"ssh_port" db:"ssh_port"`
	SSHKey        string     `json:"ssh_key,omitempty" db:"ssh_key"`
	WinRMUser     string     `json:"winrm_user,omitempty" db:"winrm_user"`
	WinRMPassword string     `json:"winrm_password,omitempty" db:"winrm_password"`
	LastSeen      *time.Time `json:"last_seen,omitempty" db:"last_seen"`
}

// NodeStore 节点数据访问。
type NodeStore struct {
	db *sqlx.DB
}

func NewNodeStore(db *sqlx.DB) *NodeStore { return &NodeStore{db: db} }

func (s *NodeStore) List(ctx context.Context) ([]DeployNode, error) {
	var list []DeployNode
	err := s.db.SelectContext(ctx, &list,
		`SELECT id, name, host, docker_url, ssh_user, status, max_apps, description, created_at,
			os_type, env, connect_type, ssh_port, ssh_key, winrm_user, winrm_password, last_seen
		 FROM deploy_node ORDER BY created_at`)
	return list, err
}

func (s *NodeStore) Get(ctx context.Context, id string) (*DeployNode, error) {
	var n DeployNode
	err := s.db.GetContext(ctx, &n,
		`SELECT id, name, host, docker_url, ssh_user, status, max_apps, description, created_at,
			os_type, env, connect_type, ssh_port, ssh_key, winrm_user, winrm_password, last_seen
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
	if n.OSType == "" {
		n.OSType = "linux"
	}
	if n.Env == "" {
		n.Env = "dev"
	}
	if n.ConnectType == "" {
		n.ConnectType = "docker_tcp"
	}
	if n.SSHPort == 0 {
		n.SSHPort = 22
	}
	n.CreatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deploy_node (id, name, host, docker_url, ssh_user, status, max_apps, description,
			os_type, env, connect_type, ssh_port, ssh_key, winrm_user, winrm_password, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`,
		n.ID, n.Name, n.Host, n.DockerURL, n.SSHUser, n.Status, n.MaxApps, n.Description,
		n.OSType, n.Env, n.ConnectType, n.SSHPort, n.SSHKey, n.WinRMUser, n.WinRMPassword, n.CreatedAt)
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

// SetNodeStatus 更新节点 status + last_seen。
// buildLog 参数当前不落库（deploy_node 无日志列），Task 9 如需持久化再加列；
// 此处接收以保持 ProvisionNode 流的调用契约稳定。
func (s *NodeStore) SetNodeStatus(ctx context.Context, id, status, buildLog string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deploy_node SET status=$1, last_seen=$2 WHERE id=$3`, status, time.Now(), id)
	return err
}
