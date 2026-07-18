# Bug: 概览/需求工作台打不开(my-tasks null 致前端崩溃)

| 日期 | 严重度 | 状态 |
|---|---|---|
| 2026-07-18 | P0(页面不可用) | 已修复 |

## 现象
首页概览(`/`)、需求工作台(`/requirements`)浏览器报 "This page couldn't load",页面白屏崩溃。

## 根因
后端 `/my-tasks` 接口用 `var toClaim []Requirement`(Go nil slice)→ JSON 序列化为 **`null`**(非 `[]`)。前端 `page.tsx` 拿到 `toClaim: null` 后执行 `toClaim.map(...)` → **`null.map is not a function` → React 崩溃**。

```go
// 错误:nil slice → JSON null
var toClaim, myDev []Requirement

// 正确:初始化空 slice → JSON []
toClaim, myDev := []Requirement{}, []Requirement{}
```

## 修复
- `requirement/handler.go` MyTasks:`var toClaim []X` → `toClaim := []X{}`(所有 4 个分组)
- commit `ecbc0fb`

## 教训(防重复)
1. **Go API 返回 slice 必须初始化 `[]X{}`**,不能用 `var x []X`(nil → JSON null,前端崩溃)
2. **后端改动后必须 curl 验证 JSON 结构**(null vs []),不只看 HTTP 200
3. **前端 `.map` 前应防御**:`(arr || []).map` 或后端保证 `[]`
4. 此 bug 是期2(my-tasks 聚合)引入,部署后用户立即发现——**新接口上线必须端到端验证**(不只 curl status)

## 关联
- PRD: `docs/PRD/2026-07-18-首页开发流程向导-PRD.md`(期2 my-tasks)
- 教训关联 memory: [[verify-cross-frontend-backend]]
