# agent-runtime/ — 智源 ANP AI/Agent 运行时（Python）

承载 Agent 编排（LangGraph，后续接入）与模型调用（LiteLLM 网关），作为**独立微服务**，由 Go 后端经 HTTP（M0）/ gRPC（后续）调用。

## 技术栈

Python 3.10+ · FastAPI · Uvicorn · LiteLLM（多模型路由）

## 目录

```
agent-runtime/
├── requirements.txt
└── agent_runtime/
    ├── main.py        # FastAPI 入口
    ├── config.py      # 环境变量配置
    ├── gateway.py     # LiteLLM 模型网关（未装 litellm 时降级 mock）
    └── routes.py      # /v1/chat 等 HTTP 路由
```

## 运行

```bash
pip install -r requirements.txt
python -m agent_runtime.main
# http://localhost:8001/healthz
```

调通真实模型需在 `.env` 配置对应 API Key（`DEEPSEEK_API_KEY` / `DASHSCOPE_API_KEY` / `OPENAI_API_KEY`）。

## 接口

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/healthz` | 健康检查 |
| POST | `/v1/chat` | 模型对话（`{messages, model?, trace_id?}`） |

## 与 Go 后端的集成

Go 后端通过 `AGENT_RUNTIME_URL`（默认 `http://localhost:8001`）调用 `/v1/chat`。trace_id 透传以串联跨语言链路。
