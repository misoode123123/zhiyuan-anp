package mwsupply

import "zhiyuan-anp/platform/backend/internal/pgsupply"

// BuildSpecs 构造并注册 redis/milvus/pg 的 KindSpec（捕获 store/flusher/ready/docker/pgProv）。
// NewReconciler 末尾调一次。加新 kind = 这里加一行 + 一个 spec 构造函数。
func BuildSpecs(store *Store, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, pgProv *pgsupply.Provisioner) {
	RegisterKind(redisSpec(store, flusher, ready, docker))
	RegisterKind(milvusSpec(store, docker))
	RegisterKind(pgSpec(pgProv))
}
