"""HTTP 路由。M0：Go 后端经 HTTP 调用；装 protoc 后切 gRPC（见 infra/proto）。"""

import base64
import json
from typing import Any

from fastapi import APIRouter
from fastapi.responses import StreamingResponse
from pydantic import BaseModel

from . import gateway

router = APIRouter(prefix="/v1")


class Message(BaseModel):
    role: str
    content: Any  # str（纯文本）或 list（多模态：[{type:text},{type:image_url}]）


class ChatRequest(BaseModel):
    messages: list[Message]
    model: str | None = None
    trace_id: str | None = None


class ChatResponse(BaseModel):
    model: str
    content: str | None = None
    usage: dict | None = None
    error: str | None = None
    mock: bool | None = None


@router.post("/chat", response_model=ChatResponse)
async def chat_endpoint(req: ChatRequest) -> ChatResponse:
    msgs = [m.model_dump() for m in req.messages]
    result = await gateway.chat(msgs, req.model)
    return ChatResponse(**result)


@router.post("/chat/stream")
async def chat_stream_endpoint(req: ChatRequest):
    """SSE 流式对话：逐 chunk 推 data: {"delta": "..."}，结束 data: [DONE]。"""
    msgs = [m.model_dump() for m in req.messages]

    def gen():
        for delta in gateway.chat_stream(msgs, req.model):
            yield f"data: {json.dumps({'delta': delta}, ensure_ascii=False)}\n\n"
        yield "data: [DONE]\n\n"

    return StreamingResponse(gen(), media_type="text/event-stream")


class ASRRequest(BaseModel):
    audio: str  # base64 编码的音频
    filename: str = "audio.webm"


@router.post("/asr")
async def asr_endpoint(req: ASRRequest):
    """语音识别：base64 音频 → 文字。"""
    try:
        data = base64.b64decode(req.audio)
    except Exception as e:  # noqa: BLE001
        return {"error": f"base64 解码失败: {e}"}
    return await gateway.asr(data, req.filename)
