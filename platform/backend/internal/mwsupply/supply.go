package mwsupply

import (
	"context"
	"fmt"
	"strings"

	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/pgsupply"
)

// EnvWriter 写/删应用 env（由 appdeploy.Store 实现，避免 mwsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
	DeleteEnv(ctx context.Context, appID, key string) error // P6：声明移除时删注入的 platform env
}

// DedicatedQuotaChecker 专属实例数配额检查（quota.Service 实现）。
// nil=不强制（开发/测试或未注入 quota）。
type DedicatedQuotaChecker interface {
	CheckDedicatedInstances(ctx context.Context, psID string) error
}

// Reconciler 中间件依赖供给。best-effort：失败记 binding，不阻塞部署。
type Reconciler struct {
	store    *Store
	env      EnvWriter
	flusher  DBFlusher     // 捕获进 spec 闭包（BuildSpecs 时）；此处仅 NewReconciler 接线保留
	ready    ReadyChecker  // 捕获进 spec 闭包（BuildSpecs 时）；此处仅 NewReconciler 接线保留
	docker   MWDockerRunner // dedicated 容器管理（run/rm）
	host     string        // AppDeployHost（dedicated REDIS_ADDR host + 就绪检测拨号）
	dedQuota DedicatedQuotaChecker // P4：dedicated 起容器前查配额；nil=不强制
	log      *zap.Logger   // 可选；flush best-effort 失败记 Warn（nil 安全）
}

// NewReconciler 构造。末尾调 BuildSpecs 注册 redis/milvus/pg 的 KindSpec（闭包捕获 store/env/flusher/ready/docker/pgProv/pgDed）。
//   env 传 appdeploy.Store（满足 EnvWriter）；
//   flusher+ready 可传同一 *redisFlusher（NewRedisFlusher 同时满足 DBFlusher+ReadyChecker）；
//   docker 传 NewOSDocker()（测试传 fake）；host 为 AppDeployHost；
//   pgProv 给 pg shared 自管供给；pgDed 给 pg dedicated（*pgsupply.InstanceManager 满足 PgDedicatedRunner）；
//   dedQuota 给 dedicated 配额检查（*quota.Service 满足 DedicatedQuotaChecker；nil=不强制）。
func NewReconciler(store *Store, env EnvWriter, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, host string, pgProv *pgsupply.Provisioner, pgDed PgDedicatedRunner, dedQuota DedicatedQuotaChecker) *Reconciler {
	r := &Reconciler{store: store, env: env, flusher: flusher, ready: ready, docker: docker, host: host, dedQuota: dedQuota}
	BuildSpecs(store, env, flusher, ready, docker, pgProv, pgDed)
	return r
}

// SetLogger 注入 logger（可选；main 装配时调，测试不调则 flush 失败静默）。
func (r *Reconciler) SetLogger(l *zap.Logger) { r.log = l }

// Reconcile 读该 app 的 binding 声明（DB）→ 逐个供给 → 写 env + binding。
// 幂等（binding 已 bound 且同实例则复用 token 不 flush）。总不返回错（best-effort，不阻塞部署）。
func (r *Reconciler) Reconcile(ctx context.Context, appID, psID string) error {
	binds, err := r.store.ListBindingsByApp(ctx, appID)
	if err != nil {
		return nil // best-effort：读声明失败不阻塞部署
	}
	deps := make([]DepService, 0, len(binds))
	for _, b := range binds {
		if b.ServiceKind == "" {
			continue
		}
		deps = append(deps, DepService{Kind: b.ServiceKind, Strategy: b.Strategy})
	}
	r.supplyAll(ctx, appID, psID, deps)
	return nil
}

// SeedFromManifest 读 repoDir/.anp/deps.yaml → 对每个声明种 declared binding（不覆盖已存在）。
// 导入时调：opencode 适配/手编的 .anp/deps.yaml 由此进入平台 DB。best-effort，无文件=空清单不报错。
func (r *Reconciler) SeedFromManifest(ctx context.Context, appID, psID, repoDir string) error {
	m, err := LoadDepsManifest(repoDir)
	if err != nil || m == nil {
		return nil
	}
	for _, dep := range m.Services {
		if dep.Kind == "" {
			continue
		}
		_ = r.store.DeclareIfAbsent(ctx, appID, psID, dep.Kind, dep.Strategy)
	}
	return nil
}

