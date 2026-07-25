#!/usr/bin/env bash
# 智源 ANP — PostgreSQL 自动备份（docker exec pg_dump + 滚动保留 7 天）
# 用法（在 .28 上 cron 每天跑）: 0 2 * * * /opt/anp/scripts/backup-pg.sh
set -euo pipefail

BACKUP_DIR="/data/backups/pg"
RETENTION_DAYS=7
CONTAINER="deploy_postgres_1"
DB_USER="anp"
DB_NAME="anp"
DATE=$(date +%Y%m%d_%H%M%S)
FILE="${BACKUP_DIR}/anp_${DATE}.sql.gz"

mkdir -p "$BACKUP_DIR"

echo "[$(date)] 开始备份 ${DB_NAME} → ${FILE}"

# pg_dump（容器内执行，gzip 压缩）
docker exec "$CONTAINER" pg_dump -U "$DB_USER" -d "$DB_NAME" --no-owner --clean --if-exists 2>/dev/null | gzip > "$FILE"

SIZE=$(du -h "$FILE" | cut -f1)
echo "[$(date)] 备份完成: ${FILE} (${SIZE})"

# 滚动清理：删 7 天前的备份
DELETED=$(find "$BACKUP_DIR" -name "anp_*.sql.gz" -mtime +${RETENTION_DAYS} -delete -print | wc -l)
if [ "$DELETED" -gt 0 ]; then
  echo "[$(date)] 清理 ${DELETED} 个过期备份（>${RETENTION_DAYS}天）"
fi

# 列出当前备份
echo "--- 当前备份列表 ---"
ls -lh "$BACKUP_DIR"/anp_*.sql.gz 2>/dev/null | tail -10
