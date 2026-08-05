package appdeploy

// DepDeclaration 应用对单个中间件的依赖声明 + 供给结果（UI/API 用）。
type DepDeclaration struct {
	Kind     string `json:"kind"`               // redis / milvus
	Strategy string `json:"strategy"`           // bind_existing / shared / dedicated
	Status   string `json:"status"`             // declared / bound / failed
	Instance string `json:"instance,omitempty"` // 供给的 service_instance_id（bound 时）
	Token    string `json:"token,omitempty"`    // 隔离 token（shared：db号/前缀）
	Error    string `json:"error,omitempty"`    // failed 时的 last_error
}

// StrategyOption 策略选项（catalog 用，UI 渲染）。
type StrategyOption struct {
	Name string `json:"name"`
	Desc string `json:"desc"`
}

// CatalogInstance 可见中间件实例（catalog 用）。
type CatalogInstance struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	SupplyMode string `json:"supply_mode"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
}

// DepsCatalog 依赖勾选器所需选项集合。
type DepsCatalog struct {
	Kinds      []string          `json:"kinds"`
	Strategies []StrategyOption  `json:"strategies"`
	Instances  []CatalogInstance `json:"instances"`
}

// validDepKinds 平台支持的依赖 kind（PutDeps 校验用）。
// 须与 mwsupply KindSpec 注册表 + DepsCatalog 的 Kinds 列表保持一致（redis/milvus/pg）。
// 新增 kind 时三处同步：此处、mwsupply BuildSpecs 注册、DepsCatalog 返回。
var validDepKinds = map[string]bool{"redis": true, "milvus": true, "pg": true}
