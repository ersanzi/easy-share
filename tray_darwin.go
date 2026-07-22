//go:build darwin

package main

import (
	_ "embed"
)

// macOS 菜单栏图标使用 PNG（理想情况是黑白 template 图，可随深浅色自适应）。
// 当前复用应用图标作为起点，后续可替换为专门的 template 图标。
//
//go:embed build/darwin/trayicon.png
var trayIcon []byte
