# Bug 修复记录：system_config 的 NULL description 致 backend 启动 FATAL

| 日期       | 作者     | 关联                                                                                |
| ---------- | -------- | ----------------------------------------------------------------------------------- |
| 2026-07-26 | ANP 团队 | ③ claude MVP 部署（[plan](../superpowers/plans/2026-07-26-多AI工具-claude-MVP.md)） |

> 根因在 **10.10.0.28（deploy_postgres_1/anp）** 现场取证：从 `/opt/anp/data/logs/error.log` 读到 FATAL、`psql` 查到 NULL 行、读源码确认 scan 路径。

## 1. 现象

③ claude MVP 部署后（重建 backend 镜像装 claude/ttyd + 手插 `claude_base_url`/`claude_model`），backend 容器 `Restarting (1)` 崩溃循环，`docker logs` 全空（logger 写文件不写 stdout）。`/opt/anp/data/logs/error.log` 反复：

```
FATAL seed system_config: sql: Scan error on column index 3, name "description": converting NULL to string is unsupported
```

## 2. 根因（双重）

- **代码缺陷（根因）**：`internal/config/sysconfig.go` 的 `ConfigItem.Description` 是 `string`，但 DB `system_config.description` 列**可空**（无 NOT NULL/default）。`Store.Load`（启动期 `SeedIfEmpty` 表非空时调用）和 `Store.All`（配置页）的 `SELECT ... description ...` 把可空列直接扫进 `string`，遇 NULL 行必崩。
- **触发数据**：部署时按交接 SQL 手插 `claude_base_url`/`claude_model` 两行**未给 description** → NULL。此前 6 行旧配置 description 均非空，故 ① 版本一直在跑未暴露；重启后 `SeedIfEmpty→Load` 重扫当前表状态才触发。

即：**schema 允许 NULL 的列，Go 端 scan 却不容忍 NULL**——一个手插无 description 的行就够让全平台启动失败。

## 3. 修复（改动文件）

**后端** `internal/config/sysconfig.go`：

- `Load` / `All` 两处 SELECT 改 `COALESCE(description,'') AS description`（NULL→""，最小改动，不动 struct/JSON 契约）。
- `ConfigItem.Description` 字段加注释说明列可空、查询须 COALESCE，防再犯。

**数据**（.28）：`UPDATE` 给两行 claude 配置补 description（双保险 + 配置页可读）。即便不补，代码层 COALESCE 也已兜底。

## 4. 验证（.28）

- backend 容器 `Up` 稳定（脱离 Restarting），`/healthz/deep` 全 ok（postgres/agent-runtime/opencode/**claude**/**ttyd**/disk）。
- app.log 出现正常 HTTP 流量，无新 FATAL。

## 5. 教训

- **可空列必须用 `sql.NullString` 或查询 `COALESCE`**——别假设应用层永远写非空。
- **交接/部署 SQL 别省列**：能省心插全列（含 description），避免踩此类 schema/ORM 缝隙。
- 相关：[[verify-cross-frontend-backend]]（部署后真打接口/查日志验证，非 curl 一把过）、[[deploy-prod-10.10.0.28]]（logger 写文件，崩溃要看 `/opt/anp/data/logs/*.log` 不是 docker logs）。
