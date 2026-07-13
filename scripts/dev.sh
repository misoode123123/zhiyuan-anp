#!/usr/bin/env bash
# 智源 ANP M0 —— 一键启动三服务（Go 后端 / Python AI 运行时 / 前端）
# 用法（Git Bash）: bash scripts/dev.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# 加载 AI 运行时 .env（含 ZHIPUAI_API_KEY），供 Go 后端与 opencode 子进程使用
if [ -f "$ROOT/platform/agent-runtime/.env" ]; then
  set -a; . "$ROOT/platform/agent-runtime/.env"; set +a
fi

echo "[1/3] Go 后端    : http://localhost:8080  (healthz)"
( cd "$ROOT/platform/backend" && go run ./cmd/server ) &

echo "[2/3] AI 运行时  : http://localhost:8001  (healthz)"
( cd "$ROOT/platform/agent-runtime" && python -m agent_runtime.main ) &

echo "[3/3] 前端       : http://localhost:3000"
( cd "$ROOT/platform/frontend" && pnpm dev ) &

echo ""
echo "三服务已启动。Ctrl+C 终止。打开 http://localhost:3000"
wait
