<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { core } from '../services/core'
import type { KnowledgeContext, KnowledgeHealth, KnowledgeStatus } from '../types/core'

// 知识问答面板：Core 作网关，令牌不进前端。
// 状态自包含（同 SettingsPanel 模式）：登录态/健康度/会话消息均为本视图私有。

interface ChatMessage {
  role: 'user' | 'assistant'
  text: string
  contexts?: KnowledgeContext[]
  pending?: boolean
  failed?: boolean
}

const status = ref<KnowledgeStatus>({ configured: false, loggedIn: false, serverUrl: '', username: '', role: '' })
const health = ref<KnowledgeHealth | null>(null)
const statusLoading = ref(true)

const serverUrl = ref('')
const username = ref('')
const password = ref('')
const loginBusy = ref(false)
const loginError = ref('')

const messages = ref<ChatMessage[]>([])
const draft = ref('')
const asking = ref(false)
const listRef = ref<HTMLElement | null>(null)

const llmLabel = computed(() => health.value?.llm === 'configured' ? 'AI 生成已启用' : health.value?.llm === 'absent' ? '未配置 AI（仅检索）' : '')

const refreshStatus = async () => {
  try {
    status.value = await core.knowledgeStatus()
  } catch (e) {
    loginError.value = e instanceof Error ? e.message : String(e)
  } finally {
    statusLoading.value = false
  }
}

const probeHealth = async () => {
  if (!status.value.loggedIn) {
    health.value = null
    return
  }
  try {
    health.value = await core.knowledgeHealth()
  } catch {
    health.value = null
  }
}

// 已配置过服务器地址时回填登录表单
watch(() => status.value.serverUrl, value => {
  if (value && !serverUrl.value) serverUrl.value = value
}, { immediate: true })

// 登录态被 Core 清掉（令牌失效/主动退出）时清空会话消息
watch(() => status.value.loggedIn, value => {
  if (!value) messages.value = []
})

const submitLogin = async () => {
  if (loginBusy.value) return
  loginError.value = ''
  if (!serverUrl.value.trim() || !username.value.trim() || !password.value) {
    loginError.value = '请填写服务器地址、账号与密码'
    return
  }
  loginBusy.value = true
  try {
    status.value = await core.knowledgeLogin(serverUrl.value.trim(), username.value.trim(), password.value)
    password.value = ''
    await probeHealth()
  } catch (e) {
    loginError.value = e instanceof Error ? e.message : String(e)
  } finally {
    loginBusy.value = false
  }
}

const logout = async () => {
  try {
    await core.knowledgeLogout()
  } catch (e) {
    loginError.value = e instanceof Error ? e.message : String(e)
  }
  await refreshStatus()
}

const scrollToBottom = async () => {
  await nextTick()
  if (listRef.value) listRef.value.scrollTop = listRef.value.scrollHeight
}

const ask = async () => {
  const question = draft.value.trim()
  if (!question || asking.value || !status.value.loggedIn) return
  draft.value = ''
  messages.value.push({ role: 'user', text: question })
  messages.value.push({ role: 'assistant', text: '', pending: true })
  asking.value = true
  await scrollToBottom()
  let answer = null
  let failMessage = ''
  try {
    answer = await core.knowledgeAsk(question)
  } catch (e) {
    // Core 网关返回的错误信息（如"登录已失效""无法连接知识服务器"）可直接展示
    failMessage = e instanceof Error && e.message ? e.message : ''
  }
  const last = messages.value[messages.value.length - 1]
  if (answer) {
    last.text = answer.answer
    last.contexts = answer.contexts
  } else {
    last.failed = true
    last.text = failMessage || '回答失败，请稍后重试。'
  }
  last.pending = false
  asking.value = false
  // 令牌失效时 Core 已清会话：刷新登录态回到登录页
  await refreshStatus()
  await scrollToBottom()
}

const formatDate = (value?: string | null) => {
  if (!value) return ''
  return value.replace('T', ' ').slice(0, 16)
}

const snippet = (text: string) => (text.length > 160 ? text.slice(0, 160) + '…' : text)

const scorePercent = (score?: number | null) => (score == null ? '' : `${Math.round(score * 100)}%`)

onMounted(async () => {
  await refreshStatus()
  await probeHealth()
})
</script>