// supplyAll 按声明列表逐个供给（best-effort，幂等）。供给逻辑核心，被 Reconcile 与测试共用。
func (r *Reconciler) supplyAll(ctx context.Context, appID, psID string, deps []DepService) {
	for _, dep := range deps {
		if dep.Kind == "" {
			continue
		}
		r.supplyOne(ctx, appID, psID, dep)
	}
}

// supplyOne 供给单个依赖。spec 驱动分派：bind_existing / shared / dedicated。
// 未注册 kind（无 KindSpec）→ mkBind failed。bind_existing 的 env 键取 spec.AddrEnv（= EnvKeyFor 旧值）。
func (r *Reconciler) supplyOne(ctx context.Context, appID, psID string, dep DepService) {
	strategy := dep.Strategy
	if strategy == "" {
		strategy = ModeBindExisting
	}
	spec, ok := LookupKind(dep.Kind)
	// binding 行 env_key：注册 kind 用 spec.AddrEnv；未注册回退 EnvKeyFor（保持 binding 行可读）。
	envKey := EnvKeyFor(dep.Kind)
	if ok {
		envKey = spec.AddrEnv
	}
	// mkBind 幂等 upsert binding（token：bind_existing 空；shared 填分配号）。
	mkBind := func(status, instID, token, lastErr string) {
		_ = r.store.UpsertBinding(ctx, &ServiceBinding{
			AppID: appID, ProjectSpaceID: psID, ServiceKind: dep.Kind,
			Strategy: strategy, ServiceInstanceID: instID, IsolationToken: token,
			EnvKey: envKey, Status: status, LastError: lastErr,
		})
	}

	if !ok {
		mkBind(StatusFailed, "", "", "未注册 kind "+dep.Kind+"（无 KindSpec）")
		return
	}
	if strategy == ModeShared {
		r.supplyShared(ctx, appID, psID, dep, spec, mkBind)
		return
	}
	if strategy == ModeDedicated {
		r.supplyDedicated(ctx, appID, psID, dep, spec, mkBind)
		return
	}
	if strategy != ModeBindExisting {
		mkBind(StatusFailed, "", "", "策略 "+strategy+" 暂未实现（仅 bind_existing/shared/dedicated）")
		return
	}

	// —— bind_existing ——
	inst, err := r.store.LookupBindExisting(ctx, psID, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无可绑定的 "+dep.Kind+" 实例")
		return
	}
	// 默认 ConnStr(inst)=host:port（redis/milvus）；spec.ConnValue 非 nil 时用自管值（pg=AuthRef 完整 DSN）。
	connVal := ConnStr(inst)
	if spec.ConnValue != nil {
		connVal = spec.ConnValue(inst)
	}
	// 通用空连接值守卫（M-3）：ConnStr/ConnValue 派生值为空（如 pg 登记实例 AuthRef 空）→
	// 不写空 env、binding failed，避免静默注入空 DATABASE_URL/地址。
	if connVal == "" {
		mkBind(StatusFailed, inst.ID, "", "无可绑定的 "+dep.Kind+" 实例连接信息（AuthRef 空）")
		return
	}
	isSecret := inst.AuthRef != ""
	if err := r.env.UpsertEnv(ctx, appID, spec.AddrEnv, connVal, isSecret, "platform"); err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	mkBind(StatusBound, inst.ID, "", "")
}

