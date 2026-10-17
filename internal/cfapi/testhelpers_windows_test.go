//go:build windows

package cfapi

import (
	"context"
	"errors"
	"testing"
)

// 测试用共享类型别名与哨兵错误。
//
// contextContext 只在测试内部用作简写；生产代码一律写完整的 context.Context。
type contextContext = context.Context

var errFakeCloudDown = errors.New("模拟：云端不可用")

// mustList 取 ListFunc 的产物，失败即终止用例。
func mustList(t *testing.T, f ListFunc) []CloudEntry {
	t.Helper()
	e, err := f(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return e
}
