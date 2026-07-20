// Package webdavfs implements a webdav.FileSystem backed by an objectstore.Store,
// allowing Windows Explorer (via WebDAV) to browse and manage cloud files.
package webdavfs

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"easyshare/internal/cloud/objectstore"

	"golang.org/x/net/webdav"
)

// FS implements webdav.FileSystem on top of an S3-compatible object store.
type FS struct {
	store  objectstore.Store
	bucket string
}

func New(store objectstore.Store, bucket string) *FS {
	return &FS{store: store, bucket: bucket}
}

// --- webdav.FileSystem ---

func (f *FS) Mkdir(ctx context.Context, name string, _ os.FileMode) error {
	key := toKey(name)
	if key == "" {
		return nil // root always exists
	}
	// S3 "directories" are zero-byte markers with a trailing slash.
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	_, err := f.store.PutObject(ctx, objectstore.PutObjectInput{
		ObjectRef: objectstore.ObjectRef{Bucket: f.bucket, Key: key},
		Body:      strings.NewReader(""),
		Size:      0,
	})
	return err
}

func (f *FS) OpenFile(ctx context.Context, name string, flag int, _ os.FileMode) (webdav.File, error) {
	key := toKey(name)

	// Writing mode: buffer in memory, flush on Close.
	if flag&(os.O_CREATE|os.O_WRONLY|os.O_RDWR|os.O_TRUNC|os.O_APPEND) != 0 {
		return &memFile{
			fs:     f,
			ctx:    ctx,
			key:    key,
			name:   path.Base(name),
			buffer: &bytes.Buffer{},
			write:  true,
		}, nil
	}

	// Directory open (for Readdir).
	if key == "" || strings.HasSuffix(name, "/") {
		return &memFile{
			fs:   f,
			ctx:  ctx,
			key:  strings.TrimSuffix(key, "/"),
			name: path.Base(strings.TrimSuffix(name, "/")),
			dir:  true,
		}, nil
	}

	// Check if it's a directory prefix (objects exist under key/).
	if f.isDir(ctx, key) {
		return &memFile{
			fs:   f,
			ctx:  ctx,
			key:  key,
			name: path.Base(name),
			dir:  true,
		}, nil
	}

	// Read file content.
	output, err := f.store.GetObject(ctx, objectstore.GetObjectInput{
		ObjectRef: objectstore.ObjectRef{Bucket: f.bucket, Key: key},
	})
	if err != nil {
		return nil, os.ErrNotExist
	}
	data, err := io.ReadAll(output.Body)
	_ = output.Body.Close()
	if err != nil {
		return nil, err
	}

	return &memFile{
		fs:   f,
		ctx:  ctx,
		key:  key,
		name: path.Base(name),
		data: bytes.NewReader(data),
		info: &fileInfo{
			name:    path.Base(name),
			size:    int64(len(data)),
			modTime: output.LastModified,
			isDir:   false,
		},
	}, nil
}

func (f *FS) RemoveAll(ctx context.Context, name string) error {
	key := toKey(name)
	if key == "" {
		return nil // cannot remove root
	}

	// Try deleting as a single object first.
	_ = f.store.DeleteObject(ctx, objectstore.ObjectRef{Bucket: f.bucket, Key: key})

	// Also delete all objects under the prefix (directory contents).
	prefix := key
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	token := ""
	for {
		result, err := f.store.ListObjects(ctx, objectstore.ListObjectsInput{
			Bucket:            f.bucket,
			Prefix:            prefix,
			MaxKeys:           500,
			ContinuationToken: token,
		})
		if err != nil {
			return err
		}
		for _, obj := range result.Objects {
			_ = f.store.DeleteObject(ctx, objectstore.ObjectRef{Bucket: f.bucket, Key: obj.Key})
		}
		if !result.IsTruncated {
			break
		}
		token = result.ContinuationToken
	}
	return nil
}

func (f *FS) Rename(ctx context.Context, oldName, newName string) error {
	oldKey := toKey(oldName)
	newKey := toKey(newName)
	if oldKey == "" || newKey == "" {
		return os.ErrPermission
	}

	// S3 has no native rename; copy then delete.
	output, err := f.store.GetObject(ctx, objectstore.GetObjectInput{
		ObjectRef: objectstore.ObjectRef{Bucket: f.bucket, Key: oldKey},
	})
	if err != nil {
		return os.ErrNotExist
	}
	data, err := io.ReadAll(output.Body)
	_ = output.Body.Close()
	if err != nil {
		return err
	}

	_, err = f.store.PutObject(ctx, objectstore.PutObjectInput{
		ObjectRef:   objectstore.ObjectRef{Bucket: f.bucket, Key: newKey},
		ContentType: output.ContentType,
		Body:        bytes.NewReader(data),
		Size:        int64(len(data)),
	})
	if err != nil {
		return err
	}
	return f.store.DeleteObject(ctx, objectstore.ObjectRef{Bucket: f.bucket, Key: oldKey})
}

func (f *FS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	key := toKey(name)

	// Root directory.
	if key == "" {
		return &fileInfo{name: "EasyShare", isDir: true, modTime: time.Now()}, nil
	}

	// Try as a file object.
	info, err := f.store.HeadObject(ctx, objectstore.ObjectRef{Bucket: f.bucket, Key: key})
	if err == nil {
		return &fileInfo{
			name:    path.Base(key),
			size:    info.Size,
			modTime: info.LastModified,
			isDir:   false,
		}, nil
	}

	// Try as a directory (prefix with objects underneath).
	if f.isDir(ctx, key) {
		return &fileInfo{
			name:    path.Base(key),
			isDir:   true,
			modTime: time.Now(),
		}, nil
	}

	return nil, os.ErrNotExist
}

