package db

import (
	"context"
	"testing"
	"testing/fstest"

	"github.com/jmoiron/sqlx"
	_ "modernc.org/sqlite"
)

// newTestDB 内存 SQLite（迁移核心逻辑与方言无关，SQLite 足以验证版本记录/事务/down）。
func newTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	database, err := sqlx.Connect("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

// testMigrations 两版本迁移（含 up/down），SQLite 兼容。
func testMigrations() fstest.MapFS {
	return fstest.MapFS{
		"0001.up.sql":   {Data: []byte("CREATE TABLE a (id INTEGER)")},
		"0001.down.sql": {Data: []byte("DROP TABLE a")},
		"0002.up.sql":   {Data: []byte("CREATE TABLE b (id INTEGER)")},
		"0002.down.sql": {Data: []byte("DROP TABLE b")},
	}
}

func TestMigrateUp_AppliesAllAndRecords(t *testing.T) {
	database := newTestDB(t)
	if err := migrateUp(context.Background(), database, testMigrations()); err != nil {
		t.Fatalf("migrateUp: %v", err)
	}
	var versions []string
	if err := database.Select(&versions, `SELECT version FROM schema_migrations ORDER BY version`); err != nil {
		t.Fatalf("select versions: %v", err)
	}
	if len(versions) != 2 || versions[0] != "0001" || versions[1] != "0002" {
		t.Fatalf("期望两版本 0001/0002，得到 %v", versions)
	}
	var n int
	for _, tbl := range []string{"a", "b"} {
		if err := database.Get(&n, "SELECT COUNT(*) FROM "+tbl); err != nil {
			t.Fatalf("表 %s 应已建： %v", tbl, err)
		}
	}
}

func TestMigrateUp_Idempotent(t *testing.T) {
	database := newTestDB(t)
	ctx := context.Background()
	if err := migrateUp(ctx, database, testMigrations()); err != nil {
		t.Fatalf("first migrateUp: %v", err)
	}
	if err := migrateUp(ctx, database, testMigrations()); err != nil {
		t.Fatalf("second migrateUp: %v", err)
	}
	var versions []string
	_ = database.Select(&versions, `SELECT version FROM schema_migrations`)
	if len(versions) != 2 {
		t.Fatalf("幂等：重复 up 后仍应 2 版本，得到 %d", len(versions))
	}
}

func TestMigrateDown_RollsBackLatest(t *testing.T) {
	database := newTestDB(t)
	ctx := context.Background()
	if err := migrateUp(ctx, database, testMigrations()); err != nil {
		t.Fatalf("migrateUp: %v", err)
	}
	if err := migrateDown(ctx, database, testMigrations()); err != nil {
		t.Fatalf("migrateDown: %v", err)
	}
	var versions []string
	_ = database.Select(&versions, `SELECT version FROM schema_migrations ORDER BY version`)
	if len(versions) != 1 || versions[0] != "0001" {
		t.Fatalf("down 后应剩 0001，得到 %v", versions)
	}
	var n int
	if err := database.Get(&n, "SELECT COUNT(*) FROM b"); err == nil {
		t.Fatal("表 b 应已 drop")
	}
}

func TestMigrateDown_NoAppliedIsNoop(t *testing.T) {
	database := newTestDB(t)
	// 空库直接 down，不应报错。
	if err := migrateDown(context.Background(), database, testMigrations()); err != nil {
		t.Fatalf("空库 migrateDown 应 noop，得到 %v", err)
	}
}

func TestMigrateUp_TransactionalOnFailure(t *testing.T) {
	database := newTestDB(t)
	// 0002 up 故意非法 SQL，应整事务回滚：0001 应用、0002 不记录。
	badFS := fstest.MapFS{
		"0001.up.sql":   {Data: []byte("CREATE TABLE a (id INTEGER)")},
		"0001.down.sql": {Data: []byte("DROP TABLE a")},
		"0002.up.sql":   {Data: []byte("THIS IS NOT VALID SQL !!!")},
		"0002.down.sql": {Data: []byte("SELECT 1")},
	}
	if err := migrateUp(context.Background(), database, badFS); err == nil {
		t.Fatal("非法 SQL 应报错")
	}
	var versions []string
	_ = database.Select(&versions, `SELECT version FROM schema_migrations`)
	if len(versions) != 1 || versions[0] != "0001" {
		t.Fatalf("失败迁移应事务回滚，仅 0001 应用，得到 %v", versions)
	}
}
