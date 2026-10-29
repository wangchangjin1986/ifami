#!/usr/bin/env bash
# ifami Phase 0 ECS 初始化脚本（本机原生，无 Docker）
#
# 用法：仓库 clone 到 /opt/ifami 后，以 root 执行：
#     cd /opt/ifami && sudo bash deploy/bootstrap.sh
#
# 职责边界：**不负责安装中间件**，只检测 MySQL / Redis / Caddy / Go(>=1.23)，
# 缺失时给出安装提示并退出。检测通过后直接完成初始化：
#   生成并写 deploy/.env → 建库建应用账号 → 编译 ifami → 迁移建表 →
#   装 systemd 单元（api/worker/caddy）→ 启动 → 健康检查（本机 + 公网）。
#
# 幂等：.env 已存在则不重复生成密钥，DB 用 IF NOT EXISTS，迁移幂等。
# 可预设环境变量跳过交互：WX_MINIAPP_APPID / WX_MINIAPP_SECRET / MYSQL_ROOT_PASSWORD / DB_PASSWORD
set -euo pipefail

# ---------------- 可调参数 ----------------
REPO_DIR="${REPO_DIR:-/opt/ifami}"
APP_DB="${APP_DB:-ifami}"
APP_USER="${APP_USER:-ifami}"
PUBLIC_IP="${PUBLIC_IP:-118.196.79.193}"   # ECS 公网 IP，用于公网健康检查

