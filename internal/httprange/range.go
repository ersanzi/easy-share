// Package httprange 提供**带 Range 的按需读取**，用于对象存储的预签名 GET。
//
// ── 为什么单独一个包 ────────────────────────────────────────────
//
// 本仓有两条「按需读取云端对象」的路径，它们各自需要区间读取：
//
//   - internal/cfmount：CfApi 占位符的 FETCH_DATA 回调（要任意区间）；
//   - internal/spacedav：WebDAV 的 readFile.Seek（视频拖动、大文件定位）。
//
// 两处都需要「拿预签名 URL 发带 Range 的 GET，并处理服务端不支持 Range 的情况」，
// 所以提成共用包，避免两份实现漂移。
//
// ── 为什么客户端加 Range 是可行的 ───────────────────────────────
//
// 控制面签发预签名 URL 时只签了 bucket + key
// （`DriveStorage.presignGetAt` → `getObjectRequest(r -> r.bucket(...).key(...))`），
// **没有把任何请求头纳入签名**。SigV4 的语义是「签名时未列出的头不参与校验」，
// 所以客户端**可以自由添加 `Range` 头**——它不在 SignedHeaders 里，不影响验签。
//
// 因此这件事不需要改 platform-drive：拿到 URL 自己发带 Range 的 GET 即可。
//
// ✅ **前提已于 2026-10-18 活栈实测**：本机 RustFS 上对 3 MB 对象请求首/中/尾三段，
// 均回 `206` 且 `content-range` 与字节数精确（探针 `scripts/diag/presign-range-probe.mjs`，
// 换存储后端时重跑一次）。实现里仍保留**完整回退**：服务端返回 200（忽略 Range）时
// 按整份读取处理——「服务端支持」是性能优化，「不支持」只退回原行为，**不影响正确性**。
package httprange

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// 本文件实现**带 Range 的按需读取**，替掉原先「每次读整份对象」的缺陷。
//
// ── 为什么客户端改就够了 ────────────────────────────────────────
//
// 控制面签发预签名 URL 时只签了 bucket + key
// （`DriveStorage.presignGetAt` → `getObjectRequest(r -> r.bucket(...).key(...))`），
// **没有把任何请求头纳入签名**。SigV4 的语义是「签名时未列出的头不参与校验」，
// 所以客户端**可以自由添加 `Range` 头**——它不在 SignedHeaders 里，不影响验签。
//
// 因此本缺陷不需要改 platform-drive：`drive.Client.PresignGet` 已经是导出方法，
// 本包拿 URL 自己发带 Range 的 GET 即可。这与 `internal/spacedav` 的 `Seek`
// 注释里那句「预签名 URL 没有暴露该能力」相反——**能力是有的，只是先前没试**。
//
// ✅ **前提已于 2026-10-18 活栈实测**：本机 RustFS 上对 3 MB 对象请求首/中/尾三段，
// 均回 `206` 且 `content-range` 与字节数精确（探针 `scripts/diag/presign-range-probe.mjs`，
// 换存储后端时重跑一次）。实现里仍保留**完整回退**：服务端返回 200（忽略 Range）时
// 按整份读取处理——「服务端支持」是性能优化，「不支持」只退回原行为，**不影响正确性**。

// Fetcher 用一个预签名 URL 发带 Range 的 GET。
type Fetcher struct {
	http *http.Client
	// presign 取预签名 URL。签名参数由调用方通过闭包绑定，
	// 这样本包不必知道「控制面/空间/令牌」这些概念。
	presign func(ctx context.Context) (string, error)
}

// NewFetcher 构造 Range 取数器。
func NewFetcher(presign func(ctx context.Context) (string, error)) *Fetcher {
	return &Fetcher{
		http: &http.Client{
			// 不用整体 Timeout：大文件读取可能超过任何固定阈值，
			// 超时由调用方的 ctx 控制（cfapi 的 FetchTimeout）。
			Transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				ResponseHeaderTimeout: 30 * time.Second,
				IdleConnTimeout:       30 * time.Second,
			},
		},
		presign: presign,
	}
}

// Fetch 取 [offset, offset+length) 区间，返回其读取器。
//
// 返回的 reader 只覆盖请求区间；若服务端不支持 Range（返回 200），
// 则整份读完后再切区间，**语义仍然正确**（只是慢了）。
func (r *Fetcher) Fetch(ctx context.Context, offset, length int64) (io.ReadCloser, error) {
	if length <= 0 {
		return nil, fmt.Errorf("httprange: range 长度必须为正（得到 %d）", length)
	}
	url, err := r.presign(ctx)
	if err != nil {
		return nil, fmt.Errorf("httprange: 取预签名 URL 失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	// 闭区间：[offset, offset+length-1]
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))

	resp, err := r.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("httprange: Range 请求失败: %w", err)
	}

	switch resp.StatusCode {
	case http.StatusPartialContent:
		// 服务端支持 Range：body 就是请求区间，直接流式返回。
		return &countingReadCloser{rc: resp.Body, expect: length}, nil

	case http.StatusOK:
		// 服务端忽略了 Range（返回整份）。按整份读，再切区间——
		// **正确性不依赖服务端是否支持 Range**。
		all, rerr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if rerr != nil {
			return nil, fmt.Errorf("httprange: 服务端忽略 Range，整份读取失败: %w", rerr)
		}
		if offset > int64(len(all)) {
			return nil, fmt.Errorf("httprange: 偏移 %d 超出对象大小 %d", offset, len(all))
		}
		end := offset + length
		if end > int64(len(all)) {
			end = int64(len(all))
		}
		return io.NopCloser(bytes.NewReader(all[offset:end])), nil

	case http.StatusRequestedRangeNotSatisfiable:
		// 区间越界：这是合法结果（例如文件被截短），如实报出而不当异常。
		resp.Body.Close()
		return nil, fmt.Errorf("httprange: 区间 %d+%d 越界（对象可能已变更）", offset, length)

	default:
		resp.Body.Close()
		return nil, fmt.Errorf("httprange: 对象存储返回 %s", resp.Status)
	}
}

// countingReadCloser 包装 206 响应的 body，并在读到预期长度前就结束时报错。
//
// 为什么要额外检查：S3 兼容实现若返回 206 但 body 短于 Content-Range 声称的长度，
// 上层会拿到静默截断的数据。宁可报错，也不要让用户读到半个文件。
type countingReadCloser struct {
	rc     io.ReadCloser
	expect int64
	read   int64
}

func (c *countingReadCloser) Read(p []byte) (int, error) {
	n, err := c.rc.Read(p)
	c.read += int64(n)
	if err == io.EOF && c.read < c.expect {
		return n, fmt.Errorf("httprange: 响应体提前结束（拿到 %d，期望 %d）: %w",
			c.read, c.expect, io.ErrUnexpectedEOF)
	}
	return n, err
}

func (c *countingReadCloser) Close() error { return c.rc.Close() }

// ParseContentRangeTotal 从 Content-Range 头解析对象总大小（诊断用）。
//
// 格式：`bytes 0-1023/2048`。解析失败返回 0,false。
func ParseContentRangeTotal(v string) (int64, bool) {
	slash := -1
	for i := len(v) - 1; i >= 0; i-- {
		if v[i] == '/' {
			slash = i
			break
		}
	}
	if slash < 0 || slash == len(v)-1 {
		return 0, false
	}
	total, err := strconv.ParseInt(v[slash+1:], 10, 64)
	if err != nil || total < 0 {
		return 0, false
	}
	return total, true
}
