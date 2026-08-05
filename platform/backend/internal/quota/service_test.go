package quota

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newPS2 类似 newPS，但用独立 project_space_id 前缀避免命名冲突；返回 (psID, appIDs 插入辅助)。
func setupPS(t *testing.T) (string, *Service) {
	t.Helper()
	db := testutil.TestDB(t)
	psID := "ps_" + uuid.NewString()[:20]
	if _, err := db.Exec(
		`INSERT INTO project_space (id, name, slug, status) VALUES ($1, $2, $3, 'active')`,
		psID, "svc-test-"+psID, "slug-"+psID); err != nil {
		t.Fatalf("建 project_space: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM project_space WHERE id=$1`, psID)
	})
	// 清配额让 GetOrCreate 重建默认
	testutil.Truncate(t, db, "project_quota")
	svc := NewService(NewStore(db), nil, nil)
	return psID, svc
}

// insertApp 插入应用（FK project_space_id 有效）。
func insertApp(t *testing.T, psID, name string) {
	t.Helper()
	db := testutil.TestDB(t)
	aid := "app_" + uuid.NewString()[:18]
	if _, err := db.Exec(
		`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status)
		 VALUES ($1, $2, $3, 8080, 'registered')`, aid, psID, name); err != nil {
		t.Fatalf("建 app: %v", err)
	}
}

func TestService_CheckApps_WithinLimit(t *testing.T) {
	psID, svc := setupPS(t)
	if err := svc.CheckApps(context.Background(), psID); err != nil {
		t.Errorf("空项目 CheckApps 应通过: %v", err)
	}
	insertApp(t, psID, "app1")
	if err := svc.CheckApps(context.Background(), psID); err != nil {
		t.Errorf("1 应用 CheckApps 应通过（默认上限 20）: %v", err)
	}
}

func TestService_CheckApps_Exceeded(t *testing.T) {
	psID, svc := setupPS(t)
	// 调小上限到 0（已用 0 ≥ 0 → 拦截）
	if _, err := svc.Set(context.Background(), psID, 0, 20, 10240, 10000); err != nil {
		t.Fatalf("Set: %v", err)
	}
	err := svc.CheckApps(context.Background(), psID)
	if err == nil {
		t.Fatal("超限应报错")
	}
	qe, ok := err.(*QuotaExceededError)
	if !ok {
		t.Fatalf("错误类型应为 *QuotaExceededError, got %T", err)
	}
	if qe.Dimension != DimensionApps {
		t.Errorf("Dimension = %q, want %q", qe.Dimension, DimensionApps)
	}
	if qe.Limit != 0 {
		t.Errorf("Limit = %d, want 0", qe.Limit)
	}
	// 0 应用 0 上限，已用 0 也算超限（>=），符合"max=0 表示完全禁用"
	if qe.Used != 0 {
		t.Errorf("Used = %d, want 0", qe.Used)
	}
	// 错误消息友好
	if msg := qe.Error(); msg == "" {
		t.Error("Error() 空消息")
	}
	t.Logf("超限消息: %s", qe)
}

func TestService_CheckDatabases_Exceeded(t *testing.T) {
	psID, svc := setupPS(t)
	if _, err := svc.Set(context.Background(), psID, 20, 0, 10240, 10000); err != nil {
		t.Fatalf("Set: %v", err)
	}
	err := svc.CheckDatabases(context.Background(), psID)
	if err == nil {
		t.Fatal("超限应报错")
	}
	qe, ok := err.(*QuotaExceededError)
	if !ok {
		t.Fatalf("错误类型错: %T", err)
	}
	if qe.Dimension != DimensionDatabases {
		t.Errorf("Dimension = %q, want databases", qe.Dimension)
	}
}

