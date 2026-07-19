# 智源 ANP —— 统一工程入口（Linux / macOS / CI 用）
# Windows 本地若无 make，改用根 package.json 的 pnpm 脚本（目标名对齐）：
#   pnpm be:test / pnpm fe:lint / pnpm py:lint ...

GO ?= go
BE  := platform/backend
FE  := platform/frontend
PY  := platform/agent-runtime

.PHONY: dev build test lint fmt check \
        be-build be-test be-cover be-vet be-lint be-fmt \
        fe-build fe-lint fe-test fe-fmt \
        py-lint py-fmt

# ---- 聚合 ----
dev:
	bash scripts/dev.sh

build: fe-build be-build

test: be-test fe-test

lint: be-vet be-lint fe-lint py-lint

fmt: be-fmt fe-fmt py-fmt

# 构建后再跑 lint+test，CI 全量入口
check: build lint test

# ---- Go 后端 ----
be-build:
	cd $(BE) && $(GO) build ./...

be-test:
	cd $(BE) && $(GO) test ./...

be-cover:
	cd $(BE) && $(GO) test ./... -coverprofile=coverage.out -covermode=atomic

be-vet:
	cd $(BE) && $(GO) vet ./...

# golangci-lint 未安装时：CI 用 action 装好后跑；本地可用 go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
be-lint:
	cd $(BE) && golangci-lint run ./...

be-fmt:
	cd $(BE) && gofmt -s -w .

# ---- 前端 ----
fe-build:
	cd $(FE) && pnpm build

fe-lint:
	cd $(FE) && pnpm lint

fe-test:
	cd $(FE) && pnpm test

fe-fmt:
	cd $(FE) && pnpm exec prettier --write .

# ---- Python AI 运行时 ----
py-lint:
	cd $(PY) && ruff check .

py-fmt:
	cd $(PY) && ruff format .
