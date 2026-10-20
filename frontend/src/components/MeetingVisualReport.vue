<script setup lang="ts">
import { computed } from 'vue'
import type { MeetingRecord, MeetingVisualItem, MeetingVisualSection } from '../types/core'

// 汇报卡（对标钉钉 AI 听记「可视化报告」）：把纪要渲染成 A4 版式的封面。
// 版式来源为用户提供的钉钉原生导出件逐页实测（见 docx_export.py 头注释）：
//   「纪要」24pt 黑 → 主题/时间/参与人 12pt → 会议标题 18pt → 灰色一句话主题
//   → 细分隔线 → 01-06 带序号彩色模块（并列色卡 / 两栏 / 清单 / 单条共识）
//   → 页脚水印。卡片 = 色块+小标题+标签 + 圆点要点 + 底色小结条。
// 纯 CSS 变量上色，与 html2canvas/打印都可截图；不引入任何图片资产。

const props = defineProps<{ meeting: MeetingRecord }>()

// 与后端 docx_export.ACCENTS 同一套色（01/05 同为玫红，故按固定序列轮转）
const ACCENTS: Record<string, { line: string; bg: string; band: string }> = {
  rose: { line: '#e8557a', bg: '#fdf1f4', band: '#f9d5de' },
  blue: { line: '#3b78d8', bg: '#eff4fc', band: '#d5e3f7' },
  teal: { line: '#1e8e88', bg: '#e8f5f4', band: '#cfeae7' },
  amber: { line: '#c98a1e', bg: '#fdf6e8', band: '#f5e3be' },
  green: { line: '#1e8e62', bg: '#eaf6f0', band: '#cdebdc' },
}
const ACCENT_ORDER = ['rose', 'blue', 'teal', 'amber', 'rose', 'green'] as const

const summary = computed(() => props.meeting.summary || null)
const visualSummary = computed(() => summary.value?.visual?.summary || '')

// 老记录（重生成前）没有 visual：按 minutes/key_points/sections/todos 就地折叠，保证永远有卡可画
const sections = computed<MeetingVisualSection[]>(() => {
  const raw = summary.value?.visual?.sections
  if (raw?.length) return raw
  const fallback: MeetingVisualSection[] = []
  const summaryValue = summary.value
  if (summaryValue?.sections?.length) {
    for (const section of summaryValue.sections) {
      fallback.push({
        title: section.title,
        layout: 'lists',
        items: [{ title: '', bullets: section.text.split('\n').map(line => line.replace(/^[-•*]\s*/, '').trim()).filter(Boolean) }],
      })
    }
  }
  if (summaryValue?.key_points?.length) {
    fallback.push({ title: '要点', layout: 'lists', items: [{ title: '', bullets: summaryValue.key_points }] })
  }
  if (summaryValue?.minutes) {
    fallback.unshift({ title: '会议纪要', layout: 'closing', items: [{ title: '', note: summaryValue.minutes }] })
  }
  if (props.meeting.todos?.length) {
    fallback.push({ title: '待办', layout: 'lists', items: [{ title: '', bullets: props.meeting.todos.map(todo => todo.task) }] })
  }
  return fallback
})

const accentOf = (section: MeetingVisualSection, index: number) =>
  ACCENTS[section.accent || ''] || ACCENTS[ACCENT_ORDER[index % ACCENT_ORDER.length]]

const attendees = computed(() => {
  const names = Object.values(props.meeting.speaker_names || {})
    .filter(name => name && !/^说话人 \d+$/.test(name))
  return [...new Set(names)]
})

const createdLabel = computed(() => {
  const raw = (props.meeting.created_at || '').replace('T', ' ')
  return raw.slice(0, 19)
})

const columns = (items: MeetingVisualItem[]) => {
  // flow 两列按顺序切开（左=第一阶段、右=第二阶段），而不是奇偶交替
  const half = Math.ceil(items.length / 2)
  return [items.slice(0, half), items.slice(half)]
}
</script>

<template>
  <div class="visual-report">
    <p class="visual-report-kicker">纪要</p>
    <p class="visual-report-meta">
      主题: {{ meeting.title }}<span class="visual-report-gap" />时间: {{ createdLabel }}<span class="visual-report-gap" />参与人: {{ attendees.join('、') || '待补充' }}
    </p>
    <h1 class="visual-report-title">{{ meeting.title }}</h1>
    <p v-if="visualSummary" class="visual-report-sub">{{ visualSummary }}</p>
    <div class="visual-report-rule" />

    <section
      v-for="(section, index) in sections"
      :key="section.title + index"
      class="visual-report-block"
      :style="{
        '--vr-line': accentOf(section, index).line,
        '--vr-bg': accentOf(section, index).bg,
        '--vr-band': accentOf(section, index).band,
      }"
    >
      <p class="visual-report-block-title">
        <span class="visual-report-num">{{ String(index + 1).padStart(2, '0') }}</span>
        <span class="visual-report-bar" aria-hidden="true">▍</span>{{ section.title }}
      </p>

      <div
        v-if="section.layout !== 'flow' && section.layout !== 'closing'"
        :class="[
          'visual-report-cards',
          `is-${section.layout || 'cards'}`,
          { 'is-single': section.items.length === 1 },
        ]"
      >
        <div
          v-for="(item, itemIndex) in section.items"
          :key="itemIndex"
          class="visual-report-card"
          :class="{ 'is-closing': section.layout === 'closing' && !item.title }"
        >
          <p v-if="item.title || item.tag" class="visual-report-card-head">
            <span class="visual-report-dot" aria-hidden="true" />
            <span v-if="item.title" class="visual-report-card-title">{{ item.title }}</span>
            <span v-if="item.tag" class="visual-report-tag">{{ item.tag }}</span>
          </p>
          <ul v-if="item.bullets?.length" class="visual-report-bullets">
            <li v-for="(bullet, bulletIndex) in item.bullets" :key="bulletIndex">{{ bullet }}</li>
          </ul>
          <p v-if="item.note" class="visual-report-note">{{ item.note }}</p>
        </div>
      </div>

      <!-- flow：左右进程与节点（钉钉 04 实施路径那种双阶段卡） -->
      <div v-if="section.layout === 'flow'" class="visual-report-flow">
        <div v-for="(col, colIndex) in columns(section.items)" :key="colIndex" class="visual-report-flow-col">
          <div v-for="item in col" :key="item.title" class="visual-report-card">
            <p class="visual-report-card-head">
              <span class="visual-report-dot" aria-hidden="true" />
              <span class="visual-report-card-title">{{ item.title }}</span>
            </p>
            <ul v-if="item.bullets?.length" class="visual-report-bullets">
              <li v-for="(bullet, bulletIndex) in item.bullets" :key="bulletIndex">{{ bullet }}</li>
            </ul>
            <p v-if="item.note" class="visual-report-note">{{ item.note }}</p>
          </div>
        </div>
        <span v-if="section.items.length > 1" class="visual-report-arrow" aria-hidden="true">→</span>
      </div>

      <!-- closing：整行单条共识（钉钉 06 核心共识那种满宽色带） -->
      <div v-if="section.layout === 'closing'" class="visual-report-closing">
        <p v-for="(item, itemIndex) in section.items" :key="itemIndex" class="visual-report-note">
          {{ item.note || item.bullets?.join('；') }}
        </p>
      </div>
    </section>

    <p class="visual-report-foot">内容由 EasyShare 会议听记生成</p>
  </div>
</template>
