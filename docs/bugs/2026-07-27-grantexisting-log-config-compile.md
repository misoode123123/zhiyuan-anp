# cmd/grantexisting 编译失败:log.Config 类型不匹配

- 日期:2026-07-27
- 发现于:AC7 全量回归 `go test -p 1 ./...`(commit 8f04db3 之后)
- 严重度:中(只影响 `cmd/grantexisting` 一次性权限回填工具,不影响 `cmd/server` 主服务)

## 现象

```
cmd\grantexisting\main.go:46:22: cannot use cfg.LogLevel (variable of type string)
  as "zhiyuan-anp/platform/backend/internal/log".Config value in argument to zhlog.New
FAIL    zhiyuan-anp/platform/backend/cmd/grantexisting [build failed]
```

`go build ./...` / `go test ./...` 因 `cmd/grantexisting` 编译失败而整体非零退出(其他 internal 包仍逐包 ok;`go test -p 1 ./...` 除 grantexisting 外全 PASS)。

## 根因(初步)

`internal/log` 包的 `New` 签名改为接受 `Config`(struct),而 `cmd/grantexisting/main.go:46` 仍传 `cfg.LogLevel`(string),未跟上签名变更。该工具不常跑,签名漂移未暴露。

## 修复方向

`cmd/grantexisting/main.go:46` 把 `zhlog.New(cfg.LogLevel)` 改为按 `log` 包当前 `New` 签名构造,例如 `zhlog.New(log.Config{Level: cfg.LogLevel})`(以当前 `internal/log` 的 `New`/`Config` 定义为准,修复时核对)。

## 备注

非 AC7 引入(AC7 只动 `requirement`+`appdeploy`)。本次未修,记录待办。修后跑 `go build ./...` 应恢复全绿。
