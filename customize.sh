#!/system/bin/sh
# NovaAI-MCP 模块安装脚本（customize.sh）
# 适配 Magisk / KernelSU / APatch，默认按 Magisk 处理。
# 约束：POSIX sh（busybox ash），不使用 bash 专有语法。

# 安装器都会导出 MODPATH（Magisk/KernelSU/APatch），优先使用；
# 回退到脚本自身所在目录。
MODDIR="${MODPATH:-${0%/*}}"
ZIPFILE=${ZIPFILE:-$3}

# ============ Root 框架识别 ============
# Magisk   : MAGISK_VER / MAGISK_VER_CODE
# KernelSU : KSU_VER / KSU_VER_CODE / KSU
# APatch   : APATCH / APATCH_VER_CODE
# 三者都识别不出时按 Magisk 处理：Magisk 最通用，本项目主要分发目标也是 Magisk。
FRAMEWORK="Magisk"
FRAMEWORK_BY="默认（未检测到框架环境变量）"
if [ -n "${KSU_VER:-}${KSU_VER_CODE:-}${KSU:-}" ]; then
  FRAMEWORK="KernelSU"
  FRAMEWORK_BY="KSU_VER=${KSU_VER:-未设置}"
elif [ -n "${APATCH:-}${APATCH_VER_CODE:-}" ]; then
  FRAMEWORK="APatch"
  FRAMEWORK_BY="APATCH=${APATCH:-未设置}"
elif [ -n "${MAGISK_VER:-}${MAGISK_VER_CODE:-}" ]; then
  FRAMEWORK="Magisk"
  FRAMEWORK_BY="MAGISK_VER=${MAGISK_VER:-未设置}"
fi

# 框架专属的 wrapper 安装目录：
#   KernelSU -> /data/adb/ksu/bin  （KernelSU 把它加入 PATH）
#   APatch   -> /data/adb/ap/bin   （APatch 把它加入 PATH）
#   Magisk   -> 模块的 system/bin  （Magisk 没有独立的 adb bin 目录，
#               官方做法是放进模块 system/ 由 magic mount 覆盖到 /system/bin）
case "$FRAMEWORK" in
  KernelSU) WRAPPER_DIR="/data/adb/ksu/bin" ;;
  APatch)   WRAPPER_DIR="/data/adb/ap/bin" ;;
  *)        WRAPPER_DIR="$MODDIR/system/bin" ;;
esac

# 仅用于日志展示，说明 Magisk 下 wrapper 最终出现在哪里
case "$FRAMEWORK" in
  KernelSU|APatch) WRAPPER_DESC="$WRAPPER_DIR" ;;
  *)               WRAPPER_DESC="$WRAPPER_DIR -> /system/bin（magic mount）" ;;
esac

# ============ 模块内容校验 ============
# 安装器在调用本脚本之前已经把 ZIP 解压到 $MODDIR（Magisk 内部执行的正是
# `unzip -o "$ZIPFILE" -x 'META-INF/*' -d "$MODPATH"`），所以这里不重复解压。
# unzip 在 Android 上并不保证存在（toybox 没有 unzip），二次解压既多余又可能静默失败。
# 注意：如果将来改成 SKIPUNZIP=1，必须自己补上解压逻辑。
if [ ! -f "$MODDIR/module.prop" ]; then
  ui_print "! 模块文件缺失：$MODDIR/module.prop"
  ui_print "! 安装器未解压模块内容，安装终止"
  exit 1
fi

# 版本号以 module.prop 为唯一来源，避免脚本内多处硬编码漂移
MODVER="$(grep '^version=' "$MODDIR/module.prop" 2>/dev/null | head -n1 | cut -d= -f2)"
[ -n "$MODVER" ] || MODVER="0.05"

# ============ 更新检测 ============
IS_UPDATE="false"
OLD_CONFIG=""
OLD_STATE_DIR="/data/adb/novaai-mcp"

