// Package driveerr 把云盘链路的失败翻译成「错误三件套」（路线图切片 7）。
//
// 对标两份取证：
//   - 钉盘：`TEC_*` 错误码家族（业务码）+ 人读文案 + 系统码；
//   - WPS：`errorCode` + `errorMsg` + `winErrorCode`。
//
// **为什么值得单独一个包**：这三者缺一个，用户看到的就只是「失败」。
// 业务码让支持人员一眼定位（不必读日志）；人读文本让用户知道下一步做什么；
// 系统码（HTTP 状态 / OS 错误号）让技术判据不被文案改写而丢失。
//
// 设计原则（与切片 3 同一取向：**规则写死，不猜**）：
//   - 业务码是**稳定字符串**，一旦发布不改名（改名等于让历史数据失去可比性）；
//   - 分类只做**从已有信息推断**，绝不为分类成功而改变行为；
//   - 认不出来的一律落 `UNKNOWN` + 保留原文——宁可分类粗，不可丢信息。
package driveerr

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// 业务码。命名口径：域_情形（大写下划线），与钉盘 TEC_* 同风格。
//
// ⚠️ **已发布的名字不得更改**：它们是用户工单、日志检索与埋点里的稳定键。
const (
	// CodeAuth 未登录 / 令牌失效 / 无控制面地址
	CodeAuth = "CLOUD_AUTH_REQUIRED"
	// CodePermission 无该空间权限（只读授权、非成员、文档级可见性）
	CodePermission = "CLOUD_PERMISSION_DENIED"
	// CodeQuota 空间配额不足
	CodeQuota = "CLOUD_QUOTA_EXCEEDED"
	// CodeConflict 版本冲突（CAS 拒绝）
	CodeConflict = "CLOUD_VERSION_CONFLICT"
	// CodeGone 目标已删除 / 不存在
	CodeGone = "CLOUD_NOT_FOUND"
	// CodeNetwork 连不上控制面 / 对象存储（DNS、超时、拒连）
	CodeNetwork = "CLOUD_NETWORK"
	// CodeStorage 对象存储返回错误（非我们的业务语义）
	CodeStorage = "CLOUD_STORAGE_ERROR"
	// CodeLocal 本地文件读不到 / 写不了（磁盘满、占用、权限）
	CodeLocal = "LOCAL_IO"
	// CodeUnsupported 当前平台/系统不支持该能力
	CodeUnsupported = "UNSUPPORTED"
	// CodeUnknown 认不出来（保留原文，不丢信息）
	CodeUnknown = "UNKNOWN"
)

// Triple 是错误三件套。
type Triple struct {
	// Code 业务码（见上面的常量）
	Code string
	// Message 人读文本（中文，可直接展示）
	Message string
	// System 系统码（HTTP 状态如 "HTTP 403"、OS 错误如 "errno 28"）；没有则空
	System string
}

// Classify 把一次失败翻译成三件套。
//
// 输入是任意 error（可能已被逐层 wrap）。识别顺序**从具体到一般**：
// 先看业务语义（配额/权限/冲突/不存在），再看传输层（网络/存储），最后本地 IO。
// 这个顺序很重要——例如「配额不足」在网络层看来只是个 HTTP 200 + code=500 的响应，
// 若先按 HTTP 判就会落成 STORAGE。
func Classify(err error) Triple {
	if err == nil {
		return Triple{Code: "", Message: ""}
	}
	msg := err.Error()
	lower := strings.ToLower(msg)

	// ── 业务语义（服务端可读文案，见 platform-drive 的 ServiceException）──
	switch {
	case containsAny(msg, "请先登录", "登录已过期", "未配置账号服务"):
		return Triple{Code: CodeAuth, Message: msg}
	case containsAny(msg, "只有只读权限", "无权访问", "无权限", "不属于当前空间",
		"没有这个空间", "只有文件的上传者"):
		return Triple{Code: CodePermission, Message: msg}
	case containsAny(msg, "配额", "空间不足", "容量不足", "已满"):
		return Triple{Code: CodeQuota, Message: msg}
	case containsAny(msg, "已被其他人修改", "版本", "已过期", "已被删除（你看到的版本"):
		return Triple{Code: CodeConflict, Message: msg}
	case containsAny(msg, "不存在或已删除", "回收站中不存在", "文件不存在", "目标位置已有同名文件"):
		return Triple{Code: CodeGone, Message: msg}
	}

	// ── 传输层 ──
	// 对象存储的错误形如「对象存储返回 403 Forbidden」：带 HTTP 状态码
	if idx := strings.Index(msg, "对象存储返回"); idx >= 0 {
		return Triple{Code: CodeStorage, Message: msg,
			System: systemOfHTTP(msg[idx:])}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return Triple{Code: CodeNetwork, Message: humanNetwork(msg, netErr)}
	}
	if containsAny(lower, "connection refused", "no such host", "i/o timeout",
		"connection reset", "tls handshake") {
		return Triple{Code: CodeNetwork, Message: humanNetwork(msg, nil)}
	}
	// 控制面返回了 HTTP 状态（call 里「云盘服务响应异常(HTTP %d)」/「连接云盘服务失败」）
	if idx := strings.Index(msg, "HTTP "); idx >= 0 {
		system := strings.TrimSpace(msg[idx : idx+len("HTTP ")+3])
		return Triple{Code: CodeNetwork, Message: msg, System: system}
	}

	// ── 本地 IO ──
	if containsAny(lower, "no such file", "access is denied", "permission denied",
		"not enough space", "disk full", "文件不存在", "打开文件失败", "读取文件信息失败") {
		return Triple{Code: CodeLocal, Message: msg}
	}

	return Triple{Code: CodeUnknown, Message: msg}
}

