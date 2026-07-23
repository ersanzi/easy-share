#!/usr/bin/env bash
#
# EasyShare macOS 构建脚本（在 Mac 或 GitHub Actions macos runner 上运行）。
# 产出：build/bin/easyshare-core（后台服务）、build/bin/easyshare.app（桌面端）、build/bin/EasyShare.dmg（安装包）。
#
# 前置：macOS + Xcode Command Line Tools、Go、Node、Wails CLI。
# 用法：bash scripts/build-mac.sh
# 可选环境变量：WAILS_PLATFORM（默认 darwin/universal，可填 darwin/arm64 或 darwin/amd64）。
#
set -euo pipefail
cd "$(dirname "$0")/.."

PLATFORM="${WAILS_PLATFORM:-darwin/universal}"

mkdir -p build/bin

build_core() {
  local arch="$1"
  local output="$2"
  CGO_ENABLED=0 GOOS=darwin GOARCH="$arch" go build -o "$output" ./cmd/core
}

echo "==> 构建 Core 后台服务（平台: $PLATFORM）"
case "$PLATFORM" in
  darwin/universal)
    if ! command -v lipo >/dev/null 2>&1; then
      echo "错误: universal 构建需要 Xcode Command Line Tools 提供的 lipo。" >&2
      exit 1
    fi
    CORE_TMP_DIR="$(mktemp -d)"
    trap 'rm -rf "$CORE_TMP_DIR"' EXIT
    build_core arm64 "$CORE_TMP_DIR/easyshare-core-arm64"
    build_core amd64 "$CORE_TMP_DIR/easyshare-core-amd64"
    lipo -create \
      "$CORE_TMP_DIR/easyshare-core-arm64" \
      "$CORE_TMP_DIR/easyshare-core-amd64" \
      -output build/bin/easyshare-core
    ;;
  darwin/arm64)
    build_core arm64 build/bin/easyshare-core
    ;;
  darwin/amd64)
    build_core amd64 build/bin/easyshare-core
    ;;
  *)
    echo "错误: 不支持的 WAILS_PLATFORM=$PLATFORM（仅支持 darwin/universal、darwin/arm64、darwin/amd64）。" >&2
    exit 2
    ;;
esac

# 应用图标（.app 需要 iconfile.icns）。若缺失则从 appicon.png 生成完整 iconset。
if [ ! -f build/darwin/iconfile.icns ]; then
  echo "==> 生成应用图标 iconfile.icns"
  if command -v iconutil >/dev/null 2>&1; then
    ICONUTIL_DIR="$(mktemp -d)/icon.iconset"
    mkdir -p "$ICONUTIL_DIR"
    make_icon() { sips -z "$1" "$1" build/appicon.png --out "$ICONUTIL_DIR/$2" >/dev/null; }
    make_icon 16   icon_16x16.png
    make_icon 32   icon_16x16@2x.png
    make_icon 32   icon_32x32.png
    make_icon 64   icon_32x32@2x.png
    make_icon 128  icon_128x128.png
    make_icon 256  icon_128x128@2x.png
    make_icon 256  icon_256x256.png
    make_icon 512  icon_256x256@2x.png
    make_icon 512  icon_512x512.png
    make_icon 1024 icon_512x512@2x.png
    iconutil -c icns "$ICONUTIL_DIR" -o build/darwin/iconfile.icns
    rm -rf "$(dirname "$ICONUTIL_DIR")"
  else
    echo "警告: 未找到 iconutil，.app 将使用默认图标。"
  fi
fi

# wails build 会自动执行 frontend:install + frontend:build（含 vue-tsc 类型检查）。
echo "==> 构建桌面端（.app，平台: $PLATFORM）"
wails build -platform "$PLATFORM"

APP_PATH="build/bin/easyshare.app"
DMG_PATH="build/bin/EasyShare.dmg"

# 关键：把 Core 放进 .app 包内，桌面端按可执行文件同目录查找 easyshare-core。
echo "==> 将 Core 放入 .app 包（Contents/MacOS/）"
cp build/bin/easyshare-core "$APP_PATH/Contents/MacOS/easyshare-core"

echo "==> 打包 DMG"
rm -f "$DMG_PATH"
hdiutil create -volname "EasyShare" -srcfolder "$APP_PATH" -ov -format UDZO "$DMG_PATH" >/dev/null

echo ""
echo "构建完成："
echo "  Core   : build/bin/easyshare-core"
echo "  App    : $APP_PATH"
echo "  DMG    : $DMG_PATH"
echo ""
echo "提示：macOS 首次打开未签名的 .app 会被 Gatekeeper 拦截，"
echo "      可在「系统设置 → 隐私与安全性」中允许，或对 .app 做签名/公证后分发。"
