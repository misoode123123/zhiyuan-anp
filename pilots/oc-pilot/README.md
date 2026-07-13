# 工单自动分类与通知服务

实现「工单自动分类与通知」需求：客户提交工单后，系统自动按紧急程度分类，并通知对应客服组长，同时跟踪响应与处理状态。

## 技术栈

Python 3.10+ / FastAPI / Pydantic v2

## 验收标准与实现对照

| # | 验收标准 | 实现位置 |
|---|---------|---------|
| 1 | 提交后 1 分钟内完成紧急程度分类 | `app/workflow.py::_classification_loop` + `app/classifier.py`（关键词评分，毫秒级完成） |
| 2 | 分类结果以通知形式发送至对应客服组长系统通知栏 | `app/notification.py` + `app/routers/notifications.py` |
| 3 | 通知含紧急程度、客户信息、组长联系方式 | `app/models.py::Notification` 字段与 `message` 拼装 |
| 4 | 客服组长 5 分钟内响应，超时升级 | `app/workflow.py::respond/escalate_overdue` + `app/config.py` |
| 5 | 记录并展示客服组长处理状态 | `GET /tickets/{id}/status` → `TicketStatusView` |

## 目录结构

```
app/
  config.py        SLA 阈值与登录安全配置
  enums.py         紧急程度、工单状态、组长处理状态
  models.py        领域模型与 DTO（含 Agent、LoginRequest/Response）
  store.py         线程安全内存存储（工单/客户/组长/通知/客服账号）
  security.py      密码哈希（PBKDF2）与令牌签发/校验
  classifier.py    紧急程度分类器（关键词评分 + VIP 提升）
  notification.py  通知构造与发送
  workflow.py      编排：分类→通知→响应→状态展示
  routers/         tickets、notifications、auth 路由
  static/          登录界面 login.html、客服操作界面 console.html
  main.py          入口、后台分类循环与 SLA 巡检
tests/             分类器、端到端工作流与登录模块测试
```

## 分类规则

- 关键词命中评分：标题权重 ×3，正文权重 ×1。
- 等级：`CRITICAL`(宕机/数据丢失/支付失败/安全…) > `HIGH`(投诉/退款/报错…) > `MEDIUM`(默认) > `LOW`(咨询/建议…)。
- VIP 客户自动提升一档（最高到 CRITICAL）。
- 按等级路由到对应客服组长（CRITICAL→紧急组、HIGH→高级组、MEDIUM/LOW→常规组）。

## 快速开始

```bash
pip install -r requirements.txt
uvicorn app.main:app --reload
# 接口文档：http://127.0.0.1:8000/docs
# 登录界面：http://127.0.0.1:8000/
```

预置数据：3 位客服组长（`L-CRITICAL`/`L-HIGH`/`L-STANDARD`）与 3 位客户（`C-1001`~`C-1003`，其中 `C-1001` 为 VIP）。
客服登录账号：用户名 `agent001` / 密码 `Agent@2024`。

## 客服系统登录

| # | 验收标准 | 实现位置 |
|---|---------|---------|
| 1 | 登录界面含用户名和密码输入框 | `app/static/login.html` + `app/models.py::LoginRequest` |
| 2 | 登录按钮清晰可见并允许提交 | `login.html` 登录按钮 → `POST /auth/login` |
| 3 | 输入框提供提示信息 | `login.html` placeholder 与字段描述 |
| 4 | 验证用户名和密码正确性 | `app/routers/auth.py::login` + `app/security.py::verify_password` |
| 5 | 登录失败提供明确错误信息 | 登录失败返回 401 `用户名或密码错误` |
| 6 | 登录成功后跳转客服操作界面 | 返回令牌 → `console.html` |
| 7 | 保证用户信息安全性 | PBKDF2 加盐哈希、HMAC 签名令牌、恒定时间比较、密码哈希不序列化 |

```bash
# 登录获取令牌
curl -X POST :8000/auth/login -H 'Content-Type: application/json' \
  -d '{"username":"agent001","password":"Agent@2024"}'

# 携带令牌访问受保护接口
curl :8000/auth/me -H 'Authorization: Bearer <token>'
```

### 接口示例

```bash
# 1. 提交工单
curl -X POST :8000/tickets -H 'Content-Type: application/json' \
  -d '{"title":"系统宕机无法访问","description":"生产事故，数据丢失","customer_id":"C-1001"}'

# 2. 查看工单分类结果（约 1 秒内完成）
curl :8000/tickets/T-xxxxxxxxxxxx

# 3. 查询组长通知栏
curl :8000/notifications?leader_id=L-CRITICAL

# 4. 组长响应
curl -X POST :8000/tickets/T-xxxxxxxxxxxx/respond -H 'Content-Type: application/json' \
  -d '{"status":"accepted","remark":"已接单处理"}'

# 5. 查看处理状态
curl :8000/tickets/T-xxxxxxxxxxxx/status
```

## 测试

```bash
python -m pytest -q
```

覆盖分类器各级别判定、VIP 提升、端到端分类+通知+响应+状态展示，以及 1 分钟分类 SLA 校验。

## 备注

- 当前使用内存存储便于演示，`app/store.py` 定义了统一接口，可替换为持久化数据库实现。
- 分类与 SLA 升级通过后台任务循环驱动，确保 1 分钟分类 SLA 与 5 分钟响应 SLA 持续满足。
