package mwsupply

import "context"

// EnvWriter 写应用 env（由 appdeploy.Store 实现，避免 mwsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
}

// Reconciler 中间件依赖供给（P1：bind_existing）。best-effort：失败记 binding，不阻塞部署。
type Reconciler struct {
	store *Store
	env   EnvWriter
}

// NewReconciler 构造。env 传 appdeploy.Store（满足 EnvWriter）。
func NewReconciler(store *Store, env EnvWriter) *Reconciler {
	return &Reconciler{store: store, env: env}
}

// Reconcile 读 repoDir 的 .anp/deps.yaml → 对每个声明服务按策略供给 → 写 env + binding。
// 幂等（binding 已 bound 且实例在则重写覆盖，无副作用）。读清单失败=空清单（不报错）。
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

// supplyOne 供给单个依赖。P1 仅 bind_existing；shared/dedicated 标 failed（P2/P3 实现）。
func (r *Reconciler) supplyOne(ctx context.Context, appID, psID string, dep DepService) {
	strategy := dep.Strategy
	if strategy == "" {
		strategy = ModeBindExisting
	}
	envKey := EnvKeyFor(dep.Kind)
	mkBind := func(status, instID, lastErr string) {
		_ = r.store.UpsertBinding(ctx, &ServiceBinding{
			AppID: appID, ProjectSpaceID: psID, ServiceKind: dep.Kind,
			Strategy: strategy, ServiceInstanceID: instID, EnvKey: envKey,
			Status: status, LastError: lastErr,
		})
	}
	if strategy != ModeBindExisting {
		mkBind(StatusFailed, "", "策略 "+strategy+" 暂未实现（P1 仅 bind_existing）")
		return
	}
	inst, err := r.store.LookupBindExisting(ctx, psID, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "无可绑定的 "+dep.Kind+" 实例")
		return
	}
	connStr := ConnStr(inst)
	isSecret := inst.AuthRef != ""
	if err := r.env.UpsertEnv(ctx, appID, envKey, connStr, isSecret, "platform"); err != nil {
		mkBind(StatusFailed, inst.ID, err.Error())
		return
	}
	mkBind(StatusBound, inst.ID, "")
}
