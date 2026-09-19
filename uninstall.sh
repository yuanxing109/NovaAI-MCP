#!/system/bin/sh

MODDIR=${0%/*}
ZCR_MODDIR="$MODDIR"
. "$MODDIR/common.sh"

# 只需要内部目录（写停止标志、日志、PID）；不要创建用户目录——
# zcr_prepare_internal 会 mkdir 用户目录，卸载时不应该触碰用户数据。
mkdir -p "$ZCR_INTERNAL_DIR" 2>/dev/null

# 写入手动停止标志
touch "$ZCR_INTERNAL_DIR/manual-stop" 2>/dev/null

# 停止 supervisor
zcr_stop_supervisor

# ============ 读取卸载配置 ============
# 字段名以 Go 侧 config.UninstallConfig 为准（src/internal/config/types.go）：
#   purgeInternalState / purgeAuditLogs / purgeCrashDumps / purgeUserData
# Go 侧这四个字段的默认值都是 false（src/internal/config/default.go），
# 所以这里同样默认"保留数据"：要删必须在 config.json 里显式写 true。
UNINSTALL_CFG="$ZCR_INTERNAL_DIR/config.json"
purge_state="false"   # 默认保留内部状态目录
purge_audit="false"   # 默认保留审计日志
purge_crash="false"   # 默认保留崩溃转储

if [ -f "$UNINSTALL_CFG" ]; then
  grep -q '"purgeInternalState"[[:space:]]*:[[:space:]]*true' "$UNINSTALL_CFG" 2>/dev/null && purge_state="true"
  grep -q '"purgeAuditLogs"[[:space:]]*:[[:space:]]*true' "$UNINSTALL_CFG" 2>/dev/null && purge_audit="true"
  grep -q '"purgeCrashDumps"[[:space:]]*:[[:space:]]*true' "$UNINSTALL_CFG" 2>/dev/null && purge_crash="true"
fi

# ============ 用户数据开关（默认保留） ============
# 与其他三个开关同源：都读 config.json 的 uninstall 段。
# purgeUserData 默认 false，要删必须在 config.json 里显式写 true。
purge_user="false"
if [ -f "$UNINSTALL_CFG" ]; then
  grep -q '"purgeUserData"[[:space:]]*:[[:space:]]*true' "$UNINSTALL_CFG" 2>/dev/null && purge_user="true"
fi

# 清理 wrapper 脚本
# 只清理当前框架的 PATH 目录（卸载时没有安装器的框架环境变量，按目录识别框架）。
# Magisk 的 wrapper 在模块 system/bin 内，随模块目录一起被移除，无需处理。
case "$(zcr_detect_framework 2>/dev/null)" in
  KernelSU) WRAPPER_DIR="/data/adb/ksu/bin" ;;
  APatch)   WRAPPER_DIR="/data/adb/ap/bin" ;;
  *)        WRAPPER_DIR="" ;;
esac
if [ -n "$WRAPPER_DIR" ]; then
  zcr_print "清理 wrapper 脚本 ($WRAPPER_DIR)..."
  for f in apktool jadx smali baksmali dexdump sqlite3; do
    rm -f "$WRAPPER_DIR/$f" 2>/dev/null
  done
  zcr_print "✅ wrapper 已清理"
else
  zcr_print "无需清理 wrapper（wrapper 随模块目录移除）"
fi

# 清理 socket 和 PID
rm -f "$ZCR_INTERNAL_DIR/mcp.sock" 2>/dev/null
rm -f "$ZCR_PID_FILE" 2>/dev/null

# ============ 清理内部状态目录（默认保留） ============
if [ "$purge_state" = "true" ]; then
  case "$ZCR_INTERNAL_DIR" in
    /data/adb/novaai-mcp)
      rm -rf "$ZCR_INTERNAL_DIR"
      zcr_print "✅ 内部状态目录已清理（含审计日志/崩溃转储）"
      ;;
    *)
      zcr_print "⚠️ 内部目录校验失败，跳过清理"
      ;;
  esac
else
  zcr_print "已保留内部状态目录：$ZCR_INTERNAL_DIR"

  # 目录保留时，仍可按配置单独清理审计日志 / 崩溃转储
  if [ "$purge_audit" = "true" ]; then
    case "$ZCR_INTERNAL_DIR" in
      /data/adb/novaai-mcp)
        rm -rf "$ZCR_INTERNAL_DIR/audit" 2>/dev/null
        rm -f "$ZCR_INTERNAL_DIR"/*.log 2>/dev/null
        zcr_print "✅ 审计日志已清理"
        ;;
      *)
        zcr_print "⚠️ 内部目录校验失败，跳过审计日志清理"
        ;;
    esac
  else
    zcr_print "已保留审计日志：$ZCR_INTERNAL_DIR/audit"
  fi

  if [ "$purge_crash" = "true" ]; then
    case "$ZCR_INTERNAL_DIR" in
      /data/adb/novaai-mcp)
        rm -rf "$ZCR_INTERNAL_DIR/crash" 2>/dev/null
        zcr_print "✅ 崩溃转储已清理"
        ;;
      *)
        zcr_print "⚠️ 内部目录校验失败，跳过崩溃转储清理"
        ;;
    esac
  else
    zcr_print "已保留崩溃转储：$ZCR_INTERNAL_DIR/crash"
  fi
fi

# ============ 清理用户数据（默认保留） ============
if [ "$purge_user" = "true" ]; then
  case "$ZCR_USER_DIR" in
    /storage/emulated/0/novaaiAI)
      rm -rf "$ZCR_USER_DIR"
      zcr_print "✅ 用户数据已清理（purgeUserData=true）"
      ;;
    *)
      zcr_print "⚠️ 用户目录校验失败，跳过清理"
      ;;
  esac
else
  zcr_print "已保留用户目录：$ZCR_USER_DIR"
fi

zcr_print ""
zcr_print "NovaAI-MCP 已卸载"
zcr_print ""
zcr_print "残留文件（如有）："
zcr_print "  - 配置: $ZCR_INTERNAL_DIR/config.json"
zcr_print "  - 用户: $ZCR_USER_DIR"
zcr_print ""
zcr_print "默认不删除任何用户数据；如需完全清理，请手动删除上述目录。"
zcr_print "（确需卸载时自动删除用户目录，请在 config.json 的 uninstall 段设置 purgeUserData: true）"

exit 0
