#!/bin/bash
# NovaAI-MCP 构建脚本（Unix / Linux / macOS / CI）
#
# Windows 上请改用 build.ps1：Windows 通常没有 zip，且 Git 自带的 bsdtar
# 写出的 zip 不保留 Unix 权限位（实测 0755 被写成 -rw-rw-rw-）。
# 两者产出同一个模块包。
#
# staging 清单在两个脚本里各有一份，改动其中一处必须同步另一处，
# 否则 Windows 包与 Unix 包内容不一致。见 docs/KNOWN_ISSUES.md。
#
# 两个模式：
#   package —— **只打包，不编译**。CI 走这条：daemon 二进制由本地编译后
#              提交进仓库（bin/<abi>/novaaimcpd），CI 不装 Go、不编译。
#   all     —— 本地用：先编译 3 个 ABI，再打包。
# 无论哪个模式，bin/ 下的随包资产（7zz、jar、wrapper）与 3 个二进制都
# **必须在位** —— require_module_files 少一个就退出，不允许产出残包。

set -e

export GOPROXY="https://goproxy.cn,https://mirrors.aliyun.com/goproxy/,https://goproxy.io,direct"
export GOSUMDB="sum.golang.google.cn"
export GO111MODULE=on

ROOT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC_DIR="$ROOT_DIR/src"
BIN_DIR="$ROOT_DIR/bin"
DIST_DIR="$ROOT_DIR/dist"

# 版本号唯一来源是 module.prop，与 customize.sh / action.sh / build.ps1 一致。
VERSION="$(sed -n 's/^version=//p' "$ROOT_DIR/module.prop" 2>/dev/null | head -n1)"
if [ -z "$VERSION" ]; then
  echo "错误: 无法从 module.prop 读取 version"
  exit 1
fi
MODULE_NAME="NovaAI-MCP-v$VERSION"

COMMIT="$(cd "$ROOT_DIR" && git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
LDFLAGS="-s -w -X main.Version=$VERSION -X main.Commit=$COMMIT"

ARCHES="arm64-v8a armeabi-v7a x86_64"

log() { echo "[build] $*"; }

check_zip_tool() {
  command -v zip >/dev/null 2>&1 || { echo "错误: 未找到 zip 命令"; exit 1; }
}

check_go_tool() {
  # 先报"源码不在本仓库"这一条：它比"没装 zip"更根本，不该被后面的检查盖住。
  if [ ! -d "$SRC_DIR" ]; then
    echo "错误: 缺少 $SRC_DIR —— 源码不在本仓库，本仓库只负责打包与发布。" >&2
    echo "      要编译 daemon，请在含源码的开发工作区里跑 all；本仓库用 package。" >&2
    exit 1
  fi
  check_zip_tool
  command -v go >/dev/null 2>&1 || { echo "错误: 未找到 go 命令"; exit 1; }
}

