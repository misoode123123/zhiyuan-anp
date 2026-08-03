package mwsupply

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// EnvWriter 写/删应用 env（由 appdeploy.Store 实现，避免 mwsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
	DeleteEnv(ctx context.Context, appID, key string) error // P6：声明移除时删注入的 platform env
}

// Reconciler 中间件依赖供给。best-effort：失败记 binding，不阻塞部署。
type Reconciler struct {
	store   *Store
	env     EnvWriter
	flusher DBFlusher     // shared 重分配时清空 redis db（Task 3）
	ready   ReadyChecker  // dedicated 起容器后轮询 AUTH+PING 至就绪（P3）
	docker  MWDockerRunner // dedicated 容器管理（run/rm）（P3）
	host    string        // AppDeployHost（dedicated REDIS_ADDR host + 就绪检测拨号）
	log     *zap.Logger   // 可选；flush best-effort 失败记 Warn（nil 安全）
}

// NewReconciler 构造。
//   env 传 appdeploy.Store（满足 EnvWriter）；
//   flusher+ready 可传同一 *redisFlusher（NewRedisFlusher 同时满足 DBFlusher+ReadyChecker）；
//   docker 传 NewOSDocker()（测试传 fake）；host 为 AppDeployHost。
func NewReconciler(store *Store, env EnvWriter, flusher DBFlusher, ready ReadyChecker, docker MWDockerRunner, host string) *Reconciler {
	return &Reconciler{store: store, env: env, flusher: flusher, ready: ready, docker: docker, host: host}
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
	if strategy == ModeDedicated {
		r.supplyDedicated(ctx, appID, psID, dep, mkBind)
		return
	}
	if strategy != ModeBindExisting {
		mkBind(StatusFailed, "", "", "策略 "+strategy+" 暂未实现（仅 bind_existing/shared/dedicated）")
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

// supplyShared shared 供给（按 kind 分派）：复用判定（幂等不换 token 不 flush）/ 新分配（按 kind 选 token → claim → env）。
// redis：db 号池（ParseDBRange + pickLowestFree + claimWithRetry 的 flush+重试）。
// milvus：随机 collection 前缀（无 flush、无池；极罕见撞号重生）。
func (r *Reconciler) supplyShared(ctx context.Context, appID, psID string, dep DepService,
	mkBind func(status, instID, token, lastErr string)) {
	inst, err := r.store.LookupShared(ctx, dep.Kind)
	if err != nil || inst == nil {
		mkBind(StatusFailed, "", "", "无 shared "+dep.Kind+" 实例")
		return
	}
	// 复用：同 app 已 bound 同实例同 token → 不换 token、不 flush（保数据）、重写 env。
	if existing, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && existing != nil &&
		existing.Status == StatusBound && existing.IsolationToken != "" && existing.ServiceInstanceID == inst.ID {
		r.writeSharedEnv(ctx, appID, inst, existing.IsolationToken)
		mkBind(StatusBound, inst.ID, existing.IsolationToken, "")
		return
	}
	// 新分配 + claim（按 kind 分派）
	token, err := r.allocAndClaimShared(ctx, appID, psID, dep.Kind, inst)
	if err != nil {
		mkBind(StatusFailed, inst.ID, "", err.Error())
		return
	}
	r.writeSharedEnv(ctx, appID, inst, token)
	mkBind(StatusBound, inst.ID, token, "")
}

// allocAndClaimShared 按 kind 分派 shared token 分配 + claim。
// redis：db 号池；milvus：随机 collection 前缀。
func (r *Reconciler) allocAndClaimShared(ctx context.Context, appID, psID, kind string, inst *ServiceInstance) (string, error) {
	if kind == "milvus" {
		return r.allocMilvusPrefix(ctx, appID, psID, kind, inst)
	}
	return r.allocRedisDB(ctx, appID, psID, kind, inst)
}

// allocRedisDB redis shared db 号分配：ParseDBRange + pickLowestFree + claimWithRetry（flush + 有界重试）。
// 逐字=重构前 supplyShared 的新分配段（零回归）。
func (r *Reconciler) allocRedisDB(ctx context.Context, appID, psID, kind string, inst *ServiceInstance) (string, error) {
	lo, hi, ok := ParseDBRange(inst.Isolation)
	if !ok {
		return "", fmt.Errorf("shared 实例 isolation 缺 db_range")
	}
	allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
	first, found := pickLowestFree(lo, hi, allocated)
	if !found {
		return "", fmt.Errorf("shared redis db 号耗尽（池 %d-%d）", lo, hi)
	}
	return r.claimWithRetry(ctx, appID, psID, kind, inst, lo, hi, first, allocated)
}

// allocMilvusPrefix milvus shared collection 前缀分配：生成随机唯一前缀 + 单次 claim。
// 无 flush（无前缀级原语）、无有限池（前缀随机生成）；极罕见并发撞号（uq_svbind_inst_token 抛 23505）换号重生，有界 ≤4。
func (r *Reconciler) allocMilvusPrefix(ctx context.Context, appID, psID, kind string, inst *ServiceInstance) (string, error) {
	allocated, _ := r.store.AllocatedTokens(ctx, inst.ID)
	taken := make(map[string]bool, len(allocated))
	for _, t := range allocated {
		taken[t] = true
	}
	for attempts := 0; attempts < 4; attempts++ {
		token := genMilvusPrefix()
		if taken[token] {
			continue
		}
		err := r.store.ClaimSharedToken(ctx, appID, psID, kind, inst.ID, token, EnvKeyFor(kind))
		if err == nil {
			return token, nil
		}
		if !isUniqueViolation(err) {
			return "", err
		}
		taken[token] = true
	}
	return "", fmt.Errorf("milvus 前缀分配重试用尽（并发撞号）")
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
			// flush 是数据卫生(best-effort)，非首次分配正确性所需：首次分配的 db 号本就干净。
			// 后端可能无 redis 网络访问（如 .28：backend 与 redis 分属不同 docker 网络，拨 host LAN IP 超时）。
			// 记 Warn 后继续 claim——重分配的卫生保证留给「部署侧确保 backend↔redis 可达」的 prod。
			if r.log != nil {
				r.log.Warn("shared redis flush failed (best-effort, proceed to claim)",
					zap.String("app", appID), zap.String("kind", kind),
					zap.String("db", token), zap.Error(ferr))
			}
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

// writeSharedEnv 写 shared env（按 kind 分派），均 source=platform。
// redis：REDIS_ADDR + REDIS_DB（+ REDIS_PASSWORD 若鉴权）。
// milvus：MILVUS_ADDR + MILVUS_COLLECTION_PREFIX（无 password，v1 无 auth；无 db token）。
func (r *Reconciler) writeSharedEnv(ctx context.Context, appID string, inst *ServiceInstance, token string) {
	if inst.Kind == "milvus" {
		_ = r.env.UpsertEnv(ctx, appID, "MILVUS_ADDR", ConnStr(inst), false, "platform")
		_ = r.env.UpsertEnv(ctx, appID, "MILVUS_COLLECTION_PREFIX", token, false, "platform")
		return
	}
	kindUp := strings.ToUpper(inst.Kind) // redis→REDIS
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_ADDR", ConnStr(inst), false, "platform")
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_DB", token, false, "platform")
	if inst.AuthRef != "" {
		_ = r.env.UpsertEnv(ctx, appID, kindUp+"_PASSWORD", inst.AuthRef, true, "platform")
	}
}

// supplyDedicated dedicated 供给（按 kind 分派）：复用判定（幂等）/ 新供给（端口→launch→ready→登记→env）。
// redis：1 容器 + AUTH+PING；milvus：专属网络 + milvus/etcd/minio 三容器 + /healthz 探针。
func (r *Reconciler) supplyDedicated(ctx context.Context, appID, psID string, dep DepService,
	mkBind func(status, instID, token, lastErr string)) {
	// 复用：同 app 已 bound dedicated 实例 → 不重启、不换端口、保数据，重写 env。
	if b, e := r.store.GetBinding(ctx, appID, dep.Kind); e == nil && b != nil &&
		b.Status == StatusBound && b.ServiceInstanceID != "" {
		if inst, ie := r.store.GetInstance(ctx, b.ServiceInstanceID); ie == nil && inst != nil && inst.Status == "active" {
			r.writeDedicatedEnv(ctx, appID, inst)
			mkBind(StatusBound, inst.ID, "", "")
			return
		}
	}
	// 新供给
	lo, hi := portRange(dep.Kind)
	port := allocPort(r.docker.UsedPorts(ctx), lo, hi)
	if port == 0 {
		mkBind(StatusFailed, "", "", fmt.Sprintf("%s 端口池 %d-%d 已满", dep.Kind, lo, hi))
		return
	}
	short := genShortID()
	base := dedicatedContainerName(dep.Kind, short)
	// launch（按 kind）：redis 起 1 容器（返密码）；milvus 起三容器栈（返空 auth）。
	authRef, launchErr := r.launchDedicated(ctx, dep.Kind, base, port)
	if launchErr != nil {
		mkBind(StatusFailed, "", "", "起 "+dep.Kind+" 容器: "+launchErr.Error())
		return
	}
	// 就绪检测（best-effort，失败不阻塞）：redis AUTH+PING(5s) / milvus /healthz 探针(120s)。
	// 失败仅记 Warn 后继续 claim→bound（容器/栈保留，app 经 host LAN IP:port 使用）——
	// .28 上 backend(deploy_default 网) 可能拨不到 host 发布端口（同 P2 flush 的 cross-network 形状），但 app 能到。
	if err := r.waitDedicatedReady(ctx, dep.Kind, base, port, authRef); err != nil {
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
		r.rmDedicated(ctx, dep.Kind, base) // 登记失败回收（redis RmForce / milvus RmMilvusStack）
		mkBind(StatusFailed, "", "", "登记实例: "+err.Error())
		return
	}
	r.writeDedicatedEnv(ctx, appID, inst)
	mkBind(StatusBound, inst.ID, "", "")
}

// launchDedicated 起 dedicated 容器/栈，返回 authRef（redis=密码 / milvus=""）。
func (r *Reconciler) launchDedicated(ctx context.Context, kind, base string, port int) (string, error) {
	switch kind {
	case "redis":
		pwd := genPassword()
		return pwd, r.docker.RunRedisContainer(ctx, base, pwd, port)
	case "milvus":
		return "", r.docker.RunMilvusStack(ctx, base, port)
	default:
		return "", fmt.Errorf("dedicated 不支持 kind %q", kind)
	}
}

// waitDedicatedReady 就绪检测（best-effort 由调用方处理）：redis AUTH+PING / milvus /healthz 探针。
func (r *Reconciler) waitDedicatedReady(ctx context.Context, kind, base string, port int, authRef string) error {
	switch kind {
	case "redis":
		readyCtx, cancel := context.WithTimeout(ctx, readyPingTimeout)
		defer cancel()
		return r.ready.Ping(readyCtx, r.host, port, authRef)
	case "milvus":
		return r.docker.MilvusReady(ctx, base, milvusReadyTimeout)
	default:
		return nil
	}
}

// rmDedicated 回收半成品/失败容器栈（best-effort）：redis RmForce / milvus RmMilvusStack。
func (r *Reconciler) rmDedicated(ctx context.Context, kind, base string) {
	switch kind {
	case "redis":
		_ = r.docker.RmForce(ctx, base)
	case "milvus":
		_ = r.docker.RmMilvusStack(ctx, base)
	}
}

// writeDedicatedEnv 写 <KIND>_ADDR（+ redis 专 _PASSWORD），均 source=platform。
// redis：REDIS_ADDR + REDIS_PASSWORD（不写 REDIS_DB，用默认 db 0）。
// milvus v1 无 auth：只写 MILVUS_ADDR（不写 password、不写 db token）。
func (r *Reconciler) writeDedicatedEnv(ctx context.Context, appID string, inst *ServiceInstance) {
	_ = r.env.UpsertEnv(ctx, appID, EnvKeyFor(inst.Kind), ConnStr(inst), false, "platform") // REDIS_ADDR / MILVUS_ADDR
	if inst.Kind == "redis" {
		pwdKey := strings.ToUpper(inst.Kind) + "_PASSWORD" // REDIS_PASSWORD
		_ = r.env.UpsertEnv(ctx, appID, pwdKey, inst.AuthRef, true, "platform")
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
			switch inst.Kind {
			case "milvus":
				if err := r.docker.RmMilvusStack(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("dedicated milvus 栈清理失败 (best-effort)",
						zap.String("app", appID), zap.String("base", inst.ContainerName), zap.Error(err))
				}
			default: // redis
				if err := r.docker.RmForce(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("dedicated 容器清理失败 (best-effort)",
						zap.String("app", appID), zap.String("container", inst.ContainerName), zap.Error(err))
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

// injectedEnvKeys 某 kind 供给时注入的全部 env 键（ReleaseDep 删除用）。
func injectedEnvKeys(kind string) []string {
	switch kind {
	case "redis":
		return []string{"REDIS_ADDR", "REDIS_DB", "REDIS_PASSWORD"}
	case "milvus":
		return []string{"MILVUS_ADDR", "MILVUS_COLLECTION_PREFIX"}
	default:
		return []string{strings.ToUpper(kind) + "_ADDR"}
	}
}

// ReleaseDep 释放单个依赖的资源（声明移除/变更时；best-effort，不报错）：
// dedicated → docker rm 容器/栈 + 删 instance 行；三类都删注入的 platform env 键 + 删 binding 行。
func (r *Reconciler) ReleaseDep(ctx context.Context, b *ServiceBinding) {
	// dedicated：docker rm（复用 Cleanup 的 per-binding rm 逻辑）。instance 行须在 binding 删后再删（FK RESTRICT）。
	var dedInstID string
	if b.Strategy == ModeDedicated && b.ServiceInstanceID != "" {
		if inst, ie := r.store.GetInstance(ctx, b.ServiceInstanceID); ie == nil && inst != nil && inst.ContainerName != "" {
			switch inst.Kind {
			case "milvus":
				if err := r.docker.RmMilvusStack(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("ReleaseDep milvus 栈清理失败 (best-effort)", zap.String("app", b.AppID), zap.Error(err))
				}
			default:
				if err := r.docker.RmForce(ctx, inst.ContainerName); err != nil && r.log != nil {
					r.log.Warn("ReleaseDep 容器清理失败 (best-effort)", zap.String("app", b.AppID), zap.Error(err))
				}
			}
			dedInstID = inst.ID
		}
	}
	// 删注入的 platform env 键
	for _, key := range injectedEnvKeys(b.ServiceKind) {
		_ = r.env.DeleteEnv(ctx, b.AppID, key)
	}
	// 先删 binding 解 FK 引用（binding.service_instance_id RESTRICT instance 删除），再删 instance。
	_ = r.store.DeleteBinding(ctx, b.ID)
	if dedInstID != "" {
		_ = r.store.DeleteInstance(ctx, dedInstID)
	}
}
