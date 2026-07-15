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
	ID             string    `json:"id" db:"id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	Name           string    `json:"name" db:"name"`                 // 应用名（也是镜像/容器名前缀）
	RepoDir        string    `json:"repo_dir" db:"repo_dir"`         // docker 守护进程可见的源码路径（含 Dockerfile）
	InternalPort   int       `json:"internal_port" db:"internal_port"` // 应用容器内监听端口（Dockerfile EXPOSE）
	Image          string    `json:"image" db:"image"`               // 镜像引用 appdeploy/<name>:v<n>
	ContainerName  string    `json:"container_name" db:"container_name"`
	HostPort       int       `json:"host_port" db:"host_port"`       // 分配的宿主端口
	URL            string    `json:"url" db:"url"`                   // http://<host>:<host_port>
	Version        int       `json:"version" db:"version"`           // 构建版本号
	Status         string    `json:"status" db:"status"`             // registered/building/running/stopped/failed
	LastError      string    `json:"last_error,omitempty" db:"last_error"`
	BuildLog       string    `json:"build_log,omitempty" db:"build_log"` // 最近一次构建输出摘要
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}
