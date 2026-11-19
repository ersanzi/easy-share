// Package shellundo 提供显式的 Windows Shell 可撤销删除入口；不接管普通 Delete。
package shellundo

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf16"
)

var (
	ErrUnsupported = errors.New("当前平台不支持 Windows Shell 可撤销删除")
	ErrAborted     = errors.New("Windows Shell 删除已取消或部分中止；不可按全部成功处理")
	ErrOutsideRoot = errors.New("删除路径不属于当前 EasyShare 云根")
	ErrClosed      = errors.New("EasyShare Shell 删除入口已关闭")
)

const (
	CommandFlag = "--easyshare-undo-delete"
	maxPaths    = 128
)

// normalizeLocalPath 在确认原始输入没有相对路径、设备路径、ADS 或歧义组件之后才规范化。
// 不使用 filepath.Clean 消除未知的 ..；仅支持本地盘符路径，NTFS 由 Windows 执行层验证。
func normalizeLocalPath(raw string) (string, error) {
	if len(utf16.Encode([]rune(raw))) > 32760 || strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("无效本机路径: 长度或 NUL")
	}
	path := strings.ReplaceAll(raw, "/", `\`)
	if len(path) < 3 || path[1] != ':' || path[2] != '\\' || !isDriveLetter(path[0]) {
		return "", fmt.Errorf("必须使用本地盘符绝对路径: %q", raw)
	}
	if strings.ContainsAny(path[3:], `:<>"|?*`) {
		return "", fmt.Errorf("拒绝 ADS、通配符或特殊路径: %q", raw)
	}
	// 只允许末尾的目录分隔符；中间的空组件、点组件不能被静默清理。
	tail := strings.TrimSuffix(path[3:], `\`)
	if tail == "" && len(path) > 3 {
		return "", fmt.Errorf("路径存在歧义分隔符: %q", raw)
	}
	if tail != "" {
		for _, component := range strings.Split(tail, `\`) {
			if component == "" || component == "." || component == ".." || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
				return "", fmt.Errorf("拒绝歧义路径组件: %q", raw)
			}
			for _, ch := range component {
				if ch < 32 {
					return "", fmt.Errorf("路径包含控制字符: %q", raw)
				}
			}
			device := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
			if device == "CON" || device == "PRN" || device == "AUX" || device == "NUL" || device == "CONIN$" || device == "CONOUT$" || isNumberedDevice(device) {
				return "", fmt.Errorf("拒绝设备名组件: %q", raw)
			}
		}
	}
	path = strings.ToUpper(path[:1]) + path[1:3] + tail
	return path, nil
}

func isNumberedDevice(device string) bool {
	characters := []rune(device)
	return len(characters) == 4 && (strings.HasPrefix(device, "COM") || strings.HasPrefix(device, "LPT")) && strings.ContainsRune("123456789¹²³", characters[3])
}

func isDriveLetter(ch byte) bool { return ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z' }

func normalizeSelection(paths []string) ([]string, error) {
	if len(paths) == 0 || len(paths) > maxPaths {
		return nil, fmt.Errorf("删除选择数量必须为 1..%d", maxPaths)
	}
	out := make([]string, 0, len(paths))
	seen := make(map[string]bool, len(paths))
	for _, path := range paths {
		normal, err := normalizeLocalPath(path)
		if err != nil {
			return nil, err
		}
		if len(normal) == 3 {
			return nil, fmt.Errorf("禁止删除盘符根: %q", path)
		}
		key := strings.ToLower(normal)
		if seen[key] {
			return nil, fmt.Errorf("重复删除路径: %q", path)
		}
		seen[key] = true
		out = append(out, normal)
	}
	for i, path := range out {
		for j, other := range out {
			if i != j && isStrictDescendant(other, path) {
				return nil, fmt.Errorf("选择同时包含目录及其子项: %q / %q", path, other)
			}
		}
	}
	return out, nil
}

func isStrictDescendant(path, root string) bool {
	return len(path) > len(root)+1 && strings.EqualFold(path[:len(root)], root) && path[len(root)] == '\\'
}

func selectionInRoot(root string, paths []string) ([]string, error) {
	normal, err := normalizeSelection(paths)
	if err != nil {
		return nil, err
	}
	for _, path := range normal {
		if !isStrictDescendant(path, root) {
			return nil, fmt.Errorf("%w: %q", ErrOutsideRoot, path)
		}
	}
	return normal, nil
}
