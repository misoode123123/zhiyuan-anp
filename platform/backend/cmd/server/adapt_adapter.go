package main

import (
	"context"

	"zhiyuan-anp/platform/backend/internal/dev"
)

// appAdaptSubmitter 把 dev.CodingAgent 适配成 appdeploy.AdaptSubmitter。
// 导入后由 appdeploy 调 SubmitAdapt → 触发 opencode/claude 把应用适配成可在 ANP 部署（改 config 走 env / 修 Dockerfile 等）。
// userID="system"、kind="adapt"、sourceID=appID；模型走默认。改动走 dev.CodingAgent 既有「变更登记→审批」。
type appAdaptSubmitter struct{ a *dev.CodingAgent }

func (s appAdaptSubmitter) SubmitAdapt(ctx context.Context, psID, appID, repoDir, prompt string) error {
	_, err := s.a.Submit(ctx, psID, "system", "adapt", appID, repoDir, prompt, "")
	return err
}
