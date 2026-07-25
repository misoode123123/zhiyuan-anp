// Package appdeploy 是「应用部署引擎」限界上下文 ——
// 把研发产出的应用（源码 + Dockerfile）自动构建为镜像、部署为容器、暴露访问 URL。
//
// 工作模型：ANP 后端经宿主 docker socket（/var/run/docker.sock）控制宿主 Docker，
// 把产出应用作为「同级容器」构建运行：
//
//	注册应用(repo_dir) → docker build → docker run(分配空闲宿主端口) → 返回 http://<host>:<port>
//
// 产出应用须自带 Dockerfile（或后续按 buildpack 模板生成）；repo_dir 为 docker 守护进程可见的路径。
package appdeploy

import "time"

// Application 产出应用（可构建可部署的独立服务）。
type Application struct {
	ID             string        `json:"id" db:"id"`
	ProjectSpaceID string        `json:"project_space_id" db:"project_space_id"`
	Name           string        `json:"name" db:"name"`                   // 应用名（也是镜像/容器名前缀）
	RepoDir        string        `json:"repo_dir" db:"repo_dir"`           // docker 守护进程可见的源码路径（含 Dockerfile）
	InternalPort   int           `json:"internal_port" db:"internal_port"` // 应用容器内监听端口（Dockerfile EXPOSE）
	Image          string        `json:"image" db:"image"`                 // 镜像引用 appdeploy/<name>:v<n>
	ContainerName  string        `json:"container_name" db:"container_name"`
	HostPort       int           `json:"host_port" db:"host_port"` // 分配的宿主端口
	DeployHost     string        `json:"deploy_host,omitempty" db:"deploy_host"` // 部署节点（空=本地 .28，如 tcp://10.10.0.30:2375）
	URL            string        `json:"url" db:"url"`             // http://<host>:<host_port>
	Version        int           `json:"version" db:"version"`     // 构建版本号
	Status         string        `json:"status" db:"status"`       // registered/building/running/stopped/failed
	LastError      string        `json:"last_error,omitempty" db:"last_error"`
	BuildLog       string        `json:"build_log,omitempty" db:"build_log"` // 最近一次构建输出摘要
	DeployMode     string        `json:"deploy_mode" db:"deploy_mode"`       // managed(A类) / external(B类纳管外部)
	ExternalURL    string        `json:"external_url" db:"external_url"`     // external 模式时外部应用访问地址
	Instances      []AppInstance `json:"instances,omitempty" db:"-"`         // 各环境部署实例（聚合展示，非列）
	CreatedAt      time.Time     `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at" db:"updated_at"`
}

// 应用接入模式常量（deploy_mode 列）。
// AppManaged（A 类，默认）：平台托管 —— 建 git 仓 + AI 编码 + 部署容器 + 供给库。
// AppExternal（B 类 ① 轻接入）：纳管外部已在运行的应用 —— 仅注册 + appgw 统一入口 + ops 按 external_url 探活；不动代码。
const (
	AppManaged  = "managed"
	AppExternal = "external"
)

// 环境常量：test=测试验证(prod 前)，prod=正式上线(用户访问)。
const (
	EnvTest = "test"
	EnvProd = "prod"
)

// IsValidEnv 合法环境校验。
func IsValidEnv(env string) bool { return env == EnvTest || env == EnvProd }

// EnvVar 应用运行时环境变量（部署时 docker run -e 注入；is_secret 时接口 mask 显示，不泄露）。
type EnvVar struct {
	ID        string    `json:"id" db:"id"`
	AppID     string    `json:"app_id" db:"app_id"`
	Key       string    `json:"key" db:"key"`
	Value     string    `json:"value" db:"value"`
	IsSecret  bool      `json:"is_secret" db:"is_secret"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
}

// 发布→部署 test（验证）；「上线」→部署 prod（用户访问）。两环境独立镜像/容器/端口/版本。
type AppInstance struct {
	ID            string    `json:"id" db:"id"`
	AppID         string    `json:"app_id" db:"app_id"`
	Env           string    `json:"env" db:"env"` // test / prod
	Image         string    `json:"image,omitempty" db:"image"`
	ContainerName string    `json:"container_name,omitempty" db:"container_name"`
	HostPort      int       `json:"host_port,omitempty" db:"host_port"`
	URL           string    `json:"url,omitempty" db:"url"`
	Version       int       `json:"version,omitempty" db:"version"`
	Status        string    `json:"status" db:"status"` // registered/building/running/stopped/failed
	LastError     string    `json:"last_error,omitempty" db:"last_error"`
	BuildLog      string    `json:"build_log,omitempty" db:"build_log"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}
