#!/system/bin/sh

ZCR_MODDIR="${ZCR_MODDIR:-${0%/*}}"
ZCR_INTERNAL_DIR="/data/adb/novaai-mcp"
ZCR_USER_DIR="/storage/emulated/0/novaaiAI"
ZCR_PID_FILE="$ZCR_INTERNAL_DIR/novaaimcpd.pid"
ZCR_LOG_FILE="$ZCR_INTERNAL_DIR/module.log"
ZCR_CONFIG="$ZCR_INTERNAL_DIR/config.json"
ZCR_TOKEN="$ZCR_INTERNAL_DIR/token"

zcr_log() {
  mkdir -p "$ZCR_INTERNAL_DIR" 2>/dev/null
  echo "[$(date '+%Y-%m-%d %H:%M:%S')] $*" >> "$ZCR_LOG_FILE"
}

zcr_print() {
  echo "$*"
}

zcr_prepare_internal() {
  mkdir -p "$ZCR_INTERNAL_DIR" 2>/dev/null
  chmod 0700 "$ZCR_INTERNAL_DIR" 2>/dev/null
  mkdir -p "$ZCR_INTERNAL_DIR/workspace" 2>/dev/null
  mkdir -p "$ZCR_INTERNAL_DIR/audit" 2>/dev/null
  mkdir -p "$ZCR_INTERNAL_DIR/crash" 2>/dev/null
  chmod 0700 "$ZCR_INTERNAL_DIR/audit" 2>/dev/null
  mkdir -p "$ZCR_USER_DIR" 2>/dev/null
}

# zcr_with_timeout <秒> <命令...>
#
# Android 的 toybox 通常提供 timeout，但不保证存在。缺失时必须退化为无超时
# 执行，绝不能因为工具缺失就让调用方判定失败——看门狗把它当成"不健康"，
# 就会每 3 次巡检杀掉并重启一次 daemon。
zcr_with_timeout() {
  local secs="$1"
  shift
  if command -v timeout >/dev/null 2>&1; then
    timeout "$secs" "$@"
  else
    "$@"
  fi
}

zcr_detect_framework() {
  if [ -d /data/adb/ksu ]; then
    echo "KernelSU"
  elif [ -d /data/adb/magisk ]; then
    echo "Magisk"
  elif [ -d /data/adb/ap ]; then
    echo "APatch"
  else
    echo "Unknown"
  fi
}

zcr_detect_su() {
  for p in /data/adb/ap/bin/su /data/adb/ksu/bin/su /data/adb/magisk/magisk su; do
    if command -v "$p" >/dev/null 2>&1 || [ -x "$p" ]; then
      echo "$p"
      return 0
    fi
  done
  return 1
}

zcr_read_pid() {
  [ -f "$ZCR_PID_FILE" ] && cat "$ZCR_PID_FILE"
}

zcr_start_supervisor() {
  local su_path
  su_path="$(zcr_detect_su)" || {
    zcr_log "未找到可用 su"
    return 1
  }

  local binary
  case "$(getprop ro.product.cpu.abi)" in
    arm64-v8a) binary="$ZCR_MODDIR/bin/arm64-v8a/novaaimcpd" ;;
    armeabi-v7a|armeabi) binary="$ZCR_MODDIR/bin/armeabi-v7a/novaaimcpd" ;;
    x86_64) binary="$ZCR_MODDIR/bin/x86_64/novaaimcpd" ;;
    *) binary="$ZCR_MODDIR/bin/arm64-v8a/novaaimcpd" ;;
  esac

  if [ ! -x "$binary" ]; then
    chmod 0755 "$binary" 2>/dev/null
  fi

  nohup "$su_path" -c "$binary --state $ZCR_INTERNAL_DIR" \
    >> "$ZCR_LOG_FILE" 2>&1 &
  echo $! > "$ZCR_PID_FILE"
  zcr_log "supervisor 已启动 pid=$(cat "$ZCR_PID_FILE")"
}

zcr_stop_supervisor() {
  local pid
  pid="$(zcr_read_pid)"
  if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
    kill -TERM "$pid" 2>/dev/null
    for i in 1 2 3 4 5; do
      sleep 1
      kill -0 "$pid" 2>/dev/null || break
    done
    kill -0 "$pid" 2>/dev/null && kill -KILL "$pid" 2>/dev/null
  fi
  rm -f "$ZCR_PID_FILE"
  zcr_log "supervisor 已停止"
}

zcr_print_summary() {
  echo "内部状态目录: $ZCR_INTERNAL_DIR"
  echo "用户目录: $ZCR_USER_DIR"
  echo "监听地址: http://127.0.0.1:5322/mcp"
  echo "Token 文件: $ZCR_TOKEN"
}
