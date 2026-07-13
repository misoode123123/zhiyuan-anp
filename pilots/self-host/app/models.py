"""领域模型与数据传输对象。"""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Optional

from pydantic import BaseModel, Field


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


class RequirementBase(BaseModel):
    title: str = Field(..., min_length=1, description="需求标题")
    description: str = Field("", description="需求详细描述")
    priority: str = Field("MEDIUM", description="需求优先级")


class RequirementCreate(RequirementBase):
    pass


class Requirement(RequirementBase):
    """需求完整记录。"""

    id: str
    created_at: datetime = Field(default_factory=_utcnow)
    updated_at: Optional[datetime] = None


class DeleteResponse(BaseModel):
    """删除接口的统一返回结构。"""

    message: str
    id: str
