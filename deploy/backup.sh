#!/usr/bin/env bash
# 备份宿主机 MySQL（数据独一份，建议 crontab 每日执行：0 3 * * * /opt/ifami/deploy/backup.sh）
# 用法: ./backup.sh  →  写入 backup/ifami_YYYYmmdd_HHMMSS.sql.gz
set -euo pipefail
cd "$(dirname "$0")"
set -a; source .env; set +a
mkdir -p backup
STAMP="$(date +%Y%m%d_%H%M%S)"
OUT="backup/ifami_${STAMP}.sql.gz"
mysqldump -h127.0.0.1 -uroot -p"${MYSQL_ROOT_PASSWORD}" \
  --single-transaction --routines --triggers ifami | gzip > "$OUT"
ls -lh "$OUT"