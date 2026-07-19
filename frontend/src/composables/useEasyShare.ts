import { onBeforeUnmount, onMounted, ref } from 'vue'
import { core } from '../services/core'
import type { CoreSnapshot } from '../types/core'

const emptySnapshot = (): CoreSnapshot => ({
  status: {
    core: false,
    discovery: false,
    receiver: false,
    webdav: false,
    driveMapped: false,
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
  const mapping = ref(false)
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

  const connectDrive = async () => {
    if (inactive() || mapping.value || snapshot.value.status.driveMapped) return false
    mapping.value = true
    try {
      return await act('连接网络驱动器', core.mapDrive)
    } finally {
      if (!disposed) mapping.value = false
    }
  }

  const initialize = async () => {
    // Wails can mount the frontend just before App.Startup has installed the
    // Core client. Wait for the first successful snapshot so that this startup
    // race cannot consume the window's one automatic mapping attempt.
    while (!inactive()) {
      const refreshed = await refresh()
      if (refreshed) {
        if (snapshot.value.status.core && !snapshot.value.status.driveMapped) {
          // This is intentionally the only automatic attempt for this window.
          // Polling only observes status, so a mapping failure cannot retry.
          await connectDrive()
        }
        return
      }
      await new Promise(resolve => window.setTimeout(resolve, 1000))
    }
  }

  const shutdown = async () => {
    if (inactive() || mapping.value) return
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

  onMounted(() => {
    disposed = false
    void core.logDirectory()
      .then(path => { if (!disposed && path) logDirectory.value = path })
      .catch(value => reportError('resolve log directory', value))

    void initialize().finally(() => {
      if (!inactive() && timer === undefined) {
        timer = window.setInterval(() => void refresh(), 1000)
      }
    })
  })
  onBeforeUnmount(() => {
    disposed = true
    stopPolling()
  })

  return {
    snapshot,
    loading,
    mapping,
    error,
    shuttingDown,
    stopped,
    logDirectory,
    clearError,
    refresh,
    send: async (peerId: string) => {
      if (inactive()) return
      const path = await core.selectFile()
      if (path) await act('发送文件', () => core.send(peerId, path))
    },
    accept: (id: string) => act('接受传输', () => core.accept(id)),
    reject: (id: string) => act('拒绝传输', () => core.reject(id)),
    startDrive: () => act('启动 WebDAV', core.startDrive),
    stopDrive: () => act('停止 WebDAV', core.stopDrive),
    mapDrive: connectDrive,
    unmapDrive: () => act('取消网络驱动器', core.unmapDrive),
    shutdown,
  }
}
