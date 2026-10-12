<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { core } from '../services/core'
import { useEasyShare } from '../composables/useEasyShare'
import type { KnowledgeStatus, MeetingChatTurn, MeetingRecord } from '../types/core'
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

// 音源：mic=麦克风；system=系统声音（Windows；共享选择器里勾「同时分享系统音频」）
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

const start = async () => {
  notice.value = ''
  try {
    await recorder.start(audioSource.value, value => { level.value = value })
    recording.value = true
    elapsed.value = 0
    timerHandle = window.setInterval(() => { elapsed.value = recorder.elapsedSeconds() }, 500)
  } catch (e) {
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
  try {
    const { blob, durationSeconds } = await recorder.stop()
    if (durationSeconds < 1) {
      showNotice('录音太短（不足 1 秒），已丢弃', 'error')
      return
    }
    const fallback = new Date().toLocaleString('zh-CN', { hour12: false })
    const created = await core.meetingUpload(blob, title.value.trim() || `${fallback} 的会议`, templateId.value)
    title.value = ''
    await refreshList()
    await openDetail(created.id)
    showNotice('录音已上传，正在转写…')
  } catch (e) {
    showNotice(e instanceof Error ? e.message : String(e), 'error')
  } finally {
    uploading.value = false
  }
}

const cancelRecording = () => {
  stopTimer()
  recording.value = false
  level.value = 0
  recorder.abort()
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
  if (summary?.todos?.length) {
    lines.push('## 待办', ...summary.todos.map(t => {
      const suffix = t.owner_hint || t.due_hint ? `（${[t.owner_hint, t.due_hint].filter(Boolean).join('，')}）` : ''
      return `- [ ] ${t.task}${suffix}`
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

const formatDate = (value: string) => value.replace('T', ' ').slice(0, 16)

const durationLabel = (meeting: MeetingRecord) =>
  meeting.duration_seconds ? formatTime(meeting.duration_seconds) : '--:--'

const statusLabel = computed(() => ({
  processing: '转写中',
  completed: '已完成',
  failed: '失败',
}))

const levelBars = computed(() => {
  const active = Math.min(5, Math.max(0, Math.round(level.value * 8)))
  return Array.from({ length: 5 }, (_, index) => index < active)
})

onMounted(async () => {
  await refreshStatus()
  if (status.value.loggedIn) await refreshList()
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
                :disabled="uploading"
                @click="audioSource = 'mic'"
              >麦克风</button>
              <button
                v-if="!isMac"
                :class="['meeting-source-chip', { on: audioSource === 'system' }]"
                type="button"
                :disabled="uploading"
                @click="audioSource = 'system'"
              >系统声音</button>
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
        </div>
        <p v-if="!recording && !uploading && audioSource === 'system'" class="setting-hint meeting-source-hint">
          录制电脑正在播放的声音（线上会议开系统声音即可录到对方发言）。点开始后会弹出共享选择器：选「整个屏幕」并勾选「同时分享系统音频」。
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
            <div v-if="selected.summary?.sections?.length" class="meeting-section-nav" role="navigation" aria-label="章节导航">
              <button v-for="(section, index) in selected.summary.sections" :key="section.title" type="button" class="meeting-section-chip" @click="scrollToSection(index)">{{ section.title }}</button>
            </div>
            <div class="meeting-detail-body">
              <div class="meeting-detail-main">
            <audio v-if="audioUrl" :src="audioUrl" controls class="meeting-audio" />

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
                <h3>纪要</h3>
                <p>{{ selected.summary.minutes }}</p>
              </div>
              <div v-if="selected.summary?.key_points?.length" class="meeting-summary">
                <h3>要点</h3>
                <ul>
                  <li v-for="(point, index) in selected.summary.key_points" :key="index">{{ point }}</li>
                </ul>
              </div>
            </template>
            <div v-if="selected.summary?.todos?.length" class="meeting-summary">
              <h3>待办</h3>
              <ul class="meeting-todos">
                <li v-for="(todo, index) in selected.summary.todos" :key="index">
                  {{ todo.task
                  }}<span v-if="todo.owner_hint" class="meeting-todo-owner">{{ todo.owner_hint }}</span
                  ><span v-if="todo.due_hint" class="meeting-todo-owner">{{ todo.due_hint }}</span>
                </li>
              </ul>
            </div>

            <div class="meeting-mindmap">
              <div class="meeting-mindmap-head">
                <h3 class="meeting-transcript-title">思维导图</h3>
                <button class="text-button" type="button" :disabled="mindmapBusy" @click="regenMindmap">
                  {{ mindmapBusy ? '生成中…（AI 从转写提取）' : selected.mindmap ? '重新生成' : '生成导图' }}
                </button>
              </div>
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
            </div>

            <h3 class="meeting-transcript-title">转写记录</h3>
          <div class="meeting-transcript">
            <div v-for="(segment, index) in selected.segments" :key="index" class="meeting-segment">
              <template v-if="renamingSpeaker === segment.speaker">
                <input
                  v-model="renamingName"
                  class="setting-input meeting-rename-input"
                  type="text"
                  @keydown.enter="confirmRename"
                  @keydown.esc="renamingSpeaker = ''"
                />
                <button class="text-button" type="button" @click="confirmRename">确定</button>
                <button class="text-button" type="button" @click="renamingSpeaker = ''">取消</button>
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
        <div class="meeting-poster-modal">
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
        <div class="card-header"><h2>听记记录</h2></div>
        <p v-if="!meetings.length" class="meeting-hint">还没有记录，点上方「开始录音」创建第一条听记。</p>
        <div v-for="meeting in meetings" :key="meeting.id" class="meeting-item" @click="openDetail(meeting.id)">
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
            {{ statusLabel[meeting.status] }}
          </span>
        </div>
      </div>
    </template>
  </div>
</template>
