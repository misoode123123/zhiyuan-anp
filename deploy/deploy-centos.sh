#!/usr/bin/env bash
# 智源 ANP — CentOS 一键部署（Docker Compose）
# 用法：在服务器项目根目录执行 bash deploy/deploy-centos.sh
set -euo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

echo "[1/5] 检查 Docker..."
docker --version || { echo "请先装 Docker"; exit 1; }
docker compose version || { echo "请启用 docker compose 插件"; exit 1; }

echo "[2/5] 准备 .env.prod..."
if [ ! -f deploy/.env.prod ]; then
  cp deploy/.env.prod.example deploy/.env.prod
  echo "已生成 deploy/.env.prod，请编辑填入 ZHIPUAI_API_KEY 后重跑此脚本"
  exit 0
fi

echo "[3/5] 准备目录..."
mkdir -p "$ROOT/data/repos"

echo "[4/5] 构建并启动..."
docker compose -f deploy/docker-compose.prod.yml --env-file deploy/.env.prod up -d --build

echo "[5/5] 等待就绪..."
for i in $(seq 1 30); do
  sleep 2
  code=$(curl -s -o /dev/null -w "%{http_code}" http://localhost/api/v1/project-spaces 2>/dev/null || echo 000)
  [ "$code" = "200" ] && break
done
echo ""
echo "============================================"
echo "✅ 部署完成：http://$(hostname -I | awk '{print $1}')"
echo "   健康检查：curl http://localhost/api/v1/project-spaces"
echo "   日志：docker compose -f deploy/docker-compose.prod.yml logs -f"
echo "   停止：docker compose -f deploy/docker-compose.prod.yml down"
echo "============================================"
