// 听记 MCP 端点：员工自己的 AI 客户端经 MCP 协议读取其账号可见的听记记录。
// 鉴权用独立配对令牌（config.mcpToken），与桌面端 apiToken 分离，可单独吊销；
// /api/mcp/* 管理端点仍走 apiToken（仅桌面端设置页调用）。
package api

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"easyshare/internal/config"
	"easyshare/internal/knowledge"
	"easyshare/internal/meetingsmcp"
	"easyshare/internal/version"
)

var (
	mcpServerOnce sync.Once
	mcpEndpoint   http.Handler
)

// serveMCP 是 /mcp 入口：先校验配对令牌，再交给 MCP Streamable HTTP handler。
func (server *Server) serveMCP(writer http.ResponseWriter, request *http.Request) {
	token := mcpBearerToken(request)
	if token == "" || !secureEquals(token, server.config.MCPToken) {
		writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "mcp_unauthorized", Message: "缺少或错误的 MCP 配对令牌"})
		return
	}
	mcpServerOnce.Do(func() {
		mcpEndpoint = mcp.NewStreamableHTTPHandler(
			func(*http.Request) *mcp.Server {
				return meetingsmcp.New(meetingsmcp.Options{
					Version: version.Version,
					Lookup:  server.knowledgeSessionLookup,
				})
			},
			nil,
		)
	})
	mcpEndpoint.ServeHTTP(writer, request)
}

// knowledgeSessionLookup 供 MCP 工具取桌面端登录态；未登录/网关未启用时给可行动的错误文案。
func (server *Server) knowledgeSessionLookup() (knowledge.Session, error) {
	if server.knowledge == nil {
		return knowledge.Session{}, errors.New("知识网关未启用")
	}
	session := server.knowledge.Current()
	if session.Token == "" {
		return session, errors.New("请先在 EasyShare 桌面端登录知识服务")
	}
	return session, nil
}

// mcpInfo 返回 AI 客户端接入所需的信息（URL + 配对令牌）。
func (server *Server) mcpInfo(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, server.mcpConnectionInfo())
}

// mcpRegenerate 重新生成配对令牌（吊销旧令牌），更新内存并持久化。
func (server *Server) mcpRegenerate(writer http.ResponseWriter, _ *http.Request) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "mcp_token_generate", Message: "生成令牌失败"})
		return
	}
	server.config.MCPToken = hex.EncodeToString(raw)
	if server.configPath != "" {
		if err := config.Save(server.configPath, server.config); err != nil {
			writeJSON(writer, http.StatusInternalServerError, ErrorResponse{Code: "mcp_token_persist", Message: err.Error()})
			return
		}
	}
	log.Printf("MCP 配对令牌已重新生成")
	writeJSON(writer, http.StatusOK, server.mcpConnectionInfo())
}

func (server *Server) mcpConnectionInfo() map[string]string {
	return map[string]string{
		"url":   "http://127.0.0.1:" + strconv.Itoa(server.config.APIPort) + "/mcp",
		"token": server.config.MCPToken,
	}
}

func mcpBearerToken(request *http.Request) string {
	header := request.Header.Get("Authorization")
	if !strings.HasPrefix(header, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(header, "Bearer "))
}

// secureEquals 恒时比较，避免令牌逐字节试探。
func secureEquals(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
