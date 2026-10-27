#!/usr/bin/env bash
# ifami Phase 0 ECS 一次性初始化脚本（本机原生，无 Docker）
#
# 用法：仓库 clone 到 /opt/ifami 后，以 root 执行：
#     cd /opt/ifami && sudo bash deploy/bootstrap.sh
#
# 做的事：装 MySQL/Redis/Caddy/Go → 建库建应用账号 → 生成并写 deploy/.env →
#         编译 ifami → 迁移建表 → 装 systemd 单元（api/worker/caddy）→ 启动 → 健康检查。
#
# 幂等：已装组件跳过、DB 用 IF NOT EXISTS、.env 已存在则不重复生成密钥。
# 可预设环境变量跳过交互：WX_MINIAPP_APPID / WX_MINIAPP_SECRET / MYSQL_ROOT_PASSWORD / DB_PASSWORD
# 中国大陆下载慢/被墙：Go 换 mirrors.aliyun.com/golang，Caddy 换国内镜像，GOPROXY 默认 goproxy.cn。
set -euo pipefail

# ---------------- 可调参数 ----------------
GO_VERSION="${GO_VERSION:-1.23.4}"       # go.mod 要求 >=1.23
CADDY_VERSION="${CADDY_VERSION:-2.9.1}"
REPO_DIR="${REPO_DIR:-/opt/ifami}"
APP_DB="${APP_DB:-ifami}"
APP_USER="${APP_USER:-ifami}"

# ---------------- 工具 ----------------
log()  { printf '\033[1;32m[ifami]\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33m[ifami]\033[0m %s\n' "$*" >&2; }
die()  { printf '\033[1;31m[ifami]\033[0m %s\n' "$*" >&2; exit 1; }
have() { command -v "$1" >/dev/null 2>&1; }
arch_pkg() { case "$(uname -m)" in x86_64) echo amd64;; aarch64|arm64) echo arm64;; *) die "不支持的架构 $(uname -m)";; esac; }

[[ $EUID -eq 0 ]] || die "请用 root 执行（sudo bash deploy/bootstrap.sh）"
[[ -f "$REPO_DIR/cmd/ifami/main.go" ]] || die "找不到仓库：REPO_DIR=$REPO_DIR（请先 git clone 到 /opt/ifami）"
cd "$REPO_DIR"

# 探测包管理器
if have apt-get; then
  PKG="apt-get install -y"; export DEBIAN_FRONTEND=noninteractive
  log "发行版：Debian/Ubuntu（apt）"; apt-get update -y
elif have dnf; then
  PKG="dnf install -y"; log "发行版：veLinux/CentOS/Rocky（dnf）"
elif have yum; then
  PKG="yum install -y"; log "发行版：CentOS（yum）"
else
  die "不支持的发行版（需要 apt/dnf/yum）"
fi

svc() { local n; for n in "$@"; do systemctl list-unit-files --no-pager 2>/dev/null | grep -q "^${n}\.service" && { echo "$n"; return 0; }; done; echo "$1"; }

wait_mysql() { local i; for i in $(seq 1 15); do mysqladmin ping >/dev/null 2>&1 && return 0; sleep 1; done; return 0; }

# ---------------- 基础工具 ----------------
log "安装基础工具…"
$PKG curl wget ca-certificates gnupg openssl git || warn "部分基础工具安装失败（可能已存在）"

# ---------------- MySQL ----------------
if ! have mysqld; then log "安装 MySQL…"; $PKG mysql-server; fi
MYSQL_SVC=$(svc mysql mysqld)
systemctl enable --now "$MYSQL_SVC" 2>/dev/null || systemctl start "$MYSQL_SVC" 2>/dev/null || true
wait_mysql

MYSQL_CONF=""
for c in /etc/mysql/mysql.conf.d/mysqld.cnf /etc/my.cnf.d/mysql-server.cnf /etc/my.cnf; do
  [[ -f "$c" ]] && { MYSQL_CONF="$c"; break; }
done
if [[ -n "$MYSQL_CONF" ]]; then
  if ! grep -q 'ifami bootstrap' "$MYSQL_CONF"; then
    cat >>"$MYSQL_CONF" <<'EOF'

# ifami bootstrap
[mysqld]
bind-address = 127.0.0.1
character-set-server = utf8mb4
collation-server = utf8mb4_unicode_ci
EOF
    systemctl restart "$MYSQL_SVC"
    wait_mysql
  fi
else
  warn "未找到 mysqld 配置文件，跳过绑 127.0.0.1（请确保安全组不放 3306）"
fi

