#!/system/bin/sh

MODDIR=${0%/*}
ZCR_MODDIR="$MODDIR"
. "$MODDIR/common.sh"

zcr_prepare_internal >/dev/null 2>&1 || true

zcr_print "NovaAI-MCP v0.05"
zcr_print "模块目录: $ZCR_MODDIR"
zcr_print ""

# 状态
pid="$(zcr_read_pid 2>/dev/null)"
if [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null; then
  zcr_print "服务状态: 运行中 (pid=$pid)"
else
  zcr_print "服务状态: 未运行"
fi

# Root 框架
zcr_print "Root 框架: $(zcr_detect_framework)"
zcr_print "su 路径: $(zcr_detect_su 2>/dev/null || echo '未找到')"

# ABI
zcr_print "ABI: $(getprop ro.product.cpu.abi)"
zcr_print "Android: $(getprop ro.build.version.release) (API $(getprop ro.build.version.sdk))"

# 地址
zcr_print ""
zcr_print "MCP 地址: http://127.0.0.1:5322/mcp"
zcr_print "Unix socket: $ZCR_INTERNAL_DIR/mcp.sock"

# Token
if [ -f "$ZCR_TOKEN" ]; then
  token_preview="$(cat "$ZCR_TOKEN" | cut -c1-4)****$(cat "$ZCR_TOKEN" | rev | cut -c1-4 | rev)"
  zcr_print "Token: $token_preview"
  zcr_print "Token 文件: $ZCR_TOKEN"
else
  zcr_print "Token: 未生成"
fi

# 配置文件
if [ -f "$ZCR_CONFIG" ]; then
  zcr_print "配置文件: $ZCR_CONFIG"
  lan_enabled="$(grep -o '"enabled"[[:space:]]*:[[:space:]]*true' "$ZCR_CONFIG" | head -1 || echo '未知')"
  zcr_print "配置摘要: $lan_enabled"
fi

# 最近的审计
if [ -d "$ZCR_INTERNAL_DIR/audit" ]; then
  zcr_print ""
  zcr_print "最近审计日志:"
  ls -t "$ZCR_INTERNAL_DIR/audit" 2>/dev/null | head -3 | while read f; do
    zcr_print "  - $f"
  done
fi

zcr_print ""
zcr_print "提示：WebUI 中点击此按钮仅显示摘要，完整控制请使用 MCP 客户端"

exit 0