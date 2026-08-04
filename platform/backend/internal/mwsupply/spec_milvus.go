package mwsupply

import (
	"context"
	"fmt"
)

// milvusSpec 构造 milvus 的 KindSpec。
//   - AllocSharedToken 逐字搬自原 supply.go:allocMilvusPrefix（随机前缀 + ClaimSharedToken + 撞号重生）；
//   - dedicated 三件套接 docker。ReadyDedicated 经 docker 探针（alpine wget /healthz），不依赖 host。
//
// 零行为变化：函数体与重构前 supply.go 的对应段一致。
func milvusSpec(st *Store, docker MWDockerRunner) KindSpec {
	return KindSpec{
		Kind: "milvus", DisplayName: "Milvus", AddrEnv: "MILVUS_ADDR", Token: TokenCollectionPrefix,
		PortRange:     func() (int, int) { return milvusPortMin, milvusPortMax },
		ContainerName: func(short string) string { return "mwmilvus-" + short },
		LaunchDedicated: func(ctx context.Context, base string, port int) (string, error) {
			return "", docker.RunMilvusStack(ctx, base, port)
		},
		// host 忽略：milvus 经 docker socket 起 alpine 探针在专属网络上 wget /healthz，不走 host:port。
		ReadyDedicated: func(ctx context.Context, _, base string, _ int, _ string) error {
			return docker.MilvusReady(ctx, base, milvusReadyTimeout)
		},
		CleanupDedicated: func(ctx context.Context, base string) error { return docker.RmMilvusStack(ctx, base) },
		SharedEnv: func(token string, _ *ServiceInstance) []EnvKV {
			return []EnvKV{{Key: "MILVUS_COLLECTION_PREFIX", Value: token}}
		},
		DedicatedEnv: func(_ string) []EnvKV { return nil },
		// AllocSharedToken：逐字搬 supply.go:allocMilvusPrefix（随机前缀 + ClaimSharedToken + 撞号重生）。
		AllocSharedToken: func(ctx context.Context, appID, psID, instID string, inst *ServiceInstance) (string, error) {
			allocated, _ := st.AllocatedTokens(ctx, instID)
			taken := make(map[string]bool, len(allocated))
			for _, t := range allocated {
				taken[t] = true
			}
			for attempts := 0; attempts < 4; attempts++ {
				token := genMilvusPrefix()
				if taken[token] {
					continue
				}
				err := st.ClaimSharedToken(ctx, appID, psID, "milvus", instID, token, "MILVUS_ADDR")
				if err == nil {
					return token, nil
				}
				if !isUniqueViolation(err) {
					return "", err
				}
				taken[token] = true
			}
			return "", fmt.Errorf("milvus 前缀分配重试用尽（并发撞号）")
		},
	}
}
