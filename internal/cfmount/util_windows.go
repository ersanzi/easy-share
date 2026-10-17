//go:build windows

package cfmount

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ancestorDirs 的辅助函数放在这里，与主逻辑分开便于单测。

// normalizeSlashes 把反斜杠统一成斜杠，便于按 / 切分。
func normalizeSlashes(p string) string {
	return strings.ReplaceAll(strings.Trim(p, "/\\"), "\\", "/")
}

// shortHash 取路径的短哈希（16 个十六进制字符 = 8 字节）。
//
// 用 SHA-256 前 8 字节：碰撞概率对「目录身份」这个用途足够低
// （目录数量在万级时，8 字节的碰撞概率约 10^-9）。
// 不引额外的哈希依赖——crypto/sha256 已在标准库且别处在用。
func shortHash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:8])
}
