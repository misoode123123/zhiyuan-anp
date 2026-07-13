"""线程安全的内存数据存储，保存需求记录。

生产环境中应替换为持久化数据库实现，本模块定义统一接口以便后续替换。
"""

from __future__ import annotations

import threading
import uuid
from datetime import datetime, timezone
from typing import Dict, List, Optional

from .models import Requirement, RequirementCreate


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


class DataStore:
    """基于锁保护的内存仓库。"""

    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._requirements: Dict[str, Requirement] = {}

    @staticmethod
    def _new_id() -> str:
        return f"R-{uuid.uuid4().hex[:12]}"

    def create_requirement(self, payload: RequirementCreate) -> Requirement:
        with self._lock:
            requirement = Requirement(
                id=self._new_id(),
                title=payload.title,
                description=payload.description,
                priority=payload.priority,
            )
            self._requirements[requirement.id] = requirement
            return requirement

    def get_requirement(self, requirement_id: str) -> Optional[Requirement]:
        with self._lock:
            return self._requirements.get(requirement_id)

    def list_requirements(self) -> List[Requirement]:
        with self._lock:
            items = list(self._requirements.values())
        return sorted(items, key=lambda r: r.created_at, reverse=True)

    def delete_requirement(self, requirement_id: str) -> Optional[Requirement]:
        """删除需求，返回被删除的记录；若不存在则返回 None。"""
        with self._lock:
            return self._requirements.pop(requirement_id, None)


# 全局单例存储
store = DataStore()


def seed_demo_data() -> None:
    """预置示例需求，便于直接体验接口。"""
    with store._lock:
        if store._requirements:
            return
    store.create_requirement(
        RequirementCreate(
            title="用户登录功能",
            description="实现基于 JWT 的用户登录与鉴权。",
            priority="HIGH",
        )
    )
    store.create_requirement(
        RequirementCreate(
            title="数据导出功能",
            description="支持将报表导出为 Excel 与 CSV。",
            priority="MEDIUM",
        )
    )
