// Package main 是智源 ANP 平台后端入口。
//
// @title           智源 ANP 平台 API
// @version         1.0
// @description     企业 AI 原生研发平台后端（AI 驱动 需求→研发→测试→审批→发布 全流程）
// @BasePath        /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/zap"

	"zhiyuan-anp/platform/backend/internal/appdeploy"
	"zhiyuan-anp/platform/backend/internal/attendance"
	"zhiyuan-anp/platform/backend/internal/auth"
	"zhiyuan-anp/platform/backend/internal/capability"
	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/codetask"
	"zhiyuan-anp/platform/backend/internal/compute"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/conversation"
	"zhiyuan-anp/platform/backend/internal/db"
	"zhiyuan-anp/platform/backend/internal/dev"
	"zhiyuan-anp/platform/backend/internal/docs"
	zhlog "zhiyuan-anp/platform/backend/internal/log"
	"zhiyuan-anp/platform/backend/internal/ops"
	"zhiyuan-anp/platform/backend/internal/qa"
	"zhiyuan-anp/platform/backend/internal/release"
	"zhiyuan-anp/platform/backend/internal/requirement"
	"zhiyuan-anp/platform/backend/internal/rule"
	"zhiyuan-anp/platform/backend/internal/security"
	"zhiyuan-anp/platform/backend/internal/server"
	"zhiyuan-anp/platform/backend/internal/standard"
	"zhiyuan-anp/platform/backend/internal/workspace"
)

