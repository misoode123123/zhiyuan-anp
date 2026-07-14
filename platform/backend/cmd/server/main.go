// Package main 是智源 ANP 平台后端入口。
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/codetask"
	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/conversation"
	"zhiyuan-anp/platform/backend/internal/db"
	"zhiyuan-anp/platform/backend/internal/dev"
	"zhiyuan-anp/platform/backend/internal/docs"
	"zhiyuan-anp/platform/backend/internal/qa"
	"zhiyuan-anp/platform/backend/internal/release"
	zhlog "zhiyuan-anp/platform/backend/internal/log"
	"zhiyuan-anp/platform/backend/internal/requirement"
	"zhiyuan-anp/platform/backend/internal/rule"
	"zhiyuan-anp/platform/backend/internal/server"
	"zhiyuan-anp/platform/backend/internal/standard"
	"zhiyuan-anp/platform/backend/internal/workspace"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger := zhlog.New(cfg.LogLevel)
	defer logger.Sync()

	// 数据层（M0: SQLite）
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("open db", zap.Error(err))
	}
	defer database.Close()
	if err := db.Migrate(context.Background(), database); err != nil {
		logger.Fatal("migrate", zap.Error(err))
	}
	logger.Info("db ready", zap.String("url", cfg.DatabaseURL))

	// 业务模块：workspace（项目空间，多租户基础）
	wsRepo := workspace.NewRepository(database)
	wsSvc := workspace.NewService(wsRepo)
	wsHandler := workspace.NewHandler(wsSvc, validator.New())

	// 系统配置仓库（业务配置入库，从「系统配置」页管理；首次从 env seed）
	store := config.NewStore(database)
	if err := store.SeedIfEmpty(context.Background(), map[string][2]string{
		"zhipuai_api_key":        {cfg.ZhipuAPIKey, "model"},
		"default_chat_model":     {"zhipu/glm-4-flash", "model"},
		"default_code_model":     {"zai-coding/glm-5.1", "model"},
		"opencode_config_path":   {cfg.OpencodeConfigPath, "opencode"},
		"opencode_git_bash_path": {cfg.GitBashPath, "opencode"},
	}); err != nil {
		logger.Fatal("seed system_config", zap.Error(err))
	}
	logger.Info("system_config ready", zap.Int("items", len(store.All())))

	// 算力用量记录（提前定义，供各业务模块记录用量）
	computeStore := compute.NewStore(database)

	// 规则治理中心（RaC 心脏）
	ruleStore := rule.NewStore(database)
	if err := ruleStore.SeedDemoRules(context.Background()); err != nil {
		logger.Fatal("seed demo rules", zap.Error(err))
	}
	ruleEngine := rule.NewEngine(ruleStore)

	// 变更闸门（🚪G3 代码审批流）
	changeStore := change.NewStore(database)

	// 异步编码任务（解决同步阻塞）
	codeTaskStore := codetask.NewStore(database)

	// 编码规范（全局+项目级，注入式生成指导）
	standardStore := standard.NewStore(database)
	if err := db.SeedDemoStandards(context.Background(), database); err != nil {
		logger.Fatal("seed coding_standard", zap.Error(err))
	}

	// 业务模块：dev（研发工作台，异步编码：规则校验→后台 opencode→登记变更）
	devAgent := dev.NewCodingAgent(store, ruleEngine, codeTaskStore, changeStore, standardStore)
	devHandler := dev.NewHandler(devAgent)

	// 业务模块：requirement（需求工作台，AI 生成规格入库）
	reqRepo := requirement.NewRepository(database)
	reqSvc := requirement.NewService(reqRepo, cfg.AgentRuntimeURL, devAgent, computeStore)
	reqHandler := requirement.NewHandler(reqSvc)

	// 对话式需求梳理（AI agent 多轮对话梳理需求 → 生成 requirement）
	convSvc := conversation.NewService(conversation.NewStore(database), reqRepo, cfg.AgentRuntimeURL)
	convHandler := conversation.NewHandler(convSvc)

	// 测试中心
	qaStore := qa.NewStore(database)
	qaSvc := qa.NewService(qaStore, cfg.AgentRuntimeURL)
	qaHandler := qa.NewHandler(qaSvc, reqRepo)

	// 发布中心
	releaseStore := release.NewStore(database)
	releaseHandler := release.NewHandler(releaseStore, changeStore, reqRepo)

	// 算力资源中心（用量看板，computeStore 已在前面定义）
	computeHandler := compute.NewHandler(computeStore)

	// 系统配置 + 规则治理 + 变更闸门 + 权限
	configHandler := config.NewHandler(store)
	ruleHandler := rule.NewHandler(ruleStore, validator.New())
	standardHandler := standard.NewHandler(standardStore, validator.New())
	docsHandler := docs.NewHandler(docs.NewService(store))
	changeHandler := change.NewHandler(changeStore)
	authStore := auth.NewStore(database)
	authHandler := auth.NewHandler(authStore)

	srv := server.New(cfg, logger)
	v1 := srv.Group("/api/v1")
	wsHandler.Register(v1)
	devHandler.Register(v1)
	reqHandler.Register(v1)
	convHandler.Register(v1)
	configHandler.Register(v1)
	ruleHandler.Register(v1)
	standardHandler.Register(v1)
	docsHandler.Register(v1)
	changeHandler.Register(v1)
	qaHandler.Register(v1)
	releaseHandler.Register(v1)
	computeHandler.Register(v1)
	authHandler.Register(v1)

	logger.Info("opencode engine ready",
		zap.String("config", cfg.OpencodeConfigPath),
		zap.Bool("zhipu_key_set", cfg.ZhipuAPIKey != ""))

	httpSrv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("backend listening",
			zap.String("addr", cfg.HTTPAddr),
			zap.String("env", cfg.Env))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatal("server error", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(ctx); err != nil {
		logger.Error("shutdown error", zap.Error(err))
	}
}
