package appdeploy

import (
	"context"

	"zhiyuan-anp/platform/backend/internal/ops"
)

// OpsHealthAlerter HealthAlerter 的 ops 实现：写 ops_alert 表（fingerprint 去重 + 恢复 resolve）。
// OnUnhealthy 先查同指纹是否已有 firing，有则跳过（去重），无则建告警；
// OnRecovered 按 OnUnhealthy 相同口径算指纹并 resolve（无命中 no-op，安全）。
type OpsHealthAlerter struct{ store *ops.Store }

// NewOpsHealthAlerter 用 ops.Store 构造 alerter。
func NewOpsHealthAlerter(store *ops.Store) *OpsHealthAlerter { return &OpsHealthAlerter{store: store} }

// alertTitle 统一告警标题（OnUnhealthy/OnRecovered 必须同口径，fingerprint 才一致）。
func alertTitle(appName, env string) string { return "应用 " + appName + " " + env + " 异常" }

// OnUnhealthy 建告警（同指纹已有 firing 时去重跳过）。
func (a *OpsHealthAlerter) OnUnhealthy(ctx context.Context, psID, appID, appName, env, severity, reason string) error {
	title := alertTitle(appName, env)
	fp := ops.Fingerprint("apphealth", title)
	if firing, _ := a.store.HasFiringFingerprint(ctx, fp); firing {
		return nil // 已有 firing 告警，去重
	}
	return a.store.CreateAlert(ctx, &ops.Alert{
		ProjectSpaceID: psID, Source: "apphealth", Severity: severity, Status: "firing",
		Title: title, Description: reason,
	})
}

// OnRecovered 按 fingerprint resolve firing 告警（无命中 no-op）。
func (a *OpsHealthAlerter) OnRecovered(ctx context.Context, psID, appID, appName, env string) error {
	return a.store.ResolveByFingerprint(ctx, ops.Fingerprint("apphealth", alertTitle(appName, env)))
}
