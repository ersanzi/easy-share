// Package cloud provides file-level cloud drive operations on top of objectstore.
package cloud

import (
	"context"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"easyshare/internal/cloud/objectstore"
)

const defaultShareExpiry = 24 * time.Hour

// File represents a cloud file entry for the frontend.
type File struct {
	Key          string    `json:"key"`
	Name         string    `json:"name"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"contentType"`
	LastModified time.Time `json:"lastModified"`
}

// UploadResult is returned after a successful upload.
type UploadResult struct {
	Key  string `json:"key"`
	ETag string `json:"etag"`
}

// Service wraps an objectstore.Store with file-level operations.
type Service struct {
	store  objectstore.Store
	bucket string
}

func NewService(store objectstore.Store, bucket string) *Service {
	return &Service{store: store, bucket: bucket}
}

// Upload reads a local file and uploads it to the cloud bucket.
func (s *Service) Upload(ctx context.Context, localPath string) (UploadResult, error) {
	info, err := os.Stat(localPath)
	if err != nil {
		return UploadResult{}, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return UploadResult{}, fmt.Errorf("cannot upload a directory")
	}

	key := sanitizeKey(filepath.Base(localPath))
	contentType := detectContentType(localPath)

	file, err := os.Open(localPath)
	if err != nil {
		return UploadResult{}, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	result, err := s.store.PutObject(ctx, objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: s.bucket, Key: key},
		ContentType: contentType,
		Body:        file,
		Size:        info.Size(),
	})
	if err != nil {
		return UploadResult{}, fmt.Errorf("upload %s: %w", key, err)
	}
	return UploadResult{Key: key, ETag: result.ETag}, nil
}

// UploadReader uploads from an io.Reader with a given name and size.
func (s *Service) UploadReader(ctx context.Context, name string, reader io.Reader, size int64) (UploadResult, error) {
	key := sanitizeKey(name)
	contentType := detectContentType(name)
	result, err := s.store.PutObject(ctx, objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: s.bucket, Key: key},
		ContentType: contentType,
		Body:        reader,
		Size:        size,
	})
	if err != nil {
		return UploadResult{}, fmt.Errorf("upload %s: %w", key, err)
	}
	return UploadResult{Key: key, ETag: result.ETag}, nil
}

// List returns all files in the bucket (optionally filtered by prefix).
func (s *Service) List(ctx context.Context, prefix string) ([]File, error) {
	var files []File
	token := ""
	for {
		result, err := s.store.ListObjects(ctx, objectstore.ListObjectsInput{
			Bucket:            s.bucket,
			Prefix:            prefix,
			MaxKeys:           500,
			ContinuationToken: token,
		})
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}
		for _, entry := range result.Objects {
			files = append(files, File{
				Key:          entry.Key,
				Name:         filepath.Base(entry.Key),
				Size:         entry.Size,
				ContentType:  entry.ContentType,
				LastModified: entry.LastModified,
			})
		}
		if !result.IsTruncated {
			break
		}
		token = result.ContinuationToken
	}
	return files, nil
}

// DownloadURL returns a presigned download URL for the given key.
func (s *Service) DownloadURL(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if expiry <= 0 {
		expiry = defaultShareExpiry
	}
	presigned, err := s.store.PresignDownload(ctx, objectstore.PresignDownloadInput{
		ObjectRef: objectstore.ObjectRef{Bucket: s.bucket, Key: key},
		Expires:   expiry,
	})
	if err != nil {
		return "", fmt.Errorf("presign download: %w", err)
	}
	return presigned.URL, nil
}

// Delete removes a file from the cloud bucket.
func (s *Service) Delete(ctx context.Context, key string) error {
	if err := s.store.DeleteObject(ctx, objectstore.ObjectRef{Bucket: s.bucket, Key: key}); err != nil {
		return fmt.Errorf("delete %s: %w", key, err)
	}
	return nil
}

// ShareLink generates a time-limited download link for sharing.
func (s *Service) ShareLink(ctx context.Context, key string, expiry time.Duration) (string, error) {
	return s.DownloadURL(ctx, key, expiry)
}

func sanitizeKey(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.ReplaceAll(name, "..", "")
	if name == "" || name == "/" {
		name = "unnamed"
	}
	return name
}

func detectContentType(fileName string) string {
	ext := filepath.Ext(fileName)
	if ext == "" {
		return "application/octet-stream"
	}
	contentType := mime.TypeByExtension(ext)
	if contentType == "" {
		return "application/octet-stream"
	}
	return contentType
}
