"""领域枚举定义。"""

from __future__ import annotations

from enum import Enum, IntEnum


class UrgencyLevel(IntEnum):
    """工单紧急程度。数字越大越紧急。"""

    LOW = 1
    MEDIUM = 2
    HIGH = 3
    CRITICAL = 4

    @classmethod
    def from_label(cls, label: str) -> "UrgencyLevel":
        return _LABEL_MAP[label.upper()]


_LABEL_MAP = {level.name: level for level in UrgencyLevel}


class TicketStatus(str, Enum):
    """工单生命周期状态。"""

    SUBMITTED = "submitted"          # 已提交，待分类
    CLASSIFIED = "classified"        # 已完成紧急程度分类
    NOTIFIED = "notified"            # 已通知客服组长
    RESPONDED = "responded"          # 客服组长已响应
    IN_PROGRESS = "in_progress"      # 处理中
    RESOLVED = "resolved"            # 已解决


class LeaderResponseStatus(str, Enum):
    """客服组长对工单的处理状态（验收标准 5）。"""

    PENDING = "pending"              # 待响应（已收到通知）
    ACCEPTED = "accepted"            # 已接单
    REJECTED = "rejected"            # 已拒单
    ESCALATED = "escalated"          # 超时升级 / 已升级
    RESOLVED = "resolved"            # 已处理完成
