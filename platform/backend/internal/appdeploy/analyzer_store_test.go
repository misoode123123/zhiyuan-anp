package appdeploy

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

func TestStore_SaveGetAnalysis(t *testing.T) {
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "appdeploy_deploy_analysis")
	st := NewStore(db)
	ctx := context.Background()

	a := &DeployAnalysis{Language: "go", AppKindGuess: "headless", Deps: []DeployDep{{Kind: "redis", Addr: "127.0.0.1:6379"}}}
	if err := st.SaveAnalysis(ctx, "app_1", a); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := st.GetAnalysis(ctx, "app_1")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Language != "go" || got.AppKindGuess != "headless" || len(got.Deps) != 1 || got.Deps[0].Kind != "redis" {
		t.Errorf("回读不一致: %+v", got)
	}

	// upsert 覆盖
	if err := st.SaveAnalysis(ctx, "app_1", &DeployAnalysis{Language: "node"}); err != nil {
		t.Fatalf("Save2: %v", err)
	}
	got2, _ := st.GetAnalysis(ctx, "app_1")
	if got2.Language != "node" {
		t.Errorf("upsert 后 Language=%q want node", got2.Language)
	}

	// 无记录返回 nil
	if g, _ := st.GetAnalysis(ctx, "not_exist"); g != nil {
		t.Errorf("无记录应返回 nil, got %+v", g)
	}
}
