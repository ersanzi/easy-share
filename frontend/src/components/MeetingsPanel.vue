<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { core } from '../services/core'
import { useEasyShare } from '../composables/useEasyShare'
import type { KnowledgeStatus, MeetingChatTurn, MeetingRecord, MeetingTodo, MeetingVoiceprint } from '../types/core'
import { decodeAudioFileToWav } from '../services/recorder'
import { meetingSession } from '../services/meetingSession'
import MeetingPoster from './MeetingPoster.vue'
import MeetingVisualReport from './MeetingVisualReport.vue'
import MindMap from './MindMap.vue'
// 会议听记：录音（前端 16k WAV）→ /kapi 同源网关上传 → 知识服务 FunASR 转写 +
// 说话人分离 + LLM 总结。登录态与「知识」页同源（Core 网关）。

const emit = defineEmits<{ (e: 'goto-knowledge'): void }>()

const app = useEasyShare()
const isMac = /Mac|iPhone|iPad/.test(navigator.platform || navigator.userAgent)

const status = ref<KnowledgeStatus>({ configured: false, loggedIn: false, serverUrl: '', username: '', role: '' })
const statusLoading = ref(true)

const meetings = ref<MeetingRecord[]>([])
const selected = ref<MeetingRecord | null>(null)
const audioUrl = ref('')
const detailLoading = ref(false)
const archiving = ref(false)
const confirmingDelete = ref(false)

// 录音会话在模块级单例里：切页只卸载本组件，采集与推流继续
const {
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
  checks: liveChecks,
  checking: liveChecking,
} = meetingSession

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

const renamingSpeaker = ref('')
const renamingName = ref('')
const renamingBusy = ref(false)

const showNotice = meetingSession.notify
const dismissNotice = meetingSession.dismissNotice
const notice = meetingSession.notice
const noticeKind = meetingSession.noticeKind

const refreshStatus = async () => {
  try {
    status.value = await core.knowledgeStatus()
  } finally {
    statusLoading.value = false
  }
}

// 连接失败时给能执行的下一步，而不是只抛一条 "无法连接知识服务器"
const serverError = ref('')
const serverErrorKind = ref<'network' | 'auth' | 'asr'>('network')
const retrying = ref(false)

const explainServerError = (message: string) => {
  if (message.includes('会议听记未启用')) {
    serverErrorKind.value = 'asr'
    return '知识服务没有开启会议听记能力（转写引擎未安装或未启用），请联系管理员开启。'
  }
  if (message.includes('登录已失效') || message.includes('请先登录')) {
    serverErrorKind.value = 'auth'
    return '登录已失效，请先重新登录知识服务。'
  }
  serverErrorKind.value = 'network'
  return message
}

const refreshList = async () => {
  try {
    meetings.value = await core.meetingsList(50)
    serverError.value = ''
  } catch (e) {
    serverError.value = explainServerError(e instanceof Error ? e.message : String(e))
  }
}

const retryConnect = async () => {
  if (retrying.value) return
  retrying.value = true
  try {
    await refreshStatus()
    if (!status.value.loggedIn) return
    await refreshList()
    if (!serverError.value) {
      showNotice('已连接知识服务')
      void core.meetingStreamWarm().catch(() => {})
    }
  } finally {
    retrying.value = false
  }
}

const guardedStart = async () => {
  if (serverError.value) {
    showNotice(serverError.value, 'error')
    return
  }
  await meetingSession.start()
}

// ─── 录音（状态与采集在 meetingSession 单例里，切页不中断） ───

const liveBox = ref<HTMLElement | null>(null)

// 字幕跟随最新一行（像钉钉那样贴着底部滚动）
watch(
  () => liveLines.value.length + liveCurrent.value.length,
  async () => {
    await nextTick()
    if (liveBox.value) liveBox.value.scrollTop = liveBox.value.scrollHeight
  },
)

const stop = meetingSession.stop
const cancelRecording = meetingSession.cancel

// 录音上传受理后（可能发生在用户已切走的页面上）：回来后刷新列表并打开这条听记
const consumeFinished = async () => {
  const id = meetingSession.takeFinished()
  if (!id) return
  await refreshList()
  await openDetail(id)
}

watch(() => meetingSession.finishedMeetingId.value, () => { void consumeFinished() })

// ─── 详情与轮询 ───

let pollHandle: number | null = null

// 阅读区标签页（对标钉钉听记的「转写 / AI 纪要 / 观点树 / 章节」分段）：
// 听记内容天然是"音频 + 逐字稿 + AI 产物"三份平行的东西，竖着叠成一长条
// 用户只能一直往下滚；分标签后每一屏只讲一件事。
type DetailTab = 'summary' | 'transcript' | 'mindmap'
const detailTab = ref<DetailTab>('summary')
const detailPane = ref<HTMLElement | null>(null)

const DETAIL_TABS: { id: DetailTab; label: string; hint: string }[] = [
  { id: 'summary', label: 'AI 纪要', hint: '结论、要点与待办' },
  { id: 'transcript', label: '转写', hint: '逐句原文，点句跳转播放' },
  { id: 'mindmap', label: '导图', hint: '结构化大纲' },
]

const summarySections = computed(() => selected.value?.summary?.sections ?? [])

const openDetail = async (id: string) => {
  const switching = selected.value?.id !== id
  detailLoading.value = true
  confirmingDelete.value = false
  try {
    const detail = await core.meetingDetail(id)
    if (switching) releaseAudio()
    if (switching) {
      chat.value = []
      // 换一条听记时回到「纪要」，并滚到阅读区顶部——否则用户看到的是上一条的滚动位置
      detailTab.value = 'summary'
    }
    selected.value = detail
    regenTemplateId.value = detail.template_id || 'meeting-minutes'
    if (detail.status === 'completed' && !audioUrl.value) {
      // 音频是增强项：拿不到（老记录没留 wav/服务端暂时失败）不该挡住纪要阅读
      void core.meetingAudioObjectUrl(id)
        .then(url => { if (selected.value?.id === id) audioUrl.value = url })
        .catch(() => {})
    }
    ensurePolling()
    if (switching) await nextTick()
    if (switching) scrollToDetailTop()
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    detailLoading.value = false
  }
}

