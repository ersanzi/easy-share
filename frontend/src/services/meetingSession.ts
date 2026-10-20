// 会议录音会话：模块级单例，跨页面存活。
//
// 之前录音状态挂在 MeetingsPanel 组件里，切到别的页组件被卸载时 onBeforeUnmount
// 会 abort() 掉 MediaRecorder——用户只是去看了一眼网盘，录音就没了。
// 这里把「采集、推流、计时、实时字幕、会中快检」全部提到组件生命周期之外：
// 面板只负责展示与操作，顶部栏再用一个胶囊提示仍在录音。
//
// 2026-10-20 第二次修正（用户：切页就中止 / 不能流式展示 / 发言人不支持更新）：
//  1. **推流改走 Wails 绑定**：fetch 属于渲染进程的文档上下文，切页/卸载时容易被打断；
//     Wails 绑定调用不受组件生命周期影响，切到任何页面都照常出字。
//  2. **推流不再「一断就收摊」**：单次调用超时或失败时撤销该请求、从服务端会话快照续上，
//     连续多次失败才降级为轮询兜底（`liveState=degraded`），录音本身始终不受影响。
//  3. **会中快检**：每次落新行后取最近几行让服务端核对数字/期限口径（`liveChecks`），
//     这是「边说边能用」的关键——整场纪要要等录音结束，会中只有快检能救场。
import { ref } from 'vue'
import { core } from './core'
import { MeetingRecorder, STREAM_CALL_TIMEOUT_MS } from './recorder'
import type { RecordingSource } from './recorder'
import type { MeetingLiveCheck, MeetingLiveLine, MeetingLiveView } from '../types/core'

// idle=没开始；warming=等流式模型就绪；live=正在出字；
// degraded=推流断了，改用轮询兜底（字仍在出，只是慢一拍）；unavailable=实时不可用（只有录音）
export type LiveState = 'idle' | 'warming' | 'live' | 'degraded' | 'unavailable'

// 推流队列：600ms 一片；网络卡顿时丢最旧，宁缺字也不无限积压
const STREAM_QUEUE_MAX = 20
// 连续失败到这个次数才降级为轮询兜底（偶发抖动只重连，不打扰用户）
const STREAM_MAX_FAILURES = 3
// 轮询兜底的间隔（服务端只回快照，很轻）
const POLL_INTERVAL_MS = 3000
// 会中快检的最小间隔：新的行多了才问，且 20 秒内最多问一次（LLM 不是免费的）
const CHECK_MIN_LINES = 6
const CHECK_MIN_INTERVAL_MS = 20000
const CHECK_LIST_MAX = 20

const recorder = new MeetingRecorder()

const recording = ref(false)
const uploading = ref(false)
const elapsed = ref(0)
const level = ref(0)
const title = ref('')
const templateId = ref('meeting-minutes')
const audioSource = ref<RecordingSource>('mic')

const liveSessionId = ref('')
const liveLines = ref<MeetingLiveLine[]>([])
const liveCurrent = ref('')
const livePoints = ref<string[]>([])
const liveState = ref<LiveState>('idle')
const liveError = ref('')
// 最后一次「收到新字幕」的墙钟时刻；面板据此显示「N 秒前出过字」，区分「静音等待」与「真的卡了」
const liveUpdatedAt = ref(0)
// 服务端是否已经加载好流式模型（预热完成前第一条要等几十秒，UI 要说得清楚）
const modelReady = ref(false)
const checks = ref<MeetingLiveCheck[]>([])
const checking = ref(false)

const notice = ref('')
const noticeKind = ref<'info' | 'error'>('info')
// 整段上传受理成功后的会议 id：面板据此打开详情；面板不在时留在单例里，回来再消费
const finishedMeetingId = ref('')

let timerHandle: number | null = null
let noticeTimer: number | null = null
let pollHandle: number | null = null
let warmPollHandle: number | null = null