if [ -d "/data/adb/modules/novaai.mcp" ] || [ -d "/data/adb/modules_update/novaai.mcp" ]; then
  IS_UPDATE="true"
fi

if [ -f "$OLD_STATE_DIR/config.json" ]; then
  OLD_CONFIG="$OLD_STATE_DIR/config.json"
fi

# ============ UI 输出 ============
ui_print "========================================="
ui_print " NovaAI-MCP v$MODVER"
ui_print "========================================="
ui_print ""
ui_print "- 架构: $(getprop ro.product.cpu.abi)"
ui_print "- Root: $FRAMEWORK ($FRAMEWORK_BY)"
ui_print "- Wrapper 目录: $WRAPPER_DESC"
ui_print "- 模式: $([ "$IS_UPDATE" = "true" ] && echo '更新' || echo '全新安装')"

# ============ 解压模块文件 ============
# 安装器已解压完毕（见上方"模块内容校验"），此处不再二次解压。

# ============ 设置权限 ============
ui_print "- 设置权限..."

for f in service.sh post-fs-data.sh action.sh uninstall.sh common.sh; do
  [ -f "$MODDIR/$f" ] && chmod 0755 "$MODDIR/$f"
done

ARCH=$(getprop ro.product.cpu.abi)
case "$ARCH" in
  arm64-v8a)
    [ -f "$MODDIR/bin/arm64-v8a/novaaimcpd" ] && chmod 0755 "$MODDIR/bin/arm64-v8a/novaaimcpd"
    [ -f "$MODDIR/bin/arm64-v8a/7zz" ] && chmod 0755 "$MODDIR/bin/arm64-v8a/7zz"
    ;;
  armeabi-v7a|armeabi)
    [ -f "$MODDIR/bin/armeabi-v7a/novaaimcpd" ] && chmod 0755 "$MODDIR/bin/armeabi-v7a/novaaimcpd"
    [ -f "$MODDIR/bin/armeabi-v7a/7zz" ] && chmod 0755 "$MODDIR/bin/armeabi-v7a/7zz"
    ;;
  x86_64)
    [ -f "$MODDIR/bin/x86_64/novaaimcpd" ] && chmod 0755 "$MODDIR/bin/x86_64/novaaimcpd"
    [ -f "$MODDIR/bin/x86_64/7zz" ] && chmod 0755 "$MODDIR/bin/x86_64/7zz"
    ;;
esac

[ -f "$MODDIR/bin/tools/apktool.jar" ] && chmod 0644 "$MODDIR/bin/tools/apktool.jar"
[ -f "$MODDIR/bin/tools/smali.jar" ] && chmod 0644 "$MODDIR/bin/tools/smali.jar"
[ -f "$MODDIR/bin/tools/baksmali.jar" ] && chmod 0644 "$MODDIR/bin/tools/baksmali.jar"

for w in apktool jadx smali baksmali dexdump sqlite3; do
  [ -f "$MODDIR/bin/wrappers/$w" ] && chmod 0755 "$MODDIR/bin/wrappers/$w"
done

# ============ 清理旧 Wrapper ============
# 只有把 wrapper 装到模块目录之外的框架（KernelSU/APatch）才需要清理；
# Magisk 的 wrapper 在模块 system/bin 内，随模块目录一起更新/移除。
if [ "$IS_UPDATE" = "true" ] && [ "$FRAMEWORK" != "Magisk" ]; then
  ui_print "- 清理旧 wrapper ($WRAPPER_DIR)..."
  for f in apktool jadx smali baksmali dexdump sqlite3; do
    rm -f "$WRAPPER_DIR/$f" 2>/dev/null
  done
  ui_print "  ✅ 旧 wrapper 已清理"
fi

