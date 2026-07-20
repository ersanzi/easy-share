import { onBeforeUnmount, onMounted, ref } from 'vue'
import { core } from '../services/core'
import type { CoreSnapshot } from '../types/core'

const emptySnapshot = (): CoreSnapshot => ({
  status: {
    core: false,
    discovery: false,
    receiver: false,
    webdav: false,
    cloudEnabled: false,
  },
  peers: [],
  tasks: [],
})

const errorMessage = (value: unknown) => value instanceof Error ? value.message : String(value)
const reportError = (operation: string, value: unknown) => {
  const runtimeError = value instanceof Error ? value : new Error(String(value))
  try {
    void Promise.resolve(core.reportError(`${operation}: ${runtimeError.message}`, runtimeError.stack ?? ''))
      .catch(() => undefined)
  } catch {
    // Diagnostics must never interrupt the user action that raised the error.
  }
}

type ErrorSource = 'refresh' | 'operation' | ''

export function useEasyShare() {
  const snapshot = ref<CoreSnapshot>(emptySnapshot())
  const loading = ref(true)
  const error = ref('')
  const shuttingDown = ref(false)
  const stopped = ref(false)
  const logDirectory = ref('%LOCALAPPDATA%\\EasyShare\\logs')
  let timer: number | undefined
  let disposed = false
  let errorSource: ErrorSource = ''

  const inactive = () => disposed || shuttingDown.value || stopped.value

  const stopPolling = () => {
    if (timer !== undefined) {
      window.clearInterval(timer)
      timer = undefined
    }
  }

  const clearError = () => {
    error.value = ''
    errorSource = ''
  }

  const setError = (source: ErrorSource, value: unknown) => {
    error.value = errorMessage(value)
    errorSource = source
  }

  const refresh = async () => {
    if (inactive()) return false
    try {
      const next = await core.snapshot()
      if (inactive()) return false
      snapshot.value = next
      // Operation failures (especially automatic mapping failures) must remain
      // visible instead of disappearing on the next one-second status poll.
      if (errorSource === 'refresh') clearError()
      return true
    } catch (value) {
      reportError('刷新状态', value)
      if (!inactive()) setError('refresh', value)
      return false
    } finally {
      if (!disposed) loading.value = false
    }
  }

  const act = async (operation: string, action: () => Promise<unknown>) => {
    if (inactive()) return false
    try {
      await action()
      if (inactive()) return false
      clearError()
      await refresh()
      return true
    } catch (value) {
      reportError(operation, value)
      if (!inactive()) setError('operation', value)
      return false
    }
  }

  const initialize = async () => {
    // Wails can mount the frontend just before App.Startup has installed the
    // Core client. Wait for the first successful snapshot before proceeding.
    while (!inactive()) {
      const refreshed = await refresh()
      if (refreshed) return
      await new Promise(resolve => window.setTimeout(resolve, 1000))
    }
  }

  const shutdown = async () => {
    if (inactive()) return
    if (!window.confirm('确定退出 EasyShare 的全部后台服务吗？正在进行的传输将会停止。')) return

    // Stop polling before ShutdownAll. Once shutdown begins, no code path is
    // allowed to contact a Core process that may already have exited.
    shuttingDown.value = true
    stopPolling()
    clearError()
    try {
      await core.shutdown()
    } catch (value) {
      reportError('退出全部服务', value)
      if (!disposed) setError('operation', `服务已停止响应：${errorMessage(value)}`)
    } finally {
      if (!disposed) {
        snapshot.value = emptySnapshot()
        stopped.value = true
        shuttingDown.value = false
        loading.value = false
      }
    }
  }

  const startPolling = (intervalMs: number) => {
    stopPolling()
    timer = window.setInterval(() => void refresh(), intervalMs)
  }

  const handleVisibility = () => {
    if (inactive()) return
    if (document.hidden) {
      // Window hidden to tray: reduce polling to save resources
      startPolling(5000)
    } else {
      // Window restored: immediate refresh then fast polling
      void refresh()
      startPolling(1000)
    }
  }

  onMounted(() => {
    disposed = false
    document.addEventListener('visibilitychange', handleVisibility)

    void core.logDirectory()
      .then(path => { if (!disposed && path) logDirectory.value = path })
      .catch(value => reportError('resolve log directory', value))

    void initialize().finally(() => {
      if (!inactive() && timer === undefined) {
        startPolling(1000)
      }
    })
  })
  onBeforeUnmount(() => {
    disposed = true
    stopPolling()
    document.removeEventListener('visibilitychange', handleVisibility)
  })

  return {
    snapshot,
    loading,
    error,
    shuttingDown,
    stopped,
    logDirectory,
    clearError,
    refresh,
    send: async (peerId: string) => {
      if (inactive()) return
      const paths = await core.selectFiles()
      if (paths && paths.length) {
        for (const path of paths) {
          await act('发送文件', () => core.send(peerId, path))
        }
      }
    },
    accept: (id: string) => act('接受传输', () => core.accept(id)),
    acceptAs: (id: string) => act('另存传输', () => core.acceptAs(id)),
    reject: (id: string) => act('拒绝传输', () => core.reject(id)),
    clearHistory: () => act('清除记录', core.clearHistory),
    deleteTask: (id: string) => act('删除记录', () => core.deleteTask(id)),
    cloudUpload: async () => {
      if (inactive()) return
      try {
        await core.cloudUpload()
        clearError()
      } catch (value) {
        reportError('上传文件', value)
        if (!inactive()) setError('operation', value)
      }
    },
    cloudDownload: (key: string) => act('下载文件', () => core.cloudDownload(key)),
    cloudDelete: (key: string) => act('删除文件', () => core.cloudDelete(key)),
    cloudShare: (key: string, hours: number) => core.cloudShare(key, hours),
    startDrive: () => act('启动 WebDAV', core.startDrive),
    stopDrive: () => act('停止 WebDAV', core.stopDrive),
    shutdown,
  }
}
