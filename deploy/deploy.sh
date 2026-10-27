#!/usr/bin/env bash
# Phase 0 发布（本机部署，无 Docker）：git tag 可回滚
set -euo pipefail
HOST="${DEPLOY_HOST:?用法: DEPLOY_HOST=<ecs-ip> ./deploy.sh}"

ssh root@"$HOST" 'cd /opt/ifami && git pull'
ssh root@"$HOST" 'cd /opt/ifami && CGO_ENABLED=0 go build -o ifami ./cmd/ifami'
ssh root@"$HOST" 'cd /opt/ifami && set -a; . deploy/.env; set +a; ./ifami migrate'   # 幂等建表
ssh root@"$HOST" 'systemctl restart ifami-api ifami-worker'
ssh root@"$HOST" 'curl -fsS http://127.0.0.1:8080/healthz' >/dev/null
echo "部署完成，/healthz 通过"