let streamQueue: Uint8Array[] = []
let streamPumping = false
// 连续推流失败计数：成功一片即清零，累计到 STREAM_MAX_FAILURES 才降级为轮询兜底
let streamFailures = 0
// 「已判死」标记：降级后不再尝试推流，直到 stop/reset
let streamBroken = false
let streamInflight: Promise<MeetingLiveView> | null = null
let pointsBusy = false
let lastPointsLines = 0
let lastCheckAt = 0
let lastCheckLines = 0

const errorText = (e: unknown) => (e instanceof Error ? e.message : String(e))

const notify = (message: string, kind: 'info' | 'error' = 'info') => {
  notice.value = message
  noticeKind.value = kind
  if (noticeTimer !== null) window.clearTimeout(noticeTimer)
  if (kind === 'info') {
    noticeTimer = window.setTimeout(() => { if (notice.value === message) notice.value = '' }, 6000)
  }
}

const dismissNotice = () => {
  if (noticeTimer !== null) {
    window.clearTimeout(noticeTimer)
    noticeTimer = null
  }
  notice.value = ''
}

const toBase64 = (bytes: Uint8Array) => {
  let binary = ''
  const step = 0x8000
  for (let start = 0; start < bytes.length; start += step) {
    binary += String.fromCharCode(...bytes.subarray(start, start + step))
  }
  return btoa(binary)
}

// 超时包一层：Wails 绑定本身没有取消吞，用 Promise.race 把「卡住的请求」从队列里摘掉
const withTimeout = <T>(work: Promise<T>, ms: number): Promise<T> =>
  new Promise<T>((resolve, reject) => {
    const handle = window.setTimeout(() => reject(new Error(`实时转写响应超时（${Math.round(ms / 1000)} 秒）`)), ms)
    work.then(
      value => { window.clearTimeout(handle); resolve(value) },
      error => { window.clearTimeout(handle); reject(error) },
    )
  })

const applyView = (view: { lines?: MeetingLiveLine[]; current?: string }) => {
  const lines = view.lines || []
  if (lines.length !== liveLines.value.length || (view.current || '') !== liveCurrent.value) {
    liveUpdatedAt.value = Date.now()
  }
  liveLines.value = lines
  liveCurrent.value = view.current || ''
  // 只有「正在推流」才升到 live；warming 由预热状态机负责，degraded 要等下一片真的推成功才恢复
  if (liveState.value === 'warming') liveState.value = 'live'
  maybeScheduleCheck()
}

// 从服务端会话快照续上（推流失败、切页回来、重连时都用这条）
const resync = async () => {
  const id = liveSessionId.value
  if (!id) return
  try {
    const snapshot = await core.meetingLiveSession(id)
    applyView({ lines: snapshot.lines, current: snapshot.current })
    modelReady.value = true
    // 续上只说明「服务端还在」，不重置失败计数——连续失败要能累积到降级阈值
  } catch {
    // 续不上就维持现状：轮询会在下一次再试
  }
}

const refreshLivePoints = async () => {
  const id = liveSessionId.value
  if (!id || pointsBusy) return
  pointsBusy = true
  try {
    livePoints.value = await core.meetingLivePoints(id)
  } catch {
    // 会中要点是增值层：失败下一轮再试，不打断录音与字幕
  } finally {
    pointsBusy = false
  }
}

const maybeScheduleCheck = () => {
  if (!recording.value) return
  const id = liveSessionId.value
  if (!id || checking.value) return
  if (liveLines.value.length < CHECK_MIN_LINES) return
  if (liveLines.value.length - lastCheckLines < 3) return
  if (Date.now() - lastCheckAt < CHECK_MIN_INTERVAL_MS) return
  void runCheck()
}

const runCheck = async () => {
  const id = liveSessionId.value
  if (!id || checking.value) return
  checking.value = true
  lastCheckAt = Date.now()
  lastCheckLines = liveLines.value.length
  try {
    const result = await core.meetingLiveCheck(id)
    // 只保留最近若干条，且同一条不要重复堆叠
    const next = [result, ...checks.value.filter(item => item.checked_at !== result.checked_at)]
    checks.value = next.slice(0, CHECK_LIST_MAX)
  } catch (e) {
    liveError.value = `会中快检失败：${errorText(e)}`
  } finally {
    checking.value = false
  }
}

