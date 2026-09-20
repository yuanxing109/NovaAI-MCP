#!/bin/bash
# NovaAI-MCP v0.05 构建脚本（Unix / Linux / macOS / CI）
#
# Windows 上请改用 build.ps1：Windows 通常没有 zip，且 Git 自带的 bsdtar
# 写出的 zip 不保留 Unix 权限位（实测 0755 被写成 -rw-rw-rw-）。
# 两者产出同一个模块包。
#
# staging 清单在两个脚本里各有一份，改动其中一处必须同步另一处，
# 否则 Windows 包与 Unix 包内容不一致。见 docs/KNOWN_ISSUES.md。

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

COMMIT="$(cd "$SRC_DIR" && git rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
LDFLAGS="-s -w -X main.Version=$VERSION -X main.Commit=$COMMIT"

log() { echo "[build] $*"; }

check_deps() {
  command -v go >/dev/null 2>&1 || { echo "错误: 未找到 go 命令"; exit 1; }
  command -v zip >/dev/null 2>&1 || { echo "错误: 未找到 zip 命令"; exit 1; }
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
  for arch in arm64-v8a armeabi-v7a x86_64; do
    if [ ! -f "$BIN_DIR/$arch/novaaimcpd" ]; then
      echo "错误: 缺少 $arch 二进制"; exit 1
    fi
  done

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
  if [ -d "$ROOT_DIR/docs" ]; then
    mkdir -p "$STAGE/docs"
    cp "$ROOT_DIR/docs/"* "$STAGE/docs/"
  fi

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
  for arch in arm64-v8a armeabi-v7a x86_64; do
    if [ -f "$BIN_DIR/$arch/7zz" ]; then
      cp "$BIN_DIR/$arch/7zz" "$STAGE/bin/$arch/"
    fi
  done
  if [ -d "$BIN_DIR/tools" ]; then
    mkdir -p "$STAGE/bin/tools"
    cp "$BIN_DIR/tools/"*.jar "$STAGE/bin/tools/"
  fi
  if [ -d "$BIN_DIR/wrappers" ]; then
    mkdir -p "$STAGE/bin/wrappers"
    cp "$BIN_DIR/wrappers/"* "$STAGE/bin/wrappers/"
  fi
  if [ -d "$ROOT_DIR/skills" ]; then
    mkdir -p "$STAGE/skills"
    cp "$ROOT_DIR/skills/"*.md "$STAGE/skills/"
  fi
  # WebUI：KernelSU 只认模块根目录的 webroot/，且必须存在 index.html，
  # 否则模块页面入口不出现。权限与 SELinux context 由 KernelSU 自动设置，
  # 因此这里不做 chmod（也**不要**把 webroot 加进可执行矩阵）。
  if [ -d "$ROOT_DIR/webroot" ]; then
    if [ ! -f "$ROOT_DIR/webroot/index.html" ]; then
      echo "错误: webroot/ 存在但没有 index.html，KernelSU 不会显示模块页面"; exit 1
    fi
    mkdir -p "$STAGE/webroot"
    cp -r "$ROOT_DIR/webroot/." "$STAGE/webroot/"
  else
    echo "错误: 缺少 $ROOT_DIR/webroot（KernelSU WebUI 入口）"; exit 1
  fi

  chmod 0755 "$STAGE"/*.sh "$STAGE"/bin/*/novaaimcpd
  # 7zz 也必须 0755：build.ps1 的 Test-Executable / Test-Package 会断言这一点，
  # 而旧实现漏了它，于是 Linux 检出上跑 build.sh 产出的包会被 Windows 侧
  # 校验器判为失败（git 索引里它是 100644，本机 core.fileMode=false）。
  # 见 docs/KNOWN_ISSUES.md。
  for f in "$STAGE"/bin/*/7zz; do
    if [ -f "$f" ]; then chmod 0755 "$f"; fi
  done
  if [ -d "$STAGE/bin/wrappers" ]; then
    chmod 0755 "$STAGE"/bin/wrappers/*
  fi

  # META-INF 只有仓库根一份，作为唯一事实来源直接复制。
  # 旧实现在这里用 heredoc 生成了第二份，且把 KernelSU 的 util_functions.sh
  # 路径写错成 ksu_util_functions.sh，也没有校验 install_module 是否存在。
  if [ ! -f "$ROOT_DIR/META-INF/com/google/android/update-binary" ]; then
    echo "错误: 缺少 $ROOT_DIR/META-INF/com/google/android/update-binary"; exit 1
  fi
  mkdir -p "$STAGE/META-INF"
  cp -r "$ROOT_DIR/META-INF/." "$STAGE/META-INF/"
  chmod 0755 "$STAGE/META-INF/com/google/android/update-binary"

  OUTPUT="$DIST_DIR/$MODULE_NAME.zip"
  rm -f "$OUTPUT"
  cd "$STAGE"
  zip -r9 "$OUTPUT" . >/dev/null
  log "打包完成: $OUTPUT ($(du -h "$OUTPUT" | cut -f1))"
}

clean() {
  log "清理构建产物"
  rm -rf "$BIN_DIR" "$DIST_DIR"
  log "完成"
}

case "${1:-all}" in
  all) check_deps; build_go; build_zip ;;
  go)  check_deps; build_go ;;
  zip) check_deps; build_zip ;;
  clean) clean ;;
  *) echo "用法: $0 [all|go|zip|clean]"; exit 1 ;;
esac
log "全部完成"