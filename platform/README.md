# platform/ — 智源 ANP 平台代码

> 详细设计见 `../docs/详细设计/`。本目录是平台自身的实现代码（dogfooding：平台用自己的需求工作台管自己的开发）。

## 结构

| 目录 | 技术 | 说明 |
|---|---|---|
| `backend/` | Go (Gin + sqlx) | 平台业务后端（模块化单体） |
| `agent-runtime/` | Python (LangGraph + LiteLLM) | AI/Agent 运行时（独立微服务） |
| `frontend/` | Next.js + TS | 工作台前端 |
| `infra/` | — | proto / 迁移 / 部署配置 |
| `packages/` | — | 跨端共享包 |

## M0 本地启动（无需 Docker / protoc / make）

> M0 阶段先用 **SQLite + HTTP**，避免依赖 Docker/protoc。装好 Docker 后可改用 PG（见根 `.env.example`）。

### 1. 后端 (Go)
```bash
cd platform/backend
go run ./cmd/server
# 健康检查: http://localhost:8080/healthz
```

### 2. AI 运行时 (Python)
```bash
cd platform/agent-runtime
pip install -r requirements.txt
python -m agent_runtime.main
# http://localhost:8001
```

### 3. 前端 (Next.js)
```bash
pnpm install
pnpm dev
# http://localhost:3000
```

## 备注

- **跨语言通信**：M0 用 HTTP/JSON；装 protoc 后切 gRPC（`infra/proto`）。
- **数据库**：M0 用 SQLite；装 Docker 后切 PG（改 `DATABASE_URL`）。
- **脚本**：用根 `package.json` 的 pnpm scripts（Windows 无 make）。