<template>
  <div class="knowledge-panel">
    <header class="workspace-header">
      <div>
        <span class="section-label">知识</span>
        <h1>知识问答</h1>
        <p>向公司知识库提问，答案附带引用来源，文件放进去就能被检索。</p>
      </div>
    </header>

    <div v-if="loginError" class="alert" role="alert">
      <svg viewBox="0 0 24 24" aria-hidden="true"><path d="M12 9v4m0 4h.01M10 3.8 2.6 17a2 2 0 0 0 1.8 3h15.2a2 2 0 0 0 1.8-3L14 3.8a2.3 2.3 0 0 0-4 0Z"/></svg>
      <span class="alert-copy"><b>{{ loginError }}</b></span>
      <button type="button" aria-label="关闭" @click="loginError = ''">×</button>
    </div>

    <!-- 登录表单：未配置或未登录 -->
    <div v-if="!statusLoading && !status.loggedIn" class="card knowledge-login-card">
      <div class="card-header">
        <h2>连接公司知识服务器</h2>
      </div>
      <form class="knowledge-login-form" @submit.prevent="submitLogin">
        <div class="setting-row">
          <label class="setting-label" for="knowledgeServer">服务器地址</label>
          <p class="setting-hint">由公司管理员提供，形如 http://192.168.1.10:8000</p>
          <input
            id="knowledgeServer"
            v-model="serverUrl"
            class="setting-input"
            type="text"
            autocomplete="url"
            placeholder="http://192.168.1.10:8000"
          />
        </div>
        <div class="setting-row">
          <label class="setting-label" for="knowledgeUser">账号</label>
          <input
            id="knowledgeUser"
            v-model="username"
            class="setting-input"
            type="text"
            autocomplete="username"
            placeholder="你的账号"
          />
        </div>
        <div class="setting-row">
          <label class="setting-label" for="knowledgePassword">密码</label>
          <input
            id="knowledgePassword"
            v-model="password"
            class="setting-input"
            type="password"
            autocomplete="current-password"
            placeholder="你的密码"
          />
        </div>
        <button class="primary-button knowledge-login-submit" type="submit" :disabled="loginBusy">
          {{ loginBusy ? '正在连接…' : '登录' }}
        </button>
      </form>
    </div>

    <!-- 问答区：已登录 -->
    <div v-else-if="status.loggedIn" class="card knowledge-chat-card">
      <div class="knowledge-chat-header">
        <div class="knowledge-chat-meta">
          <strong>{{ status.username }}</strong>
          <span class="knowledge-chat-server">{{ status.serverUrl }}</span>
        </div>
        <div class="knowledge-chat-badges">
          <span v-if="health" class="knowledge-badge">{{ health.records }} 份文档</span>
          <span v-if="llmLabel" class="knowledge-badge">{{ llmLabel }}</span>
          <button class="text-button" type="button" @click="logout">退出登录</button>
        </div>
      </div>

      <div ref="listRef" class="knowledge-message-list" role="log" aria-live="polite">
        <p v-if="!messages.length" class="knowledge-empty">
          问一个问题试试，例如「出差住宿标准是多少？」
        </p>
        <div v-for="(message, index) in messages" :key="index" :class="['knowledge-message', message.role]">
          <div v-if="message.role === 'user'" class="knowledge-bubble">{{ message.text }}</div>
          <div v-else class="knowledge-answer">
            <p v-if="message.pending" class="knowledge-pending">
              <span class="knowledge-spinner" aria-hidden="true" />正在检索知识库…
            </p>
            <template v-else>
              <p :class="['knowledge-answer-text', { failed: message.failed }]">{{ message.text }}</p>
              <details v-if="message.contexts?.length" class="knowledge-sources">
                <summary>引用 {{ message.contexts.length }} 条来源</summary>
                <ul>
                  <li v-for="(context, i) in message.contexts" :key="i">
                    <div class="knowledge-source-head">
                      <strong>{{ context.filename || context.doc_id || '未命名文档' }}</strong>
                      <span v-if="scorePercent(context.score)" class="knowledge-source-score">{{ scorePercent(context.score) }}</span>
                      <span v-if="formatDate(context.ingested_at)" class="knowledge-source-date">{{ formatDate(context.ingested_at) }}</span>
                    </div>
                    <p class="knowledge-source-snippet">{{ snippet(context.text) }}</p>
                  </li>
                </ul>
              </details>
            </template>
          </div>
        </div>
      </div>

      <form class="knowledge-composer" @submit.prevent="ask">
        <input
          v-model="draft"
          class="knowledge-input"
          type="text"
          maxlength="500"
          placeholder="输入问题，回车发送"
          :disabled="asking"
          @keydown.enter.exact.prevent="ask"
        />
        <button class="primary-button" type="submit" :disabled="asking || !draft.trim()">
          {{ asking ? '思考中…' : '提问' }}
        </button>
      </form>
    </div>

    <!-- 状态加载中 -->
    <div v-else class="knowledge-loading">正在获取登录状态…</div>
  </div>
</template>
