package clipboard

import (
	"errors"
	"testing"
)

// mockRunKey 实现 runKey 接口，绝不触碰真实注册表。
type mockRunKey struct {
	values map[string]string
	closed bool
}

func (m *mockRunKey) GetStringValue(name string) (string, uint32, error) {
	v, ok := m.values[name]
	if !ok {
		return "", 0, errors.New("值不存在")
	}
	return v, 0, nil
}

func (m *mockRunKey) SetStringValue(name, value string) error {
	m.values[name] = value
	return nil
}

func (m *mockRunKey) DeleteValue(name string) error {
	if _, ok := m.values[name]; !ok {
		return errors.New("值不存在")
	}
	delete(m.values, name)
	return nil
}

func (m *mockRunKey) Close() error {
	m.closed = true
	return nil
}

func TestReadAutoStart(t *testing.T) {
	k := &mockRunKey{values: map[string]string{}}
	if enabled, err := readAutoStart(k); err != nil || enabled {
		t.Fatalf("空键应读出未启用，got enabled=%v err=%v", enabled, err)
	}

	k.values[autoStartValueName] = `"C:\\Program Files\\EasyShare\\easyshare.exe"`
	if enabled, err := readAutoStart(k); err != nil || !enabled {
		t.Fatalf("有值应读出已启用，got enabled=%v err=%v", enabled, err)
	}

	if !k.closed {
		t.Fatal("读取后应关闭键句柄")
	}
}

func TestWriteAutoStartQuotesExePath(t *testing.T) {
	k := &mockRunKey{values: map[string]string{}}

	if err := writeAutoStart(k, ""); err == nil {
		t.Fatal("空路径应报错")
	}
	if err := writeAutoStart(k, `C:\Program Files\EasyShare\easyshare.exe`); err != nil {
		t.Fatalf("写自启失败: %v", err)
	}
	got := k.values[autoStartValueName]
	want := `"C:\Program Files\EasyShare\easyshare.exe"`
	if got != want {
		t.Fatalf("路径应引号包裹（含空格安全），got %q want %q", got, want)
	}
}

func TestRemoveAutoStartReportsMissingValueAsError(t *testing.T) {
	k := &mockRunKey{values: map[string]string{}}
	// 共享层如实上抛；「值不存在视为成功」的豁免在平台实现按错误码判断
	if err := removeAutoStart(k); err == nil {
		t.Fatal("删除不存在的值应上抛错误")
	}
	k.values[autoStartValueName] = "x"
	if err := removeAutoStart(k); err != nil {
		t.Fatalf("删除存在的值应成功: %v", err)
	}
}
