"""线程安全的内存数据存储，保存工单、客户、客服组长与通知。

生产环境中应替换为持久化数据库实现，本模块定义统一接口以便后续替换。
"""

from __future__ import annotations

import threading
import uuid
from datetime import datetime, timezone
from typing import Dict, List, Optional

from .enums import TicketStatus
from .models import Agent, Customer, Notification, TeamLeader, Ticket
from .security import hash_password


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


class DataStore:
    """基于锁保护的内存仓库。"""

    def __init__(self) -> None:
        self._lock = threading.RLock()
        self._tickets: Dict[str, Ticket] = {}
        self._customers: Dict[str, Customer] = {}
        self._leaders: Dict[str, TeamLeader] = {}
        self._agents: Dict[str, Agent] = {}
        self._agent_by_username: Dict[str, str] = {}
        self._notifications: Dict[str, Notification] = {}
        # 待分类工单队列（FIFO）
        self._pending_queue: List[str] = []

    # ------------------------------------------------------------------ #
    # 客户与客服组长
    # ------------------------------------------------------------------ #
    def upsert_customer(self, customer: Customer) -> Customer:
        with self._lock:
            self._customers[customer.id] = customer
            return customer

    def get_customer(self, customer_id: str) -> Optional[Customer]:
        with self._lock:
            return self._customers.get(customer_id)

    def upsert_leader(self, leader: TeamLeader) -> TeamLeader:
        with self._lock:
            self._leaders[leader.id] = leader
            return leader

    def get_leader(self, leader_id: str) -> Optional[TeamLeader]:
        with self._lock:
            return self._leaders.get(leader_id)

    def all_leaders(self) -> List[TeamLeader]:
        with self._lock:
            return list(self._leaders.values())

    # ------------------------------------------------------------------ #
    # 客服人员（登录账号）
    # ------------------------------------------------------------------ #
    def upsert_agent(self, agent: Agent) -> Agent:
        with self._lock:
            self._agents[agent.id] = agent
            self._agent_by_username[agent.username] = agent.id
            return agent

    def get_agent(self, agent_id: str) -> Optional[Agent]:
        with self._lock:
            return self._agents.get(agent_id)

    def get_agent_by_username(self, username: str) -> Optional[Agent]:
        """根据用户名查找客服人员（验收标准 4: 校验用户名）。"""
        with self._lock:
            agent_id = self._agent_by_username.get(username)
            if agent_id is None:
                return None
            return self._agents.get(agent_id)

    # ------------------------------------------------------------------ #
    # 工单
    # ------------------------------------------------------------------ #
    @staticmethod
    def _new_id(prefix: str) -> str:
        return f"{prefix}-{uuid.uuid4().hex[:12]}"

    def create_ticket(self, ticket: TicketCreate, customer: Customer) -> Ticket:
        with self._lock:
            ticket_id = self._new_id("T")
            ticket = Ticket(
                id=ticket_id,
                title=ticket.title,
                description=ticket.description,
                customer_id=ticket.customer_id,
                customer=customer,
            )
            self._tickets[ticket_id] = ticket
            self._pending_queue.append(ticket_id)
            return ticket

    def get_ticket(self, ticket_id: str) -> Optional[Ticket]:
        with self._lock:
            return self._tickets.get(ticket_id)

    def list_tickets(self, status: Optional[TicketStatus] = None) -> List[Ticket]:
        with self._lock:
            tickets = list(self._tickets.values())
        if status is not None:
            tickets = [t for t in tickets if t.status == status]
        return sorted(tickets, key=lambda t: t.submitted_at, reverse=True)

    def update_ticket(self, ticket: Ticket) -> Ticket:
        with self._lock:
            self._tickets[ticket.id] = ticket
            return ticket

    def next_pending_ticket(self) -> Optional[Ticket]:
        """取出最早一条尚未分类的工单。"""
        with self._lock:
            while self._pending_queue:
                ticket_id = self._pending_queue.pop(0)
                ticket = self._tickets.get(ticket_id)
                if ticket and ticket.status == TicketStatus.SUBMITTED and ticket.urgency_level is None:
                    return ticket
            return None

    # ------------------------------------------------------------------ #
    # 通知
    # ------------------------------------------------------------------ #
    def create_notification(self, notification: Notification) -> Notification:
        with self._lock:
            self._notifications[notification.id] = notification
            return notification

    def get_notification(self, notification_id: str) -> Optional[Notification]:
        with self._lock:
            return self._notifications.get(notification_id)

    def list_notifications(self, leader_id: Optional[str] = None, unread_only: bool = False) -> List[Notification]:
        with self._lock:
            items = list(self._notifications.values())
        if leader_id is not None:
            items = [n for n in items if n.leader_id == leader_id]
        if unread_only:
            items = [n for n in items if not n.is_read]
        return sorted(items, key=lambda n: n.created_at, reverse=True)

    def mark_notification_read(self, notification_id: str) -> Optional[Notification]:
        with self._lock:
            notification = self._notifications.get(notification_id)
            if notification is None:
                return None
            notification.is_read = True
            notification.read_at = _utcnow()
            self._notifications[notification_id] = notification
            return notification


# 全局单例存储
store = DataStore()


def seed_demo_data() -> None:
    """预置示例客服组长与客户，便于直接体验接口。"""

    if store.all_leaders():
        return
    # 预置客服人员登录账号（验收标准 4: 校验用户名与密码）
    #   用户名 agent001 / 密码 Agent@2024
    if store.get_agent_by_username("agent001") is None:
        store.upsert_agent(
            Agent(
                id="A-1001",
                username="agent001",
                full_name="陈客服",
                password_hash=hash_password("Agent@2024"),
                department="客服一部",
                role="agent",
            )
        )
    store.upsert_leader(
        TeamLeader(
            id="L-CRITICAL",
            name="王组长",
            contact="13800000001 / wang@company.com",
            department="紧急工单响应组",
        )
    )
    store.upsert_leader(
        TeamLeader(
            id="L-HIGH",
            name="李组长",
            contact="13800000002 / li@company.com",
            department="高级工单组",
        )
    )
    store.upsert_leader(
        TeamLeader(
            id="L-STANDARD",
            name="赵组长",
            contact="13800000003 / zhao@company.com",
            department="常规工单组",
        )
    )
    store.upsert_customer(Customer(id="C-1001", name="张三", contact="13900000001", is_vip=True))
    store.upsert_customer(Customer(id="C-1002", name="李四", contact="13900000002", is_vip=False))
    store.upsert_customer(Customer(id="C-1003", name="王五", contact="13900000003", is_vip=False))