func TestService_CheckCapabilityToday_Exceeded(t *testing.T) {
	psID, svc := setupPS(t)
	// 插入 1 条今日用量
	db := testutil.TestDB(t)
	_, err := db.Exec(
		`INSERT INTO capability_usage (id, project_space_id, skill_id, success)
		 VALUES ($1, $2, 'skl_test', TRUE)`, "usg_"+uuid.NewString()[:18], psID)
	if err != nil {
		t.Fatalf("seed usage: %v", err)
	}
	if _, err := svc.Set(context.Background(), psID, 20, 20, 10240, 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	err = svc.CheckCapabilityToday(context.Background(), psID)
	if err == nil {
		t.Fatal("超限应报错")
	}
	qe, _ := err.(*QuotaExceededError)
	if qe.Dimension != DimensionCapabilityToday {
		t.Errorf("Dimension = %q", qe.Dimension)
	}
	if qe.Used != 1 {
		t.Errorf("Used = %d, want 1", qe.Used)
	}
	if qe.Limit != 0 {
		t.Errorf("Limit = %d, want 0", qe.Limit)
	}
	if qe.Unit != "次" {
		t.Errorf("Unit = %q, want 次", qe.Unit)
	}
}

func TestService_Usage(t *testing.T) {
	psID, svc := setupPS(t)
	insertApp(t, psID, "a1")
	insertApp(t, psID, "a2")
	// Usage 应反映 used_apps=2，其余 0
	u, err := svc.Usage(context.Background(), psID)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.UsedApps != 2 {
		t.Errorf("UsedApps = %d, want 2", u.UsedApps)
	}
	if u.UsedDatabases != 0 {
		t.Errorf("UsedDatabases = %d, want 0", u.UsedDatabases)
	}
	if u.UsedCapabilityToday != 0 {
		t.Errorf("UsedCapabilityToday = %d, want 0", u.UsedCapabilityToday)
	}
	if u.UsedDBSizeMb != 0 {
		t.Errorf("UsedDBSizeMb = %d, want 0（未注入 PGAdmin）", u.UsedDBSizeMb)
	}
	if u.Quota.MaxApps != DefaultMaxApps {
		t.Errorf("Quota.MaxApps = %d, want default", u.Quota.MaxApps)
	}
}

// fakePGSizeChecker 假 PGSizeChecker（注入测试库大小）。
type fakePGSizeChecker struct {
	sizes map[string]int64
}

func (f fakePGSizeChecker) DatabaseSizes(ctx context.Context, adminURL string, dbNames []string) (map[string]int64, error) {
	return f.sizes, nil
}

// fakeInstanceLookup 假 InstanceLookup 返回固定 admin_url（非空触发查 size）。
type fakeInstanceLookup struct{ url string }

func (f fakeInstanceLookup) GetInstanceAdminURL(ctx context.Context, psID string) (string, error) {
	return f.url, nil
}

// insertInstanceForFK 建一条 pg_instance 记录满足 appdeploy_database 的 FK 约束。
func insertInstanceForFK(t *testing.T, db interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
}, psID, pgiID string) {
	t.Helper()
	if _, err := db.Exec(
		`INSERT INTO pg_instance (id, project_space_id, host, port, admin_url_ref, deploy_mode, status)
		 VALUES ($1, $2, 'localhost', 5432, 'postgres://fake', 'managed', 'active')`, pgiID, psID); err != nil {
		t.Fatalf("建 pg_instance: %v", err)
	}
}

func TestService_CheckDBSize(t *testing.T) {
	psID, _ := setupPS(t)
	// 注：setupPS 用 NewService(nil,nil)，需重建 svc 带 fakes
	db := testutil.TestDB(t)
	// 正式路径：插真 app + pg_instance + appdb（FK 一致）
	aid := "app_" + uuid.NewString()[:18]
	if _, err := db.Exec(
		`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status)
		 VALUES ($1, $2, 'sizeapp', 8080, 'registered')`, aid, psID); err != nil {
		t.Fatalf("建 app: %v", err)
	}
	pgiID := "pgi_" + uuid.NewString()[:18]
	insertInstanceForFK(t, db, psID, pgiID)
	dbName := "app_" + uuid.NewString()[:8]
	if _, err := db.Exec(
		`INSERT INTO appdeploy_database (id, app_id, project_space_id, db_name, db_role, pg_instance_id, db_host, db_port, status)
		 VALUES ($1, $2, $3, $4, $4, $5, 'localhost', 5432, 'ready')`,
		"apdb_"+uuid.NewString()[:18], aid, psID, dbName, pgiID); err != nil {
		t.Fatalf("建 appdb: %v", err)
	}

	// fake 返回 5MB
	svcWithFakes := NewService(NewStore(db), fakeInstanceLookup{url: "postgres://fake"}, fakePGSizeChecker{
		sizes: map[string]int64{dbName: 5 * 1024 * 1024},
	})
	// 上限 10MB，已用 5MB → 通过
	if _, err := svcWithFakes.Set(context.Background(), psID, 20, 20, 10, 10000); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := svcWithFakes.CheckDBSize(context.Background(), psID); err != nil {
		t.Errorf("5/10MB 应通过: %v", err)
	}
	// 上限 5MB，已用 5MB → 拦截（>=）
	if _, err := svcWithFakes.Set(context.Background(), psID, 20, 20, 5, 10000); err != nil {
		t.Fatalf("Set: %v", err)
	}
	err := svcWithFakes.CheckDBSize(context.Background(), psID)
	if err == nil {
		t.Fatal("5/5MB 应拦截")
	}
	qe, ok := err.(*QuotaExceededError)
	if !ok {
		t.Fatalf("错误类型: %T", err)
	}
	if qe.Dimension != DimensionDBSize {
		t.Errorf("Dimension = %q", qe.Dimension)
	}
	if qe.Used != 5 || qe.Limit != 5 {
		t.Errorf("Used=%d Limit=%d, want 5/5", qe.Used, qe.Limit)
	}
	if qe.Unit != "MB" {
		t.Errorf("Unit = %q, want MB", qe.Unit)
	}
}

