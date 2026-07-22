package pgsupply

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	_ "github.com/jackc/pgx/v5/stdlib" // 驱动名 "pgx"
	"github.com/jmoiron/sqlx"
)

// PGAdmin 在目标 PG 实例上执行管理 DDL（连 superuser 的 adminURL）。
// 抽成接口便于单测用 fake；生产用 pgAdminClient。
type PGAdmin interface {
	CreateDatabase(ctx context.Context, adminURL, dbName string) error
	CreateRole(ctx context.Context, adminURL, role, password string) error
	GrantAll(ctx context.Context, adminURL, dbName, role string) error
	DropDatabase(ctx context.Context, adminURL, dbName string) error
	DropRole(ctx context.Context, adminURL, role string) error
	Ping(ctx context.Context, adminURL string) error
}

// pgAdminClient 默认实现：每次连 adminURL 执行一条 DDL。
type pgAdminClient struct{}

// NewPGAdmin 构造。
func NewPGAdmin() PGAdmin { return pgAdminClient{} }

// connect 连 adminURL（连 postgres 库）。
func connect(ctx context.Context, adminURL string) (*sqlx.DB, error) {
	db, err := sqlx.ConnectContext(ctx, "pgx", adminURL)
	if err != nil {
		return nil, fmt.Errorf("连 admin PG: %w", err)
	}
	return db, nil
}

func (pgAdminClient) CreateDatabase(ctx context.Context, adminURL, dbName string) error {
	db, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	// 标识符双引号。注意：PG 不支持 CREATE DATABASE IF NOT EXISTS，库已存在会报 error；
	// 本供给流程每应用用新 dbName(app_<hex>)，不会重复，故无需捕获 already-exists。
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`CREATE DATABASE "%s"`, dbName)); err != nil {
		return fmt.Errorf("create database %s: %w", dbName, err)
	}
	return nil
}

func (pgAdminClient) CreateRole(ctx context.Context, adminURL, role, password string) error {
	db, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	escPwd := strings.ReplaceAll(password, "'", "''")
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf(`CREATE ROLE "%s" WITH LOGIN PASSWORD '%s'`, role, escPwd)); err != nil {
		return fmt.Errorf("create role %s: %w", role, err)
	}
	return nil
}

func (pgAdminClient) GrantAll(ctx context.Context, adminURL, dbName, role string) error {
	// 1) 库级权限（连 adminURL=postgres 维护库）：CONNECT/TEMP 等。
	db, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf(`GRANT ALL ON DATABASE "%s" TO "%s"`, dbName, role)); err != nil {
		db.Close()
		return fmt.Errorf("grant db %s to %s: %w", dbName, role, err)
	}
	db.Close()
	// 2) schema 级权限（必须连到目标库，schema public 在应用库里）：
	//    PG 15+ 不再默认给 public schema CREATE，应用 role 不授权则建不了表。
	//    改造为连应用库（adminURL 替换 dbname）执行 GRANT ON SCHEMA + ALL TABLES。
	appDBURL, err := adminURLForDB(adminURL, dbName)
	if err != nil {
		return fmt.Errorf("derive app admin url: %w", err)
	}
	adb, err := connect(ctx, appDBURL)
	if err != nil {
		return fmt.Errorf("connect app db for grant: %w", err)
	}
	defer adb.Close()
	escRole := strings.ReplaceAll(role, `"`, `""`)
	stmts := []string{
		fmt.Sprintf(`GRANT ALL ON SCHEMA public TO "%s"`, escRole),
		fmt.Sprintf(`GRANT ALL ON ALL TABLES IN SCHEMA public TO "%s"`, escRole),
		// 让后续由 admin 跑的迁移建的表也自动授权给应用 role（应用自己建的表本就归自己）
		fmt.Sprintf(`ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO "%s"`, escRole),
	}
	for _, s := range stmts {
		if _, err := adb.ExecContext(ctx, s); err != nil {
			return fmt.Errorf("grant schema public to %s: %w (stmt=%s)", role, err, s)
		}
	}
	return nil
}

// adminURLForDB 把 adminURL（指向 postgres 维护库）改成指向 dbName 的 admin 连接串。
// 解析 URL 替换 path 段；解析失败时回退字符串替换 path 末段。
func adminURLForDB(adminURL, dbName string) (string, error) {
	u, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	return u.String(), nil
}

func (pgAdminClient) DropDatabase(ctx context.Context, adminURL, dbName string) error {
	db, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	// 先断开该库连接（避免 DROP 被 active 连接阻塞）
	escDB := strings.ReplaceAll(dbName, "'", "''")
	_, _ = db.ExecContext(ctx, fmt.Sprintf(
		`SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='%s' AND pid<>pg_backend_pid()`, escDB))
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP DATABASE IF EXISTS "%s"`, dbName)); err != nil {
		return fmt.Errorf("drop database %s: %w", dbName, err)
	}
	return nil
}

func (pgAdminClient) DropRole(ctx context.Context, adminURL, role string) error {
	db, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, fmt.Sprintf(`DROP ROLE IF EXISTS "%s"`, role)); err != nil {
		return fmt.Errorf("drop role %s: %w", role, err)
	}
	return nil
}

// Ping 探活（等 PG ready 用）。
func (pgAdminClient) Ping(ctx context.Context, adminURL string) error {
	db, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return db.PingContext(ctx)
}
