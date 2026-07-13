"""HTTP 路由。M0：Go 后端经 HTTP 调用；装 protoc 后切 gRPC（见 infra/proto）。"""
from typing import Any, Optional

from fastapi import APIRouter
from pydantic import BaseModel

from . import gateway

router = APIRouter(prefix="/v1")


class Message(BaseModel):
    role: str
    content: Any  # str（纯文本）或 list（多模态：[{type:text},{type:image_url}]）


class ChatRequest(BaseModel):
    messages: list[Message]
    model: Optional[str] = None
    trace_id: Optional[str] = None


class ChatResponse(BaseModel):
    model: str
    content: Optional[str] = None
    usage: Optional[dict] = None
    error: Optional[str] = None
    mock: Optional[bool] = None


@router.post("/chat", response_model=ChatResponse)
async def chat_endpoint(req: ChatRequest) -> ChatResponse:
    msgs = [m.model_dump() for m in req.messages]
    result = await gateway.chat(msgs, req.model)
    return ChatResponse(**result)
