package plugin

import (
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// sdkMountPath 公共 SDK 在插件静态服务下的挂载路径。
// 插件 HTML 内以 <script src="/plugins/_sdk/eshare.js"> 引用，版本随宿主统一。
const sdkMountPath = "/plugins/_sdk/"

// HTTPHandler 返回挂在 Wails AssetServer fallback Handler 上的静态文件服务：
//
//	/plugins/_sdk/{file}   宿主内嵌的公共 SDK（embed FS）
//	/plugins/{id}/{path}   已安装且未禁用插件的包内文件
//
// 所有响应 no-store：插件可能刚被更新，且内容只在受控路径内。
func (m *Manager) HTTPHandler(sdkFS fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只服务 GET/HEAD；AssetServer 语义下非 GET 本不会进来，防御性拒绝。
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		path := filepath.ToSlash(r.URL.Path)
		if !strings.HasPrefix(path, "/plugins/") {
			http.NotFound(w, r)
			return
		}

		// 公共 SDK 分支。
		if strings.HasPrefix(path, sdkMountPath) {
			file := strings.TrimPrefix(path, sdkMountPath)
			if file == "" || strings.Contains(file, "..") {
				http.NotFound(w, r)
				return
			}
			data, err := fs.ReadFile(sdkFS, filepath.ToSlash(filepath.Join("sdk", filepath.FromSlash(file))))
			if err != nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", contentTypeFor(file))
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write(data)
			return
		}

		// 插件包文件分支：/plugins/{id}/{path...}
		rest := strings.TrimPrefix(path, "/plugins/")
		parts := strings.SplitN(rest, "/", 2)
		id := parts[0]
		if err := ValidateID(id); err != nil {
			http.NotFound(w, r)
			return
		}
		if info, ok := m.Get(id); !ok || info.Disabled {
			// 未安装或已禁用：一律 404，不给探测面。
			http.NotFound(w, r)
			return
		}
		rel := "index.html"
		if len(parts) == 2 && parts[1] != "" {
			rel = parts[1]
		}
		// 路径清洗：解出的最终路径必须落在插件目录内（防穿越）。
		pluginRoot := m.pluginDir(id)
		full := filepath.Join(pluginRoot, filepath.FromSlash(rel))
		cleanRoot := filepath.Clean(pluginRoot)
		if full != cleanRoot && !strings.HasPrefix(full, cleanRoot+string(filepath.Separator)) {
			http.NotFound(w, r)
			return
		}
		if rel == "manifest.json" || strings.HasPrefix(rel, ".staging") {
			// manifest 走 API（PluginList 已含信息），staging 目录不可访问。
			http.NotFound(w, r)
			return
		}
		stat, err := os.Stat(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if stat.IsDir() {
			// 目录回退 index.html（与插件入口约定一致）。
			full = filepath.Join(full, "index.html")
			if _, err := os.Stat(full); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		data, err := os.ReadFile(full)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentTypeFor(full))
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(data)
	})
}

// contentTypeFor 按扩展名给 Content-Type（插件包内全是静态 Web 资源）。
func contentTypeFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".svg":
		return "image/svg+xml"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ico":
		return "image/x-icon"
	case ".txt", ".md":
		return "text/plain; charset=utf-8"
	case ".woff2":
		return "font/woff2"
	default:
		return "application/octet-stream"
	}
}
