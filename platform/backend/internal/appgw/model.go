// Package appgw 是「应用 API 网关」限界上下文 ——
// 把产出应用的对外访问从裸端口（http://<host>:9xxx）收敛为统一前缀
// /apps/<app_code>/，经 backend 反代 + 平台鉴权 + 身份头注入。
//
// 工作模型（阶段 2a：appgw 并入 backend，不拆独立服务）：
//
//	nginx :8088  /apps/  →  backend(Go) appgw.ReverseProxy
//	  → 读 appdeploy_route 取 upstream_host:upstream_port
//	  → auth_required=true 时验平台 JWT，401 拦截未登录
//	  → 注入 X-User / X-Project-Space-Id / X-Trace-Id
//	  → httputil.ReverseProxy 转发到应用容器
//
// appgw 不 import appdeploy（单向依赖：appdeploy → appgw，部署成功后写路由表）。
package appgw

import "time"

// Route appdeploy_route 表的一行：app_code → upstream 容器映射。
// appgw 消费此结构做反向代理。
type Route struct {
	ID             string    `json:"id" db:"id"`
	AppID          string    `json:"app_id" db:"app_id"`
	ProjectSpaceID string    `json:"project_space_id" db:"project_space_id"`
	AppCode        string    `json:"app_code" db:"app_code"` // URL 路径段（= app_id）
	Env            string    `json:"env" db:"env"`           // test / prod
	UpstreamHost   string    `json:"upstream_host" db:"upstream_host"`
	UpstreamPort   int       `json:"upstream_port" db:"upstream_port"`
	Status         string    `json:"status" db:"status"` // active / inactive
	AuthRequired   bool      `json:"auth_required" db:"auth_required"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
	UpdatedAt      time.Time `json:"updated_at" db:"updated_at"`
}

// 路由状态。
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

// 默认环境：URL 形态 /apps/<code>/ → prod；/apps/<code>~test/ → test。
const DefaultEnv = "prod"