# 打包前把"模块包里必须有"的东西逐一点名检查。
#
# 为什么必须是硬失败而不是条件复制：这些文件里有 7z、反编译 jar、wrapper、
# 三个 ABI 的 daemon。旧实现在 build_zip 里对它们是 `if [ -f ]` 条件复制，
# 缺件时打包**照样成功**、产物校验**照样通过**（校验器只看存在的条目），
# 于是一个没有 7z / 没有 wrapper 的残包会被发出去 —— 安装后功能静默缺失。
# 缺件只能报错，不能降级。
require_module_files() {
  log "检查模块必需文件"
  missing=0
  need_file() {
    if [ ! -f "$1" ]; then
      echo "错误: 缺少必需文件 $1"
      missing=1
    fi
  }

  for f in module.prop customize.sh service.sh post-fs-data.sh uninstall.sh \
           action.sh common.sh sepolicy.rule README.md LICENSE; do
    need_file "$ROOT_DIR/$f"
  done
  need_file "$ROOT_DIR/META-INF/com/google/android/update-binary"

  # KernelSU 只认模块根目录的 webroot/，且必须存在 index.html
  need_file "$ROOT_DIR/webroot/index.html"

  # 库文件（README.md 之外，docs 是随包分发的文档目录）
  if [ ! -d "$ROOT_DIR/docs" ]; then
    echo "错误: 缺少 $ROOT_DIR/docs"; missing=1
  fi
  if [ ! -d "$ROOT_DIR/skills" ]; then
    echo "错误: 缺少 $ROOT_DIR/skills"; missing=1
  fi

  # 三个 ABI 的 daemon（编译产物，由本地编译后提交进仓库）
  for arch in $ARCHES; do
    need_file "$BIN_DIR/$arch/novaaimcpd"
    need_file "$BIN_DIR/$arch/7zz"
  done

  # 随包分发的反编译 jar（wrapper 指向它们）
  for j in apktool.jar smali.jar baksmali.jar; do
    need_file "$BIN_DIR/tools/$j"
  done

  # 安装到 PATH 的 wrapper
  for w in apktool baksmali dexdump jadx smali sqlite3; do
    need_file "$BIN_DIR/wrappers/$w"
  done

  if [ "$missing" -ne 0 ]; then
    cat >&2 <<'MSG'

模块包缺件，已中止。补齐方式：
  · daemon  → 在 src/ 下编译三个 ABI（build.sh all / build.ps1 all 的编译段）
  · 7zz/jar/wrapper → 这些是随包分发的资产，应随仓库一起提供
不要用"少打几个文件"绕过：残包装到设备上是静默的功能缺失。
MSG
    exit 1
  fi
}

build_go() {
  log "构建 Go 二进制 (version=$VERSION commit=$COMMIT)"
  mkdir -p "$BIN_DIR/arm64-v8a" "$BIN_DIR/armeabi-v7a" "$BIN_DIR/x86_64"
  cd "$SRC_DIR"
  go mod tidy

  log "  -> arm64-v8a"
  CGO_ENABLED=0 GOOS=android GOARCH=arm64 \
    go build -trimpath -ldflags="$LDFLAGS" \
    -o "$BIN_DIR/arm64-v8a/novaaimcpd" ./cmd/novaaimcpd

  log "  -> armeabi-v7a (使用 GOOS=linux)"
  CGO_ENABLED=0 GOOS=linux GOARCH=arm GOARM=7 \
    go build -trimpath -ldflags="$LDFLAGS" \
    -o "$BIN_DIR/armeabi-v7a/novaaimcpd" ./cmd/novaaimcpd

  log "  -> x86_64"
  CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -trimpath -ldflags="$LDFLAGS" \
    -o "$BIN_DIR/x86_64/novaaimcpd" ./cmd/novaaimcpd

  log "Go 构建完成"
}

