package pgsupply

import (
	"context"
	"fmt"
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
	db, err := connect(ctx, adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx,
		fmt.Sprintf(`GRANT ALL ON DATABASE "%s" TO "%s"`, dbName, role)); err != nil {
		return fmt.Errorf("grant %s to %s: %w", dbName, role, err)
	}
	return nil
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
