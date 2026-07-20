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
	"zhiyuan-anp/platform/backend/internal/codews"
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

	// 数据层（M0: SQLite）
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
	// 把平台维护的 opencode.json（本地 platform/opencode.json；容器内由 compose 挂载到 /app/opencode.json）
	// 复制到该默认路径，否则交互编码工作台(serve)加载不到 provider(zai-coding)，web UI 卡在无 provider。
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
	authStore := auth.NewStore(database)

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

	// 应用部署引擎 Store（提前构造，供需求派发按应用解析仓库路径）
	appDeployStore := appdeploy.NewStore(database)

	// 业务模块：requirement（需求工作台，AI 生成规格入库）
	reqRepo := requirement.NewRepository(database)
	reqSvc := requirement.NewService(reqRepo, cfg.AgentRuntimeURL, devAgent, computeStore, appDeployStore)
	reqHandler := requirement.NewHandler(reqSvc, changeStore, authStore)

	// 对话式需求梳理（AI agent 多轮对话梳理需求 → 生成 requirement）
	convSvc := conversation.NewService(conversation.NewStore(database), reqRepo, cfg.AgentRuntimeURL)
	convHandler := conversation.NewHandler(convSvc)

	// 测试中心（AI 生成用例 + 对着已部署应用 URL 自动验收）
	qaStore := qa.NewStore(database)
	qaSvc := qa.NewService(qaStore, cfg.AgentRuntimeURL)
	qaHandler := qa.NewHandler(qaSvc, reqRepo, appDeployStore)

	// 应用部署引擎（板块06 M2）：产出应用 build→docker run→暴露 URL（appDeployStore 已提前构造）
	// + 交互编码工作台（opencode serve 官方 web UI，codews.Manager 管理进程）
	appDeployHandler := appdeploy.NewHandler(appDeployStore, appdeploy.NewDeployer(cfg.AppDeployHost), codews.NewManager(cfg.AppDeployHost), changeStore, store, reqRepo)

	// 发布中心（发布后可自动触发应用部署）
	releaseStore := release.NewStore(database)
	// 发布测试门禁开关：首次启动确保 key 存在（默认关，不破坏现有流程）；用户可在系统配置页改。
	if store.Get("release_require_passed_test", "\x00missing\x00") == "\x00missing\x00" {
		_ = store.Set(context.Background(), "release_require_passed_test", "false", "release", "发布门禁：true 时要求来源需求至少 1 条 passed 测试用例才允许发布")
	}
	releaseHandler := release.NewHandler(releaseStore, changeStore, reqRepo, appDeployHandler, store, qaStore)

	// 算力资源中心（用量看板，computeStore 已在前面定义）
	computeHandler := compute.NewHandler(computeStore)

	// 系统配置 + 规则治理 + 变更闸门 + 权限
	configHandler := config.NewHandler(store)
	ruleHandler := rule.NewHandler(ruleStore, validator.New())
	standardHandler := standard.NewHandler(standardStore, validator.New())
	docsHandler := docs.NewHandler(docs.NewService(store))
	changeHandler := change.NewHandler(changeStore)
	// 真实登录：首次为 admin 设默认密码（已有不覆盖；建议登录后改密）。
	_ = authStore.EnsurePassword(context.Background(), "admin", "admin123")
	authHandler := auth.NewHandler(authStore)

	// 运维中心（板块07）：健康检查 + 看板聚合 + 告警 + SOP 预案
	opsStore := ops.NewStore(database)
	if err := db.SeedDemoSOPs(context.Background(), database); err != nil {
		logger.Fatal("seed ops_sop", zap.Error(err))
	}
	opsHandler := ops.NewHandler(opsStore, cfg.AgentRuntimeURL, validator.New())

	// 安全与合规中心（板块05）：Go 原生扫描（密钥/SAST/提示注入）+ 安全门 + 数据分级 + 审计
	securityStore := security.NewStore(database)
	securityHandler := security.NewHandler(securityStore)

	// AI 能力市场（板块09）：技能注册 + APIKey + 调用网关 + 用量 + 领域 Agent
	capabilityStore := capability.NewStore(database)
	if err := db.SeedDemoSkills(context.Background(), database); err != nil {
		logger.Fatal("seed capability_skill", zap.Error(err))
	}
	capabilityGateway := capability.NewGateway(capabilityStore, cfg.AgentRuntimeURL, "")
	capabilityHandler := capability.NewHandler(capabilityStore, capabilityGateway)

	// 业务模块：attendance（考勤管理：员工提交休息/加班/请假，转直接上级审批）
	attendanceStore := attendance.NewStore(database)
	attendanceSvc := attendance.NewService(attendanceStore)
	attendanceHandler := attendance.NewHandler(attendanceSvc)

	srv := server.New(cfg, logger)
	v1 := srv.Group("/api/v1")
	// 认证：优先 Authorization Bearer token（真实登录），无则回退 X-User 头（兼容调试/旧前端）。
	v1.Use(auth.AuthUser(authStore))
	// 集中式 RBAC：按路由模板强制写/危险操作鉴权（authStore 已构造）。
	v1.Use(auth.AutoRequire(authStore))
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
	opsHandler.Register(v1)
	securityHandler.Register(v1)
	capabilityHandler.Register(v1)
	attendanceHandler.Register(v1)
	appDeployHandler.Register(v1)

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
