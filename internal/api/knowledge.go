// 知识网关 API：桌面端与后续扩展访问知识服务的唯一代理通道。
// 会话令牌由 Core 持有（knowledge.json），前端只见登录态视图。
package api

import (
	"strconv"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"
	"time"

	"easyshare/internal/knowledge"
)

// knowledgeQueryTimeout 问答上限：多跳检索 + LLM 生成链路可能远超全局 30s WriteTimeout。
const knowledgeQueryTimeout = 120 * time.Second

// knowledgeUploadTimeout 会议音频上传受理上限：本机/局域网传数百 MB WAV，远端只落盘即返回。
const knowledgeUploadTimeout = 10 * time.Minute

// knowledgeSession 取当前会话（须已登录），未登录时写 401 并返回 false。
func (server *Server) knowledgeSession(writer http.ResponseWriter) (knowledge.Session, bool) {
	session := server.knowledge.Current()
	if session.Token == "" {
		writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "knowledge_not_logged_in", Message: "请先登录知识服务"})
		return knowledge.Session{}, false
	}
	return session, true
}

func (server *Server) knowledgeUnavailable(writer http.ResponseWriter) bool {
	if server.knowledge == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "knowledge_disabled", Message: "知识网关未启用"})
		return true
	}
	return false
}

// knowledgeStatus 返回本地登录态，不做网络探测，保证 UI 秒开。
func (server *Server) knowledgeStatus(writer http.ResponseWriter, _ *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	writeJSON(writer, http.StatusOK, server.knowledge.Status())
}

// knowledgeLogin 用服务器地址 + 账号密码登录远端，成功后 Core 落盘会话。
func (server *Server) knowledgeLogin(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	var input struct {
		ServerURL string `json:"serverUrl"`
		Username  string `json:"username"`
		Password  string `json:"password"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "invalid JSON"})
		return
	}
	if !knowledge.ValidServerURL(input.ServerURL) {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_server_url", Message: "服务器地址无效，应形如 http://192.168.1.10:8000"})
		return
	}
	if input.Username == "" || input.Password == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_credentials", Message: "账号与密码不能为空"})
		return
	}

	client := knowledge.NewClient(input.ServerURL)
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	session, err := client.Login(ctx, input.Username, input.Password)
	if err != nil {
		writeKnowledgeError(writer, "login", err)
		return
	}
	if err := server.knowledge.SignIn(session); err != nil {
		log.Printf("knowledge login persist: %v", err)
		writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "knowledge_persist_failed", Message: "登录成功但保存会话失败"})
		return
	}
	log.Printf("knowledge login ok: user=%s server=%s", session.Username, session.ServerURL)
	writeJSON(writer, http.StatusOK, server.knowledge.Status())
}

// knowledgeLogout 清空会话。
func (server *Server) knowledgeLogout(writer http.ResponseWriter, _ *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	if err := server.knowledge.SignOut(); err != nil {
		log.Printf("knowledge logout: %v", err)
		writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "knowledge_logout_failed", Message: err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"loggedOut": true})
}

// knowledgeHealth 探测远端 /health，供面板展示知识库规模与 LLM 状态。
func (server *Server) knowledgeHealth(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session := server.knowledge.Current()
	if session.ServerURL == "" {
		writeJSON(writer, http.StatusConflict, ErrorResponse{Code: "knowledge_not_configured", Message: "请先登录知识服务"})
		return
	}
	health, err := knowledge.NewClient(session.ServerURL).HealthWithTimeout(request.Context(), 5*time.Second)
	if err != nil {
		writeKnowledgeError(writer, "health", err)
		return
	}
	writeJSON(writer, http.StatusOK, health)
}

// knowledgeStats 代理远端 /stats（管理员汇总页聚合数字；days 1-90）。
func (server *Server) knowledgeStats(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session := server.knowledge.Current()
	if session.Token == "" {
		writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "knowledge_not_logged_in", Message: "请先登录知识服务"})
		return
	}
	days := 30
	if raw := request.URL.Query().Get("days"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil {
			days = value
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), 20*time.Second)
	defer cancel()
	stats, err := knowledge.NewClient(session.ServerURL).Stats(ctx, session.Token, days)
	if err != nil {
		writeKnowledgeError(writer, "stats", err)
		return
	}
	writeJSON(writer, http.StatusOK, stats)
}

// knowledgeQuery 代理远端 /query。全局 WriteTimeout 30s 不够 LLM 链路，先解除期限再转发。
func (server *Server) knowledgeQuery(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	var input struct {
		Question string `json:"question"`
		Mode     string `json:"mode"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Question == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "question required"})
		return
	}
	if input.Mode == "" {
		input.Mode = "ask"
	}
	session := server.knowledge.Current()
	if session.Token == "" {
		writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "knowledge_not_logged_in", Message: "请先登录知识服务"})
		return
	}

	// 解除 Core 全局读写期限，改由 120s 上下文兜底，避免长问答被拦腰截断。
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})

	ctx, cancel := context.WithTimeout(request.Context(), knowledgeQueryTimeout)
	defer cancel()
	var answer knowledge.Answer
	var err error
	if input.Mode == "search" {
		// 快搜：仅检索，短超时（不等 LLM）
		searchCtx, searchCancel := context.WithTimeout(request.Context(), 20*time.Second)
		defer searchCancel()
		answer, err = knowledge.NewClient(session.ServerURL).Search(searchCtx, session.Token, input.Question)
	} else {
		answer, err = knowledge.NewClient(session.ServerURL).Query(ctx, session.Token, input.Question)
	}
	if err != nil {
		var remote *knowledge.RemoteError
		if errors.As(err, &remote) && remote.Status == http.StatusUnauthorized {
			// 令牌已失效：清掉本地会话，前端刷新登录态后自然回到登录页。
			if signErr := server.knowledge.SignOut(); signErr != nil {
				log.Printf("knowledge sign out on expired token: %v", signErr)
			}
			writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "knowledge_auth_expired", Message: "登录已失效，请重新登录"})
			return
		}
		writeKnowledgeError(writer, "query", err)
		return
	}
	writeJSON(writer, http.StatusOK, answer)
}

