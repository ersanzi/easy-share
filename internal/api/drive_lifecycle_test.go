package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"easyshare/internal/config"
	"easyshare/internal/task"
)

type orderedDriveService struct {
	events  *[]string
	running bool
}

func (service *orderedDriveService) Start(_ int) error {
	*service.events = append(*service.events, "start WebDAV")
	service.running = true
	return nil
}
func (service *orderedDriveService) Stop(_ context.Context) error {
	*service.events = append(*service.events, "stop WebDAV")
	service.running = false
	return nil
}
func (service *orderedDriveService) Running() bool { return service.running }

func postAction(t *testing.T, server *Server, path string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, path, nil)
	request.Header.Set("Authorization", "Bearer secret")
	recorder := httptest.NewRecorder()
	server.httpServer.Handler.ServeHTTP(recorder, request)
	return recorder
}

func driveTestServer() *Server {
	return NewServer(config.Config{
		APIToken:       "secret",
		WebDAVPort:     19080,
		WebDAVUsername: "EasyShare",
		WebDAVPassword: "password",
	}, task.NewStore())
}

func TestStartDriveRunsWebDAV(t *testing.T) {
	events := []string{}
	server := driveTestServer()
	service := &orderedDriveService{events: &events}
	server.ConfigureDrive(service)

	response := postAction(t, server, "/api/drive/start")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	want := []string{"start WebDAV"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	if !server.status.WebDAV {
		t.Fatalf("unexpected drive status: %+v", server.status)
	}
}

func TestShutdownCleansUpInDependencyOrder(t *testing.T) {
	events := []string{}
	server := driveTestServer()
	service := &orderedDriveService{events: &events, running: true}
	server.ConfigureDrive(service)
	server.setDriveStatus(true)
	server.ConfigureShutdown(func() { events = append(events, "cancel background") })

	response := postAction(t, server, "/api/shutdown")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	want := []string{
		"stop WebDAV",
		"cancel background",
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	select {
	case <-server.ShutdownSignal():
	default:
		t.Fatal("Core shutdown signal was not closed")
	}
	if server.status.WebDAV {
		t.Fatalf("unexpected drive status after shutdown: %+v", server.status)
	}
}
