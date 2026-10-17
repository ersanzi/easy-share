//go:build windows

package cfapi

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"unsafe"
)

// 本文件是「云端文件清单 → 本地占位符树」的编排层。
//
// ── 为什么需要它 ──────────────────────────────────────────────
//
// 控制面给出的是一份**扁平的相对路径清单**（`[]drive.Object`，每个带
// Path/Size/LastModified/FileId）。而 CfApi 的建树语义是**逐层**的：
//
//   - `CfCreatePlaceholders(rootPath, ...)` 里 `rootPath` 决定父目录；
//   - 每个 `RelativeFileName` 只能是该父目录下的**一个名字**，带分隔符会
//     整批失败（0x8007007B，实测见 dirplaceholder_windows_test.go）。
//
// 所以必须自己把扁平清单**按目录分层**，从根往下逐层建。本文件做这件事，
// 且**不依赖任何真实云端**（输入是纯数据），便于单测。
//
// ── 两个来自 WPS 取证的取值决定 ────────────────────────────────
//
//  1. 目录占位符用 `DISABLE_ON_DEMAND_POPULATION`（取证 §3.3b 实测）：
//     目录内容由我们自己的列表任务驱动，不让系统替它按需填充——否则系统会
//     在枚举目录时向我们发 FETCH_PLACEHOLDERS 请求，而我们没注册那个回调。
//  2. 全部占位符用 `MARK_IN_SYNC`（同源）：避免系统把新建占位符当成
//     「待同步的本地改动」而触发上报。

// CloudEntry 是编排层的输入：云端一个条目（文件或目录）。
//
// 刻意不复用 drive.Object：那个类型定义在 internal/drive，本包不应反向依赖它
// （cfapi 是更底层的包）。由调用方做一次映射。
type CloudEntry struct {
	// Path 是相对同步根的路径，如 "docs/2026/plan.md"。**目录不带结尾斜杠**。
	Path string
	// Size 是字节数；目录为 0。
	Size int64
	// ModifiedUnix 是最后修改时间（Unix 秒）。0 表示未知。
	ModifiedUnix int64
	// IsDir 为 true 表示目录。
	IsDir bool
	// Identity 是该条目的稳定身份（通常由控制面 fileId 编码）。
	// 用于 FETCH_DATA 回调反查文件——**不能用路径**（改名后路径会变）。
	Identity []byte
	// ReadOnly 为 true 时占位符带 FILE_ATTRIBUTE_READONLY。
	//
	// 只读原型必须置位：cfapi 没有写回调，用户编辑不会被上报，
	// 而重建会把本地改动覆盖掉（实测见 write_probe_windows_test.go）。
	// 只读位把写挡在文件系统层，用户得到明确的「拒绝访问」而不是静默丢失。
	ReadOnly bool
}

// TreeStats 是一次建树的结果统计。
type TreeStats struct {
	// DirsCreated / FilesCreated 是成功创建的目录与文件占位符数量。
	DirsCreated  int
	FilesCreated int
	// Updated 是通过 CfUpdatePlaceholder 刷新元数据的条目数（**不是**新建）。
	//
	// 单独计数而不是并进 Created：两者语义不同——
	// 「新建」是首次铺树，「更新」是远端变了只对齐元数据。
	// 混在一起会让「重连后铺了 10000 个」与「刷新了 10000 个」看起来一样，
	// 而前者是真实工作量，后者应当是零下载的。
	Updated int
	// Removed 是清理掉的**幽灵占位符**数（云端已删、本地还在）。
	//
	// 只统计**已删除**的。跳过不删的（已水合的）记在 SkippedHydrated，
	// 两者分开是因为它们的语义完全不同：前者是我们主动清理，
	// 后者是**刻意不删**（见 removeGhosts 的说明）。
	Removed int
	// SkippedHydrated 是「云端已删但本地已水合、故刻意保留」的数量。
	//
	// 这不是失败。保留是因为那份内容**只存在于本地**——
	// 删掉它就是销毁用户仅存的副本（实测见 ghost_probe_windows_test.go）。
	SkippedHydrated int
	// Failures 记录逐条失败（不中断整批——对齐 WPS 的做法：批量接口允许部分成功）。
	Failures []EntryFailure
}

// EntryFailure 是单个条目的创建失败记录。
//
// **区分两种失败**（这一点曾被自己的测试抓出来）：
//   - **调用级失败**：`CfCreatePlaceholders` 整体返回非 0（如父目录不是同步根），
//     此时 `Result` 恒为 0——**0 在这里表示「未回填」而不是「成功」**，
//     所以必须用 `Err` 字段承载原因，否则日志会打印出「HRESULT=0x00000000」
//     这种看起来像成功的假信息。
//   - **条目级失败**：调用成功但该条的 `Result` 非 0，用一个 `EntryFailure` 承载。
type EntryFailure struct {
	// Path 是失败条目在云端的相对路径。
	Path string
	// IsDir 标识它是目录还是文件。
	IsDir bool
	// Result 是 CfCreatePlaceholders 回填的 HRESULT；调用级失败时为 0（未回填）。
	Result uint32
	// Err 是调用级失败的原因；条目级失败时为 nil。
	Err error
}

