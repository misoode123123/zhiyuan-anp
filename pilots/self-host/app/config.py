"""应用全局配置。"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Settings:
    project_name: str = "需求管理服务"
    version: str = "1.0.0"


settings = Settings()