func TestService_Usage_WithDBSize(t *testing.T) {
	psID, _ := setupPS(t)
	db := testutil.TestDB(t)
	aid := "app_" + uuid.NewString()[:18]
	dbName := "app_" + uuid.NewString()[:8]
	if _, err := db.Exec(
		`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status)
		 VALUES ($1, $2, 'sizeusage', 8080, 'registered')`, aid, psID); err != nil {
		t.Fatalf("建 app: %v", err)
	}
	pgiID := "pgi_" + uuid.NewString()[:18]
	insertInstanceForFK(t, db, psID, pgiID)
	if _, err := db.Exec(
		`INSERT INTO appdeploy_database (id, app_id, project_space_id, db_name, db_role, pg_instance_id, db_host, db_port, status)
		 VALUES ($1, $2, $3, $4, $4, $5, 'localhost', 5432, 'ready')`,
		"apdb_"+uuid.NewString()[:18], aid, psID, dbName, pgiID); err != nil {
		t.Fatalf("建 appdb: %v", err)
	}
	// fake 返回 3.5MB → 向上取整 4MB
	svc := NewService(NewStore(db), fakeInstanceLookup{url: "postgres://fake"}, fakePGSizeChecker{
		sizes: map[string]int64{dbName: 3*1024*1024 + 512*1024},
	})
	u, err := svc.Usage(context.Background(), psID)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if u.UsedDBSizeMb != 4 {
		t.Errorf("UsedDBSizeMb = %d, want 4 (向上取整)", u.UsedDBSizeMb)
	}
	if u.UsedApps != 1 {
		t.Errorf("UsedApps = %d, want 1", u.UsedApps)
	}
	if u.UsedDatabases != 1 {
		t.Errorf("UsedDatabases = %d, want 1", u.UsedDatabases)
	}
}

func TestQuotaExceededError_Message(t *testing.T) {
	cases := []struct {
		e    *QuotaExceededError
		want string
	}{
		{&QuotaExceededError{Dimension: DimensionApps, Used: 5, Limit: 5}, "应用数已达上限：5 / 5"},
		{&QuotaExceededError{Dimension: DimensionCapabilityToday, Used: 100, Limit: 100, Unit: "次"}, "今日 AI 调用已达上限：100次 / 100次"},
		{&QuotaExceededError{Dimension: DimensionDBSize, Used: 10240, Limit: 10240, Unit: "MB"}, "数据库总大小已达上限：10240MB / 10240MB"},
	}
	for i, c := range cases {
		got := c.e.Error()
		if got != c.want {
			t.Errorf("case %d Error() = %q, want %q", i, got, c.want)
		}
		t.Logf("case %d: %s", i, got)
	}
	// 友好提示需含"上限"两字
	for _, c := range cases {
		if msg := fmt.Sprint(c.e); msg == "" {
			t.Error("空消息")
		}
	}
}