// String 给出可读的失败描述，避开「Result=0 看起来像成功」的陷阱。
func (f EntryFailure) String() string {
	kind := "文件"
	if f.IsDir {
		kind = "目录"
	}
	if f.Err != nil {
		return fmt.Sprintf("%s %s: %v", kind, f.Path, f.Err)
	}
	return fmt.Sprintf("%s %s: HRESULT=0x%08X", kind, f.Path, f.Result)
}

// Error 实现 error，便于把失败集当作整体上报。
func (s TreeStats) Error() string {
	if len(s.Failures) == 0 {
		return ""
	}
	return fmt.Sprintf("cfapi: %d 个占位符创建失败（首个 %s）",
		len(s.Failures), s.Failures[0])
}

// BuildTree 把扁平清单建成占位符树。
//
// 顺序保证：**先父后子**（按路径深度排序），这是逐层创建的前提。
// 任一目录创建失败时，其子项会一并失败——但我们不跳过尝试，因为
// 内核可能已把父目录建出来（批量接口允许部分成功），逐条记录更利于诊断。
//
// 幂等性：重复调用会对已存在的占位符再建一次。内核允许「同名已存在」的情况
// （会更新元数据），但本函数**不做去重**——去重是调用方的事（它才知道
// 本地已有多少、哪些需要更新）。
func BuildTree(rootPath string, entries []CloudEntry) TreeStats {
	var stats TreeStats

	// 按深度排序确保先父后子；同深度按路径排序保证结果稳定（可复现）。
	sorted := make([]CloudEntry, 0, len(entries))
	sorted = append(sorted, entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		di, dj := pathDepth(sorted[i].Path), pathDepth(sorted[j].Path)
		if di != dj {
			return di < dj
		}
		return sorted[i].Path < sorted[j].Path
	})

	for _, e := range sorted {
		parent, name := splitParent(e.Path)
		if name == "" {
			// 空路径或只有分隔符：跳过（调用方数据问题，不当作创建失败）
			continue
		}
		// 父目录的绝对路径 = rootPath + 父相对路径
		parentAbs := rootPath
		if parent != "" {
			parentAbs = joinPath(rootPath, parent)
		}

		specs := []PlaceholderSpec{{
			RelativeName: name,
			Size:         e.Size,
			ModifiedUnix: e.ModifiedUnix,
			CreatedUnix:  e.ModifiedUnix, // 云端未必给创建时间；用修改时间兜底，避免显示 1601 年
			IsDir:        e.IsDir,
			Identity:     e.Identity,
			ReadOnly:     e.ReadOnly,
		}}
		ph := NewPlaceholders(specs)
		if len(ph.Infos) == 0 {
			// NewPlaceholders 只在名字含 NUL 时跳过
			stats.Failures = append(stats.Failures, EntryFailure{
				Path: e.Path, IsDir: e.IsDir,
				Err: fmt.Errorf("名字非法（含 NUL 或为空）"),
			})
			continue
		}
		if _, err := CreatePlaceholders(parentAbs, ph.Infos, CreateFlagNone); err != nil {
			// 调用级失败：Result 未被回填，原因在 err 里（不要打印 Result=0）。
			//
			// ⚠️ 实测（2026-10-16）：**ERROR_ALREADY_EXISTS 正是从这里返回的**，
			// 不是条目级 Result。我第一版只处理了条目级分支，于是刷新仍然报错。
			// 批量接口在「首个条目即失败」时会把错误上抛到调用级——
			// 所以两条路径都要处理，不能只挑一条。
			if isAlreadyExistsErr(err) {
				if uerr := updateExisting(parentAbs, name, e); uerr != nil {
					stats.Failures = append(stats.Failures, EntryFailure{
						Path: e.Path, IsDir: e.IsDir, Err: uerr,
					})
					continue
				}
				stats.Updated++
				continue
			}
			stats.Failures = append(stats.Failures, EntryFailure{
				Path: e.Path, IsDir: e.IsDir, Err: err,
			})
			continue
		}
		if r := ph.Infos[0].Result; r != 0 {
			// 条目级失败。**最常见的一类是 ERROR_ALREADY_EXISTS**——
			// 占位符已经在磁盘上，而 CfCreatePlaceholders 默认不覆盖。
			//
			// 这正是「定时刷新」走不通的原因（KI-14 实测：第一次刷新就整批失败）。
			// 但**不能**用 `SUPERSEDE` 去覆盖：那条路会清掉本地已水合的内容
			// （实测被覆盖成 0 字节）。正解是走**元数据更新**
			// （CfUpdatePlaceholder，路线图切片 5 第 5 步）：
			// 占位符与内容都保留，只把大小/时间对齐云端。
			if isAlreadyExists(r) {
				if err := updateExisting(parentAbs, name, e); err != nil {
					stats.Failures = append(stats.Failures, EntryFailure{
						Path: e.Path, IsDir: e.IsDir, Err: err,
					})
					continue
				}
				stats.Updated++
				continue
			}
			stats.Failures = append(stats.Failures, EntryFailure{
				Path:   e.Path,
				IsDir:  e.IsDir,
				Result: uint32(r),
			})
			continue
		}
		if e.IsDir {
			stats.DirsCreated++
		} else {
			stats.FilesCreated++
		}
	}
	return stats
}

