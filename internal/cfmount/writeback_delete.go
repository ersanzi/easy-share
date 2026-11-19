package cfmount

import (
	"encoding/json"
	"errors"
)

// PendingForDelete只复制持久意图，不取消或释放正在上传/未提交快照。
// 调用方应在操作文件前核实此状态，不能把远端登记行当作已提交正文。
func (e *WritebackEngine) PendingForDelete(path string) (SyncBaseline, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r := e.state.Records[path]
	if r == nil {
		return SyncBaseline{Path: path}, false
	}
	return SyncBaseline{Path: path, FileID: r.FileID, Version: r.Version, Size: r.Size, Directory: r.Directory, Pending: r.Pending != nil}, true
}

// ProtectDeletion阻止普通新增扫描将删除/还原未决文件重新上传；所有快照继续保存。
func (e *WritebackEngine) ProtectDeletion(paths []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, path := range paths {
		if !validWritebackPath(path) {
			return errors.New("删除保护路径无效")
		}
	}
	e.deletionProtected = append([]string(nil), paths...)
	return nil
}

// RetainRestoredBaseline在准确Windows恢复后保留原服务器身份及未提交快照。
// 远端已还原且正文核验由调用方先完成；缺旧记录时不伪造可信哈希。
func (e *WritebackEngine) RetainRestoredBaseline(path string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !validWritebackPath(path) {
		return errors.New("还原路径无效")
	}
	delete(e.seen, path)
	return nil
}

// DeletionStateCopy只为本轮持久安全操作留证，不暴露账号或令牌。
func (e *WritebackEngine) DeletionStateCopy(path string) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return json.Marshal(e.state.Records[path])
}

// ForgetConfirmedDeletion仅在明确服务器删除结果和本机投影移除后摘除该身份基线。
// Pending快照不可丢弃，调用方须先保护它并处理冲突。
func (e *WritebackEngine) ForgetConfirmedDeletion(paths []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, path := range paths {
		if !validWritebackPath(path) {
			return errors.New("已确认删除路径无效")
		}
		if r := e.state.Records[path]; r != nil && r.Pending != nil {
			return errors.New("删除目标有未提交内容，保留原快照")
		}
	}
	previous := make(map[string]*writebackRecord, len(paths))
	for _, path := range paths {
		previous[path] = e.state.Records[path]
		delete(e.state.Records, path)
		delete(e.seen, path)
	}
	if err := e.save(); err != nil {
		for path, r := range previous {
			if r != nil {
				e.state.Records[path] = r
			}
		}
		return err
	}
	return nil
}
