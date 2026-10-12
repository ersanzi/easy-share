// 会议听记的桌面端接线：
// 1. 小请求（列表/详情/管理/归档）走 Wails 绑定 → desktop.Client → Core JSON 代理；
// 2. 大文件（音频上传/回放）走同源 fetch /kapi/meetings/*——经 pluginAssetMiddleware
//    接管（dev 下 Vite 会吞 fallback Handler，必须 Middleware 阶段拦截），流式转发，
//    不把上百 MB 音频塞进 Wails 绑定的 JSON 通道。令牌仍不出 Core。
package main

import (
	"encoding/json"
	"net/http"
	"strings"

	"easyshare/internal/desktop"
	"easyshare/internal/knowledge"
)

// --- Wails 绑定（小请求） ---

// MeetingsList 会议听记列表（不含分段全文，详情按需拉取）。
func (a *App) MeetingsList(limit int) ([]knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	result, err := client.Meetings(a.ctx, limit)
	a.reportError("meetings list", err)
	return result, err
}

// MeetingDetail 单条完整记录（转写分段 + 总结），受理后轮询用。
func (a *App) MeetingDetail(id string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.Meeting(a.ctx, id)
	a.reportError("meeting detail", err)
	return result, err
}

// MeetingRemove 删除会议记录与音频。
func (a *App) MeetingRemove(id string) error {
	client, err := a.coreClient()
	if err != nil {
		return err
	}
	err = client.MeetingDelete(a.ctx, id)
	a.reportError("meeting remove", err)
	return err
}

// MeetingSpeakerRename 发言人改名（单次一个）。
func (a *App) MeetingSpeakerRename(id, speaker, name string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingRenameSpeaker(a.ctx, id, speaker, name)
	a.reportError("meeting rename speaker", err)
	return result, err
}

// MeetingSummaryRegenerate 按记录登记的模板重新生成总结（LLM 调用，秒级到数十秒）。
func (a *App) MeetingSummaryRegenerate(id, templateID string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingSummaryRegenerate(a.ctx, id, templateID)
	a.reportError("meeting summary regen", err)
	return result, err
}

// MeetingAsk 听记 AI 问答：对本条听记提问（纪要+转写全文进上下文，LLM 调用，秒级到数十秒）。
func (a *App) MeetingAsk(id, question string, history []knowledge.MeetingChatTurn) (knowledge.MeetingAskResult, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.MeetingAskResult{}, err
	}
	result, err := client.MeetingAsk(a.ctx, id, question, history)
	a.reportError("meeting ask", err)
	return result, err
}

// MeetingMindmapRegenerate 重新生成思维导图大纲（LLM 调用，秒级到数十秒）。
func (a *App) MeetingMindmapRegenerate(id string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingMindmapRegenerate(a.ctx, id)
	a.reportError("meeting mindmap", err)
	return result, err
}

// MeetingArchiveToKnowledge 把纪要与转写归档进知识库（可被问答检索引用）。
func (a *App) MeetingArchiveToKnowledge(id string) (knowledge.ArchiveResult, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.ArchiveResult{}, err
	}
	result, err := client.MeetingArchive(a.ctx, id)
	a.reportError("meeting archive", err)
	return result, err
}

// --- 同源网关（大文件流式，/kapi/meetings/*） ---

// serveMeetingGateway 在 pluginAssetMiddleware 中接管 /kapi/meetings/ 前缀：
// 仅两条流式路由，其余听记操作走上面的绑定。
func (a *App) serveMeetingGateway(writer http.ResponseWriter, request *http.Request) {
	client, err := a.coreClient()
	if err != nil {
		http.Error(writer, err.Error(), http.StatusServiceUnavailable)
		return
	}
	path := request.URL.Path
	switch {
	case request.Method == http.MethodPost && path == "/kapi/meetings/transcribe":
		contentType := request.Header.Get("Content-Type")
		if !strings.HasPrefix(contentType, "multipart/form-data") {
			http.Error(writer, "expected multipart/form-data", http.StatusBadRequest)
			return
		}
		var meeting knowledge.Meeting
		if err := client.MeetingUpload(request.Context(), contentType, request.Body, &meeting); err != nil {
			http.Error(writer, err.Error(), http.StatusBadGateway)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(writer).Encode(meeting)
	case request.Method == http.MethodGet && strings.HasSuffix(path, "/audio"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/kapi/meetings/"), "/audio")
		if id == "" {
			http.NotFound(writer, request)
			return
		}
		// 出错时响应可能已开始写出，无法改写状态码，仅记录
		if err := client.MeetingAudioStream(request.Context(), id, writer); err != nil {
			a.reportError("meeting audio stream", err)
		}
	case request.Method == http.MethodGet && strings.Contains(path, "/export/"):
		// /kapi/meetings/{id}/export/{markdown|docx}：备忘录导出文件透传
		rest := strings.TrimPrefix(path, "/kapi/meetings/")
		parts := strings.Split(rest, "/export/")
		if len(parts) != 2 || parts[0] == "" {
			http.NotFound(writer, request)
			return
		}
		format := parts[1]
		if format != "markdown" && format != "docx" {
			http.Error(writer, "unsupported export format", http.StatusBadRequest)
			return
		}
		// 同上：错误时响应可能已开始写出，仅记录
		if err := client.MeetingExportStream(request.Context(), parts[0], format, writer); err != nil {
			a.reportError("meeting export stream", err)
		}
	default:
		http.NotFound(writer, request)
	}
}

// --- 听记 MCP（设置页「AI 工具连接」） ---

// McpConnectionGet 听记 MCP 接入信息（URL + 配对令牌），粘进 AI 客户端即完成接入。
func (a *App) McpConnectionGet() (desktop.McpConnection, error) {
	client, err := a.coreClient()
	if err != nil {
		return desktop.McpConnection{}, err
	}
	return client.McpInfo(a.ctx)
}

// McpConnectionRegenerate 重新生成听记 MCP 配对令牌（吊销旧令牌，已接入的客户端需更新）。
func (a *App) McpConnectionRegenerate() (desktop.McpConnection, error) {
	client, err := a.coreClient()
	if err != nil {
		return desktop.McpConnection{}, err
	}
	result, err := client.McpRegenerate(a.ctx)
	a.reportError("mcp regenerate", err)
	return result, err
}
