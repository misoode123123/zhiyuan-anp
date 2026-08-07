# ANP 模型中心 · 凭证安全加固设计（Phase 1）—— ⚠️ 已废弃

> **⛔ DEPRECATED（2026-08-07）**：本 spec 的「凭证 AES 加密」方向已废弃。用户选择维持凭证现状
> （`compute_provider.api_key` 明文），改做「用户模型授权与选择」。
> **取代本文档**：`2026-08-07-model-center-user-grant-design.md`。本文件仅作历史留存。
>
> ~~本设计基于桌面 `模型用户.txt` 草案 + 2026-08-07 全代码核验修订。~~
> 原草案假设「从零造模型目录」——核验发现 `compute_provider`/`compute_model`/`compute_route`
> 三表 + `compute.Gateway` **已是半个模型中心**，故改为**扩展 `compute_*`**，首期聚焦**凭证安全加固**。
> 三条已确认决策：① 扩展 `compute_*`（不新建并行表）；② 首期做凭证安全加固；③ opencode 编码路径 per-user key 暂不动。

---

## 一、目标与非目标

### 目标（Phase 1，本 spec 范围）

- 模型 API key **加密存储**（AES-256-GCM），数据库落盘为密文。
- **永不明文回显**前端（API 返回 mask）。
- **永不入 git**：清理已泄漏进仓库的硬编码 key。
- `compute.Gateway` 调用模型时**内存解密**，明文只存在于进程内存、不入日志/error。

### 非目标（留作 follow-up，见路线图）

- 用户自带凭证 + 三级 resolver（P2）。
- 收拢 `appdeploy` 两处散落调用（P3）。
- 用户模型策略 + `/compute`→`/models` 前端改名（P4）。
- opencode 编码路径 per-user key（P5，受 opencode serve 单文件限制，本期明确不动）。
- `system_config.zhipuai_api_key` 的加密/迁移（P2 随凭证统一处理；本期该键仍由 env 种子提供，保持现状）。

---

## 二、现状核验（决策依据，file:line 锚定真实代码）

| 维度               | 现状（核验确认）                                                                                                                                                                                                       | 说明                                                                                                     |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| 模型目录           | `compute_provider`/`compute_model`/`compute_route` 三表（migration `000011_compute_provider_model.up.sql`）                                                                                                            | 已是多 provider/多 model/按 task_type 路由的目录                                                         |
| 统一网关           | `compute.Gateway.doForward`（`internal/compute/route.go:212-277`，OpenAI 兼容 `/chat/completions` + Bearer + 解析 usage，含路由/fallback/cost）；暴露 `POST /compute/chat`                                             | **已是对统一 client 的雏形**，被 `requirement`/`qa` service 使用                                         |
| opencode.json 生成 | `compute.GenerateOpenCodeConfig`（`opencode_gen.go:44`）/`WriteOpenCodeConfig`（`:106`），从 `compute_provider` 表生成，**`apiKey` 明文写进 json**（`opencode_gen.go:64`）                                             | 凭证落盘明文                                                                                             |
| 刷新触发           | `refreshOpenCodeConfig`（`provider_handler.go:345-349`）只在 Create/Delete 调用，**`UpdateProvider/UpdateModel` 不触发**（bug）                                                                                        | 改 provider 不重写 json                                                                                  |
| 硬编码真 key       | 🔴 `platform/opencode.json:9` 明文 key `9712b8a64fa94af88ea0717ed9820e4f.AgsGNHtdcvqidRhE` **已提交进 git**；`opencode.example.json:4` 用 `{env:ZHIPUAI_API_KEY}` 占位                                                 | 历史泄漏，须清理 + 轮换                                                                                  |
| 平台 key 来源      | `system_config.zhipuai_api_key`（`main.go:132` seed，值来自 env `ZHIPUAI_API_KEY`）；`compute_provider.api_key`（DB，SeedProviders 留空 `''`）                                                                         | 两套并存，本期只处理 compute_provider 侧                                                                 |
| 现有加密           | 全仓**仅 bcrypt**（`auth/user.go`），**无任何对称加密/secret 工具**；`compute_provider.api_key TEXT` 明文、`omitempty` 直回前端                                                                                        | AES 须全新建                                                                                             |
| 迁移机制           | `internal/db/migrate.go`：`//go:embed migrations/pg/*.sql`，按文件名 version 排序，每迁移一个 tx + `schema_migrations` 版本表，启动时跑、幂等；当前最新 `000034`；pgvector 已启用                                      | 新表/改表走 `000035_*.up/down.sql`；**迁移是纯 SQL，不能调 Go 的加密函数**（影响 backfill 设计，见 4.2） |
| 编码路径 key 注入  | `dev/coding.go:148-190`（headless `opencode run`）按进程 env 注入 key，可行；`codews`（`opencode serve`）**只读全局 `$HOME/.config/opencode/opencode.json`，忽略 `OPENCODE_CONFIG` env**，`toolEnv` 对 opencode 返 nil | 编码 serve 路径无法 per-user 注 key → P5                                                                 |