// pathDepth 返回路径深度（顶层条目为 1）。
func pathDepth(p string) int {
	p = strings.Trim(p, "/\\")
	if p == "" {
		return 0
	}
	return strings.Count(strings.ReplaceAll(p, "\\", "/"), "/") + 1
}

// splitParent 把 "a/b/c.txt" 拆成 ("a/b", "c.txt")；顶层条目父为空。
func splitParent(p string) (string, string) {
	p = strings.Trim(strings.ReplaceAll(p, "\\", "/"), "/")
	if p == "" {
		return "", ""
	}
	i := strings.LastIndex(p, "/")
	if i < 0 {
		return "", p
	}
	return p[:i], p[i+1:]
}

// joinPath 用平台无关的分隔符拼绝对路径。
//
// 用 path.Join 而非 filepath.Join：Windows 上两者结果一致（filepath 会转成 \），
// 而 CfApi 接受两种分隔符。固定用 / 让测试断言更稳定。
func joinPath(base, rel string) string {
	if rel == "" {
		return base
	}
	return path.Join(strings.ReplaceAll(base, "\\", "/"), rel)
}

// isAlreadyExists 判断 CfCreatePlaceholders 的条目级错误是不是
// ERROR_ALREADY_EXISTS (0x800700B7)。
//
// 单独抽出来是因为它**不是**一个"失败"：它表示这个占位符已经在本地，
// 调用方应当转而走元数据更新，而不是记一条 Failure。
func isAlreadyExists(hr int32) bool {
	// 0x800700B7：HRESULT_FROM_WIN32(ERROR_ALREADY_EXISTS)
	return uint32(hr) == 0x800700B7
}

// isAlreadyExistsErr 判断调用级错误是不是同一个 ERROR_ALREADY_EXISTS。
//
// 调用级错误是 checkHResult 包装过的 error，不带 HRESULT 数值，
// 所以按文本判断。**看起来不优雅，但这里要的正是"认出这个特定失败"**：
// 认错会把真实错误当成"已存在"从而吞掉（更糟），所以只匹配
// 那个稳定的十六进制串——它来自 checkHResult 的格式化方式。
func isAlreadyExistsErr(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "0x800700b7") || strings.Contains(s, "0x800700B7")
}

// updateExisting 对已存在的占位符走元数据更新（**不下载内容**）。
//
// 与「删掉重建」的关键区别（KI-14 实测）：
//   - 删掉重建（SUPERSEDE）：本地已水合的内容被清成 0 字节；
//   - 本函数：占位符与内容都保留，只把大小/时间对齐云端。
//
// 路径由 parentAbs + name 拼出——CfUpdatePlaceholder 按**句柄**操作，
// 所以要先把绝对路径打开。句柄用 FILE_READ_ATTRIBUTES 打开：
// 用 GENERIC_READ 会触发水合（实测），那会让「元数据同步」变成「下载整个文件」。
func updateExisting(parentAbs, name string, e CloudEntry) error {
	if !Available() {
		return fmt.Errorf("cfapi: cldapi.dll 不可用")
	}
	abs := joinPath(parentAbs, name)
	h, err := OpenPlaceholderHandle(abs)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(h)

	meta := &FsMetadata{
		BasicInfo: FileBasicInfo{
			CreationTime:   TimeToFiletime(e.ModifiedUnix), // 与创建时一致：拿不到真创建时间就沿用修改时间
			LastAccessTime: TimeToFiletime(e.ModifiedUnix),
			LastWriteTime:  TimeToFiletime(e.ModifiedUnix),
			ChangeTime:     TimeToFiletime(e.ModifiedUnix),
			// 只读位必须**显式带上**：只读原型靠它挡住本地改写（KI-14）。
			// 若这里漏掉，一次同步就会把防护冲掉。
			FileAttributes: FileAttributeNormal | FileAttributeReadOnly,
		},
		FileSize: e.Size,
	}
	if e.IsDir {
		meta.BasicInfo.FileAttributes = FileAttributeDirectory | FileAttributeReadOnly
	}
	flags := uint32(UpdateFlagMarkInSync)
	if e.IsDir {
		flags |= UpdateFlagDisableOnDemandPopulation
	}
	return UpdatePlaceholder(h, meta, e.Identity, flags)
}

