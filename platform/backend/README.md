# backend/ — 智源 ANP 平台后端（Go）

平台业务后端，**模块化单体**：单个部署单元，按限界上下文分 package，模块间只通过 Service 接口/领域事件通信，禁止跨域查库。

## 技术栈

Go 1.22 · Gin · Viper · Zap · go-playground/validator · sqlx

## 目录（随阶段演进）

```
backend/
├── cmd/server/        # 入口
└── internal/
    ├── config/        # 配置加载
    ├── log/           # zap 日志
    ├── server/        # HTTP 路由 + 中间件
    ├── httpx/         # 统一响应
    └── (workspace|requirement|dev|rule|gate|...)/  # 各业务域，后续任务接入
```

## 运行

```bash
go mod tidy
go run ./cmd/server
# http://localhost:8080/healthz
```

## 关键约定

- 多租户路由键 `project_space_id` 经 `X-Project-Space-Id` 头注入，所有业务域必须携带。
- 每个请求带 `X-Trace-Id`（缺失则生成），跨 Go/Python 透传。
- 统一响应走 `httpx.OK` / `httpx.Err`。
