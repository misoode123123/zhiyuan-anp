"""应用入口。

启动时：
1. 初始化示例数据；
2. 挂载需求路由。
"""

from __future__ import annotations

import logging

from fastapi import FastAPI

from .config import settings
from .routers import requirements
from .store import seed_demo_data

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
logger = logging.getLogger(__name__)

seed_demo_data()

app = FastAPI(title=settings.project_name, version=settings.version)

app.include_router(requirements.router)


@app.get("/health", tags=["meta"])
def health() -> dict:
    return {"status": "ok"}
