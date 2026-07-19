package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"easyshare/internal/config"
	"easyshare/internal/task"
)

func TestHealthAndAuthentication(t *testing.T) {
	server := NewServer(config.Config{APIToken: "secret", DeviceID: "device"}, task.NewStore())
	testServer := httptest.NewServer(server.httpServer.Handler)
	defer testServer.Close()
	response, err := http.Get(testServer.URL + "/health?nonce=test")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}
	response, err = http.Get(testServer.URL + "/api/tasks")
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d", response.StatusCode)
	}
	request, _ := http.NewRequest(http.MethodGet, testServer.URL+"/api/tasks", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authenticated status = %d", response.StatusCode)
	}
}

func TestUnknownRouteIsJSON404(t *testing.T) {
	server := NewServer(config.Config{APIToken: "secret"}, task.NewStore())
	recorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusNotFound || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected response: %d %s", recorder.Code, recorder.Header().Get("Content-Type"))
	}
}
