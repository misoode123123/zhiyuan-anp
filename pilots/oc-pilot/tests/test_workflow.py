"""端到端工作流测试：分类 -> 通知 -> 响应 -> 状态展示。"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from app.enums import LeaderResponseStatus, TicketStatus, UrgencyLevel
from app.main import app


@pytest.fixture(scope="module")
def client() -> TestClient:
    return TestClient(app)


def _submit(client: TestClient, customer_id: str, title: str, body: str) -> dict:
    resp = client.post("/tickets", json={"title": title, "description": body, "customer_id": customer_id})
    assert resp.status_code == 201, resp.text
    return resp.json()


def test_submit_and_auto_classify(client: TestClient) -> None:
    ticket = _submit(client, "C-1001", "系统宕机，无法访问", "生产事故，需立即处理")
    ticket_id = ticket["id"]

    # 触发同步处理（background loop 在 TestClient 中亦可通过 next_pending 调用链完成，
    # 这里直接校验轮询后状态）。等待后台分类完成。
    from app.store import store
    from app.workflow import get_workflow

    pending = store.next_pending_ticket()
    while pending is not None and pending.id != ticket_id:
        get_workflow().process_one(pending)
        pending = store.next_pending_ticket()
    if pending is not None:
        get_workflow().process_one(pending)

    detail = client.get(f"/tickets/{ticket_id}").json()
    assert detail["urgency_level"] == UrgencyLevel.CRITICAL
    assert detail["status"] in (TicketStatus.NOTIFIED, TicketStatus.CLASSIFIED)
    assert detail["classification_completed_at"] is not None
    # 验收标准 1：分类耗时远小于 1 分钟
    assert detail["classification_duration"] <= 60
    assert detail["assigned_leader"] is not None


def test_notification_sent_to_leader(client: TestClient) -> None:
    ticket = _submit(client, "C-1002", "频繁报错", "多人无法下单，严重")
    ticket_id = ticket["id"]

    from app.store import store
    from app.workflow import get_workflow

    pending = store.next_pending_ticket()
    while pending is not None and pending.id != ticket_id:
        get_workflow().process_one(pending)
        pending = store.next_pending_ticket()
    if pending is not None:
        get_workflow().process_one(pending)

    detail = client.get(f"/tickets/{ticket_id}").json()
    leader_id = detail["assigned_leader"]["id"]
    notifications = client.get(f"/notifications?leader_id={leader_id}").json()
    assert any(n["ticket_id"] == ticket_id for n in notifications)

    # 验收标准 3：通知包含紧急程度、客户信息、组长联系方式
    n = next(n for n in notifications if n["ticket_id"] == ticket_id)
    assert n["urgency_level"] is not None
    assert n["customer_name"] and n["customer_contact"]
    assert n["leader_name"] and n["leader_contact"]
    assert n["customer_name"] in n["message"]
    assert n["leader_contact"] in n["message"]


def test_leader_response_records_status(client: TestClient) -> None:
    ticket = _submit(client, "C-1003", "咨询文档", "我想了解如何配置")
    ticket_id = ticket["id"]

    from app.store import store
    from app.workflow import get_workflow

    pending = store.next_pending_ticket()
    while pending is not None and pending.id != ticket_id:
        get_workflow().process_one(pending)
        pending = store.next_pending_ticket()
    if pending is not None:
        get_workflow().process_one(pending)

    # 验收标准 4：客服组长响应工单
    resp = client.post(
        f"/tickets/{ticket_id}/respond",
        json={"status": LeaderResponseStatus.ACCEPTED, "remark": "已接单处理"},
    )
    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["leader_status"] == LeaderResponseStatus.ACCEPTED
    assert body["leader_response_at"] is not None

    # 验收标准 5：状态视图展示处理状态
    status_view = client.get(f"/tickets/{ticket_id}/status").json()
    assert status_view["leader_status"] == LeaderResponseStatus.ACCEPTED
    assert status_view["leader_response_duration"] is not None


def test_get_nonexistent_ticket(client: TestClient) -> None:
    assert client.get("/tickets/nope").status_code == 404
