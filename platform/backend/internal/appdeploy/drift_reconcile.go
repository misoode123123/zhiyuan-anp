package appdeploy

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// DriftStore DriftReconciler 需要的 store 子集（*Store 满足）。
type DriftStore interface {
	ListActiveInstancesForDrift(ctx context.Context) ([]driftInstance, error)
	UpdateInstanceVersionImage(ctx context.Context, appID, env string, version int, image string) error
}

// ImageInspector 容器镜像探查（*Deployer 满足）。
type ImageInspector interface {
	InspectImage(ctx context.Context, container string) (string, error)
}

// DriftAlerter 部署态漂移/恢复告警（ops 实现见 health_alerter.go）。
type DriftAlerter interface {
	OnDrift(ctx context.Context, projectSpaceID, appID, appName, env, reason string) error
	OnDriftResolved(ctx context.Context, projectSpaceID, appID, appName, env string) error
}

// DriftReconciler 周期巡检部署态一致性：DB image/version ↔ 运行容器镜像 ↔ deploy.yaml actual。
// 发现漂移按「安全自愈」纠正——actual ← 容器镜像（缓存记录，无副作用）；DB 计数器 high-water-mark
// 只升不降（防版本号复用致镜像 tag 碰撞）。无法安全自愈的向下漂移（容器比记录旧=疑似回滚）只告警请人工。
// 真相源=运行容器（与部署时回读不同：部署时真相是意图 ins.Image）。仿 HealthReconciler.Start。
type DriftReconciler struct {
	store    DriftStore
	deployer ImageInspector
	alerter  DriftAlerter
	interval time.Duration
}

// NewDriftReconciler 构造（interval 默认 2min）。
func NewDriftReconciler(store DriftStore, deployer ImageInspector, alerter DriftAlerter) *DriftReconciler {
	return &DriftReconciler{store: store, deployer: deployer, alerter: alerter, interval: 2 * time.Minute}
}

// Start 后台 ticker 循环，直到 ctx 取消。内部起 goroutine，非阻塞。
func (r *DriftReconciler) Start(ctx context.Context) {
	ticker := time.NewTicker(r.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.reconcileOnce(ctx)
			}
		}
	}()
}

func (r *DriftReconciler) reconcileOnce(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("drift reconcile panic: %v\n", rec)
		}
	}()
	targets, err := r.store.ListActiveInstancesForDrift(ctx)
	if err != nil {
		return
	}
	for _, t := range targets {
		r.checkOne(ctx, t)
	}
}

// checkOne 比对一个实例的三方镜像一致性，漂移即安全自愈 + 告警/恢复。
func (r *DriftReconciler) checkOne(ctx context.Context, t driftInstance) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("drift check panic app=%s env=%s: %v\n", t.AppID, t.Env, rec)
		}
	}()
	if t.ContainerName == "" {
		return // 无容器名（异常数据），跳过
	}
	containerImg, err := r.deployer.InspectImage(ctx, t.ContainerName)
	if err != nil {
		return // inspect 失败（容器查不到/docker 不可达）：无真相源，不误报，跳过
	}
	mf, _ := LoadDeployManifest(t.RepoDir)
	manifestImg := ""
	if mf != nil {
		manifestImg = mf.Actual.ImageDigest
	}
	res := checkDrift(t.Image, containerImg, manifestImg)
	if res.OK {
		_ = r.alerter.OnDriftResolved(ctx, t.ProjectSpaceID, t.AppID, t.Name, t.Env)
		return
	}
	// 安全自愈（真相源=运行容器）：
	// 1. deploy.yaml actual.image_digest ← 容器镜像（actual 仅缓存记录，覆写无副作用；不动 mounts_src/host_port）
	reconcileActual(t.RepoDir, mf, containerImg)
	// 2. DB 计数器 high-water-mark（只升不降）；向上纠正时同步把 image 记到容器真相
	newDBImg := t.Image
	if v, ch := highWaterMarkVersion(t.Version, containerImg); ch {
		_ = r.store.UpdateInstanceVersionImage(ctx, t.AppID, t.Env, v, containerImg)
		newDBImg = containerImg
	}
	// 重判：DB 现已对齐容器（向上纠正后）→ 漂移解除（记自愈审计 + resolve 旧告警）；
	// 否则（向下，容器更旧无法安全升 DB）→ 告警请人工确认。
	if newDBImg == containerImg {
		zap.L().Info("部署态漂移已自愈（DB 记录对齐运行容器）",
			zap.String("app", t.Name), zap.String("env", t.Env),
			zap.Int("old_version", t.Version), zap.String("container_image", containerImg),
			zap.String("reason", res.Reason))
		_ = r.alerter.OnDriftResolved(ctx, t.ProjectSpaceID, t.AppID, t.Name, t.Env)
	} else {
		_ = r.alerter.OnDrift(ctx, t.ProjectSpaceID, t.AppID, t.Name, t.Env,
			"容器 "+containerImg+" 落后于记录 "+t.Image+
				"，疑似回滚；计数器保持高位避免版本复用，请人工确认")
	}
}
