package s3store

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"easyshare/internal/cloud/objectstore"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func TestRustFSIntegration(t *testing.T) {
	if os.Getenv("EASYSHARE_RUSTFS_INTEGRATION") != "1" {
		t.Skip("set EASYSHARE_RUSTFS_INTEGRATION=1 to run against RustFS")
	}
	endpoint := requiredEnvironment(t, "EASYSHARE_RUSTFS_ENDPOINT")
	accessKey := requiredEnvironment(t, "EASYSHARE_RUSTFS_ACCESS_KEY")
	secretKey := requiredEnvironment(t, "EASYSHARE_RUSTFS_SECRET_KEY")
	bucket := requiredEnvironment(t, "EASYSHARE_RUSTFS_BUCKET")
	parsedEndpoint, err := url.Parse(endpoint)
	if err != nil {
		t.Fatalf("parse endpoint: %v", err)
	}

	store, err := New(Config{
		Endpoint:          endpoint,
		Region:            defaultRegion,
		AccessKeyID:       accessKey,
		SecretAccessKey:   secretKey,
		AllowInsecureHTTP: parsedEndpoint.Scheme == "http",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ensureBucket(t, ctx, store, bucket)

	ref := objectstore.ObjectRef{
		Bucket: bucket,
		Key:    fmt.Sprintf("easyshare-conformance/%d-%d", time.Now().UnixNano(), os.Getpid()),
	}
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = store.DeleteObject(cleanupContext, ref)
	})

	content := []byte("EasyShare RustFS multipart conformance\n")
	wantDigest := sha256.Sum256(content)
	upload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadInput{
		ObjectRef:   ref,
		ContentType: "application/octet-stream",
		Metadata:    map[string]string{"easyshare-sha256": fmt.Sprintf("%x", wantDigest)},
	})
	if err != nil {
		t.Fatalf("create multipart upload: %v", err)
	}
	uploadOpen := true
	t.Cleanup(func() {
		if !uploadOpen {
			return
		}
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		_ = store.AbortMultipartUpload(cleanupContext, objectstore.AbortMultipartUploadInput{ObjectRef: ref, UploadID: upload.UploadID})
	})

	presignedPart, err := store.PresignUploadPart(ctx, objectstore.PresignUploadPartInput{
		ObjectRef: ref, UploadID: upload.UploadID, PartNumber: 1, Expires: 5 * time.Minute,
	})
	if err != nil {
		t.Fatalf("presign upload part: %v", err)
	}
	partToken := uploadPresignedPart(t, ctx, presignedPart, content)
	_, err = store.CompleteMultipartUpload(ctx, objectstore.CompleteMultipartUploadInput{
		ObjectRef: ref,
		UploadID:  upload.UploadID,
		Parts:     []objectstore.CompletedPart{{PartNumber: 1, Token: partToken}},
	})
	if err != nil {
		t.Fatalf("complete multipart upload: %v", err)
	}
	uploadOpen = false

	info, err := store.HeadObject(ctx, ref)
	if err != nil {
		t.Fatalf("head object: %v", err)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("object size = %d, want %d", info.Size, len(content))
	}
	if got := info.Metadata["easyshare-sha256"]; got != fmt.Sprintf("%x", wantDigest) {
		t.Fatalf("SHA-256 metadata = %q", got)
	}

	presignedDownload, err := store.PresignDownload(ctx, objectstore.PresignDownloadInput{ObjectRef: ref, Expires: 5 * time.Minute})
	if err != nil {
		t.Fatalf("presign download: %v", err)
	}
	downloaded := downloadPresignedObject(t, ctx, presignedDownload)
	gotDigest := sha256.Sum256(downloaded)
	if gotDigest != wantDigest {
		t.Fatalf("download SHA-256 = %x, want %x", gotDigest, wantDigest)
	}

	if err := store.DeleteObject(ctx, ref); err != nil {
		t.Fatalf("delete object: %v", err)
	}
	if _, err := store.HeadObject(ctx, ref); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("head deleted object error = %v, want ErrNotFound", err)
	}

	abortRef := objectstore.ObjectRef{Bucket: bucket, Key: ref.Key + "-abort"}
	abortUpload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadInput{ObjectRef: abortRef})
	if err != nil {
		t.Fatalf("create abort upload: %v", err)
	}
	if err := store.AbortMultipartUpload(ctx, objectstore.AbortMultipartUploadInput{ObjectRef: abortRef, UploadID: abortUpload.UploadID}); err != nil {
		t.Fatalf("abort multipart upload: %v", err)
	}
}

func ensureBucket(t *testing.T, ctx context.Context, store *Store, bucket string) {
	t.Helper()
	client, ok := store.client.(*s3.Client)
	if !ok {
		t.Fatal("integration store does not use an AWS S3 client")
	}
	if _, err := client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(bucket)}); err == nil {
		return
	}
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Fatalf("create or access integration bucket %q: %v", bucket, err)
	}
}

func uploadPresignedPart(t *testing.T, ctx context.Context, presigned objectstore.PresignedRequest, body []byte) string {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, presigned.Method, presigned.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	applySignedHeaders(request, presigned.Headers)
	request.ContentLength = int64(len(body))
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("upload presigned part: %v", err)
	}
	defer response.Body.Close()
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		t.Fatalf("upload status = %s; body = %s", response.Status, responseBody)
	}
	token := response.Header.Get("ETag")
	if strings.TrimSpace(token) == "" {
		t.Fatal("upload response did not include ETag part token")
	}
	return token
}

func downloadPresignedObject(t *testing.T, ctx context.Context, presigned objectstore.PresignedRequest) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(ctx, presigned.Method, presigned.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	applySignedHeaders(request, presigned.Headers)
	response, err := (&http.Client{Timeout: 30 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("download presigned object: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4<<10))
		t.Fatalf("download status = %s; body = %s", response.Status, responseBody)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read downloaded object: %v", err)
	}
	return body
}

func applySignedHeaders(request *http.Request, headers http.Header) {
	for key, values := range headers {
		if strings.EqualFold(key, "Host") && len(values) > 0 {
			request.Host = values[0]
			continue
		}
		for _, value := range values {
			request.Header.Add(key, value)
		}
	}
}

func requiredEnvironment(t *testing.T, name string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatalf("%s must be set when EASYSHARE_RUSTFS_INTEGRATION=1", name)
	}
	return value
}
