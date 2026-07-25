#!/usr/bin/env bash
# 智源 ANP — 一键推生产（本地打包 → SFTP 上传 → 解压 → docker-compose 重建 → 验证）
# 用法: bash scripts/push-prod.sh [文件...]
#   无参数 = 全量推送
#   有参数 = 只传指定文件（快速修复单个文件）
#
# 依赖: Python paramiko（pip install paramiko）、docker-compose 在 .28 已装
set -euo pipefail

HOST="10.10.0.28"
USER="root"
PASS="00##88aa"
REMOTE_DIR="/opt/anp"
COMPOSE_FILE="deploy/docker-compose.prod.yml"
ENV_FILE="deploy/.env.prod"
LOCAL_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# 颜色
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[✓]${NC} $1"; }
warn()  { echo -e "${YELLOW}[!]${NC} $1"; }
fail()  { echo -e "${RED}[✗]${NC} $1"; exit 1; }

# ---- 检查 paramiko ----
python -c "import paramiko" 2>/dev/null || fail "请先装 paramiko: pip install paramiko"

# ---- 模式判断 ----
if [ $# -gt 0 ]; then
    MODE="files"
    FILES=("$@")
    info "模式: 只传 ${#FILES[@]} 个文件"
else
    MODE="full"
    info "模式: 全量推送"
fi

export PYTHONIOENCODING=utf-8

# ---- Python 一键推送 ----
python - "$LOCAL_ROOT" "$HOST" "$USER" "$PASS" "$REMOTE_DIR" "$MODE" "${FILES[@]:-}" <<'PYEOF'
import sys, os, json, time, paramiko, tarfile, io

local_root = sys.argv[1]
host, user, passwd, remote_dir = sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]
mode = sys.argv[6]
files = sys.argv[7:] if len(sys.argv) > 7 else []

def connect():
    c = paramiko.SSHClient()
    c.set_missing_host_key_policy(paramiko.AutoAddPolicy())
    c.connect(host, username=user, password=passwd, timeout=15)
    return c

if mode == "files":
    # 只传指定文件
    c = connect()
    sftp = c.open_sftp()
    for f in files:
        local = os.path.join(local_root, f)
        remote = f"{remote_dir}/{f}"
        if not os.path.exists(local):
            # 尝试绝对路径
            local = f
            remote = f"{remote_dir}/{os.path.basename(f)}"
        if os.path.exists(local):
            sftp.put(local, remote)
            print(f"  ✓ {os.path.basename(local)}")
        else:
            print(f"  ✗ 找不到: {f}")
    sftp.close()
    c.close()
else:
    # 全量打包
    excludes = {'node_modules', '.next', 'tmp', '.git', 'data', '__pycache__', '.pytest_cache', 'dist', 'bin'}
    excludes_ext = {'.db', '.sqlite', '.pyc', '.log', '.exe'}
    excludes_files = {'.env', 'server.exe', 'anp-deploy.tar.gz'}

    buf = io.BytesIO()
    count = 0
    with tarfile.open(fileobj=buf, mode='w:gz') as tar:
        for root, dirs, fnames in os.walk(local_root):
            dirs[:] = [d for d in dirs if d not in excludes]
            for fname in fnames:
                if fname in excludes_files: continue
                if any(fname.endswith(ext) for ext in excludes_ext): continue
                full = os.path.join(root, fname)
                arc = os.path.relpath(full, local_root)
                tar.add(full, arcname=arc)
                count += 1
    buf.seek(0)
    size_mb = len(buf.getvalue()) / 1024 / 1024
    print(f"  打包 {count} 文件, {size_mb:.1f}MB")

    # 上传
    c = connect()
    sftp = c.open_sftp()
    sftp.putfo(buf, '/root/anp-deploy.tar.gz')
    sftp.close()
    print(f"  ✓ 上传完成")

    # 解压
    _, out, _ = c.exec_command(f'cd {remote_dir} && tar xzf /root/anp-deploy.tar.gz && echo OK', timeout=60)
    print(f"  ✓ 解压: {out.read().decode()[:30].strip()}")
    c.close()

# 触发重建
print("\n[2/3] 触发 docker-compose 重建...")
c = connect()
c.exec_command(f'cd {remote_dir} && nohup docker-compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build > /tmp/anp-push.log 2>&1 &')
c.close()
print("  ✓ 后台重建已触发")

# 等待 + 验证
print("\n[3/3] 等待构建完成（最多 6 分钟）...")
for i in range(72):  # 6 分钟
    time.sleep(5)
    c = connect()
    _, out, _ = c.exec_command('ps aux | grep docker-compose | grep -v grep | wc -l', timeout=10)
    n = int(out.read().decode().strip())
    c.close()
    if n == 0:
        # 构建完成
        c = connect()
        _, out, _ = c.exec_command(
            'tail -3 /tmp/anp-push.log; echo "===";'
            'cd /opt/anp && docker-compose -f deploy/docker-compose.prod.yml ps 2>&1 | grep -c "Up";'
            'echo "===";'
            'curl -s -o /dev/null -w "%{http_code}" http://localhost:8088/',
            timeout=15)
        result = out.read().decode(errors='replace')
        c.close()

        if 'failed' in result.lower():
            print(f"\n✗ 构建失败！\n{result}")
            sys.exit(1)

        # 解析容器数 + http
        lines = result.strip().split('===')
        containers = lines[1].strip() if len(lines) > 1 else '?'
        http_code = lines[2].strip() if len(lines) > 2 else '?'
        print(f"\n  构建日志尾部:\n{lines[0].strip()}")
        print(f"  容器 Up 数: {containers}")
        print(f"  8088 首页: {http_code}")
        if http_code == '200':
            print("\n✅ 部署成功！")
        else:
            print(f"\n⚠️  8088 未就绪 ({http_code})，查看日志: ssh root@{host} tail -20 /tmp/anp-push.log")
        break
    if i % 6 == 0:
        print(f"  ... 构建中 ({(i+1)*5}s)")
else:
    print("\n⚠️ 超时（6分钟），构建可能仍在进行，请手动检查")
PYEOF

echo ""
info "推送完成"
