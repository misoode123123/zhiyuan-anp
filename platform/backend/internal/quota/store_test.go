package quota

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"zhiyuan-anp/platform/backend/internal/testutil"
)

// newPS 在 project_space 表里插一条新项目空间，返回其 id（FK 约束要求 project_quota 必须先有父行）。
func newPS(t *testing.T) string {
	t.Helper()
	db := testutil.TestDB(t)
	psID := "ps_" + uuid.NewString()[:20]
	name := "quota-test-" + psID
	if _, err := db.Exec(
		`INSERT INTO project_space (id, name, slug, status) VALUES ($1, $2, $3, 'active')`,
		psID, name, "slug-"+psID); err != nil {
		t.Fatalf("建 project_space: %v", err)
	}
	t.Cleanup(func() {
		// 删项目空间级联清 project_quota（FK ON DELETE CASCADE）
		_, _ = db.Exec(`DELETE FROM project_space WHERE id=$1`, psID)
	})
	return psID
}

func TestStore_GetOrCreate_Defaults(t *testing.T) {
	db := testutil.TestDB(t)
	psID := newPS(t)
	testutil.Truncate(t, db, "project_quota")

	s := NewStore(db)
	q, err := s.GetOrCreate(context.Background(), psID)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if q.ProjectSpaceID != psID {
		t.Errorf("ProjectSpaceID = %q, want %q", q.ProjectSpaceID, psID)
	}
	if q.MaxApps != DefaultMaxApps {
		t.Errorf("MaxApps = %d, want %d", q.MaxApps, DefaultMaxApps)
	}
	if q.MaxDatabases != DefaultMaxDatabases {
		t.Errorf("MaxDatabases = %d, want %d", q.MaxDatabases, DefaultMaxDatabases)
	}
	if q.MaxTotalDBMb != DefaultMaxTotalDBMb {
		t.Errorf("MaxTotalDBMb = %d, want %d", q.MaxTotalDBMb, DefaultMaxTotalDBMb)
	}
	if q.MaxCapabilityCallsPerDay != DefaultMaxCapabilityCallsPerDay {
		t.Errorf("MaxCapabilityCallsPerDay = %d, want %d", q.MaxCapabilityCallsPerDay, DefaultMaxCapabilityCallsPerDay)
	}
}

func TestStore_GetOrCreate_Idempotent(t *testing.T) {
	// 已存在 → 直接返回已有（不覆盖）
	db := testutil.TestDB(t)
	psID := newPS(t)
	testutil.Truncate(t, db, "project_quota")

	s := NewStore(db)
	// 预置：max_apps=5
	if _, err := db.Exec(
		`INSERT INTO project_quota (project_space_id, max_apps, max_databases, max_total_db_mb, max_capability_calls_per_day)
		 VALUES ($1, 5, 6, 7, 8)`, psID); err != nil {
		t.Fatalf("seed: %v", err)
	}
	q, err := s.GetOrCreate(context.Background(), psID)
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if q.MaxApps != 5 || q.MaxDatabases != 6 || q.MaxTotalDBMb != 7 || q.MaxCapabilityCallsPerDay != 8 {
		t.Errorf("GetOrCreate 覆盖了已存在行: %+v", q)
	}
}

func TestStore_Set(t *testing.T) {
	db := testutil.TestDB(t)
	psID := newPS(t)
	testutil.Truncate(t, db, "project_quota")

	s := NewStore(db)
	// 不存在 → 报 ErrNotExists
	if err := s.Set(context.Background(), psID, 1, 2, 3, 4, 5); err != ErrNotExists {
		t.Errorf("不存在时 Set err = %v, want ErrNotExists", err)
	}
	// 先 GetOrCreate 建默认
	if _, err := s.GetOrCreate(context.Background(), psID); err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	// Set 新值（含第 5 列 max_dedicated_instances）
	if err := s.Set(context.Background(), psID, 30, 40, 50, 60, 70); err != nil {
		t.Fatalf("Set: %v", err)
	}
	q, _ := s.Get(context.Background(), psID)
	if q.MaxApps != 30 || q.MaxDatabases != 40 || q.MaxTotalDBMb != 50 || q.MaxCapabilityCallsPerDay != 60 {
		t.Errorf("Set 后值不对: %+v", q)
	}
	if q.MaxDedicatedInstances != 70 {
		t.Errorf("MaxDedicatedInstances = %d, want 70", q.MaxDedicatedInstances)
	}
	// updated_at 应最近
	if time.Since(q.UpdatedAt) > 5*time.Second {
		t.Errorf("updated_at 未刷新: %v", q.UpdatedAt)
	}
}

func TestStore_GetOrCreate_Concurrent(t *testing.T) {
	// 并发 GetOrCreate 不应冲突（ON CONFLICT DO NOTHING 兜底）
	db := testutil.TestDB(t)
	psID := newPS(t)
	testutil.Truncate(t, db, "project_quota")

	s := NewStore(db)
	const N = 8
	errs := make(chan error, N)
	for i := 0; i < N; i++ {
		go func() {
			_, err := s.GetOrCreate(context.Background(), psID)
			errs <- err
		}()
	}
	for i := 0; i < N; i++ {
		if err := <-errs; err != nil {
			t.Errorf("并发 GetOrCreate 出错: %v", err)
		}
	}
	// 最终仅一行
	q, _ := s.Get(context.Background(), psID)
	if q == nil {
		t.Fatal("并发后配额行丢失")
	}
	if q.MaxApps != DefaultMaxApps {
		t.Errorf("并发覆盖了默认值: MaxApps=%d", q.MaxApps)
	}
}
