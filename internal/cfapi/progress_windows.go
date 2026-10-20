//go:build windows

package cfapi

import "fmt"

// 按需取数的进度回报。
//
// 原型取自本机 Windows SDK 头
// `Windows Kits\10\Include\10.0.26100.0\um\cfapi.h` 第 1143–1149 行：
//
//	STDAPI CfReportProviderProgress(
//	    CF_CONNECTION_KEY ConnectionKey,
//	    CF_TRANSFER_KEY   TransferKey,
//	    LARGE_INTEGER     ProviderProgressTotal,
//	    LARGE_INTEGER     ProviderProgressCompleted);
//
// ⚠️ 参数顺序是**先总量后已完成**，写反了不会报错——进度条只会显示成
// 「已完成比总量还大」而被内核丢弃，症状是进度框一直 0%。
//
// 为什么需要它：本包的取数是**同步回调**，一个大文件从云端拉多久，
// 回调线程就占多久。期间用户看到的是一个既不报错也不动的窗口。
// WPS 把「点开才下载」做成可感知进度，靠的就是这条接口（取证 §6）。

// reportProviderProgress 把「总量 total / 已完成 completed」推给内核。
//
// 失败不影响取数本身（进度是锦上添花），调用方按「记一条日志继续」处理，
// 不能因为进度回报失败就把用户的读取打断。
func reportProviderProgress(conn connectionKey, transfer, total, completed int64) error {
	p, err := proc("CfReportProviderProgress")
	if err != nil {
		return err
	}
	r1, _, _ := p.Call(
		uintptr(conn),
		uintptr(transfer),
		uintptr(total),
		uintptr(completed),
	)
	if r1 != 0 {
		return fmt.Errorf("cfapi: CfReportProviderProgress 返回 0x%08X", uint32(r1))
	}
	return nil
}
