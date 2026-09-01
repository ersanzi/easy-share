// 一次性冒烟：验证插件安装全链路（安装 zip → 登记 → storage 鉴权 → 未授权拒绝 → 卸载）。
// 用法：go run ./scripts/diag/plugin-smoke <zip 路径>；跑完本目录即删。
package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing/fstest"

	"easyshare/internal/plugin"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("用法: plugin-smoke <zip>")
		os.Exit(1)
	}
	root, _ := os.MkdirTemp("", "plugin-smoke-*")
	defer os.RemoveAll(root)

	m, err := plugin.NewManager(root)
	must("NewManager", err)

	// 1) 安装 todo 插件 zip
	man, err := m.InstallZip(os.Args[1])
	must("InstallZip", err)
	fmt.Printf("安装成功: %s v%s (permissions=%v)\n", man.ID, man.Version, man.Permissions)

	// 2) 列表可见
	list := m.List()
	if len(list) != 1 || list[0].ID != "todo" {
		fatalf("List 应含 todo，实际 %+v", list)
	}
	fmt.Println("列表登记 OK:", list[0].Name, "entry:", list[0].Entry)

	// 3) storage 能力：授权通过 + 数据隔离持久化
	reg := plugin.NewRegistry()
	_, err = m.InvokeFor(reg, "todo", "storage.set", raw(`{"key":"week","value":[1,2,3]}`))
	must("storage.set", err)
	got, err := m.InvokeFor(reg, "todo", "storage.get", raw(`{"key":"week"}`))
	must("storage.get", err)
	fmt.Println("storage 读写 OK:", string(mustJSON(got)))

	// 4) 未授权能力被拒：todo 没有 clipboard.read
	_, err = m.InvokeFor(reg, "todo", "clipboard.history", raw(`{}`))
	if err == nil {
		fatalf("clipboard.history 应被拒绝（todo 未声明 clipboard.read）")
	}
	fmt.Println("未授权拒绝 OK:", err)

	// 5) 未知能力被拒
	_, err = m.InvokeFor(reg, "todo", "evil.exfiltrate", raw(`{}`))
	if err == nil {
		fatalf("未知能力应被拒绝")
	}
	fmt.Println("未知能力拒绝 OK:", err)

	// 6) 内置插件保护：EnsureBuiltin 后不可卸载/禁用/被外部包覆盖
	//    （此处只验证登记语义：外部安装同 id 内置插件会被拒——用 clipboard id 试）
	_ = m.EnsureBuiltin(builtinFS())
	info, ok := m.Get("clipboard")
	if !ok || !info.Builtin {
		fatalf("内置插件 clipboard 应已登记且 builtin=true")
	}
	if err := m.Uninstall("clipboard"); err == nil {
		fatalf("内置插件应不可卸载")
	}
	if err := m.SetDisabled("clipboard", true); err == nil {
		fatalf("内置插件应不可禁用")
	}
	fmt.Println("内置插件保护 OK（不可卸载/不可禁用）")

	// 6.5) 权限同意：预览返回权限清单；未同意的新权限被拒、同意后放行
	preview, err := m.PreviewInstall(readZip(os.Args[1]), "")
	must("PreviewInstall", err)
	if !preview.IsUpdate || preview.InstalledVersion != "1.0.0" {
		fatalf("预览应识别为更新：isUpdate=%v installed=%q", preview.IsUpdate, preview.InstalledVersion)
	}
	// 构造带新权限的包：基于 todo zip 重打包，manifest 增加 clipboard.read
	tampered := withExtraPermission(os.Args[1])
	_, err = m.InstallWithConsent(tampered, "", []string{})
	if err == nil {
		fatalf("未同意的新权限应被拒绝")
	}
	fmt.Println("权限同意拒绝 OK:", err)
	man2, err := m.InstallWithConsent(tampered, "", []string{"clipboard.read"})
	must("InstallWithConsent(同意后)", err)
	if man2.Version != "1.0.0" {
		fatalf("更新后版本不符: %s", man2.Version)
	}
	got2, _ := m.Get("todo")
	if !containsPerm(got2.Permissions, "clipboard.read") {
		fatalf("同意后的新权限应生效")
	}
	fmt.Println("权限同意放行 OK（同意 clipboard.read 后更新生效）")

	// 7) 卸载外部插件
	if err := m.Uninstall("todo"); err != nil {
		fatalf("卸载 todo 失败: %v", err)
	}
	if _, err := m.InvokeFor(reg, "todo", "storage.get", raw(`{"key":"week"}`)); err == nil {
		fatalf("卸载后调用应被拒绝")
	}
	fmt.Println("卸载 OK（后续调用被拒）")

	// 8) 静态 serve：正常文件 200、穿越 404、未安装 404、SDK 200
	m2, err := plugin.NewManager(root)
	must("NewManager(2)", err)
	_, err = m2.InstallZip(os.Args[1])
	must("InstallZip(2)", err)
	handler := m2.HTTPHandler(sdkFS())
	checkHTTP(handler, "/plugins/todo/index.html", 200)
	checkHTTP(handler, "/plugins/todo/style.css", 200)
	checkHTTP(handler, "/plugins/todo/../../../config.json", 404)
	checkHTTP(handler, "/plugins/not-installed/index.html", 404)
	checkHTTP(handler, "/plugins/_sdk/eshare.js", 200)
	fmt.Println("静态 serve 安全检查 OK")

	fmt.Println("\n全部冒烟通过 ✅")
}

