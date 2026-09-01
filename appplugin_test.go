package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// 回归：dev 模式下 Vite 对未知路径回 200，若 /plugins/、/clipboard-files/
// 不在 Middleware 阶段被宿主接管，插件 iframe 会加载到前端应用本身。
// 断言这两个前缀绕过默认资产链（next），其余路径正常放行。
func TestAssetMiddlewareRoutesDynamicPrefixes(t *testing.T) {
	app := NewApp()
	pluginHit := false
	app.assetMux.Handle("/plugins/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pluginHit = true
		w.WriteHeader(http.StatusOK)
	}))
	filesHit := false
	app.assetMux.Handle("/clipboard-files/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		filesHit = true
		w.WriteHeader(http.StatusOK)
	}))

	nextCalled := 0
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled++
		w.WriteHeader(http.StatusTeapot)
	})
	handler := pluginAssetMiddleware(app)(next)

	cases := []struct {
		path      string
		muxHit    *bool
		nextCalls int
		status    int
	}{
		{"/plugins/clipboard/index.html", &pluginHit, 0, http.StatusOK},
		{"/plugins/_sdk/eshare.js", &pluginHit, 0, http.StatusOK},
		{"/plugins/todo/app.js", &pluginHit, 0, http.StatusOK},
		{"/clipboard-files/img/abc.png", &filesHit, 0, http.StatusOK},
		{"/", nil, 1, http.StatusTeapot},
		{"/src/main.ts", nil, 1, http.StatusTeapot},
	}

	for _, c := range cases {
		pluginHit, filesHit = false, false
		before := nextCalled
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != c.status {
			t.Errorf("GET %s status = %d, want %d", c.path, rec.Code, c.status)
		}
		if nextCalled-before != c.nextCalls {
			t.Errorf("GET %s next 调用次数 = %d, want %d（前缀必须绕过默认链）", c.path, nextCalled-before, c.nextCalls)
		}
		if c.muxHit != nil && !*c.muxHit {
			t.Errorf("GET %s 未进入宿主 mux", c.path)
		}
	}
}
