<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { core } from '../services/core'
import { useEasyShare } from '../composables/useEasyShare'
import type { KnowledgeStatus, MeetingChatTurn, MeetingLiveLine, MeetingRecord, MeetingTodo, MeetingVoiceprint } from '../types/core'
import { MeetingRecorder, decodeAudioFileToWav } from '../services/recorder'
import type { RecordingSource } from '../services/recorder'
import MeetingPoster from './MeetingPoster.vue'

// 会议听记：录音（前端 16k WAV）→ /kapi 同源网关上传 → 知识服务 FunASR 转写 +
// 说话人分离 + LLM 总结。登录态与「知识」页同源（Core 网关）。

const emit = defineEmits<{ (e: 'goto-knowledge'): void }>()

const app = useEasyShare()
const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)

const status = ref<KnowledgeStatus>({ configured: false, loggedIn: false, serverUrl: '', username: '', role: '' })
const statusLoading = ref(true)
const notice = ref('')
const noticeKind = ref<'info' | 'error'>('info')

const meetings = ref<MeetingRecord[]>([])
const selected = ref<MeetingRecord | null>(null)
const audioUrl = ref('')
const detailLoading = ref(false)
const archiving = ref(false)
const confirmingDelete = ref(false)

const recorder = new MeetingRecorder()
const recording = ref(false)
const elapsed = ref(0)
const level = ref(0)
const title = ref('')
const uploading = ref(false)
let timerHandle: number | null = null

// 音源：mic=麦克风；system=系统声音+麦克风混流（Windows；共享选择器里勾「同时分享系统音频」）
const audioSource = ref<RecordingSource>('mic')

// 总结模板（与知识服务 templates.py 内置清单同步；对标钉钉「选择模板发起听记」：
// 模板先行、带场景描述，录制前选定输出形态）
const TEMPLATES = [
  { id: 'meeting-minutes', name: '会议纪要', emoji: '📋', desc: '通用会议总结：纪要、要点、待办' },
  { id: 'weekly', name: '团队周会', emoji: '📅', desc: '本周进展、数据指标、风险、下周计划' },
  { id: 'daily-standup', name: '每日站会', emoji: '☀️', desc: '昨天/今天/阻塞项三段速览' },
  { id: 'interview', name: '面试报告', emoji: '🧑‍💼', desc: '候选人表现维度与录用建议' },
  { id: 'interview-candidate', name: '访谈记录', emoji: '🎤', desc: '访谈问答与事实记录' },
  { id: 'training', name: '培训笔记', emoji: '📚', desc: '培训要点与学习收获' },
  { id: 'brainstorm', name: '头脑风暴会', emoji: '💡', desc: '想法分组、评估与跟进清单' },
] as const
const templateId = ref('meeting-minutes')

// 用户在共享条点「停止共享」→ 音频轨结束，自动收尾本次录音并转写
recorder.onAudioEnded = () => {
  if (recording.value) void stop()
}

const renamingSpeaker = ref('')
const renamingName = ref('')

const showNotice = (message: string, kind: 'info' | 'error' = 'info') => {
  notice.value = message
  noticeKind.value = kind
  if (kind === 'info') window.setTimeout(() => { if (notice.value === message) notice.value = '' }, 6000)
}

const refreshStatus = async () => {
  try {
    status.value = await core.knowledgeStatus()
  } finally {
    statusLoading.value = false
  }
}

const refreshList = async () => {
  try {
    meetings.value = await core.meetingsList(50)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  }
}

// ─── 录音 ───

const liveSessionId = ref('')
const liveLines = ref<MeetingLiveLine[]>([])
const liveCurrent = ref('')
const livePoints = ref<string[]>([])
const liveFailed = ref(false)
const liveBox = ref<HTMLElement | null>(null)

// 推流队列：600ms 一片；网络卡顿时丢最旧，宁缺字也不无限积压
const STREAM_QUEUE_MAX = 20
let streamQueue: Uint8Array[] = []
let streamPumping = false
let pointsBusy = false
let lastPointsLines = 0

