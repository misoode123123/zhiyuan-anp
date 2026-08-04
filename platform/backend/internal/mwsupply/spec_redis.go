package mwsupply

import (
	"context"
	"fmt"
	"strconv"
)

// redisSpec 构造 redis 的 KindSpec。
//   - AllocSharedToken 逐字搬自原 supply.go:allocRedisDB + claimWithRetry（db 号池 + flush + 撞号重试），
//     闭包捕获 st/flusher；
//   - dedicated 三件套接 docker/ready。ReadyDedicated 接 host（=r.host）拨 AUTH+PING。
//
// 零行为变化：函数体与重构前 supply.go 的对应段一致。
func redisSpec(st *Store, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner) KindSpec {
	return KindSpec{
		Kind: "redis", DisplayName: "Redis", AddrEnv: "REDIS_ADDR", Token: TokenDBNumber,
		PortRange:     func() (int, int) { return mwPortMin, mwPortMax },
		ContainerName: func(short string) string { return "mwredis-" + short },
		LaunchDedicated: func(ctx context.Context, name string, port int) (string, error) {
			pwd := genPassword()
			if err := docker.RunRedisContainer(ctx, name, pwd, port); err != nil {
				return "", err
			}
			return pwd, nil
		},
		// host 由 supplyDedicated 传入（=r.host）；redis 用来 AUTH+PING 拨号。
		ReadyDedicated: func(ctx context.Context, host, _ string, port int, authRef string) error {
			readyCtx, cancel := context.WithTimeout(ctx, readyPingTimeout)
			defer cancel()
			return ready.Ping(readyCtx, host, port, authRef)
		},
		CleanupDedicated: func(ctx context.Context, name string) error { return docker.RmForce(ctx, name) },
		SharedEnv: func(token string, inst *ServiceInstance) []EnvKV {
			out := []EnvKV{{Key: "REDIS_DB", Value: token}}
			if inst != nil && inst.AuthRef != "" {
				out = append(out, EnvKV{Key: "REDIS_PASSWORD", Value: inst.AuthRef, IsSecret: true})
			}
			return out
		},
		DedicatedEnv: func(authRef string) []EnvKV {
			return []EnvKV{{Key: "REDIS_PASSWORD", Value: authRef, IsSecret: true}}
		},
		// AllocSharedToken：逐字搬 supply.go:allocRedisDB（ParseDBRange + pickLowestFree + claimWithRetry）。
		AllocSharedToken: func(ctx context.Context, appID, psID, instID string, inst *ServiceInstance) (string, error) {
			lo, hi, ok := ParseDBRange(inst.Isolation)
			if !ok {
				return "", fmt.Errorf("shared 实例 isolation 缺 db_range")
			}
			allocated, _ := st.AllocatedTokens(ctx, instID)
			first, found := pickLowestFree(lo, hi, allocated)
			if !found {
				return "", fmt.Errorf("shared redis db 号耗尽（池 %d-%d）", lo, hi)
			}
			return redisClaimWithRetry(ctx, st, flusher, appID, psID, inst, lo, hi, first, allocated)
		},
	}
}

// redisClaimWithRetry 逐字搬自原 supply.go:claimWithRetry（flush + ClaimSharedToken + 撞唯一索引换号重试）。
// 签名从 Reconciler 方法外提为包函数，加 st/flusher 入参（闭包捕获值的等价）。
//
// flush 日志：原方法用 r.log.Warn 记 flush 失败（best-effort，非正确性所需）；搬迁后无 r.log，静默继续。
// 不为 best-effort 日志单透传 *zap.Logger（见 task brief「其他决议」）。
func redisClaimWithRetry(ctx context.Context, st *Store, flusher DBFlusher,
	appID, psID string, inst *ServiceInstance, lo, hi int, first string, allocated []string) (string, error) {
	token := first
	seen := append([]string{}, allocated...)
	for attempts := 0; attempts <= (hi - lo + 1); attempts++ {
		dbNum, _ := strconv.Atoi(token)
		// flush best-effort：失败静默继续 claim（首次分配的 db 号本就干净；重分配卫生留给 prod 网络可达性）。
		_ = flusher.FlushDB(ctx, inst.Host, inst.Port, inst.AuthRef, dbNum)
		err := st.ClaimSharedToken(ctx, appID, psID, "redis", inst.ID, token, "REDIS_ADDR")
		if err == nil {
			return token, nil
		}
		if !isUniqueViolation(err) {
			return "", err // 非冲突，真错
		}
		seen = append(seen, token)
		next, found := pickLowestFree(lo, hi, seen)
		if !found {
			return "", fmt.Errorf("shared redis db 号耗尽（并发重试）")
		}
		token = next
	}
	return "", fmt.Errorf("claim 重试用尽")
}
