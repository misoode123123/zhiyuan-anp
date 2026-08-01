package mwsupply

import "fmt"

// EnvKeyFor 把 service kind 映射到注入容器的 env key。
func EnvKeyFor(kind string) string {
	switch kind {
	case "redis":
		return "REDIS_ADDR"
	case "milvus":
		return "MILVUS_ADDR"
	default:
		return kind + "_ADDR"
	}
}

// ConnStr 构造连接地址串（P1 bind_existing：host:port；无鉴权即裸地址）。
// redis：REDIS_ADDR=host:port；milvus：MILVUS_ADDR=host:port。
// 隔离 token（shared 的 db号/前缀）/鉴权（auth_ref）见 P2/P3 扩展。
func ConnStr(inst *ServiceInstance) string {
	return fmt.Sprintf("%s:%d", inst.Host, inst.Port)
}
