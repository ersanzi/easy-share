package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"easyshare/internal/config"
	"easyshare/internal/drive"
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

type orderedMapper struct{ events *[]string }

func (mapper *orderedMapper) Map(_ context.Context, _, remote, _, _ string) error {
	*mapper.events = append(*mapper.events, "map "+remote)
	return nil
}
func (mapper *orderedMapper) Unmap(_ context.Context, _, remote string) error {
	*mapper.events = append(*mapper.events, "unmap "+remote)
	return nil
}

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
		DriveLetter:    "Z:",
	}, task.NewStore())
}

func TestMapDriveStartsWebDAVBeforeMapping(t *testing.T) {
	events := []string{}
	server := driveTestServer()
	service := &orderedDriveService{events: &events}
	server.ConfigureDrive(service, &orderedMapper{events: &events})

	response := postAction(t, server, "/api/drive/map")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	want := []string{"start WebDAV", "map http://127.0.0.1:19080"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("events = %#v, want %#v", events, want)
	}
	if !server.status.WebDAV || !server.status.DriveMapped {
		t.Fatalf("unexpected drive status: %+v", server.status)
	}
}

type occupiedMapper struct{}

func (occupiedMapper) Map(context.Context, string, string, string, string) error {
	return drive.ErrDriveOccupied
}
func (occupiedMapper) Unmap(context.Context, string, string) error { return nil }

func TestMapDriveExplainsOccupiedLetter(t *testing.T) {
	events := []string{}
	server := driveTestServer()
	server.ConfigureDrive(&orderedDriveService{events: &events}, occupiedMapper{})

	response := postAction(t, server, "/api/drive/map")
	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	var apiError ErrorResponse
	if err := json.Unmarshal(response.Body.Bytes(), &apiError); err != nil {
		t.Fatal(err)
	}
	want := "Z: 已被其他磁盘或网络位置占用，请释放该盘符后重试"
	if apiError.Message != want {
		t.Fatalf("message = %q, want %q", apiError.Message, want)
	}
	if !server.status.WebDAV || server.status.DriveMapped {
		t.Fatalf("unexpected drive status: %+v", server.status)
	}
}

func TestShutdownCleansUpInDependencyOrder(t *testing.T) {
	events := []string{}
	server := driveTestServer()
	service := &orderedDriveService{events: &events, running: true}
	server.ConfigureDrive(service, &orderedMapper{events: &events})
	server.setDriveStatus(true, true)
	server.ConfigureShutdown(func() { events = append(events, "cancel background") })

	response := postAction(t, server, "/api/shutdown")
	if response.Code != http.StatusAccepted {
		t.Fatalf("status = %d; body = %s", response.Code, response.Body.String())
	}
	want := []string{
		"unmap http://127.0.0.1:19080",
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
	if server.status.WebDAV || server.status.DriveMapped {
		t.Fatalf("unexpected drive status after shutdown: %+v", server.status)
	}
}