// supplyShared shared 供给（spec 驱动）：
//  1. spec.SupplyShared != nil → 自管路径（pg：实例+库+role+env+明细自管；不走 LookupShared/ConnStr）。含 reuse 判定。
//  2. 否则默认路径：LookupShared 实例 → reuse 判定（幂等不换 token 不 flush）/ 新分配 spec.AllocSharedToken → env。
//
// redis：db 号池；milvus：随机 collection 前缀。token 分配逻辑封装在 spec 闭包内（见 spec_redis.go/spec_milvus.go）。
func (r *Reconciler) supplyShared(ctx context.Context, appID, psID string, dep DepService, spec KindSpec,
	mkBind func(status, instID, token, lastErr string)) {
	// pg-style 自管供给（spec.SupplyShared 自管全套：实例+库+role+env+明细；不走 LookupShared/ConnStr）。
	if spec.SupplyShared != nil {
		// reuse：binding 已 bound 且 IsolationToken 非空 → 不重复供给（pgsupply.Provision 虽幂等，仍避免无谓调用）。
		// 用 IsolationToken != "" 判已供给：兼容 pg 的空 instID（binding 不存 service_instance_id）与
		// redis/milvus 的非空 instID（此分支仅 SupplyShared kind 进入，redis/milvus 走下方默认路径不受影响）。
		if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
			existing.Status == StatusBound && existing.IsolationToken != "" {
			mkBind(StatusBound, existing.ServiceInstanceID, existing.IsolationToken, "")
			return
		}
		instID, token, err := spec.SupplyShared(ctx, appID, psID)
		if err != nil {
			mkBind(StatusFailed, "", "", err.Error())
			return
		}
		mkBind(StatusBound, instID, token, "")
		return
	}
	inst, err := r.store.LookupShared(ctx, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无 shared "+dep.Kind+" 实例")
		return
	}
	// 复用：同 app 已 bound 同实例同 token → 不换 token、不 flush（保数据）、重写 env。
	if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
		existing.Status == StatusBound && existing.IsolationToken != "" && existing.ServiceInstanceID == inst.ID {
		r.writeSpecEnv(ctx, appID, spec, inst, existing.IsolationToken)
		mkBind(StatusBound, inst.ID, existing.IsolationToken, "")
		return
	}
	// 新分配 + claim（spec 驱动）
	token, err := spec.AllocSharedToken(ctx, appID, psID, inst.ID, inst)
	if err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	r.writeSpecEnv(ctx, appID, spec, inst, token)
	mkBind(StatusBound, inst.ID, token, "")
}

// writeSpecEnv 写 shared env（spec 驱动），均 source=platform。
// 写 spec.AddrEnv=ConnStr + 遍历 spec.SharedEnv(token,inst)（redis→[REDIS_DB(,+REDIS_PASSWORD)] / milvus→[MILVUS_COLLECTION_PREFIX]）。
func (r *Reconciler) writeSpecEnv(ctx context.Context, appID string, spec KindSpec, inst *ServiceInstance, token string) {
	_ = r.env.UpsertEnv(ctx, appID, spec.AddrEnv, ConnStr(inst), false, "platform")
	for _, e := range spec.SharedEnv(token, inst) {
		_ = r.env.UpsertEnv(ctx, appID, e.Key, e.Value, e.IsSecret, "platform")
	}
}