func main() {
	// 子命令：migrate-up / migrate-down（仅迁移不启 server，供 make 调用）。
	if len(os.Args) > 1 && (os.Args[1] == "migrate-up" || os.Args[1] == "migrate-down") {
		runMigrateCmd(os.Args[1])
		return
	}

	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger := zhlog.New(cfg.LogLevel)
	defer logger.Sync()

	// 数据层（强制 PG，禁 SQLite——见 config.Load 校验）
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("open db", zap.Error(err))
	}
	defer database.Close()
	if err := db.Migrate(context.Background(), database); err != nil {
		logger.Fatal("migrate", zap.Error(err))
	}
	if err := db.SeedBootstrapMembers(context.Background(), database); err != nil {
		logger.Fatal("seed bootstrap members", zap.Error(err))
	}
	if err := db.SeedUsers(context.Background(), database); err != nil {
		logger.Fatal("seed users", zap.Error(err))
	}
	logger.Info("db ready", zap.String("url", cfg.DatabaseURL))

	// opencode serve 只认默认路径 $HOME/.config/opencode/opencode.json，不读 OPENCODE_CONFIG env。
	// 把平台维护的 opencode.json 复制到默认路径，否则交互编码工作台加载不到 provider。
	ocSrc := cfg.OpencodeConfigPath
	if ocSrc == "" {
		ocSrc = "/app/opencode.json"
	}
	ocDest := filepath.Join(os.Getenv("HOME"), ".config", "opencode", "opencode.json")
	if data, rerr := os.ReadFile(ocSrc); rerr != nil {
		logger.Warn("opencode 配置源读取失败，交互编码工作台将无 provider",
			zap.String("path", ocSrc), zap.Error(rerr))
	} else if merr := os.MkdirAll(filepath.Dir(ocDest), 0o755); merr != nil {
		logger.Warn("opencode 配置目录创建失败", zap.String("dir", filepath.Dir(ocDest)), zap.Error(merr))
	} else if werr := os.WriteFile(ocDest, data, 0o644); werr != nil {
		logger.Warn("opencode 配置写入默认路径失败，工作台将无 provider",
			zap.String("dest", ocDest), zap.Error(werr))
	} else {
		logger.Info("opencode 配置已安装到默认路径",
			zap.String("src", ocSrc), zap.String("dest", ocDest))
	}

	// ---- 共享 store + seed（main 构造，供各模块 Register 使用）----
	wsRepo := workspace.NewRepository(database)
	wsSvc := workspace.NewService(wsRepo)
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

	computeStore := compute.NewStore(database)
	ruleStore := rule.NewStore(database)
	if err := ruleStore.SeedDemoRules(context.Background()); err != nil {
		logger.Fatal("seed demo rules", zap.Error(err))
	}
	ruleEngine := rule.NewEngine(ruleStore)
	changeStore := change.NewStore(database)
	authStore := auth.NewStore(database)
	codeTaskStore := codetask.NewStore(database)
	standardStore := standard.NewStore(database)
	if err := db.SeedDemoStandards(context.Background(), database); err != nil {
		logger.Fatal("seed coding_standard", zap.Error(err))
	}
	appDeployStore := appdeploy.NewStore(database)
	qaStore := qa.NewStore(database)
	opsStore := ops.NewStore(database)
	if err := db.SeedDemoSOPs(context.Background(), database); err != nil {
		logger.Fatal("seed ops_sop", zap.Error(err))
	}
	securityStore := security.NewStore(database)
	capabilityStore := capability.NewStore(database)
	if err := db.SeedDemoSkills(context.Background(), database); err != nil {
		logger.Fatal("seed capability_skill", zap.Error(err))
	}
	capabilityGateway := capability.NewGateway(capabilityStore, cfg.AgentRuntimeURL, "")
	attendanceSvc := attendance.NewService(attendance.NewStore(database))

	// ---- 跨模块枢纽（main 构造，多模块共用：reqRepo/devAgent/appDeployHandler）----
	reqRepo := requirement.NewRepository(database)
	devAgent := dev.NewCodingAgent(store, ruleEngine, codeTaskStore, changeStore, standardStore)
	_ = authStore.EnsurePassword(context.Background(), "admin", "admin123")
	if store.Get("release_require_passed_test", "\x00missing\x00") == "\x00missing\x00" {
		_ = store.Set(context.Background(), "release_require_passed_test", "false", "release", "发布门禁：true 时要求来源需求至少 1 条 passed 测试用例才允许发布")
	}
	v := validator.New()

	srv := server.New(cfg, logger)
	v1 := srv.Group("/api/v1")
	// 认证：Authorization Bearer token（真实登录，撤 X-User 模拟回退）。
	v1.Use(auth.AuthUser(authStore))
	// 集中式 RBAC：按路由模板强制写/危险操作鉴权。
	v1.Use(auth.AutoRequire(authStore))

	// ---- 路由装配：各模块自包含 Register（main 不再 new 各 handler，8 人改模块不碰 main）----
	appDeployHandler := appdeploy.Register(v1, appDeployStore, cfg.AppDeployHost, changeStore, store, reqRepo)
	workspace.Register(v1, wsSvc, v)
	config.Register(v1, store)
	rule.Register(v1, ruleStore, v)
	standard.Register(v1, standardStore, v)
	change.Register(v1, changeStore)
	auth.Register(v1, authStore)
	compute.Register(v1, computeStore)
	security.Register(v1, securityStore)
	attendance.Register(v1, attendanceSvc)
	capability.Register(v1, capabilityStore, capabilityGateway)
	ops.Register(v1, opsStore, cfg.AgentRuntimeURL, v)
	docs.Register(v1, store)
	dev.Register(v1, devAgent)
	requirement.Register(v1, reqRepo, cfg.AgentRuntimeURL, devAgent, computeStore, appDeployStore, changeStore, authStore)
	conversation.Register(v1, database, reqRepo, cfg.AgentRuntimeURL)
	qa.Register(v1, database, cfg.AgentRuntimeURL, reqRepo, appDeployStore)
	release.Register(v1, database, changeStore, reqRepo, appDeployHandler, store, qaStore)

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

// runMigrateCmd 处理 migrate-up / migrate-down 子命令：只跑迁移（不启 server）。
func runMigrateCmd(cmd string) {
	cfg, err := config.Load()
	if err != nil {
		panic(err)
	}
	logger := zhlog.New(cfg.LogLevel)
	defer logger.Sync()
	database, err := db.Open(cfg.DatabaseURL)
	if err != nil {
		logger.Fatal("open db", zap.Error(err))
	}
	defer database.Close()
	ctx := context.Background()
	switch cmd {
	case "migrate-up":
		if err := db.Migrate(ctx, database); err != nil {
			logger.Fatal("migrate-up", zap.Error(err))
		}
		logger.Info("migrate-up done")
	case "migrate-down":
		if err := db.MigrateDown(ctx, database); err != nil {
			logger.Fatal("migrate-down", zap.Error(err))
		}
		logger.Info("migrate-down done（回滚最新一步）")
	}
}
