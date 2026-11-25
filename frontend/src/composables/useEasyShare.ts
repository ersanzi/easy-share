import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { core } from '../services/core'
import { useConfirm } from './useConfirm'
import type { AuthUser, CoreSnapshot, TransferTask, Space, SpaceType } from '../types/core'
import { EventsOff, EventsOn, OnFileDrop, OnFileDropOff } from '../../wailsjs/runtime/runtime'

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

const deviceTask = (task: TransferTask) => !task.ownerId && (!task.kind || task.kind === 'lan_send' || task.kind === 'lan_receive')

// 轮询与事件共用稳定行身份；未变任务不触发整表 VNode 重建。
const retainTaskRows = (previous: TransferTask[], incoming: TransferTask[]) => {
  const byId = new Map(previous.map(task => [task.id, task]))
  const tasks = incoming.map(task => {
    const old = byId.get(task.id)
    if (!old) return task
    const keys = Object.keys(task) as Array<keyof TransferTask>
    return keys.length === Object.keys(old).length && keys.every(key => Object.is(old[key], task[key])) ? old : task
  })
  return tasks.length === previous.length && tasks.every((task, index) => task === previous[index]) ? previous : tasks
}

export function useEasyShare() {
  const snapshot = ref<CoreSnapshot>(emptySnapshot())
  const loading = ref(true)
  const error = ref('')
  const shuttingDown = ref(false)
  const stopped = ref(false)
  const logDirectory = ref('%LOCALAPPDATA%\\EasyShare\\logs')
  const droppedFiles = ref<string[]>([])
  const droppedDirs = ref<string[]>([])
  const dropSending = ref(false)
  const activeView = ref('overview')
  // 账号登录态（P1）。未登录一律 isAdmin=false，登出后必须回到此值以收起「管理」入口。
  const emptyUser: AuthUser = {
    loggedIn: false,
    userId: '',
    userName: '',
    nickName: '',
    avatar: '',
    isAdmin: false,
    adminScope: '',
    features: { knowledge: false, meeting: false, drive: false, shared: false, plugin: false },
  }
  const currentUser = ref<AuthUser>({ ...emptyUser })
  // 网盘空间独立于挂载开关；仅显示控制面mine返回且功能已开通的空间。
  const driveSpaces = ref<Space[]>([])
  const selectedDriveSpace = ref<SpaceType>('personal')
  let spaceRevision = 0
  const sharedDriveAvailable = computed(() => currentUser.value.features?.drive && currentUser.value.features?.shared
    && driveSpaces.value.some(space => space.spaceType === 'shared' && !!space.permission))
  const refreshDriveSpaces = async () => {
    const revision = ++spaceRevision
    if (!currentUser.value.loggedIn || !currentUser.value.features?.drive) {
      driveSpaces.value = []
      selectedDriveSpace.value = 'personal'
      return
    }
    try {
      const spaces = await core.mySpaces()
      if (!disposed && revision === spaceRevision) driveSpaces.value = spaces ?? []
    } catch {
      if (!disposed && revision === spaceRevision) driveSpaces.value = []
    }
    if (revision === spaceRevision && !sharedDriveAvailable.value) selectedDriveSpace.value = 'personal'
  }
  // 头像/昵称更新不触发空间清空；身份或功能变化才重新裁决。
  watch([() => currentUser.value.userId, () => currentUser.value.loggedIn,
    () => currentUser.value.features?.drive, () => currentUser.value.features?.shared],
  () => { driveSpaces.value = []; void refreshDriveSpaces() })
  const loggingIn = ref(false)
  // 在线升级：启动自动检查（Go 侧 24h 节流）发现新版本时记录版本号，
  // 设置入口据此打红点；用户检查/下载后由设置页自身状态接管。
  const updateAvailable = ref('')
  let timer: number | undefined
  let disposed = false
  let errorSource: ErrorSource = ''
  let loginRevision = 0
  let authRevision = 0
  let snapshotRevision = 0
  let refreshing: Promise<boolean> | null = null

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

  const refresh = (): Promise<boolean> => {
    if (inactive()) return Promise.resolve(false)
    if (refreshing) return refreshing
    const revision = snapshotRevision
    refreshing = Promise.resolve().then(() => core.snapshot()).then(next => {
      if (inactive()) return false
      // 轮询开始后收到的实时任务更近，旧快照不能把完成状态退回运行中。
      if (revision === snapshotRevision) {
        snapshot.value = { ...next, tasks: retainTaskRows(snapshot.value.tasks, next.tasks ?? []) }
      }
      if (errorSource === 'refresh') clearError()
      return true
    }).catch(value => {
      reportError('刷新状态', value)
      if (!inactive() && revision === snapshotRevision) setError('refresh', value)
      return false
    }).finally(() => {
      refreshing = null
      if (!disposed) loading.value = false
    })
    return refreshing
  }

  // 换号/撤权立即清掉已经画出的账号历史，迟到快照不能重新填回。
  watch([() => currentUser.value.userId || '', () => currentUser.value.loggedIn, () => currentUser.value.features?.drive === true], () => {
    snapshotRevision++
    snapshot.value.tasks = snapshot.value.tasks.filter(deviceTask)
    const current = snapshotRevision
    void (refreshing ?? Promise.resolve()).then(() => {
      if (!inactive() && current === snapshotRevision) void refresh()
    })
  }, { flush: 'sync' })

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

  const { confirm } = useConfirm()

  const shutdown = async () => {
    if (inactive()) return
    const confirmed = await confirm({
      title: '退出 EasyShare',
      message: '确定退出 EasyShare 的全部后台服务吗？正在进行的传输将会停止。',
      confirmText: '退出',
      danger: true,
    })
    if (!confirmed) return

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
    if (!document.hidden) {
      // 窗口恢复：立即刷新一次补齐隐藏期间可能遗漏的状态
      void refresh()
      void syncAuth()
    }
  }

  const handleFilesDropped = async (_x: number, _y: number, paths: string[]) => {
    if (inactive() || !paths || !paths.length) return
    // 网盘页：拖入直接上传到云端
    if (activeView.value === 'cloud') {
      try {
        await core.cloudUploadPaths(paths)
        clearError()
      } catch (value) {
        reportError('上传到网盘', value)
        if (!inactive()) setError('operation', value)
      }
      return
    }
    // 其他页面：弹设备选择浮层走局域网发送
    try {
      const result = await core.processDroppedFiles(paths)
      if (inactive()) return
      if (!result.files.length && !result.dirs.length) return
      droppedFiles.value = result.files
      droppedDirs.value = result.dirs
    } catch (value) {
      reportError('处理拖入文件', value)
    }
  }

  const cancelDrop = () => {
    droppedFiles.value = []
    droppedDirs.value = []
    dropSending.value = false
  }

  const sendDropped = async (peerId: string) => {
    if (inactive() || dropSending.value) return
    const items = [...droppedDirs.value, ...droppedFiles.value]
    if (!items.length) return
    dropSending.value = true
    try {
      if (items.length === 1) {
        await core.send(peerId, items[0])
      } else {
        await core.sendBatch(peerId, items)
      }
      clearError()
      await refresh()
    } catch (value) {
      reportError('发送拖入文件', value)
      if (!inactive()) setError('operation', value)
    } finally {
      if (!inactive()) {
        dropSending.value = false
        cancelDrop()
      }
    }
  }

  // --- 实时事件处理 ---
  // Go 桌面端通过 WebSocket 订阅 Core 事件流，转发为 Wails "core-event"。
  // 前端按 type 分发：transfer.updated 原地更新任务，其余触发全量刷新。
  const handleCoreEvent = (raw: string) => {
    if (inactive()) return
    try {
      const event = JSON.parse(raw) as { type: string; data: unknown }
      switch (event.type) {
        case 'transfer.updated': {
          const task = event.data as TransferTask
          if (!deviceTask(task) && (!currentUser.value.loggedIn || !currentUser.value.features?.drive)) break
          snapshotRevision++
          const index = snapshot.value.tasks.findIndex(t => t.id === task.id)
          if (index >= 0) {
            snapshot.value.tasks[index] = task
          } else {
            snapshot.value.tasks = [task, ...snapshot.value.tasks]
          }
          break
        }
        case 'status.changed':
        case 'drive.status.changed':
          void refresh()
          break
        case 'error': {
          const errData = event.data as { message?: string }
          if (errData?.message) setError('operation', errData.message)
          break
        }
        default:
          // peer.changed 等未知事件：全量刷新保证一致性
          void refresh()
          break
      }
    } catch {
      // 解析失败忽略，等下次轮询兜底
    }
  }

  // 启动自动检查发现新版本（Go 侧 emit，24h 节流）
  const handleUpdateAvailable = (data: { latestVersion?: string }) => {
    if (inactive()) return
    updateAvailable.value = data?.latestVersion ?? ''
  }

  // 后台会话恢复可能晚于前端首次查询，按事件更新且不让早到请求的迟到结果覆盖。
  const handleAuthRestored = (user: AuthUser) => {
    if (disposed || !user) return
    authRevision++
    currentUser.value = user
  }

  // 启动恢复的账号令牌被控制面判定失效：清空登录态，App 自动回到登录页。
  const handleAuthExpired = () => {
    if (disposed) return
    authRevision++
    currentUser.value = { ...emptyUser }
    error.value = '登录已过期，请重新登录'
    errorSource = 'operation'
  }

  // 拉取当前登录态。Go 侧 Startup 要等 EnsureCore 返回后才恢复落盘会话，
  // 冷启动时前端可能先挂载——首次调用拿到的是未登录，必须等 Core 就绪后再取一次，
  // 否则有效会话会被显示成登录页、逼用户重登。
  const syncAuth = () => {
    const revision = ++authRevision
    return Promise.resolve(core.currentUser())
      .then(user => { if (!disposed && user && authRevision === revision) currentUser.value = user })
      .catch(value => {
        if (disposed || authRevision !== revision) return
        // 身份可保留，无法确认的管理权/功能权必须收起，恢复窗口时可重新核验。
        currentUser.value = { ...currentUser.value, isAdmin: false, adminScope: '', features: { ...emptyUser.features! } }
        setError('operation', '暂时无法确认访问权限，请恢复连接后重试')
        reportError('刷新访问权限', value)
      })
  }

  // 桌面端后台刷新了账号态（资料修改、启动校验拿到新权限）：重新取一次。
  const handleUserUpdated = () => {
    void syncAuth().then(() => { if (!disposed) void refreshDriveSpaces() })
  }

  onMounted(() => {
    disposed = false
    document.addEventListener('visibilitychange', handleVisibility)
    // useDropTarget=false: the whole window accepts drops, no CSS marker needed.
    OnFileDrop(handleFilesDropped, false)
    // 订阅 Go 端转发的 Core 实时事件
    EventsOn('core-event', handleCoreEvent)
    // 订阅升级可用通知（update:available）
    EventsOn('update:available', handleUpdateAvailable)
    EventsOn('auth:expired', handleAuthExpired)
    EventsOn('auth:user-updated', handleUserUpdated)
    EventsOn('auth:restored', handleAuthRestored)

    void core.logDirectory()
      .then(path => { if (!disposed && path) logDirectory.value = path })
      .catch(value => reportError('resolve log directory', value))

    // 拉取当前登录态（进程内若已登录，重开窗口后恢复头像）；revision 守卫防止
    // 早到请求的迟到结果覆盖 auth:restored 事件恢复的登录态。
    const initialAuthRevision = authRevision
    void Promise.resolve(core.currentUser())
      .then(user => { if (!disposed && user && authRevision === initialAuthRevision) currentUser.value = user })
      .catch(() => undefined)

    void initialize().finally(() => {
      void syncAuth()
      if (!inactive() && timer === undefined) {
        // 轮询降为 5s fallback：实时事件覆盖快速路径
        startPolling(5000)
      }
    })
  })
  onBeforeUnmount(() => {
    disposed = true
    stopPolling()
    EventsOff('core-event')
    EventsOff('update:available')
    EventsOff('auth:expired')
    EventsOff('auth:user-updated')
    EventsOff('auth:restored')
    document.removeEventListener('visibilitychange', handleVisibility)
    OnFileDropOff()
  })

  const activeTasks = computed(() =>
    snapshot.value.tasks.filter(t =>
      ['pending', 'accepted', 'running', 'queued', 'paused', 'waiting_network'].includes(t.status)))

  return {
    snapshot,
    loading,
    error,
    shuttingDown,
    stopped,
    logDirectory,
    droppedFiles,
    droppedDirs,
    dropSending,
    activeView,
    activeTasks,
    currentUser,
    driveSpaces,
    selectedDriveSpace,
    sharedDriveAvailable,
    loggingIn,
    updateAvailable,
    login: async (username: string, password: string): Promise<boolean> => {
      if (disposed || loggingIn.value) return false
      const attempt = ++loginRevision
      const revision = ++authRevision
      loggingIn.value = true
      try {
        const user = await core.login(username, password)
        if (disposed || authRevision !== revision) return false
        currentUser.value = user
        clearError()
        return true
      } catch (value) {
        if (!disposed && authRevision === revision) setError('operation', value)
        return false
      } finally {
        if (!disposed && loginRevision === attempt) loggingIn.value = false
      }
    },
    logout: async () => {
      authRevision++
      loginRevision++
      loggingIn.value = false
      // 先撤去界面权限与旧数据；迟到的退出响应不能覆盖后来登录的账号。
      currentUser.value = { ...emptyUser }
      driveSpaces.value = []
      spaceRevision++
      try { await core.logout() } catch { /* 本地清除即可 */ }
    },
    openAdminConsole: async () => {
      try {
        await core.openAdminConsole()
      } catch (value) {
        error.value = errorMessage(value)
        errorSource = 'operation'
      }
    },
    clearError,
    refresh,
    sendDropped,
    cancelDrop,
    send: async (peerId: string) => {
      if (inactive()) return
      const paths = await core.selectFiles()
      if (paths && paths.length) {
        if (paths.length === 1) {
          await act('发送文件', () => core.send(peerId, paths[0]))
        } else {
          await act('发送文件', () => core.sendBatch(peerId, paths))
        }
      }
    },
    accept: (id: string) => act('接受传输', () => core.accept(id)),
    acceptAs: (id: string) => act('另存传输', () => core.acceptAs(id)),
    reject: (id: string) => act('拒绝传输', () => core.reject(id)),
    clearHistory: () => act('清除记录', core.clearHistory),
    deleteTask: (id: string) => act('删除记录', () => core.deleteTask(id)),
    cloudUpload: async (targetDir = '') => {
      if (inactive()) return
      try {
        await core.cloudUpload(targetDir)
        clearError()
      } catch (value) {
        reportError('上传文件', value)
        if (!inactive()) setError('operation', value)
      }
    },
    cloudUploadFolder: async (targetDir = '') => {
      if (inactive()) return
      try {
        await core.cloudUploadFolder(targetDir)
        clearError()
      } catch (value) {
        reportError('上传文件夹', value)
        if (!inactive()) setError('operation', value)
      }
    },
    cloudDownload: (key: string) => act('下载文件', () => core.cloudDownload(key)),
    cloudDownloadAs: (key: string) => act('另存云端文件', () => core.cloudDownloadAs(key)),
    cloudDelete: (key: string) => act('删除文件', () => core.cloudDelete(key)),
    cloudShare: (key: string, hours: number) => core.cloudShare(key, hours),
    startDrive: () => act('启动 WebDAV', core.startDrive),
    stopDrive: () => act('停止 WebDAV', core.stopDrive),
    shutdown,
  }
}
