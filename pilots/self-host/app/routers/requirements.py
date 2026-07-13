"""需求相关接口。"""

from __future__ import annotations

import logging
from typing import List

from fastapi import APIRouter, HTTPException, status

from ..models import DeleteResponse, Requirement, RequirementCreate
from ..store import store

logger = logging.getLogger(__name__)

router = APIRouter(prefix="/requirements", tags=["requirements"])


@router.post("", response_model=Requirement, status_code=status.HTTP_201_CREATED)
def create_requirement(payload: RequirementCreate):
    """创建需求。"""
    return store.create_requirement(payload)


@router.get("", response_model=List[Requirement])
def list_requirements():
    """查询全部需求。"""
    return store.list_requirements()


@router.get("/{requirement_id}", response_model=Requirement)
def get_requirement(requirement_id: str):
    """查询单个需求。"""
    requirement = store.get_requirement(requirement_id)
    if requirement is None:
        raise HTTPException(status_code=404, detail="需求不存在")
    return requirement


@router.delete("/{requirement_id}", response_model=DeleteResponse)
def delete_requirement(requirement_id: str):
    """按需求 ID 删除需求。

    - 需求存在：返回 200 与删除成功信息（验收标准 3）。
    - 需求不存在：返回 404 与需求不存在信息（验收标准 4）。
    - 删除过程发生错误：返回 500 与内部服务器错误信息（验收标准 5）。
    """
    # 先确认需求是否存在，以区分「不存在」与「删除失败」
    if store.get_requirement(requirement_id) is None:
        raise HTTPException(status_code=404, detail="需求不存在")

    try:
        store.delete_requirement(requirement_id)
    except Exception:
        logger.exception("删除需求 %s 时发生内部错误", requirement_id)
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail="内部服务器错误，删除需求失败",
        )

    return DeleteResponse(message="删除成功", id=requirement_id)
