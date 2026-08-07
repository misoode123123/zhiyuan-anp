package compute_test

import (
	"context"
	"errors"
	"testing"

	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestChat_RejectsUnauthorizedModel 验证：指定 model + userID 但未授权时，
// Chat 入口在转发前直接拒绝（ErrModelNotAuthorized），不会真发 HTTP。
// 夹具不设 ID（CreateProvider/CreateModel 自动生成 cpv_/cmd_ 前缀），
// 测试间用 testutil.Truncate 清表隔离（与 grant_test.go 同模式）。
func TestChat_RejectsUnauthorizedModel(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "user_model_grant", "compute_model", "compute_provider")
	s := compute.NewStore(db)
	gw := compute.NewGateway(s)
	ctx := context.Background()

	prov := &compute.Provider{
		Name: "t-prov-a", Type: "api", BaseURL: "http://x",
		APIKey: "k", Enabled: true,
	}
	if err := s.CreateProvider(ctx, prov); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	mdl := &compute.Model{
		ProviderID: prov.ID, Name: "t-a", Modality: "text",
		Enabled: true,
	}
	if err := s.CreateModel(ctx, mdl); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	// 注意：不给 u1 授权 mdl

	_, err := gw.Chat(ctx, compute.ChatRequest{
		TaskType: "spec",
		Model:    mdl.ID, // 指定模型
		UserID:   "u1",   // 但未授权
		Messages: []map[string]interface{}{{"role": "user", "content": "hi"}},
	})
	if !errors.Is(err, compute.ErrModelNotAuthorized) {
		t.Fatalf("期望 ErrModelNotAuthorized，got %v", err)
	}
}

// TestChat_EmptyUserID_NoCheck 验证：空 UserID（兼容老调用）时不校验授权，
// 走到正常转发流程。用真实 model 证明"跳过 auth 后走到转发"——
// 转发对 http://x 会失败，但绝不是 ErrModelNotAuthorized。
func TestChat_EmptyUserID_NoCheck(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "user_model_grant", "compute_model", "compute_provider")
	s := compute.NewStore(db)
	gw := compute.NewGateway(s)
	ctx := context.Background()

	prov := &compute.Provider{
		Name: "t-prov-b", Type: "api", BaseURL: "http://x",
		APIKey: "k", Enabled: true,
	}
	if err := s.CreateProvider(ctx, prov); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	mdl := &compute.Model{
		ProviderID: prov.ID, Name: "t-b", Modality: "text",
		Enabled: true,
	}
	if err := s.CreateModel(ctx, mdl); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	_, err := gw.Chat(ctx, compute.ChatRequest{
		TaskType: "spec",
		Model:    mdl.ID, // 真实 model，证明跳过 auth 后走到转发
		UserID:   "",     // 空 → 不校验（兼容老调用）
		Messages: []map[string]interface{}{{"role": "user", "content": "hi"}},
	})
	if errors.Is(err, compute.ErrModelNotAuthorized) {
		t.Fatal("空 UserID 不应触发授权拒绝")
	}
}
