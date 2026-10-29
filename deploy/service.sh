#!/usr/bin/env bash
# ifami 服务启停管理（在 ECS 本机以 root 执行）
# 用法：
#   bash deploy/service.sh start|stop|restart|status   # 管理 api/worker/caddy
#   bash deploy/service.sh logs [api|worker]           # tail 落盘日志，默认两个一起看
set -euo pipefail

SERVICES=(ifami-api ifami-worker caddy)
LOG_API=/var/log/ifami-api.log
LOG_WORKER=/var/log/ifami-worker.log

[[ $EUID -eq 0 ]] || { echo "请用 root 执行（sudo bash deploy/service.sh …）" >&2; exit 1; }

cmd="${1:-}"
case "$cmd" in
  start)
    systemctl start "${SERVICES[@]}"
    echo "已启动：${SERVICES[*]}"
    ;;
  stop)
    systemctl stop "${SERVICES[@]}"
    echo "已停止：${SERVICES[*]}"
    ;;
  restart)
    systemctl restart "${SERVICES[@]}"
    echo "已重启：${SERVICES[*]}"
    ;;
  status)
    systemctl --no-pager --full status "${SERVICES[@]}" || true
    echo
    curl -fsS --max-time 3 http://127.0.0.1:8080/healthz >/dev/null \
      && echo "healthz: ok" || echo "healthz: 不通"
    ;;
  logs)
    case "${2:-}" in
      api)    exec tail -F "$LOG_API" ;;
      worker) exec tail -F "$LOG_WORKER" ;;
      "")     exec tail -F "$LOG_API" "$LOG_WORKER" ;;
      *)      echo "用法：bash deploy/service.sh logs [api|worker]" >&2; exit 1 ;;
    esac
    ;;
  *)
    sed -n '2,5p' "$0"
    exit 1
    ;;
esac
