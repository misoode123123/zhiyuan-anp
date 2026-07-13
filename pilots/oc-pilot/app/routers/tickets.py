"""工单相关接口。"""

from __future__ import annotations

from typing import List, Optional

from fastapi import APIRouter, HTTPException, status

from ..enums import TicketStatus
from ..models import LeaderResponse, Ticket, TicketCreate, TicketStatusView
from ..store import store
from ..workflow import get_workflow

router = APIRouter(prefix="/tickets", tags=["tickets"])


@router.post("", response_model=Ticket, status_code=status.HTTP_201_CREATED)
def submit_ticket(payload: TicketCreate):
    """客户提交工单，进入待分类队列。"""
    customer = store.get_customer(payload.customer_id)
    if customer is None:
        raise HTTPException(status_code=404, detail=f"客户 {payload.customer_id} 不存在")
    ticket = store.create_ticket(payload, customer)
    return ticket


@router.get("", response_model=List[Ticket])
def list_tickets(status_filter: Optional[TicketStatus] = None):
    return store.list_tickets(status_filter)


@router.get("/{ticket_id}", response_model=Ticket)
def get_ticket(ticket_id: str):
    ticket = store.get_ticket(ticket_id)
    if ticket is None:
        raise HTTPException(status_code=404, detail="工单不存在")
    return ticket


@router.get("/{ticket_id}/status", response_model=TicketStatusView)
def get_ticket_status(ticket_id: str):
    """查看客服组长处理状态（验收标准 5）。"""
    ticket = store.get_ticket(ticket_id)
    if ticket is None:
        raise HTTPException(status_code=404, detail="工单不存在")
    return get_workflow().build_status_view(ticket)


@router.post("/{ticket_id}/respond", response_model=Ticket)
def respond_to_ticket(ticket_id: str, response: LeaderResponse):
    """客服组长响应工单（验收标准 4）。"""
    ticket = store.get_ticket(ticket_id)
    if ticket is None:
        raise HTTPException(status_code=404, detail="工单不存在")
    if ticket.status == TicketStatus.SUBMITTED:
        raise HTTPException(status_code=409, detail="工单尚未完成分类，无法响应")
    return get_workflow().respond(ticket, response)