func TestIsQuotaExceeded(t *testing.T) {
	qe := &QuotaExceededError{Dimension: DimensionApps, Used: 1, Limit: 1}
	if !IsQuotaExceeded(qe) {
		t.Error("IsQuotaExceeded(*QuotaExceededError) 应 true")
	}
	if IsQuotaExceeded(nil) {
		t.Error("IsQuotaExceeded(nil) 应 false")
	}
	// 普通错误
	if IsQuotaExceeded(fmt.Errorf("其他错误")) {
		t.Error("IsQuotaExceeded(普通 err) 应 false")
	}
	// wrap 后仍能识别
	if !IsQuotaExceeded(fmt.Errorf("wrap: %w", qe)) {
		t.Error("wrapped 应识别")
	}
}

// insertDedicated 建 1 个 active dedicated service_instance + app + binding（binding→app→ps 归属 psID）。
// 自清理：binding.service_instance_id RESTRICT instance → 先删 binding 再删 instance。
// 注：appdeploy_service_binding.project_space_id / env_key 为 NOT NULL（000028），
// 需显式给值（brief 原文漏列 → NOT NULL 违规；本处补正，语义不变）。
func insertDedicated(t *testing.T, psID, kind string) {
	t.Helper()
	db := testutil.TestDB(t)
	insID := "svinst-ded-" + uuid.NewString()[:14]
	appID := "app_" + uuid.NewString()[:18]
	bndID := "bnd_" + uuid.NewString()[:14]
	envKey := strings.ToUpper(kind) + "_ADDR"
	must := func(q string, args ...any) {
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	must(`INSERT INTO appdeploy_service_instance (id, kind, name, supply_mode, host, port, status) VALUES ($1,$2,$3,'dedicated','h',0,'active')`, insID, kind, insID)
	must(`INSERT INTO appdeploy_application (id, project_space_id, name, internal_port, status) VALUES ($1,$2,$3,8080,'running')`, appID, psID, appID)
	must(`INSERT INTO appdeploy_service_binding (id, app_id, project_space_id, service_kind, strategy, service_instance_id, env_key, status) VALUES ($1,$2,$3,$4,'dedicated',$5,$6,'bound')`, bndID, appID, psID, kind, insID, envKey)
	t.Cleanup(func() {
		db.Exec(`DELETE FROM appdeploy_service_binding WHERE id=$1`, bndID)
		db.Exec(`DELETE FROM appdeploy_service_instance WHERE id=$1`, insID)
		db.Exec(`DELETE FROM appdeploy_application WHERE id=$1`, appID)
	})
}

// TestService_CheckDedicatedInstances 边界：N<5 通过；N=5 超限（默认上限 5）。
// 跨 kind 合计；非 active 不算。qe.Limit==5 亦验证 GetOrCreate 默认值。
func TestService_CheckDedicatedInstances(t *testing.T) {
	psID, svc := setupPS(t)
	ctx := context.Background()
	for _, k := range []string{"redis", "milvus", "pg", "redis"} {
		insertDedicated(t, psID, k)
	}
	if err := svc.CheckDedicatedInstances(ctx, psID); err != nil {
		t.Fatalf("4 active dedicated 应通过（默认上限 5）: %v", err)
	}
	insertDedicated(t, psID, "milvus") // 第 5 个 → used=5 >= 5
	err := svc.CheckDedicatedInstances(ctx, psID)
	qe, ok := err.(*QuotaExceededError)
	if !ok {
		t.Fatalf("5 active dedicated 应 *QuotaExceededError，得 %v", err)
	}
	if qe.Dimension != DimensionDedicated || qe.Limit != 5 {
		t.Fatalf("维度/上限不符: %+v", qe)
	}
	// 非 active 不算：把一个转 draining → 计数回 4
	db := testutil.TestDB(t)
	if _, err := db.Exec(`UPDATE appdeploy_service_instance SET status='draining' WHERE id IN (SELECT b.service_instance_id FROM appdeploy_service_binding b JOIN appdeploy_application a ON a.id=b.app_id WHERE a.project_space_id=$1 LIMIT 1)`, psID); err != nil {
		t.Fatalf("转 draining: %v", err)
	}
	if n, _ := svc.countDedicatedInstances(ctx, psID); n != 4 {
		t.Fatalf("1 个转 draining 后应计 4，得 %d", n)
	}
}
