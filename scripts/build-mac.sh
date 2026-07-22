#!/usr/bin/env bash
#
# EasyShare macOS 构建脚本（在 Mac 上运行）。
# 产出：build/bin/easyshare-core（后台服务）、build/bin/easyshare.app（桌面端）、build/bin/EasyShare.dmg（安装包）。
#
# 前置：macOS + Xcode Command Line Tools（xcode-select --install）、Go、Node、Wails CLI。
# 用法：bash scripts/build-mac.sh
#
set -euo pipefail
cd "$(dirname "$0")/.."

echo "==> 前端构建（含 vue-tsc 类型检查）"
npm --prefix frontend run build

echo "==> 构建 Core 后台服务"
go build -o build/bin/easyshare-core ./cmd/core

# 应用图标（.app 需要 iconfile.icns）。若缺失则尝试从 appicon.png 生成。
if [ ! -f build/darwin/iconfile.icns ]; then
  echo "==> 生成应用图标 iconfile.icns"
  if command -v iconutil >/dev/null 2>&1; then
    ICONUTIL_DIR="$(mktemp -d)/icon.iconset"
    mkdir -p "$ICONUTIL_DIR"
    for size in 16 32 64 128 256 512; do
      sips -z "$size" "$size" build/appicon.png --out "$ICONUTIL_DIR/icon_${size}x${size}.png" >/dev/null
    done
    cp "$ICONUTIL_DIR/icon_512x512.png" "$ICONUTIL_DIR/icon_256x256@2x.png"
    iconutil -c icns "$ICONUTIL_DIR" -o build/darwin/iconfile.icns
  else
    echo "警告: 未找到 iconutil，.app 将使用默认图标。"
  fi
fi

echo "==> 构建桌面端（.app）"
wails build -platform darwin/universal

APP_PATH="build/bin/easyshare.app"
DMG_PATH="build/bin/EasyShare.dmg"

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
