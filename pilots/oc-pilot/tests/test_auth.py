"""登录模块测试。

验收标准对照：
- 1/2/3 界面字段与提示 → 通过请求体校验
- 4 用户名与密码校验 → test_login_success / test_login_wrong_password
- 5 失败明确错误信息 → test_login_*_fails
- 6 成功返回令牌并可用于受保护接口 → test_token_works
- 7 用户信息安全 → 密码不出现在响应中、无效/过期令牌被拒
"""

from __future__ import annotations

import time

import pytest
from fastapi.testclient import TestClient

from app.config import settings
from app.main import app
from app.security import create_token, decode_token, hash_password, verify_password


@pytest.fixture(scope="module")
def client() -> TestClient:
    return TestClient(app)


# --------------------------------------------------------------------------- #
# 纯函数：密码哈希与令牌
# --------------------------------------------------------------------------- #
def test_password_hash_and_verify() -> None:
    raw = "S3cret!_pass"
    stored = hash_password(raw)
    # 验收标准 7: 哈希值不含明文
    assert raw not in stored
    assert verify_password(raw, stored)
    assert not verify_password(raw + "x", stored)
    # 每次加盐不同
    assert hash_password(raw) != stored


def test_token_roundtrip() -> None:
    token = create_token("A-1001")
    assert decode_token(token) == "A-1001"
    # 篡改后校验失败
    assert decode_token(token + "x") is None
    assert decode_token("garbage") is None


def test_token_expiry() -> None:
    # 直接构造一个已过期令牌
    import base64
    import hashlib
    import hmac
    import json

    payload = {"sub": "A-1001", "exp": int(time.time()) - 1}
    raw = base64.urlsafe_b64encode(json.dumps(payload).encode()).decode()
    sig = hmac.new(settings.secret_key.encode(), raw.encode(), hashlib.sha256).hexdigest()
    expired = f"{raw}.{sig}"
    assert decode_token(expired) is None


# --------------------------------------------------------------------------- #
# 登录接口
# --------------------------------------------------------------------------- #
def test_login_success(client: TestClient) -> None:
    # 验收标准 4: 正确用户名与密码通过校验
    resp = client.post("/auth/login", json={"username": "agent001", "password": "Agent@2024"})
    assert resp.status_code == 200, resp.text
    body = resp.json()
    # 验收标准 6: 返回令牌
    assert body["token"]
    assert body["token_type"] == "Bearer"
    assert body["expires_in"] == settings.token_expire_seconds
    # 验收标准 7: 响应中绝不包含密码哈希
    assert "password_hash" not in body["agent"]
    assert "password" not in str(body)
    assert body["agent"]["username"] == "agent001"


def test_login_wrong_password_fails(client: TestClient) -> None:
    # 验收标准 5: 明确错误信息
    resp = client.post("/auth/login", json={"username": "agent001", "password": "wrong"})
    assert resp.status_code == 401
    assert resp.json()["detail"] == "用户名或密码错误"


def test_login_unknown_user_fails(client: TestClient) -> None:
    # 验收标准 5: 明确错误信息，且不泄露用户名是否存在
    resp = client.post("/auth/login", json={"username": "nobody", "password": "whatever"})
    assert resp.status_code == 401
    assert resp.json()["detail"] == "用户名或密码错误"


def test_login_empty_fields_rejected(client: TestClient) -> None:
    # 验收标准 1/3: 用户名与密码均不可为空
    resp = client.post("/auth/login", json={"username": "", "password": ""})
    assert resp.status_code == 422


# --------------------------------------------------------------------------- #
# 受保护接口 /auth/me
# --------------------------------------------------------------------------- #
def test_token_works_for_me(client: TestClient) -> None:
    login = client.post("/auth/login", json={"username": "agent001", "password": "Agent@2024"})
    token = login.json()["token"]
    resp = client.get("/auth/me", headers={"Authorization": f"Bearer {token}"})
    assert resp.status_code == 200
    assert resp.json()["username"] == "agent001"
    assert "password_hash" not in resp.text


def test_me_requires_token(client: TestClient) -> None:
    assert client.get("/auth/me").status_code == 401


def test_me_rejects_invalid_token(client: TestClient) -> None:
    resp = client.get("/auth/me", headers={"Authorization": "Bearer not.a.valid.token"})
    assert resp.status_code == 401
