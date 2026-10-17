package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func TestLoadMissingFileCreatesSecureDefaults(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	path := filepath.Join(t.TempDir(), "EasyShare", "config.json")
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if got.APIHost != "127.0.0.1" {
		t.Errorf("APIHost = %q, want 127.0.0.1", got.APIHost)
	}
	if got.WebDAVPort != 19080 {
		t.Errorf("WebDAVPort = %d, want 19080", got.WebDAVPort)
	}
	if got.DiscoveryPort != 9527 {
		t.Errorf("DiscoveryPort = %d, want 9527", got.DiscoveryPort)
	}
	if got.DeviceID == "" {
		t.Error("DeviceID is empty")
	}
	if got.APIToken == "" {
		t.Error("APIToken is empty")
	}
	if got.WebDAVPassword == "" {
		t.Error("WebDAVPassword is empty")
	}
	if got.ReceiveDir != filepath.Join(home, "Downloads", "EasyShare") {
		t.Errorf("ReceiveDir = %q, want Windows Downloads EasyShare directory", got.ReceiveDir)
	}
	if got.WebDAVRoot != filepath.Join(home, "EasyShare") {
		t.Errorf("WebDAVRoot = %q, want user EasyShare directory", got.WebDAVRoot)
	}

	loaded, err := Load(path)
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, got) {
		t.Errorf("second Load() = %#v, want %#v", loaded, got)
	}
}

func TestLoadGeneratesDifferentSecretsForDifferentConfigs(t *testing.T) {
	first, err := Load(filepath.Join(t.TempDir(), "first", "config.json"))
	if err != nil {
		t.Fatalf("first Load() error = %v", err)
	}
	second, err := Load(filepath.Join(t.TempDir(), "second", "config.json"))
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}

	if first.DeviceID == second.DeviceID {
		t.Error("DeviceID was reused")
	}
	if first.APIToken == second.APIToken {
		t.Error("APIToken was reused")
	}
	if first.WebDAVPassword == second.WebDAVPassword {
		t.Error("WebDAVPassword was reused")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	want := Config{
		DeviceID:       "device-id",
		DeviceName:     "workstation",
		APIHost:        "127.0.0.1",
		APIPort:        19079,
		APIToken:       "api-token",
		MCPToken:       "mcp-token",
		DiscoveryPort:  9527,
		TransferPort:   9528,
		ReceiveDir:     `C:\Users\tester\Downloads\EasyShare`,
		WebDAVRoot:     `C:\Users\tester\EasyShare`,
		WebDAVPort:     19080,
		WebDAVUsername: "EasyShare",
		WebDAVPassword: "webdav-password",
		PlatformBaseURL: "http://localhost:8090",
		AdminConsoleURL: "http://localhost:8091",
	}

	if err := Save(path, want); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Load() = %#v, want %#v", got, want)
	}

	replacement := want
	replacement.DeviceName = "replacement"
	if err := Save(path, replacement); err != nil {
		t.Fatalf("replacement Save() error = %v", err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatalf("replacement Load() error = %v", err)
	}
	if !reflect.DeepEqual(got, replacement) {
		t.Errorf("replacement Load() = %#v, want %#v", got, replacement)
	}
}

func TestLoadRejectsInvalidPersistedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("Load() accepted an insecure empty configuration")
	}
}

// 账号体系之前的 config.json 没有 platformBaseUrl / adminConsoleUrl，
// 老用户升级后必须自动补上默认值，否则登录与「管理」入口都会因空地址而不可用。
func TestLoadMigratesMissingPlatformURLs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"deviceId":"device-id","deviceName":"legacy","apiHost":"127.0.0.1","apiPort":19079,
		"apiToken":"api-token","discoveryPort":9527,"transferPort":9528,
		"receiveDir":"C:\\tmp\\recv","webdavRoot":"C:\\tmp\\share","webdavPort":19080,
		"webdavUsername":"EasyShare","webdavPassword":"webdav-password"
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.PlatformBaseURL != "http://localhost:8090" {
		t.Errorf("PlatformBaseURL = %q, want default", got.PlatformBaseURL)
	}
	if got.AdminConsoleURL != "http://localhost:8091" {
		t.Errorf("AdminConsoleURL = %q, want default", got.AdminConsoleURL)
	}
}

// 听记 MCP 配对令牌：旧配置缺省回填一次并落盘——重启后必须保持不变
// （否则 AI 客户端里的令牌天天失效），吊销 = 显式重新生成。
func TestLoadMigratesMissingMCPToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	legacy := `{
		"deviceId":"device-id","deviceName":"legacy","apiHost":"127.0.0.1","apiPort":19079,
		"apiToken":"api-token","discoveryPort":9527,"transferPort":9528,
		"receiveDir":"C:\\tmp\\recv","webdavRoot":"C:\\tmp\\share","webdavPort":19080,
		"webdavUsername":"EasyShare","webdavPassword":"webdav-password"
	}`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	first, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if first.MCPToken == "" {
		t.Fatal("MCPToken was not backfilled for legacy config")
	}
	second, err := Load(path)
	if err != nil {
		t.Fatalf("second Load() error = %v", err)
	}
	if second.MCPToken != first.MCPToken {
		t.Error("MCPToken was regenerated on reload; it must persist across restarts")
	}
}

