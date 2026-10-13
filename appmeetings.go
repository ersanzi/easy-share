// 会议听记的桌面端接线：
//  1. 小请求（列表/详情/管理/归档）走 Wails 绑定 → desktop.Client → Core JSON 代理；
//  2. 大文件（音频上传/回放）走同源 fetch /kapi/meetings/*——经 pluginAssetMiddleware
//     接管（dev 下 Vite 会吞 fallback Handler，必须 Middleware 阶段拦截），流式转发，
//     不把上百 MB 音频塞进 Wails 绑定的 JSON 通道。令牌仍不出 Core。
package main

import (
	"encoding/base64"
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

// MeetingKeywordsRegenerate 单独补跑关键词区块（LLM 调用，秒级）。
func (a *App) MeetingKeywordsRegenerate(id string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingKeywordsRegenerate(a.ctx, id)
	a.reportError("meeting keywords regen", err)
	return result, err
}

// MeetingTodoAdd 手工新增一条待办（对标钉钉待办为一等实体）。
func (a *App) MeetingTodoAdd(id, task, ownerHint, dueHint string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingTodoAdd(a.ctx, id, knowledge.MeetingTodoInput{
		Task: &task, OwnerHint: &ownerHint, DueHint: &dueHint,
	})
	a.reportError("meeting todo add", err)
	return result, err
}

// MeetingTodoUpdate 编辑待办内容/负责人/期限或勾选完成（整行提交）。
func (a *App) MeetingTodoUpdate(id, todoID, task, ownerHint, dueHint string, done bool) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingTodoUpdate(a.ctx, id, todoID, knowledge.MeetingTodoInput{
		Task: &task, OwnerHint: &ownerHint, DueHint: &dueHint, Done: &done,
	})
	a.reportError("meeting todo update", err)
	return result, err
}

// MeetingTodoRemove 删除一条待办。
func (a *App) MeetingTodoRemove(id, todoID string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingTodoRemove(a.ctx, id, todoID)
	a.reportError("meeting todo remove", err)
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

// --- 实时上屏绑定（边说边出字：会话/推流/收尾/会中要点） ---

// MeetingStreamWarm 请求知识服务后台预热流式模型（听记页打开即调，首片不再等冷加载）。
func (a *App) MeetingStreamWarm() error {
	client, err := a.coreClient()
	if err != nil {
		return err
	}
	err = client.MeetingStreamWarm(a.ctx)
	a.reportError("meeting stream warm", err)
	return err
}

// MeetingStreamOpen 开一场实时录制会话，返回 recording 记录（其 id 即推流会话 id）。
func (a *App) MeetingStreamOpen(title, templateID string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingStreamOpen(a.ctx, title, templateID)
	a.reportError("meeting stream open", err)
	return result, err
}

// MeetingStreamChunk 推一片 PCM16/16kHz/单声道（base64），回当前已上屏字幕。
func (a *App) MeetingStreamChunk(id, pcmBase64 string) (knowledge.MeetingLiveView, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.MeetingLiveView{}, err
	}
	pcm, err := base64.StdEncoding.DecodeString(pcmBase64)
	if err != nil {
		return knowledge.MeetingLiveView{}, err
	}
	view, err := client.MeetingStreamChunk(a.ctx, id, pcm)
	a.reportError("meeting stream chunk", err)
	return view, err
}

// MeetingStreamStop 结束推流会话，回完整实时字幕行。
func (a *App) MeetingStreamStop(id string) (knowledge.MeetingLiveView, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.MeetingLiveView{}, err
	}
	view, err := client.MeetingStreamStop(a.ctx, id)
	a.reportError("meeting stream stop", err)
	return view, err
}

// MeetingLivePoints 会中要点（LLM 基于已上屏内容归纳，随录随长）。
func (a *App) MeetingLivePoints(id string) ([]string, error) {
	client, err := a.coreClient()
	if err != nil {
		return nil, err
	}
	points, err := client.MeetingLivePoints(a.ctx, id)
	a.reportError("meeting live points", err)
	return points, err
}

// MeetingVoiceprintsGet 当前用户已登记声纹（仅名字与样本时长，向量不出服务端）。
func (a *App) MeetingVoiceprintsGet() ([]knowledge.MeetingVoiceprint, error) {
	client, err := a.coreClient()
	if err != nil {
		return nil, err
	}
	items, err := client.MeetingVoiceprints(a.ctx)
	a.reportError("meeting voiceprints get", err)
	if items == nil {
		items = []knowledge.MeetingVoiceprint{}
	}
	return items, err
}

// MeetingVoiceprintRemove 删除一条声纹（生物特征数据必须可删）。
func (a *App) MeetingVoiceprintRemove(name string) ([]knowledge.MeetingVoiceprint, error) {
	client, err := a.coreClient()
	if err != nil {
		return nil, err
	}
	items, err := client.MeetingVoiceprintRemove(a.ctx, name)
	a.reportError("meeting voiceprint remove", err)
	if items == nil {
		items = []knowledge.MeetingVoiceprint{}
	}
	return items, err
}

// --- 个性化热词与重新转写 ---

// MeetingHotwordsGet 当前用户热词表（含每人上限，供前端显示 N/max）。
func (a *App) MeetingHotwordsGet() (knowledge.HotwordsResult, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.HotwordsResult{}, err
	}
	result, err := client.MeetingHotwords(a.ctx)
	a.reportError("meeting hotwords get", err)
	return result, err
}

// MeetingHotwordsAdd 批量新增热词（字符合法性与上限由知识服务校验），返回最新词表。
func (a *App) MeetingHotwordsAdd(words []string) (knowledge.HotwordsResult, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.HotwordsResult{}, err
	}
	result, err := client.MeetingHotwordsAdd(a.ctx, words)
	a.reportError("meeting hotwords add", err)
	return result, err
}

// MeetingHotwordRemove 删除一条热词，返回最新词表。
func (a *App) MeetingHotwordRemove(word string) (knowledge.HotwordsResult, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.HotwordsResult{}, err
	}
	result, err := client.MeetingHotwordRemove(a.ctx, word)
	a.reportError("meeting hotword remove", err)
	return result, err
}

// MeetingRetranscribe 重新转写整场（加了热词后重跑全管线，含总结/关键词/导图）。
func (a *App) MeetingRetranscribe(id string) (knowledge.Meeting, error) {
	client, err := a.coreClient()
	if err != nil {
		return knowledge.Meeting{}, err
	}
	result, err := client.MeetingRetranscribe(a.ctx, id)
	a.reportError("meeting retranscribe", err)
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