**核心判断**：目录 + 网关已就绪，真正空白是**凭证加密 + 用户级凭证/策略**。Phase 1 先填「凭证加密」这一最高价值、且不受 opencode 限制的空白。

---

## 三、架构决策：扩展 `compute_*`（非新建并行表）

**决策**：不新建 `model_provider`/`model_catalog` 等表，而是**扩展现有 `compute_provider`/`compute_model`**：

- `compute_provider` 增加密 key 列；凭证与目录共用同一实体。
- 复用 `compute.Gateway` 作为统一调用出口（后续 P3 把 `appdeploy` 散落调用收拢到它）。
- 前端 `/compute` 后续（P4）改名为 `/models`，本期不改名以缩小 blast radius。

**理由**：零数据迁移、不造重复目录、复用已验证的 Gateway 路由/fallback/cost 能力。新建并行表会导致两套 provider 数据脱节、需双向同步，是草案的主要缺陷。

---

## 四、Phase 1 详细设计

### 4.1 新建 `internal/secret`（AES-256-GCM，零新依赖）

纯标准库 `crypto/aes` + `crypto/cipher`。包路径建议 `platform/backend/internal/secret`。

**对外 API**：

```go
// 包级单例，启动时 New(masterKey) 注入
type Cipher struct{ key []byte }
func New(masterKey []byte) (*Cipher, error)   // 校验 len==32
func (c *Cipher) Encrypt(plaintext string) (string, error)   // → base64(nonce||ciphertext)
func (c *Cipher) Decrypt(b64 string) (string, error)         // → plaintext
func (c *Cipher) Mask(plaintext string) string               // → "****"+末4位（用于 API 回显）
```

- nonce 每次随机生成（`crypto/rand`），与密文一同存 base64；解密时拆分。
- GCM 提供 机密性 + 完整性（防篡改）。

**主密钥来源**：env `ANP_CRED_MASTER_KEY`（32 字节，建议 base64 编码；生成：`openssl rand -base64 32`）。

- `config.Config` 增字段 `CredMasterKey`（`config.go` viper 读 `anp_cred_master_key`）。
- `deploy/.env.prod.example` 增 `ANP_CRED_MASTER_KEY=` 占位（值留空，部署时填）。

**启动行为（fail-fast）**：`main.go` 启动时构造 `secret.New(cfg.CredMasterKey)`；**主密钥缺失/长度错 → 启动失败并明确报错**（不静默降级为明文，避免「以为加密实际明文」的假安全）。注入到需要它的 store/handler。

> 注意：fail-fast 意味着部署 Phase 1 时**必须先在 `.env.prod` 配好 `ANP_CRED_MASTER_KEY` 再重启 backend**，否则起不来。部署步骤会在实施计划里强调。

### 4.2 数据模型变更（migration `000035_secret` + Go 级 backfill）

**migration `000035_secret.up.sql`**（纯 DDL，只加列）：

```sql
ALTER TABLE compute_provider ADD COLUMN IF NOT EXISTS api_key_enc TEXT;
```

**`000035_secret.down.sql`**：

```sql
ALTER TABLE compute_provider DROP COLUMN IF EXISTS api_key_enc;
```

**Go 级幂等 backfill**（不能用 SQL——迁移是纯 SQL，调不到 AES；放在迁移后启动钩子）：

- 新增 `compute.Store.MigrateCredentials(ctx, c *secret.Cipher)`，在 `main.go` `db.Migrate` 之后调用一次。
- 逻辑：`SELECT id, api_key FROM compute_provider WHERE COALESCE(api_key,'')<>'' AND api_key_enc IS NULL` → 逐行 `enc,_ := c.Encrypt(api_key)` → `UPDATE compute_provider SET api_key_enc=$1, api_key='' WHERE id=$2`。
- 幂等：只处理 `api_key` 非空且 `api_key_enc` 为空的行；已迁移行（api_key 空、api_key_enc 有值）跳过。
- 当前 .28 的 `compute_provider` 由 `SeedProviders` 种入、key 留空，故 backfill 实际多为 no-op；但保证存量任意手填 key 被加密。

