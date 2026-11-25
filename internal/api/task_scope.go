package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"easyshare/internal/account"
	"easyshare/internal/task"
)

const taskAccountHeader = "X-EasyShare-Account-Token"

// taskOwner 不信任客户端声明的 userId；仅使用控制面实时确认的稳定身份。
func (server *Server) taskOwner(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	base := strings.TrimRight(strings.TrimSpace(server.config.PlatformBaseURL), "/")
	if base == "" {
		return "", fmt.Errorf("未配置账号服务")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	permission, err := account.New(base).MyPermission(ctx, token)
	if err != nil {
		return "", err
	}
	if permission == nil || permission.UserID == "" || !permission.Drive {
		return "", fmt.Errorf("无法确认网盘任务访问权限")
	}
	digest := sha256.Sum256([]byte(base + "\x00" + permission.UserID))
	return hex.EncodeToString(digest[:]), nil
}

func (server *Server) requestTaskOwner(writer http.ResponseWriter, request *http.Request) (string, bool) {
	owner, err := server.taskOwner(request.Context(), request.Header.Get(taskAccountHeader))
	if err != nil {
		writeJSON(writer, http.StatusForbidden, ErrorResponse{Code: "task_identity_unavailable", Message: "无法确认任务所属账号，请重新登录或检查账号服务"})
		return "", false
	}
	return owner, true
}

// taskViewOwner 在账号校验失败时仅返回设备任务，并明确标记受限范围。
// LAN 本来不依赖企业身份，控制面故障不能阻断设备事件与局域网历史。
func (server *Server) taskViewOwner(writer http.ResponseWriter, request *http.Request) string {
	owner, err := server.taskOwner(request.Context(), request.Header.Get(taskAccountHeader))
	if err != nil || owner == "" {
		writer.Header().Set("X-EasyShare-Task-Scope", "device")
		return ""
	}
	writer.Header().Set("X-EasyShare-Task-Scope", "account")
	return owner
}

func (server *Server) listTasks(writer http.ResponseWriter, request *http.Request) {
	writeJSON(writer, http.StatusOK, server.tasks.ListFor(server.taskViewOwner(writer, request)))
}

// 设备级任务不需要企业身份；账号级修改仍严格校验，不降级成成功。
func (server *Server) taskMutationOwner(writer http.ResponseWriter, request *http.Request) (string, bool) {
	value, exists := server.tasks.Get(request.PathValue("id"))
	if exists && value.VisibleTo("") {
		return "", true
	}
	return server.requestTaskOwner(writer, request)
}

// checkTaskPath 防止旧页面持有的云下载路径绕过账号隔离直接打开文件。
// 这约束应用入口；同一 Windows 用户的操作系统文件权限不由任务历史改变。
func (server *Server) checkTaskPath(writer http.ResponseWriter, request *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if json.NewDecoder(request.Body).Decode(&input) != nil || strings.TrimSpace(input.Path) == "" {
		writeJSON(writer, http.StatusBadRequest, ErrorResponse{Code: "invalid_path", Message: "文件路径无效"})
		return
	}
	normalize := func(value string) string {
		value, _ = filepath.Abs(filepath.Clean(value))
		if runtime.GOOS == "windows" {
			value = strings.ToLower(value)
		}
		return value
	}
	target := normalize(input.Path)
	var latest *task.Task
	for _, value := range server.tasks.List() {
		if value.Kind != task.KindCloudDownload || value.LocalPath == "" || normalize(value.LocalPath) != target {
			continue
		}
		// 完成记录才能证明目标文件已落盘；同一路径以最后完成的归属为准。
		if latest == nil || (value.Status == task.StatusCompleted && (latest.Status != task.StatusCompleted || value.UpdatedAt.After(latest.UpdatedAt))) {
			copy := value
			latest = &copy
		}
	}
	if latest != nil {
		owner, ok := server.requestTaskOwner(writer, request)
		if !ok {
			return
		}
		if owner == "" || latest.OwnerID == "" || latest.OwnerID != owner {
			writeJSON(writer, http.StatusForbidden, ErrorResponse{Code: "task_path_forbidden", Message: "该下载文件不属于当前账号"})
			return
		}
	}
	writeJSON(writer, http.StatusOK, map[string]bool{"allowed": true})
}
