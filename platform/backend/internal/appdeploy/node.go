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
	WinRMPort     int        `json:"winrm_port,omitempty" db:"winrm_port"`
	LastSeen      *time.Time `json:"last_seen,omitempty" db:"last_seen"`
	ProvisionLog  string     `json:"provision_log,omitempty" db:"provision_log"`
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
			os_type, env, connect_type, ssh_port,
			COALESCE(ssh_key,'') AS ssh_key, COALESCE(winrm_user,'') AS winrm_user, COALESCE(winrm_password,'') AS winrm_password,
			winrm_port, last_seen, COALESCE(provision_log,'') AS provision_log
		 FROM deploy_node ORDER BY created_at`)
	return list, err
}

func (s *NodeStore) Get(ctx context.Context, id string) (*DeployNode, error) {
	var n DeployNode
	err := s.db.GetContext(ctx, &n,
		`SELECT id, name, host, docker_url, ssh_user, status, max_apps, description, created_at,
			os_type, env, connect_type, ssh_port,
			COALESCE(ssh_key,'') AS ssh_key, COALESCE(winrm_user,'') AS winrm_user, COALESCE(winrm_password,'') AS winrm_password,
			winrm_port, last_seen, COALESCE(provision_log,'') AS provision_log
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
	if n.WinRMPort == 0 {
		n.WinRMPort = 5985
	}
	n.CreatedAt = time.Now()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO deploy_node (id, name, host, docker_url, ssh_user, status, max_apps, description,
			os_type, env, connect_type, ssh_port, ssh_key, winrm_user, winrm_password, winrm_port, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		n.ID, n.Name, n.Host, n.DockerURL, n.SSHUser, n.Status, n.MaxApps, n.Description,
		n.OSType, n.Env, n.ConnectType, n.SSHPort, n.SSHKey, n.WinRMUser, n.WinRMPassword, n.WinRMPort, n.CreatedAt)
	return err
}

func (s *NodeStore) Delete(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM deploy_node WHERE id = $1 AND id != 'node_local'`, id)
	return err
}

// Update 编辑节点（不含 created_at；updated_at 若表有该列则一并写 now()，无则不写）。
// 与 Delete 一致：不硬保护 node_local，由前端控制是否允许编辑本地节点。
func (s *NodeStore) Update(ctx context.Context, n *DeployNode) error {
	if n.WinRMPort == 0 {
		n.WinRMPort = 5985
	}
	if n.SSHPort == 0 {
		n.SSHPort = 22
	}
	_, err := s.db.ExecContext(ctx,
		`UPDATE deploy_node SET
			name=$1, host=$2, docker_url=$3, ssh_user=$4, status=$5, max_apps=$6, description=$7,
			os_type=$8, env=$9, connect_type=$10, ssh_port=$11, ssh_key=$12,
			winrm_user=$13, winrm_password=$14, winrm_port=$15
		 WHERE id=$16`,
		n.Name, n.Host, n.DockerURL, n.SSHUser, n.Status, n.MaxApps, n.Description,
		n.OSType, n.Env, n.ConnectType, n.SSHPort, n.SSHKey,
		n.WinRMUser, n.WinRMPassword, n.WinRMPort, n.ID)
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

// SetNodeStatus 更新节点 status + last_seen + provision_log（I4 修复：provision 日志落库，
// spec §4.4）。buildLog 为空时清空旧日志（如重新 provisioning 前重置）。
func (s *NodeStore) SetNodeStatus(ctx context.Context, id, status, buildLog string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE deploy_node SET status=$1, last_seen=$2, provision_log=$3 WHERE id=$4`,
		status, time.Now(), buildLog, id)
	return err
}
