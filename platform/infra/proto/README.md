# proto/ — gRPC 契约（M0 仅定义，不生成）

M0 跨语言通信用 **HTTP/JSON**（Go 后端 ↔ Python agent-runtime）。本目录定义未来 gRPC 契约，
装 `protoc` + `buf` 后生成 Go/Python 桩并切换。

| 文件 | 服务 | 说明 |
|---|---|---|
| `orchestration.proto` | OrchestrationService | Agent 编排 + 模型对话 |
| `gateway.proto` | ModelGateway | 模型路由（成本/故障切换） |

## 生成桩代码（装 protoc 后）

```bash
protoc \
  --go_out=. --go-grpc_out=. \
  --python_out=. --grpc_python_out=. \
  orchestration.proto gateway.proto
```

## M0 的 HTTP 等价实现

- `OrchestrationService.Chat` → `POST http://localhost:8001/v1/chat`
- trace_id 经 `X-Trace-Id` 头跨语言透传
