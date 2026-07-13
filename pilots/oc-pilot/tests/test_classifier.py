"""紧急程度分类器单元测试。"""

from __future__ import annotations

import pytest

from app.classifier import UrgencyClassifier
from app.enums import UrgencyLevel
from app.models import Customer, TeamLeader, Ticket
from app.store import DataStore


@pytest.fixture
def store() -> DataStore:
    s = DataStore()
    s.upsert_leader(TeamLeader(id="L-CRITICAL", name="王", contact="1381"))
    s.upsert_leader(TeamLeader(id="L-HIGH", name="李", contact="1382"))
    s.upsert_leader(TeamLeader(id="L-STANDARD", name="赵", contact="1383"))
    return s


def _ticket(store: DataStore, title: str, body: str = "", vip: bool = False) -> Ticket:
    customer = Customer(id="C1", name="客户", contact="1390", is_vip=vip)
    return Ticket(id="T1", title=title, description=body, customer_id="C1", customer=customer)


def test_critical_keywords(store: DataStore) -> None:
    clf = UrgencyClassifier(store)
    t = _ticket(store, "系统宕机，无法访问", "生产事故，需要立即处理")
    level, leader = clf.classify(t)
    assert level == UrgencyLevel.CRITICAL
    assert leader.id == "L-CRITICAL"


def test_high_keywords(store: DataStore) -> None:
    clf = UrgencyClassifier(store)
    t = _ticket(store, "频繁报错", "多人无法下单，严重")
    level, _ = clf.classify(t)
    assert level == UrgencyLevel.HIGH


def test_low_keywords(store: DataStore) -> None:
    clf = UrgencyClassifier(store)
    t = _ticket(store, "咨询文档", "我想了解一下如何配置")
    level, _ = clf.classify(t)
    assert level == UrgencyLevel.LOW


def test_default_medium(store: DataStore) -> None:
    clf = UrgencyClassifier(store)
    t = _ticket(store, "普通问题", "某个界面显示有点问题")
    level, _ = clf.classify(t)
    assert level == UrgencyLevel.MEDIUM


def test_vip_boosts_urgency(store: DataStore) -> None:
    clf = UrgencyClassifier(store)
    t = _ticket(store, "普通问题", "某个界面显示有点问题", vip=True)
    level, _ = clf.classify(t)
    assert level == UrgencyLevel.HIGH


def test_vip_high_stays_below_critical(store: DataStore) -> None:
    clf = UrgencyClassifier(store)
    t = _ticket(store, "频繁报错", "严重", vip=True)
    level, _ = clf.classify(t)
    assert level == UrgencyLevel.CRITICAL