// isDir checks whether any objects exist under the given prefix.
func (f *FS) isDir(ctx context.Context, key string) bool {
	prefix := key
	if !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	result, err := f.store.ListObjects(ctx, objectstore.ListObjectsInput{
		Bucket:  f.bucket,
		Prefix:  prefix,
		MaxKeys: 1,
	})
	return err == nil && len(result.Objects) > 0
}

// listChildren returns the immediate children (files and subdirectories) of a prefix.
func (f *FS) listChildren(ctx context.Context, prefix string) ([]os.FileInfo, error) {
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}

	var entries []os.FileInfo
	seen := make(map[string]bool)
	token := ""

	for {
		result, err := f.store.ListObjects(ctx, objectstore.ListObjectsInput{
			Bucket:            f.bucket,
			Prefix:            prefix,
			MaxKeys:           500,
			ContinuationToken: token,
		})
		if err != nil {
			return nil, err
		}

		for _, obj := range result.Objects {
			remainder := strings.TrimPrefix(obj.Key, prefix)
			if remainder == "" {
				continue // the prefix marker itself
			}

			// If remainder contains "/", it's inside a subdirectory.
			if idx := strings.Index(remainder, "/"); idx >= 0 {
				dirName := remainder[:idx]
				if !seen[dirName] {
					seen[dirName] = true
					entries = append(entries, &fileInfo{
						name:    dirName,
						isDir:   true,
						modTime: obj.LastModified,
					})
				}
			} else {
				// Direct file child.
				if !seen[remainder] {
					seen[remainder] = true
					entries = append(entries, &fileInfo{
						name:    remainder,
						size:    obj.Size,
						modTime: obj.LastModified,
						isDir:   false,
					})
				}
			}
		}

		if !result.IsTruncated {
			break
		}
		token = result.ContinuationToken
	}

	return entries, nil
}

// --- webdav.File implementation ---

type memFile struct {
	fs     *FS
	ctx    context.Context
	key    string
	name   string
	dir    bool
	write  bool
	data   *bytes.Reader
	buffer *bytes.Buffer
	info   *fileInfo
	closed bool
}

func (m *memFile) Read(p []byte) (int, error) {
	if m.dir {
		return 0, os.ErrInvalid
	}
	if m.data == nil {
		return 0, io.EOF
	}
	return m.data.Read(p)
}

func (m *memFile) Seek(offset int64, whence int) (int64, error) {
	if m.data == nil {
		return 0, nil
	}
	return m.data.Seek(offset, whence)
}

func (m *memFile) Write(p []byte) (int, error) {
	if m.buffer == nil {
		m.buffer = &bytes.Buffer{}
	}
	return m.buffer.Write(p)
}

func (m *memFile) Close() error {
	if m.closed {
		return nil
	}
	m.closed = true

	// Flush buffered writes to S3.
	if m.write && m.buffer != nil && m.key != "" {
		data := m.buffer.Bytes()
		_, err := m.fs.store.PutObject(m.ctx, objectstore.PutObjectInput{
			ObjectRef: objectstore.ObjectRef{Bucket: m.fs.bucket, Key: m.key},
			Body:      bytes.NewReader(data),
			Size:      int64(len(data)),
		})
		return err
	}
	return nil
}

func (m *memFile) Readdir(count int) ([]os.FileInfo, error) {
	if !m.dir {
		return nil, os.ErrInvalid
	}
	entries, err := m.fs.listChildren(m.ctx, m.key)
	if err != nil {
		return nil, err
	}
	if count <= 0 {
		return entries, nil
	}
	if len(entries) > count {
		return entries[:count], nil
	}
	return entries, nil
}

func (m *memFile) Stat() (os.FileInfo, error) {
	if m.info != nil {
		return m.info, nil
	}
	if m.dir {
		return &fileInfo{name: m.name, isDir: true, modTime: time.Now()}, nil
	}
	return &fileInfo{name: m.name, size: 0, modTime: time.Now()}, nil
}

// --- os.FileInfo implementation ---

type fileInfo struct {
	name    string
	size    int64
	modTime time.Time
	isDir   bool
}

func (fi *fileInfo) Name() string      { return fi.name }
func (fi *fileInfo) Size() int64        { return fi.size }
func (fi *fileInfo) Mode() os.FileMode {
	if fi.isDir {
		return os.ModeDir | 0o755
	}
	return 0o644
}
func (fi *fileInfo) ModTime() time.Time { return fi.modTime }
func (fi *fileInfo) IsDir() bool        { return fi.isDir }
func (fi *fileInfo) Sys() any           { return nil }

// --- helpers ---

// toKey converts a WebDAV path (e.g. "/photos/cat.jpg") to an S3 key ("photos/cat.jpg").
func toKey(name string) string {
	name = strings.TrimPrefix(name, "/")
	name = strings.TrimSuffix(name, "/")
	name = path.Clean(name)
	if name == "." {
		return ""
	}
	return name
}

// Compile-time interface checks.
var (
	_ webdav.FileSystem = (*FS)(nil)
	_ webdav.File       = (*memFile)(nil)
	_ fs.FileInfo       = (*fileInfo)(nil)
)
