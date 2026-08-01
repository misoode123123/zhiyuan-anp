package mwsupply

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// EnvWriter 写应用 env（由 appdeploy.Store 实现，避免 mwsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
}

// Reconciler 中间件依赖供给。best-effort：失败记 binding，不阻塞部署。
type Reconciler struct {
	store   *Store
	env     EnvWriter
	flusher DBFlusher // shared 重分配时清空 redis db（Task 3）
}

// NewReconciler 构造。env 传 appdeploy.Store（满足 EnvWriter）；flusher 传 NewRedisFlusher()（测试传 fake）。
func NewReconciler(store *Store, env EnvWriter, flusher DBFlusher) *Reconciler {
	return &Reconciler{store: store, env: env, flusher: flusher}
}

// Reconcile 读 repoDir 的 .anp/deps.yaml → 对每个声明服务按策略供给 → 写 env + binding。
// 幂等（binding 已 bound 且同实例则复用 token 不 flush）。读清单失败=空清单（不报错）。
// 总不返回错（best-effort，不阻塞部署）。
func (r *Reconciler) Reconcile(ctx context.Context, appID, psID, repoDir string) error {
	m, err := LoadDepsManifest(repoDir)
	if err != nil || m == nil {
		return nil
	}
	for _, dep := range m.Services {
		if dep.Kind == "" {
			continue
		}
		r.supplyOne(ctx, appID, psID, dep)
	}
	return nil
}

// supplyOne 供给单个依赖。bind_existing（P1）/ shared（P2 redis）；dedicated 暂 failed（P3）。
func (r *Reconciler) supplyOne(ctx context.Context, appID, psID string, dep DepService) {
	strategy := dep.Strategy
	if strategy == "" {
		strategy = ModeBindExisting
	}
	envKey := EnvKeyFor(dep.Kind)
	// mkBind 幂等 upsert binding（token：bind_existing 空；shared 填分配号）。
	mkBind := func(status, instID, token, lastErr string) {
		_ = r.store.UpsertBinding(ctx, &ServiceBinding{
			AppID: appID, ProjectSpaceID: psID, ServiceKind: dep.Kind,
			Strategy: strategy, ServiceInstanceID: instID, IsolationToken: token,
			EnvKey: envKey, Status: status, LastError: lastErr,
		})
	}

	if strategy == ModeShared {
		r.supplyShared(ctx, appID, psID, dep, mkBind)
		return
	}
	if strategy != ModeBindExisting {
		mkBind(StatusFailed, "", "", "策略 "+strategy+" 暂未实现（仅 bind_existing/shared）")
		return
	}

	// —— bind_existing（P1，不动）——
	inst, err := r.store.LookupBindExisting(ctx, psID, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无可绑定的 "+dep.Kind+" 实例")
		return
	}
	connStr := ConnStr(inst)
	isSecret := inst.AuthRef != ""
	if err := r.env.UpsertEnv(ctx, appID, envKey, connStr, isSecret, "platform"); err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	mkBind(StatusBound, inst.ID, "", "")
}

// supplyShared shared redis 供给：复用判定（幂等不换号不 flush）/ 新分配（最小空闲号 → flush → claim）。
func (r *Reconciler) supplyShared(ctx context.Context, appID, psID string, dep DepService,
	mkBind func(status, instID, token, lastErr string)) {
	inst, err := r.store.LookupShared(ctx, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无 shared "+dep.Kind+" 实例")
		return
	}
	// 复用：同 app 已 bound 同实例同 token → 不换号、不 flush（保数据）、重写 env。
	if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
		existing.Status == StatusBound && existing.IsolationToken != "" && existing.ServiceInstanceID == inst.ID {
		r.writeSharedEnv(ctx, appID, inst, existing.IsolationToken)
		mkBind(StatusBound, inst.ID, existing.IsolationToken, "")
		return
	}
	// 新分配
	lo, hi, ok := ParseDBRange(inst.Isolation)
	if !ok {
		mkBind(StatusFailed, inst.ID, "", "shared 实例 isolation 缺 db_range")
		return
	}
	allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
	first, found := pickLowestFree(lo, hi, allocated)
	if !found {
		mkBind(StatusFailed, inst.ID, "", fmt.Sprintf("shared redis db 号耗尽（池 %d-%d）", lo, hi))
		return
	}
	token, err := r.claimWithRetry(ctx, appID, psID, dep.Kind, inst, lo, hi, first, allocated)
	if err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	r.writeSharedEnv(ctx, appID, inst, token)
	mkBind(StatusBound, inst.ID, token, "")
}

// claimWithRetry flush 后原子 claim；撞唯一索引（并发抢同号）→ 刷新占用集换号重试，有界 ≤ 池大小。
// 返回最终 claim 到的 token。
func (r *Reconciler) claimWithRetry(ctx context.Context, appID, psID, kind string, inst *ServiceInstance,
	lo, hi int, first string, allocated []string) (string, error) {
	token := first
	seen := append([]string{}, allocated...)
	for attempts := 0; attempts <= (hi - lo + 1); attempts++ {
		dbNum, _ := strconv.Atoi(token)
		if ferr := r.flusher.FlushDB(ctx, inst.Host, inst.Port, inst.AuthRef, dbNum); ferr != nil {
			return "", fmt.Errorf("flush db %s 失败: %w", token, ferr)
		}
		err := r.store.ClaimSharedToken(ctx, appID, psID, kind, inst.ID, token, EnvKeyFor(kind))
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

// writeSharedEnv 写 REDIS_ADDR + REDIS_DB（+ REDIS_PASSWORD 若鉴权），均 source=platform。
func (r *Reconciler) writeSharedEnv(ctx context.Context, appID string, inst *ServiceInstance, token string) {
	kindUp := strings.ToUpper(inst.Kind) // redis→REDIS
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_ADDR", ConnStr(inst), false, "platform")
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_DB", token, false, "platform")
	if inst.AuthRef != "" {
		_ = r.env.UpsertEnv(ctx, appID, kindUp+"_PASSWORD", inst.AuthRef, true, "platform")
	}
}
