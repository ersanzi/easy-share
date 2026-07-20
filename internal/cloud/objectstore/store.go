// Package objectstore defines EasyShare's provider-neutral object storage boundary.
package objectstore

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	MinPartNumber = 1
	MaxPartNumber = 10_000
	MaxPresignTTL = 7 * 24 * time.Hour
)

// Store contains only the S3 data-plane operations needed by EasyShare's
// multipart upload and download flows.
type Store interface {
	PutObject(context.Context, PutObjectInput) (CompleteResult, error)
	GetObject(context.Context, GetObjectInput) (GetObjectOutput, error)
	CreateMultipartUpload(context.Context, CreateMultipartUploadInput) (MultipartUpload, error)
	PresignUploadPart(context.Context, PresignUploadPartInput) (PresignedRequest, error)
	CompleteMultipartUpload(context.Context, CompleteMultipartUploadInput) (CompleteResult, error)
	AbortMultipartUpload(context.Context, AbortMultipartUploadInput) error
	HeadObject(context.Context, ObjectRef) (ObjectInfo, error)
	ListObjects(context.Context, ListObjectsInput) (ListObjectsResult, error)
	PresignDownload(context.Context, PresignDownloadInput) (PresignedRequest, error)
	DeleteObject(context.Context, ObjectRef) error
}

// GetObjectInput requests the content of an object.
type GetObjectInput struct {
	ObjectRef
}

// GetObjectOutput carries the object body and metadata.
type GetObjectOutput struct {
	Body         io.ReadCloser
	Size         int64
	ContentType  string
	ETag         string
	LastModified time.Time
}

type ObjectRef struct {
	Bucket string
	Key    string
}

func (value ObjectRef) Validate() error {
	if strings.TrimSpace(value.Bucket) == "" {
		return invalidInput("bucket must not be empty")
	}
	if strings.TrimSpace(value.Key) == "" {
		return invalidInput("object key must not be empty")
	}
	return nil
}

// PutObjectInput is a simple single-request upload for small files.
type PutObjectInput struct {
	ObjectRef
	ContentType string
	Metadata    map[string]string
	Body        io.Reader
	Size        int64
}

func (value PutObjectInput) Validate() error {
	if err := value.ObjectRef.Validate(); err != nil {
		return err
	}
	if value.Size < 0 {
		return invalidInput("size must not be negative")
	}
	return nil
}

// ListObjectsInput requests a page of objects under a prefix.
type ListObjectsInput struct {
	Bucket            string
	Prefix            string
	MaxKeys           int32
	ContinuationToken string
}

func (value ListObjectsInput) Validate() error {
	if strings.TrimSpace(value.Bucket) == "" {
		return invalidInput("bucket must not be empty")
	}
	return nil
}

// ObjectEntry is a lightweight listing entry.
type ObjectEntry struct {
	Key          string
	Size         int64
	ETag         string
	LastModified time.Time
	ContentType  string
}

// ListObjectsResult is a page of listing results.
type ListObjectsResult struct {
	Objects               []ObjectEntry
	ContinuationToken     string
	IsTruncated           bool
}

type CreateMultipartUploadInput struct {
	ObjectRef
	ContentType string
	Metadata    map[string]string
}

func (value CreateMultipartUploadInput) Validate() error {
	return value.ObjectRef.Validate()
}

type MultipartUpload struct {
	UploadID string
}

type PresignUploadPartInput struct {
	ObjectRef
	UploadID   string
	PartNumber int32
	Expires    time.Duration
}

func (value PresignUploadPartInput) Validate() error {
	if err := value.ObjectRef.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(value.UploadID) == "" {
		return invalidInput("upload ID must not be empty")
	}
	if value.PartNumber < MinPartNumber || value.PartNumber > MaxPartNumber {
		return invalidInput("part number must be between %d and %d", MinPartNumber, MaxPartNumber)
	}
	return validatePresignTTL(value.Expires)
}

type CompletedPart struct {
	PartNumber int32
	// Token is the opaque provider part token returned after uploading a part.
	// For S3-compatible stores this is normally the part ETag, not a content hash.
	Token string
}

type CompleteMultipartUploadInput struct {
	ObjectRef
	UploadID string
	Parts    []CompletedPart
}

func (value CompleteMultipartUploadInput) Validate() error {
	if err := value.ObjectRef.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(value.UploadID) == "" {
		return invalidInput("upload ID must not be empty")
	}
	_, err := NormalizeCompletedParts(value.Parts)
	return err
}

// NormalizeCompletedParts returns a sorted copy and rejects invalid or
// duplicate part numbers. Callers may provide completion results in any order.
func NormalizeCompletedParts(parts []CompletedPart) ([]CompletedPart, error) {
	if len(parts) == 0 {
		return nil, invalidInput("at least one completed part is required")
	}
	normalized := append([]CompletedPart(nil), parts...)
	sort.Slice(normalized, func(left, right int) bool {
		return normalized[left].PartNumber < normalized[right].PartNumber
	})
	for index, part := range normalized {
		if part.PartNumber < MinPartNumber || part.PartNumber > MaxPartNumber {
			return nil, invalidInput("part number must be between %d and %d", MinPartNumber, MaxPartNumber)
		}
		if strings.TrimSpace(part.Token) == "" {
			return nil, invalidInput("part %d token must not be empty", part.PartNumber)
		}
		if index > 0 && normalized[index-1].PartNumber == part.PartNumber {
			return nil, invalidInput("part number %d is duplicated", part.PartNumber)
		}
	}
	return normalized, nil
}

type CompleteResult struct {
	// ETag is provider metadata and must not be used as EasyShare's content hash.
	ETag      string
	VersionID string
}

type AbortMultipartUploadInput struct {
	ObjectRef
	UploadID string
}

func (value AbortMultipartUploadInput) Validate() error {
	if err := value.ObjectRef.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(value.UploadID) == "" {
		return invalidInput("upload ID must not be empty")
	}
	return nil
}

type ObjectInfo struct {
	ObjectRef
	Size         int64
	ETag         string
	VersionID    string
	ContentType  string
	Metadata     map[string]string
	LastModified time.Time
}

type PresignDownloadInput struct {
	ObjectRef
	Expires time.Duration
}

func (value PresignDownloadInput) Validate() error {
	if err := value.ObjectRef.Validate(); err != nil {
		return err
	}
	return validatePresignTTL(value.Expires)
}

type PresignedRequest struct {
	Method    string
	URL       string
	Headers   http.Header
	ExpiresAt time.Time
}

func validatePresignTTL(value time.Duration) error {
	if value <= 0 || value > MaxPresignTTL {
		return invalidInput("presign expiry must be greater than zero and no more than %s", MaxPresignTTL)
	}
	return nil
}

func invalidInput(format string, values ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidInput, fmt.Sprintf(format, values...))
}

func CloneMetadata(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