func TestConcurrentLoadReturnsOnePersistedIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	results := make(chan Config, 8)
	var waitGroup sync.WaitGroup
	for range 8 {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			value, err := Load(path)
			if err != nil {
				t.Errorf("Load() error = %v", err)
				return
			}
			results <- value
		}()
	}
	waitGroup.Wait()
	close(results)
	var first Config
	for value := range results {
		if first.DeviceID == "" {
			first = value
		}
		if value.DeviceID != first.DeviceID || value.APIToken != first.APIToken {
			t.Fatal("concurrent Load returned different identities")
		}
	}
}

// 早期 dev 版把源码默认值 localhost 写进了 config.json，之后升级安装不清理该文件。
// 结果：烘焙的公司地址只对「字段为空」的新装生效，老用户装完仍连本机，公司环境本就无服务。
// 这类配置必须被改写为烘焙值，且改写要落盘（否则每次启动都要重新判一次）。
func TestLoadRebasesLoopbackPlatformURLToBakedCompanyServer(t *testing.T) {
	const baked = "http://47.96.128.231:11054"
	restore := stubDefaultPlatformURL(t, baked)
	defer restore()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigWithPlatformURL(t, path, "http://localhost:8090")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.PlatformBaseURL != baked {
		t.Errorf("PlatformBaseURL = %q, want baked %q", got.PlatformBaseURL, baked)
	}

	// 落盘验证：直接读原始 JSON，而不是再 Load 一次（后者会掩盖「没写盘」）。
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	var persisted Config
	if err := json.Unmarshal(onDisk, &persisted); err != nil {
		t.Fatalf("decode persisted config: %v", err)
	}
	if persisted.PlatformBaseURL != baked {
		t.Errorf("persisted PlatformBaseURL = %q, want baked %q — 改写未落盘", persisted.PlatformBaseURL, baked)
	}
}

// 本地 dev 构建的烘焙值本身就是 loopback，此时绝不能把用户的本机配置改掉。
func TestLoadKeepsLoopbackWhenBakedIsAlsoLoopback(t *testing.T) {
	restore := stubDefaultPlatformURL(t, "http://localhost:8090")
	defer restore()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigWithPlatformURL(t, path, "http://127.0.0.1:8090")

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.PlatformBaseURL != "http://127.0.0.1:8090" {
		t.Errorf("PlatformBaseURL = %q, want the user's own loopback value untouched", got.PlatformBaseURL)
	}
}

// 用户手工填过真实地址时必须被尊重，不能被下一次发版的烘焙值覆盖
// —— 否则「换服务器」这个动作会静默发生在所有人机器上，且无法单独回退。
func TestLoadKeepsUserSuppliedExternalPlatformURL(t *testing.T) {
	const userValue = "http://10.0.0.9:8090"
	restore := stubDefaultPlatformURL(t, "http://47.96.128.231:11054")
	defer restore()

	path := filepath.Join(t.TempDir(), "config.json")
	writeConfigWithPlatformURL(t, path, userValue)

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if got.PlatformBaseURL != userValue {
		t.Errorf("PlatformBaseURL = %q, want user value %q preserved", got.PlatformBaseURL, userValue)
	}
}

// 烘焙值与存量值都非 loopback 且不同：存量值是用户填的，不动。
// 与上一条的区别仅在存量值形态，用表驱动把判定函数的边界钉死。
func TestShouldRebasePlatformURLBoundaries(t *testing.T) {
	cases := []struct {
		name   string
		stored string
		baked  string
		want   bool
	}{
		{"本机值应改写为服务器值", "http://localhost:8090", "http://47.96.128.231:11054", true},
		{"127.0.0.1 同理", "http://127.0.0.1:8090", "http://47.96.128.231:11054", true},
		{"IPv6 回环同理", "http://[::1]:8090", "http://47.96.128.231:11054", true},
		{"本机值遇本机烘焙值不动", "http://localhost:8090", "http://localhost:8090", false},
		{"烘焙值为空不动", "http://localhost:8090", "", false},
		{"外部值不动", "http://10.0.0.9:8090", "http://47.96.128.231:11054", false},
		{"空值不由本函数处理", "", "http://47.96.128.231:11054", false},
		{"坏地址按非本机处理", "://bad", "http://47.96.128.231:11054", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := shouldRebasePlatformURL(testCase.stored, testCase.baked); got != testCase.want {
				t.Errorf("shouldRebasePlatformURL(%q, %q) = %v, want %v",
					testCase.stored, testCase.baked, got, testCase.want)
			}
		})
	}
}

// stubDefaultPlatformURL 临时改写构建期烘焙值，返回还原函数。
// 必须在改写前保存原值：其他用例依赖默认值就是 localhost。
func stubDefaultPlatformURL(t *testing.T, value string) func() {
	t.Helper()
	previous := defaultPlatformBaseURL
	defaultPlatformBaseURL = value
	return func() { defaultPlatformBaseURL = previous }
}

func writeConfigWithPlatformURL(t *testing.T, path, platformURL string) {
	t.Helper()
	content := `{
		"deviceId":"device-id","deviceName":"tester","apiHost":"127.0.0.1","apiPort":19079,
		"apiToken":"api-token","mcpToken":"mcp-token",
		"discoveryPort":9527,"transferPort":9528,
		"receiveDir":"C:\\tmp\\recv","webdavRoot":"C:\\tmp\\share","webdavPort":19080,
		"webdavUsername":"EasyShare","webdavPassword":"webdav-password",
		"platformBaseUrl":"` + platformURL + `","adminConsoleUrl":"http://localhost:8091"
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
