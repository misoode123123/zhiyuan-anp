package appdeploy

import (
	"context"
	"fmt"
	"time"
)

// InstanceHealthStore HealthReconciler 需要的 store 子集(*Store 满足)。
type InstanceHealthStore interface {
	ListHeadlessActiveInstances(ctx context.Context) ([]headlessInstance, error)
	UpdateInstanceHealth(ctx context.Context, appID, env, status, lastErr string, restartCount int) error
	UpdateRestartCount(ctx context.Context, appID, env string, restartCount int) error
}

// HealthInspector 容器健康探查(*Deployer 满足)。
type HealthInspector interface {
	InspectHealth(ctx context.Context, container string) (ContainerHealth, error)
}

// HealthAlerter 不健康/恢复告警(ops 实现见 health_alerter.go)。
type HealthAlerter interface {
	OnUnhealthy(ctx context.Context, projectSpaceID, appID, appName, env, severity, reason string) error
	OnRecovered(ctx context.Context, projectSpaceID, appID, appName, env string) error
}

// HealthReconciler 周期巡检 headless 实例进程存活,翻 status + 联动告警。仿 ServerMonitor.Start。
type HealthReconciler struct {
	store    InstanceHealthStore
	deployer HealthInspector
	alerter  HealthAlerter
	interval time.Duration
	burst    int // 单周期新增重启阈值,判 crash-loop
}

func NewHealthReconciler(store InstanceHealthStore, deployer HealthInspector, alerter HealthAlerter) *HealthReconciler {
	return &HealthReconciler{store: store, deployer: deployer, alerter: alerter, interval: 30 * time.Second, burst: 3}
}

// Start 后台 ticker 循环,直到 ctx 取消。内部起 goroutine,非阻塞。
func (r *HealthReconciler) Start(ctx context.Context) {
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

func (r *HealthReconciler) reconcileOnce(ctx context.Context) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("health reconcile panic: %v\n", rec)
		}
	}()
	targets, err := r.store.ListHeadlessActiveInstances(ctx)
	if err != nil {
		return
	}
	for _, t := range targets {
		r.checkOne(ctx, t)
	}
}

func (r *HealthReconciler) checkOne(ctx context.Context, t headlessInstance) {
	defer func() {
		if rec := recover(); rec != nil {
			fmt.Printf("health check panic app=%s env=%s: %v\n", t.AppID, t.Env, rec)
		}
	}()
	h, err := r.deployer.InspectHealth(ctx, t.ContainerName)
	if err != nil {
		// inspect 失败(docker 不可达/容器查不到):记 last_error,保留原 status,不误判崩溃
		_ = r.store.UpdateInstanceHealth(ctx, t.AppID, t.Env, t.Status, "inspect 失败: "+err.Error(), t.RestartCount)
		return
	}
	newStatus, newCount := aggregateHealth(h, t.RestartCount, r.burst)
	if newStatus != t.Status {
		reason := describeHealth(h, t.RestartCount)
		_ = r.store.UpdateInstanceHealth(ctx, t.AppID, t.Env, newStatus, reason, newCount)
		switch newStatus {
		case "degraded", "failed":
			_ = r.alerter.OnUnhealthy(ctx, t.ProjectSpaceID, t.AppID, t.Name, t.Env, severityFor(newStatus), reason)
		case "running":
			_ = r.alerter.OnRecovered(ctx, t.ProjectSpaceID, t.AppID, t.Name, t.Env)
		}
	} else if newCount != t.RestartCount {
		_ = r.store.UpdateRestartCount(ctx, t.AppID, t.Env, newCount) // 仅更新基线
	}
}

// aggregateHealth 由 docker 观测 + 上次存储 restart_count 推导新 status 与新基线。纯函数,增量判 crash-loop(非粘)。
func aggregateHealth(h ContainerHealth, storedCount, burst int) (string, int) {
	newCount := h.RestartCount
	if !h.Running {
		return "failed", newCount
	}
	if storedCount == 0 && h.RestartCount > 0 {
		return "running", newCount // 冷启动基线:首次观测到历史重启,只记不告警
	}
	if newCount-storedCount >= burst {
		return "degraded", newCount // 本周期新增 ≥burst = 活跃 crash-loop
	}
	return "running", newCount
}

func describeHealth(h ContainerHealth, stored int) string {
	if !h.Running {
		if h.OOMKilled {
			return fmt.Sprintf("容器退出(OOMKilled exit=%d)", h.ExitCode)
		}
		return fmt.Sprintf("容器退出(exit=%d)", h.ExitCode)
	}
	return fmt.Sprintf("crash-loop(本周期新增重启 %d 次)", h.RestartCount-stored)
}

func severityFor(status string) string {
	if status == "failed" {
		return "critical"
	}
	return "warning"
}
