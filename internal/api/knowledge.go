// 知识网关 API：桌面端与后续扩展访问知识服务的唯一代理通道。
// 会话令牌由 Core 持有（knowledge.json），前端只见登录态视图。
package api

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"easyshare/internal/knowledge"
)

// knowledgeQueryTimeout 问答上限：多跳检索 + LLM 生成链路可能远超全局 30s WriteTimeout。
const knowledgeQueryTimeout = 120 * time.Second

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

// knowledgeQuery 代理远端 /query。全局 WriteTimeout 30s 不够 LLM 链路，先解除期限再转发。
func (server *Server) knowledgeQuery(writer http.ResponseWriter, request *http.Request) {
	if server.knowledgeUnavailable(writer) {
		return
	}
	var input struct {
		Question string `json:"question"`
	}
	if err := json.NewDecoder(request.Body).Decode(&input); err != nil || input.Question == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "question required"})
		return
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
	answer, err := knowledge.NewClient(session.ServerURL).Query(ctx, session.Token, input.Question)
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
