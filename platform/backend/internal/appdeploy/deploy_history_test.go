package appdeploy

import (
	"context"
	"testing"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newHistStore 建干净 deploy_history 环境（truncate 独立，防与其它 fixture 串数据）。
func newHistStore(t *testing.T) *Store {
	t.Helper()
	db := testutil.TestDB(t)
	testutil.Truncate(t, db, "deploy_history")
	return NewStore(db)
}

// TestDeployHistory_InsertFinishRoundtrip 一行=一次尝试：INSERT 在途 → Finish 终态。
func TestDeployHistory_InsertFinishRoundtrip(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()

	if err := s.InsertDeployHistory(ctx, "app_x", EnvTest, 3, "fixed", "yxt", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	// 在途行：result=''，duration/finished 为 NULL
	items, err := s.ListDeployHistoryByApp(ctx, "app_x", 20)
	if err != nil || len(items) != 1 {
		t.Fatalf("在途应 1 行: %v %v", items, err)
	}
	if items[0].Result != "" || items[0].DurationSec != nil || items[0].FinishedAt != nil {
		t.Fatalf("在途行应为空 result/nil duration: %+v", items[0])
	}
	// 终结 success
	if err := s.FinishDeployHistory(ctx, "app_x", EnvTest, 3, "success", "", "", "appdeploy/x-test:v3", 9100); err != nil {
		t.Fatalf("finish: %v", err)
	}
	items, _ = s.ListDeployHistoryByApp(ctx, "app_x", 20)
	if len(items) != 1 || items[0].Result != "success" || items[0].DurationSec == nil || *items[0].DurationSec < 0 {
		t.Fatalf("终态行: %+v", items[0])
	}
	if items[0].Image != "appdeploy/x-test:v3" || items[0].HostPort != 9100 {
		t.Fatalf("成功行应记实态镜像/端口: %+v", items[0])
	}
	// 重复终结 no-op（result='' 守卫）：再次 finish failed 不覆写
	_ = s.FinishDeployHistory(ctx, "app_x", EnvTest, 3, "failed", "later", "", "", 0)
	items, _ = s.ListDeployHistoryByApp(ctx, "app_x", 20)
	if items[0].Result != "success" {
		t.Fatalf("已终态行不得被覆写: %+v", items[0])
	}
}

// TestDeployHistory_OrphanFiltered 孤儿行（result=” 超 30min）不进 List；在途（<30min）可见。
func TestDeployHistory_OrphanFiltered(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	_ = s.InsertDeployHistory(ctx, "app_o", EnvTest, 1, "ai", "yxt", "")
	// backdate 成孤儿
	if _, err := s.db.Exec(`UPDATE deploy_history SET created_at = NOW() - interval '31 minutes' WHERE app_id='app_o'`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	_ = s.InsertDeployHistory(ctx, "app_o", EnvTest, 2, "ai", "yxt", "") // 在途（新）
	_ = s.InsertDeployHistory(ctx, "app_o", EnvTest, 3, "ai", "yxt", "")
	_ = s.FinishDeployHistory(ctx, "app_o", EnvTest, 3, "failed", "boom", "", "", 0)

	items, err := s.ListDeployHistoryByApp(ctx, "app_o", 20)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("孤儿应被过滤（剩在途 v2 + 终态 v3 共 2 行），得到 %d: %+v", len(items), items)
	}
}

// TestDeployHistory_OperatorNormalized 空 operator 归一 unknown。
func TestDeployHistory_OperatorNormalized(t *testing.T) {
	s := newHistStore(t)
	ctx := context.Background()
	if err := s.InsertDeployHistory(ctx, "app_n", EnvTest, 1, "fixed", "", ""); err != nil {
		t.Fatalf("insert: %v", err)
	}
	items, _ := s.ListDeployHistoryByApp(ctx, "app_n", 20)
	if len(items) != 1 || items[0].Operator != "unknown" {
		t.Fatalf("空 operator 应归一 unknown: %+v", items)
	}
}
