// Package memory provides an in-memory ObjectStore fake for state-machine unit tests.
package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"easyshare/internal/cloud/objectstore"
)

type Store struct {
	mutex      sync.RWMutex
	nextUpload uint64
	uploads    map[string]*upload
	objects    map[string]storedObject
	now        func() time.Time
}

type upload struct {
	ref         objectstore.ObjectRef
	contentType string
	metadata    map[string]string
	parts       map[int32]storedPart
}

type storedPart struct {
	token string
	body  []byte
}

type storedObject struct {
	body         []byte
	etag         string
	contentType  string
	metadata     map[string]string
	lastModified time.Time
}

func New() *Store {
	return &Store{
		uploads: make(map[string]*upload),
		objects: make(map[string]storedObject),
		now:     time.Now,
	}
}

func (store *Store) CreateMultipartUpload(_ context.Context, input objectstore.CreateMultipartUploadInput) (objectstore.MultipartUpload, error) {
	if err := input.Validate(); err != nil {
		return objectstore.MultipartUpload{}, err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	store.nextUpload++
	uploadID := "memory-upload-" + strconv.FormatUint(store.nextUpload, 10)
	store.uploads[uploadID] = &upload{
		ref:         input.ObjectRef,
		contentType: input.ContentType,
		metadata:    objectstore.CloneMetadata(input.Metadata),
		parts:       make(map[int32]storedPart),
	}
	return objectstore.MultipartUpload{UploadID: uploadID}, nil
}

func (store *Store) PresignUploadPart(_ context.Context, input objectstore.PresignUploadPartInput) (objectstore.PresignedRequest, error) {
	if err := input.Validate(); err != nil {
		return objectstore.PresignedRequest{}, err
	}
	store.mutex.RLock()
	upload, found := store.uploads[input.UploadID]
	store.mutex.RUnlock()
	if !found || upload.ref != input.ObjectRef {
		return objectstore.PresignedRequest{}, notFound("presign upload part")
	}
	return objectstore.PresignedRequest{
		Method:    http.MethodPut,
		URL:       memoryURL(input.ObjectRef, input.UploadID, input.PartNumber),
		Headers:   make(http.Header),
		ExpiresAt: store.now().Add(input.Expires),
	}, nil
}

// PutPart simulates the HTTP PUT performed against a presigned URL. It is a
// test helper and intentionally is not part of objectstore.Store.
func (store *Store) PutPart(uploadID string, partNumber int32, body []byte) (objectstore.CompletedPart, error) {
	if partNumber < objectstore.MinPartNumber || partNumber > objectstore.MaxPartNumber {
		return objectstore.CompletedPart{}, fmt.Errorf("%w: invalid part number", objectstore.ErrInvalidInput)
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	upload, found := store.uploads[uploadID]
	if !found {
		return objectstore.CompletedPart{}, notFound("put part")
	}
	digest := sha256.Sum256(body)
	token := hex.EncodeToString(digest[:])
	upload.parts[partNumber] = storedPart{token: token, body: append([]byte(nil), body...)}
	return objectstore.CompletedPart{PartNumber: partNumber, Token: token}, nil
}

func (store *Store) CompleteMultipartUpload(_ context.Context, input objectstore.CompleteMultipartUploadInput) (objectstore.CompleteResult, error) {
	if err := input.ObjectRef.Validate(); err != nil {
		return objectstore.CompleteResult{}, err
	}
	parts, err := objectstore.NormalizeCompletedParts(input.Parts)
	if err != nil {
		return objectstore.CompleteResult{}, err
	}
	if input.UploadID == "" {
		return objectstore.CompleteResult{}, fmt.Errorf("%w: upload ID must not be empty", objectstore.ErrInvalidInput)
	}

	store.mutex.Lock()
	defer store.mutex.Unlock()
	upload, found := store.uploads[input.UploadID]
	if !found || upload.ref != input.ObjectRef {
		return objectstore.CompleteResult{}, notFound("complete multipart upload")
	}
	body := make([]byte, 0)
	for _, completed := range parts {
		part, exists := upload.parts[completed.PartNumber]
		if !exists || part.token != completed.Token {
			return objectstore.CompleteResult{}, fmt.Errorf("%w: part %d does not match", objectstore.ErrPreconditionFailed, completed.PartNumber)
		}
		body = append(body, part.body...)
	}
	digest := sha256.Sum256(body)
	etag := hex.EncodeToString(digest[:])
	store.objects[objectKey(input.ObjectRef)] = storedObject{
		body:         body,
		etag:         etag,
		contentType:  upload.contentType,
		metadata:     objectstore.CloneMetadata(upload.metadata),
		lastModified: store.now(),
	}
	delete(store.uploads, input.UploadID)
	return objectstore.CompleteResult{ETag: etag}, nil
}

func (store *Store) AbortMultipartUpload(_ context.Context, input objectstore.AbortMultipartUploadInput) error {
	if err := input.Validate(); err != nil {
		return err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	upload, found := store.uploads[input.UploadID]
	if !found || upload.ref != input.ObjectRef {
		return notFound("abort multipart upload")
	}
	delete(store.uploads, input.UploadID)
	return nil
}

func (store *Store) HeadObject(_ context.Context, ref objectstore.ObjectRef) (objectstore.ObjectInfo, error) {
	if err := ref.Validate(); err != nil {
		return objectstore.ObjectInfo{}, err
	}
	store.mutex.RLock()
	value, found := store.objects[objectKey(ref)]
	store.mutex.RUnlock()
	if !found {
		return objectstore.ObjectInfo{}, notFound("head object")
	}
	return objectstore.ObjectInfo{
		ObjectRef:    ref,
		Size:         int64(len(value.body)),
		ETag:         value.etag,
		ContentType:  value.contentType,
		Metadata:     objectstore.CloneMetadata(value.metadata),
		LastModified: value.lastModified,
	}, nil
}

func (store *Store) PresignDownload(_ context.Context, input objectstore.PresignDownloadInput) (objectstore.PresignedRequest, error) {
	if err := input.Validate(); err != nil {
		return objectstore.PresignedRequest{}, err
	}
	if _, err := store.HeadObject(context.Background(), input.ObjectRef); err != nil {
		return objectstore.PresignedRequest{}, err
	}
	return objectstore.PresignedRequest{
		Method:    http.MethodGet,
		URL:       memoryURL(input.ObjectRef, "", 0),
		Headers:   make(http.Header),
		ExpiresAt: store.now().Add(input.Expires),
	}, nil
}

func (store *Store) DeleteObject(_ context.Context, ref objectstore.ObjectRef) error {
	if err := ref.Validate(); err != nil {
		return err
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	delete(store.objects, objectKey(ref))
	return nil
}

// ReadObject returns a copy of object bytes for tests. It is not part of
// objectstore.Store because production downloads use presigned URLs.
func (store *Store) ReadObject(ref objectstore.ObjectRef) ([]byte, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	store.mutex.RLock()
	value, found := store.objects[objectKey(ref)]
	store.mutex.RUnlock()
	if !found {
		return nil, notFound("read object")
	}
	return append([]byte(nil), value.body...), nil
}

func objectKey(ref objectstore.ObjectRef) string {
	return ref.Bucket + "\x00" + ref.Key
}

func memoryURL(ref objectstore.ObjectRef, uploadID string, partNumber int32) string {
	query := url.Values{}
	if uploadID != "" {
		query.Set("uploadId", uploadID)
		query.Set("partNumber", strconv.FormatInt(int64(partNumber), 10))
	}
	return (&url.URL{Scheme: "memory", Host: ref.Bucket, Path: "/" + ref.Key, RawQuery: query.Encode()}).String()
}

func notFound(operation string) error {
	return &objectstore.ProviderError{
		Operation: operation,
		Kind:      objectstore.ErrNotFound,
		Err:       objectstore.ErrNotFound,
	}
}

var _ objectstore.Store = (*Store)(nil)
