package dev

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"zhiyuan-anp/platform/backend/internal/change"
	"zhiyuan-anp/platform/backend/internal/codetask"
	"zhiyuan-anp/platform/backend/internal/config"
	"zhiyuan-anp/platform/backend/internal/rule"
)

// CodingAgent 封装 opencode，支持同步 Run 与异步 Submit。
type CodingAgent struct {
	store   *config.Store
	engine  *rule.Engine
	tasks   *codetask.Store
	changes *change.Store
}

// NewCodingAgent 构造。
func NewCodingAgent(store *config.Store, engine *rule.Engine, tasks *codetask.Store, changes *change.Store) *CodingAgent {
	return &CodingAgent{store: store, engine: engine, tasks: tasks, changes: changes}
}

// Submit 异步提交编码任务：规则校验 → 创建 running 任务 → goroutine 跑 opencode → 完成登记变更。
// HTTP 立即返回 task_id，不阻塞。
func (a *CodingAgent) Submit(ctx context.Context, psID, kind, sourceID, repoDir, prompt, model string) (*codetask.Task, error) {
	if err := a.checkRules(ctx, prompt); err != nil {
		return nil, err
	}
	t := &codetask.Task{
		ID: "ctask_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:19],
		ProjectSpaceID: psID, Kind: kind, SourceID: sourceID,
		RepoDir: repoDir, Prompt: prompt, Model: model,
	}
	if err := a.tasks.Create(ctx, t); err != nil {
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
	t, err := a.tasks.Get(ctx, taskID)
	if err != nil {
		return
	}
	out, err := a.opencodeRun(ctx, t.RepoDir, t.Prompt, t.Model)
	if err != nil {
		_ = a.tasks.MarkFailed(ctx, taskID, out+"\n"+err.Error())
		return
	}
	_ = a.tasks.MarkCompleted(ctx, taskID, out)
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
	absConfig, _ := filepath.Abs(configPath)

	cctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, "opencode", "run", prompt, "-m", model, "--auto", "--dir", absRepo)
	cmd.Dir = absRepo
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
