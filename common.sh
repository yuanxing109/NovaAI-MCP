#!/system/bin/sh

ZCR_MODDIR="${ZCR_MODDIR:-${0%/*}}"
ZCR_INTERNAL_DIR="/data/adb/novaai-mcp"
ZCR_USER_DIR="/storage/emulated/0/novaaiAI"
ZCR_PID_FILE="$ZCR_INTERNAL_DIR/novaaimcpd.pid"
ZCR_LOG_FILE="$ZCR_INTERNAL_DIR/module.log"
ZCR_CONFIG="$ZCR_INTERNAL_DIR/config.json"

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

# 判断 pid 是否确实是本模块的 daemon。
#
# PID 文件会残留（daemon 被 SIGKILL 时来不及清理），而 PID 会被系统复用；
# 不做身份校验就可能 kill 掉一个无关进程。
zcr_is_daemon() {
  local pid="$1"
  [ -n "$pid" ] || return 1
  kill -0 "$pid" 2>/dev/null || return 1
  # comm 由内核按可执行文件名设置，比 cmdline 少一层 NUL 分隔解析
  if [ "$(cat "/proc/$pid/comm" 2>/dev/null)" = "novaaimcpd" ]; then
    return 0
  fi
  case "$(tr '\0' ' ' < "/proc/$pid/cmdline" 2>/dev/null)" in
    *novaaimcpd*) return 0 ;;
  esac
  return 1
}

# 输出 daemon 的 PID。文件不存在、进程已退出或 PID 已被复用时输出空。
zcr_read_pid() {
  local pid
  [ -f "$ZCR_PID_FILE" ] || return 0
  pid="$(cat "$ZCR_PID_FILE" 2>/dev/null)"
  zcr_is_daemon "$pid" || return 0
  echo "$pid"
}

# 版本号唯一来源是 module.prop，脚本内不再硬编码版本。
zcr_module_version() {
  local v
  v="$(grep '^version=' "$ZCR_MODDIR/module.prop" 2>/dev/null | head -n1 | cut -d= -f2)"
  if [ -n "$v" ]; then echo "$v"; else echo "unknown"; fi
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

  # PID 文件由 daemon 自己写（唯一 owner），这里只负责等它出现。
  #
  # 旧实现在这里写 `$!`，那是 `su` 进程的 PID；daemon 是 su 的孙进程，
  # 停止信号因此可能到不了 daemon。见 docs/KNOWN_ISSUES.md。
  rm -f "$ZCR_PID_FILE" 2>/dev/null
  nohup "$su_path" -c "$binary --state $ZCR_INTERNAL_DIR" \
    >> "$ZCR_LOG_FILE" 2>&1 &

  local i pid
  i=0
  while [ "$i" -lt 50 ]; do
    pid="$(zcr_read_pid 2>/dev/null)"
    if [ -n "$pid" ]; then
      zcr_log "daemon 已启动 pid=$pid"
      return 0
    fi
    sleep 0.1
    i=$((i + 1))
  done
  zcr_log "daemon 启动后 5 秒内未写入 PID 文件"
  return 1
}

zcr_stop_supervisor() {
  local pid i
  pid="$(zcr_read_pid)"
  if [ -n "$pid" ]; then
    kill -TERM "$pid" 2>/dev/null
    i=0
    while [ "$i" -lt 5 ]; do
      sleep 1
      zcr_is_daemon "$pid" || break
      i=$((i + 1))
    done
    if zcr_is_daemon "$pid"; then
      kill -KILL "$pid" 2>/dev/null
    fi
  fi
  rm -f "$ZCR_PID_FILE"
  zcr_log "daemon 已停止"
}
