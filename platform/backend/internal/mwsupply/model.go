// Package mwsupply 是「中间件依赖供给」限界上下文 ——
// 按应用声明的依赖（仓库根 .anp/deps.yaml，由 opencode 适配回写）供给中间件连接信息并注入 env。
// P1 仅 bind_existing（绑定部署机已运行服务，如 .28 的 yxt-redis/yxt-milvus）；
// shared（共享实例+隔离 token）/ dedicated（每 app 专属容器）见 P2/P3。
//
// 注入经 appdeploy_env 表（source=platform），由 appdeploy 现有 docker run -e 链路消费，
// 部署主流程零改造。范式参照 pgsupply（DATABASE_URL）。
package mwsupply

import "time"

// 供给策略。
const (
	ModeBindExisting = "bind_existing" // 绑定部署机已运行服务（导入项目最常见，.28 redis/milvus）
	ModeShared       = "shared"        // ANP 级共享实例 + 每 app 隔离 token（P2）
	ModeDedicated    = "dedicated"     // 每 app 专属容器（P3）
)

// 绑定状态。
const (
	StatusDeclared = "declared" // 清单已声明，未供给
	StatusBound    = "bound"    // 已供给，env 已写
	StatusFailed   = "failed"   // 供给失败（无实例/策略未实现/写 env 错）
)

// ServiceInstance 可绑定的中间件实例（注册表：bind_existing 目标 / shared 池 / dedicated 供给出来的）。
type ServiceInstance struct {
	ID             string   `json:"id" db:"id"`
	ProjectSpaceID *string  `json:"project_space_id,omitempty" db:"project_space_id"` // NULL=平台全局
	Kind           string   `json:"kind" db:"kind"`                                   // redis/milvus/...
	Name           string   `json:"name" db:"name"`
	SupplyMode     string   `json:"supply_mode" db:"supply_mode"`
	Host           string   `json:"host" db:"host"`
	Port           int      `json:"port" db:"port"`
	AuthRef        string   `json:"auth_ref,omitempty" db:"auth_ref"`       // 密码/token 引用（明文，同 pgsupply I1 债）
	Isolation      string   `json:"isolation,omitempty" db:"isolation"`     // raw jsonb text（shared 用）
	Status         string   `json:"status" db:"status"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// ServiceBinding 每应用对某中间件的绑定（声明 + 供给结果）。
type ServiceBinding struct {
	ID                string    `json:"id" db:"id"`
	AppID             string    `json:"app_id" db:"app_id"`
	ProjectSpaceID    string    `json:"project_space_id" db:"project_space_id"`
	ServiceKind       string    `json:"service_kind" db:"service_kind"`
	Strategy          string    `json:"strategy" db:"strategy"`
	ServiceInstanceID string    `json:"service_instance_id,omitempty" db:"service_instance_id"`
	IsolationToken    string    `json:"isolation_token,omitempty" db:"isolation_token"` // redis db号 / milvus collection 前缀
	EnvKey            string    `json:"env_key" db:"env_key"`                           // REDIS_ADDR / MILVUS_ADDR / ...
	Status            string    `json:"status" db:"status"`
	LastError         string    `json:"last_error,omitempty" db:"last_error"`
	CreatedAt         time.Time `json:"created_at" db:"created_at"`
	UpdatedAt         time.Time `json:"updated_at" db:"updated_at"`
}