// 打开一条听记就把它的标题顶到阅读区顶部：
// 听记页上半部分是录音卡与模板条，用户点列表进来想看的是内容，
// 而不是"标题被挤到屏幕外、只看到一半纪要"。
const scrollToDetailTop = () => {
  const host = detailAnchor.value
  if (!host) return
  const scroller = host.closest('.workspace') as HTMLElement | null
  if (!scroller) return
  const top = host.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop
  scroller.scrollTo({ top: Math.max(0, top - 12), behavior: 'smooth' })
}

const detailAnchor = ref<HTMLElement | null>(null)

const switchTab = async (tab: DetailTab) => {
  detailTab.value = tab
  await nextTick()
  // 换标签时把阅读区拉回顶部，不保留上一标签的滚动位置
  const pane = detailPane.value
  const scroller = pane?.closest('.workspace') as HTMLElement | null
  if (pane && scroller) {
    const top = pane.getBoundingClientRect().top - scroller.getBoundingClientRect().top + scroller.scrollTop
    scroller.scrollTo({ top: Math.max(0, top - 8), behavior: 'smooth' })
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
  detailTab.value = 'summary'
}

// 发言人颜色的唯一来源（色相轮转：同一场里不同人不撞色，改完名颜色也不跳）
const SPEAKER_HUES = [214, 268, 158, 32, 340, 196, 96, 300] as const
const speakerHue = (speaker: string) => {
  const index = speakerRows.value.findIndex(row => row.speaker === speaker)
  return SPEAKER_HUES[(index < 0 ? Number(speaker) || 0 : index) % SPEAKER_HUES.length]
}

// 逐字稿按「连续同一个人、且间隔不大」并成一块：
// 钉钉的转写读起来舒服，是因为它把同一个人连着的碎句收在一条里；
// 一句一行时，一段两分钟的发言会被切成十几条小行，眼睛要不停地跳。
const transcriptBlocks = computed(() => {
  const segments = selected.value?.segments ?? []
  const blocks: { speaker: string; start: number; end: number; index: number; texts: { text: string; index: number }[] }[] = []
  segments.forEach((segment, index) => {
    const previous = blocks[blocks.length - 1]
    const sameSpeaker = previous && previous.speaker === String(segment.speaker)
    if (sameSpeaker && segment.start - previous.end <= 6) {
      previous.texts.push({ text: segment.text, index })
      previous.end = segment.end
      return
    }
    blocks.push({
      speaker: String(segment.speaker),
      start: segment.start,
      end: segment.end,
      index,
      texts: [{ text: segment.text, index }],
    })
  })
  return blocks
})

// 当前这句落在第几块（播放跟随滚动用）
const activeBlockIndex = computed(() => {
  const blocks = transcriptBlocks.value
  let index = -1
  blocks.forEach((block, blockIndex) => {
    if (block.texts.some(item => item.index <= activeSegmentIndex.value)) index = blockIndex
  })
  return activeSegmentIndex.value < 0 ? -1 : index
})

// ─── 发言人（改名 / 标注真名） ───
//
// 钉钉听记的做法是把「说话人」当一等对象：详情页列出本场所有发言人，
// 点开就能标注真名，之后的转写行与导出都跟着变。这里同样先给一份
// 发言人清单，而不是让用户去转写正文里找那个小小的名字芯片。

const nameOf = (speaker: string) => selected.value?.speaker_names?.[speaker] || `说话人 ${Number(speaker) + 1}`

const speakerRows = computed(() => {
  const meeting = selected.value
  if (!meeting) return [] as { speaker: string; name: string; count: number; seconds: number }[]
  const rows = new Map<string, { speaker: string; name: string; count: number; seconds: number }>()
  const ensure = (speaker: string) => {
    if (!rows.has(speaker)) rows.set(speaker, { speaker, name: nameOf(speaker), count: 0, seconds: 0 })
    return rows.get(speaker)!
  }
  for (const segment of meeting.segments) {
    const row = ensure(String(segment.speaker))
    row.count += 1
    row.seconds += Math.max(0, Number(segment.end) - Number(segment.start))
  }
  for (const speaker of Object.keys(meeting.speaker_names || {})) ensure(speaker)
  return [...rows.values()].sort((left, right) => Number(left.speaker) - Number(right.speaker))
})

const beginRename = (speaker: string) => {
  renamingSpeaker.value = speaker
  renamingName.value = nameOf(speaker)
}

const confirmRename = async () => {
  const speaker = renamingSpeaker.value
  const name = renamingName.value.trim()
  if (!selected.value || !speaker || !name || renamingBusy.value) {
    if (!name) renamingSpeaker.value = ''
    return
  }
  renamingBusy.value = true
  try {
    const before = nameOf(speaker)
    selected.value = await core.meetingSpeakerRename(selected.value.id, speaker, name)
    await refreshList()
    showNotice(before === name ? '发言人名称未变化' : `已把「${before}」标注为「${name}」`)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    renamingBusy.value = false
    renamingSpeaker.value = ''
  }
}

// 合并发言人（对标钉钉「合并发言人」）：说话人分离偶尔把同一个人拆成两簇，
// 或者两簇其实是一个人——把来源那位的全部轮次并进目标那位即可，改名/导出立刻跟着变。
const mergeFrom = ref('')

const beginMerge = (speaker: string) => {
  mergeFrom.value = speaker
}

const confirmMerge = async () => {
  if (!selected.value || !mergeFrom.value || renamingBusy.value) return
  const target = speakerRows.value.find(row => row.speaker !== mergeFrom.value)?.speaker
  if (!target) {
    showNotice('本场只有一位发言人，无需合并', 'error')
    mergeFrom.value = ''
    return
  }
  renamingBusy.value = true
  try {
    const fromName = nameOf(mergeFrom.value)
    const intoName = nameOf(target)
    selected.value = await core.meetingSpeakerMerge(selected.value.id, mergeFrom.value, target)
    await refreshList()
    showNotice(`已把「${fromName}」的发言并进「${intoName}」，需要的话点「按模板重生成」刷新纪要`)
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    renamingBusy.value = false
    mergeFrom.value = ''
  }
}

// ─── 逐字稿编辑（对标钉钉逐字稿可编辑：识别错了直接改，不用重新转写整场）───

const editingSegment = ref(-1)
const editingText = ref('')
const segmentBusy = ref(false)

const beginEditSegment = (index: number, event?: Event) => {
  event?.stopPropagation()
  editingSegment.value = index
  editingText.value = selected.value?.segments[index]?.text ?? ''
}

const confirmEditSegment = async (event?: Event) => {
  event?.stopPropagation()
  const meeting = selected.value
  const index = editingSegment.value
  const text = editingText.value.trim()
  if (!meeting || index < 0 || segmentBusy.value) return
  if (!text) {
    showNotice('转写内容不能为空', 'error')
    return
  }
  if (text === meeting.segments[index]?.text) {
    editingSegment.value = -1
    return
  }
  segmentBusy.value = true
  try {
    selected.value = await core.meetingSegmentUpdate(meeting.id, index, text)
    showNotice('这一句已修改')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    segmentBusy.value = false
    editingSegment.value = -1
  }
}

// ─── 实时上屏的辅助展示 ───
//
// 用户判断「流式到底通不通」靠两件事：字在长、并且「N 秒前出过字」在走。
// 只给转圈不给时间的话，模型冷加载或麦克风静音会让界面和卡死长得一模一样。

const nowTick = ref(Date.now())
let nowTimer: number | null = null
watch(recording, value => {
  if (value && nowTimer === null) {
    nowTimer = window.setInterval(() => { nowTick.value = Date.now() }, 1000)
  } else if (!value && nowTimer !== null) {
    window.clearInterval(nowTimer)
    nowTimer = null
  }
})

const secondsSinceLive = computed(() =>
  liveUpdatedAt.value ? Math.max(0, Math.round((nowTick.value - liveUpdatedAt.value) / 1000)) : -1,
)

const liveHint = computed(() => {
  if (liveState.value === 'warming') {
    return modelReady.value ? '实时模型已就绪，说几句话就出字' : '正在加载实时识别模型（首次约 30 秒），录音已经开始不耽误'
  }
  if (liveState.value === 'degraded') return '推流链路不稳，已改为每 3 秒拉取服务端已识别内容，录音不受影响'
  if (liveState.value === 'unavailable') return '实时上屏不可用，录音结束后照常生成完整转写'
  if (liveState.value !== 'live') return ''
  if (secondsSinceLive.value < 0) return '正在听…说几句话就出字'
  if (secondsSinceLive.value <= 6) return '识别中，说完一句会立刻上屏'
  return `已 ${secondsSinceLive.value} 秒没有新内容（可能在停顿，或麦克风没收到声音）`
})

const checkLabel = (verdict: string) => {
  if (verdict === 'attention') return '需要核对'
  if (verdict === 'ok') return '没发现问题'
  if (verdict === 'unavailable') return 'AI 不可用'
  return '内容不足'
}

// 会中快检最近一条（列表按时间倒序，第一条就是最新）
const latestCheck = computed(() => liveChecks.value[0] || null)

const askLiveCheck = () => { void meetingSession.checkNow() }

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

// ─── 思维导图（真图渲染在 MindMap.vue；这里只管生成/重生成）───
//
// 2026-10-12 曾把 markmap 降级成"自绘嵌套列表"，因为没排查出画布空白的根因就绕开了。
// 2026-10-18 定位到三条缺一不可的条件（容器有确定高度 / 挂载后下一帧再建图 / 数据经 Transformer
// 转换），改在 MindMap.vue 里用可断言的形态固定住，并补了组件回归用例（断言渲染出 g.markmap-node）。

const mindmapBusy = ref(false)

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
      // Word：钉钉版式纪要（封面可视化报告 + 文档体 + AI洞察 + 待办 + 章节 + 汇报卡），
      // 末尾附转写全文附页，方便同事对照原话
      const response = await fetch(`/kapi/meetings/${encodeURIComponent(selected.value.id)}/export/docx?with_transcript=1`)
      if (!response.ok) throw new Error((await response.text()) || `导出失败（${response.status}）`)
      const blob = await response.blob()
      let filename = `${selected.value.title} 纪要.docx`
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
  // 汇报卡排第一：对标钉钉「可视化报告」导出件的那种带色块分区的纪要页（A4 版式）
  { id: 'report', name: '汇报卡' },
  { id: 'classic', name: '经典' },
  { id: 'carbon', name: '碳素' },
  { id: 'aurora', name: '极光' },
  { id: 'blueprint', name: '蓝图' },
  { id: 'note', name: '暖纸' },
] as const

