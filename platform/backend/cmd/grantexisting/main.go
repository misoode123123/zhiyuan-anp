// Package main —— grantexisting 一次性补丁：为既有应用库（GrantAll 修复前供给的）
// 补 GRANT USAGE,CREATE ON SCHEMA public + GRANT ALL TABLES + ALTER DEFAULT PRIVILEGES。
//
// 背景：pgadmin.GrantAll 在 commit 01f5738 之前只 GRANT DATABASE，没补 PG 15+ 的
// schema public CREATE 权限 → 修复前供给的既有应用库内 role 建表失败
// （permission denied for schema public）。新供给的库已 OK，本程序只补旧库。
//
// 用法（在 .28 上，已部署最新 backend 镜像后）：
//
//	cd /opt/anp/platform/backend && \
//	  DATABASE_URL=postgres://anp:anp_dev_pwd@10.10.0.28:5432/anp_dev?sslmode=disable \
//	  go run ./cmd/grantexisting
//
// 或（容器内，源码已挂载）：
//
//	docker exec anp-backend sh -c 'cd /app/platform/backend && ./grantexisting'
//
// 流程：连平台库 anp_dev → 列所有 status<>deleted 的 appdeploy_database →
// 对每条 (instance.admin_url_ref, db_name, db_role) 调 pgAdmin.GrantAll（已修复版）。
// 单库失败不中断（记日志继续下一个），便于部分库 PG 实例下线时其它仍能补。
//
// 幂等：GrantAll 内部用 GRANT/ALTER DEFAULT PRIVILEGES，重复执行无副作用
// （PG 对已授权再 GRANT 仅刷新，不报错）。
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/db"
	zhlog "zhiyuan-anp/platform/backend/internal/log"
	"zhiyuan-anp/platform/backend/internal/pgsupply"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}
	logger := zhlog.New(zhlog.Config{Level: cfg.LogLevel, Format: "console"})
	defer func() { _ = logger.Sync() }()

	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("open db", zap.Error(err))
	}
	defer database.Close()

	store := pgsupply.NewStore(database)
	pgAdmin := pgsupply.NewPGAdmin()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	list, err := store.ListAppDBs(ctx, "")
	if err != nil {
		logger.Fatal("list appdbs", zap.Error(err))
	}
	logger.Info("grantexisting: 扫到应用库", zap.Int("count", len(list)))

	ok, skipped, failed := 0, 0, 0
	for _, ad := range list {
		ins, err := store.GetInstance(ctx, ad.PGInstanceID)
		if err != nil || ins == nil {
			logger.Warn("跳过：实例不存在",
				zap.String("app_id", ad.AppID),
				zap.String("instance_id", ad.PGInstanceID), zap.Error(err))
			skipped++
			continue
		}
		if ins.AdminURLRef == "" {
			logger.Warn("跳过：实例无 admin_url_ref",
				zap.String("app_id", ad.AppID),
				zap.String("instance_id", ins.ID))
			skipped++
			continue
		}
		if err := pgAdmin.GrantAll(ctx, ins.AdminURLRef, ad.DBName, ad.DBRole); err != nil {
			logger.Error("授权失败",
				zap.String("app_id", ad.AppID),
				zap.String("db_name", ad.DBName),
				zap.String("role", ad.DBRole),
				zap.Error(err))
			failed++
			continue
		}
		logger.Info("授权补齐",
			zap.String("app_id", ad.AppID),
			zap.String("db_name", ad.DBName),
			zap.String("role", ad.DBRole))
		ok++
	}

	logger.Info("grantexisting 完成",
		zap.Int("total", len(list)),
		zap.Int("ok", ok),
		zap.Int("skipped", skipped),
		zap.Int("failed", failed))

	if failed > 0 {
		os.Exit(1) // 有失败让脚本感知（CI/部署可重试）
	}
}
