"""登录鉴权相关接口。

验收标准对照：
- 4. 系统应验证用户名和密码的正确性 → ``POST /auth/login`` 校验账号与密码哈希
- 5. 登录失败时提供明确的错误信息 → 校验失败返回 401 ``用户名或密码错误``
- 6. 登录成功后返回令牌，前端据此跳转客服操作界面 → ``LoginResponse``
- 7. 保证用户信息安全性 → 密码哈希校验 + 令牌鉴权
"""

from __future__ import annotations

from typing import Optional

from fastapi import APIRouter, Depends, Header, HTTPException, status

from ..config import settings
from ..models import AgentInfo, LoginRequest, LoginResponse
from ..security import create_token, decode_token, verify_password
from ..store import store

router = APIRouter(prefix="/auth", tags=["auth"])


def _to_info(agent) -> AgentInfo:
    return AgentInfo(
        id=agent.id,
        username=agent.username,
        full_name=agent.full_name,
        department=agent.department,
        role=agent.role,
    )


def get_current_agent_id(authorization: Optional[str] = Header(default=None)) -> str:
    """从 ``Authorization: Bearer <token>`` 中解析当前客服人员 ID。"""
    if not authorization:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="未提供身份凭证，请先登录")
    parts = authorization.split(" ", 1)
    if len(parts) != 2 or parts[0].lower() != "bearer":
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="凭证格式错误，请重新登录")
    agent_id = decode_token(parts[1])
    if agent_id is None:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="登录已失效，请重新登录")
    agent = store.get_agent(agent_id)
    if agent is None:
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="账号不存在，请重新登录")
    return agent_id


@router.post("/login", response_model=LoginResponse)
def login(payload: LoginRequest):
    """客服人员登录（验收标准 2、4、5、6）。"""
    agent = store.get_agent_by_username(payload.username)
    # 验收标准 4: 校验用户名存在且密码匹配；验收标准 7: 恒定时间比对
    if agent is None or not agent.password_hash or not verify_password(payload.password, agent.password_hash):
        # 验收标准 5: 明确但不泄露「用户名」与「密码」哪一个错误
        raise HTTPException(status_code=status.HTTP_401_UNAUTHORIZED, detail="用户名或密码错误")
    # 验收标准 6: 签发令牌
    token = create_token(agent.id)
    return LoginResponse(
        token=token,
        expires_in=settings.token_expire_seconds,
        agent=_to_info(agent),
    )


@router.get("/me", response_model=AgentInfo)
def current_agent(agent_id: str = Depends(get_current_agent_id)) -> AgentInfo:
    """获取当前登录客服人员信息（需携带令牌）。"""
    agent = store.get_agent(agent_id)
    return _to_info(agent)
