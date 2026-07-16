# 智源 ANP 生产部署手册（10.10.0.28）

> 本手册记录**实际验证过**的部署流程（已在 .28 上线几十次），供后续维护不遗忘。
> 增量更新请直接看「二、增量部署」；首次装新服务器看「三、首次全新部署」。

---

## 一、生产环境一览

| 项 | 值 |
|---|---|
| 服务器 | **10.10.0.28**（⚠️ 共享服务器，还跑 lowcode/帆软/腾讯微搭等，**只动 `deploy_` 前缀容器**） |
| 平台入口 | **http://10.10.0.28:8088**（nginx 暴露 :8088；公网 IP 不通，只能内网/SSH 隧道） |
| 登录 | `admin / admin123`（真实账号密码 + token 鉴权） |
| 源码位置 | `.28:/opt/anp/`（tar 包解压，**非 git 仓库**） |
| 编排 | `/opt/anp/deploy/docker-compose.prod.yml`（用 `docker-compose` v1，非 `docker compose`） |
| 容器 | `deploy_backend_1` / `deploy_frontend_1` / `deploy_agent-runtime_1` / `deploy_nginx_1` |
| 端口段 | 平台 `8088`；产出应用 test `9100-9199` / prod `9200-9300`；opencode 编码工作台 `9400-9450` |
| 生产库 | `/opt/anp/data/anp.db`（SQLite） |
| 密钥 | `/opt/anp/deploy/.env.prod`（含 `ZHIPUAI_API_KEY`、`APPDEPLOY_HOST=10.10.0.28` 等） |

---

## 二、远程访问（打开慢 / 不在内网的解决）

opencode 编码工作台前端 JS 约 **2.8MB**，且 opencode 官方 web UI **不对静态资源做 gzip 压缩**——
同局域网秒开（.28 内网约 10ms），但**远程 / 窄带宽 / 经公网就会很慢**。

> 工作台 URL 形如 `http://10.10.0.28:9400`，端口 **9400-9450 动态分配**（每应用一个）。
> 因此 `-L` 单端口映射覆盖不了，**远程推荐用 SSH 动态代理（SOCKS）**。

### 方案 A：SSH 动态代理（推荐——覆盖所有端口 + 压缩）

本地终端起一条压缩隧道（保持窗口开着）：
```bash
ssh -C -D 1080 -N -i ~/.ssh/miscode root@10.10.0.28
```
- `-C` 启用压缩 → 2.8MB JS 经压缩约 **→ 600KB**
- `-D 1080` 本地 SOCKS5 代理（任意端口都走它，含动态的 9400-9450）
- `-N` 不开远端 shell

浏览器配 **SOCKS5 代理 `127.0.0.1:1080`**（Chrome 装 SwitchyOmega，或系统代理），
然后正常访问 `http://10.10.0.28:8088`，点「🧑‍💻编码」打开的工作台也走压缩隧道，不再慢。

### 方案 B：同局域网直连（不慢就别折腾）

与 .28 同网段：浏览器直接开 `http://10.10.0.28:8088`，工作台 `http://10.10.0.28:<port>` 直达，无需隧道。

> ⚠️ 不要用 `-L 8088:localhost:8088` 单端口映射：平台能开，但点「编码」弹出的工作台 URL 仍是
> `10.10.0.28:9400`，你的 `-L` 没映射 9400 → 打不开。要么用方案 A（SOCKS），要么把 9400-9450 也 `-L` 逐个映射。

---

## 三、前置条件（一次性配置）

- 本机私钥 `~/.ssh/miscode` 已加入 .28 root 的 `authorized_keys`（**免密 keyless**）。
- 本机**无 Docker**，靠 SSH 驱动远端 Docker。
- 验证连通：
  ```bash
  ssh -i ~/.ssh/miscode root@10.10.0.28 "echo OK; docker ps --filter name=deploy_ --format '{{.Names}} | {{.Status}}'"
  ```
  若某天 keyless 失效（如 .28 重装/换密钥），用密码重装公钥：
  ```bash
  ssh-copy-id -i ~/.ssh/miscode.pub root@10.10.0.28   # 会提示输一次 root 密码
  ```

---

## 四、增量部署（改代码后推送，最常用）

> 原则：**本机改代码 → scp 同步到 /opt/anp → 远端重建受影响容器 → 验证**。

### 方式 A：少量文件改动（快，推荐）

```bash
SSH="ssh -i $HOME/.ssh/miscode root@10.10.0.28"
SCP="scp -i $HOME/.ssh/miscode root@10.10.0.28"

# 1) 逐文件同步（目标路径与仓库结构一致，根为 /opt/anp）
$SCP platform/backend/internal/xxx.go root@10.10.0.28:/opt/anp/platform/backend/internal/xxx.go

# 2) 重建受影响的容器
$SSH "cd /opt/anp && docker-compose -f deploy/docker-compose.prod.yml up --build -d backend"
```

- **仅后端**改动（go/internal/cmd、config、opencode.json）→ 重建 `backend`
- **含前端**改动 → 重建 `frontend`（耗时几分钟，建议后台跑）：
  ```bash
  $SSH "cd /opt/anp && nohup docker-compose -f deploy/docker-compose.prod.yml up --build -d frontend >/tmp/fe.log 2>&1 &"
  ```