# ---------------- Redis ----------------
if ! have redis-server; then log "安装 Redis…"; $PKG redis-server || $PKG redis; fi
REDIS_SVC=$(svc redis-server redis)
systemctl enable --now "$REDIS_SVC" 2>/dev/null || systemctl start "$REDIS_SVC" 2>/dev/null || true

# ---------------- Caddy（静态二进制） ----------------
if ! have caddy; then
  log "安装 Caddy v${CADDY_VERSION}…"
  tmp=$(mktemp -d)
  curl -fsSL "https://github.com/caddyserver/caddy/releases/download/v${CADDY_VERSION}/caddy_${CADDY_VERSION}_linux_$(arch_pkg).tar.gz" -o "$tmp/caddy.tgz"
  tar -C "$tmp" -xzf "$tmp/caddy.tgz" caddy
  install -m 755 "$tmp/caddy" /usr/local/bin/caddy
  rm -rf "$tmp"
fi
id -u caddy >/dev/null 2>&1 || useradd --system --home-dir /var/lib/caddy --create-home --shell /usr/sbin/nologin caddy
mkdir -p /etc/caddy /var/lib/caddy
chown -R caddy:caddy /var/lib/caddy
install -m 644 deploy/caddy.service /etc/systemd/system/caddy.service

# ---------------- Go 工具链 ----------------
need_go=0
if have go; then
  minor=$(go version | sed -n 's/.*go1\.\([0-9][0-9]*\)\..*/\1/p')
  [[ "$minor" =~ ^[0-9]+$ ]] && [[ "$minor" -ge 23 ]] || need_go=1
else
  need_go=1
fi
if [[ $need_go -eq 1 ]]; then
  log "安装 Go v${GO_VERSION}…"
  tmp=$(mktemp -d)
  curl -fsSL "https://go.dev/dl/go${GO_VERSION}.linux-$(arch_pkg).tar.gz" -o "$tmp/go.tgz"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "$tmp/go.tgz"
  rm -rf "$tmp"
fi
export PATH="/usr/local/go/bin:$PATH"
echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh

# ---------------- 密钥与 .env（首次生成，重复执行跳过） ----------------
ENV_FILE="deploy/.env"
if [[ ! -f "$ENV_FILE" ]]; then
  log "生成密钥并写 deploy/.env…"
  if [[ -n "${WX_MINIAPP_APPID:-}" ]]; then WX_APPID="$WX_MINIAPP_APPID"; else read -r -p "小程序 AppID（回车用示例值 wxcf35b1844a8c3fe0）: " WX_APPID || true; WX_APPID="${WX_APPID:-wxcf35b1844a8c3fe0}"; fi
  if [[ -n "${WX_MINIAPP_SECRET:-}" ]]; then WX_SECRET="$WX_MINIAPP_SECRET"; else read -r -s -p "小程序 AppSecret: " WX_SECRET || true; echo; fi
  [[ -n "$WX_SECRET" ]] || warn "AppSecret 为空，微信登录会失败，稍后可在 deploy/.env 补填"

  ROOT_PW="${MYSQL_ROOT_PASSWORD:-$(openssl rand -hex 16)}"
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
MYSQL_ROOT_PASSWORD=${ROOT_PW}
RDS_DSN=${APP_USER}:${APP_PW}@tcp(127.0.0.1:3306)/${APP_DB}?parseTime=true

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

  log "建库 + 建应用账号…"
  mysql <<SQL
CREATE DATABASE IF NOT EXISTS ${APP_DB} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS '${APP_USER}'@'localhost' IDENTIFIED BY '${APP_PW}';
GRANT ALL PRIVILEGES ON ${APP_DB}.* TO '${APP_USER}'@'localhost';
ALTER USER 'root'@'localhost' IDENTIFIED BY '${ROOT_PW}';
FLUSH PRIVILEGES;
SQL
else
  log "已存在 deploy/.env，跳过密钥生成与建库（如需重置：删掉后重跑）"
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

cat <<'EOF'

后续步骤：
 1. 域名解析：api.ifami.site 的 A 记录指向本机公网 IP（ICP 备案通过后）
 2. HTTPS：80/443 放通后 Caddy 自动签发证书，用 curl https://api.ifami.site/healthz 验证
 3. 小程序：上传前把 miniapp/app.js 的 apiBase 改为 https://api.ifami.site
 4. 微信后台配置 request / uploadFile / downloadFile 合法域名
 5. 备份：crontab -e 加一行  `0 3 * * * /opt/ifami/deploy/backup.sh`
 6. 密钥都在 /opt/ifami/deploy/.env，务必另行备份（尤其 FIELD_ENC_KEY / MYSQL_ROOT_PASSWORD / INVITE_SECRET）
EOF