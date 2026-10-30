#!/usr/bin/env bash
# ifami 服务启停管理（在 ECS 本机以 root 执行）——服务起停的唯一入口，deploy.sh 也委托它重启。
# 用法：
#   bash deploy/service.sh start|stop|restart|status [api|worker|caddy ...]   # 缺省操作全部
#   bash deploy/service.sh logs [api|worker]           # tail 落盘日志，默认两个一起看
set -euo pipefail

LOG_API=/var/log/ifami-api.log
LOG_WORKER=/var/log/ifami-worker.log

[[ $EUID -eq 0 ]] || { echo "请用 root 执行（sudo bash deploy/service.sh …）" >&2; exit 1; }

cmd="${1:-}"
[[ $# -gt 0 ]] && shift
comps=("$@")

# 组件名 → systemd 单元名（缺省全部）
map_units() {
  local comps_=("$@")
  [[ ${#comps_[@]} -eq 0 ]] && comps_=(api worker caddy)
  units=()
  local c
  for c in "${comps_[@]}"; do
    case "$c" in
      api)    units+=(ifami-api) ;;
      worker) units+=(ifami-worker) ;;
      caddy)  units+=(caddy) ;;
      *)      echo "未知组件：$c（可选：api worker caddy）" >&2; exit 1 ;;
    esac
  done
}

case "$cmd" in
  start|stop|restart)
    map_units ${comps[@]+"${comps[@]}"}
    systemctl "$cmd" "${units[@]}"
    case "$cmd" in
      start)   verb=启动 ;;
      stop)    verb=停止 ;;
      restart) verb=重启 ;;
    esac
    echo "已${verb}：${units[*]}"
    ;;
  status)
    map_units ${comps[@]+"${comps[@]}"}
    systemctl --no-pager --full status "${units[@]}" || true
    echo
    curl -fsS --max-time 3 http://127.0.0.1:8080/healthz >/dev/null \
      && echo "healthz: ok" || echo "healthz: 不通"
    ;;
  logs)
    case "${comps[0]:-}" in
      api)    exec tail -F "$LOG_API" ;;
      worker) exec tail -F "$LOG_WORKER" ;;
      "")     exec tail -F "$LOG_API" "$LOG_WORKER" ;;
      *)      echo "用法：bash deploy/service.sh logs [api|worker]" >&2; exit 1 ;;
    esac
    ;;
  *)
    sed -n '2,6p' "$0"
    exit 1
    ;;
esac
