package api

import (
	"log"
	"net/http"

	"easyshare/internal/knowledge"
)

// knowledgeResponseWriter 在发送头和每次正文写入前确认会话，覆盖导出及音频流。
// 已发送给调用方的内容无法撤回，但切换后不能继续发送旧账号的后续数据。
type knowledgeResponseWriter struct {
	http.ResponseWriter
	service *knowledge.Service
	session knowledge.Session
	started bool
	blocked bool
}

func (writer *knowledgeResponseWriter) current() bool {
	if writer.blocked {
		return false
	}
	if writer.service.IsCurrent(writer.session) {
		return true
	}
	writer.blocked = true
	if !writer.started {
		// 上游文件名、长度等也属于旧账号信息，不可留在拒绝响应中。
		for name := range writer.Header() {
			writer.Header().Del(name)
		}
		writeJSON(writer.ResponseWriter, http.StatusConflict, ErrorResponse{Code: "knowledge_session_changed", Message: "知识账号状态已变化，请重试"})
	}
	return false
}

func (writer *knowledgeResponseWriter) WriteHeader(status int) {
	if writer.current() {
		writer.started = true
		writer.ResponseWriter.WriteHeader(status)
	}
}

func (writer *knowledgeResponseWriter) Write(data []byte) (int, error) {
	if !writer.current() {
		return 0, knowledge.ErrSessionChanged
	}
	writer.started = true
	return writer.ResponseWriter.Write(data)
}

func (server *Server) writeKnowledgeJSON(writer http.ResponseWriter, session knowledge.Session, status int, value any) {
	writeJSON(&knowledgeResponseWriter{ResponseWriter: writer, service: server.knowledge, session: session}, status, value)
}

// 尚未开始下载时传递真实错误；已发送正文后中止连接，避免截断文件被标记为成功。
func (server *Server) finishKnowledgeStream(writer *knowledgeResponseWriter, operation string, err error) {
	log.Printf("knowledge %s stream: %v", operation, err)
	if writer.started {
		panic(http.ErrAbortHandler)
	}
	if !writer.blocked {
		writeKnowledgeError(writer, operation, err)
	}
}
