package appdeploy

import (
	"context"
	"testing"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// TestSeedBuildConfigs_Idempotent 验证 4 种非 web 形态构建配置被幂等写入：
// 第一次 seed 后 4 种 kind 都能 Get 到；再 seed 一次不报错。
func TestSeedBuildConfigs_Idempotent(t *testing.T) {
	db, _ := sqlx.Connect("sqlite", ":memory:")
	db.Exec(`CREATE TABLE appdeploy_build_config (app_kind TEXT PRIMARY KEY, build_image TEXT, build_command TEXT, artifact_dir TEXT, scaffold TEXT, created_at DATETIME)`)
	if err := SeedBuildConfigs(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	bc := NewBuildConfigStore(db)
	for _, k := range []string{AppKindDesktop, AppKindMobile, AppKindCLI, AppKindService} {
		if _, err := bc.Get(context.Background(), k); err != nil {
			t.Fatalf("kind %s not seeded: %v", k, err)
		}
	}
	// 再 seed 一次不报错（幂等）
	if err := SeedBuildConfigs(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	// 幂等后仍恰好 4 条，不重复插入
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM appdeploy_build_config`); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("want 4 build configs after re-seed, got %d", n)
	}
}

// TestSeedKindStandards_Idempotent 验证 4 条全局形态编码规范被幂等写入 coding_standard。
func TestSeedKindStandards_Idempotent(t *testing.T) {
	db, _ := sqlx.Connect("sqlite", ":memory:")
	db.Exec(`CREATE TABLE coding_standard (id TEXT PRIMARY KEY, project_space_id TEXT, name TEXT, category TEXT,
		content TEXT, priority INTEGER, enabled BOOLEAN, scope TEXT, module TEXT, created_at DATETIME, updated_at DATETIME)`)
	if err := SeedKindStandards(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Get(&n, `SELECT COUNT(*) FROM coding_standard WHERE project_space_id IS NULL AND name IN ('desktop-packaging','mobile-android','cli-cross-platform','service-build')`); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("want 4 kind standards, got %d", n)
	}
	// 再 seed 一次不报错（幂等），且不重复插入
	if err := SeedKindStandards(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if err := db.Get(&n, `SELECT COUNT(*) FROM coding_standard WHERE project_space_id IS NULL AND name IN ('desktop-packaging','mobile-android','cli-cross-platform','service-build')`); err != nil {
		t.Fatal(err)
	}
	if n != 4 {
		t.Fatalf("want 4 kind standards after re-seed, got %d", n)
	}
}
