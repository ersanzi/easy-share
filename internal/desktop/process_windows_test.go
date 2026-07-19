package desktop

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCoreHealthyVerifiesIdentityAndProof(t *testing.T) {
	const token = "secret"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		nonce := request.URL.Query().Get("nonce")
		mac := hmac.New(sha256.New, []byte(token))
		_, _ = mac.Write([]byte(nonce))
		_, _ = writer.Write([]byte(`{"deviceId":"device","proof":"` + hex.EncodeToString(mac.Sum(nil)) + `"}`))
	}))
	defer server.Close()

	options := ProcessOptions{BaseURL: server.URL, Token: token, DeviceID: "device"}
	if !CoreHealthy(options) {
		t.Fatal("CoreHealthy rejected a matching Core")
	}
	options.Token = "wrong"
	if CoreHealthy(options) {
		t.Fatal("CoreHealthy accepted an invalid proof")
	}
	options.Token, options.DeviceID = token, "wrong"
	if CoreHealthy(options) {
		t.Fatal("CoreHealthy accepted a different device identity")
	}
}
