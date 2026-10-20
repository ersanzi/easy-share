package drive

// 增量合并是**消费方共有**的语义：挂载盘（internal/spacedav）与按需占位符
// （internal/cfmount）都从 `/easyshare/drive/changes` 拿同一套生命周期事件，
// 各自维护一份完整视图。合并规则只要有两份实现就会分叉——分叉的后果是
// 「同一个空间在两个入口里内容不一样」，所以放这里，只有一份。
//
// 事件语义与控制面 DriveChangeLogService 的写入点一一对应：
// created / updated / restored / deleted / moved / purged。

// ApplyChanges 把一串生命周期事件依次应用到给定清单，返回**新的**切片。
//
// 为什么不原地改传入的 slice：调用方可能把旧视图同时交给了别的 goroutine
// （挂载盘的缓存就是这样——列举结果会喂给 WebDAV 的 Stat/ReadDir 与占位符重建）。
// 复制一次是这里的契约，调用方不必自己先 copy。
//
// 事件按 spaceVersion 升序到达，重复应用同一条流得到相同结果（幂等）。
func ApplyChanges(objects []Object, changes []Change) []Object {
	next := make([]Object, len(objects))
	copy(next, objects)
	for _, change := range changes {
		next = applyChange(next, change)
	}
	return next
}

// applyChange 把一条事件应用到清单。
func applyChange(objects []Object, change Change) []Object {
	switch change.Event {
	case ChangeCreated, ChangeUpdated, ChangeRestored:
		// 事件流只带身份不带大小/时间；这里不能用零值对象充当完整视图——
		// 否则 Stat 会返回 0 字节（真实缺陷：用户看到文件“变小”）。
		// 正确做法：先保留既有对象的元数据；没有元数据时**从全量视图查询**。
		for i := range objects {
			if objects[i].Path == change.Path {
				return objects
			}
		}
		return append(objects, Object{
			Path:         change.Path,
			Size:         change.Size,
			LastModified: change.LastModified(),
			FileId:       change.FileId,
		})
	case ChangeDeleted, ChangePurged:
		kept := make([]Object, 0, len(objects))
		for _, obj := range objects {
			if obj.Path != change.Path {
				kept = append(kept, obj)
			}
		}
		return kept
	case ChangeMoved:
		// moved 只改路径：size/lastModified 等元数据跟着走，fileId 用事件里的值
		next := make([]Object, 0, len(objects))
		var moved *Object
		for _, obj := range objects {
			if obj.Path == change.OldPath {
				renamed := obj
				renamed.Path = change.Path
				renamed.FileId = change.FileId
				moved = &renamed
				continue
			}
			if obj.Path == change.Path {
				// 目标位置已有同名对象：事件流把它排在同一批里，以 moved 的结果为准
				continue
			}
			next = append(next, obj)
		}
		if moved != nil {
			next = append(next, *moved)
		}
		return next
	default:
		return objects
	}
}
