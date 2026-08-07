package compute_test

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestGrantModels_AndListRevokeIsGranted 覆盖授权 CRUD 往返 + 幂等。
// 夹具不设 ID（CreateProvider/CreateModel 自动生成 cpv_/cmd_ 前缀），
// 测试间用 testutil.Truncate 清表隔离（compute 包首个 DB-backed 测试）。
func TestGrantModels_AndListRevokeIsGranted(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "user_model_grant", "compute_model", "compute_provider")
	s := compute.NewStore(db)
	ctx := context.Background()

	// 前置：一个 provider + 一个 model
	prov := &compute.Provider{
		Name: "t-prov", Type: "api", BaseURL: "http://x",
		APIKey: "k", Enabled: true,
	}
	if err := s.CreateProvider(ctx, prov); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	mdl := &compute.Model{
		ProviderID: prov.ID, Name: "t-model", Modality: "text",
		Enabled: true,
	}
	if err := s.CreateModel(ctx, mdl); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}

	// 授权前：未授权
	ok, err := s.IsGranted(ctx, "u1", mdl.ID)
	if err != nil {
		t.Fatalf("IsGranted(授权前): %v", err)
	}
	if ok {
		t.Fatal("授权前不应 IsGranted")
	}

	// 授权
	if n, err := s.GrantModels(ctx, "u1", []string{mdl.ID}, "admin"); err != nil {
		t.Fatalf("GrantModels: %v", err)
	} else if n != 1 {
		t.Fatalf("首次授权应新增 1 行，got %d", n)
	}

	// 授权后：IsGranted=true
	ok, err = s.IsGranted(ctx, "u1", mdl.ID)
	if err != nil {
		t.Fatalf("IsGranted(授权后): %v", err)
	}
	if !ok {
		t.Fatal("授权后应 IsGranted")
	}

	// ListGrants 返回该模型
	list, err := s.ListGrants(ctx, "u1")
	if err != nil {
		t.Fatalf("ListGrants: %v", err)
	}
	if len(list) != 1 || list[0].ID != mdl.ID {
		t.Fatalf("ListGrants 期望 1 个 %s，got %+v", mdl.ID, list)
	}

	// 重复授权幂等（ON CONFLICT DO NOTHING）：返回新增 0 行（已存在的 model 不计）
	if n, err := s.GrantModels(ctx, "u1", []string{mdl.ID}, "admin"); err != nil {
		t.Fatalf("重复授权应幂等: %v", err)
	} else if n != 0 {
		t.Fatalf("重复授权应新增 0 行（幂等），got %d", n)
	}
	list, _ = s.ListGrants(ctx, "u1")
	if len(list) != 1 {
		t.Fatalf("重复授权后仍应 1 个，got %d", len(list))
	}

	// 收回
	if err := s.RevokeModel(ctx, "u1", mdl.ID); err != nil {
		t.Fatalf("RevokeModel: %v", err)
	}
	ok, _ = s.IsGranted(ctx, "u1", mdl.ID)
	if ok {
		t.Fatal("收回后不应 IsGranted")
	}
}

// TestGrantModels_CascadeOnDeleteModel 覆盖 model_id ON DELETE CASCADE：
// 删除模型时其授权行被自动清理。
func TestGrantModels_CascadeOnDeleteModel(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "user_model_grant", "compute_model", "compute_provider")
	s := compute.NewStore(db)
	ctx := context.Background()

	prov := &compute.Provider{
		Name: "t-prov-c", Type: "api", BaseURL: "http://x",
		APIKey: "k", Enabled: true,
	}
	if err := s.CreateProvider(ctx, prov); err != nil {
		t.Fatalf("CreateProvider: %v", err)
	}
	mdl := &compute.Model{
		ProviderID: prov.ID, Name: "t-c", Modality: "text",
		Enabled: true,
	}
	if err := s.CreateModel(ctx, mdl); err != nil {
		t.Fatalf("CreateModel: %v", err)
	}
	if _, err := s.GrantModels(ctx, "u2", []string{mdl.ID}, "admin"); err != nil {
		t.Fatalf("GrantModels: %v", err)
	}

	// 删 model → CASCADE 删授权
	if err := s.DeleteModel(ctx, mdl.ID); err != nil {
		t.Fatalf("DeleteModel: %v", err)
	}
	ok, err := s.IsGranted(ctx, "u2", mdl.ID)
	if err != nil {
		t.Fatalf("IsGranted(级联后): %v", err)
	}
	if ok {
		t.Fatal("删 model 后授权应被级联删除")
	}
}
