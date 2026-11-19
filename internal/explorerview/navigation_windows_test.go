//go:build windows

package explorerview

import "testing"

// Shell在Navigate期间可以成功返回空视图；不能把空接口传入vtable调用。
func TestExplorerNavigationWithoutViewReturnsPending(t *testing.T) {
	if _, err := query(nil, iidFolderView); err == nil {
		t.Fatal("未发布视图应等待后续导航采样")
	}
	if err := applyDetails(nil); err == nil {
		t.Fatal("无视图时不能宣称默认列已应用")
	}
}
