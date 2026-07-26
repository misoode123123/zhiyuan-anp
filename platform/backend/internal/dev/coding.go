package dev

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/notif"
	"zhiyuan-anp/platform/backend/internal/codetask"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/rule"
	"zhiyuan-anp/platform/backend/internal/standard"
)

// ErrActiveTaskConflict 派发幂等冲突：同一来源（需求）已有进行中的编码任务，拒绝重复创建。
// handler 据此返回 409，避免"派发编码"连点生成多个并发 opencode 抢同一仓库导致卡死。
var ErrActiveTaskConflict = errors.New("该需求已有进行中的编码任务，请勿重复派发")

// CodingAgent 封装 opencode，支持同步 Run 与异步 Submit。
type CodingAgent struct {
	store     *config.Store
	engine    *rule.Engine
	tasks     *codetask.Store
	changes   *change.Store
	standards *standard.Store // 编码规范（全局+项目级）注入
}

// NewCodingAgent 构造。
func NewCodingAgent(store *config.Store, engine *rule.Engine, tasks *codetask.Store, changes *change.Store, standards *standard.Store) *CodingAgent {
	return &CodingAgent{store: store, engine: engine, tasks: tasks, changes: changes, standards: standards}
}

// Submit 异步提交编码任务：规则校验 → 创建 running 任务 → goroutine 跑 opencode → 完成登记变更。
// HTTP 立即返回 task_id，不阻塞。
func (a *CodingAgent) Submit(ctx context.Context, psID, kind, sourceID, repoDir, prompt, model string) (*codetask.Task, error) {
	if err := a.checkRules(ctx, prompt); err != nil {
		return nil, err
	}
	// 派发幂等（快速路径）：同一来源已有 running 任务则拒绝，避免连点生成多个并发 opencode。
	if sourceID != "" {
		if n, err := a.tasks.CountActiveBySource(ctx, sourceID); err == nil && n > 0 {
			return nil, ErrActiveTaskConflict
		}
	}
	t := &codetask.Task{
		ID:             "ctask_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:19],
		ProjectSpaceID: psID, Kind: kind, SourceID: sourceID,
		RepoDir: repoDir, Prompt: prompt, Model: model,
	}
	if err := a.tasks.Create(ctx, t); err != nil {
		// 并发兜底：极端并发下两条请求同时通过上面的计数判重，DB 部分唯一索引（迁移 000017）会拦下
		// 第二条 Create；此时再确认一次存在 running，转为友好冲突错误（让 handler 返回 409 而非 500）。
		if sourceID != "" {
			if n, qerr := a.tasks.CountActiveBySource(ctx, sourceID); qerr == nil && n > 0 {
				return nil, ErrActiveTaskConflict
			}
		}
		return nil, err
	}
	go a.run(t.ID)
	return t, nil
}

// checkRules 规则校验，block 违反返回错误。
func (a *CodingAgent) checkRules(ctx context.Context, prompt string) error {
	if a.engine == nil {
		return nil
	}
	vs, err := a.engine.Check(ctx, "dev", "prompt", prompt)
	if err != nil || !rule.HasBlock(vs) {
		return nil
	}
	var names []string
	for _, v := range vs {
		if v.Rule != nil && v.Rule.Action == "block" {
			names = append(names, v.Rule.Name)
		}
	}
	return fmt.Errorf("编码被规则阻断（🚪需人工评估）：%s", strings.Join(names, "、"))
}

