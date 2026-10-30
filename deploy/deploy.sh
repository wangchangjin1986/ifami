#!/usr/bin/env bash
# Phase 0 发布（在 ECS 本机执行，无 Docker）：git tag 可回滚
# 用法：cd /opt/ifami && bash deploy/deploy.sh
#
# 脚本分工：
#   bootstrap.sh  首次初始化（中间件检测、建库建账号、systemd 单元安装）
#   deploy.sh     日常迭代发布：拉代码 → 编译 → 迁移 → 停服重启 → 健康检查
#   service.sh    服务起停的唯一入口（deploy.sh 的重启动作也委托给它）
set -euo pipefail
REPO_DIR="${REPO_DIR:-/opt/ifami}"

export PATH="$PATH:/usr/local/go/bin"   # 非登录 shell 下 profile.d 不生效
cd "$REPO_DIR"

git pull
CGO_ENABLED=0 go build -o ifami ./cmd/ifami
set -a; . deploy/.env; set +a
./ifami migrate                          # 幂等建表

# Caddy 配置随仓库走：有变化才更新并 reload
if ! cmp -s deploy/Caddyfile /etc/caddy/Caddyfile; then
  install -m 644 deploy/Caddyfile /etc/caddy/Caddyfile
  systemctl reload caddy 2>/dev/null || systemctl restart caddy
  echo "Caddyfile 已更新并重载"
fi

# 停服重启委托 service.sh（起停逻辑单一入口）；caddy 配置有变化时已在上面 reload
bash deploy/service.sh restart api worker

# 重启后 Hertz 需要一点时间绑定端口，轮询等待而非立即检查
ok=0
for _ in $(seq 1 15); do
  curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && { ok=1; break; }
  sleep 1
done
[[ $ok -eq 1 ]] || { echo "健康检查未通过，排查：bash deploy/service.sh logs api" >&2; exit 1; }
echo "部署完成，/healthz 通过"
