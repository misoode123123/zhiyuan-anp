"""模型网关：智谱 GLM（官方 zhipuai SDK）。

M1：单默认模型（智谱 GLM）；后续按"任务难度 × 成本 × 延迟"动态选模并抽象多 provider。
未配置 ZHIPUAI_API_KEY 或未装 zhipuai 时降级为 mock，保证骨架可独立验证。
"""
import logging
import os
from typing import Optional

from .config import settings  # 先触发 config.load_dotenv()，确保读到 .env

try:
    from zhipuai import ZhipuAI

    _KEY = os.getenv("ZHIPUAI_API_KEY", "")
    _CLIENT = ZhipuAI(api_key=_KEY) if _KEY else None
except ImportError:  # pragma: no cover - 环境降级
    _CLIENT = None

logger = logging.getLogger(__name__)


def _strip(model: str) -> str:
    """zhipuai SDK 用不带 provider 前缀的模型名（如 glm-4-flash）。"""
    return model.split("/", 1)[1] if "/" in model else model


async def chat(messages: list[dict], model: Optional[str] = None) -> dict:
    """调用智谱 GLM。返回 {model, content, usage}。"""
    name = _strip(model or settings.default_model)
    if _CLIENT is None:
        return {
            "model": name,
            "content": "[agent-runtime] zhipuai 未安装或 ZHIPUAI_API_KEY 未配置，降级为 mock。",
            "usage": None,
            "mock": True,
        }
    try:
        resp = _CLIENT.chat.completions.create(model=name, messages=messages)
        u = resp.usage
        usage = {
            "prompt_tokens": getattr(u, "prompt_tokens", 0),
            "completion_tokens": getattr(u, "completion_tokens", 0),
            "total_tokens": getattr(u, "total_tokens", 0),
        } if u else None
        return {
            "model": name,
            "content": resp.choices[0].message.content,
            "usage": usage,
        }
    except Exception as e:  # noqa: BLE001
        logger.exception("model call failed")
        return {"model": name, "content": None, "error": str(e)}
