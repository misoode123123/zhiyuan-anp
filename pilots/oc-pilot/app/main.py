"""应用入口。

启动时：
1. 初始化工作流与示例数据；
2. 启动后台分类循环（验收标准 1：1 分钟内完成分类）；
3. 启动后台 SLA 升级巡检（验收标准 4：5 分钟未响应则升级）；
4. 挂载工单与通知路由。
"""

from __future__ import annotations

import asyncio
import logging
from contextlib import asynccontextmanager
from pathlib import Path

from fastapi import FastAPI
from fastapi.responses import RedirectResponse
from fastapi.staticfiles import StaticFiles

from .config import settings
from .routers import auth, notifications, tickets
from .store import seed_demo_data, store
from .workflow import init_workflow

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s: %(message)s")
logger = logging.getLogger(__name__)

workflow = init_workflow(store)
seed_demo_data()

_STATIC_DIR = Path(__file__).resolve().parent / "static"


@asynccontextmanager
async def lifespan(app: FastAPI):
    """启动后台分类循环（验收标准 1）与 SLA 升级巡检（验收标准 4）。"""
    classification_task = asyncio.create_task(_classification_loop())
    escalation_task = asyncio.create_task(_escalation_loop())
    try:
        yield
    finally:
        classification_task.cancel()
        escalation_task.cancel()


app = FastAPI(title="工单自动分类与通知服务", version="1.0.0", lifespan=lifespan)

app.include_router(tickets.router)
app.include_router(notifications.router)
app.include_router(auth.router)

# 静态资源：登录界面与客服操作界面
app.mount("/static", StaticFiles(directory=str(_STATIC_DIR), html=True), name="static")


@app.get("/", include_in_schema=False)
def root():
    """访问根路径时跳转到登录界面（验收标准 6 的入口）。"""
    return RedirectResponse(url="/static/login.html")


async def _classification_loop() -> None:
    """持续处理待分类工单，确保在 1 分钟 SLA 内完成分类。"""
    while True:
        ticket = store.next_pending_ticket()
        if ticket is not None:
            try:
                workflow.process_one(ticket)
            except Exception:
                logger.exception("工单 %s 分类失败", ticket.id)
        await asyncio.sleep(settings.poll_interval_seconds)


async def _escalation_loop() -> None:
    """巡检已通知工单，超过 5 分钟未响应则自动升级。"""
    while True:
        for ticket in store.list_tickets():
            try:
                workflow.escalate_overdue(ticket)
            except Exception:
                logger.exception("工单 %s 升级巡检失败", ticket.id)
        await asyncio.sleep(settings.poll_interval_seconds)


@app.get("/health", tags=["meta"])
def health() -> dict:
    return {"status": "ok"}