// writeKnowledgeError 将远端/网络错误映射为面向用户的可读响应。
func writeKnowledgeError(writer http.ResponseWriter, operation string, err error) {
	var remote *knowledge.RemoteError
	switch {
	case errors.As(err, &remote) && remote.Status == http.StatusUnauthorized:
		writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "knowledge_invalid_credentials", Message: "账号或密码错误"})
	case errors.As(err, &remote):
		log.Printf("knowledge %s: remote %d: %s", operation, remote.Status, remote.Detail)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "knowledge_upstream_error", Message: "知识服务返回错误：" + remote.Detail})
	default:
		log.Printf("knowledge %s: %v", operation, err)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "knowledge_unreachable", Message: "无法连接知识服务器，请检查地址与网络"})
	}
}

// --- 会议听记（/api/knowledge/meetings/*，代理远端 /meetings） ---

// knowledgeMeetingCreate 代理会议音频上传（multipart 流式透传，不落内存）。
// 远端只落盘即返回 202，转写在远端异步执行，前端轮询详情。
func (server *Server) knowledgeMeetingCreate(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	contentType := request.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "multipart/form-data") {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "expected multipart/form-data"})
		return
	}

	// 大文件上传解除全局读写期限，改由 10min 上下文兜底
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(request.Context(), knowledgeUploadTimeout)
	defer cancel()

	meeting, err := knowledge.NewClient(session.ServerURL).UploadMeeting(ctx, session.Token, contentType, request.Body)
	if err != nil {
		writeKnowledgeError(writer, "meeting upload", err)
		return
	}
	writeJSON(writer, http.StatusAccepted, meeting)
}

// knowledgeMeetings 代理会议列表（不含分段全文）。
func (server *Server) knowledgeMeetings(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 && value <= 200 {
			limit = value
		}
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	meetings, err := knowledge.NewClient(session.ServerURL).Meetings(ctx, session.Token, limit)
	if err != nil {
		writeKnowledgeError(writer, "meetings", err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{"meetings": meetings})
}

// knowledgeMeetingByID 取路径中的会议 ID。
func knowledgeMeetingByID(request *http.Request) string {
	return request.PathValue("id")
}

// knowledgeMeeting 代理单条完整记录（分段 + 总结），前端受理后轮询此接口。
func (server *Server) knowledgeMeeting(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	meeting, err := knowledge.NewClient(session.ServerURL).Meeting(ctx, session.Token, knowledgeMeetingByID(request))
	if err != nil {
		writeKnowledgeError(writer, "meeting", err)
		return
	}
	writeJSON(writer, http.StatusOK, meeting)
}

// knowledgeMeetingDelete 代理删除会议记录与音频。
func (server *Server) knowledgeMeetingDelete(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	if err := knowledge.NewClient(session.ServerURL).DeleteMeeting(ctx, session.Token, knowledgeMeetingByID(request)); err != nil {
		writeKnowledgeError(writer, "meeting delete", err)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"deleted": true})
}

