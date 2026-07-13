"""安全模块。

验收标准 7: 系统应保证用户信息的安全性。

实现要点：
- 密码使用 PBKDF2-HMAC-SHA256 加盐哈希存储，永不保存明文；
- 登录令牌（token）采用 HMAC-SHA256 签名、自带过期时间，无状态可校验；
- 密码比对使用恒定时间比较（``hmac.compare_digest``），防范计时攻击。
"""

from __future__ import annotations

import base64
import hashlib
import hmac
import json
import secrets
import time
from typing import Optional

from .config import settings

_ALGO = "pbkdf2_sha256"
_SALT_BYTES = 16


def hash_password(password: str) -> str:
    """对明文密码进行加盐哈希，返回形如 ``pbkdf2_sha256$<iter>$<salt>$<hash>`` 的字符串。"""
    salt = secrets.token_bytes(_SALT_BYTES)
    iterations = settings.pbkdf2_iterations
    derived = hashlib.pbkdf2_hmac("sha256", password.encode("utf-8"), salt, iterations)
    return (
        f"{_ALGO}${iterations}"
        f"${base64.b64encode(salt).decode('ascii')}"
        f"${base64.b64encode(derived).decode('ascii')}"
    )


def verify_password(password: str, stored: str) -> bool:
    """恒定时间校验明文密码是否与已存储的哈希匹配。"""
    try:
        algo, iterations_str, salt_b64, hash_b64 = stored.split("$", 3)
    except ValueError:
        return False
    if algo != _ALGO:
        return False
    salt = base64.b64decode(salt_b64)
    expected = base64.b64decode(hash_b64)
    derived = hashlib.pbkdf2_hmac("sha256", password.encode("utf-8"), salt, int(iterations_str))
    return hmac.compare_digest(derived, expected)


def create_token(agent_id: str) -> str:
    """生成带签名与过期时间的登录令牌（验收标准 6 登录成功后返回）。"""
    payload = {"sub": agent_id, "exp": int(time.time()) + settings.token_expire_seconds}
    raw = base64.urlsafe_b64encode(json.dumps(payload, separators=(",", ":")).encode("utf-8")).decode("ascii")
    sig = hmac.new(settings.secret_key.encode("utf-8"), raw.encode("ascii"), hashlib.sha256).hexdigest()
    return f"{raw}.{sig}"


def decode_token(token: str) -> Optional[str]:
    """校验令牌签名与有效期，成功返回客服人员 ID，否则返回 ``None``。"""
    try:
        raw, sig = token.split(".", 1)
    except ValueError:
        return None
    expected_sig = hmac.new(settings.secret_key.encode("utf-8"), raw.encode("ascii"), hashlib.sha256).hexdigest()
    if not hmac.compare_digest(sig, expected_sig):
        return None
    try:
        payload = json.loads(base64.urlsafe_b64decode(raw.encode("ascii")))
        agent_id = payload["sub"]
        exp = int(payload["exp"])
    except Exception:
        return None
    if time.time() > exp:
        return None
    return agent_id
