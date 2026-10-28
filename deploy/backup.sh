#!/usr/bin/env bash
# 备份宿主机 MySQL（数据独一份，建议 crontab 每日执行：0 3 * * * /opt/ifami/deploy/backup.sh）
# 用法: ./backup.sh  →  写入 backup/ifami_YYYYmmdd_HHMMSS.sql.gz
set -euo pipefail
cd "$(dirname "$0")"
set -a; source .env; set +a
mkdir -p backup
STAMP="$(date +%Y%m%d_%H%M%S)"
OUT="backup/ifami_${STAMP}.sql.gz"
# MYSQL_ROOT_PASSWORD 为空 = root 本机免密，走 socket 不带 -p（-p"" 可能挂起或鉴权失败）
if [[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]; then
  mysqldump -h127.0.0.1 -uroot -p"${MYSQL_ROOT_PASSWORD}" \
    --single-transaction --routines --triggers ifami | gzip > "$OUT"
else
  mysqldump -uroot \
    --single-transaction --routines --triggers ifami | gzip > "$OUT"
fi
ls -lh "$OUT"