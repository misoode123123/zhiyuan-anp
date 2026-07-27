"""模型网关：智谱 GLM via Anthropic 兼容端点。

2026-07-27：智谱 key（<id>.<secret>）只在智谱 **Anthropic 兼容端点**
（``https://open.bigmodel.cn/api/anthropic``，Anthropic /v1/messages 协议）有效，
打原生 ``/api/paas/v4``（zhipuai SDK）会 401「身份验证失败」。故 chat/chat_stream 改走
Anthropic 兼容端点（urllib，无额外 SDK 依赖），稳定模型 glm-4.6。

ASR（glm-asr 语音转写）仍走 zhipuai 原生 SDK——Anthropic 协议无 ASR；非主线闭环必需，
key 若在原生端点也 401，ASR 调用会返回 error（不影响文本闭环）。
"""

import json
import logging
import os
import urllib.error
import urllib.request

from .config import settings  # 先触发 config.load_dotenv()，确保读到 .env

logger = logging.getLogger(__name__)

# 智谱 key（在 Anthropic 兼容端点有效）。
_KEY = os.getenv("ZHIPUAI_API_KEY", "")
# Anthropic 兼容端点（main.go 的 claude_base_url 同址）。
_BASE = os.getenv("ANTHROPIC_BASE_URL", "https://open.bigmodel.cn/api/anthropic").rstrip("/")
# 端点稳定支持的模型（glm-4-flash 经 Anthropic 协议不稳定/超时；glm-4/plus 限流）。
_MODEL = os.getenv("ANTHROPIC_MODEL", "glm-4.6")

# ASR 仍用 zhipuai 原生 SDK（best-effort；key 在原生端点可能 401，ASR 非闭环必需）。
try:
    from zhipuai import ZhipuAI

    _ZHIPU = ZhipuAI(api_key=_KEY) if _KEY else None
except ImportError:  # pragma: no cover - 环境降级
    _ZHIPU = None


def _strip(model: str) -> str:
    """去掉 provider 前缀（如 zhipu/glm-4-flash → glm-4-flash），仅用于日志/返回。"""
    return model.split("/", 1)[1] if "/" in model else model


def _messages_url() -> str:
    return _BASE + "/v1/messages"


def _headers() -> dict:
    return {
        "x-api-key": _KEY,
        "anthropic-version": "2023-06-01",
        "content-type": "application/json",
    }


def _split_system(messages: list[dict]) -> tuple[str, list[dict]]:
    """Anthropic 把 system 与 messages 分离；content 可能是 str 或 multimodal list。"""
    sys_parts: list[str] = []
    user_msgs: list[dict] = []
    for m in messages:
        if m.get("role") == "system":
            c = m.get("content")
            sys_parts.append(c if isinstance(c, str) else json.dumps(c, ensure_ascii=False))
        else:
            user_msgs.append(m)
    return "\n\n".join(sys_parts), user_msgs


def _body(messages: list[dict], stream: bool = False) -> dict:
    sys_msg, user_msgs = _split_system(messages)
    body: dict = {"model": _MODEL, "max_tokens": 4096, "messages": user_msgs}
    if sys_msg:
        body["system"] = sys_msg
    if stream:
        body["stream"] = True
    return body


async def chat(messages: list[dict], model: str | None = None) -> dict:
    """调用智谱 GLM（Anthropic 兼容端点）。返回 {model, content, usage}。"""
    name = _strip(model or settings.default_model)
    if not _KEY:
        return {
            "model": name,
            "content": "[agent-runtime] ZHIPUAI_API_KEY 未配置，降级为 mock。",
            "usage": None,
            "mock": True,
        }
    req = urllib.request.Request(
        _messages_url(), data=json.dumps(_body(messages)).encode("utf-8"), method="POST"
    )
    for k, v in _headers().items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            d = json.loads(resp.read().decode("utf-8", "replace"))
        text = "".join(
            b.get("text", "")
            for b in d.get("content", [])
            if b.get("type") == "text"
        )
        u = d.get("usage") or {}
        inp, out = u.get("input_tokens", 0), u.get("output_tokens", 0)
        usage = {"prompt_tokens": inp, "completion_tokens": out, "total_tokens": inp + out}
        return {"model": d.get("model", name), "content": text, "usage": usage}
    except urllib.error.HTTPError as e:
        logger.exception("anthropic chat failed: HTTP %s", e.code)
        return {"model": name, "content": None, "error": f"HTTP {e.code}: {e.read().decode('utf-8','replace')[:200]}"}
    except Exception as e:  # noqa: BLE001
        logger.exception("anthropic chat failed")
        return {"model": name, "content": None, "error": str(e)}


def chat_stream(messages: list[dict], model: str | None = None):
    """流式调用智谱 GLM（Anthropic 兼容端点），yield delta content。"""
    name = _strip(model or settings.default_model)
    if not _KEY:
        yield "[agent-runtime] mock（ZHIPUAI_API_KEY 未配置）"
        return
    req = urllib.request.Request(
        _messages_url(), data=json.dumps(_body(messages, stream=True)).encode("utf-8"), method="POST"
    )
    for k, v in _headers().items():
        req.add_header(k, v)
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            for raw in resp:
                line = raw.decode("utf-8", "replace").strip()
                if not line.startswith("data:"):
                    continue
                data = line[5:].strip()
                if data == "[DONE]":
                    break
                try:
                    ev = json.loads(data)
                except json.JSONDecodeError:
                    continue
                if ev.get("type") == "content_block_delta":
                    t = ev.get("delta", {}).get("text")
                    if t:
                        yield t
    except Exception as e:  # noqa: BLE001
        logger.exception("stream call failed")
        yield f"\n[stream error: {e}]"


async def asr(audio_bytes: bytes, filename: str = "audio.webm") -> dict:
    """语音识别：智谱 GLM-ASR（原生 SDK，OpenAI 兼容 audio.transcriptions）。返回 {text} 或 {error}。

    Anthropic 协议无 ASR；若 key 在原生端点 401，此处返回 error（非闭环必需）。
    """
    import io

    if _ZHIPU is None:
        return {"error": "zhipuai 未安装或 ZHIPUAI_API_KEY 未配置"}
    try:
        resp = _ZHIPU.audio.transcriptions.create(
            model="glm-asr",
            file=(filename, io.BytesIO(audio_bytes)),
        )
        return {"text": getattr(resp, "text", "") or ""}
    except Exception as e:  # noqa: BLE001
        logger.exception("asr failed")
        return {"error": str(e)}
