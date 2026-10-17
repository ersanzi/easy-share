//go:build windows

package cfmount

import (
	"testing"
	"time"

	"easyshare/internal/cfapi"
	"easyshare/internal/drive"
)

// TestAncestorDirs 覆盖父目录推导（纯函数，不需要 CfApi）。
func TestAncestorDirs(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"a.txt", nil},
		{"a/b.txt", []string{"a"}},
		{"a/b/c.txt", []string{"a", "a/b"}},
		{"a/b/c/d.txt", []string{"a", "a/b", "a/b/c"}},
		{`a\b.txt`, []string{"a"}}, // 反斜杠容错
		{"/a/b.txt", []string{"a"}},
	} {
		got := ancestorDirs(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ancestorDirs(%q) = %v，期望 %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ancestorDirs(%q)[%d] = %q，期望 %q", c.in, i, got[i], c.want[i])
			}
		}
	}
}

// TestEntriesInfersDirectories 是适配层的核心验收：控制面只给**扁平文件列表**
// （对象存储里没有目录对象），适配层必须**推导出全部中间目录**，
// 否则 cfapi 逐层建树时会找不到父目录。
func TestEntriesInfersDirectories(t *testing.T) {
	mod := time.Unix(1767225600, 0)
	objects := []drive.Object{
		{Path: "readme.txt", Size: 10, LastModified: mod, FileId: 101},
		{Path: "docs/guide.md", Size: 20, LastModified: mod, FileId: 102},
		{Path: "docs/2026/plan.md", Size: 30, LastModified: mod, FileId: 103},
	}

	entries := Entries(objects)

	byPath := map[string]int{}
	for i, e := range entries {
		byPath[e.Path] = i
	}

	// ① 全部原始文件都在，且身份是 fileId 编码
	for _, o := range objects {
		i, ok := byPath[o.Path]
		if !ok {
			t.Fatalf("文件 %s 丢失", o.Path)
		}
		e := entries[i]
		if e.IsDir {
			t.Errorf("%s 被标成了目录", o.Path)
		}
		if e.Size != o.Size {
			t.Errorf("%s 大小 = %d，期望 %d", o.Path, e.Size, o.Size)
		}
		if string(e.Identity) != string(encodeIdentityFor(o.FileId.Int64())) {
			t.Errorf("%s 身份 = %q，期望 %q", o.Path, e.Identity, encodeIdentityFor(o.FileId.Int64()))
		}
	}

	// ② 中间目录被推导出来（两层都要）
	for _, d := range []string{"docs", "docs/2026"} {
		i, ok := byPath[d]
		if !ok {
			t.Fatalf("目录 %s 没被推导出来：%v", d, keys(byPath))
		}
		if !entries[i].IsDir {
			t.Errorf("%s 没被标成目录", d)
		}
	}

	// ③ 目录不重复（docs 只出现一次，虽然有两个文件在它下面）
	count := 0
	for _, e := range entries {
		if e.Path == "docs" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("目录 docs 出现了 %d 次，期望 1", count)
	}

	// ④ 目录身份与文件身份前缀不同（便于回调里区分并定位问题）
	if e := entries[byPath["docs"]]; len(e.Identity) == 0 || e.Identity[0] == 'e' && string(e.Identity[:4]) != "esd:" {
		t.Errorf("目录身份前缀不对: %q", e.Identity)
	}
	// ⑤ 目录 mtime 是 0（未知），不是编造的假时间
	if e := entries[byPath["docs"]]; e.ModifiedUnix != 0 {
		t.Errorf("目录 mtime = %d，期望 0（未知）", e.ModifiedUnix)
	}
}

// TestEntriesFileIdZeroNoCollision 是一条**回归测试**，守住一个静默数据错误的边界。
//
// 背景：控制面的 `fileId` 是装箱类型，遇到「不合规路径的历史脏数据」时返回 null
// （DriveFileService.list 内部 reconcileAndMap 步骤的 catch 分支），
// Go 侧反序列化后是 0。
// 若直接把 0 编码进身份，**所有这类文件会共享身份 `esf:0`**——
// fetch 回调按身份反查路径时会全部指错，表现为「打开 A 读到 B 的内容」。
//
// 本用例断言：fileId=0 的多个文件，**身份各不相同**，且与正常 fileId 的身份不冲突。
func TestEntriesFileIdZeroNoCollision(t *testing.T) {
	mod := time.Unix(1767225600, 0)
	objects := []drive.Object{
		{Path: "dirty-a.txt", Size: 1, LastModified: mod, FileId: 0}, // 脏数据：无 fileId
		{Path: "dirty-b.txt", Size: 2, LastModified: mod, FileId: 0}, // 另一个脏数据
		{Path: "clean.txt", Size: 3, LastModified: mod, FileId: 42},  // 正常
	}

	entries := Entries(objects)
	byID := map[string]string{}
	for _, e := range entries {
		if e.IsDir {
			continue
		}
		id := string(e.Identity)
		if other, dup := byID[id]; dup {
			t.Fatalf("身份碰撞：%s 与 %s 共用身份 %q", other, e.Path, id)
		}
		byID[id] = e.Path
	}
	if len(byID) != 3 {
		t.Fatalf("身份去重后 = %d 个，期望 3：%v", len(byID), byID)
	}

	// 正常文件用 esf: 前缀（可跨会话稳定）
	clean := findEntry(t, entries, "clean.txt")
	if string(clean.Identity) != "esf:42" {
		t.Errorf("正常文件身份 = %q，期望 esf:42", clean.Identity)
	}
	// 脏数据用 esp: 前缀（路径派生），与 esf: 明确区分
	for _, name := range []string{"dirty-a.txt", "dirty-b.txt"} {
		e := findEntry(t, entries, name)
		if len(e.Identity) < 4 || string(e.Identity[:4]) != "esp:" {
			t.Errorf("%s 身份 = %q，期望 esp: 前缀", name, e.Identity)
		}
	}
}

func findEntry(t *testing.T, entries []cfapi.CloudEntry, path string) cfapi.CloudEntry {
	t.Helper()
	for _, e := range entries {
		if e.Path == path {
			return e
		}
	}
	t.Fatalf("清单里没有 %s", path)
	return cfapi.CloudEntry{}
}

// encodeIdentityFor 复刻 cfapi.EncodeIdentity 的结果，避免测试为了比对而 import cfapi。
func encodeIdentityFor(id int64) []byte {
	return []byte("esf:" + itoa(id))
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func keys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
