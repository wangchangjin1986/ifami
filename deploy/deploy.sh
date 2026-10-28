#!/usr/bin/env bash
# Phase 0 发布（本机部署，无 Docker）：git tag 可回滚
# 用法：./deploy.sh（默认发到 118.196.79.193；换机器用 DEPLOY_HOST=<ip> ./deploy.sh）
set -euo pipefail
HOST="${DEPLOY_HOST:-118.196.79.193}"

# 单次 SSH 完成全部远端操作（profile.d 在非登录 shell 不生效，需显式 export Go 路径）
ssh root@"$HOST" 'set -euo pipefail
export PATH="$PATH:/usr/local/go/bin"
cd /opt/ifami
git pull
CGO_ENABLED=0 go build -o ifami ./cmd/ifami
set -a; . deploy/.env; set +a
./ifami migrate
systemctl restart ifami-api ifami-worker
'

# 走公网 + Caddy 做健康检查，验证整条链路（Caddy → api）
curl -fsS "http://$HOST/healthz" >/dev/null
echo "部署完成：http://$HOST/healthz 通过"
