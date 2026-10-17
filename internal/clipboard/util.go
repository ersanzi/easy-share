package clipboard

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// 平台无关的小工具。
//
// 这三个函数原先住在 listener_windows.go 里，但 listener_darwin.go 也在用——
// Windows 上编译得过、macOS 上直接 undefined。放进无 build tag 的文件后两边
// 共用同一份实现，这对语义也是必要的：同一份内容在任意平台必须得到同一个
// hash，否则跨平台的回环去重（markSelfWrite / record）会失效。
//
// 教训：同包内跨平台共用的辅助函数，绝不能顺手写在某个 *_windows.go 里。

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}

func stringsJoin(list []string, sep string) string {
	out := ""
	for i, s := range list {
		if i > 0 {
			out += sep
		}
		out += s
	}
	return out
}

func nowMillis() int64 { return time.Now().UnixMilli() }
