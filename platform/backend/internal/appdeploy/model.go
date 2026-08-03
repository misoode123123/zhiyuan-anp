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
	HostPort       int           `json:"host_port" db:"host_port"`               // 分配的宿主端口
	DeployHost     string        `json:"deploy_host,omitempty" db:"deploy_host"` // 部署节点（空=本地 .28，如 tcp://10.10.0.30:2375）
	URL            string        `json:"url" db:"url"`                           // http://<host>:<host_port>
	Version        int           `json:"version" db:"version"`                   // 构建版本号
	Status         string        `json:"status" db:"status"`                     // registered/building/running/stopped/failed
	LastError      string        `json:"last_error,omitempty" db:"last_error"`
	BuildLog       string        `json:"build_log,omitempty" db:"build_log"`     // 最近一次构建输出摘要
	DeployMode     string        `json:"deploy_mode" db:"deploy_mode"`           // managed(A类) / external(B类纳管外部)
	AppKind        string        `json:"app_kind" db:"app_kind"`                 // web/desktop/mobile/cli/service，默认 web
	NetworkMode    string        `json:"network_mode" db:"network_mode"`         // bridge(默认) / host(需 gatekeeper/admin，op app.net.host)
	ExternalURL    string        `json:"external_url" db:"external_url"`         // external 模式时外部应用访问地址
	ImportSource   string        `json:"import_source" db:"import_source"`       // ''/git/dir
	ImportRef      string        `json:"import_ref" db:"import_ref"`             // git=url / dir=来源标识
	ImportedAt     *time.Time    `json:"imported_at,omitempty" db:"imported_at"` // 导入完成时间，进行中 nil
	Instances      []AppInstance `json:"instances,omitempty" db:"-"`             // 各环境部署实例（聚合展示，非列）
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

// 应用形态（app_kind 列）。与 deploy_mode 正交：web 走现有容器链路，其余走预置构建容器出产物。
const (
	AppKindWeb      = "web"      // 现有链路（docker build→容器→URL）
	AppKindDesktop  = "desktop"  // 桌面安装包（exe/dmg/AppImage）
	AppKindMobile   = "mobile"   // 移动安装包（apk/ipa）
	AppKindCLI      = "cli"      // 命令行二进制
	AppKindService  = "service"  // 后端服务（可能非 HTTP，本期等同 web）
	AppKindHeadless = "headless" // 无端口长驻进程(bot/worker),进程存活健康监控
)

// 非常 web 形态产物就绪态（web 链路不出现此状态）。
const StatusBuilt = "built"

// 导入来源（import_source 列）。
const (
	ImportSourceGit = "git" // 远程仓库 clone
	ImportSourceDir = "dir" // 本地目录（zip 上传 或 服务器目录复制）
)

// StatusImporting 导入进行中态（复用 status 列）。
const StatusImporting = "importing"

// 环境常量：test=测试验证(prod 前)，prod=正式上线(用户访问)。
const (
	EnvTest = "test"
	EnvProd = "prod"
)

// IsValidEnv 合法环境校验。
func IsValidEnv(env string) bool { return env == EnvTest || env == EnvProd }

// EnvVar 应用运行时环境变量（部署时 docker run -e 注入；is_secret 时接口 mask 显示，不泄露）。
// Source: user(用户面板填) / platform(平台 pgsupply/mwsupply 注入，部署 reconcile 保障，前端只读)。
type EnvVar struct {
	ID        string    `json:"id" db:"id"`
	AppID     string    `json:"app_id" db:"app_id"`
	Key       string    `json:"key" db:"key"`
	Value     string    `json:"value" db:"value"`
	IsSecret  bool      `json:"is_secret" db:"is_secret"`
	Source    string    `json:"source" db:"source"`
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
	RestartCount  int       `json:"restart_count" db:"restart_count"` // 上次观测的 docker RestartCount(reconcile 增量判定基准)
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
	UpdatedAt     time.Time `json:"updated_at" db:"updated_at"`
}

// Artifact 一次构建产出的一个产物文件（如一个 exe 或 apk）。与 Application 一对多。
type Artifact struct {
	ID            string    `json:"id" db:"id"` // art_xxx
	ApplicationID string    `json:"application_id" db:"application_id"`
	BuildVersion  int       `json:"build_version" db:"build_version"`
	AppKind       string    `json:"app_kind" db:"app_kind"`
	Platform      string    `json:"platform" db:"platform"` // windows/macos/linux/android/ios/multi
	Arch          string    `json:"arch" db:"arch"`         // x64/arm64/x86/universal/multi
	Filename      string    `json:"filename" db:"filename"`
	SizeBytes     int64     `json:"size_bytes" db:"size_bytes"`
	SHA256        string    `json:"sha256" db:"sha256"`
	StorageKey    string    `json:"storage_key" db:"storage_key"`
	ContentType   string    `json:"content_type" db:"content_type"`
	CreatedAt     time.Time `json:"created_at" db:"created_at"`
}

// ArtifactOutput Builder 产出的产物描述（构建容器内路径 + 平台/架构元数据），
// 由 Builder 收集后交给产物层上传 MinIO + 写 Artifact 记录。
type ArtifactOutput struct {
	Platform    string // windows/macos/linux/android/ios/multi
	Arch        string // x64/arm64/x86/universal/multi
	Filename    string
	ContentType string
	SrcPath     string // 构建容器内产物文件绝对路径
}

// BuildConfig 某形态的构建配置（镜像/命令/产物目录/脚手架），存 appdeploy_build_config 表。
type BuildConfig struct {
	AppKind      string    `json:"app_kind" db:"app_kind"`
	BuildImage   string    `json:"build_image" db:"build_image"`
	BuildCommand string    `json:"build_command" db:"build_command"`
	ArtifactDir  string    `json:"artifact_dir" db:"artifact_dir"`
	Scaffold     string    `json:"scaffold" db:"scaffold"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
}