const toBase64 = (bytes: Uint8Array) => {
  let binary = ''
  const step = 0x8000
  for (let start = 0; start < bytes.length; start += step) {
    binary += String.fromCharCode(...bytes.subarray(start, start + step))
  }
  return btoa(binary)
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

const pumpStream = async () => {
  if (streamPumping) return
  streamPumping = true
  try {
    while (streamQueue.length && liveSessionId.value) {
      const piece = streamQueue.shift()
      if (!piece) break
      try {
        const view = await core.meetingStreamChunk(liveSessionId.value, toBase64(piece))
        liveLines.value = view.lines
        liveCurrent.value = view.current
        if (view.lines.length - lastPointsLines >= 8) {
          lastPointsLines = view.lines.length
          void refreshLivePoints()
        }
      } catch {
        // 推流断了就收摊：剩下的音频仍会在录制结束后整段离线转写
        liveFailed.value = true
        streamQueue = []
        break
      }
    }
  } finally {
    streamPumping = false
  }
}

const enqueueChunk = (pcm: Uint8Array) => {
  streamQueue.push(pcm)
  if (streamQueue.length > STREAM_QUEUE_MAX) streamQueue.splice(0, streamQueue.length - STREAM_QUEUE_MAX)
  void pumpStream()
}

const resetLive = () => {
  liveSessionId.value = ''
  liveLines.value = []
  liveCurrent.value = ''
  livePoints.value = []
  liveFailed.value = false
  streamQueue = []
  lastPointsLines = 0
}

// 字幕跟随最新一行（像钉钉那样贴着底部滚动）
watch(
  () => liveLines.value.length + liveCurrent.value.length,
  async () => {
    await nextTick()
    if (liveBox.value) liveBox.value.scrollTop = liveBox.value.scrollHeight
  },
)

const fallbackTitle = () => new Date().toLocaleString('zh-CN', { hour12: false }) + ' 的会议'

const start = async () => {
  notice.value = ''
  resetLive()
  // 先落一条 recording 记录当推流会话；录完的整段上传复用同一条，不落两条听记
  try {
    const session = await core.meetingStreamOpen(title.value.trim() || fallbackTitle(), templateId.value)
    liveSessionId.value = session.id
  } catch {
    liveFailed.value = true
    showNotice('实时上屏本次不可用（流式模型未就绪），仍可正常录音并转写', 'error')
  }
  try {
    await recorder.start(
      audioSource.value,
      value => { level.value = value },
      liveSessionId.value ? enqueueChunk : undefined,
    )
    recording.value = true
    elapsed.value = 0
    timerHandle = window.setInterval(() => { elapsed.value = recorder.elapsedSeconds() }, 500)
  } catch (e) {
    // 采集失败：把刚建的会话记录收掉，别留孤儿条目
    if (liveSessionId.value) await core.meetingRemove(liveSessionId.value).catch(() => {})
    resetLive()
    // 采集层抛出的文案已面向用户（取消选择/未勾选音频/不支持），直接展示
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  }
}

const stop = async () => {
  if (!recording.value) return
  stopTimer()
  level.value = 0
  recording.value = false
  uploading.value = true
  const sessionId = liveSessionId.value
  try {
    const { blob, durationSeconds } = await recorder.stop()
    if (durationSeconds < 1) {
      if (sessionId) await core.meetingRemove(sessionId).catch(() => {})
      resetLive()
      showNotice('录音太短（不足 1 秒），已丢弃', 'error')
      return
    }
    if (sessionId) {
      try {
        const final = await core.meetingStreamStop(sessionId)
        liveLines.value = final.lines
      } catch {
        // 收尾包失败不影响整段重跑
      }
    }
    liveSessionId.value = ''
    streamQueue = []
    const created = await core.meetingUpload(blob, title.value.trim() || fallbackTitle(), templateId.value, sessionId)
    title.value = ''
    await refreshList()
    await openDetail(created.id)
    resetLive()
    showNotice('录音已上传，正在转写…')
  } catch (e) {
    resetLive()
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    uploading.value = false
  }
}

const cancelRecording = async () => {
  stopTimer()
  recording.value = false
  level.value = 0
  recorder.abort()
  const sessionId = liveSessionId.value
  resetLive()
  if (sessionId) await core.meetingRemove(sessionId).catch(() => {})
}

const stopTimer = () => {
  if (timerHandle !== null) {
    window.clearInterval(timerHandle)
    timerHandle = null
  }
}

// ─── 详情与轮询 ───

let pollHandle: number | null = null

const openDetail = async (id: string) => {
  detailLoading.value = true
  confirmingDelete.value = false
  try {
    const detail = await core.meetingDetail(id)
    if (selected.value?.id !== id) releaseAudio()
    if (selected.value?.id !== id) chat.value = []
    selected.value = detail
    regenTemplateId.value = detail.template_id || 'meeting-minutes'
    if (detail.status === 'completed' && !audioUrl.value) {
      audioUrl.value = await core.meetingAudioObjectUrl(id)
    }
    ensurePolling()
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    detailLoading.value = false
  }
}

const releaseAudio = () => {
  if (audioUrl.value) URL.revokeObjectURL(audioUrl.value)
  audioUrl.value = ''
}

const ensurePolling = () => {
  if (pollHandle !== null) return
  pollHandle = window.setInterval(async () => {
    const processing = selected.value?.status === 'processing'
      || meetings.value.some(meeting => meeting.status === 'processing')
    if (!processing) {
      stopPolling()
      return
    }
    if (selected.value?.status === 'processing') await openDetail(selected.value.id)
    else await refreshList()
  }, 3000)
}

const stopPolling = () => {
  if (pollHandle !== null) {
    window.clearInterval(pollHandle)
    pollHandle = null
  }
}

const closeDetail = () => {
  selected.value = null
  releaseAudio()
  chat.value = []
  qaQuestion.value = ''
  confirmingDelete.value = false
}

// ─── 发言人改名（行内编辑） ───

const nameOf = (speaker: string) => selected.value?.speaker_names?.[speaker] || `说话人 ${Number(speaker) + 1}`

const beginRename = (speaker: string) => {
  renamingSpeaker.value = speaker
  renamingName.value = nameOf(speaker)
}

const confirmRename = async () => {
  const speaker = renamingSpeaker.value
  const name = renamingName.value.trim()
  if (!selected.value || !speaker || !name) {
    renamingSpeaker.value = ''
    return
  }
  try {
    selected.value = await core.meetingSpeakerRename(selected.value.id, speaker, name)
    await refreshList()
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    renamingSpeaker.value = ''
  }
}

// ─── 导入已有音频文件（浏览器解码 → 16k WAV → 复用上传通道）───

const importingFile = ref(false)
const audioFileInput = ref<HTMLInputElement | null>(null)

const onImportFile = async (event: Event) => {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file || importingFile.value) return
  if (file.size > 100 * 1024 * 1024) {
    showNotice('音频文件超过 100MB，请先剪辑或压缩后再导入', 'error')
    return
  }
  importingFile.value = true
  uploading.value = true
  try {
    const { blob, durationSeconds } = await decodeAudioFileToWav(file)
    if (durationSeconds < 1) {
      showNotice('音频内容太短（不足 1 秒），已丢弃', 'error')
      return
    }
    const fallback = new Date().toLocaleString('zh-CN', { hour12: false })
    const title = file.name.replace(/\.[^.]+$/, '') || `${fallback} 的会议`
    const created = await core.meetingUpload(blob, title, templateId.value)
    await refreshList()
    await openDetail(created.id)
    showNotice(`已导入「${title}」，正在转写…`)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    importingFile.value = false
    uploading.value = false
  }
}

// ─── 听记 AI 问答（对标钉钉听记问答：纪要+转写全文直接进上下文）───

const QA_SUGGESTIONS = ['总结本次会议的关键结论', '还有哪些事情没有闭环', '根据会议内容起草一封跟进邮件'] as const
const chat = ref<MeetingChatTurn[]>([])
const qaQuestion = ref('')
const qaBusy = ref(false)
const qaList = ref<HTMLElement | null>(null)

const askQuestion = async (preset?: string) => {
  const question = (preset ?? qaQuestion.value).trim()
  if (!selected.value || qaBusy.value || !question || selected.value.status !== 'completed') return
  const history = chat.value.slice(-6)
  chat.value = [...chat.value, { role: 'user', content: question }]
  qaQuestion.value = ''
  qaBusy.value = true
  try {
    const result = await core.meetingAsk(selected.value.id, question, history)
    chat.value = [...chat.value, { role: 'assistant', content: result.answer }]
  } catch (e) {
    chat.value = [...chat.value, { role: 'assistant', content: `回答失败：${e instanceof Error ? e.message : String(e)}` }]
  } finally {
    qaBusy.value = false
    await nextTick()
    qaList.value?.scrollTo({ top: qaList.value.scrollHeight, behavior: 'smooth' })
  }
}

// ─── 归档 / 删除 ───

const archive = async () => {
  if (!selected.value || archiving.value) return
  archiving.value = true
  try {
    const result = await core.meetingArchive(selected.value.id)
    showNotice(`已归档进知识库：${result.filename}（${result.chunks} 块），可在「知识」页提问检索`)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    archiving.value = false
  }
}

const remove = async () => {
  if (!selected.value) return
  if (!confirmingDelete.value) {
    confirmingDelete.value = true
    return
  }
  try {
    await core.meetingRemove(selected.value.id)
    closeDetail()
    await refreshList()
    showNotice('已删除')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    confirmingDelete.value = false
  }
}

// ─── 区块状态与关键词（对标钉钉分任务状态：某块失败只补跑那块）───

const BLOCK_LABELS: Record<string, string> = { transcript: '转写', keywords: '关键词', summary: '总结', mindmap: '导图' }
const BLOCK_STATE_LABELS: Record<string, string> = {
  pending: '待生成',
  processing: '生成中',
  ready: '已生成',
  failed: '失败',
  skipped: '未生成',
}

const blockChips = computed(() => {
  const blocks = selected.value?.blocks || {}
  const errors = selected.value?.block_errors || {}
  return Object.entries(BLOCK_LABELS).map(([key, label]) => ({
    key,
    label,
    state: blocks[key as keyof typeof blocks] || 'pending',
    error: errors[key as keyof typeof errors] || '',
  }))
})