// removeGhosts 清理「云端已删、本地还在」的幽灵占位符。
//
// ── 为什么需要它 ─────────────────────────────────────────────
//
// BuildTree 只做「创建 / 更新」，**没有任何删除逻辑**。在 Sync 只能跑一次的时候
// 这个问题摸不到；一旦 Sync 可重复（本轮刚修），立刻暴露：
// 云端删掉的文件会**永久**留在本地，用户看到一个点开就报错的幽灵文件
// （实测见 sync_delete_windows_test.go）。
//
// ── 为什么不能无脑删 ─────────────────────────────────────────
//
// 实测：**已水合**的幽灵占位符，本地**仍持有完整内容**
// （ghost_probe_windows_test.go：34 字节、磁盘分配 34）。
// 既然云端已经删了，那份内容就是**本地仅存**的一份——
// 直接删掉它就是销毁用户数据，而且用户毫无察觉。
//
// 所以只删**未水合**的：那种占位符自己不持有任何字节，
// 删掉不损失数据（云端本来就没有了，本地也从来没有过）。
// 已水合的**保留并计数**，由上层决定怎么提示用户（例如
// 把它标成「云端已删除，本地仍保留」）。这个取舍与 KI-14 一脉相承：
// **代价不对称时取保守一侧**——少删一个幽灵只是留下一个碍眼的文件，
// 误删一个水合文件是真丢数据。
//
// ── 为什么按「删除 + 重建」而不是「原地判断」 ────────────────
//
// 已经水合的判断需要磁盘分配（GetCompressedFileSize）——那是一次额外系统调用。
// 只对「清单里没有、本地却有」的少数条目做这件事，代价可忽略。
func removeGhosts(rootPath string, entries []CloudEntry) (removed, skippedHydrated int) {
	// 期望存在的路径集合（文件与目录都算）
	want := make(map[string]bool, len(entries))
	for _, e := range entries {
		want[filepath.ToSlash(strings.Trim(e.Path, "/"))] = true
	}

	// 遍历本地实际存在的条目（只走两层以内？不——全量递归，但要跳过已删父目录）
	var walk func(absDir, relDir string)
	walk = func(absDir, relDir string) {
		items, err := os.ReadDir(absDir)
		if err != nil {
			return // 读不到就跳过：清理是尽力而为，不该让整次 Sync 失败
		}
		for _, item := range items {
			rel := item.Name()
			if relDir != "" {
				rel = relDir + "/" + rel
			}
			abs := filepath.Join(absDir, item.Name())

			if want[rel] {
				// 云端还有它：保留，继续往里看（目录）
				if item.IsDir() {
					walk(abs, rel)
				}
				continue
			}

			// 云端没有它 —— 幽灵。只删未水合的。
			if !item.IsDir() && isHydrated(abs) {
				skippedHydrated++
				continue
			}
			// 目录：先递归清子项，再删自己（否则非空目录删不掉）
			if item.IsDir() {
				walk(abs, rel)
			}
			if err := os.RemoveAll(abs); err != nil {
				continue // 删不掉就留着（可能是被占用），下次再试
			}
			removed++
		}
	}
	walk(rootPath, "")
	return removed, skippedHydrated
}

// isHydrated 判断占位符是否已在本地持有内容。
//
// 用 GetCompressedFileSize：未下载的占位符返回 0（这正是「列表零下载」的观测口径，
// 见 allocsize_windows_test.go）。判据用「> 0」而不是「== 文件大小」——
// 部分水合（读了前几 MB）同样是"本地有内容"，也不能删。
func isHydrated(absPath string) bool {
	p, err := syscall.UTF16PtrFromString(absPath)
	if err != nil {
		return true // 判断不了就当作有水合内容 → 保守不删
	}
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getCompressedFileSize := kernel32.NewProc("GetCompressedFileSizeW")
	var high uint32
	low, _, errno := getCompressedFileSize.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&high)),
	)
	if uint32(low) == 0xFFFFFFFF && errno != syscall.Errno(0) {
		return true // 查询失败 → 保守不删
	}
	size := (int64(high) << 32) | int64(uint32(low))
	return size > 0
}