// supplyDedicated dedicated 供给（spec 驱动）：复用判定（幂等）/ 新供给（端口→launch→ready→登记→env）。
// redis：1 容器 + AUTH+PING；milvus：专属网络 + milvus/etcd/minio 三容器 + /healthz 探针。
func (r *Reconciler) supplyDedicated(ctx context.Context, appID, psID string, dep DepService, spec KindSpec,
	mkBind func(status, instID, token, lastErr string)) {
	// 复用：同 app 已 bound dedicated 实例 → 不重启、不换端口、保数据。
	if b, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && b != nil &&
		b.Status == StatusBound && b.ServiceInstanceID != "" {
		if inst, ie := r.store.GetInstance(ctx, b.ServiceInstanceID); ie == nil && inst != nil && inst.Status == "active" {
			// spec.SupplyDedicated 自管 kind（pg）：env 由首次供给正确写入，无法从 ConnStr/AuthRef 重建
			// （ConnStr=host:port 会覆盖有效 app-role DSN）→ 跳过 env 重写，仅续 binding（保 token）。
			// 镜像 SupplyShared reuse 分支：自管 env kind 同样不重写 env。
			if spec.SupplyDedicated != nil {
				mkBind(StatusBound, inst.ID, b.IsolationToken, "")
				return
			}
			r.writeDedicatedEnvSpec(ctx, appID, spec, inst)
			mkBind(StatusBound, inst.ID, "", "")
			return
		}
	}
	// P4 配额：起容器前查 dedicated 实例数（reuse 已 bound 不耗新配额，故在 reuse 之后）。
	if r.dedQuota != nil {
		if err := r.dedQuota.CheckDedicatedInstances(ctx, psID); err != nil {
			mkBind(StatusFailed, "", "", "专属中间件实例数已达上限: "+err.Error())
			return
		}
	}
	// spec.SupplyDedicated 自管路径（pg：起 per-app 容器+建库/role+写 env+登记 service_instance）。
	// 非 nil 时跳过默认 PortRange/LaunchDedicated；env/登记由实现内部自管，此处只取 (instID, token) 写 binding。
	if spec.SupplyDedicated != nil {
		instID, token, err := spec.SupplyDedicated(ctx, appID, psID, r.host)
		if err != nil {
			mkBind(StatusFailed, "", "", "起 "+dep.Kind+" 容器: "+err.Error())
			return
		}
		mkBind(StatusBound, instID, token, "")
		return
	}
	// 新供给（默认 dedicated 路径：端口池 → launch → ready → 登记 → env）
	lo, hi := spec.PortRange()
	port := allocPort(r.docker.UsedPorts(ctx), lo, hi)
	if port == 0 {
		mkBind(StatusFailed, "", "", fmt.Sprintf("%s 端口池 %d-%d 已满", dep.Kind, lo, hi))
		return
	}
	short := genShortID()
	base := spec.ContainerName(short)
	// launch：redis 起 1 容器（返密码）；milvus 起三容器栈（返空 auth）。
	authRef, launchErr := spec.LaunchDedicated(ctx, base, port)
	if launchErr != nil {
		mkBind(StatusFailed, "", "", "起 "+dep.Kind+" 容器: "+launchErr.Error())
		return
	}
	// 就绪检测（best-effort，失败不阻塞）：redis AUTH+PING(5s) / milvus /healthz 探针(120s)。
	// 失败仅记 Warn 后继续 claim→bound（容器/栈保留，app 经 host LAN IP:port 使用）——
	// .28 上 backend(deploy_default 网) 可能拨不到 host 发布端口（同 P2 flush 的 cross-network 形状），但 app 能到。
	if err := spec.ReadyDedicated(ctx, r.host, base, port, authRef); err != nil {
		if r.log != nil {
			r.log.Warn("dedicated 就绪检测失败 (best-effort, proceed to bound)",
				zap.String("app", appID), zap.String("kind", dep.Kind),
				zap.Int("port", port), zap.Error(err))
		}
	}
	inst := &ServiceInstance{
		ID:             "svinst-" + dep.Kind + "-ded-" + short,
		ProjectSpaceID: nil, // dedicated 实例不挂项目，靠 binding 关联 app
		Kind:           dep.Kind,
		Name:           base,
		SupplyMode:     ModeDedicated,
		Host:           r.host,
		Port:           port,
		AuthRef:        authRef,
		ContainerName:  base,
		Status:         "active",
	}
	if err := r.store.CreateInstance(ctx, inst); err != nil {
		_ = spec.CleanupDedicated(ctx, base) // 登记失败回收（best-effort：redis RmForce / milvus RmMilvusStack）
		mkBind(StatusFailed, "", "", "登记实例: "+err.Error())
		return
	}
	r.writeDedicatedEnvSpec(ctx, appID, spec, inst)
	mkBind(StatusBound, inst.ID, "", "")
}

// writeDedicatedEnvSpec 写 dedicated env（spec 驱动），均 source=platform。
// 写 spec.AddrEnv=ConnStr + 遍历 spec.DedicatedEnv(inst.AuthRef)（redis→[REDIS_PASSWORD] / milvus→[]）。
func (r *Reconciler) writeDedicatedEnvSpec(ctx context.Context, appID string, spec KindSpec, inst *ServiceInstance) {
	_ = r.env.UpsertEnv(ctx, appID, spec.AddrEnv, ConnStr(inst), false, "platform")
	for _, e := range spec.DedicatedEnv(inst.AuthRef) {
		_ = r.env.UpsertEnv(ctx, appID, e.Key, e.Value, e.IsSecret, "platform")
	}
}

// Cleanup 删 app 的 dedicated 中间件容器（best-effort，不阻塞 Delete）。
// 只动 strategy=dedicated 的 binding（bind_existing/shared 靠 ON DELETE CASCADE，不碰）。
// dedicated 容器是宿主资源，CASCADE 只删 DB 行不删容器 → 必须显式 docker rm + 删 instance 行。
// 总返回 nil（失败记日志，不阻塞删 app）。
func (r *Reconciler) Cleanup(ctx context.Context, appID string) error {
	binds, err := r.store.ListBindingsByApp(ctx, appID)
	if err != nil {
		return nil
	}
	for _, b := range binds {
		if b.Strategy != ModeDedicated || b.ServiceInstanceID == "" {
			continue
		}
		inst, ie := r.store.GetInstance(ctx, b.ServiceInstanceID)
		if ie != nil || inst == nil {
			continue
		}
		if inst.ContainerName != "" {
			if spec, ok := LookupKind(inst.Kind); ok {
				if err := spec.CleanupDedicated(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("dedicated 清理失败 (best-effort)",
						zap.String("app", appID), zap.String("kind", inst.Kind),
						zap.String("container", inst.ContainerName), zap.Error(err))
				}
			}
		}
		// 先删 binding 解 FK 引用（binding.service_instance_id RESTRICT instance 删除），再删 instance。
		// binding 本就要在 app ON DELETE CASCADE 时删，此处提前无观察差异。
		_ = r.store.DeleteBinding(ctx, b.ID)
		_ = r.store.DeleteInstance(ctx, inst.ID)
	}
	return nil
}

