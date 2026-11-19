package drive

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
)

// PUT/Complete 的成功不能替代 HEAD 核验；启用后缺失/虚假成功必须向上传者报错。
func TestUploadFileCommitVerification(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		enabled, multipart, dedup, rejected bool
		reply                               string
		wantErr                             bool
	}{
		{name: "legacy-default-off"},
		{name: "legacy-multipart-off", multipart: true},
		{name: "small-verified", enabled: true},
		{name: "small-business-rejection", enabled: true, reply: `{"code":500,"msg":"object missing"}`, wantErr: true},
		{name: "small-false-success", enabled: true, reply: `{"code":200,"data":{"fileId":"71","version":"2","size":3,"verified":false}}`, wantErr: true},
		{name: "small-missing-proof", enabled: true, reply: `{"code":200}`, wantErr: true},
		{name: "small-size-mismatch", enabled: true, reply: `{"code":200,"data":{"fileId":71,"version":2,"size":4,"verified":true}}`, wantErr: true},
		{name: "small-presign-rejected", enabled: true, rejected: true, wantErr: true},
		{name: "dedup-still-verified", enabled: true, dedup: true},
		{name: "dedup-false-success", enabled: true, dedup: true, reply: `{"code":200,"data":{}}`, wantErr: true},
		{name: "multipart-verified", enabled: true, multipart: true},
		{name: "multipart-false-success", enabled: true, multipart: true, reply: `{"code":200,"data":{}}`, wantErr: true},
		{name: "multipart-commit-rejected", enabled: true, multipart: true, reply: `{"code":500,"msg":"HEAD failed"}`, wantErr: true},
		{name: "multipart-complete-rejected", enabled: true, multipart: true, rejected: true, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			content := []byte("abc")
			if tc.multipart {
				content = make([]byte, multipartThreshold)
			}
			size := int64(len(content))
			sum := md5.Sum(content)
			hash := hex.EncodeToString(sum[:])
			localPath, _ := writeFile(t, content)
			var puts, completed, commits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/easyshare/drive/presign-put":
					if tc.rejected {
						io.WriteString(w, `{"code":500,"msg":"presign denied"}`)
					} else if tc.dedup {
						io.WriteString(w, `{"code":200,"data":{"deduped":true}}`)
					} else {
						fmt.Fprintf(w, `{"code":200,"data":{"url":"http://%s/object"}}`, r.Host)
					}
				case "/easyshare/drive/upload-session/create":
					fmt.Fprintf(w, `{"code":200,"data":{"sessionId":"9","uploadId":"upload-1","partSize":%d}}`, size)
				case "/easyshare/drive/upload-session/part":
					fmt.Fprintf(w, `{"code":200,"data":{"url":"http://%s/object"}}`, r.Host)
				case "/object":
					n, err := io.Copy(io.Discard, r.Body)
					if err != nil || n != size || r.Method != http.MethodPut {
						t.Errorf("PUT: bytes=%d method=%s error=%v", n, r.Method, err)
					}
					puts.Add(1)
					w.Header().Set("ETag", `"`+hash+`"`)
				case "/easyshare/drive/upload-session/complete":
					if tc.rejected {
						io.WriteString(w, `{"code":500,"msg":"complete denied"}`)
					} else {
						completed.Add(1)
						io.WriteString(w, `{"code":200,"data":{"fileId":"71"}}`)
					}
				case "/easyshare/drive/upload-commit":
					commits.Add(1)
					if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer tok" || r.Header.Get("clientid") != pcClientID {
						t.Error("commit lost method/login headers")
					}
					var body struct {
						Path, Space, ContentMd5 string
						Size                    int64
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Path != "a.txt" || body.Space != SpacePersonal || body.Size != size || body.ContentMd5 != hash {
						t.Errorf("commit body=%+v error=%v", body, err)
					}
					if (!tc.dedup && puts.Load() != 1) || (tc.multipart && completed.Load() != 1) {
						t.Error("commit must follow successful PUT/Complete")
					}
					if tc.reply != "" {
						io.WriteString(w, tc.reply)
					} else {
						fmt.Fprintf(w, `{"code":200,"data":{"fileId":"2110952103974817794","version":"2","size":%d,"verified":true}}`, size)
					}
				default:
					t.Errorf("unexpected request (no automatic capabilities GET): %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
				}
			}))
			defer server.Close()
			client := New(server.URL)
			if client.VerifyUploadCommit {
				t.Fatal("verification must default to false")
			}
			client.VerifyUploadCommit = tc.enabled
			if tc.multipart {
				client.Sessions = NewSessionStore(filepath.Join(t.TempDir(), "sessions"))
			}
			err := client.UploadFileIfVersion(context.Background(), "tok", "", "a.txt", localPath, nil, nil)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v wantErr=%t", err, tc.wantErr)
			}
			var wantCommits int32
			if tc.enabled && !tc.rejected {
				wantCommits = 1
			}
			if commits.Load() != wantCommits {
				t.Errorf("commit calls=%d want=%d", commits.Load(), wantCommits)
			}
		})
	}
}

func TestCommitUploadNumbersStringsAndEmptyFile(t *testing.T) {
	for _, reply := range []string{
		`{"code":200,"data":{"fileId":71,"version":2,"size":0,"verified":true}}`,
		`{"code":200,"data":{"fileId":"2110952103974817794","version":"2","size":"0","verified":true}}`,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, reply)
		}))
		result, err := New(server.URL).CommitUpload(context.Background(), "tok", "", "empty.txt", 0,
			"d41d8cd98f00b204e9800998ecf8427e")
		server.Close()
		if err != nil || result == nil || result.Size.Int64() != 0 || result.Version.Int64() != 2 {
			t.Fatalf("result=%+v error=%v", result, err)
		}
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"code":200,"data":{"fileId":71,"version":2,"verified":true}}`)
	}))
	defer server.Close()
	if _, err := New(server.URL).CommitUpload(context.Background(), "tok", "", "empty.txt", 0,
		"d41d8cd98f00b204e9800998ecf8427e"); err == nil {
		t.Fatal("空文件也不能接受缺失真实 size 的虚假成功")
	}
}