build_zip() {
  log "打包模块 ZIP"

  mkdir -p "$DIST_DIR"
  STAGE="$(mktemp -d)"
  trap 'rm -rf "$STAGE"' EXIT

  cp "$ROOT_DIR/module.prop" "$STAGE/"
  cp "$ROOT_DIR/customize.sh" "$STAGE/"
  cp "$ROOT_DIR/service.sh" "$STAGE/"
  cp "$ROOT_DIR/post-fs-data.sh" "$STAGE/"
  cp "$ROOT_DIR/uninstall.sh" "$STAGE/"
  cp "$ROOT_DIR/action.sh" "$STAGE/"
  cp "$ROOT_DIR/common.sh" "$STAGE/"
  cp "$ROOT_DIR/sepolicy.rule" "$STAGE/"

  # 随模块附带文档，便于在设备上离线查阅配置说明。
  # LICENSE 必须随包分发：MIT 要求版权与许可声明随软件副本一同提供，
  # 而本 ZIP 就是一份副本。
  cp "$ROOT_DIR/README.md" "$STAGE/"
  cp "$ROOT_DIR/LICENSE" "$STAGE/"
  mkdir -p "$STAGE/docs"
  cp "$ROOT_DIR/docs/"* "$STAGE/docs/"

  mkdir -p "$STAGE/bin/arm64-v8a" "$STAGE/bin/armeabi-v7a" "$STAGE/bin/x86_64"
  cp "$BIN_DIR/arm64-v8a/novaaimcpd"   "$STAGE/bin/arm64-v8a/"
  cp "$BIN_DIR/armeabi-v7a/novaaimcpd" "$STAGE/bin/armeabi-v7a/"
  cp "$BIN_DIR/x86_64/novaaimcpd"      "$STAGE/bin/x86_64/"

  # customize.sh 在安装时还会用到下面这些内容，必须一起打进模块：
  #   bin/<abi>/7zz        按 ABI 分发的 7zz
  #   bin/tools/*.jar      随模块分发的 apktool/smali/baksmali
  #                        （wrapper 与 daemon 都直接读模块内这一份，不再复制到状态目录）
  #   bin/wrappers/*       安装到 PATH 的 wrapper（apktool/jadx/smali/baksmali/dexdump/sqlite3）
  #   skills/*.md          安装到状态目录的技能文件
  #   webroot/*            KernelSU WebUI（须含 index.html）
  # 这些是**无条件**复制：require_module_files 已经把在位性检查过了。
  for arch in $ARCHES; do
    cp "$BIN_DIR/$arch/7zz" "$STAGE/bin/$arch/"
  done
  mkdir -p "$STAGE/bin/tools" "$STAGE/bin/wrappers" "$STAGE/skills"
  cp "$BIN_DIR/tools/"*.jar "$STAGE/bin/tools/"
  cp "$BIN_DIR/wrappers/"* "$STAGE/bin/wrappers/"
  cp "$ROOT_DIR/skills/"*.md "$STAGE/skills/"
  # WebUI：KernelSU 只认模块根目录的 webroot/，且必须存在 index.html，
  # 否则模块页面入口不出现。权限与 SELinux context 由 KernelSU 自动设置，
  # 因此这里不做 chmod（也**不要**把 webroot 加进可执行矩阵）。
  mkdir -p "$STAGE/webroot"
  cp -r "$ROOT_DIR/webroot/." "$STAGE/webroot/"

  chmod 0755 "$STAGE"/*.sh "$STAGE"/bin/*/novaaimcpd "$STAGE"/bin/*/7zz
  chmod 0755 "$STAGE"/bin/wrappers/*

  # META-INF 只有仓库根一份，作为唯一事实来源直接复制。
  # 旧实现在这里用 heredoc 生成了第二份，且把 KernelSU 的 util_functions.sh
  # 路径写错成 ksu_util_functions.sh，也没有校验 install_module 是否存在。
  mkdir -p "$STAGE/META-INF"
  cp -r "$ROOT_DIR/META-INF/." "$STAGE/META-INF/"
  chmod 0755 "$STAGE/META-INF/com/google/android/update-binary"

  OUTPUT="$DIST_DIR/$MODULE_NAME.zip"
  rm -f "$OUTPUT"
  cd "$STAGE"
  zip -r9 "$OUTPUT" . >/dev/null
  log "打包完成: $OUTPUT ($(du -h "$OUTPUT" | cut -f1))"
}

# clean 只删 dist/（本地产物，不入库）。
#
# 不要删 bin/ —— 从 v0.07 起 bin/ 里是**版本化内容**：三个 ABI 的编译产物
# （由本地编译后提交）与随包分发的 7zz/jar/wrapper。删掉它等于删掉工作区里
# 被 git 跟踪的文件，`git status` 立刻一片 D。
clean() {
  log "清理构建产物（只删 dist/）"
  rm -rf "$DIST_DIR"
  log "完成"
}

case "${1:-all}" in
  all)          check_go_tool; build_go; require_module_files; build_zip ;;
  go)           check_go_tool; build_go ;;
  package|zip)  check_zip_tool; require_module_files; build_zip ;;
  # 只做"模块必需文件是否在位"的判定，不打包、不需要 zip。留这个模式是为了
  # 能单独跑这一道闸门（也便于对它做变异测试：拿掉一个文件必须失败）。
  check)        require_module_files ;;
  clean)        clean ;;
  *) echo "用法: $0 [all|go|package|zip|check|clean]"; exit 1 ;;
esac
log "全部完成"