// 面板上「手动再检一次」按钮（不经过节流）
const checkNow = async () => {
  lastCheckAt = 0
  lastCheckLines = 0
  await runCheck()
}

const pumpStream = async () => {
  if (streamPumping) return
  streamPumping = true
  try {
    while (streamQueue.length && liveSessionId.value) {
      const piece = streamQueue.shift()
      if (!piece) break
      try {
        streamInflight = core.meetingStreamChunk(liveSessionId.value, toBase64(piece))
        const view = await withTimeout(streamInflight, STREAM_CALL_TIMEOUT_MS)
        streamInflight = null
        streamFailures = 0
        // 降级后不再尝试推流，所以这里没有「从 degraded 回到 live」的路径：
        // 真要恢复得重连会话（新的一场录音），避免半通不通时来回闪状态
        modelReady.value = true
        applyView(view)
        if (view.lines.length - lastPointsLines >= 8) {
          lastPointsLines = view.lines.length
          void refreshLivePoints()
        }
      } catch (e) {
        streamInflight = null
        streamFailures += 1
        liveError.value = `实时上屏重试中：${errorText(e)}`
        if (streamFailures >= STREAM_MAX_FAILURES) {
          // 不丢录音、不清空已有字幕：降级为轮询兜底，服务端还在继续识别
          streamBroken = true
          liveState.value = 'degraded'
          streamQueue = []
          startPolling()
          await resync()
          return
        }
        // 偶发抖动：先续上快照（拿到卡住期间已出屏的字），再继续推后面的片
        await resync()
      }
    }
  } finally {
    streamPumping = false
  }
}

const enqueueChunk = (pcm: Uint8Array) => {
  // 已降级：不再推流（服务端会话还在，轮询会继续把字续到屏幕上）
  if (streamBroken) return
  streamQueue.push(pcm)
  if (streamQueue.length > STREAM_QUEUE_MAX) streamQueue.splice(0, streamQueue.length - STREAM_QUEUE_MAX)
  void pumpStream()
}

const startPolling = () => {
  if (pollHandle !== null) return
  pollHandle = window.setInterval(() => { void resync() }, POLL_INTERVAL_MS)
}

const stopPolling = () => {
  if (pollHandle !== null) {
    window.clearInterval(pollHandle)
    pollHandle = null
  }
}

// 模型冷加载：本机实测首次约 14~70 秒（取决于磁盘/内存），进页面就预热并等到 ready 再开始录音
const warmModel = async (wait: boolean) => {
  try {
    await core.meetingStreamWarm()
  } catch (e) {
    modelReady.value = false
    liveError.value = `实时转写预热失败：${errorText(e)}`
    return
  }
  if (!wait) return
  // 预热接口是异步受理的：等服务端真的加载完再开录，避免第一句话等一分钟
  if (warmPollHandle !== null) window.clearInterval(warmPollHandle)
  warmPollHandle = window.setInterval(async () => {
    const id = liveSessionId.value
    if (!id) return
    try {
      const snapshot = await core.meetingLiveSession(id)
      if (snapshot.recording !== false) {
        modelReady.value = true
        if (warmPollHandle !== null) {
          window.clearInterval(warmPollHandle)
          warmPollHandle = null
        }
        if (liveState.value === 'warming') liveState.value = 'live'
      }
    } catch {
      // 继续等
    }
  }, 2000)
}

const resetLive = () => {
  liveSessionId.value = ''
  liveLines.value = []
  liveCurrent.value = ''
  livePoints.value = []
  liveState.value = 'idle'
  liveError.value = ''
  liveUpdatedAt.value = 0
  checks.value = []
  streamQueue = []
  lastPointsLines = 0
  lastCheckAt = 0
  lastCheckLines = 0
  streamFailures = 0
  streamBroken = false
  stopPolling()
}

const stopTimer = () => {
  if (timerHandle !== null) {
    window.clearInterval(timerHandle)
    timerHandle = null
  }
}