// Apply 在已有三件套上叠加（用于调用链里更具体的一层覆盖更粗的一层）。
func Apply(err error, code, system string) Triple {
	base := Classify(err)
	if code != "" {
		base.Code = code
	}
	if system != "" {
		base.System = system
	}
	return base
}

// Error 把三件套拼成可直接展示/落库的一行（含业务码，便于支持定位）。
//
// 展示口径：`[业务码] 人读文本 (系统码)`；系统码缺失时省略括号。
func (t Triple) Error() string {
	if t.Code == "" && t.Message == "" {
		return ""
	}
	var b strings.Builder
	if t.Code != "" {
		b.WriteString("[")
		b.WriteString(t.Code)
		b.WriteString("] ")
	}
	b.WriteString(t.Message)
	if t.System != "" {
		b.WriteString(" (")
		b.WriteString(t.System)
		b.WriteString(")")
	}
	return b.String()
}

// Format 返回给任务字段写入的三元组（error / errorCode / systemCode）。
func (t Triple) Format() (message, code, system string) {
	return t.Message, t.Code, t.System
}

// Wrap 给 error 附加业务码，返回一个 errors.Is 可穿透的包装错误。
//
// 用途：调用点已经知道分类（例如「这里失败一定是配额」）时不靠文案猜。
func Wrap(err error, code string) error {
	if err == nil {
		return nil
	}
	return &codedError{code: code, err: err}
}

type codedError struct {
	code string
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// CodeOf 取调用方显式附加的业务码（没有则空）。
func CodeOf(err error) string {
	var coded *codedError
	if errors.As(err, &coded) {
		return coded.code
	}
	return ""
}

// humanNetwork 给网络错误补一句「下一步做什么」，原文保留。
func humanNetwork(msg string, netErr net.Error) string {
	if netErr != nil && netErr.Timeout() {
		return msg + "；请检查网络或稍后重试"
	}
	if strings.Contains(strings.ToLower(msg), "no such host") {
		return msg + "；请确认账号服务地址是否正确"
	}
	return msg
}

// systemOfHTTP 从「对象存储返回 403 Forbidden」里取 "HTTP 403"。
func systemOfHTTP(s string) string {
	fields := strings.Fields(s)
	for i, f := range fields {
		if f == "返回" && i+1 < len(fields) {
			return "HTTP " + trimToDigits(fields[i+1])
		}
	}
	return ""
}

// trimToDigits 取字符串的前导数字段。
func trimToDigits(s string) string {
	end := 0
	for end < len(s) && s[end] >= '0' && s[end] <= '9' {
		end++
	}
	if end == 0 {
		return s
	}
	return s[:end]
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// StatusText 把 HTTP 状态码翻译成人读中文（用于本地错误展示）。
func StatusText(code int) string {
	if text := http.StatusText(code); text != "" {
		return fmt.Sprintf("%d %s", code, text)
	}
	return fmt.Sprintf("%d", code)
}
