// 平台无关的「开机自动记录」逻辑。
//
// 需求边界（iterations/2026-08-31-plugin-system.md：录制随桌面端进程生命周期，
// 「开机自启记录」是独立特性）：录制本身随应用启动即恢复（syncClipboardSurface），
// 本特性补的是 OS 层——用户在应用内可控「开机自动启动 EasyShare 并继续记录」，
// 不再依赖装机时 NSIS 的一次性问句。实现为 HKCU Run 键读写（用户级，无需提权），
// 与安装器写入同名值（EasyShare），卸载器删除该值时自然一并清理。
package clipboard

import "fmt"

// autoStartValueName 注册表 Run 值名，必须与 build/windows/installer/project.nsi
// 的 ${INFO_PRODUCTNAME} 一致（同名同键：开关覆盖安装器写入，卸载器删除时一并清）。
const autoStartValueName = "EasyShare"

// runKey 抽象 Run 键操作，单测注入 mock，绝不写真注册表。
type runKey interface {
	GetStringValue(name string) (string, uint32, error)
	SetStringValue(name, value string) error
	DeleteValue(name string) error
	Close() error
}

// readAutoStart 从已打开的 Run 键读自启状态。
func readAutoStart(k runKey) (bool, error) {
	if k == nil {
		return false, fmt.Errorf("run key 不可用")
	}
	defer k.Close()
	_, _, err := k.GetStringValue(autoStartValueName)
	if err != nil {
		return false, nil // 键或值不存在 = 未启用（注册表查询的常规路径）
	}
	return true, nil
}

// writeAutoStart 写入自启值：引号包裹的当前可执行文件路径（与安装器同格式）。
func writeAutoStart(k runKey, exe string) error {
	if k == nil {
		return fmt.Errorf("run key 不可用")
	}
	if exe == "" {
		return fmt.Errorf("可执行文件路径为空")
	}
	defer k.Close()
	return k.SetStringValue(autoStartValueName, `"`+exe+`"`)
}

// removeAutoStart 删除自启值；值不存在的豁免由平台实现按具体错误码判断，
// 这里如实上抛错误，避免吞掉权限等真实故障。
func removeAutoStart(k runKey) error {
	if k == nil {
		return fmt.Errorf("run key 不可用")
	}
	defer k.Close()
	return k.DeleteValue(autoStartValueName)
}

// AutoStartSupported 当前平台是否支持开机自启。
func (s *Service) AutoStartSupported() bool { return autoStartSupported() }

// AutoStartEnabled 读取自启状态（注册表为唯一真相源，不落 settings.json）。
func (s *Service) AutoStartEnabled() (bool, error) { return autoStartEnabled() }

// SetAutoStart 开关「开机自动记录」：enable=true 写 Run 键（当前 exe 路径）。
func (s *Service) SetAutoStart(enable bool) error { return setAutoStart(enable) }