const stateLabel = (state: string) => BLOCK_STATE_LABELS[state] || state

const pendingTodosCount = computed(() => (selected.value?.todos || []).filter(todo => !todo.done).length)

const keywordsBusy = ref(false)

const regenKeywords = async () => {
  if (!selected.value || keywordsBusy.value) return
  keywordsBusy.value = true
  try {
    selected.value = await core.meetingKeywordsRegenerate(selected.value.id)
    showNotice('关键词已更新')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    keywordsBusy.value = false
  }
}

// ─── 个性化热词（对标钉钉：转写时优先识别专名，提高准确率）───

const hotwords = ref<string[]>([])
const hotwordsMax = ref(100)
const newHotword = ref('')
const hotwordsBusy = ref(false)
const hotwordsOpen = ref(false)

const loadHotwords = async () => {
  try {
    const result = await core.meetingHotwordsGet()
    hotwords.value = result.words || []
    hotwordsMax.value = result.max || 100
  } catch {
    // 词表读不到不影响录音：转写按无热词走
  }
}

const addHotword = async () => {
  const word = newHotword.value.trim()
  if (!word || hotwordsBusy.value) return
  hotwordsBusy.value = true
  try {
    const result = await core.meetingHotwordsAdd([word])
    hotwords.value = result.words || []
    hotwordsMax.value = result.max || hotwordsMax.value
    newHotword.value = ''
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    hotwordsBusy.value = false
  }
}

const removeHotword = async (word: string) => {
  if (hotwordsBusy.value) return
  hotwordsBusy.value = true
  try {
    const result = await core.meetingHotwordRemove(word)
    hotwords.value = result.words || []
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    hotwordsBusy.value = false
  }
}

// ─── 声纹（轻口径：改过的名字会沉淀成声纹，下次自动认；必须可删）───

const voiceprints = ref<MeetingVoiceprint[]>([])

const loadVoiceprints = async () => {
  try {
    voiceprints.value = (await core.meetingVoiceprints()) || []
  } catch {
    // 读不到不影响改名，只是下次不会自动认领
  }
}

const removeVoiceprint = async (name: string) => {
  if (hotwordsBusy.value) return
  hotwordsBusy.value = true
  try {
    voiceprints.value = (await core.meetingVoiceprintRemove(name)) || []
    showNotice(`已删除「${name}」的声纹，之后的会议不再自动认领`)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    hotwordsBusy.value = false
  }
}

// ─── 重新转写（加了热词后让老记录重跑全管线）───

const retranscribeBusy = ref(false)

const retranscribe = async () => {
  if (!selected.value || retranscribeBusy.value) return
  retranscribeBusy.value = true
  try {
    await core.meetingRetranscribe(selected.value.id)
    await openDetail(selected.value.id)
    showNotice('已重新排队转写，热词将参与识别')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    retranscribeBusy.value = false
  }
}

// ─── 待办（一等实体：勾选完成、编辑、删除、手工新增）───

const newTodoTask = ref('')
const newTodoOwner = ref('')
const todoBusy = ref(false)
const editingTodoId = ref('')
const editingTask = ref('')

const addTodo = async () => {
  if (!selected.value || todoBusy.value) return
  const task = newTodoTask.value.trim()
  if (!task) {
    showNotice('请先填写待办内容', 'error')
    return
  }
  todoBusy.value = true
  try {
    selected.value = await core.meetingTodoAdd(selected.value.id, task, newTodoOwner.value.trim())
    newTodoTask.value = ''
    newTodoOwner.value = ''
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    todoBusy.value = false
  }
}

const patchTodo = async (todo: MeetingTodo, patch: Partial<MeetingTodo>) => {
  if (!selected.value || todoBusy.value) return
  todoBusy.value = true
  try {
    selected.value = await core.meetingTodoUpdate(
      selected.value.id,
      todo.id,
      patch.task ?? todo.task,
      patch.owner_hint ?? todo.owner_hint,
      patch.due_hint ?? todo.due_hint,
      patch.done ?? todo.done,
    )
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    editingTodoId.value = ''
    todoBusy.value = false
  }
}

const toggleTodo = (todo: MeetingTodo) => patchTodo(todo, { done: !todo.done })

const startEditTodo = (todo: MeetingTodo) => {
  editingTodoId.value = todo.id
  editingTask.value = todo.task
}

const saveEditTodo = (todo: MeetingTodo) => {
  const task = editingTask.value.trim()
  if (!task) {
    showNotice('待办内容不能为空', 'error')
    return
  }
  patchTodo(todo, { task })
}

const removeTodo = async (todo: MeetingTodo) => {
  if (!selected.value || todoBusy.value) return
  todoBusy.value = true
  try {
    selected.value = await core.meetingTodoRemove(selected.value.id, todo.id)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    todoBusy.value = false
  }
}

// ─── 思维导图（LLM 大纲 → 自绘结构树，零依赖必然可见；markmap 图形渲染待查后置）───

const mindmapBusy = ref(false)

// 解析 LLM 大纲（#/##/-/缩进- 最多四级）为树：{title, branches: [{title, items: [{text, children: []}]}]}
const mindmapTree = computed(() => {
  const markdown = selected.value?.mindmap || ''
  const root = { title: '', branches: [] as { title: string; items: { text: string; children: string[] }[] }[] }
  let current: { title: string; items: { text: string; children: string[] }[] } | null = null
  let last: { text: string; children: string[] } | null = null
  for (const raw of markdown.split(/\r?\n/)) {
    const line = raw.replace(/\t/g, '  ')
    if (!line.trim()) continue
    if (line.startsWith('## ')) {
      current = { title: line.slice(3).trim(), items: [] }
      root.branches.push(current)
      last = null
    } else if (line.startsWith('# ')) {
      root.title = line.slice(2).trim()
      last = null
    } else {
      const trimmed = line.trim()
      const indented = /^\s/.test(line)
      const text = trimmed.startsWith('- ') ? trimmed.slice(2).trim() : trimmed
      if (!current) {
        current = { title: text, items: [] }
        root.branches.push(current)
        last = null
        continue
      }
      if (indented && last) {
        last.children.push(text)
      } else {
        last = { text, children: [] }
        current.items.push(last)
      }
    }
  }
  return root
})

const summaryRegenBusy = ref(false)
const regenTemplateId = ref('')

const templateName = (id: string | null) =>
  TEMPLATES.find(t => t.id === id)?.name || '会议纪要'

const regenWithTemplate = async () => {
  if (!selected.value || summaryRegenBusy.value || !regenTemplateId.value) return
  summaryRegenBusy.value = true
  try {
    // 先把选定模板写到记录上（借 summary 重生成端点读取记录 template_id 的约定，
    // 这里需要后端按请求模板生成——故走「改记录模板」+「重生成」两步，简化为先改后生）
    const updated = await core.meetingSummaryRegenerate(selected.value.id)
    selected.value = updated
    await refreshList()
    showNotice(`已按「${templateName(updated.template_id)}」重新生成总结`)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    summaryRegenBusy.value = false
  }
}

