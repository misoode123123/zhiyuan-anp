"""领域模型与数据传输对象。"""

from __future__ import annotations

from datetime import datetime, timezone
from typing import Optional

from pydantic import BaseModel, Field

from .enums import LeaderResponseStatus, TicketStatus, UrgencyLevel


def _utcnow() -> datetime:
    return datetime.now(timezone.utc)


class Customer(BaseModel):
    """提交工单的客户。"""

    id: str
    name: str
    contact: str
    # VIP 客户会在分类时提升一个紧急等级
    is_vip: bool = False


class TeamLeader(BaseModel):
    """客服组长（通知接收方）。"""

    id: str
    name: str
    contact: str  # 电话 / 工号 / 邮箱等联系方式
    department: str = "默认客服组"


class Agent(BaseModel):
    """客服人员（登录系统、处理客户咨询的主体）。"""

    id: str
    username: str
    full_name: str
    # 验收标准 7: 密码仅以哈希存储，序列化/日志中永不暴露明文
    password_hash: str = Field(default="", repr=False, exclude=True)
    department: str = "客服部"
    role: str = "agent"
    created_at: datetime = Field(default_factory=_utcnow)


class AgentInfo(BaseModel):
    """对外暴露的客服人员信息（不含密码相关字段）。"""

    id: str
    username: str
    full_name: str
    department: str
    role: str


class LoginRequest(BaseModel):
    """登录请求（验收标准 1: 用户名 + 密码输入框；验收标准 3: 提示信息）。"""

    username: str = Field(..., min_length=1, description="请输入用户名")
    password: str = Field(..., min_length=1, description="请输入密码")


class LoginResponse(BaseModel):
    """登录成功响应（验收标准 6: 返回令牌供后续访问客服操作界面）。"""

    token: str
    token_type: str = "Bearer"
    expires_in: int
    agent: AgentInfo


class TicketBase(BaseModel):
    title: str = Field(..., min_length=1)
    description: str = Field("", description="工单正文，用于紧急程度分类")
    customer_id: str


class TicketCreate(TicketBase):
    pass


class Ticket(TicketBase):
    """工单完整记录。"""

    id: str
    customer: Customer
    status: TicketStatus = TicketStatus.SUBMITTED
    urgency_level: Optional[UrgencyLevel] = None
    assigned_leader: Optional[TeamLeader] = None
    leader_status: LeaderResponseStatus = LeaderResponseStatus.PENDING
    submitted_at: datetime = Field(default_factory=_utcnow)
    classification_completed_at: Optional[datetime] = None
    notified_at: Optional[datetime] = None
    leader_response_at: Optional[datetime] = None
    resolved_at: Optional[datetime] = None
    # 分类耗时（秒），用于验证 SLA
    classification_duration: Optional[float] = None
    # 响应用时（秒）
    leader_response_duration: Optional[float] = None

    def within_classification_sla(self, sla_seconds: int) -> bool:
        if self.classification_completed_at is None:
            return False
        return self.classification_duration is not None and self.classification_duration <= sla_seconds


class Notification(BaseModel):
    """发送至客服组长系统通知栏的通知（验收标准 2、3）。"""

    id: str
    ticket_id: str
    leader_id: str
    # 通知内容：紧急程度 + 客户信息 + 组长联系方式
    urgency_level: UrgencyLevel
    customer_name: str
    customer_contact: str
    leader_name: str
    leader_contact: str
    message: str
    created_at: datetime = Field(default_factory=_utcnow)
    read_at: Optional[datetime] = None
    is_read: bool = False


class LeaderResponse(BaseModel):
    """客服组长对工单的响应动作（验收标准 4）。"""

    status: LeaderResponseStatus
    remark: str = ""


class TicketStatusView(BaseModel):
    """客服组长处理状态展示（验收标准 5）。"""

    ticket_id: str
    status: TicketStatus
    urgency_level: Optional[UrgencyLevel]
    leader_status: LeaderResponseStatus
    assigned_leader: Optional[str]
    submitted_at: datetime
    classification_completed_at: Optional[datetime]
    notified_at: Optional[datetime]
    leader_response_at: Optional[datetime]
    resolved_at: Optional[datetime]
    classification_duration: Optional[float]
    leader_response_duration: Optional[float]
    response_overdue: bool
