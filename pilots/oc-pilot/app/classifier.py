"""紧急程度分类器。

验收标准 1: 系统应在客户提交工单后 1 分钟内自动完成紧急程度分类。

实现采用基于规则的关键词评分法：
1. 对标题与正文进行关键词命中统计，标题权重更高；
2. 结合客户等级（VIP 提升一档）得出最终紧急程度；
3. 确定紧急程度后路由到对应的客服组长。

该算法为纯 CPU 计算，可在毫秒级完成，远低于 1 分钟 SLA。
"""

from __future__ import annotations

import re
from typing import Dict, List, Optional, Tuple

from .enums import UrgencyLevel
from .models import TeamLeader, Ticket
from .store import DataStore


# 各等级关键词表。命中即倾向该等级。
_KEYWORDS: Dict[UrgencyLevel, List[str]] = {
    UrgencyLevel.CRITICAL: [
        "宕机", "瘫痪", "无法访问", "系统崩溃", "数据丢失", "数据泄露",
        "安全漏洞", "被攻击", "黑客", "支付失败", "资金损失", "紧急", "立即",
        "马上", "事故", "全站", "生产事故", "outage", "down",
    ],
    UrgencyLevel.HIGH: [
        "投诉", "退款", "逾期", "严重", "影响", "多人", "批量", "无法下单",
        "功能不可用", "报错", "异常", "频繁", "超时", "客户流失",
    ],
    UrgencyLevel.LOW: [
        "咨询", "建议", "文档", "了解", "请问", "如何", "能否", "希望",
        "优化", "体验", "想知道",
    ],
    # MEDIUM 作为兜底，无显式关键词
    UrgencyLevel.MEDIUM: [],
}

TITLE_WEIGHT = 3  # 标题命中权重
BODY_WEIGHT = 1   # 正文命中权重


def _count_hits(text: str, keywords: List[str]) -> int:
    """统计文本中各关键词出现次数（按词去重）。"""
    hits = 0
    for kw in keywords:
        if re.search(re.escape(kw), text, flags=re.IGNORECASE):
            hits += 1
    return hits


def _score_text(title: str, body: str) -> Dict[UrgencyLevel, int]:
    scores: Dict[UrgencyLevel, int] = {level: 0 for level in UrgencyLevel}
    for level, keywords in _KEYWORDS.items():
        if not keywords:
            continue
        scores[level] += TITLE_WEIGHT * _count_hits(title, keywords)
        scores[level] += BODY_WEIGHT * _count_hits(body, keywords)
    return scores


class UrgencyClassifier:
    """紧急程度分类器，负责对单张工单评分并指派客服组长。"""

    # 紧急程度 -> 默认负责的客服组长 ID
    LEADER_ROUTING: Dict[UrgencyLevel, str] = {
        UrgencyLevel.CRITICAL: "L-CRITICAL",
        UrgencyLevel.HIGH: "L-HIGH",
        UrgencyLevel.MEDIUM: "L-STANDARD",
        UrgencyLevel.LOW: "L-STANDARD",
    }

    def __init__(self, store: DataStore) -> None:
        self._store = store

    def classify(self, ticket: Ticket) -> Tuple[UrgencyLevel, Optional[TeamLeader]]:
        """对工单进行紧急程度分类，返回 (紧急程度, 指派客服组长)。"""
        scores = _score_text(ticket.title, ticket.description)

        # 选择得分最高的等级；CRITICAL 因权重高会自然胜出。
        best_level = max(scores, key=lambda lvl: scores[lvl])
        if scores[best_level] == 0:
            best_level = UrgencyLevel.MEDIUM  # 无命中则中等

        # VIP 客户提升一档
        if ticket.customer.is_vip and best_level < UrgencyLevel.CRITICAL:
            best_level = UrgencyLevel(best_level + 1)

        leader = self._route_to_leader(best_level)
        return best_level, leader

    def _route_to_leader(self, level: UrgencyLevel) -> Optional[TeamLeader]:
        leader_id = self.LEADER_ROUTING.get(level)
        if leader_id is None:
            return None
        leader = self._store.get_leader(leader_id)
        if leader is not None:
            return leader
        # 路由目标不存在时回退到任意一位在册组长
        leaders = self._store.all_leaders()
        return leaders[0] if leaders else None