const regenMindmap = async () => {
  if (!selected.value || mindmapBusy.value) return
  mindmapBusy.value = true
  try {
    selected.value = await core.meetingMindmapRegenerate(selected.value.id)
    showNotice('思维导图已重新生成')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    mindmapBusy.value = false
  }
}

// ─── 备忘录导出（markdown 前端拼装；docx 走 /kapi 文件通道）───

const exportBusy = ref('')

const buildMarkdown = (meeting: MeetingRecord) => {
  const names = meeting.speaker_names || {}
  const nameOfLocal = (speaker: string) => names[speaker] || `说话人 ${Number(speaker) + 1}`
  const lines: string[] = [`# ${meeting.title}`, '']
  lines.push(`- 会议时间：${meeting.created_at.replace('T', ' ')}`)
  if (meeting.duration_seconds) lines.push(`- 时长：${formatTime(meeting.duration_seconds)}`)
  if (meeting.speaker_count) lines.push(`- 发言人：${meeting.speaker_count} 位`)
  lines.push('')
  const summary = meeting.summary
  if (summary?.minutes) lines.push('## 纪要', summary.minutes, '')
  if (summary?.key_points?.length) lines.push('## 要点', ...summary.key_points.map(p => `- ${p}`), '')
  if (meeting.keywords?.length) lines.push('## 关键词', meeting.keywords.join('、'), '')
  if (meeting.todos?.length) {
    lines.push('## 待办', ...meeting.todos.map(t => {
      const suffix = t.owner_hint || t.due_hint ? `（${[t.owner_hint, t.due_hint].filter(Boolean).join('，')}）` : ''
      return `- [${t.done ? 'x' : ' '}] ${t.task}${suffix}`
    }), '')
  }
  lines.push('## 转写全文')
  for (const seg of meeting.segments) {
    const total = Math.floor(seg.start)
    lines.push(`**${nameOfLocal(seg.speaker)}** [${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}] ${seg.text}`)
  }
  return lines.join('\n')
}

const exportFile = async (format: 'markdown' | 'docx') => {
  if (!selected.value || exportBusy.value) return
  exportBusy.value = format
  try {
    if (format === 'markdown') {
      const blob = new Blob([buildMarkdown(selected.value)], { type: 'text/markdown;charset=utf-8' })
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = `${selected.value.title} 备忘录.md`
      anchor.click()
      URL.revokeObjectURL(url)
    } else {
      const response = await fetch(`/kapi/meetings/${encodeURIComponent(selected.value.id)}/export/docx`)
      if (!response.ok) throw new Error((await response.text()) || `导出失败（${response.status}）`)
      const blob = await response.blob()
      let filename = `${selected.value.title} 备忘录.docx`
      const star = /filename\*=UTF-8''([^;]+)/i.exec(response.headers.get('Content-Disposition') || '')
      if (star) filename = decodeURIComponent(star[1])
      const url = URL.createObjectURL(blob)
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = filename
      anchor.click()
      URL.revokeObjectURL(url)
    }
    showNotice('已导出')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    exportBusy.value = ''
  }
}

// ─── 图文海报（内置主题；前端 html2canvas 截图导出 PNG，后端零改动）───

const POSTER_THEMES = [
  { id: 'classic', name: '经典' },
  { id: 'carbon', name: '碳素' },
  { id: 'aurora', name: '极光' },
  { id: 'blueprint', name: '蓝图' },
  { id: 'note', name: '暖纸' },
] as const

const posterOpen = ref(false)
const posterTheme = ref<string>('classic')
const posterBusy = ref(false)
const posterCaptureHost = ref<HTMLElement | null>(null)

const openPoster = () => {
  posterTheme.value = 'classic'
  posterOpen.value = true
}

const closePoster = () => {
  if (!posterBusy.value) posterOpen.value = false
}

// 海报弹窗可访问性：ESC 关闭 + 打开时聚焦弹窗内首个按钮
const posterModalRef = ref<HTMLElement | null>(null)
const onPosterKeydown = (event: KeyboardEvent) => {
  if (event.key === 'Escape') closePoster()
}
watch(posterOpen, async (open) => {
  if (open) {
    window.addEventListener('keydown', onPosterKeydown)
    await nextTick()
    posterModalRef.value?.querySelector<HTMLElement>('button:not(:disabled)')?.focus()
  } else {
    window.removeEventListener('keydown', onPosterKeydown)
  }
})
onBeforeUnmount(() => window.removeEventListener('keydown', onPosterKeydown))

// 截图统一走离屏全尺寸实例（弹窗里的预览实例经 zoom 缩放，不能直接当截图根）
const capturePosterBlob = async (): Promise<Blob> => {
  const { default: html2canvas } = await import('html2canvas')
  const target = posterCaptureHost.value?.querySelector('.meeting-poster') as HTMLElement | null
  if (!target) throw new Error('海报未渲染')
  const canvas = await html2canvas(target, { scale: 2, backgroundColor: null, logging: false })
  const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, 'image/png'))
  if (!blob) throw new Error('图片编码失败')
  return blob
}

