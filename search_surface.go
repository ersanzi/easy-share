// 全局快捷搜索（Everything 式）：第二面板表面——独立热键唤起的搜索小窗，
// 聚合知识检索（Core 网关 /api/knowledge/query 的 search 模式，仅检索不生成）
// 与网盘文件按名匹配（个人 + 共享，观察期数据量客户端过滤足够，量大后服务端化）。
//
// 平台边界：Windows 全量实现（panel_windows.go 按 kind 分支）；darwin 暂 no-op
// （NSPanel 面板待真机批次）；页面是 go:embed 的自包含 HTML（本文件底部引用），
// 经面板 loopback 静态服务的 /search 路由提供，RPC 走 host.search 信封
// （panel_surface.go 的 host.* 分支——面板窗口是宿主自有表面，不经插件权限体系）。
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"easyshare/internal/clipboard"
	"easyshare/internal/drive"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

//go:embed search_page.html
var searchPageHTML []byte

// 搜索表面窗口逻辑尺寸（96 DPI 基准，平台实现按 DPI 缩放）。
const (
	searchSurfaceWidth  = 480
	searchSurfaceHeight = 560
)

// serveSearchPage 提供搜索表面页（面板窗口加载 /search?panel=1）。
func (a *App) serveSearchPage(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	_, _ = writer.Write(searchPageHTML)
}

// searchHit 搜索结果条目：kind=knowledge（知识片段）/ file（网盘文件）。
type searchHit struct {
	Kind    string  `json:"kind"`
	Title   string  `json:"title"`
	Sub     string  `json:"sub"`
	Key     string  `json:"key"`
	Space   string  `json:"space,omitempty"`
	Score   float64 `json:"score,omitempty"`
	Pointer string  `json:"pointer,omitempty"`
}

// hostPanelSearchAsync 异步执行搜索并经搜索面板 Eval 通道回投 panelReply；
// 面板不可见（检索期间被收起）时静默丢弃。
func (a *App) hostPanelSearchAsync(id int, query string) {
	go func() {
		hits := a.hostPanelSearch(query)
		raw, err := json.Marshal(hits)
		if err != nil {
			return
		}
		reply, err := json.Marshal(panelReply{Eshare: 1, ID: id, OK: true, Data: raw})
		if err != nil {
			return
		}
		a.searchEmitMu.RLock()
		emit := a.searchEmit
		a.searchEmitMu.RUnlock()
		if emit != nil {
			emit("window.__eshareNative&&window.__eshareNative.deliver(" + string(reply) + ")")
		}
	}()
}

// hostPanelSearch 聚合两路检索：知识（仅检索模式，截前 6 条）+ 网盘按名匹配
// （个人/共享各截 6 条）。任一路失败不影响另一路——搜索面板宁可少一半结果
// 也不整页报错。
func (a *App) hostPanelSearch(query string) []searchHit {
	q := strings.TrimSpace(query)
	hits := make([]searchHit, 0, 16)
	if q == "" {
		return hits
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// 知识路：走 Core 网关（登录态由 Core 持有），仅检索不生成
	if core, err := a.coreClient(); err == nil {
		if answer, err := core.KnowledgeSearch(ctx, q); err == nil {
			for index, context := range answer.Contexts {
				if index >= 6 {
					break
				}
				title := "知识片段"
				if context.Filename != nil && *context.Filename != "" {
					title = *context.Filename
				}
				snippet := strings.TrimSpace(context.Text)
				if len(snippet) > 160 {
					snippet = snippet[:160] + "…"
				}
				docID := ""
				if context.DocID != nil {
					docID = *context.DocID
				}
				var score float64
				if context.Score != nil {
					score = *context.Score
				}
				hits = append(hits, searchHit{
					Kind: "knowledge", Title: title, Sub: snippet,
					Key: docID, Score: score,
				})
			}
		} else {
			a.logger.Printf("search: 知识检索不可用: %v", err)
		}
	}

	// 文件路：个人 + 共享按名过滤（大小写不敏感子串）
	if client, token, err := a.driveClient(); err == nil {
		for _, space := range []string{drive.SpacePersonal, drive.SpaceShared} {
			objects, err := client.Objects(ctx, token, space)
			if err != nil {
				continue // 共享空间无权限等：跳过该路
			}
			lower := strings.ToLower(q)
			count := 0
			for _, object := range objects {
				if count >= 6 {
					break
				}
				name := filepath.Base(object.Path)
				if !strings.Contains(strings.ToLower(name), lower) {
					continue
				}
				hits = append(hits, searchHit{
					Kind: "file", Title: name,
					Sub: filepath.ToSlash(filepath.Dir(object.Path)),
					Key: object.Path, Space: space,
				})
				count++
			}
		}
	} else {
		a.logger.Printf("search: 网盘不可用: %v", err)
	}
	return hits
}

// hostPanelOpenFile 签发网盘文件下载链接并交系统浏览器打开。
func (a *App) hostPanelOpenFile(key, space string) error {
	client, token, err := a.driveClient()
	if err != nil {
		return err
	}
	url, err := client.PresignGet(context.Background(), token, space, key)
	if err != nil {
		return err
	}
	runtime.BrowserOpenURL(a.ctx, url)
	return nil
}

// hostPanelCopy 写系统剪切板（复用剪切板服务的宿主写入通道，带回写防环标记）。
func (a *App) hostPanelCopy(text string) error {
	if a.clipboardService == nil {
		return fmt.Errorf("剪切板服务不可用")
	}
	return a.clipboardService.Write(clipboard.WriteRequest{Kind: clipboard.KindText, Text: text})
}
