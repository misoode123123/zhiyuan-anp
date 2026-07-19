"""智源 ANP AI 运行时入口（FastAPI + Uvicorn）。"""

import logging

import uvicorn
from fastapi import FastAPI
from fastapi.middleware.cors import CORSMiddleware

from .config import settings
from .routes import router

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(levelname)s %(name)s %(message)s")
logger = logging.getLogger("agent-runtime")

app = FastAPI(title="智源 ANP Agent Runtime", version="0.1.0")
app.add_middleware(
    CORSMiddleware,
    allow_origins=["http://localhost:3000"],
    allow_methods=["*"],
    allow_headers=["*"],
)
app.include_router(router)


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
