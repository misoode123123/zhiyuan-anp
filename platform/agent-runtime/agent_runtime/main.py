"""智源 ANP AI 运行时入口（FastAPI + Uvicorn）。

P1 统一日志：ERROR 自动回传后端 platform_log。
"""

import logging
import os
import traceback

import requests
import uvicorn
from fastapi import FastAPI, Request
from fastapi.middleware.cors import CORSMiddleware
from fastapi.responses import JSONResponse

from .config import settings
from .routes import router

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
logger = logging.getLogger("agent-runtime")

BACKEND_LOG_URL = os.getenv("BACKEND_LOG_URL", "http://backend:8080/api/v1/logs")


class BackendLogHandler(logging.Handler):
    """ERROR 级别日志回传到后端 platform_log。"""

    def emit(self, record):
        if record.levelno < logging.ERROR:
            return
        try:
            stack = ""
            if record.exc_info:
                stack = "".join(traceback.format_exception(*record.exc_info))
            requests.post(
                BACKEND_LOG_URL,
                json={
                    "level": record.levelname,
                    "source": "agent-runtime",
                    "message": record.getMessage(),
                    "fields": {"stack": stack, "module": record.name},
                },
                timeout=5,
            )
        except Exception:  # noqa: S110  # 日志回传失败不影响业务，handler 内不递归记日志
            pass


logging.getLogger().addHandler(BackendLogHandler())

app = FastAPI(title="智源 ANP Agent Runtime", version="0.1.0")
app.add_middleware(
    CORSMiddleware,
    allow_origins=["http://localhost:3000"],
    allow_methods=["*"],
    allow_headers=["*"],
)
app.include_router(router)


@app.exception_handler(Exception)
async def global_exception_handler(request: Request, exc: Exception):
    """未捕获异常 → 记 ERROR（触发 BackendLogHandler 回传）+ 返回 500。"""
    logger.error("Unhandled exception: %s", exc, exc_info=True)
    return JSONResponse(status_code=500, content={"error": str(exc)})


@app.get("/healthz")
async def health() -> dict:
    return {"status": "ok", "service": "agent-runtime", "model": settings.default_model}


def main() -> None:
    logger.info(
        "agent-runtime starting on :%s (env=%s, model=%s)",
        settings.port,
        settings.env,
        settings.default_model,
    )
    uvicorn.run("agent_runtime.main:app", host="0.0.0.0", port=settings.port, reload=False)


if __name__ == "__main__":
    main()
