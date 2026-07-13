"""通知服务。

验收标准 2: 将分类结果以通知形式发送至对应客服组长的系统通知栏。
验收标准 3: 通知内容应包括工单的紧急程度、客户信息和客服组长的联系方式。
验收标准 4: 客服组长在接收到通知后应在 5 分钟内响应工单。
"""

from __future__ import annotations

import uuid
from datetime import datetime, timezone

from .enums import LeaderResponseStatus, TicketStatus, UrgencyLevel
from .models import Notification, TeamLeader, Ticket
from .store import DataStore

_URGENCY_CN = {
    UrgencyLevel.CRITICAL: "紧急",
    UrgencyLevel.HIGH: "高",
    UrgencyLevel.MEDIUM: "中",
    UrgencyLevel.LOW: "低",
}


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


class NotificationService:
    """构造并发送系统通知，同时更新工单状态为「已通知」。"""

    def __init__(self, store: DataStore) -> None:
        self._store = store

    def notify_leader(self, ticket: Ticket, urgency: UrgencyLevel, leader: TeamLeader) -> Notification:
        message = (
            f"【新工单待处理】工单 {ticket.id}\n"
            f"标题：{ticket.title}\n"
            f"紧急程度：{_URGENCY_CN.get(urgency, urgency.name)}\n"
            f"客户信息：{ticket.customer.name}（联系方式：{ticket.customer.contact}）\n"
            f"负责组长：{leader.name}（联系方式：{leader.contact}）\n"
            f"请在 5 分钟内响应。"
        )
        notification = Notification(
            id=f"N-{uuid.uuid4().hex[:12]}",
            ticket_id=ticket.id,
            leader_id=leader.id,
            urgency_level=urgency,
            customer_name=ticket.customer.name,
            customer_contact=ticket.customer.contact,
            leader_name=leader.name,
            leader_contact=leader.contact,
            message=message,
        )
        self._store.create_notification(notification)

        # 更新工单状态：标记为已通知，记录客服组长
        ticket.status = TicketStatus.NOTIFIED
        ticket.assigned_leader = leader
        ticket.leader_status = LeaderResponseStatus.PENDING
        ticket.notified_at = _utcnow()
        self._store.update_ticket(ticket)
        return notification
