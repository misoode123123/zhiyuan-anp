package db

import (
	"context"
	"fmt"
	"os"
	"testing"
	"testing/fstest"

	"github.com/jmoiron/sqlx"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// newTestDB 连 anp_test PG，但每测试独占一个 schema，隔离 testutil 预跑的平台迁移
// （public schema 中 schema_migrations 已记录 000001~000004，与本测试的合成 0001/0002 冲突）。
// 替代 sqlite :memory:（迁移核心逻辑与方言无关，但 PG 模式下需避免污染共享库）。
func newTestDB(t *testing.T) *sqlx.DB {
	t.Helper()
	url := os.Getenv("ANP_TEST_DATABASE_URL")
	if url == "" {
		url = "postgres://anp:anp_dev_pwd@10.10.0.28:5432/anp_test?sslmode=disable"
	}
	database, err := sqlx.Connect("pgx", url)
	if err != nil {
		t.Fatalf("connect pg: %v", err)
	}
	// 独占 schema（每次测试一个）；search_path 限定到本 schema，建表/查 schema_migrations 都在此。
	schema := fmt.Sprintf("test_migrate_%s", sanitizeSchema(t.Name()))
	database.MustExec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
	database.MustExec(fmt.Sprintf("CREATE SCHEMA %s", schema))
	database.MustExec(fmt.Sprintf("SET search_path TO %s", schema))
	t.Cleanup(func() {
		_, _ = database.Exec(fmt.Sprintf("DROP SCHEMA IF EXISTS %s CASCADE", schema))
		_ = database.Close()
	})
	return database
}

// sanitizeSchema 把测试名中的非 [a-z0-9_] 字符替换为 _，作为 PG schema 名。
// PG schema 名不允许 "/" 等字符（t.Name() 在子测试中含 "/"）。
func sanitizeSchema(name string) string {
	out := make([]byte, 0, len(name))
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			out = append(out, c)
		} else {
			out = append(out, '_')
		}
	}
	return string(out)
}

// testMigrations 两版本迁移（含 up/down），PG/SQLite 兼容（用 INTEGER 防 PG 严格类型问题）。
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
