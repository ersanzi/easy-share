package drive

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func digestAuthorization(t *testing.T, method, uri, username, password, challenge string) string {
	t.Helper()
	scheme, parameters, ok := parseDigestHeader(challenge)
	if !ok || !strings.EqualFold(scheme, "Digest") {
		t.Fatalf("invalid Digest challenge: %q", challenge)
	}
	nonce := parameters["nonce"]
	nonceCount, clientNonce := "00000001", "test-client-nonce"
	ha1 := md5Hex(username + ":" + parameters["realm"] + ":" + password)
	ha2 := md5Hex(method + ":" + uri)
	response := md5Hex(ha1 + ":" + nonce + ":" + nonceCount + ":" + clientNonce + ":auth:" + ha2)
	return fmt.Sprintf(
		`Digest username=%q, realm=%q, nonce=%q, uri=%q, algorithm=MD5, response=%q, qop=auth, nc=%s, cnonce=%q`,
		username, parameters["realm"], nonce, uri, response, nonceCount, clientNonce,
	)
}

func TestWebDAVAuthenticationAndReadWrite(t *testing.T) {
	root := t.TempDir()
	service := NewService(root, "EasyShare", "secret")
	if err := service.Start(0); err != nil {
		t.Fatal(err)
	}
	defer service.Stop(context.Background())

	url := "http://" + service.Addr() + "/note.txt"
	unauthorizedRequest, _ := http.NewRequest(http.MethodPut, url, strings.NewReader("hello"))
	unauthorizedResponse, err := http.DefaultClient.Do(unauthorizedRequest)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, unauthorizedResponse.Body)
	unauthorizedResponse.Body.Close()
	if unauthorizedResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.StatusCode)
	}
	challenge := unauthorizedResponse.Header.Get("WWW-Authenticate")
	if !strings.HasPrefix(challenge, "Digest ") {
		t.Fatalf("challenge = %q, want Digest", challenge)
	}

	request, _ := http.NewRequest(http.MethodPut, url, strings.NewReader("hello"))
	request.Header.Set("Authorization", digestAuthorization(t, http.MethodPut, "/note.txt", "EasyShare", "secret", challenge))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode >= 300 {
		t.Fatalf("PUT status = %d", response.StatusCode)
	}
	if data, err := os.ReadFile(filepath.Join(root, "note.txt")); err != nil || string(data) != "hello" {
		t.Fatalf("file = %q, error = %v", data, err)
	}
}

func TestWebDAVRejectsBasicAndInvalidDigestCredentials(t *testing.T) {
	service := NewService(t.TempDir(), "EasyShare", "secret")
	if err := service.Start(0); err != nil {
		t.Fatal(err)
	}
	defer service.Stop(context.Background())
	url := "http://" + service.Addr() + "/"

	basicRequest, _ := http.NewRequest(http.MethodOptions, url, nil)
	basicRequest.SetBasicAuth("EasyShare", "secret")
	basicResponse, err := http.DefaultClient.Do(basicRequest)
	if err != nil {
		t.Fatal(err)
	}
	challenge := basicResponse.Header.Get("WWW-Authenticate")
	basicResponse.Body.Close()
	if basicResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Basic status = %d", basicResponse.StatusCode)
	}

	digestRequest, _ := http.NewRequest(http.MethodOptions, url, nil)
	digestRequest.Header.Set("Authorization", digestAuthorization(t, http.MethodOptions, "/", "EasyShare", "wrong", challenge))
	digestResponse, err := http.DefaultClient.Do(digestRequest)
	if err != nil {
		t.Fatal(err)
	}
	digestResponse.Body.Close()
	if digestResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("invalid Digest status = %d", digestResponse.StatusCode)
	}
}
