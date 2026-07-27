# GLM key 只在智谱 Anthropic 兼容端点有效（agent-runtime 改 transport）

| 日期       | 模块                      | 严重度 |
| ---------- | ------------------------- | ------ |
| 2026-07-27 | agent-runtime / AI 传输层 | P0     |

## 现象

主线闭环 dogfood 时，需求生成（`POST /requirements`）500：

```
生成需求规格: AI 返回错误: Error code: 401,
with error text {"error":{"code":"1000","message":"身份验证失败。"}}
```

整个 AI 链路（需求规格、测试用例、对话、变更总结）全部 401，闭环卡在第 1 步。

## 取证

- 智谱 key `<id>.<secret>` **只在 `https://open.bigmodel.cn/api/anthropic`（Anthropic /v1/messages 协议）有效**，打原生 `/api/paas/v4`（zhipuai SDK 默认端点）一律 401（code 1000 身份验证失败）。
- agent-runtime 容器内用 zhipuai SDK 直测 key → 401；用 urllib 打 `/api/anthropic/v1/messages`（x-api-key 头）→ 200（glm-4.6 正常返回）。
- 模型可用性：anthropic 端点稳定支持 `glm-4.6`；`glm-4-flash` 超时、`glm-4`/`glm-4-plus` 限流（模型名被接受）。

## 根因

`platform/agent-runtime/agent_runtime/gateway.py` 用 `from zhipuai import ZhipuAI` 打原生端点，与现有 key（仅 anthropic 端点授权）不兼容。zhipuai SDK 无法通过 base_url 切到 anthropic 端点（协议不同：OpenAI 风格 vs Anthropic 风格）。

## 修复

`gateway.py` 的 `chat`/`chat_stream` 改走 Anthropic 兼容端点（urllib，零新依赖）：

- 端点 `_BASE/v1/messages`（`ANTHROPIC_BASE_URL` 可覆盖，默认 `https://open.bigmodel.cn/api/anthropic`）。
- 头 `x-api-key` + `anthropic-version: 2023-06-01`。
- 模型 `glm-4.6`（`ANTHROPIC_MODEL` 可覆盖）。
- system 消息抽离（Anthropic 把 system 与 messages 分开）；usage 映射 input_tokens/output_tokens。
- 流式按 SSE `content_block_delta.delta.text` yield。
- `asr`（glm-asr 语音转写）保留 zhipuai 原生 SDK——Anthropic 协议无 ASR，且非文本闭环必需。

`.28` 配套：`.env.prod` 的 `ZHIPUAI_API_KEY` + system_config 已更新为有效 key；agent-runtime 镜像重建。

## 验证

`.28` prod 实测 `POST /requirements`（描述「健康检查接口 GET /health」）→ 201，GLM 生成正确中文标题「系统健康检查接口」+ user_story + 3 条验收标准。AI 链路恢复。

## 遗留

- **图片规格生成**：Anthropic 图片格式（`{type:image, source:{...}}`）与 OpenAI 的 `image_url` 不同，当前 `_split_system` 未做图片格式转换 → 带图需求规格生成可能失败（文本规格已恢复）。后续按需补 multimodal 映射。
- **opencode 编码路径**（dispatch-code）走 opencode.json 自己的 provider 配置，是否需同样指向 anthropic 端点待 AC5 dogfood 时确认。
- **ASR**：key 若在原生端点也 401，语音转写不可用（非闭环必需）。
