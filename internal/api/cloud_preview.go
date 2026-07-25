package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"time"

	"easyshare/internal/cloud"
)

const previewTicketTTL = 5 * time.Minute

// cloudPreview 返回预览能力和短期内容票据，避免把长期 API Token 暴露给 WebView 资源请求。
func (server *Server) cloudPreview(writer http.ResponseWriter, request *http.Request) {
	if server.cloud == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "cloud_disabled", Message: "cloud drive not configured"})
		return
	}
	key := request.URL.Query().Get("key")
	if key == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "key required"})
		return
	}
	preview, err := server.cloud.PreviewInfo(request.Context(), key)
	if err != nil {
		log.Printf("cloud preview: %v", err)
		writeJSON(writer, http.StatusBadGateway, ErrorResponse{Code: "cloud_preview_failed", Message: err.Error()})
		return
	}
	if preview.Kind == cloud.PreviewImage || preview.Kind == cloud.PreviewPDF {
		expires := time.Now().Add(previewTicketTTL).Unix()
		query := url.Values{
			"key":       []string{key},
			"expires":   []string{strconv.FormatInt(expires, 10)},
			"signature": []string{server.previewSignature(key, expires)},
		}
		preview.ContentURL = "/api/cloud/preview/content?" + query.Encode()
	}
	writeJSON(writer, http.StatusOK, preview)
}

// cloudPreviewContent 使用短期 HMAC 票据流式返回可安全内嵌的图片或 PDF。
func (server *Server) cloudPreviewContent(writer http.ResponseWriter, request *http.Request) {
	if server.cloud == nil {
		writeJSON(writer, http.StatusServiceUnavailable, ErrorResponse{Code: "cloud_disabled", Message: "cloud drive not configured"})
		return
	}
	key := request.URL.Query().Get("key")
	expires, err := strconv.ParseInt(request.URL.Query().Get("expires"), 10, 64)
	if key == "" || err != nil || !server.validPreviewTicket(key, expires, request.URL.Query().Get("signature")) {
		writeJSON(writer, http.StatusUnauthorized, ErrorResponse{Code: "preview_ticket_invalid", Message: "preview link is invalid or expired"})
		return
	}
	preview, body, err := server.cloud.OpenPreview(request.Context(), key)
	if err != nil {
		log.Printf("cloud preview content: %v", err)
		writeJSON(writer, http.StatusUnsupportedMediaType, ErrorResponse{Code: "cloud_preview_unsupported", Message: err.Error()})
		return
	}
	defer body.Close()

	writer.Header().Set("Content-Type", preview.ContentType)
	writer.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": filepath.Base(preview.Name)}))
	writer.Header().Set("Content-Length", strconv.FormatInt(preview.Size, 10))
	writer.Header().Set("Cache-Control", "private, max-age=300")
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusOK)
	if _, err := io.Copy(writer, body); err != nil {
		log.Printf("cloud preview stream: %v", err)
	}
}

func (server *Server) previewSignature(key string, expires int64) string {
	mac := hmac.New(sha256.New, []byte(server.config.APIToken))
	_, _ = fmt.Fprintf(mac, "%s\n%d", key, expires)
	return hex.EncodeToString(mac.Sum(nil))
}

func (server *Server) validPreviewTicket(key string, expires int64, signature string) bool {
	now := time.Now().Unix()
	if expires < now || expires > time.Now().Add(previewTicketTTL+time.Minute).Unix() {
		return false
	}
	expected, err := hex.DecodeString(server.previewSignature(key, expires))
	if err != nil {
		return false
	}
	provided, err := hex.DecodeString(signature)
	return err == nil && hmac.Equal(expected, provided)
}