// injectedEnvKeysFromSpec 从 KindSpec 派生该 kind 供给时注入的全部 env 键（ReleaseDep 删除用）。
// 合并 AddrEnv + SharedEnv(占位 token/inst 取全集键) + DedicatedEnv(占位 authRef) 的键，去重保序。
// 单一真源：新增 kind 的 env 键随 spec 自动更新，无须在此维护 switch。
func injectedEnvKeysFromSpec(spec KindSpec) []string {
	seen := map[string]bool{}
	keys := []string{}
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	add(spec.AddrEnv)
	// 占位 inst 带 AuthRef 以触发 SharedEnv 的条件键（如 redis 的 REDIS_PASSWORD）。
	if spec.SharedEnv != nil {
		for _, e := range spec.SharedEnv("__token__", &ServiceInstance{Kind: spec.Kind, AuthRef: "__auth__"}) {
			add(e.Key)
		}
	}
	if spec.DedicatedEnv != nil {
		for _, e := range spec.DedicatedEnv("__auth__") {
			add(e.Key)
		}
	}
	return keys
}

// ReleaseDep 释放单个依赖的资源（声明移除/变更时；best-effort，不报错）：
// dedicated → docker rm 容器/栈 + 删 instance 行；三类都删注入的 platform env 键 + 删 binding 行。
func (r *Reconciler) ReleaseDep(ctx context.Context, b *ServiceBinding) {
	// dedicated：docker rm（spec.CleanupDedicated 统一 redis/milvus）。instance 行须在 binding 删后再删（FK RESTRICT）。
	var dedInstID string
	if b.Strategy == ModeDedicated && b.ServiceInstanceID != "" {
		if inst, ie := r.store.GetInstance(ctx, b.ServiceInstanceID); ie == nil && inst != nil && inst.ContainerName != "" {
			if spec, ok := LookupKind(inst.Kind); ok {
				if err := spec.CleanupDedicated(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("ReleaseDep 清理失败 (best-effort)",
						zap.String("app", b.AppID), zap.String("kind", inst.Kind), zap.Error(err))
				}
			}
			dedInstID = inst.ID
		}
	}
	// 删注入的 platform env 键（spec 驱动：注册 kind 从 spec 派生全集键；未注册回退 <KIND>_ADDR）。
	var envKeys []string
	if spec, ok := LookupKind(b.ServiceKind); ok {
		envKeys = injectedEnvKeysFromSpec(spec)
	} else {
		envKeys = []string{strings.ToUpper(b.ServiceKind) + "_ADDR"}
	}
	for _, key := range envKeys {
		_ = r.env.DeleteEnv(ctx, b.AppID, key)
	}
	// 先删 binding 解 FK 引用（binding.service_instance_id RESTRICT instance 删除），再删 instance。
	_ = r.store.DeleteBinding(ctx, b.ID)
	if dedInstID != "" {
		_ = r.store.DeleteInstance(ctx, dedInstID)
	}
}

// ListDeps 该 app 的依赖声明（binding → DepDeclaration）。
func (r *Reconciler) ListDeps(ctx context.Context, appID string) ([]appdeploy.DepDeclaration, error) {
	binds, err := r.store.ListBindingsByApp(ctx, appID)
	if err != nil {
		return nil, err
	}
	out := make([]appdeploy.DepDeclaration, 0, len(binds))
	for _, b := range binds {
		out = append(out, appdeploy.DepDeclaration{
			Kind: b.ServiceKind, Strategy: b.Strategy, Status: b.Status,
			Instance: b.ServiceInstanceID, Token: b.IsolationToken, Error: b.LastError,
		})
	}
	return out, nil
}