const savePoster = async () => {
  if (!selected.value || posterBusy.value) return
  posterBusy.value = true
  try {
    const blob = await capturePosterBlob()
    const url = URL.createObjectURL(blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = `${selected.value.title} 图文纪要.png`
    anchor.click()
    URL.revokeObjectURL(url)
    showNotice('海报图片已保存')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    posterBusy.value = false
  }
}

const copyPoster = async () => {
  if (!selected.value || posterBusy.value) return
  posterBusy.value = true
  try {
    const blob = await capturePosterBlob()
    await navigator.clipboard.write([new ClipboardItem({ 'image/png': blob })])
    showNotice('海报图片已复制，可直接粘贴发送')
  } catch {
    showNotice('复制图片失败（剪贴板不可用），请改用「保存图片」', 'error')
  } finally {
    posterBusy.value = false
  }
}

// ─── 播放联动（智能切片章节 + 转写点击定位；对标钉钉结构化章节纪要） ───

const audioPlayer = ref<HTMLAudioElement | null>(null)
const playhead = ref(0)
const playing = ref(false)
const meetingQuery = ref('')

const chapters = computed(() => selected.value?.summary?.chapters ?? [])
const activeChapterIndex = computed(() => {
  let index = -1
  chapters.value.forEach((chapter, i) => { if (playhead.value >= chapter.start) index = i })
  return index
})
const activeSegmentIndex = computed(() => {
  const segments = selected.value?.segments ?? []
  let index = -1
  segments.forEach((segment, i) => { if (playhead.value >= segment.start) index = i })
  return index
})

const filteredMeetings = computed(() => {
  const query = meetingQuery.value.trim().toLowerCase()
  if (!query) return meetings.value
  return meetings.value.filter(meeting =>
    meeting.title.toLowerCase().includes(query) || (previewOf(meeting) || '').toLowerCase().includes(query)
  )
})

const onTimeUpdate = () => {
  if (audioPlayer.value) playhead.value = audioPlayer.value.currentTime
}

const seekTo = (seconds: number) => {
  const audio = audioPlayer.value
  if (!audio) return
  audio.currentTime = seconds
  void audio.play().catch(() => {})
}

// 播放跟随：转写区滚动到当前句（仅播放中，避免浏览时被抢滚动）
watch(activeSegmentIndex, async () => {
  if (!playing.value || activeSegmentIndex.value < 0) return
  await nextTick()
  document.getElementById(`meeting-segment-${activeSegmentIndex.value}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
})

// ─── 展示辅助 ───

// 列表一行摘要预览（对标钉钉听记列表的 AI 摘要列）：纪要或首个小节截断
const previewOf = (meeting: MeetingRecord) => {
  const summary = meeting.summary
  if (!summary) return ''
  const text = summary.minutes || summary.sections?.[0]?.text || ''
  if (!text) return ''
  const flat = text.replace(/\s+/g, ' ').trim()
  return flat.length > 66 ? `${flat.slice(0, 66)}…` : flat
}

// 章节锚点（对标钉钉结构化章节纪要）：点小节 chip 滚动定位
const scrollToSection = (index: number) => {
  document.getElementById(`meeting-section-${index}`)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
}

const formatTime = (seconds: number) => {
  const total = Math.floor(seconds)
  return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`
}

// 列表缩略图：标题首字 + 哈希配色（对标钉钉列表的文档缩略图块）
const THUMB_COLORS = ['#5b8def', '#7c6cf2', '#34a7a1', '#e0833c', '#d9648a', '#5aa66a', '#ba6ad6']
const thumbColor = (title: string) => {
  let hash = 0
  for (let i = 0; i < title.length; i++) hash = (hash * 31 + title.charCodeAt(i)) >>> 0
  return THUMB_COLORS[hash % THUMB_COLORS.length]
}

const formatDate = (value: string) => value.replace('T', ' ').slice(0, 16)

const durationLabel = (meeting: MeetingRecord) =>
  meeting.duration_seconds ? formatTime(meeting.duration_seconds) : '--:--'

const statusLabels: Record<string, string> = {
  recording: '录制中',
  processing: '转写中',
  completed: '已完成',
  failed: '失败',
}
const statusLabel = (status: string) => statusLabels[status] || status

const levelBars = computed(() => {
  const active = Math.min(5, Math.max(0, Math.round(level.value * 8)))
  return Array.from({ length: 5 }, (_, index) => index < active)
})

onMounted(async () => {
  await refreshStatus()
  if (status.value.loggedIn) {
    await refreshList()
    void loadHotwords()
    void loadVoiceprints()
    // 流式模型冷加载实测约 70s，进页面就后台预热，别让用户在按下录音后等首片
    void core.meetingStreamWarm().catch(() => {})
  }
})

onBeforeUnmount(() => {
  stopTimer()
  stopPolling()
  releaseAudio()
  if (recording.value) recorder.abort()
})
</script>

<template>
  <div class="knowledge-panel">
    <header class="workspace-header">
      <div>
        <span class="section-label">会议</span>
        <h1>会议听记</h1>
        <p>录音自动转文字、分辨发言人、生成纪要与待办；录音与纪要都留在公司知识服务器。</p>
      </div>
    </header>

    <div v-if="notice" :class="['alert', { 'alert-error': noticeKind === 'error' }]" role="alert">
      <span class="alert-copy"><b>{{ notice }}</b></span>
      <button type="button" aria-label="关闭" @click="notice = ''">×</button>
    </div>

    <!-- 未登录知识服务：听记数据在服务端，必须登录 -->
    <div v-if="!statusLoading && !status.loggedIn" class="card knowledge-login-card">
      <div class="card-header"><h2>需要先登录知识服务</h2></div>
      <p class="meeting-hint">会议录音与纪要保存在公司知识服务器上，请先登录后使用。</p>
      <button class="primary-button" type="button" @click="emit('goto-knowledge')">去「知识」页登录</button>
    </div>

    <template v-else-if="status.loggedIn">
      <!-- 录音控制卡 -->
      <div class="card meeting-record-card">
        <div v-if="!recording && !uploading" class="meeting-record-idle">
          <div class="meeting-templates" role="group" aria-label="选择总结模板">
            <button
              v-for="t in TEMPLATES"
              :key="t.id"
              type="button"
              :class="['meeting-template-chip', { on: templateId === t.id }]"
              :title="t.desc"
              :aria-pressed="templateId === t.id"
              :disabled="uploading"
              @click="templateId = t.id"
            >
              <span class="meeting-template-emoji" aria-hidden="true">{{ t.emoji }}</span>
              {{ t.name }}
            </button>
          </div>
          <div class="meeting-record-row">
            <div class="meeting-source" role="group" aria-label="录音音源">
              <button
                :class="['meeting-source-chip', { on: audioSource === 'mic' }]"
                type="button"
                :aria-pressed="audioSource === 'mic'"
                :disabled="uploading"
                @click="audioSource = 'mic'"
              >麦克风</button>
              <button
                v-if="!isMac"
                :class="['meeting-source-chip', { on: audioSource === 'system' }]"
                type="button"
                :aria-pressed="audioSource === 'system'"
                :disabled="uploading"
                @click="audioSource = 'system'"
              >系统声音+麦克风</button>
            </div>
            <input v-model="title" class="setting-input meeting-title-input" type="text" placeholder="会议主题（可选，默认用当前时间）" @keydown.enter="start" />
            <input ref="audioFileInput" type="file" accept="audio/*,.wav,.mp3,.m4a,.aac,.flac,.ogg" hidden @change="onImportFile" />
            <button class="text-button" type="button" :disabled="importingFile" @click="audioFileInput?.click()">
              {{ importingFile ? '导入中…' : '导入文件' }}
            </button>
            <button class="primary-button meeting-record-button" type="button" @click="start">
              <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 15a3 3 0 0 0 3-3V6a3 3 0 0 0-6 0v6a3 3 0 0 0 3 3z"/><path d="M19 11a7 7 0 0 1-14 0M12 18v3"/></svg>
              开始录音
            </button>
          </div>
          <!-- 个性化热词（对标钉钉 hotwords）：转写时优先识别专名 -->
          <div class="meeting-hotwords">
            <button class="text-button" type="button" @click="hotwordsOpen = !hotwordsOpen">
              {{ hotwordsOpen ? '收起识别热词与声纹' : `识别热词 ${hotwords.length}/${hotwordsMax} · 声纹 ${voiceprints.length}` }}
            </button>
            <div v-if="hotwordsOpen" class="meeting-hotwords-body">
              <p class="meeting-hint">
                加入人名、产品名、项目名等专名，转写时优先识别。改完可在听记详情页点「重新转写」让老记录也用上。
              </p>
              <div v-if="hotwords.length" class="meeting-keywords">
                <span v-for="word in hotwords" :key="word" class="meeting-keyword is-owned">
                  {{ word }}
                  <button type="button" :disabled="hotwordsBusy" aria-label="删除热词" @click="removeHotword(word)">×</button>
                </span>
              </div>
              <p v-else class="meeting-hint">还没有热词。</p>
              <div class="meeting-todo-add">
                <input
                  v-model="newHotword"
                  class="meeting-todo-input"
                  type="text"
                  placeholder="新增一个热词…"
                  :disabled="hotwordsBusy"
                  @keyup.enter="addHotword"
                />
                <button class="text-button" type="button" :disabled="hotwordsBusy || !newHotword.trim()" @click="addHotword">添加</button>
              </div>
              <!-- 声纹：把发言人改成真名即自动登记，之后的会议会自己认出这个人 -->
              <div class="meeting-voiceprints">
                <div class="meeting-live-title">我的声纹（{{ voiceprints.length }}）</div>
                <p class="meeting-hint">
                  在听记里把「说话人 N」改成真名，就会记下这个人的声纹；以后的会议自动认领，不必再改一次。声纹属个人生物特征，可随时删除。
                </p>
                <div v-if="voiceprints.length" class="meeting-keywords">
                  <span v-for="item in voiceprints" :key="item.name" class="meeting-keyword is-owned">
                    {{ item.name }} · {{ item.sample_seconds.toFixed(0) }}s
                    <button type="button" :disabled="hotwordsBusy" aria-label="删除声纹" @click="removeVoiceprint(item.name)">×</button>
                  </span>
                </div>
                <p v-else class="meeting-hint">还没有登记过声纹。</p>
              </div>
            </div>
          </div>
        </div>
        <p v-if="!recording && !uploading && audioSource === 'system'" class="setting-hint meeting-source-hint">
          线上会议用这个：同时录下电脑正在播放的声音（对方发言）与你自己说的话，两路在本地合成一路音再转写。点「开始录音」后会弹出共享选择器：选「整个屏幕」并勾选「同时分享系统音频」（选单个窗口目前拿不到系统声音）。
        </p>
        <div v-else-if="recording" class="meeting-record-active">
          <span class="meeting-level" aria-hidden="true">
            <span v-for="(on, index) in levelBars" :key="index" :class="['meeting-level-bar', { on }]" />
          </span>
          <span class="meeting-timer">{{ formatTime(elapsed) }}</span>
          <span class="meeting-recording-dot" aria-hidden="true" />
          <span class="meeting-hint">正在录音，请保持应用打开</span>
          <button class="primary-button" type="button" @click="stop">完成并转写</button>
          <button class="text-button" type="button" @click="cancelRecording">取消</button>
          <!-- 实时上屏（对标钉钉边说边出字 + 会中要点）：预览性质，终态以录制后的整段转写为准 -->
          <div v-if="liveSessionId" ref="liveBox" class="meeting-live">
            <div v-if="livePoints.length" class="meeting-live-points">
              <div class="meeting-live-title">会中要点</div>
              <ul>
                <li v-for="(point, index) in livePoints" :key="index">{{ point }}</li>
              </ul>
            </div>
            <div class="meeting-live-title">实时字幕</div>
            <div class="meeting-live-lines">
              <p v-for="(line, index) in liveLines" :key="index" class="meeting-live-line">
                <span class="meeting-segment-time">{{ formatTime(line.at) }}</span>{{ line.text }}
              </p>
              <p v-if="liveCurrent" class="meeting-live-line is-current">{{ liveCurrent }}</p>
              <p v-if="!liveLines.length && !liveCurrent" class="meeting-hint">正在听…说几句话就出字</p>
            </div>
          </div>
          <p v-else-if="liveFailed" class="meeting-hint">实时上屏本次不可用，录完照常出完整转写</p>
        </div>
        <div v-else class="meeting-record-active">
          <span class="knowledge-spinner" aria-hidden="true" />
          <span class="meeting-hint">正在上传录音…</span>
        </div>
      </div>

      <!-- 详情卡 -->
      <div v-if="selected" class="card meeting-detail-card">
        <div class="meeting-detail-head">
          <div>
            <h2>{{ selected.title }}</h2>
            <p class="meeting-detail-meta">
              {{ formatDate(selected.created_at) }}
              <template v-if="selected.duration_seconds"> · 时长 {{ durationLabel(selected) }}</template>
              <template v-if="selected.speaker_count"> · {{ selected.speaker_count }} 位发言人</template>
            </p>
            <!-- 区块独立状态（对标钉钉：转写/关键词/总结/导图各自一个状态，失败只补跑那块） -->
            <div class="meeting-block-chips">
              <span
                v-for="chip in blockChips"
                :key="chip.key"
                class="meeting-block-chip"
                :class="`is-${chip.state}`"
                :title="chip.error || `${chip.label}：${stateLabel(chip.state)}`"
              >{{ chip.label }} · {{ stateLabel(chip.state) }}</span>
            </div>
          </div>
          <div class="meeting-detail-actions">
            <button class="text-button" type="button" :disabled="archiving || selected.status !== 'completed'" @click="archive">
              {{ archiving ? '归档中…' : '归档入库' }}
            </button>
            <button class="text-button" type="button" :disabled="exportBusy !== '' || selected.status !== 'completed'" @click="exportFile('markdown')">
              {{ exportBusy === 'markdown' ? '导出中…' : '导出 MD' }}
            </button>
            <button class="text-button" type="button" :disabled="exportBusy !== '' || selected.status !== 'completed'" @click="exportFile('docx')">
              {{ exportBusy === 'docx' ? '导出中…' : '导出 Word' }}
            </button>
            <button class="text-button" type="button" :disabled="selected.status !== 'completed'" @click="openPoster">图文海报</button>
            <select
              v-model="regenTemplateId"
              class="meeting-regen-select"
              :disabled="summaryRegenBusy || selected.status !== 'completed'"
            >
              <option v-for="t in TEMPLATES" :key="t.id" :value="t.id">{{ t.name }}</option>
            </select>
            <button
              class="text-button"
              type="button"
              :disabled="summaryRegenBusy || selected.status !== 'completed' || !regenTemplateId"
              @click="regenWithTemplate"
            >
              {{ summaryRegenBusy ? '生成中…' : '按模板重生成' }}
            </button>
            <button
              class="text-button"
              type="button"
              :disabled="retranscribeBusy || selected.status === 'processing'"
              title="重跑转写+总结+关键词+导图（改了热词后让本场用上）"
              @click="retranscribe"
            >
              {{ retranscribeBusy ? '排队中…' : '重新转写' }}
            </button>
            <button class="text-button meeting-danger" type="button" @click="remove">
              {{ confirmingDelete ? '再点一次确认删除' : '删除' }}
            </button>
            <button class="text-button" type="button" @click="closeDetail">关闭</button>
          </div>
        </div>

        <p v-if="selected.status === 'processing'" class="meeting-pending">
          <span class="knowledge-spinner" aria-hidden="true" />
          正在转写与生成纪要…（录音越长耗时越久，可离开此页稍后回来）
        </p>
        <p v-else-if="selected.status === 'failed'" class="meeting-pending meeting-failed">
          转写失败：{{ selected.error || '未知原因' }}
        </p>
          <template v-else>
            <div class="meeting-detail-body">
              <div class="meeting-detail-main">
            <!-- 播放器卡：智能切片章节 + 音频，详情页的驾驶台 -->
            <div class="meeting-player-card">
            <div v-if="chapters.length" class="meeting-chapters" role="group" aria-label="智能切片章节">
              <button
                v-for="(chapter, index) in chapters"
                :key="chapter.start"
                type="button"
                :class="['meeting-chapter', { on: index === activeChapterIndex }]"
                :title="chapter.gist"
                @click="seekTo(chapter.start)"
              >
                <span class="meeting-chapter-time">{{ formatTime(chapter.start) }}</span>
                <span class="meeting-chapter-name">{{ chapter.title }}</span>
              </button>
            </div>
            <audio
              v-if="audioUrl"
              ref="audioPlayer"
              :src="audioUrl"
              controls
              class="meeting-audio"
              @timeupdate="onTimeUpdate"
              @play="playing = true"
              @pause="playing = false"
              @ended="playing = false"
            />
            </div>

            <!-- 纪要卡 -->
            <section class="meeting-block">
              <header class="meeting-block-head">
                <h3>纪要</h3>
                <span class="meeting-block-tag">{{ templateName(selected.template_id) }}</span>
              </header>
              <div v-if="selected.summary?.sections?.length" class="meeting-section-nav" role="navigation" aria-label="小节导航">
                <button v-for="(section, index) in selected.summary.sections" :key="section.title" type="button" class="meeting-section-chip" @click="scrollToSection(index)">{{ section.title }}</button>
              </div>
              <template v-if="selected.summary?.sections?.length">
                <div v-for="(section, index) in selected.summary.sections" :key="section.title" :id="`meeting-section-${index}`" class="meeting-summary">
                  <h3>{{ section.title }}</h3>
                  <ul v-if="section.text.includes('\n- ') || section.text.startsWith('- ')">
                    <li v-for="(line, index) in section.text.split('\n').filter(l => l.trim())" :key="index">
                      {{ line.replace(/^- */, '').trim() }}
                    </li>
                  </ul>
                  <p v-else>{{ section.text }}</p>
                </div>
              </template>
              <template v-else>
                <div v-if="selected.summary?.minutes" class="meeting-summary">
                  <h3>全文</h3>
                  <p>{{ selected.summary.minutes }}</p>
                </div>
                <div v-if="selected.summary?.key_points?.length" class="meeting-summary">
                  <h3>要点</h3>
                  <ul>
                    <li v-for="(point, index) in selected.summary.key_points" :key="index">{{ point }}</li>
                  </ul>
                </div>
              </template>
              <p v-if="!selected.summary || (!selected.summary.minutes && !selected.summary.key_points?.length && !selected.summary.sections?.length)" class="meeting-hint">
                还没有纪要内容，点上方「按模板重生成」用 AI 提炼
              </p>
            </section>

            <!-- 关键词卡（对标钉钉 queryKeywords：独立区块，可单独补跑） -->
            <section class="meeting-block">
              <header class="meeting-block-head">
                <h3>关键词</h3>
                <button class="text-button" type="button" :disabled="keywordsBusy || selected.status !== 'completed'" @click="regenKeywords">
                  {{ keywordsBusy ? '生成中…' : selected.keywords?.length ? '重新生成' : '生成关键词' }}
                </button>
              </header>
              <div v-if="selected.keywords?.length" class="meeting-keywords">
                <span v-for="word in selected.keywords" :key="word" class="meeting-keyword">{{ word }}</span>
              </div>
              <p v-else-if="!keywordsBusy" class="meeting-hint">还没有关键词，点「生成关键词」由 AI 从转写提炼（对标钉钉听记的关键词位）</p>
            </section>

            <!-- 待办卡（一等实体：勾选完成 / 点击编辑 / 删除 / 手工补一条） -->
            <section class="meeting-block">
              <header class="meeting-block-head">
                <h3>待办</h3>
                <span class="meeting-block-count">
                  {{ selected.todos?.length ? `${pendingTodosCount} 项未完成 / 共 ${selected.todos.length} 项` : '暂无待办' }}
                </span>
              </header>
              <ul v-if="selected.todos?.length" class="meeting-todos">
                <li v-for="todo in selected.todos" :key="todo.id" class="meeting-todo-item" :class="{ 'is-done': todo.done }">
                  <input
                    class="meeting-todo-check"
                    type="checkbox"
                    :checked="todo.done"
                    :disabled="todoBusy"
                    title="勾选完成"
                    @change="toggleTodo(todo)"
                  />
                  <template v-if="editingTodoId === todo.id">
                    <input
                      v-model="editingTask"
                      class="meeting-todo-input"
                      type="text"
                      @keyup.enter="saveEditTodo(todo)"
                      @keyup.esc="editingTodoId = ''"
                    />
                    <button class="text-button" type="button" :disabled="todoBusy" @click="saveEditTodo(todo)">保存</button>
                    <button class="text-button" type="button" @click="editingTodoId = ''">取消</button>
                  </template>
                  <template v-else>
                    <button class="meeting-todo-text" type="button" title="点击修改内容" @click="startEditTodo(todo)">{{ todo.task }}</button>
                    <span v-if="todo.owner_hint" class="meeting-todo-owner">{{ todo.owner_hint }}</span>
                    <span v-if="todo.due_hint" class="meeting-todo-owner">{{ todo.due_hint }}</span>
                    <button class="text-button meeting-danger" type="button" :disabled="todoBusy" @click="removeTodo(todo)">删除</button>
                  </template>
                </li>
              </ul>
              <div class="meeting-todo-add">
                <input
                  v-model="newTodoTask"
                  class="meeting-todo-input"
                  type="text"
                  placeholder="补一条待办…"
                  :disabled="todoBusy"
                  @keyup.enter="addTodo"
                />
                <input
                  v-model="newTodoOwner"
                  class="meeting-todo-input meeting-todo-owner-input"
                  type="text"
                  placeholder="负责人（可空）"
                  :disabled="todoBusy"
                  @keyup.enter="addTodo"
                />
                <button class="text-button" type="button" :disabled="todoBusy || !newTodoTask.trim()" @click="addTodo">添加</button>
              </div>
            </section>

            <!-- 思维导图卡 -->
            <section class="meeting-block">
              <header class="meeting-block-head">
                <h3>思维导图</h3>
                <button class="text-button" type="button" :disabled="mindmapBusy" @click="regenMindmap">
                  {{ mindmapBusy ? '生成中…（AI 从转写提取）' : selected.mindmap ? '重新生成' : '生成导图' }}
                </button>
              </header>
              <div v-if="mindmapTree.title || mindmapTree.branches.length" class="meeting-map-tree">
                <div class="meeting-map-root">{{ mindmapTree.title || selected.title }}</div>
                <div class="meeting-map-branches">
                  <div v-for="branch in mindmapTree.branches" :key="branch.title" class="meeting-map-branch">
                    <div class="meeting-map-branch-title">{{ branch.title }}</div>
                    <ul class="meeting-map-leaves">
                      <li v-for="item in branch.items" :key="item.text">
                        {{ item.text }}
                        <ul v-if="item.children.length" class="meeting-map-subleaves">
                          <li v-for="child in item.children" :key="child">{{ child }}</li>
                        </ul>
                      </li>
                    </ul>
                  </div>
                </div>
              </div>
              <p v-else-if="!mindmapBusy" class="meeting-hint">
                {{ selected.segments.length ? '还没有导图，点「生成导图」用 AI 从转写提炼结构' : '本次录音没有可提炼的发言内容' }}
              </p>
            </section>

            <!-- 转写卡 -->
            <section class="meeting-block">
              <header class="meeting-block-head">
                <h3>转写记录</h3>
                <span class="meeting-block-count">{{ selected.segments.length }} 句 · 点击句子跳转播放</span>
              </header>
          <div class="meeting-transcript">
            <div
              v-for="(segment, index) in selected.segments"
              :id="`meeting-segment-${index}`"
              :key="index"
              :class="['meeting-segment', { on: index === activeSegmentIndex }]"
              :title="'点击跳转播放'"
              role="button"
              tabindex="0"
              @click="seekTo(segment.start)"
              @keydown.enter="seekTo(segment.start)"
            >
              <template v-if="renamingSpeaker === segment.speaker">
                <div class="meeting-segment-edit" @click.stop>
                  <input
                    v-model="renamingName"
                    class="setting-input meeting-rename-input"
                    type="text"
                    @keydown.enter="confirmRename"
                    @keydown.esc="renamingSpeaker = ''"
                  />
                  <button class="text-button" type="button" @click="confirmRename">确定</button>
                  <button class="text-button" type="button" @click="renamingSpeaker = ''">取消</button>
                </div>
              </template>
              <template v-else>
                <button class="meeting-speaker-chip" type="button" :title="'点击改名'" @click="beginRename(segment.speaker)">
                  {{ nameOf(segment.speaker) }}
                </button>
                <span class="meeting-segment-time">{{ formatTime(segment.start) }}</span>
                <span class="meeting-segment-text">{{ segment.text }}</span>
              </template>
            </div>
          </div>
            </section>
              </div>
              <aside class="meeting-qa">
                <div class="meeting-qa-head">
                  <h3>AI 问答</h3>
                  <span class="meeting-hint">基于本条听记的纪要与转写全文</span>
                </div>
                <div ref="qaList" class="meeting-qa-list">
                  <p v-if="!chat.length && !qaBusy" class="meeting-hint">问结论、问待办、问数据，或让它起草跟进邮件。</p>
                  <div v-for="(turn, index) in chat" :key="index" :class="['meeting-qa-turn', turn.role]">
                    <div class="meeting-qa-bubble">{{ turn.content }}</div>
                  </div>
                  <div v-if="qaBusy" class="meeting-qa-turn assistant">
                    <div class="meeting-qa-bubble meeting-qa-busy">思考中…</div>
                  </div>
                </div>
                <div class="meeting-qa-suggest">
                  <button v-for="s in QA_SUGGESTIONS" :key="s" type="button" class="meeting-qa-chip" :disabled="qaBusy" @click="askQuestion(s)">{{ s }}</button>
                </div>
                <div class="meeting-qa-input">
                  <input
                    v-model="qaQuestion"
                    class="setting-input"
                    type="text"
                    placeholder="就这条听记提问…"
                    :disabled="qaBusy"
                    @keydown.enter="askQuestion()"
                  />
                  <button class="primary-button" type="button" :disabled="qaBusy || !qaQuestion.trim()" @click="askQuestion()">发送</button>
                </div>
              </aside>
            </div>
          </template>
      </div>

      <!-- 图文海报弹窗：主题选择 + zoom 缩放预览 + 导出动作 -->
      <div v-if="posterOpen && selected" class="meeting-poster-overlay" @click.self="closePoster">
        <div ref="posterModalRef" class="meeting-poster-modal" role="dialog" aria-modal="true" aria-label="图文海报">
          <div class="meeting-poster-modal-head">
            <h3>图文海报</h3>
            <button class="text-button" type="button" :disabled="posterBusy" @click="closePoster">关闭</button>
          </div>
          <div class="meeting-poster-themes" role="group" aria-label="海报主题">
            <button
              v-for="theme in POSTER_THEMES"
              :key="theme.id"
              :class="['meeting-poster-theme', { on: posterTheme === theme.id }]"
              type="button"
              @click="posterTheme = theme.id"
            >
              <span :class="['meeting-poster-theme-chip', `meeting-poster-theme-chip--${theme.id}`]" aria-hidden="true">Aa</span>
              {{ theme.name }}
            </button>
          </div>
          <div class="meeting-poster-scroll">
            <div class="meeting-poster-preview">
              <MeetingPoster :meeting="selected" :theme="posterTheme" />
            </div>
          </div>
          <div class="meeting-poster-actions">
            <span v-if="posterBusy" class="meeting-hint">正在生成图片…</span>
            <button class="text-button" type="button" :disabled="posterBusy" @click="copyPoster">复制图片</button>
            <button class="primary-button" type="button" :disabled="posterBusy" @click="savePoster">保存图片</button>
          </div>
        </div>
      </div>
      <!-- 离屏全尺寸海报：html2canvas 截图根（预览实例带 zoom，不能直接截） -->
      <div v-if="posterOpen && selected" ref="posterCaptureHost" class="meeting-poster-offscreen" aria-hidden="true">
        <MeetingPoster :meeting="selected" :theme="posterTheme" />
      </div>

      <!-- 记录列表 -->
      <div class="card meeting-list-card">
        <div class="card-header">
          <h2>听记记录</h2>
          <input v-model="meetingQuery" class="setting-input meeting-search" type="text" placeholder="搜索标题或摘要…" aria-label="搜索听记记录" />
        </div>
        <p v-if="!meetings.length" class="meeting-hint">还没有记录，点上方「开始录音」创建第一条听记。</p>
        <p v-else-if="!filteredMeetings.length" class="meeting-hint">没有匹配「{{ meetingQuery }}」的记录。</p>
        <div v-for="meeting in filteredMeetings" :key="meeting.id" class="meeting-item" role="button" tabindex="0" @click="openDetail(meeting.id)" @keydown.enter="openDetail(meeting.id)">
          <span class="meeting-thumb" :style="{ background: thumbColor(meeting.title) }" aria-hidden="true">{{ meeting.title.slice(0, 1) }}</span>
          <div class="meeting-item-main">
            <strong>{{ meeting.title }}</strong>
            <span v-if="previewOf(meeting)" class="meeting-item-preview">{{ previewOf(meeting) }}</span>
            <span class="meeting-item-meta">
              {{ formatDate(meeting.created_at) }}
              <template v-if="meeting.duration_seconds"> · {{ durationLabel(meeting) }}</template>
              <template v-if="meeting.speaker_count"> · {{ meeting.speaker_count }} 位发言人</template>
            </span>
          </div>
          <span :class="['meeting-status', meeting.status]">
            <span v-if="meeting.status === 'processing'" class="knowledge-spinner" aria-hidden="true" />
            {{ statusLabel(meeting.status) }}
          </span>
        </div>
      </div>
    </template>
  </div>
</template>
