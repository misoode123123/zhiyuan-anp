package mwsupply

// BuildSpecs 构造并注册 redis/milvus 的 KindSpec（捕获 store/flusher/ready/docker）。
// NewReconciler 末尾调一次。加新 kind（如 pg）= 这里加一行 + 一个 spec 构造函数。
func BuildSpecs(store *Store, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner) {
	RegisterKind(redisSpec(store, flusher, ready, docker))
	RegisterKind(milvusSpec(store, docker))
}
