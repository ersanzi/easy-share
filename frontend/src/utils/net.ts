// 判断服务地址是否指向本机回环（与 Go 侧 account.IsLoopbackURL 同口径）。
// 企业部署下员工机器上残留的 127.0.0.1 地址连不到任何公司服务，只在界面上标出来。
export function isLoopbackURL(raw: string): boolean {
  if (!raw) return false
  try {
    const host = new URL(raw).hostname.toLowerCase()
    return host === 'localhost' || host === '[::1]' || host.startsWith('127.')
  } catch {
    return false
  }
}
