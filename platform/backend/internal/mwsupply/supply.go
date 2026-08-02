package mwsupply

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

// EnvWriter 写应用 env（由 appdeploy.Store 实现，避免 mwsupply→appdeploy 循环依赖）。
type EnvWriter interface {
	UpsertEnv(ctx context.Context, appID, key, value string, isSecret bool, source string) error
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

// writeSharedEnv 写 REDIS_ADDR + REDIS_DB（+ REDIS_PASSWORD 若鉴权），均 source=platform。
func (r *Reconciler) writeSharedEnv(ctx context.Context, appID string, inst *ServiceInstance, token string) {
	kindUp := strings.ToUpper(inst.Kind) // redis→REDIS
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_ADDR", ConnStr(inst), false, "platform")
	_ = r.env.UpsertEnv(ctx, appID, kindUp+"_DB", token, false, "platform")
	if inst.AuthRef != "" {
		_ = r.env.UpsertEnv(ctx, appID, kindUp+"_PASSWORD", inst.AuthRef, true, "platform")
	}
}

// supplyDedicated dedicated redis 供给：复用判定（幂等不重启/不换端口/保数据）/ 新供给（端口→起容器→就绪→登记→env）。
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
	port := allocPort(r.docker.UsedPorts(ctx), mwPortMin, mwPortMax)
	if port == 0 {
		mkBind(StatusFailed, "", "", fmt.Sprintf("redis 端口池 %d-%d 已满", mwPortMin, mwPortMax))
		return
	}
	short := genShortID()
	name := dedicatedContainerName(short)
	pwd := genPassword()
	if err := r.docker.RunRedisContainer(ctx, name, pwd, port); err != nil {
		mkBind(StatusFailed, "", "", "起 redis 容器: "+err.Error())
		return
	}
	// 就绪检测（轮询 AUTH+PING，超时 readyTimeout）；失败清半成品容器。
	readyCtx, cancel := context.WithTimeout(ctx, readyTimeout)
	defer cancel()
	if err := r.ready.Ping(readyCtx, r.host, port, pwd); err != nil {
		_ = r.docker.RmForce(ctx, name)
		mkBind(StatusFailed, "", "", "redis 未就绪: "+err.Error())
		return
	}
	inst := &ServiceInstance{
		ID:             "svinst-redis-ded-" + short,
		ProjectSpaceID: nil, // dedicated 实例不挂项目，靠 binding 关联 app
		Kind:           dep.Kind,
		Name:           name,
		SupplyMode:     ModeDedicated,
		Host:           r.host,
		Port:           port,
		AuthRef:        pwd,
		ContainerName:  name,
		Status:         "active",
	}
	if err := r.store.CreateInstance(ctx, inst); err != nil {
		_ = r.docker.RmForce(ctx, name) // 登记失败回收容器
		mkBind(StatusFailed, "", "", "登记实例: "+err.Error())
		return
	}
	r.writeDedicatedEnv(ctx, appID, inst)
	mkBind(StatusBound, inst.ID, "", "")
}

// writeDedicatedEnv 写 REDIS_ADDR + REDIS_PASSWORD，均 source=platform（不写 REDIS_DB，dedicated 用默认 db 0）。
func (r *Reconciler) writeDedicatedEnv(ctx context.Context, appID string, inst *ServiceInstance) {
	_ = r.env.UpsertEnv(ctx, appID, EnvKeyFor(inst.Kind), ConnStr(inst), false, "platform") // REDIS_ADDR=host:port
	pwdKey := strings.ToUpper(inst.Kind) + "_PASSWORD"                                      // REDIS_PASSWORD
	_ = r.env.UpsertEnv(ctx, appID, pwdKey, inst.AuthRef, true, "platform")                 // secret
}