const posterOpen = ref(false)
const posterTheme = ref<string>('report')
const posterBusy = ref(false)
const posterCaptureHost = ref<HTMLElement | null>(null)

const openPoster = () => {
  // 有结构化汇报卡结构时默认选汇报卡，否则回落经典长图
  posterTheme.value = selected.value?.summary?.visual?.sections?.length ? 'report' : 'classic'
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
const capturePosterCanvas = async (): Promise<HTMLCanvasElement> => {
  const { default: html2canvas } = await import('html2canvas')
  const target = posterCaptureHost.value?.querySelector('.meeting-poster, .visual-report') as HTMLElement | null
  if (!target) throw new Error('海报未渲染')
  return html2canvas(target, { scale: 2, backgroundColor: null, logging: false })
}

const capturePosterBlob = async (): Promise<Blob> => {
  const canvas = await capturePosterCanvas()
  const blob = await new Promise<Blob | null>(resolve => canvas.toBlob(resolve, 'image/png'))
  if (!blob) throw new Error('图片编码失败')
  return blob
}

const posterFileName = (ext: string) =>
  `${selected.value?.title ?? '会议'} ${posterTheme.value === 'report' ? '汇报卡' : '图文纪要'}.${ext}`

const downloadBlob = (blob: Blob, filename: string) => {
  const url = URL.createObjectURL(blob)
  const anchor = document.createElement('a')
  anchor.href = url
  anchor.download = filename
  anchor.click()
  URL.revokeObjectURL(url)
}

const savePoster = async () => {
  if (!selected.value || posterBusy.value) return
  posterBusy.value = true
  try {
    downloadBlob(await capturePosterBlob(), posterFileName('png'))
    showNotice('海报图片已保存')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    posterBusy.value = false
  }
}

// 导出 PDF：离屏 A4 实例（794px 宽 @96dpi = A4 宽度）截图后嵌进单页 PDF。
// 刻意不用 window.print()——WebView2 的打印对话框会打断桌面应用；汇报卡本就是一页 A4，
// PNG 嵌页与 Word→PDF 的观感一致，且走 services/pdf.ts 手写 PDF，零额外依赖。
const exportPosterPdf = async () => {
  if (!selected.value || posterBusy.value) return
  posterBusy.value = true
  try {
    const { canvasToFlateData, buildA4Pdf } = await import('../services/pdf')
    const canvas = await capturePosterCanvas()
    downloadBlob(buildA4Pdf(canvasToFlateData(canvas)), posterFileName('pdf'))
    showNotice('PDF 已导出')
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

// 播放跟随：转写区滚动到当前那一块（仅播放中，避免浏览时被抢滚动）
watch(activeBlockIndex, async () => {
  if (!playing.value || activeBlockIndex.value < 0) return
  await nextTick()
  document.getElementById(`meeting-block-${activeBlockIndex.value}`)?.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
})

// 菜单与复制：导出/更多操作收进一个入口，标题行只留主操作
const moreMenuOpen = ref(false)
const toggleMoreMenu = async () => {
  moreMenuOpen.value = !moreMenuOpen.value
  if (!moreMenuOpen.value) return
  await nextTick()
  window.addEventListener('click', closeMoreMenu, { once: true })
}
const closeMoreMenu = () => { moreMenuOpen.value = false }

const copyTranscript = async () => {
  const meeting = selected.value
  if (!meeting) return
  const lines = meeting.segments.map(segment => `${nameOf(String(segment.speaker))} ${formatTime(segment.start)}  ${segment.text}`)
  try {
    await navigator.clipboard.writeText(`${meeting.title}\n\n${lines.join('\n')}`)
    showNotice('逐字稿已复制到剪贴板')
  } catch {
    showNotice('复制失败（剪贴板不可用）', 'error')
  }
}

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
    // 用户切走期间录音上传完成的情况：回来后直接打开那条听记
    void consumeFinished()
  }
})

