package compute_test

import (
	"context"
	"os"
	"testing"

	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/testutil"
)

// TestStore_GenerateOpenCodeConfigForModels 覆盖 per-user opencode config 生成 +
// cmd_xxx → "provider/name" 解析 + 写盘。过滤语义：只含授权模型，同 provider 的
// 未授权模型必须排除；provider 命中 ≥1 授权模型才出现。
func TestStore_GenerateOpenCodeConfigForModels(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "compute_model", "compute_provider")
	s := compute.NewStore(db)
	ctx := context.Background()
	prov := &compute.Provider{Name: "ZAI Coding", Type: "api", BaseURL: "http://x", APIKey: "k1", Enabled: true}
	if err := s.CreateProvider(ctx, prov); err != nil {
		t.Fatal(err)
	}
	m1 := &compute.Model{ProviderID: prov.ID, Name: "glm-5.1", Modality: "code", ContextWindow: 204800, MaxOutput: 131072, Enabled: true}
	if err := s.CreateModel(ctx, m1); err != nil {
		t.Fatal(err)
	}
	m2 := &compute.Model{ProviderID: prov.ID, Name: "glm-5-turbo", Modality: "code", ContextWindow: 204800, MaxOutput: 131072, Enabled: true}
	if err := s.CreateModel(ctx, m2); err != nil {
		t.Fatal(err)
	}

	cfg, err := s.GenerateOpenCodeConfigForModels(ctx, []string{m1.ID})
	if err != nil {
		t.Fatalf("GenerateOpenCodeConfigForModels: %v", err)
	}
	p, ok := cfg.Provider["zai-coding"]
	if !ok {
		t.Fatalf("期望 provider key zai-coding，got %v", cfg.Provider)
	}
	if p.Options.APIKey != "k1" {
		t.Errorf("apiKey 期望 k1，got %v", p.Options.APIKey)
	}
	if _, has := p.Models["glm-5.1"]; !has {
		t.Error("期望含 glm-5.1")
	}
	if _, has := p.Models["glm-5-turbo"]; has {
		t.Error("不应含 glm-5-turbo（未授权）")
	}
	// 默认模型：首个授权模型写入顶层 model + small_model，杜绝 opencode 回退内置免费模型
	// (big-pickle) 当默认/后台小模型 → 界面默认显示免费模型 + 后台任务 429 FreeUsageLimitError。
	if cfg.Model != "zai-coding/glm-5.1" {
		t.Errorf("默认 model 期望 zai-coding/glm-5.1，got %q", cfg.Model)
	}
	if cfg.SmallModel != "zai-coding/glm-5.1" {
		t.Errorf("默认 small_model 期望 zai-coding/glm-5.1，got %q", cfg.SmallModel)
	}

	id, err := s.ResolveOpencodeModelID(ctx, m1.ID)
	if err != nil || id != "zai-coding/glm-5.1" {
		t.Fatalf("ResolveOpencodeModelID 期望 zai-coding/glm-5.1，got %q err=%v", id, err)
	}
	name, err := s.ModelName(ctx, m1.ID)
	if err != nil || name != "glm-5.1" {
		t.Fatalf("ModelName 期望 glm-5.1，got %q err=%v", name, err)
	}
	// 写盘
	tmp := t.TempDir() + "/opencode/opencode.json"
	if err := s.WriteOpenCodeConfigForModels(ctx, []string{m1.ID}, tmp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tmp); err != nil {
		t.Errorf("config 未写入: %v", err)
	}

	// 未命中：ResolveOpencodeModelID / ModelName 返回 ("", nil)，不报错。
	if id, err := s.ResolveOpencodeModelID(ctx, "cmd_does_not_exist"); err != nil || id != "" {
		t.Fatalf("未命中 ResolveOpencodeModelID 期望 (\"\", nil)，got %q err=%v", id, err)
	}
	if name, err := s.ModelName(ctx, "cmd_does_not_exist"); err != nil || name != "" {
		t.Fatalf("未命中 ModelName 期望 (\"\", nil)，got %q err=%v", name, err)
	}

	// 空 modelIDs → 无 provider（无命中模型）。
	empty, err := s.GenerateOpenCodeConfigForModels(ctx, nil)
	if err != nil {
		t.Fatalf("空 modelIDs GenerateOpenCodeConfigForModels: %v", err)
	}
	if len(empty.Provider) != 0 {
		t.Errorf("空 modelIDs 期望 0 provider，got %d", len(empty.Provider))
	}
	// 空 modelIDs → 无默认 model（无命中模型，不写顶层默认）。
	if empty.Model != "" || empty.SmallModel != "" {
		t.Errorf("空 modelIDs 期望无默认 model，got model=%q small_model=%q", empty.Model, empty.SmallModel)
	}
}

// TestStore_NoKeyProviderGuard #30：无 APIKey 的 provider 不进 opencode config，
// 其模型也不解析为可用 ref（建会话不注入），从源头杜绝 opencode 用空 key → 401 空白。
func TestStore_NoKeyProviderGuard(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "compute_model", "compute_provider")
	s := compute.NewStore(db)
	ctx := context.Background()
	// 无 key provider（典型：glm-coding-plan 忘填 key）
	noKey := &compute.Provider{Name: "NoKey Prov", Type: "api", BaseURL: "http://x", APIKey: "", Enabled: true}
	if err := s.CreateProvider(ctx, noKey); err != nil {
		t.Fatal(err)
	}
	m := &compute.Model{ProviderID: noKey.ID, Name: "glm-coding-plan", Modality: "code", ContextWindow: 204800, MaxOutput: 131072, Enabled: true}
	if err := s.CreateModel(ctx, m); err != nil {
		t.Fatal(err)
	}
	// 即便授权了该模型，config 也不应含其 provider（无 key → 跳过）
	cfg, err := s.GenerateOpenCodeConfigForModels(ctx, []string{m.ID})
	if err != nil {
		t.Fatalf("GenerateOpenCodeConfigForModels: %v", err)
	}
	if len(cfg.Provider) != 0 {
		t.Errorf("无 key provider 不应进 config，got %d provider: %v", len(cfg.Provider), cfg.Provider)
	}
	if cfg.Model != "" {
		t.Errorf("无 key provider 不应作为默认 model，got %q", cfg.Model)
	}
	// ResolveOpencodeModelID：无 key 不解析为可用 ref → 建会话不注入
	id, err := s.ResolveOpencodeModelID(ctx, m.ID)
	if err != nil {
		t.Fatalf("ResolveOpencodeModelID: %v", err)
	}
	if id != "" {
		t.Errorf("无 key provider 的模型不应解析为 ref，got %q（应为空→建会话不注入）", id)
	}
}
