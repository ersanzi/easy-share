package drive

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// CreateDirectory 创建持久化远端目录。重复创建同名目录幂等，同名文件报冲突。
// 不向对象存储写 marker，不在本地缓存目录存在性；旧服务端不支持时直接返回错误。
func (c *Client) CreateDirectory(ctx context.Context, token, space, path string) (*Object, error) {
	body, err := json.Marshal(map[string]string{"space": normalizeSpace(space), "path": path})
	if err != nil {
		return nil, err
	}
	var object Object
	if err := c.call(ctx, http.MethodPost, "/easyshare/drive/directories", token, body, &object); err != nil {
		return nil, fmt.Errorf("创建远端目录失败：%w", err)
	}
	if !object.Directory || object.Path == "" || object.FileId <= 0 {
		return nil, fmt.Errorf("创建远端目录失败：服务端未返回持久化目录身份")
	}
	return &object, nil
}

// Directories 通过统一对象列表列举远端持久化目录，不含仅由文件路径推断的隐式目录。
func (c *Client) Directories(ctx context.Context, token, space string) ([]Object, error) {
	objects, err := c.Objects(ctx, token, space)
	if err != nil {
		return nil, err
	}
	dirs := make([]Object, 0)
	for _, object := range objects {
		if object.Directory {
			dirs = append(dirs, object)
		}
	}
	return dirs, nil
}
