#!/usr/bin/env bash
# Phase 0 发布（在 ECS 本机执行，无 Docker）：git tag 可回滚
# 用法：cd /opt/ifami && bash deploy/deploy.sh
#
# 脚本分工：
#   bootstrap.sh  首次初始化（中间件检测、建库建账号、systemd 单元安装）
#   deploy.sh     日常迭代发布：编译当前工作区 → 停旧服务 → 迁移 → 重启 → 健康检查
#   service.sh    服务起停的唯一入口（deploy.sh 的重启动作也委托给它）
set -euo pipefail
REPO_DIR="${REPO_DIR:-/opt/ifami}"

export PATH="$PATH:/usr/local/go/bin"   # 非登录 shell 下 profile.d 不生效
cd "$REPO_DIR"

CGO_ENABLED=0 go build -o ifami ./cmd/ifami
[[ -f deploy/.env ]] || { echo "缺少 deploy/.env，请先执行 sudo bash deploy/bootstrap.sh" >&2; exit 1; }

# 兼容早期 .env：没有 MIGRATIONS_DIR 时补齐，保持“bootstrap.sh / deploy.sh”部署口径不变。
if ! grep -q '^MIGRATIONS_DIR=' deploy/.env; then
  chmod u+w deploy/.env 2>/dev/null || true
  cat >>deploy/.env <<EOF

# golang-migrate 迁移文件目录
MIGRATIONS_DIR=${REPO_DIR}/migrations
EOF
  chmod 600 deploy/.env 2>/dev/null || true
fi

set -a; . deploy/.env; set +a

# 拍照导入识别原图临时目录（早期 .env 无 INGEST_DIR 时用默认值；程序启动也会 MkdirAll，这里提前收紧权限）。
INGEST_DIR="${INGEST_DIR:-/var/lib/ifami/ingest}"
mkdir -p "$INGEST_DIR" && chmod 700 "$INGEST_DIR"
[[ -n "${ARK_API_KEY:-}" && -n "${ARK_MODEL:-}" ]] || echo "提示：ARK_API_KEY / ARK_MODEL 未配置，拍照导入日程会识别失败（重跑 bootstrap.sh 可补齐配置项）" >&2

# 先停旧 api/worker，再迁移结构性变更，避免旧进程在迁移期间继续按旧 schema 写库。
bash deploy/service.sh stop api worker
./ifami migrate                          # 幂等建表

# Caddy 配置随仓库走：有变化才更新并 reload
if ! cmp -s deploy/Caddyfile /etc/caddy/Caddyfile; then
  install -m 644 deploy/Caddyfile /etc/caddy/Caddyfile
  systemctl reload caddy 2>/dev/null || systemctl restart caddy
  echo "Caddyfile 已更新并重载"
fi

# 启动委托 service.sh（起停逻辑单一入口）。
# 日常发布也顺手确保 caddy 处于运行态，否则 api 本机健康但公网/真机仍然连不到。
bash deploy/service.sh start api worker caddy

# 重启后 Hertz 需要一点时间绑定端口，轮询等待而非立即检查
ok=0
for _ in $(seq 1 15); do
  curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1 && { ok=1; break; }
  sleep 1
done
[[ $ok -eq 1 ]] || { echo "本机健康检查未通过，排查：bash deploy/service.sh logs api" >&2; exit 1; }
echo "本机 /healthz 通过"

PUBLIC_IP="${PUBLIC_IP:-118.196.79.193}"
if curl -fsS --max-time 5 "http://${PUBLIC_IP}/healthz" >/dev/null 2>&1; then
  echo "公网 /healthz 通过：http://${PUBLIC_IP}/healthz"
else
  echo "公网 http://${PUBLIC_IP}/healthz 不通：请检查 caddy 状态、本机防火墙和云安全组 80 端口；真机调试会因此连不到后端" >&2
fi
