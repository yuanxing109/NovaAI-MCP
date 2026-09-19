#!/system/bin/sh

MODDIR=${0%/*}
ZCR_MODDIR="$MODDIR"
. "$MODDIR/common.sh"

[ -e "$MODDIR/disable" ] && exit 0
[ -e "$MODDIR/remove" ] && exit 0

umask 077
zcr_prepare_internal || exit 0

# 确保目录权限正确
chmod 0700 "$ZCR_INTERNAL_DIR" 2>/dev/null
chmod 0600 "$ZCR_CONFIG" 2>/dev/null
chmod 0600 "$ZCR_TOKEN" 2>/dev/null

exit 0