// checkHTTP 断言 handler 对 path 的响应状态码。
func checkHTTP(handler http.Handler, path string, want int) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != want {
		fatalf("GET %s 期望 %d 实际 %d", path, want, rec.Code)
	}
}

// sdkFS 用内存 FS 模拟公共 SDK（真实资产见 assets/sdk/）。
func sdkFS() fstest.MapFS {
	return fstest.MapFS{
		"sdk/eshare.js": &fstest.MapFile{Data: []byte("// eshare sdk")},
	}
}

// readZip 读入 zip 文件内容（PreviewInstall 用）。
func readZip(path string) []byte {
	data, err := os.ReadFile(path)
	must("readZip", err)
	return data
}

// withExtraPermission 基于 todo zip 重打包：manifest 权限里追加 clipboard.read，
// 模拟「插件升级时申请新权限」的场景。
func withExtraPermission(orig string) []byte {
	src, err := zip.NewReader(bytes.NewReader(readZip(orig)), int64(len(readZip(orig))))
	must("打开原包", err)
	var buf bytes.Buffer
	w := zip.NewWriter(&buf)
	for _, f := range src.File {
		data, err := f.Open()
		must("读包内文件", err)
		content, _ := io.ReadAll(data)
		data.Close()
		if f.Name == "manifest.json" {
			content = []byte(strings.Replace(string(content),
				`"permissions": ["storage", "clipboard.write", "notification", "drive.upload"]`,
				`"permissions": ["storage", "clipboard.write", "notification", "drive.upload", "clipboard.read"]`, 1))
		}
		out, err := w.Create(f.Name)
		must("写包内文件", err)
		_, _ = out.Write(content)
	}
	must("关闭 zip", w.Close())
	return buf.Bytes()
}

func containsPerm(list []string, target string) bool {
	for _, v := range list {
		if v == target {
			return true
		}
	}
	return false
}

func must(what string, err error) {
	if err != nil {
		fatalf("%s 失败: %v", what, err)
	}
}

func fatalf(f string, args ...any) {
	fmt.Printf("❌ "+f+"\n", args...)
	os.Exit(1)
}

func raw(s string) json.RawMessage { return json.RawMessage(s) }

// builtinFS 用内存 FS 模拟随宿主内嵌的内置插件（真实资产见仓库 assets/builtin-plugins/）。
func builtinFS() fstest.MapFS {
	return fstest.MapFS{
		"clipboard/manifest.json": &fstest.MapFile{Data: []byte(`{
			"id": "clipboard", "name": "剪切板", "version": "1.0.0",
			"description": "内置剪切板记录", "icon": "📋",
			"permissions": ["clipboard.read", "clipboard.write", "clipboard.events"]
		}`)},
		"clipboard/index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
