"""配置：从环境变量读取（加载同目录 .env）。"""

import os
from dataclasses import dataclass

from dotenv import load_dotenv

load_dotenv()


@dataclass(frozen=True)
class Settings:
    env: str = os.getenv("ENV", "dev")
    port: int = int(os.getenv("AGENT_RUNTIME_PORT", "8001"))
    default_model: str = os.getenv("DEFAULT_MODEL", "zhipu/glm-4-flash")


settings = Settings()
