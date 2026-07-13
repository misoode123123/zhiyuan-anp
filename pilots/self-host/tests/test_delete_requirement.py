"""需求删除功能端到端测试，覆盖验收标准 1-5。"""

from __future__ import annotations

import pytest
from fastapi.testclient import TestClient

from app.main import app
from app.store import store


@pytest.fixture(scope="module")
def client() -> TestClient:
    return TestClient(app)


def _create(client: TestClient, title: str = "测试需求") -> dict:
    resp = client.post(
        "/requirements",
        json={"title": title, "description": "用于删除测试", "priority": "HIGH"},
    )
    assert resp.status_code == 201, resp.text
    return resp.json()


def test_delete_existing_requirement_returns_200(client: TestClient) -> None:
    """验收标准 1、2、3：DELETE 请求，需求 ID 作为路径参数，存在时返回 200 与成功信息。"""
    requirement = _create(client, "待删除需求")

    resp = client.delete(f"/requirements/{requirement['id']}")

    assert resp.status_code == 200, resp.text
    body = resp.json()
    assert body["id"] == requirement["id"]
    assert "成功" in body["message"]

    # 确认已被真正删除
    assert client.get(f"/requirements/{requirement['id']}").status_code == 404


def test_delete_nonexistent_requirement_returns_404(client: TestClient) -> None:
    """验收标准 4：需求不存在时返回 404 与不存在信息。"""
    resp = client.delete("/requirements/R-not-exist")

    assert resp.status_code == 404, resp.text
    detail = resp.json()["detail"]
    assert "不存在" in detail


def test_delete_requirement_returns_500_on_internal_error(
    client: TestClient, monkeypatch: pytest.MonkeyPatch
) -> None:
    """验收标准 5：删除过程发生内部错误时返回 500 与内部服务器错误信息。"""
    requirement = _create(client, "触发异常的需求")

    original_delete = store.delete_requirement

    def _boom(_requirement_id: str):
        raise RuntimeError("模拟存储层故障")

    # 仅破坏 delete 操作，get 仍正常以通过存在性检查
    monkeypatch.setattr(store, "delete_requirement", _boom)
    try:
        resp = client.delete(f"/requirements/{requirement['id']}")
    finally:
        monkeypatch.setattr(store, "delete_requirement", original_delete)

    assert resp.status_code == 500, resp.text
    detail = resp.json()["detail"]
    assert "内部服务器错误" in detail

    # 恢复后手动清理
    store.delete_requirement(requirement["id"])
