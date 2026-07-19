package memory

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"easyshare/internal/cloud/objectstore"
)

func TestMultipartLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := New()
	ref := objectstore.ObjectRef{Bucket: "bucket", Key: "objects/test"}
	metadata := map[string]string{"sha256": "application-owned-digest"}
	upload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadInput{
		ObjectRef:   ref,
		ContentType: "text/plain",
		Metadata:    metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	metadata["sha256"] = "mutated-after-call"

	presigned, err := store.PresignUploadPart(ctx, objectstore.PresignUploadPartInput{
		ObjectRef: ref, UploadID: upload.UploadID, PartNumber: 2, Expires: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if presigned.Method != "PUT" || presigned.URL == "" {
		t.Fatalf("unexpected presigned request: %+v", presigned)
	}

	part2, err := store.PutPart(upload.UploadID, 2, []byte("world"))
	if err != nil {
		t.Fatal(err)
	}
	part1, err := store.PutPart(upload.UploadID, 1, []byte("hello "))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteMultipartUpload(ctx, objectstore.CompleteMultipartUploadInput{
		ObjectRef: ref,
		UploadID:  upload.UploadID,
		Parts:     []objectstore.CompletedPart{part2, part1},
	}); err != nil {
		t.Fatal(err)
	}

	body, err := store.ReadObject(ref)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "hello world" {
		t.Fatalf("ReadObject() = %q", body)
	}
	info, err := store.HeadObject(ctx, ref)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size != int64(len(body)) || info.ContentType != "text/plain" {
		t.Fatalf("unexpected object info: %+v", info)
	}
	if !reflect.DeepEqual(info.Metadata, map[string]string{"sha256": "application-owned-digest"}) {
		t.Fatalf("metadata = %#v", info.Metadata)
	}
	if _, err := store.PresignDownload(ctx, objectstore.PresignDownloadInput{ObjectRef: ref, Expires: time.Minute}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteObject(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := store.HeadObject(ctx, ref); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("HeadObject() after delete error = %v, want ErrNotFound", err)
	}
}

func TestAbortRemovesUpload(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := New()
	ref := objectstore.ObjectRef{Bucket: "bucket", Key: "key"}
	upload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadInput{ObjectRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AbortMultipartUpload(ctx, objectstore.AbortMultipartUploadInput{ObjectRef: ref, UploadID: upload.UploadID}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PutPart(upload.UploadID, 1, []byte("late")); !errors.Is(err, objectstore.ErrNotFound) {
		t.Fatalf("PutPart() after abort error = %v, want ErrNotFound", err)
	}
}

func TestCompleteRejectsWrongPartToken(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	store := New()
	ref := objectstore.ObjectRef{Bucket: "bucket", Key: "key"}
	upload, err := store.CreateMultipartUpload(ctx, objectstore.CreateMultipartUploadInput{ObjectRef: ref})
	if err != nil {
		t.Fatal(err)
	}
	part, err := store.PutPart(upload.UploadID, 1, []byte("body"))
	if err != nil {
		t.Fatal(err)
	}
	part.Token = "wrong"
	_, err = store.CompleteMultipartUpload(ctx, objectstore.CompleteMultipartUploadInput{
		ObjectRef: ref, UploadID: upload.UploadID, Parts: []objectstore.CompletedPart{part},
	})
	if !errors.Is(err, objectstore.ErrPreconditionFailed) {
		t.Fatalf("CompleteMultipartUpload() error = %v, want ErrPreconditionFailed", err)
	}
}