const startTimer = () => {
  stopTimer()
  timerHandle = window.setInterval(() => { elapsed.value = recorder.elapsedSeconds() }, 500)
}

const fallbackTitle = () => new Date().toLocaleString('zh-CN', { hour12: false }) + ' 的会议'

const start = async () => {
  if (recording.value || uploading.value) return
  dismissNotice()
  resetLive()
  liveState.value = 'warming'
  // 先落一条 recording 记录当推流会话；录完的整段上传复用同一条，不落两条听记
  try {
    const session = await core.meetingStreamOpen(title.value.trim() || fallbackTitle(), templateId.value)
    liveSessionId.value = session.id
    // 开录即预热：热词/模型冷加载和麦克风权限申请并行，谁也不等谁
    void warmModel(true)
  } catch (e) {
    liveState.value = 'unavailable'
    liveError.value = `实时上屏不可用：${errorText(e)}`
  }
  try {
    await recorder.start(
      audioSource.value,
      value => { level.value = value },
      liveSessionId.value ? enqueueChunk : undefined,
    )
    recording.value = true
    elapsed.value = 0
    startTimer()
  } catch (e) {
    // 采集失败：把刚建的会话记录收掉，别留孤儿条目
    if (liveSessionId.value) await core.meetingRemove(liveSessionId.value).catch(() => {})
    resetLive()
    notify(errorText(e), 'error')
  }
}

const stop = async () => {
  if (!recording.value) return
  stopTimer()
  if (warmPollHandle !== null) {
    window.clearInterval(warmPollHandle)
    warmPollHandle = null
  }
  level.value = 0
  recording.value = false
  uploading.value = true
  const sessionId = liveSessionId.value
  try {
    const { blob, durationSeconds } = await recorder.stop()
    if (durationSeconds < 1) {
      if (sessionId) await core.meetingRemove(sessionId).catch(() => {})
      resetLive()
      notify('录音太短（不足 1 秒），已丢弃', 'error')
      return
    }
    // 收尾：先把队列里剩下的片推完，再让服务端冲尾包（否则最后几个字会丢）
    if (sessionId) {
      try {
        await pumpStream()
        const final = await withTimeout(core.meetingStreamStop(sessionId), STREAM_CALL_TIMEOUT_MS * 2)
        liveLines.value = final.lines
        liveCurrent.value = final.current || ''
      } catch {
        // 收尾包失败不影响整段重跑
      }
    }
    liveSessionId.value = ''
    streamQueue = []
    stopPolling()
    const created = await core.meetingUpload(blob, title.value.trim() || fallbackTitle(), templateId.value, sessionId)
    title.value = ''
    finishedMeetingId.value = created.id
    notify('录音已上传，正在生成完整转写…')
  } catch (e) {
    notify(errorText(e), 'error')
  } finally {
    uploading.value = false
  }
}

const cancel = async () => {
  if (!recording.value && !uploading.value) return
  stopTimer()
  if (warmPollHandle !== null) {
    window.clearInterval(warmPollHandle)
    warmPollHandle = null
  }
  recording.value = false
  uploading.value = false
  level.value = 0
  recorder.abort()
  const sessionId = liveSessionId.value
  resetLive()
  if (sessionId) await core.meetingRemove(sessionId).catch(() => {})
  notify('已取消本次录音')
}

// 用户从共享条点「停止共享」→ 音频轨结束，自动收尾并转写
recorder.onAudioEnded = () => {
  if (recording.value) void stop()
}

const takeFinished = () => {
  const id = finishedMeetingId.value
  finishedMeetingId.value = ''
  return id
}

export const meetingSession = {
  recording,
  uploading,
  elapsed,
  level,
  title,
  templateId,
  audioSource,
  liveSessionId,
  liveLines,
  liveCurrent,
  livePoints,
  liveState,
  liveError,
  liveUpdatedAt,
  modelReady,
  checks,
  checking,
  notice,
  noticeKind,
  finishedMeetingId,
  start,
  stop,
  cancel,
  notify,
  dismissNotice,
  resetLive,
  takeFinished,
  resync,
  checkNow,
  warmModel,
}