- **compose / Dockerfile / nginx.conf** 改动 → 重建对应服务；nginx 改完记得 `docker restart deploy_nginx_1`

### 方式 B：大量改动（全量 tar 同步）

```bash
cd <仓库根>
tar --force-local -czf /tmp/anp.tar.gz \
  --exclude=node_modules --exclude=.next --exclude=.git --exclude=tmp \
  --exclude='*.exe' --exclude=.claude --exclude=data --exclude='deploy/.env.prod' .
scp  -i ~/.ssh/miscode /tmp/anp.tar.gz root@10.10.0.28:/root/
ssh  -i ~/.ssh/miscode root@10.10.0.28 "tar -xzf /root/anp.tar.gz -C /opt/anp && rm -f /root/anp.tar.gz"
# 再按方式 A 第 2 步重建容器
```

> ⚠️ **必须排除 `data/` 和 `deploy/.env.prod`**——否则会覆盖生产 SQLite 库与密钥。

---

## 五、首次全新部署（新服务器）

仓库自带 `deploy/deploy-centos.sh`（在**服务器上**项目根目录跑，需服务器已装 Docker + Docker Compose）：

```bash
# 在新服务器上
git clone <repo> /opt/anp && cd /opt/anp
cp deploy/.env.prod.example deploy/.env.prod   # 填入 ZHIPUAI_API_KEY 等
bash deploy/deploy-centos.sh
```

> 注：当前 .28 实际走的是「三、增量部署」链路（本机无 Docker、SSH 驱动远端），`deploy-centos.sh` 适用于首次干净装机。

---

## 六、部署后验证（必做）

```bash
# 1) 容器都在跑
ssh -i ~/.ssh/miscode root@10.10.0.28 "docker ps --filter name=deploy_ --format '{{.Names}} | {{.Status}}'"

# 2) 登录拿 token + 业务接口 200
TOK=$(curl -s -X POST http://10.10.0.28:8088/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"name":"admin","password":"admin123"}' \
  | python -c "import sys,json;print(json.load(sys.stdin)['data']['token'])")
curl -s -o /dev/null -w "spaces: %{http_code}\n" -H "Authorization: Bearer $TOK" http://10.10.0.28:8088/api/v1/project-spaces
```

- 涉及**前端**改动：浏览器开 http://10.10.0.28:8088 ，**Ctrl+Shift+R 强刷**（否则缓存旧 bundle）。
- 涉及 **opencode 编码工作台**：某应用点「🧑‍💻编码」，确认 `http://10.10.0.28:9400` 打开不卡转圈（serve 已加载 provider）。

---

## 七、保护事项（部署时勿碰）

| 资产 | 路径 | 保护方式 |
|---|---|---|
| 生产数据库 | `/opt/anp/data/anp.db` | 全量同步排除 `data/`；迁移用幂等 `ALTER`（addColumnIfMissing） |
| 密钥 | `/opt/anp/deploy/.env.prod` | 全量同步排除 `deploy/.env.prod` |
| 他人容器 | lowcode/帆软/腾讯等 | `docker` 操作只认 `deploy_` 前缀，别 `docker rm -f` 陌生容器 |

---

## 八、历史踩坑（避免重蹈覆辙）

| 坑 | 现象 | 解法（已修入仓库） |
|---|---|---|
| 前端 `--frozen-lockfile` | 构建报 `ERR_PNPM_OUTDATED_LOCKFILE` | `Dockerfile.frontend` 用 `--no-frozen-lockfile` |
| 后端 apk 装包卡死 | `docker build` 9min+ 无响应 | `Dockerfile.backend` `sed` 换阿里云 alpine 源 |
| nginx 重建后 502 | 重建 backend 后接口 502（IP 变了） | `nginx.conf` 加 `resolver 127.0.0.11 valid=10s` |
| `repo_dir` 路径 | build 找不到上下文 | repo_dir 用**容器内**路径 `/data/repos/x`，非宿主路径 |
| opencode 工作台卡转圈 | serve 无 provider | serve 只读 `$HOME/.config/opencode/opencode.json`；backend 启动时把 `opencode.json` 复制过去（见 `main.go`） |
| buildpack 空仓库/端口 | 应用白屏 | 空仓库兜底 `static`；static 类型 nginx `listen` 指定端口 |

---

## 九、回滚

- **后端/前端**：代码层 `git checkout <旧版>` 重新 scp + 重建；镜像有历史层可指定旧 context。
- **产出应用实例**：平台「应用详情 → 版本历史」按 commit 回滚（`POST /apps/:id/deploy-commit {sha}`）。

---

## 十、关键路径速查

```
SSH       : ssh -i ~/.ssh/miscode root@10.10.0.28
源码      : /opt/anp
编排      : /opt/anp/deploy/docker-compose.prod.yml
重建命令  : cd /opt/anp && docker-compose -f deploy/docker-compose.prod.yml up --build -d <backend|frontend|...>
生产库    : /opt/anp/data/anp.db
密钥      : /opt/anp/deploy/.env.prod
平台入口  : http://10.10.0.28:8088     登录 admin / admin123
```