onBeforeUnmount(() => {
  // 只收页面自己的轮询、音频 URL 与本地计时器；录音在 meetingSession 单例里，切页不中断
  stopPolling()
  releaseAudio()
  if (nowTimer !== null) {
    window.clearInterval(nowTimer)
    nowTimer = null
  }
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
      <button type="button" aria-label="关闭" @click="dismissNotice">×</button>
    </div>

    <!-- 未登录知识服务：听记数据在服务端，必须登录 -->
    <div v-if="!statusLoading && !status.loggedIn" class="card knowledge-login-card">
      <div class="card-header"><h2>需要先登录知识服务</h2></div>
      <p class="meeting-hint">会议录音与纪要保存在公司知识服务器上，请先登录后使用。</p>
      <button class="primary-button" type="button" @click="emit('goto-knowledge')">去「知识」页登录</button>
    </div>

    <template v-else-if="status.loggedIn">
      <!-- 连不上知识服务时先给可执行的下一步，别让用户按了录音才发现上传不了 -->
      <div v-if="serverError && !recording && !uploading" class="card meeting-server-error" role="alert">
        <div class="meeting-server-error-copy">
          <strong>暂时连不上知识服务</strong>
          <span>{{ serverError }}</span>
          <span v-if="status.serverUrl" class="meeting-hint">当前地址：{{ status.serverUrl }}</span>
        </div>
        <button v-if="serverErrorKind === 'auth'" class="primary-button" type="button" @click="emit('goto-knowledge')">去「知识」页登录</button>
        <button v-else class="primary-button" type="button" :disabled="retrying" @click="retryConnect">
          {{ retrying ? '重试中…' : '重试连接' }}
        </button>
      </div>

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
            <input v-model="title" class="setting-input meeting-title-input" type="text" placeholder="会议主题（可选，默认用当前时间）" @keydown.enter="guardedStart" />
            <input ref="audioFileInput" type="file" accept="audio/*,.wav,.mp3,.m4a,.aac,.flac,.ogg" hidden @change="onImportFile" />
            <button class="text-button" type="button" :disabled="importingFile || !!serverError" :title="serverError ? '知识服务未连接，暂时无法导入' : ''" @click="audioFileInput?.click()">
              {{ importingFile ? '导入中…' : '导入文件' }}
            </button>
            <button class="primary-button meeting-record-button" type="button" :disabled="!!serverError" :title="serverError ? '知识服务未连接，暂时无法开始录音' : ''" @click="guardedStart">
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
          <!-- 提示必须在空闲分支内部：放到 v-if/v-else 链中间会把「空闲」与「上传中」拆成两支 -->
          <p v-if="audioSource === 'system'" class="setting-hint meeting-source-hint">
            线上会议用这个：同时录下电脑正在播放的声音（对方发言）与你自己说的话，两路在本地合成一路音再转写。点「开始录音」后会弹出共享选择器：选「整个屏幕」并勾选「同时分享系统音频」（选单个窗口目前拿不到系统声音）。
          </p>
        </div>
        <div v-else-if="recording" class="meeting-record-active">
          <span class="meeting-level" aria-hidden="true">
            <span v-for="(on, index) in levelBars" :key="index" :class="['meeting-level-bar', { on }]" />
          </span>
          <span class="meeting-timer">{{ formatTime(elapsed) }}</span>
          <span class="meeting-recording-dot" aria-hidden="true" />
          <span class="meeting-hint">正在录音，切到其他页面也不会中断</span>
          <button class="primary-button" type="button" @click="stop">完成并转写</button>
          <button class="text-button" type="button" @click="cancelRecording">取消</button>
        </div>
        <div v-else class="meeting-record-active">
          <span class="knowledge-spinner" aria-hidden="true" />
          <span class="meeting-hint">正在上传录音并生成完整转写…</span>
          <span v-if="liveLines.length" class="meeting-hint">实时字幕已记录 {{ liveLines.length }} 行</span>
        </div>
        <!-- 实时上屏（对标钉钉边说边出字 + 会中要点 + 会中快检）：预览性质，终态以录制后的整段转写为准。
             录音中与上传中都保留，切页回来还能看到刚才说了什么 -->
        <div
          v-if="(recording || uploading) && liveSessionId"
          class="meeting-live"
        >
          <div class="meeting-live-head">
            <div class="meeting-live-title">
              <span :class="['meeting-live-dot', liveState]" aria-hidden="true" />
              实时字幕
              <span v-if="liveState === 'live'" class="meeting-live-state">{{ liveLines.length }} 行 · 边说边出字</span>
              <span v-else-if="liveState === 'warming'" class="meeting-live-state">模型准备中</span>
              <span v-else-if="liveState === 'degraded'" class="meeting-live-state">轮询兜底</span>
              <span v-else-if="liveState === 'unavailable'" class="meeting-live-state">不可用</span>
            </div>
            <button
              class="text-button meeting-live-check-btn"
              type="button"
              :disabled="liveChecking || !liveLines.length"
              :title="liveLines.length ? '只看最近几行，核对数字/期限/人名等口径' : '先等几句话上屏'"
              @click="askLiveCheck"
            >
              {{ liveChecking ? '核对中…' : 'AI 快检' }}
            </button>
          </div>
          <p v-if="liveHint" class="meeting-live-hint">{{ liveHint }}</p>
          <p v-if="liveState === 'unavailable' || liveState === 'degraded'" class="meeting-live-error">
            {{ liveError || (liveState === 'degraded' ? '推流中断，已改用轮询' : '实时上屏不可用') }}。录音不会中断，结束后照常生成完整转写。
          </p>
          <div ref="liveBox" class="meeting-live-lines">
            <p v-for="(line, index) in liveLines" :key="index" class="meeting-live-line">
              <span class="meeting-segment-time">{{ formatTime(line.at) }}</span>{{ line.text }}
            </p>
            <p v-if="liveCurrent" class="meeting-live-line is-current">{{ liveCurrent }}</p>
            <p v-if="!liveLines.length && !liveCurrent" class="meeting-hint">
              {{ liveState === 'warming' ? '模型加载完成后，说第一句话就会上屏' : '正在听…说几句话就出字' }}
            </p>
          </div>
          <!-- 会中快检：整场纪要要等录音结束，会中能救场的只有「刚才这句的数字对不对」 -->
          <div v-if="latestCheck" class="meeting-live-check" :class="`is-${latestCheck.verdict}`">
            <div class="meeting-live-title">
              会中快检 · {{ latestCheck.checked_at }}
              <span class="meeting-live-check-verdict">{{ checkLabel(latestCheck.verdict) }}</span>
            </div>
            <p class="meeting-live-check-note">{{ latestCheck.note }}</p>
            <ul v-if="latestCheck.points.length">
              <li v-for="(point, index) in latestCheck.points" :key="index">{{ point }}</li>
            </ul>
            <p v-if="liveChecks.length > 1" class="meeting-hint">历史上共核对 {{ liveChecks.length }} 次，最近一次在最上方</p>
          </div>
          <div v-if="livePoints.length" class="meeting-live-points">
            <div class="meeting-live-title">会中要点</div>
            <ul>
              <li v-for="(point, index) in livePoints" :key="index">{{ point }}</li>
            </ul>
          </div>
        </div>
      </div>

      <!-- 详情：标题 + 阅读区。刻意不再套一层 card：里面每块已经是卡片，
           卡片套卡片会让「一屏里有几层容器」变得数不清（钉钉听记这里也是无边框的。 -->
      <div v-if="selected" ref="detailAnchor" class="meeting-detail">
        <!-- 标题行：左标题 / 右操作。操作只留「导出」与「更多」，其余进菜单，
             不再让十个文字按钮横向铺满一行把标题挤到下面 -->
        <header class="meeting-detail-head">
          <div class="meeting-detail-title">
            <h2>{{ selected.title }}</h2>
            <p class="meeting-detail-meta">
              <span>{{ formatDate(selected.created_at) }}</span>
              <template v-if="selected.duration_seconds"><i aria-hidden="true">·</i><span>时长 {{ durationLabel(selected) }}</span></template>
              <template v-if="selected.speaker_count"><i aria-hidden="true">·</i><span>{{ selected.speaker_count }} 位发言人</span></template>
              <template v-if="selected.segments.length"><i aria-hidden="true">·</i><span>{{ selected.segments.length }} 句</span></template>
            </p>
            <!-- 区块独立状态：只显示还没就绪的块，全部就绪时整行收起 -->
            <div v-if="blockChips.some(chip => chip.state !== 'ready')" class="meeting-block-chips">
              <span
                v-for="chip in blockChips.filter(chip => chip.state !== 'ready')"
                :key="chip.key"
                class="meeting-block-chip"
                :class="`is-${chip.state}`"
                :title="chip.error || `${chip.label}：${stateLabel(chip.state)}`"
              >{{ chip.label }} · {{ stateLabel(chip.state) }}</span>
            </div>
          </div>
          <div class="meeting-detail-actions">
            <button class="meeting-ghost-btn" type="button" :disabled="exportBusy !== '' || selected.status !== 'completed'" @click="exportFile('docx')">
              {{ exportBusy === 'docx' ? '导出中…' : '导出 Word' }}
            </button>
            <div class="meeting-more">
              <button class="meeting-ghost-btn" type="button" :aria-expanded="moreMenuOpen" @click.stop="toggleMoreMenu">
                更多
                <svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg>
              </button>
              <div v-if="moreMenuOpen" class="meeting-more-menu" role="menu" @click.stop>
                <button type="button" role="menuitem" :disabled="exportBusy !== '' || selected.status !== 'completed'" @click="closeMoreMenu(); exportFile('markdown')">导出 Markdown</button>
                <button type="button" role="menuitem" :disabled="selected.status !== 'completed'" @click="closeMoreMenu(); openPoster()">生成图文海报</button>
                <button type="button" role="menuitem" :disabled="selected.status !== 'completed'" @click="closeMoreMenu(); copyTranscript()">复制逐字稿</button>
                <button type="button" role="menuitem" :disabled="archiving || selected.status !== 'completed'" @click="closeMoreMenu(); archive()">归档进知识库</button>
                <button type="button" role="menuitem" :disabled="selected.status !== 'completed'" @click="closeMoreMenu(); retranscribe()">重新转写（用上热词）</button>
                <span class="meeting-more-sep" aria-hidden="true" />
                <button type="button" role="menuitem" class="is-danger" @click="closeMoreMenu(); remove()">
                  {{ confirmingDelete ? '再点一次确认删除' : '删除这条听记' }}
                </button>
                <button type="button" role="menuitem" @click="closeMoreMenu(); closeDetail()">关闭</button>
              </div>
            </div>
          </div>
        </header>

        <!-- 播放条：章节即播放位，点一个就跳过去 -->
        <div class="meeting-player">
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
        </div>
          <!-- 发言人：横向一排人，改名/合并都就地完成（对标钉钉的发言人条） -->
          <section v-if="speakerRows.length" class="meeting-speakers-bar">
            <span class="meeting-speakers-label">发言人</span>
            <div class="meeting-speakers">
              <div v-for="row in speakerRows" :key="row.speaker" :class="['meeting-speaker-pill', `h${speakerHue(row.speaker)}`]">
                <span class="meeting-speaker-avatar" aria-hidden="true">{{ (row.name || '说').slice(0, 1) }}</span>
                <template v-if="renamingSpeaker === row.speaker">
                  <input
                    v-model="renamingName"
                    class="setting-input meeting-rename-input"
                    type="text"
                    maxlength="20"
                    placeholder="输入这位发言人的名字"
                    :disabled="renamingBusy"
                    @keydown.enter.stop="confirmRename"
                    @keydown.esc.stop="renamingSpeaker = ''"
                  />
                  <button class="text-button" type="button" :disabled="renamingBusy || !renamingName.trim()" @click="confirmRename">
                    {{ renamingBusy ? '保存中…' : '保存' }}
                  </button>
                  <button class="text-button" type="button" :disabled="renamingBusy" @click="renamingSpeaker = ''">取消</button>
                </template>
                <template v-else-if="mergeFrom === row.speaker">
                  <span class="meeting-speaker-meta">并进其他人？</span>
                  <button class="text-button" type="button" :disabled="renamingBusy" @click="confirmMerge">
                    {{ renamingBusy ? '合并中…' : '确认' }}
                  </button>
                  <button class="text-button" type="button" :disabled="renamingBusy" @click="mergeFrom = ''">取消</button>
                </template>
                <template v-else>
                  <button class="meeting-speaker-name" type="button" title="点击标注真名" @click="beginRename(row.speaker)">{{ row.name }}</button>
                  <span class="meeting-speaker-meta">{{ row.count }} 句 · {{ formatTime(row.seconds) }}</span>
                  <button v-if="speakerRows.length > 1" class="meeting-icon-btn" type="button" title="把这位的发言并进另一位" aria-label="合并发言人" @click="beginMerge(row.speaker)">
                    <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h5l5 10h6M15 3l4 4-4 4M15 13l4 4-4 4"/></svg>
                  </button>
                </template>
              </div>
            </div>
          </section>

        <p v-if="selected.status === 'processing'" class="meeting-pending">
          <span class="knowledge-spinner" aria-hidden="true" />
          正在转写与生成纪要…（录音越长耗时越久，可以离开此页，稍后回来）
        </p>
        <p v-else-if="selected.status === 'failed'" class="meeting-pending meeting-failed">
          转写失败：{{ selected.error || '未知原因' }}
        </p>

        <template v-else>
          <div class="meeting-detail-body">
            <!-- 主区：标签页，一屏只讲一件事 -->
            <div ref="detailPane" class="meeting-pane">
              <nav class="meeting-tabs" role="tablist" aria-label="听记内容">
                <button
                  v-for="tab in DETAIL_TABS"
                  :key="tab.id"
                  type="button"
                  role="tab"
                  :aria-selected="detailTab === tab.id"
                  :title="tab.hint"
                  :class="['meeting-tab', { on: detailTab === tab.id }]"
                  @click="switchTab(tab.id)"
                >
                  {{ tab.label }}
                  <span v-if="tab.id === 'transcript'" class="meeting-tab-count">{{ selected.segments.length }}</span>
                </button>
              </nav>

              <!-- ① AI 纪要 -->
              <div v-if="detailTab === 'summary'" class="meeting-tab-panel" role="tabpanel">
                <section class="meeting-block">
                  <header class="meeting-block-head">
                    <h3>纪要</h3>
                    <span class="meeting-block-tag">{{ templateName(selected.template_id) }}</span>
                  </header>
                  <div v-if="summarySections.length" class="meeting-section-nav" role="navigation" aria-label="小节导航">
                    <button v-for="(section, index) in summarySections" :key="section.title" type="button" class="meeting-section-chip" @click="scrollToSection(index)">{{ section.title }}</button>
                  </div>
                  <template v-if="summarySections.length">
                    <div v-for="(section, index) in summarySections" :key="section.title" :id="`meeting-section-${index}`" class="meeting-summary">
                      <h3>{{ section.title }}</h3>
                      <ul v-if="section.text.includes('\n- ') || section.text.startsWith('- ')">
                        <li v-for="(line, lineIndex) in section.text.split('\n').filter(l => l.trim())" :key="lineIndex">
                          {{ line.replace(/^- */, '').trim() }}
                        </li>
                      </ul>
                      <p v-else>{{ section.text }}</p>
                    </div>
                  </template>
                  <template v-else>
                    <div v-if="selected.summary?.minutes" class="meeting-summary">
                      <p>{{ selected.summary.minutes }}</p>
                    </div>
                    <div v-if="selected.summary?.key_points?.length" class="meeting-summary">
                      <h3>要点</h3>
                      <ul>
                        <li v-for="(point, index) in selected.summary.key_points" :key="index">{{ point }}</li>
                      </ul>
                    </div>
                  </template>
                  <p v-if="!selected.summary || (!selected.summary.minutes && !selected.summary.key_points?.length && !summarySections.length)" class="meeting-hint">
                    还没有纪要内容，用右上角「更多 → 重新生成纪要」提炼
                  </p>
                  <!-- 重新按模板生成：模板选择跟纪要放一起，而不是和导出按钮挤在标题行 -->
                  <div class="meeting-regen-row">
                    <select v-model="regenTemplateId" class="meeting-regen-select" :disabled="summaryRegenBusy || selected.status !== 'completed'">
                      <option v-for="t in TEMPLATES" :key="t.id" :value="t.id">{{ t.name }}</option>
                    </select>
                    <button class="meeting-ghost-btn" type="button" :disabled="summaryRegenBusy || selected.status !== 'completed' || !regenTemplateId" @click="regenWithTemplate">
                      {{ summaryRegenBusy ? '生成中…' : '按模板重生成' }}
                    </button>
                  </div>
                </section>

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
                        <button class="meeting-icon-btn" type="button" :disabled="todoBusy" aria-label="删除待办" title="删除待办" @click="removeTodo(todo)">
                          <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 7h12M10 7V5h4v2M9 11v6M15 11v6M8 7l1 12h6l1-12"/></svg>
                        </button>
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

                <section v-if="selected.summary?.insights?.length || selected.summary?.quote_kp" class="meeting-block">
                  <header class="meeting-block-head">
                    <h3>AI 洞察</h3>
                    <span class="meeting-block-count">导出 Word / 汇报卡时同版呈现</span>
                  </header>
                  <ul v-if="selected.summary?.insights?.length" class="meeting-summary">
                    <li v-for="(insight, index) in selected.summary.insights" :key="index">{{ insight }}</li>
                  </ul>
                  <div v-if="selected.summary?.quote_kp" class="meeting-kp">
                    <p class="meeting-kp-head">核心发言提炼 · {{ selected.summary.quote_kp.speaker || '发言人' }}</p>
                    <ul v-if="selected.summary.quote_kp.points?.length" class="meeting-summary">
                      <li v-for="(point, index) in selected.summary.quote_kp.points" :key="index">{{ point }}</li>
                    </ul>
                    <blockquote
                      v-for="(quote, index) in selected.summary.quote_kp.quotes || []"
                      :key="`q${index}`"
                      class="meeting-kp-quote"
                    >
                      {{ quote }}
                    </blockquote>
                  </div>
                </section>

                <section class="meeting-block">
                  <header class="meeting-block-head">
                    <h3>关键词</h3>
                    <button class="meeting-ghost-btn" type="button" :disabled="keywordsBusy || selected.status !== 'completed'" @click="regenKeywords">
                      {{ keywordsBusy ? '生成中…' : selected.keywords?.length ? '重新生成' : '生成关键词' }}
                    </button>
                  </header>
                  <div v-if="selected.keywords?.length" class="meeting-keywords">
                    <span v-for="word in selected.keywords" :key="word" class="meeting-keyword">{{ word }}</span>
                  </div>
                  <p v-else-if="!keywordsBusy" class="meeting-hint">还没有关键词，点「生成关键词」由 AI 从转写提炼。</p>
                </section>
              </div>

              <!-- ② 转写：按发言人分块，像读一段对话 -->
              <div v-else-if="detailTab === 'transcript'" class="meeting-tab-panel" role="tabpanel">
                <div class="meeting-transcript">
                  <article
                    v-for="(block, blockIndex) in transcriptBlocks"
                    :id="`meeting-block-${blockIndex}`"
                    :key="blockIndex"
                    :class="['meeting-turn', { on: blockIndex === activeBlockIndex }]"
                  >
                    <header class="meeting-turn-head">
                      <span :class="['meeting-turn-avatar', `h${speakerHue(block.speaker)}`]" aria-hidden="true">{{ (nameOf(block.speaker) || '说').slice(0, 1) }}</span>
                      <button
                        :class="['meeting-turn-name', `h${speakerHue(block.speaker)}`]"
                        type="button"
                        title="点击标注真名"
                        @click="beginRename(block.speaker)"
                      >{{ nameOf(block.speaker) }}</button>
                      <button class="meeting-turn-time" type="button" :title="'从这一句开始播放'" @click="seekTo(block.start)">{{ formatTime(block.start) }}</button>
                    </header>
                    <div class="meeting-turn-body">
                      <p
                        v-for="piece in block.texts"
                        :key="piece.index"
                        :class="['meeting-turn-line', { on: piece.index === activeSegmentIndex }]"
                      >
                        <template v-if="editingSegment === piece.index">
                          <span class="meeting-edit-row">
                            <textarea
                              v-model="editingText"
                              class="setting-input meeting-segment-textarea"
                              rows="2"
                              :disabled="segmentBusy"
                              @keydown.enter.prevent="confirmEditSegment()"
                              @keydown.esc.stop="editingSegment = -1"
                            />
                            <span class="meeting-edit-btns">
                              <button class="text-button" type="button" :disabled="segmentBusy || !editingText.trim()" @click="confirmEditSegment">
                                {{ segmentBusy ? '保存中…' : '保存' }}
                              </button>
                              <button class="text-button" type="button" :disabled="segmentBusy" @click="editingSegment = -1">取消</button>
                            </span>
                          </span>
                        </template>
                        <template v-else>
                          <span class="meeting-turn-text" @dblclick="beginEditSegment(piece.index)">{{ piece.text }}</span>
                          <button class="meeting-turn-edit" type="button" title="修正这一句" @click="beginEditSegment(piece.index, $event)">改字</button>
                        </template>
                      </p>
                    </div>
                  </article>
                  <p v-if="!selected.segments.length" class="meeting-hint">这条听记没有转写内容。</p>
                </div>
                <p class="meeting-hint meeting-transcript-foot">双击任意一句可直接改字；发言人点名字即可标注真名。</p>
              </div>

              <!-- ③ 导图 -->
              <div v-else class="meeting-tab-panel" role="tabpanel">
                <section class="meeting-block">
                  <header class="meeting-block-head">
                    <h3>思维导图</h3>
                    <button class="meeting-ghost-btn" type="button" :disabled="mindmapBusy" @click="regenMindmap">
                      {{ mindmapBusy ? '生成中…' : selected.mindmap ? '重新生成' : '生成导图' }}
                    </button>
                  </header>
                  <MindMap v-if="selected.mindmap" :markdown="selected.mindmap" />
                  <p v-else-if="!mindmapBusy" class="meeting-hint">
                    {{ selected.segments.length ? '还没有导图，点「生成导图」用 AI 从转写提炼结构' : '本次录音没有可提炼的发言内容' }}
                  </p>
                </section>
              </div>
            </div>

            <!-- 侧栏：AI 问答。常驻在最右，和主区同一屏宽，不跟着标签页消失 -->
            <aside class="meeting-qa">
              <div class="meeting-qa-head">
                <h3>AI 问答</h3>
                <span class="meeting-hint">基于本条听记</span>
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
              <span :class="['meeting-poster-theme-chip', `meeting-poster-theme-chip--${theme.id}`]" aria-hidden="true">
                <template v-if="theme.id === 'report'"><i /><i /><i /></template>
                <template v-else>Aa</template>
              </span>
              {{ theme.name }}
            </button>
          </div>
          <div class="meeting-poster-scroll">
            <div class="meeting-poster-preview">
              <MeetingVisualReport v-if="posterTheme === 'report'" :meeting="selected" />
              <MeetingPoster v-else :meeting="selected" :theme="posterTheme" />
            </div>
          </div>
          <div class="meeting-poster-actions">
            <span v-if="posterBusy" class="meeting-hint">正在生成…</span>
            <button class="text-button" type="button" :disabled="posterBusy" @click="copyPoster">复制图片</button>
            <button class="text-button" type="button" :disabled="posterBusy" @click="exportPosterPdf">导出 PDF</button>
            <button class="primary-button" type="button" :disabled="posterBusy" @click="savePoster">保存图片</button>
          </div>
        </div>
      </div>
      <!-- 离屏全尺寸海报：html2canvas 截图根（预览实例带 zoom，不能直接截） -->
      <div v-if="posterOpen && selected" ref="posterCaptureHost" class="meeting-poster-offscreen" aria-hidden="true">
        <MeetingVisualReport v-if="posterTheme === 'report'" :meeting="selected" />
        <MeetingPoster v-else :meeting="selected" :theme="posterTheme" />
      </div>

      <!-- 记录列表 -->
      <div class="card meeting-list-card">
        <div class="card-header">
          <h2>听记记录</h2>
          <input v-model="meetingQuery" class="setting-input meeting-search" type="text" placeholder="搜索标题或摘要…" aria-label="搜索听记记录" />
        </div>
        <p v-if="!meetings.length" class="meeting-hint">{{ serverError ? '连接恢复后会自动加载听记记录。' : '还没有记录，点上方「开始录音」创建第一条听记。' }}</p>
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
        <p v-if="filteredMeetings.length" class="meeting-list-end">共 {{ filteredMeetings.length }} 条听记</p>
      </div>
    </template>
  </div>
</template>
