package appdeploy

import (
	"context"
	"strings"
	"testing"
)

// TestMarkBuilding 部署开始前同步标记 building：application + 当前环境实例都置 building，
// 且清空上一次的 last_error（避免上次失败的红条/错误残留，前端进度条立即出现）。
//
// 修复背景：原实现 a.Status="building" 在 docker build 之后才写（handler.go:1046），
// 构建期间（可能数分钟）前端 3s 轮询拿不到 building → 进度条不出现。
func TestMarkBuilding(t *testing.T) {
	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	// 预置上次失败残留：app + test 实例都是 failed + 错误信息
	_ = h.store.SetStatus(ctx, "ps_1", a.ID, "failed", "上次构建失败的原因", "old build log")
	ins, _ := h.store.GetOrCreateInstance(ctx, a.ID, EnvTest)
	ins.Status = "failed"
	ins.LastError = "上次构建失败的原因"
	_ = h.store.UpdateInstance(ctx, ins)

	h.markBuilding(ctx, "ps_1", a.ID, EnvTest)

	// application 概览：building + 清空错误
	got, _ := h.store.GetByAppID(ctx, a.ID)
	if got.Status != "building" {
		t.Fatalf("markBuilding 后 app.status 应 building，得到 %s", got.Status)
	}
	if got.LastError != "" {
		t.Fatalf("markBuilding 应清空 app.last_error（避免残留），得到 %q", got.LastError)
	}
	// test 实例：building + 清空错误
	gotIns, _ := h.store.GetInstance(ctx, a.ID, EnvTest)
	if gotIns == nil {
		t.Fatal("test 实例应存在")
	}
	if gotIns.Status != "building" {
		t.Fatalf("markBuilding 后 test 实例应 building，得到 %s", gotIns.Status)
	}
	if gotIns.LastError != "" {
		t.Fatalf("markBuilding 应清空 instance.last_error，得到 %q", gotIns.LastError)
	}
}

// TestMarkFailed_writesAppStatusFailedForTestEnv 部署失败时把 application.status 写成 failed
// 并记录原因 —— 对 test 环境也必须写。
//
// 修复背景：原实现失败仅更新 instance，再调 syncOverviewIfProd，而它对 test 环境 early return，
// 导致 test 部署失败时 a.status 不变，前端 a.status==="failed" 红条永远不触发，用户只看到
// 应用卡停（"只有一个 failed" 来自 instance 徽章，无原因）。
func TestMarkFailed_writesAppStatusFailedForTestEnv(t *testing.T) {
	h, _ := newHTTPHandler(t)
	ctx := context.Background()
	a := seedApp(t, h, "ps_1", "snake", "/tmp/snake")
	_ = h.store.UpdateAppStatus(ctx, a.ID, "building") // 模拟构建中

	h.markFailed(ctx, "ps_1", a.ID, "docker build 失败: exit 1", "Step 2/5: error")

	got, _ := h.store.GetByAppID(ctx, a.ID)
	if got.Status != "failed" {
		t.Fatalf("失败后 app.status 应 failed，得到 %s", got.Status)
	}
	if !strings.Contains(got.LastError, "docker build 失败") {
		t.Fatalf("app.last_error 应含失败原因，得到 %q", got.LastError)
	}
	if !strings.Contains(got.BuildLog, "Step 2/5") {
		t.Fatalf("app.build_log 应记录构建日志，得到 %q", got.BuildLog)
	}
}