// RegisterBindExisting 注册一个已有中间件实例(运维登记部署机服务)。
// scope="project" 挂项目空间 psID;否则平台级(NULL)。转 ServiceInstance 委托 store。
func (r *Reconciler) RegisterBindExisting(ctx context.Context, psID string, m appdeploy.MWInstance) (*appdeploy.MWInstance, error) {
	inst := &ServiceInstance{Kind: m.Kind, Name: m.Name, Host: m.Host, Port: m.Port, AuthRef: m.AuthRef}
	if m.Scope == "project" && psID != "" {
		inst.ProjectSpaceID = &psID
	}
	if err := r.store.RegisterBindExisting(ctx, inst); err != nil {
		return nil, err
	}
	return &appdeploy.MWInstance{ID: inst.ID, Kind: inst.Kind, Name: inst.Name, Host: inst.Host, Port: inst.Port, Scope: m.Scope}, nil
}

// ListBindExisting 列出项目空间可见的已注册实例(转 MWInstance,auth_ref 掩码返回)。
func (r *Reconciler) ListBindExisting(ctx context.Context, psID string) ([]appdeploy.MWInstance, error) {
	list, err := r.store.ListBindExisting(ctx, psID)
	if err != nil {
		return nil, err
	}
	out := make([]appdeploy.MWInstance, 0, len(list))
	for _, s := range list {
		scope := "platform"
		if s.ProjectSpaceID != nil {
			scope = "project"
		}
		auth := ""
		if s.AuthRef != "" {
			auth = "***" // 掩码
		}
		out = append(out, appdeploy.MWInstance{ID: s.ID, Kind: s.Kind, Name: s.Name, Host: s.Host, Port: s.Port, AuthRef: auth, Scope: scope})
	}
	return out, nil
}

// DeleteInstance 删除已注册实例(委托 store)。
func (r *Reconciler) DeleteInstance(ctx context.Context, id string) error {
	return r.store.DeleteInstance(ctx, id)
}

// DepsCatalog 勾选器选项：固定 kinds/strategies + 可见 active 实例。
func (r *Reconciler) DepsCatalog(ctx context.Context, psID string) (appdeploy.DepsCatalog, error) {
	insts, err := r.store.ListActiveInstances(ctx, psID)
	if err != nil {
		return appdeploy.DepsCatalog{}, err
	}
	cis := make([]appdeploy.CatalogInstance, 0, len(insts))
	for _, ins := range insts {
		cis = append(cis, appdeploy.CatalogInstance{
			ID: ins.ID, Kind: ins.Kind, Name: ins.Name, SupplyMode: ins.SupplyMode,
			Host: ins.Host, Port: ins.Port,
		})
	}
	return appdeploy.DepsCatalog{
		Kinds: []string{"redis", "milvus", "pg"},
		Strategies: []appdeploy.StrategyOption{
			{Name: ModeBindExisting, Desc: "绑定部署机已运行的服务（最省，导入最常见）"},
			{Name: ModeShared, Desc: "ANP 共享实例 + 每 app 隔离 token（db号/前缀）"},
			{Name: ModeDedicated, Desc: "每 app 专属容器（隔离最强，资源独占）"},
		},
		Instances: cis,
	}, nil
}

// SetDeps 整体替换声明（PutDeps 核心，best-effort）：
// removed（现 binding 不在新声明）/ changed（同 kind 策略变）→ ReleaseDep 释放资源；
// added（新声明不在现 binding）/ changed → DeclareBinding 写 declared；
// unchanged（同 kind 同策略）→ 不动（保 bound，不重供）。
func (r *Reconciler) SetDeps(ctx context.Context, appID, psID string, decls []appdeploy.DepDeclaration) error {
	// 写路径（用户 PUT），错误必须传播：ListBindingsByApp 瞬时失败→curMap 空→所有 bound
	// 当 missing→DeclareBinding upsert 成 declared（ON CONFLICT DO UPDATE）丢 instance/token。
	cur, err := r.store.ListBindingsByApp(ctx, appID)
	if err != nil {
		return err
	}
	curMap := map[string]ServiceBinding{}
	for _, b := range cur {
		curMap[b.ServiceKind] = b
	}
	newStrategy := map[string]string{}
	for _, d := range decls {
		newStrategy[d.Kind] = d.Strategy
	}
	// removed / changed → 释放（ReleaseDep 保持 best-effort：即发即忘清理，不阻断主流程）
	for kind, b := range curMap {
		ns, ok := newStrategy[kind]
		if !ok || ns != b.Strategy {
			r.ReleaseDep(ctx, &b)
		}
	}
	// added / changed → 声明
	for kind, strategy := range newStrategy {
		b, exists := curMap[kind]
		if !exists || b.Strategy != strategy {
			if err := r.store.DeclareBinding(ctx, appID, psID, kind, strategy); err != nil {
				return err
			}
		}
	}
	return nil
}
