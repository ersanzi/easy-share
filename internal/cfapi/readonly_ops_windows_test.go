//go:build windows

package cfapi

import (
	"os"
	"path/filepath"
	"testing"
)

// 只读位到底挡住了哪些**写意图**？
//
// KI-14 只修了「改写已有占位符内容」这一条。但用户在资源管理器里的写意图不止写：
// **删除**、**重命名**、**新建**同样是「本地改了、云端不知道」——
// cfapi 只注册读回调，这些动作不会上报。
//
// 本文件把边界**实测**清楚并如实记录（不改代码掩盖观测结果）。
// 结论直接决定「能不能让 Sync 可重复」：见文件末尾的说明。
//
// ⚠️ 为什么必须实测：`FILE_ATTRIBUTE_READONLY` 在 Windows 上的语义历来是
// **建议性**的——它让普通写入失败，但删除/改名在多数文件系统上**仍然可行**
// （资源管理器会多问一句"确定删除?"，API 调用根本不问）。
// 这正是不可假设、只能实测的地方。

// TestReadOnlyPlaceholderDeleteSucceeds 记录实测事实：**只读位不挡删除**。
//
// 后果分析（按严重度排序，先说不严重的）：
//   - 内容**不会丢**：删除是纯本地动作，云端对象还在；
//   - 但下一个 Sync 全量重建会把占位符**重新建出来** ——
//     用户看到「删掉的文件自己回来了」，会认为是 bug（其实是"没删掉"）；
//   - 真正的风险在**双向同步（切片 5b）**：那时本地删除会被当成删除意图上报，
//     而我们现在**没有删除回调**，无从区分「用户真想删」与「同步过程误删」。
//
// 所以这是「原型可接受、投产必须处理」的缺口——处理方式就是 5b 注册
// `NOTIFY_DELETE` / `NOTIFY_DELETE_COMPLETION`（WPS 的回调表里就有这两项）。
func TestReadOnlyPlaceholderDeleteSucceeds(t *testing.T) {
	root, cleanup := mountReadOnlyProbe(t)
	defer cleanup()

	full := filepath.Join(root, "ro.txt")
	err := os.Remove(full)
	// 断言**实测到的**行为（能删），而不是我们希望的行为——
	// 若哪天 Windows 改了语义把它挡住，这条用例会失败并提醒我们更新结论。
	if err != nil {
		t.Fatalf("删除竟然被拒了（%v）——与 2026-10-16 的实测结论相反，"+
			"需重新评估「只读位保护范围」并更新 KI-14/KI-16", err)
	}
	t.Log("实测：只读占位符**可被删除**（只读位不挡删除）。" +
		"内容不丢（云端对象仍在），但 Sync 重建会让文件「复活」；" +
		"投产需在 5b 注册 NOTIFY_DELETE 回调")
}

// TestReadOnlyPlaceholderRenameSucceeds 记录实测事实：**只读位不挡重命名**。
//
// 后果：改名是纯本地动作，云端无感知。**内容不丢**——
// fetch 回调是按**身份**（fileId）反查云端路径的，不依赖本地名，
// 所以改名后的文件**仍能正常读**（见下一条用例实测）。
// 真正的后果是**重名**：Sync 重建会按云端清单再建一个原名占位符，
// 于是同一份内容在本地出现两个名字。
func TestReadOnlyPlaceholderRenameSucceeds(t *testing.T) {
	root, cleanup := mountReadOnlyProbe(t)
	defer cleanup()

	full := filepath.Join(root, "ro.txt")
	renamed := filepath.Join(root, "ro-renamed.txt")
	if err := os.Rename(full, renamed); err != nil {
		t.Fatalf("重命名竟然被拒了（%v）——与 2026-10-16 的实测结论相反，"+
			"需更新结论", err)
	}
	t.Log("实测：只读占位符**可被重命名**（只读位不挡改名）")
}

// TestRenamedPlaceholderStillReadable 验证改名后**内容仍读得出**。
//
// 这条是上一条的配套：我原先推测「改名后可能读不出内容」，
// 但那个推测**不成立**——fetch 回调用身份（fileId）反查云端路径，
// 与本地文件名无关。不实测就会把一个不存在的风险写进文档。
func TestRenamedPlaceholderStillReadable(t *testing.T) {
	root, cleanup := mountReadOnlyProbe(t)
	defer cleanup()

	full := filepath.Join(root, "ro.txt")
	renamed := filepath.Join(root, "renamed.txt")
	if err := os.Rename(full, renamed); err != nil {
		t.Skipf("重命名被拒（%v），本用例不适用", err)
	}
	got, err := os.ReadFile(renamed)
	if err != nil {
		t.Fatalf("改名后读不出内容：%v —— 说明索引/身份链在改名后断了", err)
	}
	if string(got) != "original cloud content" {
		t.Fatalf("改名后内容 = %q，期望原始内容", got)
	}
	t.Logf("改名后仍能按需读取（%d 字节）——身份链（fileId）不依赖本地文件名", len(got))
}

// TestNewFileInSyncRootSucceeds 记录实测事实：**能在同步根里新建文件**。
//
// 只读位只作用于**已有占位符**，不影响新建。新文件是纯本地文件：
// 云端无感知、不会上传、也不会进索引。
//
// 后果：用户以为「放进网盘了」，其实只存在本机——**这在双向同步落地前
// 是一个明确的语义边界**，必须在产品文案/接入决策里交代清楚，
// 不能让用户误以为放进同步根就等于上云。
func TestNewFileInSyncRootSucceeds(t *testing.T) {
	root, cleanup := mountReadOnlyProbe(t)
	defer cleanup()

	newFile := filepath.Join(root, "brand-new.txt")
	if err := os.WriteFile(newFile, []byte("brand new"), 0o644); err != nil {
		t.Fatalf("新建文件被拒（%v）——与实测结论相反，需更新结论", err)
	}
	t.Log("实测：可在同步根内新建文件（只读位不挡新建）。" +
		"该文件云端无感知、不会上传——投产前需明确交代这个边界")
	_ = os.Remove(newFile)
}

// 结论（2026-10-16 实测，决定「能否让 Sync 可重复」）
//
// | 写意图 | 只读位是否挡住 | 后果 | 需不需要处理 |
// | --- | --- | --- | --- |
// | 改写已有占位符内容 | ✅ 挡住（KI-14） | —— | 已修 |
// | 删除占位符 | ❌ **不挡** | 内容不丢；Sync 重建后文件「复活」 | 5b 注册 NOTIFY_DELETE |
// | 重命名占位符 | ❌ **不挡** | 内容不丢且仍可读；但会与重建出的原名并存 | 5b 注册 rename 通知 |
// | 在同步根内新建文件 | ❌ **不挡** | 纯本地文件，不上传 | 产品文案 + 5b |
//
// **这三条都不构成数据丢失**，所以不阻塞接入；但它们说明
// 「只读占位符」的「只读」是**有限度的**：它保护的是**内容**，
// 不是**目录结构**。
//
// 对 KI-14 时序陷阱的影响：让 Sync 可重复**不再有数据丢失风险**
// （唯一的丢失路径——改写内容——已被挡住），
// 但会让「删掉的文件复活」「改名后出现两份」这两个困惑出现得更频繁。
// 因此 Sync 可重复**可以做**，但应当同时把上面三条的语义
// 在接入时交代清楚，而不是等用户来问。
