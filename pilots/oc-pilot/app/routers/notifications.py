"""通知相关接口（验收标准 2、3）。"""

from __future__ import annotations

from typing import List, Optional

from fastapi import APIRouter, HTTPException

from ..models import Notification
from ..store import store

router = APIRouter(prefix="/notifications", tags=["notifications"])


@router.get("", response_model=List[Notification])
def list_notifications(leader_id: Optional[str] = None, unread_only: bool = False):
    """查询客服组长的系统通知栏。"""
    return store.list_notifications(leader_id, unread_only)


@router.get("/{notification_id}", response_model=Notification)
def get_notification(notification_id: str):
    notification = store.get_notification(notification_id)
    if notification is None:
        raise HTTPException(status_code=404, detail="通知不存在")
    return notification


@router.post("/{notification_id}/read", response_model=Notification)
def mark_read(notification_id: str):
    """标记通知已读。"""
    notification = store.mark_notification_read(notification_id)
    if notification is None:
        raise HTTPException(status_code=404, detail="通知不存在")
    return notification
