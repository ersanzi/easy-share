<script setup lang="ts">
import { computed } from 'vue'
import type { MeetingRecord } from '../types/core'

// 会议图文海报：把纪要/要点/待办渲染为可分享的单页长图（720px 宽）。
// 皮肤为纯 CSS 主题变量（hex/rgba + linear-gradient），刻意规避 html2canvas
// 不支持的 oklch/box-shadow；截图导出由父组件对离屏全尺寸实例执行。

const props = defineProps<{ meeting: MeetingRecord; theme: string }>()

interface PosterSection { title: string; paragraphs: string[]; bullets: string[] }

// summary 双形状归一：模板 sections 结构 ↔ 默认模板 minutes/key_points
const sections = computed<PosterSection[]>(() => {
  const summary = props.meeting.summary
  if (!summary) return []
  const toSection = (title: string, text: string): PosterSection => {
    const paragraphs: string[] = []
    const bullets: string[] = []
    for (const rawLine of String(text).split(/\r?\n/)) {
      const line = rawLine.trim()
      if (!line) continue
      if (/^[-•*·]\s+/.test(line)) bullets.push(line.replace(/^[-•*·]\s+/, ''))
      else paragraphs.push(line)
    }
    return { title, paragraphs, bullets }
  }
  if (summary.sections?.length) {
    return summary.sections.map(section => toSection(section.title, section.text))
  }
  const normalized: PosterSection[] = []
  if (summary.minutes) normalized.push(toSection('会议纪要', summary.minutes))
  if (summary.key_points?.length) {
    normalized.push({ title: '关键要点', paragraphs: [], bullets: [...summary.key_points] })
  }
  return normalized
})

const todos = computed(() => props.meeting.summary?.todos ?? [])

// 已改名的发言人展示在头部（「说话人 N」默认名不占版面）
const attendees = computed(() => {
  const names = Object.values(props.meeting.speaker_names || {})
    .filter(name => name && !/^说话人 \d+$/.test(name))
  return [...new Set(names)]
})

const dateLabel = computed(() => props.meeting.created_at.replace('T', ' ').slice(0, 16))

const durationLabel = computed(() => {
  const total = Math.floor(props.meeting.duration_seconds || 0)
  return `${String(Math.floor(total / 60)).padStart(2, '0')}:${String(total % 60).padStart(2, '0')}`
})
</script>

<template>
  <div :class="['meeting-poster', `meeting-poster--${theme}`]">
    <header class="meeting-poster-head">
      <p class="meeting-poster-brand"><span class="meeting-poster-brand-dot" aria-hidden="true" />EasyShare · 会议听记</p>
      <h1 class="meeting-poster-title">{{ meeting.title }}</h1>
      <p class="meeting-poster-meta">
        {{ dateLabel }}
        <template v-if="meeting.duration_seconds"> · 时长 {{ durationLabel }}</template>
        <template v-if="meeting.speaker_count"> · {{ meeting.speaker_count }} 位发言人</template>
      </p>
      <p v-if="attendees.length" class="meeting-poster-meta">发言人：{{ attendees.join('、') }}</p>
    </header>
    <main class="meeting-poster-body">
      <p v-if="!sections.length && !todos.length" class="meeting-poster-empty">本次会议还没有可生成的纪要内容</p>
      <section v-for="section in sections" :key="section.title" class="meeting-poster-card">
        <h2 class="meeting-poster-card-title">{{ section.title }}</h2>
        <p v-for="(paragraph, index) in section.paragraphs" :key="`p${index}`" class="meeting-poster-text">{{ paragraph }}</p>
        <ul v-if="section.bullets.length" class="meeting-poster-list">
          <li v-for="(bullet, index) in section.bullets" :key="`b${index}`">{{ bullet }}</li>
        </ul>
      </section>
      <section v-if="todos.length" class="meeting-poster-card">
        <h2 class="meeting-poster-card-title">待办事项</h2>
        <ul class="meeting-poster-todos">
          <li v-for="(todo, index) in todos" :key="index">
            <span class="meeting-poster-check" aria-hidden="true" />
            <span class="meeting-poster-todo-task">{{ todo.task }}</span>
            <span v-if="todo.owner_hint" class="meeting-poster-tag">{{ todo.owner_hint }}</span>
            <span v-if="todo.due_hint" class="meeting-poster-tag">{{ todo.due_hint }}</span>
          </li>
        </ul>
      </section>
    </main>
    <footer class="meeting-poster-foot">EasyShare 会议听记 · 图文纪要</footer>
  </div>
</template>
