package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"easyshare/internal/cloud"
	"easyshare/internal/cloud/objectstore"
	"easyshare/internal/cloud/objectstore/memory"
	"easyshare/internal/config"
	"easyshare/internal/task"
)

func TestCloudPreviewRequiresAuthentication(t *testing.T) {
	server, _, testServer := newCloudPreviewTestServer(t)
	response, err := http.Get(testServer.URL + "/api/cloud/preview?key=note.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
	if server.cloud == nil {
		t.Fatal("cloud service was not configured")
	}
}

func TestCloudPreviewTextIsInlineAndUnsupportedHasNoContentURL(t *testing.T) {
	_, store, testServer := newCloudPreviewTestServer(t)
	putPreviewObject(t, store, "note.txt", "text/plain", []byte("<script>alert('safe')</script>"))
	putPreviewObject(t, store, "vector.svg", "image/svg+xml", []byte("<svg><script/></svg>"))

	textPreview := requestCloudPreview(t, testServer.URL, "note.txt")
	if textPreview.Kind != cloud.PreviewText || textPreview.Text != "<script>alert('safe')</script>" {
		t.Fatalf("unexpected text preview: %+v", textPreview)
	}
	if textPreview.ContentURL != "" {
		t.Fatalf("text content URL = %q, want empty", textPreview.ContentURL)
	}

	svgPreview := requestCloudPreview(t, testServer.URL, "vector.svg")
	if svgPreview.Kind != cloud.PreviewUnsupported || svgPreview.ContentURL != "" {
		t.Fatalf("unexpected SVG preview: %+v", svgPreview)
	}
}

func TestCloudPreviewContentTicketStreamsImage(t *testing.T) {
	_, store, testServer := newCloudPreviewTestServer(t)
	body := []byte("fake-png-content")
	putPreviewObject(t, store, "image.png", "image/png", body)

	preview := requestCloudPreview(t, testServer.URL, "image.png")
	if preview.Kind != cloud.PreviewImage || preview.ContentURL == "" {
		t.Fatalf("unexpected image preview: %+v", preview)
	}
	response, err := http.Get(testServer.URL + preview.ContentURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	got, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !bytes.Equal(got, body) {
		t.Fatalf("status/body = %d/%q", response.StatusCode, got)
	}
	if response.Header.Get("Content-Type") != "image/png" || response.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected content headers: %v", response.Header)
	}
	if !strings.HasPrefix(response.Header.Get("Content-Disposition"), "inline") {
		t.Fatalf("content disposition = %q", response.Header.Get("Content-Disposition"))
	}
}

func TestCloudPreviewRejectsExpiredAndTamperedTickets(t *testing.T) {
	server, store, testServer := newCloudPreviewTestServer(t)
	putPreviewObject(t, store, "image.png", "image/png", []byte("png"))

	expires := time.Now().Add(-time.Minute).Unix()
	expired := url.Values{
		"key":       {"image.png"},
		"expires":   {strconv.FormatInt(expires, 10)},
		"signature": {server.previewSignature("image.png", expires)},
	}
	assertPreviewTicketUnauthorized(t, testServer.URL+"/api/cloud/preview/content?"+expired.Encode())

	preview := requestCloudPreview(t, testServer.URL, "image.png")
	contentURL, err := url.Parse(preview.ContentURL)
	if err != nil {
		t.Fatal(err)
	}
	query := contentURL.Query()
	query.Set("signature", "00")
	contentURL.RawQuery = query.Encode()
	assertPreviewTicketUnauthorized(t, testServer.URL+contentURL.String())
}

func newCloudPreviewTestServer(t *testing.T) (*Server, *memory.Store, *httptest.Server) {
	t.Helper()
	store := memory.New()
	server := NewServer(config.Config{APIToken: "secret"}, task.NewStore())
	server.ConfigureCloud(cloud.NewService(store, "bucket"))
	testServer := httptest.NewServer(server.httpServer.Handler)
	t.Cleanup(testServer.Close)
	return server, store, testServer
}

func putPreviewObject(t *testing.T, store *memory.Store, key, contentType string, body []byte) {
	t.Helper()
	_, err := store.PutObject(context.Background(), objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: "bucket", Key: key},
		ContentType: contentType,
		Body:        bytes.NewReader(body),
		Size:        int64(len(body)),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func requestCloudPreview(t *testing.T, baseURL, key string) cloud.Preview {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, baseURL+"/api/cloud/preview?key="+url.QueryEscape(key), nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer secret")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("status = %d: %s", response.StatusCode, data)
	}
	var preview cloud.Preview
	if err := json.NewDecoder(response.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	return preview
}

func assertPreviewTicketUnauthorized(t *testing.T, requestURL string) {
	t.Helper()
	response, err := http.Get(requestURL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusUnauthorized)
	}
}