# ---------------- 工具 ----------------
log()  { printf '\033[1;32m[ifami]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[ifami]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[ifami]\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }

[[ $EUID -eq 0 ]] || die "请用 root 执行（sudo bash deploy/bootstrap.sh）"
[[ -f "$REPO_DIR/cmd/ifami/main.go" ]] || die "找不到仓库：REPO_DIR=$REPO_DIR（请先 git clone 到 /opt/ifami）"
cd "$REPO_DIR"

# ---------------- 中间件检测（只检测不安装，缺失给提示） ----------------
log "检测中间件…"
MISSING=0
check() { # check <命令> <安装提示>
  if have "$1"; then log "  ✔ $1"; else warn "  ✘ 缺少 $1 —— $2"; MISSING=1; fi
}
check mysqld       "apt-get install -y mysql-server（Debian/Ubuntu）或 dnf install -y mysql-server（veLinux/CentOS）"
check redis-server "apt-get install -y redis-server 或 dnf install -y redis"
check caddy        "https://caddyserver.com/docs/install"

if have go; then
  minor=$(go version | sed -n 's/.*go1\.\([0-9][0-9]*\)\..*/\1/p')
  if [[ "$minor" =~ ^[0-9]+$ ]] && [[ "$minor" -ge 23 ]]; then
    log "  ✔ go（$(go version | awk '{print $3}')）"
  else
    warn "  ✘ go 版本过低（需要 >=1.23）—— 下载：https://mirrors.aliyun.com/golang/"
    MISSING=1
  fi
else
  warn "  ✘ 缺少 go —— 安装 >=1.23，大陆镜像：https://mirrors.aliyun.com/golang/"
  MISSING=1
fi
[[ $MISSING -eq 0 ]] || die "请先安装以上缺失组件后重跑"

# 已装未起的，同样只提示（root 设了密码时 ping 会 Access denied，也说明进程活着）
mysql_out=$(mysqladmin ping 2>&1 || true)
[[ "$mysql_out" == *alive* || "$mysql_out" == *"Access denied"* ]] \
  || die "MySQL 已安装但未运行：systemctl enable --now mysql（或 mysqld）"
redis-cli ping >/dev/null 2>&1 || die "Redis 已安装但未运行：systemctl enable --now redis-server（或 redis）"

# ---------------- 密钥与 .env（首次生成，重复执行跳过） ----------------
ENV_FILE="deploy/.env"
if [[ ! -f "$ENV_FILE" ]]; then
  log "生成密钥并写 deploy/.env…"
  if [[ -n "${WX_MINIAPP_APPID:-}" ]]; then WX_APPID="$WX_MINIAPP_APPID"; else read -r -p "小程序 AppID（回车用示例值 wxcf35b1844a8c3fe0）: " WX_APPID || true; WX_APPID="${WX_APPID:-wxcf35b1844a8c3fe0}"; fi
  if [[ -n "${WX_MINIAPP_SECRET:-}" ]]; then WX_SECRET="$WX_MINIAPP_SECRET"; else read -r -s -p "小程序 AppSecret: " WX_SECRET || true; echo; fi
  [[ -n "$WX_SECRET" ]] || warn "AppSecret 为空，微信登录会失败，稍后可在 deploy/.env 补填"

  ROOT_PW="${MYSQL_ROOT_PASSWORD:-}"   # 默认空 = 保持 root 本机免密；预设则同步修改为该密码
  APP_PW="${DB_PASSWORD:-$(openssl rand -hex 16)}"
  ENC_KEY="$(openssl rand -base64 32)"
  INVITE_SECRET="$(openssl rand -hex 32)"

  umask 077
  cat >"$ENV_FILE" <<EOF
# ifami Phase 0 环境变量（bootstrap 生成，请妥善备份本文件）
API_ADDR=127.0.0.1:8080

# 微信
WX_MINIAPP_APPID=${WX_APPID}
WX_MINIAPP_SECRET=${WX_SECRET}
WX_OFFIACCOUNT_APPID=
WX_OFFIACCOUNT_SECRET=

# MySQL（本机 127.0.0.1:3306）
# RDS_DSN 必须加双引号：deploy.sh / backup.sh 用 source 读本文件，括号会被 bash 当语法解析
MYSQL_ROOT_PASSWORD=${ROOT_PW}
RDS_DSN="${APP_USER}:${APP_PW}@tcp(127.0.0.1:3306)/${APP_DB}?parseTime=true"

# 火山引擎（TOS / SMS / 方舟，Phase 0 可选）
TOS_AK=
TOS_SK=
TOS_BUCKET_MEDIA=ifami-media
TOS_ENDPOINT=
SMS_AK=
SMS_SK=
SMS_SIGN=
ARK_API_KEY=

# Redis（本机）
REDIS_ADDR=127.0.0.1:6379

# 字段级加密密钥
FIELD_ENC_KEY=${ENC_KEY}

# 邀请 token 签名密钥
INVITE_SECRET=${INVITE_SECRET}

# golang-migrate 迁移文件目录
MIGRATIONS_DIR=${REPO_DIR}/migrations
EOF

else
  log "已存在 deploy/.env，跳过密钥生成"
fi

# ---------------- 建库 + 校齐应用账号（幂等，每次执行，确保与 .env 一致） ----------------
log "建库 + 校齐应用账号…"
set -a; source "$ENV_FILE"; set +a
# root 认证：.env 里 MYSQL_ROOT_PASSWORD 非空则密码登录，否则走本机 socket 免密
MYSQL_CLI=(mysql)
[[ -n "${MYSQL_ROOT_PASSWORD:-}" ]] && MYSQL_CLI=(mysql -uroot -p"${MYSQL_ROOT_PASSWORD}")
# 从 RDS_DSN 解析应用账号密码（生成值为 hex，不含 : @）
APP_DB_PW=$(sed -n 's/^RDS_DSN="[^:]*:\(.*\)@tcp.*/\1/p' "$ENV_FILE")
[[ -n "$APP_DB_PW" ]] || die "无法从 RDS_DSN 解析应用账号密码，请检查 deploy/.env"
"${MYSQL_CLI[@]}" <<SQL
CREATE DATABASE IF NOT EXISTS ${APP_DB} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS '${APP_USER}'@'localhost' IDENTIFIED BY '${APP_DB_PW}';
ALTER USER '${APP_USER}'@'localhost' IDENTIFIED BY '${APP_DB_PW}';
GRANT ALL PRIVILEGES ON ${APP_DB}.* TO '${APP_USER}'@'localhost';
FLUSH PRIVILEGES;
SQL

# 预设了 root 密码才同步修改；默认不动，保持本机免密登录
if [[ -n "${MYSQL_ROOT_PASSWORD:-}" ]]; then
  "${MYSQL_CLI[@]}" -e "ALTER USER 'root'@'localhost' IDENTIFIED BY '${MYSQL_ROOT_PASSWORD}'; FLUSH PRIVILEGES;"
fi

# ---------------- 编译 ----------------
log "编译 ifami…"
export GOPROXY="${GOPROXY:-https://goproxy.cn,direct}"
CGO_ENABLED=0 go build -o ifami ./cmd/ifami

# ---------------- 迁移建表 ----------------
log "迁移建表…"
set -a; source "$ENV_FILE"; set +a
./ifami migrate

# ---------------- systemd 服务 ----------------
log "安装并启动 systemd 服务…"
id -u caddy >/dev/null 2>&1 || useradd --system --home-dir /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
mkdir -p /etc/caddy /var/lib/caddy
chown -R caddy:caddy /var/lib/caddy
install -m 644 deploy/caddy.service /etc/systemd/system/caddy.service
install -m 644 deploy/ifami-api.service deploy/ifami-worker.service /etc/systemd/system/
install -m 644 deploy/Caddyfile /etc/caddy/Caddyfile
systemctl daemon-reload
systemctl enable --now ifami-api ifami-worker caddy

# ---------------- 健康检查 ----------------
log "等待 api 就绪…"
ok=0
for _ in $(seq 1 15); do
  if curl -fsS http://127.0.0.1:8080/healthz >/dev/null 2>&1; then ok=1; break; fi
  sleep 1
done

echo
if [[ $ok -eq 1 ]]; then
  log "部署完成：/healthz 返回 ok"
else
  warn "/healthz 未能通过，请排查：journalctl -u ifami-api -e"
fi

# 公网链路验证（Caddy :80 → api），安全组需放通 80
if curl -fsS --max-time 5 "http://${PUBLIC_IP}/healthz" >/dev/null 2>&1; then
  log "公网链路正常：http://${PUBLIC_IP}/healthz"
else
  warn "公网 http://${PUBLIC_IP}/healthz 不通，请检查安全组是否放通 80 端口"
fi

cat <<EOF

后续步骤（备案完成前，IP 直连阶段）：
 1. 安全组：放通 80/22（22 限办公网 IP），不放 3306/6379
 2. 小程序：开发者工具勾选「不校验合法域名」，apiBase 已指向 http://${PUBLIC_IP}
 3. 备份：crontab -e 加一行  \`0 3 * * * ${REPO_DIR}/deploy/backup.sh\`
 4. 密钥都在 ${REPO_DIR}/deploy/.env，务必另行备份（尤其 FIELD_ENC_KEY / MYSQL_ROOT_PASSWORD / INVITE_SECRET）
 5. 日常发布：本机执行 bash ${REPO_DIR}/deploy/deploy.sh

域名备案完成后：
 1. api.ifami.site 的 A 记录指向 ${PUBLIC_IP}，Caddyfile 把 :80 改成 api.ifami.site（自动 HTTPS）
 2. miniapp/app.js 的 apiBase 改为 https://api.ifami.site
 3. 微信后台配置 request / uploadFile / downloadFile 合法域名
EOF
