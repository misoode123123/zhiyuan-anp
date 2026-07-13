"""应用全局配置与 SLA 阈值。"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    # 验收标准 1: 提交后 1 分钟内完成分类
    classification_sla_seconds: int = 60
    # 验收标准 4: 客服组长 5 分钟内响应
    leader_response_sla_seconds: int = 300
    # 紧急程度分类任务的轮询间隔（秒）
    poll_interval_seconds: float = 1.0
    # 登录模块（验收标准 7: 保证用户信息安全性）
    # 登录令牌签名密钥；生产环境应通过环境变量 SECRET_KEY 注入
    secret_key: str = "CHANGE_ME_IN_PRODUCTION_dev_only_secret_key"
    # 登录令牌有效期（秒），默认 8 小时
    token_expire_seconds: int = 8 * 60 * 60
    # 密码哈希迭代次数（PBKDF2-HMAC-SHA256）
    pbkdf2_iterations: int = 200_000


settings = Settings()
