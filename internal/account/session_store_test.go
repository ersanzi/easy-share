package account

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSessionStoreRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account-session.json")
	expiresAt := time.Now().Add(7 * 24 * time.Hour).Truncate(time.Second)
	want := &Session{
		Token:       "tk-test",
		ExpireIn:    604800,
		ExpiresAt:   expiresAt,
		User:        User{UserID: "1", UserName: "lifeng", NickName: "李丰"},
		Roles:       []string{"superadmin"},
		Permissions: []string{"*:*:*"},
	}
	if err := SaveSession(path, want); err != nil {
		t.Fatalf("SaveSession: %v", err)
	}
	got, err := LoadSession(path)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if got.Token != want.Token || got.ExpireIn != want.ExpireIn || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("session metadata mismatch: %+v", got)
	}
	if got.User != want.User || !reflect.DeepEqual(got.Roles, want.Roles) || !reflect.DeepEqual(got.Permissions, want.Permissions) {
		t.Fatalf("session identity mismatch: %+v", got)
	}
	if got.Expired(time.Now()) {
		t.Fatal("fresh session reported expired")
	}
	if !got.Expired(expiresAt.Add(time.Second)) {
		t.Fatal("expired session not detected")
	}
	if err := RemoveSession(path); err != nil {
		t.Fatalf("RemoveSession: %v", err)
	}
	if _, err := LoadSession(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("LoadSession after remove = %v, want os.ErrNotExist", err)
	}
}

func TestSessionStoreRejectsEmptyToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "account-session.json")
	if err := SaveSession(path, &Session{}); err == nil {
		t.Fatal("SaveSession accepted empty token")
	}
}

func TestAuthenticatedRequestMarksUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "msg": "登录状态已过期"})
	}))
	defer server.Close()

	_, err := New(server.URL).MySpaces(context.Background(), "expired-token")
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("MySpaces error = %v, want ErrUnauthorized", err)
	}
}

func TestLoginBusinessErrorIsNotUnauthorized(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"code": 401, "msg": "密码输入错误1次"})
	}))
	defer server.Close()

	_, err := New(server.URL).Login(context.Background(), "lifeng", "wrong")
	if err == nil {
		t.Fatal("Login succeeded on business error")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Login error must stay user-facing, got %v", err)
	}
}
