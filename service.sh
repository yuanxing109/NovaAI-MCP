#!/system/bin/sh

MODDIR=${0%/*}
ZCR_MODDIR="$MODDIR"
. "$MODDIR/common.sh"

[ -e "$MODDIR/disable" ] && exit 0
[ -e "$MODDIR/remove" ] && exit 0

# 等待 boot_completed（最多 300 秒）
for i in $(seq 1 150); do
  if [ "$(getprop sys.boot_completed)" = "1" ]; then
    break
  fi
  sleep 2
done

# 等待 /data/adb 就绪（最多 15 秒）
for i in $(seq 1 30); do
  if [ -w /data/adb ]; then
    break
  fi
  sleep 0.5
done

umask 077
zcr_prepare_internal || exit 0

zcr_start_supervisor >/dev/null 2>&1 &

# 看门狗循环：60s 巡检，3 次失败重启
(
  fail_count=0
  PORT=5322

  while true; do
    sleep 60

    # zcr_read_pid 已校验进程身份，非空即代表 daemon 活着
    pid="$(zcr_read_pid 2>/dev/null)"
    if [ -z "$pid" ]; then
      zcr_log "watchdog: daemon 未运行，重启"
      zcr_start_supervisor >/dev/null 2>&1 &
      sleep 5
      fail_count=0
      continue
    fi

    # 健康检查：curl -> nc -> 都没有则只信 PID。
    #
    # 关键点：探测工具缺失时必须视为"健康"，不能计失败。
    # 模块并不自带 curl，Android 也不保证有；旧实现因此在没有 curl 的
    # 设备上恒定探测失败，每 3 分钟就杀掉并重启一次 daemon。
    # timeout 同理：缺失时 zcr_with_timeout 退化为无超时执行。
    if command -v curl >/dev/null 2>&1; then
      # 无鉴权：不带任何认证头即可通过 hostMiddleware（Host 是 IP 字面量）。
      code=$(zcr_with_timeout 5 curl -s -o /dev/null -w '%{http_code}' \
        -X POST "http://127.0.0.1:$PORT/mcp" \
        -H "Content-Type: application/json" \
        -H "Accept: application/json" \
        -d '{"jsonrpc":"2.0","id":1,"method":"ping"}' 2>/dev/null)
      [ "$code" = "200" ] && healthy=1 || healthy=0
    elif command -v nc >/dev/null 2>&1; then
      zcr_with_timeout 5 nc -z 127.0.0.1 "$PORT" >/dev/null 2>&1 && healthy=1 || healthy=0
    else
      healthy=1
    fi

    if [ "$healthy" = "1" ]; then
      fail_count=0
    else
      fail_count=$((fail_count + 1))
      if [ "$fail_count" -ge 3 ]; then
        zcr_log "watchdog: 连续 3 次健康检查失败，重启"
        zcr_stop_supervisor
        sleep 2
        zcr_start_supervisor >/dev/null 2>&1 &
        fail_count=0
      fi
    fi
  done
) &

exit 0