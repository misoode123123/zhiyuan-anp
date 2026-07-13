"""SLA 与工单流转服务。

负责：
- 自动分类流水线（验证分类在 1 分钟 SLA 内完成）；
- 客服组长响应判定（5 分钟 SLA，超时则升级）；
- 处理状态视图构建（验收标准 5）。
"""

from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import Optional

from .classifier import UrgencyClassifier
from .config import settings
from .enums import LeaderResponseStatus, TicketStatus, UrgencyLevel
from .models import LeaderResponse, TicketStatusView
from .notification import NotificationService
from .store import DataStore

logger = logging.getLogger(__name__)


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


class TicketWorkflow:
    """编排分类 -> 通知 -> 响应 -> 状态展示 的核心流程。"""

    def __init__(self, store: DataStore) -> None:
        self._store = store
        self._classifier = UrgencyClassifier(store)
        self._notifier = NotificationService(store)

    def process_one(self, ticket) -> Optional[TicketStatus]:
        """对单张待分类工单执行自动分类并通知（验收标准 1、2、3）。"""
        if ticket.status != TicketStatus.SUBMITTED or ticket.urgency_level is not None:
            return None

        urgency, leader = self._classifier.classify(ticket)
        if leader is None:
            logger.warning("无法为工单 %s 找到客服组长，分类中止", ticket.id)
            return None

        now = _utcnow()
        ticket.urgency_level = urgency
        ticket.classification_completed_at = now
        ticket.classification_duration = (now - ticket.submitted_at).total_seconds()
        ticket.status = TicketStatus.CLASSIFIED
        self._store.update_ticket(ticket)

        # 立即发送通知（同一事务内）
        self._notifier.notify_leader(ticket, urgency, leader)

        sla_ok = ticket.classification_duration <= settings.classification_sla_seconds
        logger.info(
            "工单 %s 分类完成：紧急程度=%s 耗时=%.2fs SLA达标=%s",
            ticket.id, urgency.name, ticket.classification_duration, sla_ok,
        )
        return TicketStatus.CLASSIFIED

    def respond(self, ticket, response: LeaderResponse) -> Ticket:
        """客服组长对工单进行响应（验收标准 4）。"""
        now = _utcnow()

        # 计算响应用时与超时判定
        baseline = ticket.notified_at or ticket.submitted_at
        ticket.leader_response_duration = (now - baseline).total_seconds()
        overdue = ticket.leader_response_duration > settings.leader_response_sla_seconds

        ticket.leader_status = response.status
        ticket.leader_response_at = now

        if response.status == LeaderResponseStatus.RESOLVED:
            ticket.status = TicketStatus.RESOLVED
            ticket.resolved_at = now
        elif response.status == LeaderResponseStatus.ACCEPTED:
            ticket.status = TicketStatus.IN_PROGRESS
        elif response.status == LeaderResponseStatus.REJECTED:
            ticket.status = TicketStatus.IN_PROGRESS
        # ESCALATED / PENDING 保持当前状态不变

        if overdue:
            logger.warning(
                "工单 %s 客服组长响应超时（%.1fs > %ds SLA）",
                ticket.id, ticket.leader_response_duration, settings.leader_response_sla_seconds,
            )
        self._store.update_ticket(ticket)
        return ticket

    def escalate_overdue(self, ticket) -> bool:
        """若已通知但仍未响应且超过 SLA，则自动升级。返回是否执行了升级。"""
        if ticket.status not in (TicketStatus.NOTIFIED, TicketStatus.IN_PROGRESS):
            return False
        if ticket.leader_status != LeaderResponseStatus.PENDING:
            return False
        baseline = ticket.notified_at or ticket.submitted_at
        elapsed = (_utcnow() - baseline).total_seconds()
        if elapsed <= settings.leader_response_sla_seconds:
            return False
        ticket.leader_status = LeaderResponseStatus.ESCALATED
        self._store.update_ticket(ticket)
        logger.warning("工单 %s 超过 5 分钟未响应，已自动升级", ticket.id)
        return True

    def build_status_view(self, ticket) -> TicketStatusView:
        baseline = ticket.notified_at or ticket.submitted_at
        elapsed = (_utcnow() - baseline).total_seconds()
        response_overdue = (
            ticket.leader_status == LeaderResponseStatus.PENDING
            and elapsed > settings.leader_response_sla_seconds
        )
        return TicketStatusView(
            ticket_id=ticket.id,
            status=ticket.status,
            urgency_level=ticket.urgency_level,
            leader_status=ticket.leader_status,
            assigned_leader=ticket.assigned_leader.name if ticket.assigned_leader else None,
            submitted_at=ticket.submitted_at,
            classification_completed_at=ticket.classification_completed_at,
            notified_at=ticket.notified_at,
            leader_response_at=ticket.leader_response_at,
            resolved_at=ticket.resolved_at,
            classification_duration=ticket.classification_duration,
            leader_response_duration=ticket.leader_response_duration,
            response_overdue=response_overdue,
        )


# 全局工作流实例
workflow: Optional[TicketWorkflow] = None


def init_workflow(store: DataStore) -> TicketWorkflow:
    global workflow
    workflow = TicketWorkflow(store)
    return workflow


def get_workflow() -> TicketWorkflow:
    if workflow is None:
        raise RuntimeError("工作流尚未初始化，请先调用 init_workflow()")
    return workflow