**读路径（双读过渡）**：`compute.Store` 读取 provider 时，密钥字段解析为「`api_key_enc` 优先，空则回退 `api_key`」；稳运行后（P2）删 `api_key` 列。

**Provider model 变更**（`internal/compute/provider.go`）：

- 新增 `APIKeyEnc string \`db:"api_key_enc" json:"-"\``（json `-`，永不序列化回前端）。
- `APIKey string \`db:"api_key" json:"api_key,omitempty"\`` 保留过渡，但**写库前由 store 加密**、**读出后由 Gateway 解密**；JSON 输出改为 mask 字段（见 4.4）。

### 4.3 `compute.Gateway` 调用时内存解密

`internal/compute/route.go` `doForward`（现 `route.go:225` `Authorization: Bearer <p.APIKey>`）改为：

```go
plainKey, err := svc.cipher.Decrypt(p.APIKeyEnc) // 空 enc 则回退 p.APIKey（兼容未迁移）
if err != nil { /* 记 warn，不把 key 进日志 */ }
httpReq.Header.Set("Authorization", "Bearer "+plainKey)
```

- 明文 `plainKey` 仅本次请求存活于栈，不落库、不日志、不 error message。
- Gateway 持有 `*secret.Cipher` 引用（`main.go` 注入）。

### 4.4 API mask（前端永不見明文）

- `GET /compute/providers`：响应里 **去掉明文 `api_key`**，改返 `api_key_masked`（如 `****RhE.`，取 `Mask` 末 4 位；空 key 返空串）。
- `POST /compute/providers` / `PUT /compute/providers/:id`：**入参收明文 `api_key`** → store 写库时 `Encrypt` 进 `api_key_enc`、清空 `api_key`。
- 前端 `platform/frontend/app/compute/page.tsx`（现 `api_key?: string`，`saveProvider` 传明文）：展示改读 `api_key_masked`；保存仍提交明文 `api_key`（后端加密）。
- `swagger.json`/`api-types.ts` 同步：provider 响应 schema 加 `api_key_masked`、去 `api_key` 明文输出。

### 4.5 清理 git 硬编码 key + 运行期生成

- `platform/opencode.json:9`：真 key → `{env:ZHIPUAI_API_KEY}` 占位（对齐 `opencode.example.json`），**仓库不再含任何真 key**。
- 运行期 opencode.json 由 `compute.GenerateOpenCodeConfig` 生成时，`options.apiKey` 从**加密库解出的平台 key** 写入（serve 路径仍用平台 key，本期不动 per-user——符合决策③）。
  - 即：`opencode_gen.go:64` 的 `APIKey: p.APIKey` 改为 `APIKey: decryptIfEncrypted(p)`。
- `main.go:107-125` 的「复制 committed opencode.json 到 `$HOME/.config/opencode/`」逻辑保留兜底，但主路径是 DB 生成（解密后写入）。

> 🔴 **密钥已进 git 历史，代码层清理无法撤销历史泄漏**。Phase 1 交付时须**同时在智谱控制台轮换这把 key**，新 key 只走 `ANP_CRED_MASTER_KEY` 加密链路，不再写进任何文件。

### 4.6 顺带 bugfix（核验发现，纳入本期）

`internal/compute/provider_handler.go`：

- `UpdateProvider`、`UpdateModel` 末尾补调 `h.refreshOpenCodeConfig(c)`（现仅 Create/Delete 调，改 provider/model 不重写 json）。

---

## 五、安全考量

- **明文边界**：明文 key 仅存在于 ① 用户 POST 的请求体（TLS 传输，落库即加密）② Gateway 调用时的栈变量（瞬时）。其余位置（DB、json、日志、error、前端）一律密文或 mask。
- **日志审计**：确认 `compute/route.go`、`appdeploy`（虽本期不动，但 shared key 同源）、opencode stdout 落库（`coding.go`）不打印 `Authorization` 头 / key。实施时 grep 一遍 `api_key`/`apiKey`/`Bearer` 的日志点。
- **主密钥生命周期**：`ANP_CRED_MASTER_KEY` 不入库、不入 git、不日志；轮换策略留 follow-up（轮换需全量 re-encrypt，P5 一并设计）。
- **mask 不可逆**：`Mask` 只暴露末 4 位，无法还原；空 key 不暴露长度信息。