# ============ 安装 Wrapper ============
ui_print "- 安装 wrapper 到 $WRAPPER_DIR..."
mkdir -p "$WRAPPER_DIR"
for w in apktool jadx smali baksmali dexdump sqlite3; do
  if [ -f "$MODDIR/bin/wrappers/$w" ]; then
    # Magisk 用 magic mount 覆盖 /system/bin，不能遮蔽 ROM 自带命令
    if [ "$FRAMEWORK" = "Magisk" ] && [ -e "/system/bin/$w" ]; then
      ui_print "  ⏭ 跳过 $w（/system/bin/$w 已存在，无需覆盖）"
      continue
    fi
    cp "$MODDIR/bin/wrappers/$w" "$WRAPPER_DIR/$w"
    chmod 0755 "$WRAPPER_DIR/$w"
  fi
done
ui_print "  ✅ wrapper 已安装"

# ============ 创建目录 ============
ui_print "- 创建目录结构..."
mkdir -p "$OLD_STATE_DIR"
chmod 0700 "$OLD_STATE_DIR"
mkdir -p "$OLD_STATE_DIR/workspace"
mkdir -p "$OLD_STATE_DIR/audit"
mkdir -p "$OLD_STATE_DIR/crash"
mkdir -p "$OLD_STATE_DIR/skills"
mkdir -p "$OLD_STATE_DIR/tools"
mkdir -p /storage/emulated/0/novaaiAI
mkdir -p /storage/emulated/0/novaaiAI/reverse

# ============ 配置处理 ============
if [ "$IS_UPDATE" = "true" ]; then
  ui_print "- 更新模式：保留旧配置"
  if [ -n "$OLD_CONFIG" ]; then
    ui_print "  ✅ 保留配置: $OLD_CONFIG"
    ui_print "  ℹ️  新功能将使用默认值（首次启动时自动合并）"
    
    # 备份旧配置
    cp "$OLD_CONFIG" "$OLD_CONFIG.bak" 2>/dev/null
    ui_print "  ✅ 已备份: $OLD_CONFIG.bak"
  else
    ui_print "  ℹ️  无旧配置，将生成新配置"
  fi
else
  ui_print "- 全新安装，配置将在首次启动时生成"
fi

# ============ 安装技能文件 ============
ui_print "- 安装技能文件..."
if [ -d "$MODDIR/skills" ]; then
  cp "$MODDIR/skills/"*.md "$OLD_STATE_DIR/skills/" 2>/dev/null
  ui_print "  ✅ 技能文件已安装"
fi

# ============ 安装工具 ============
ui_print "- 安装工具..."
if [ -d "$MODDIR/bin/tools" ]; then
  cp "$MODDIR/bin/tools/"*.jar "$OLD_STATE_DIR/tools/" 2>/dev/null
  ui_print "  ✅ 工具已安装"
fi

# ============ 版本记录 ============
echo "$MODDIR" > "$OLD_STATE_DIR/module_path"
echo "$MODVER" > "$OLD_STATE_DIR/version"
echo "$(date '+%Y-%m-%d %H:%M:%S')" > "$OLD_STATE_DIR/install_time"

# ============ 完成 ============
ui_print ""
ui_print "========================================="
ui_print " 安装完成"
ui_print "========================================="
ui_print ""
ui_print "- MCP 地址: http://127.0.0.1:5322/mcp"
ui_print "- Unix Socket: $OLD_STATE_DIR/mcp.sock"
ui_print "- Token 文件: $OLD_STATE_DIR/token"
ui_print "- 配置文件: $OLD_STATE_DIR/config.json"
ui_print "- 技能目录: $OLD_STATE_DIR/skills/"
ui_print "- 工具目录: $OLD_STATE_DIR/tools/"
ui_print ""
if [ "$IS_UPDATE" = "true" ]; then
  ui_print "- 更新说明："
  ui_print "  1. 旧配置已保留"
  ui_print "  2. 新功能使用默认值"
  ui_print "  3. 首次启动时自动合并配置"
  ui_print "  4. 如有问题，恢复备份: $OLD_CONFIG.bak"
fi
ui_print "- 重启后生效"
ui_print "========================================="

exit 0