// knowledgeMeetingRename 代理发言人改名。
func (server *Server) knowledgeMeetingRename(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	var input struct {
		Speaker string `json:"speaker"`
		Name    string `json:"name"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Speaker == "" || input.Name == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "speaker and name required"})
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), 15*time.Second)
	defer cancel()
	meeting, err := knowledge.NewClient(session.ServerURL).RenameMeetingSpeaker(ctx, session.Token, knowledgeMeetingByID(request), input.Speaker, input.Name)
	if err != nil {
		writeKnowledgeError(writer, "meeting rename", err)
		return
	}
	writeJSON(writer, http.StatusOK, meeting)
}

// knowledgeMeetingArchive 代理归档入库。
func (server *Server) knowledgeMeetingArchive(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	// 归档含向量入库（embedding 可能较慢），放宽到 120s
	ctx, cancel := context.WithTimeout(request.Context(), knowledgeQueryTimeout)
	defer cancel()
	result, err := knowledge.NewClient(session.ServerURL).ArchiveMeeting(ctx, session.Token, knowledgeMeetingByID(request))
	if err != nil {
		writeKnowledgeError(writer, "meeting archive", err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// knowledgeMeetingAudio 代理音频回放（流式透传，解除读写期限）。
func (server *Server) knowledgeMeetingAudio(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	if err := knowledge.NewClient(session.ServerURL).StreamMeetingAudio(request.Context(), session.Token, knowledgeMeetingByID(request), writer); err != nil {
		// 响应头可能已写出，只能记日志
		log.Printf("knowledge meeting audio: %v", err)
	}
}

// knowledgeMeetingMindmap 代理思维导图重新生成（LLM 链路，慢）。
func (server *Server) knowledgeMeetingMindmap(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	// harness 阶段一+阶段二为多次 LLM 调用，会超全局 30s 写超时，解除之
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	ctx, cancel := context.WithTimeout(request.Context(), knowledgeQueryTimeout)
	defer cancel()
	meeting, err := knowledge.NewClient(session.ServerURL).RegenerateMeetingMindmap(ctx, session.Token, knowledgeMeetingByID(request))
	if err != nil {
		writeKnowledgeError(writer, "meeting mindmap", err)
		return
	}
	writeJSON(writer, http.StatusOK, meeting)
}

// knowledgeMeetingSummaryRegen 代理按模板重新生成总结（harness/LLM 慢链路）。
func (server *Server) knowledgeMeetingSummaryRegen(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	// 可选 body {"template_id": "..."}：指定模板（同时改写记录模板）；缺省用记录已有模板
	var summaryBody struct {
		TemplateID string `json:"template_id"`
	}
	_ = json.NewDecoder(request.Body).Decode(&summaryBody)
	ctx, cancel := context.WithTimeout(request.Context(), knowledgeQueryTimeout)
	defer cancel()
	meeting, err := knowledge.NewClient(session.ServerURL).RegenerateMeetingSummary(ctx, session.Token, knowledgeMeetingByID(request), summaryBody.TemplateID)
	if err != nil {
		writeKnowledgeError(writer, "meeting summary regen", err)
		return
	}
	writeJSON(writer, http.StatusOK, meeting)
}

// knowledgeMeetingAsk 代理听记 AI 问答（纪要+转写全文进上下文，LLM 慢链路）。
func (server *Server) knowledgeMeetingAsk(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	var askBody struct {
		Question string                      `json:"question"`
		History  []knowledge.MeetingChatTurn `json:"history"`
	}
	if err := json.NewDecoder(request.Body).Decode(&askBody); err != nil {
		writeKnowledgeError(writer, "meeting ask decode", err)
		return
	}
	ctx, cancel := context.WithTimeout(request.Context(), knowledgeQueryTimeout)
	defer cancel()
	result, err := knowledge.NewClient(session.ServerURL).MeetingAsk(ctx, session.Token, knowledgeMeetingByID(request), askBody.Question, askBody.History)
	if err != nil {
		writeKnowledgeError(writer, "meeting ask", err)
		return
	}
	writeJSON(writer, http.StatusOK, result)
}

// knowledgeMeetingExport 代理备忘录导出文件（markdown|docx，流式透传）。
func (server *Server) knowledgeMeetingExport(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	session, ok := server.knowledgeSession(writer)
	if !ok {
		return
	}
	format := request.PathValue("format")
	if format != "markdown" && format != "docx" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "unsupported export format"})
		return
	}
	controller := http.NewResponseController(writer)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	if err := knowledge.NewClient(session.ServerURL).StreamMeetingExport(request.Context(), session.Token, knowledgeMeetingByID(request), format, writer); err != nil {
		log.Printf("knowledge meeting export: %v", err)
	}
}