// run goroutine：跑 opencode → 更新任务 → 登记变更（脱离 HTTP context）。
func (a *CodingAgent) run(taskID string) {
	ctx := context.Background()
	// panic 兜底：goroutine 内任何 panic 都回写失败，避免任务永卡 running、output 为空（历史卡 running 根因之一）。
	defer func() {
		if r := recover(); r != nil {
			_ = a.tasks.MarkFailed(ctx, taskID, fmt.Sprintf("[panic] 编码任务异常崩溃: %v", r))
		}
	}()
	t, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		// 任务记录读取失败也要回写失败，否则任务无声卡 running（output 为空，前端永远转圈）。
		_ = a.tasks.MarkFailed(ctx, taskID, "[系统]任务记录读取失败: "+err.Error())
		return
	}
	prompt := t.Prompt
	// 规范单一载体：编码前刷新应用 AGENTS.md。dev 调 opencode 编码，opencode 自动读 repo 的
	// AGENTS.md，所以这里只刷新（保证最新），不把 AGENTS.md 拼进 prompt（拼了和 opencode 自读重复）。
	if a.standards != nil && t.RepoDir != "" {
		_ = a.standards.RefreshAgentsMD(ctx, t.RepoDir, t.ProjectSpaceID, "")
	}
	out, err := a.opencodeRun(ctx, t.RepoDir, prompt, t.Model)
	if err != nil {
		_ = a.tasks.MarkFailed(ctx, taskID, out+"\n"+err.Error())
		notif.Emit("", t.ProjectSpaceID, "code_failed", "编码失败", "任务 "+taskID+" 失败", "/dev")
		return
	}
	// 编码产出落定为仓库版本（应用托管仓库由此有 commit 历史 = 版本）
	a.gitCommit(ctx, t.RepoDir, "ANP编码: "+t.SourceID)
	_ = a.tasks.MarkCompleted(ctx, taskID, out)
	notif.Emit("", t.ProjectSpaceID, "code_done", "编码完成", "任务 "+taskID+" 已完成，待审批", "/approvals")
	if a.changes != nil {
		chg := &change.ChangeRequest{
			ProjectSpaceID: t.ProjectSpaceID, Kind: t.Kind, SourceID: t.SourceID,
			RepoDir: t.RepoDir, Prompt: t.Prompt, Model: t.Model, Output: out,
		}
		if err := a.changes.Create(ctx, chg); err == nil {
			_ = a.tasks.SetChangeID(ctx, taskID, chg.ID)
		}
	}
}

// gitCommit 把编码产出提交到仓库（best-effort：仓库未 git init 或无变更则忽略）。
func (a *CodingAgent) gitCommit(ctx context.Context, repoDir, msg string) {
	if repoDir == "" {
		return
	}
	run := func(args ...string) {
		cmd := exec.CommandContext(ctx, "git", args...)
		cmd.Dir = repoDir
		_ = cmd.Run()
	}
	run("add", "-A")
	run("commit", "-q", "-m", msg)
}

// opencodeRun 同步执行 opencode（任务内部使用，配置从 system_config 读）。
func (a *CodingAgent) opencodeRun(ctx context.Context, repoDir, prompt, model string) (string, error) {
	zhipuKey := a.store.Get("zhipuai_api_key", "")
	if zhipuKey == "" {
		return "", fmt.Errorf("system_config 缺少 zhipuai_api_key")
	}
	configPath := a.store.Get("opencode_config_path", "../opencode.json")
	gitBash := a.store.Get("opencode_git_bash_path", "")
	if model == "" {
		model = a.store.Get("default_code_model", "zai-coding/glm-5.1")
	}
	absRepo, err := filepath.Abs(repoDir)
	if err != nil {
		return "", err
	}
	// Windows 下中文路径会让 opencode 的 argv 解析失败，转成 ASCII junction 再传入。
	workDir, cleanup, err := opencodeDir(absRepo)
	if err != nil {
		return "", err
	}
	defer cleanup()
	absConfig, _ := filepath.Abs(configPath)

	cctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "opencode", "run", prompt, "-m", model, "--auto", "--dir", workDir)
	cmd.Dir = workDir
	env := append(os.Environ(), "ZHIPUAI_API_KEY="+zhipuKey, "OPENCODE_CONFIG="+absConfig)
	if gitBash != "" {
		env = append(env, "OPENCODE_GIT_BASH_PATH="+gitBash)
	}
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("opencode run 失败: %w", err)
	}
	return out.String(), nil
}
