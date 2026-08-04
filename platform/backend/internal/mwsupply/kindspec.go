package mwsupply

import "context"

// TokenSemantics 描述 shared 策略下"实例内隔离 token"的语义（文档/诊断用；实际分配由 AllocSharedToken）。
type TokenSemantics string

const (
	TokenDBNumber         TokenSemantics = "db-number"         // redis：db 号池
	TokenCollectionPrefix TokenSemantics = "collection-prefix" // milvus：collection 前缀
	// 未来：TokenDatabaseName(pg) / TokenTopicPrefix(kafka) ...
)

// EnvKV 单条注入 env（source=platform）。
type EnvKV struct {
	Key      string
	Value    string
	IsSecret bool
}

// KindSpec 每 kind 的供给规格。registry 驱动：供给主逻辑查 spec 分派，无 kind switch。
// 依赖（store/flusher/ready/docker）由 BuildSpecs 在构造闭包时捕获。
type KindSpec struct {
	Kind        string
	DisplayName string
	AddrEnv     string // 主连接 env 键：REDIS_ADDR / MILVUS_ADDR
	Token       TokenSemantics

	// dedicated 三件套（起 / 探 / 收）
	PortRange        func() (int, int)                                                          // 端口池上下界
	ContainerName    func(short string) string                                                 // dedicated 容器名
	LaunchDedicated  func(ctx context.Context, name string, port int) (authRef string, err error)
	ReadyDedicated   func(ctx context.Context, name string, port int, authRef string) error
	CleanupDedicated func(ctx context.Context, name string) error

	// shared：为某实例分配一个隔离 token（per-kind 策略；redis=db 号池 / milvus=随机前缀）。
	// 实现内部用 store.ClaimSharedToken 写库；返回 token + nil。
	AllocSharedToken func(ctx context.Context, appID, psID, instID string, inst *ServiceInstance) (string, error)

	// env 派生（主连接 env 由调用方写 spec.AddrEnv，这里只给 token/authRef 派生项）
	SharedEnv    func(token string, inst *ServiceInstance) []EnvKV // redis→[REDIS_DB(,+REDIS_PASSWORD)]；milvus→[MILVUS_COLLECTION_PREFIX]
	DedicatedEnv func(authRef string) []EnvKV                       // redis→[REDIS_PASSWORD]；milvus→[]
}

var kindRegistry = map[string]KindSpec{}

func RegisterKind(s KindSpec) { kindRegistry[s.Kind] = s }

func LookupKind(kind string) (KindSpec, bool) {
	s, ok := kindRegistry[kind]
	return s, ok
}
