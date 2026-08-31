//go:build windows

package namespace

import (
	"testing"

	"golang.org/x/sys/windows/registry"
)

// 用一次性 CLSID 做真实注册表往返，验证「写进去的确实是资源管理器要读的那些值」。
//
// 不碰产品的三个 CLSID：那会动用户「此电脑」里真实的条目。测试结束即删。
const testCLSID = "{DEADBEEF-0000-4000-8000-EA5Y5HARE001}"

func cleanupTestCLSID(t *testing.T) {
	t.Helper()
	t.Cleanup(func() {
		unregisterOne(testCLSID)
		base := `Software\Classes\CLSID\` + testCLSID
		if _, err := registry.OpenKey(registry.CURRENT_USER, base, registry.QUERY_VALUE); err == nil {
			t.Errorf("测试 CLSID 未被清理干净：%s", base)
		}
	})
}

func readValue(t *testing.T, subPath, name string) string {
	t.Helper()
	key, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Classes\CLSID\`+testCLSID+subPath, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("打不开 %s：%v", subPath, err)
	}
	defer key.Close()
	value, _, err := key.GetStringValue(name)
	if err != nil {
		t.Fatalf("读 %s\\%s 出错：%v", subPath, name, err)
	}
	return value
}

func TestRegisterWritesSubtitlePair(t *testing.T) {
	cleanupTestCLSID(t)
	entry := Entry{
		CLSID:      testCLSID,
		Name:       "测试网盘",
		TargetPath: WebDAVUNC(19081),
		Subtitle:   "已用 1.00 GB / 10.00 GB",
		UserID:     "1761100000000000001",
	}
	if err := registerOne(entry); err != nil {
		t.Fatalf("registerOne 出错：%v", err)
	}

	// 副标题必须是这两个值配对：只写 TileInfo 不写属性值，磁贴会显示空白或错文本
	if got := readValue(t, "", "System.ItemAuthors"); got != entry.Subtitle {
		t.Errorf("System.ItemAuthors = %q，应为 %q", got, entry.Subtitle)
	}
	if got := readValue(t, "", "TileInfo"); got != "prop:System.ItemAuthors" {
		t.Errorf("TileInfo = %q，应为 prop:System.ItemAuthors", got)
	}
	// 账号绑定：条目要标明属于哪个账号
	if got := readValue(t, "", "UserId"); got != entry.UserID {
		t.Errorf("UserId = %q，应为 %q", got, entry.UserID)
	}
	if got := readValue(t, "", ""); got != entry.Name {
		t.Errorf("默认值（磁贴主标题）= %q，应为 %q", got, entry.Name)
	}
	// 目标路径要落在 InitPropertyBag 里，资源管理器双击时读它
	if got := readValue(t, `\Instance\InitPropertyBag`, "TargetFolderPath"); got != entry.TargetPath {
		t.Errorf("TargetFolderPath = %q，应为 %q", got, entry.TargetPath)
	}
	if got := readValue(t, `\Instance`, "CLSID"); got != shellFolder {
		t.Errorf("Instance\\CLSID = %q，应为标准本地文件夹委托 %q", got, shellFolder)
	}
}

func TestRegisterClearsSubtitleWhenEmpty(t *testing.T) {
	cleanupTestCLSID(t)
	withSubtitle := Entry{
		CLSID: testCLSID, Name: "测试", TargetPath: WebDAVUNC(19081),
		Subtitle: "已用 1 GB / 2 GB", UserID: "42",
	}
	if err := registerOne(withSubtitle); err != nil {
		t.Fatalf("首次注册出错：%v", err)
	}

	// 再注册一次、这次不带副标题与账号：残值必须被清掉，否则会显示上个账号的数字
	bare := Entry{CLSID: testCLSID, Name: "测试", TargetPath: WebDAVUNC(19081)}
	if err := registerOne(bare); err != nil {
		t.Fatalf("二次注册出错：%v", err)
	}

	key, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Classes\CLSID\`+testCLSID, registry.QUERY_VALUE)
	if err != nil {
		t.Fatalf("打不开 CLSID 键：%v", err)
	}
	defer key.Close()
	for _, name := range []string{"System.ItemAuthors", "TileInfo", "UserId"} {
		if _, _, err := key.GetStringValue(name); err == nil {
			t.Errorf("%s 应已被清除，但仍存在", name)
		}
	}
}

func TestUnregisterRemovesWholeTree(t *testing.T) {
	entry := Entry{
		CLSID: testCLSID, Name: "测试", TargetPath: WebDAVUNC(19081),
		Subtitle: "x", UserID: "1", IconPath: `C:\nonexistent.exe`,
	}
	if err := registerOne(entry); err != nil {
		t.Fatalf("注册出错：%v", err)
	}
	unregisterOne(testCLSID)

	// CLSID 树与 NameSpace 条目都要没了：留一半会在「此电脑」里显示成空条目
	if _, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Classes\CLSID\`+testCLSID, registry.QUERY_VALUE); err == nil {
		t.Error("CLSID 键仍存在")
	}
	if _, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Explorer\MyComputer\NameSpace\`+testCLSID,
		registry.QUERY_VALUE); err == nil {
		t.Error("NameSpace 条目仍存在")
	}
}