---

## 六、测试策略

`internal/secret` 单测（`secret_test.go`）：

- `Encrypt→Decrypt` 往返一致。
- 相同明文两次 `Encrypt` 密文不同（nonce 随机）。
- 篡改密文（翻转字节）→ `Decrypt` 报错（GCM 完整性）。
- 错误/缺失主密钥 → `New` 报错。
- `Mask` 格式（末 4 位 + 前缀；空串/短串边界）。

`compute` 集成（`provider_handler_test.go` / store 测试，复用 `testutil.TestDB` 连 .28 `anp_test` PG）：

- **backfill 幂等**：插入明文 `api_key` 行 → `MigrateCredentials` → `api_key_enc` 非空、`api_key` 空；再跑一次无变化。
- **双读**：手动插入仅 `api_key`（未迁移）行 → Gateway 能解出（回退）；仅 `api_key_enc` 行 → 能解出。
- **API mask**：`GET /compute/providers` 响应含 `api_key_masked`、**不含明文 `api_key`**；`POST` 后落库为密文。
- **Gateway 解密调用**：mock 上游 `/chat/completions`，断言收到的 `Authorization: Bearer <解密后的key>`（用测试专用 key）。

串行跑：`go test -p 1 -count=1 ./internal/secret/... ./internal/compute/...`（CI 权威；本地 .28 PG）。

---

## 七、路线图（Phase 1 之后，本期不实现）

| 阶段                     | 范围                                                                                                                                                                                                             | 前置                         |
| ------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| **P2 用户自带凭证**      | `model_credential` 表（三级 scope：user/space/platform）+ `Resolver`（user>space>platform>system_config 兜底）+ 员工自助 API/页；`system_config.zhipuai_api_key` 收编进凭证体系；覆盖 `compute.Gateway` 生成路径 | Phase 1 的 `internal/secret` |
| **P3 收拢散落调用**      | 删 `appdeploy/handler.go` 两处手写硬编码 glm-5.1 + URL（`summarizeChange`/`checkRequirement`），统一走 `compute.Gateway`                                                                                         | P2（凭证统一）               |
| **P4 策略 + 前端改名**   | `user_model_policy`（白名单 + 默认模型）+ `/compute`→`/models` 改名 + `/workspace`、`/dev` 模型下拉（net-new）接 policy                                                                                          | P2                           |
| **P5 编码 per-user key** | 解 opencode serve 单文件限制：per-user opencode.json + 独立 `$HOME` 启动，或本地鉴权重写代理                                                                                                                     | 决策③明确后置                |

---

## 八、风险与注意点

- **fail-fast 部署顺序**：必须先在 `.env.prod` 配 `ANP_CRED_MASTER_KEY` 再重启 backend，否则起不来。部署文档/计划须标注。
- **密钥轮换不可省**：git 里的硬编码 key 须在智谱控制台作废重发；代码清理 ≠ 撤销泄漏。
- **双读过渡期**：`api_key`（明文）与 `api_key_enc`（密文）并存期间，读路径优先 enc、回退 plain；删 `api_key` 列须等所有环境确认 enc 已全量 backfill（P2 末）。
- **GenerateOpenCodeConfig 解密**：生成 opencode.json 时解密平台 key 写入——该 json 仍含明文平台 key（在 .28 服务器本地、平台自用、非用户可见），这是可接受的（安全目标是「不入 git + 不回前端 + DB 加密」）。
- **向后兼容**：未迁移行（仅 `api_key`）通过双读继续工作；backfill 在启动后台静默完成，无需停机。
- **opencode serve 路径**：本期不碰 per-user；平台 key 经生成器解密后写 json，行为不变，只是 key 来源从「committed 文件硬编码」变为「加密 DB 解密」。

---

## 九、一句话总结

把 `compute_provider.api_key`（明文）和 git 里硬编码的真 key，重构为「AES-256-GCM 加密 + 运行期内存解密 + API mask + 主密钥 env fail-fast」的凭证安全层；复用既有 `compute_*` 目录与 `compute.Gateway`，不造重复表；opencode 编码路径 per-user 与用户自带凭证留 P2-P5。交付时同步轮换已泄漏的智谱 key。
