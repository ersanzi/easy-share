// 插件系统前端中枢：已装插件状态 + 沙箱 iframe postMessage 桥 + 宿主事件转发。
//
// 安全边界（与 internal/plugin 的设计对应）：
// - iframe 为 sandbox="allow-scripts"（opaque origin），只能通过 postMessage 通信；
// - 桥只接受「已注册 iframe」的消息（按 contentWindow 精确匹配 event.source），
//   外部页面注入的消息一律丢弃；
// - 能力鉴权在 Go 侧（按 manifest permissions），前端不做权限判断只做转发。
import { ref } from 'vue'
import { EventsOn } from '../../wailsjs/runtime/runtime'
import { core } from '../services/core'
import type { PluginInfo, PluginUpdateNotice } from '../types/core'

// iframe ↔ 宿主协议（与 assets/sdk/eshare.js 对应）
interface BridgeMessage {
  __eshare: 1
  id?: number
  api?: string
  args?: unknown
  event?: string
  payload?: unknown
  ok?: boolean
  data?: unknown
  error?: string
}

// 已打开插件 iframe 的注册表：contentWindow → 插件信息。
const registeredFrames = new Map<Window, PluginInfo>()
let bridgeInstalled = false

// 已装插件列表（App 启动时加载；安装/卸载/启停后刷新）。
const plugins = ref<PluginInfo[]>([])
const pluginsLoaded = ref(false)

// 启动检查发现的可更新插件（plugin:updates-available 事件，插件中心入口红点用）。
const updateNotices = ref<PluginUpdateNotice[]>([])

function refreshPlugins(): Promise<void> {
  return core.pluginList().then(list => {
    plugins.value = list ?? []
    pluginsLoaded.value = true
  }).catch(() => {
    // 插件系统未就绪（如初始化失败）：保持空列表，不阻塞主界面
    plugins.value = []
    pluginsLoaded.value = true
  })
}

// postMessage 给指定插件 iframe。
function postToFrame(win: Window, message: BridgeMessage): void {
  try {
    win.postMessage(message, '*')
  } catch {
    // 插件页面可能正在跳转/关闭，忽略投递失败
  }
}

// 安装全局桥（App 挂载时调用一次）。
function installBridge(): void {
  if (bridgeInstalled) return
  bridgeInstalled = true

  window.addEventListener('message', (event: MessageEvent) => {
    const msg = event.data as BridgeMessage
    if (!msg || msg.__eshare !== 1 || msg.id === undefined) return
    // 严格校验来源：只接受已注册插件 iframe 的请求。
    const info = registeredFrames.get(event.source as Window)
    if (!info || !msg.api) return
    void core.pluginInvoke(info.id, msg.api, msg.args ?? {}).then(result => {
      const target = event.source as Window
      if (!registeredFrames.has(target)) return // iframe 已关闭，丢弃响应
      postToFrame(target, {
        __eshare: 1, id: msg.id,
        ok: result.ok, data: result.data, error: result.error,
      })
    })
  })

  // 宿主事件 → 转发给声明了对应权限的插件 iframe。
  // clipboard:changed（Go 侧 EventsEmit）→ 需 clipboard.events 权限。
  EventsOn('clipboard:changed', (entry: unknown) => {
    registeredFrames.forEach((info, win) => {
      if (info.permissions?.includes('clipboard.events')) {
        postToFrame(win, { __eshare: 1, event: 'clipboard:changed', payload: entry })
      }
    })
  })

  // 启动检查发现插件更新 → 亮「插件中心」红点（进入插件中心即清除）
  EventsOn('plugin:updates-available', (notices: PluginUpdateNotice[]) => {
    updateNotices.value = notices ?? []
  })
}

// PluginHost 挂载/卸载时登记 iframe。
function registerFrame(win: Window, info: PluginInfo): void {
  registeredFrames.set(win, info)
}

function unregisterFrame(win: Window): void {
  registeredFrames.delete(win)
}

export function usePlugins() {
  if (!pluginsLoaded.value && !bridgeInstalled) {
    installBridge()
    void refreshPlugins()
  } else if (!pluginsLoaded.value) {
    void refreshPlugins()
  }

  // 插件视图名：plugin:{id}
  const pluginView = (id: string) => `plugin:${id}` as const
  const parsePluginView = (view: string): string | null =>
    view.startsWith('plugin:') ? view.slice('plugin:'.length) : null

  return {
    plugins,
    pluginsLoaded,
    updateNotices,
    // 进入插件中心后清除红点（页面内的「更新」按钮继续引导）
    clearUpdateNotices() { updateNotices.value = [] },
    refreshPlugins,
    registerFrame,
    unregisterFrame,
    pluginView,
    parsePluginView,
    // 管理动作（设置页用）：执行后刷新列表
    async setDisabled(id: string, disabled: boolean) {
      await core.pluginSetDisabled(id, disabled)
      await refreshPlugins()
    },
    async uninstall(id: string) {
      await core.pluginUninstall(id)
      await refreshPlugins()
    },
    async installFromPath(path: string) {
      const info = await core.pluginInstallFromPath(path)
      await refreshPlugins()
      return info
    },
  }
}
