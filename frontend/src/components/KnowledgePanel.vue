<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { core } from '../services/core'
import { useEasyShare } from '../composables/useEasyShare'
import KnowledgeDocumentList from './KnowledgeDocumentList.vue'
import KnowledgeReader from './KnowledgeReader.vue'
import type {
  KnowledgeContext,
  KnowledgeDocumentSummary,
  KnowledgeDocumentView,
  KnowledgeHealth,
  KnowledgeStatus,
} from '../types/core'

// 知识问答面板：Core 作网关，令牌不进前端。
// 状态自包含（同 SettingsPanel 模式）：登录态/健康度/会话消息均为本视图私有。

const app = useEasyShare()

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
const inputRef = ref<HTMLInputElement | null>(null)

// 阅读页：引用点击后按 file_id/version_id 取结构化正文，命中块用 block_ids 高亮
const readerOpen = ref(false)
const readerLoading = ref(false)
const readerError = ref('')
const readerView = ref<KnowledgeDocumentView | null>(null)
const readerFocus = ref<string[]>([])
const readerDownloading = ref(false)
const readerNotice = ref('')
// 连续点两条引用时，只让最后一次请求的结果落地
let readerRequest = 0

// 知识库清单：问答与文档两个视图共用同一个登录态
type KnowledgeTab = 'chat' | 'documents'
const activeTab = ref<KnowledgeTab>('chat')
const documents = ref<KnowledgeDocumentSummary[]>([])
const documentsLoading = ref(false)
const documentsError = ref('')
const documentsQuery = ref('')
let documentsTimer: ReturnType<typeof setTimeout> | null = null
const filteredDocuments = computed(() => {
  const query = documentsQuery.value.trim().toLowerCase()
  return query ? documents.value.filter(doc => doc.filename.toLowerCase().includes(query)) : documents.value
})

const suggestions = [
  '出差住宿标准是多少？',
  '报销需要哪些材料？',
  '知识库支持哪些文件格式？',
]
const useSuggestion = (question: string) => {
  draft.value = question
  void nextTick(() => inputRef.value?.focus())
}

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
  if (!value) {
    messages.value = []
    readerOpen.value = false
    documents.value = []
    documentsError.value = ''
    clearDocumentPoll()
  } else if (activeTab.value === 'documents') {
    void loadDocuments()
  }
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

const openReader = async (fileID: string, versionID: string, blockIds: string[] = []) => {
  readerOpen.value = true
  readerLoading.value = true
  readerError.value = ''
  readerView.value = null
  readerFocus.value = blockIds
  readerNotice.value = ''
  const requestId = ++readerRequest
  try {
    const result = await core.knowledgeDocument(fileID, versionID)
    if (requestId !== readerRequest) return
    readerView.value = result
  } catch (e) {
    if (requestId !== readerRequest) return
    readerError.value = e instanceof Error ? e.message : '读取原文失败，请稍后重试。'
    // 令牌失效时 Core 已清会话；刷新登录态会关闭阅读页并回到登录表单
    void refreshStatus()
  } finally {
    if (requestId === readerRequest) readerLoading.value = false
  }
}

const openSource = (context: KnowledgeContext) => {
  if (!context.file_id || !context.version_id) return
  void openReader(context.file_id, context.version_id, context.block_ids ?? [])
}

const closeReader = () => {
  readerRequest += 1
  readerOpen.value = false
  readerView.value = null
  readerError.value = ''
  readerFocus.value = []
  readerNotice.value = ''
}

const downloadSource = async () => {
  const view = readerView.value
  if (!view) return
  const fileID = view.manifest.file_id || view.document.file_id
  const versionID = view.manifest.version_id || view.document.version_id
  if (!fileID || !versionID) return
  readerDownloading.value = true
  readerNotice.value = ''
  try {
    const saved = await core.knowledgeDownload(
      fileID,
      versionID,
      view.manifest.filename || view.document.filename || '文档',
    )
    if (saved) readerNotice.value = `已保存到 ${saved}`
  } catch (e) {
    readerNotice.value = e instanceof Error ? e.message : '保存失败，请稍后重试。'
  } finally {
    readerDownloading.value = false
  }
}

const formatDate = (value?: string | null) => {
  if (!value) return ''
  return value.replace('T', ' ').slice(0, 16)
}

const snippet = (text: string) => (text.length > 160 ? text.slice(0, 160) + '…' : text)

const scorePercent = (score?: number | null) => (score == null ? '' : `${Math.round(score * 100)}%`)

function clearDocumentPoll() {
  if (documentsTimer !== null) {
    clearTimeout(documentsTimer)
    documentsTimer = null
  }
}

async function loadDocuments(quiet = false) {
  if (!status.value.loggedIn) return
  if (!quiet) {
    documentsLoading.value = true
    documentsError.value = ''
  }
  try {
    const result = await core.knowledgeDocuments(200)
    documents.value = result.documents
    scheduleDocumentPoll()
  } catch (e) {
    if (!quiet) documentsError.value = e instanceof Error ? e.message : '读取文档清单失败，请稍后重试。'
    clearDocumentPoll()
    void refreshStatus()
  } finally {
    if (!quiet) documentsLoading.value = false
  }
}

function scheduleDocumentPoll() {
  clearDocumentPoll()
  if (activeTab.value !== 'documents' || !status.value.loggedIn) return
  if (!documents.value.some(doc => doc.status === 'queued' || doc.status === 'processing')) return
  documentsTimer = setTimeout(() => { void loadDocuments(true) }, 3000)
}

const switchTab = (tab: KnowledgeTab) => {
  if (activeTab.value === tab) return
  activeTab.value = tab
  if (tab === 'documents') {
    void loadDocuments()
  } else {
    clearDocumentPoll()
  }
}

const openDocument = (doc: KnowledgeDocumentSummary) => {
  if (!doc.ready || !doc.read_version_id) return
  void openReader(doc.file_id, doc.read_version_id)
}

onBeforeUnmount(clearDocumentPoll)

const onMountedPrefill = async () => {
  await refreshStatus()
  await probeHealth()
  // 未配置过服务器时自动填好：服务地址来自登录后下发的服务拓扑（控制面登记，
  // 未登记则同主机推导），账号预填当前登录名——员工只差输一次密码。
  if (status.value.configured || status.value.loggedIn) return
  try {
    const endpoints = await core.serviceEndpoints()
    if (endpoints.knowledgeUrl && !serverUrl.value) serverUrl.value = endpoints.knowledgeUrl
  } catch {
    // 未登录（理论上进不到这里）/拉取失败：保持手填，不阻塞登录表单
  }
  if (!username.value && app.currentUser.value.userName) username.value = app.currentUser.value.userName
}

onMounted(onMountedPrefill)
</script>

<template>
  <div class="knowledge-panel">
    <header class="workspace-header">
      <div>
        <span class="section-label">知识</span>
        <h1>知识库</h1>
        <p>向公司知识库提问，或浏览已入库文档与引用原文。</p>
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
          <p class="setting-hint">已自动填写公司服务器；如有出入请联系管理员</p>
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

    <!-- 已登录：问答 / 文档 -->
    <template v-else-if="status.loggedIn">
      <div class="knowledge-session-bar">
        <div class="knowledge-chat-meta">
          <strong>{{ status.username }}</strong>
          <span class="knowledge-chat-server">{{ status.serverUrl }}</span>
        </div>
        <div class="knowledge-chat-badges">
          <span v-if="health" class="knowledge-badge" :title="`${health.records} 个索引块`">{{ health.documents ?? '—' }} 份文档</span>
          <span v-if="llmLabel" class="knowledge-badge">{{ llmLabel }}</span>
          <button class="text-button" type="button" @click="logout">退出登录</button>
        </div>
      </div>

      <div class="knowledge-tabs" role="tablist" aria-label="知识库视图">
        <button
          :class="{ active: activeTab === 'chat' }"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'chat'"
          @click="switchTab('chat')"
        >问答</button>
        <button
          :class="{ active: activeTab === 'documents' }"
          type="button"
          role="tab"
          :aria-selected="activeTab === 'documents'"
          @click="switchTab('documents')"
        >文档</button>
      </div>

      <div v-if="activeTab === 'chat'" class="card knowledge-chat-card">
      <div ref="listRef" class="knowledge-message-list" role="log" aria-live="polite">
        <div v-if="!messages.length" class="knowledge-empty">
          <span class="knowledge-empty-glyph" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M12 4.5C10.5 3.5 8.5 3 6 3v15c2.5 0 4.5.5 6 1.5 1.5-1 3.5-1.5 6-1.5V3c-2.5 0-4.5.5-6 1.5Z"/><path d="M12 4.5v15"/></svg>
          </span>
          <strong>问一个问题，答案会带引用来源</strong>
          <p>从公司知识库里检索，回答下方会列出引用的文档。</p>
          <div class="knowledge-suggestions">
            <button v-for="question in suggestions" :key="question" type="button" @click="useSuggestion(question)">{{ question }}</button>
          </div>
        </div>
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
                    <button
                      class="knowledge-source"
                      type="button"
                      :disabled="!context.file_id || !context.version_id"
                      :title="context.file_id && context.version_id ? '打开原文并定位到引用位置' : '这条引用没有关联原文件'"
                      @click="openSource(context)"
                    >
                      <span class="knowledge-source-head">
                        <strong>{{ context.filename || context.doc_id || '未命名文档' }}</strong>
                        <span v-if="scorePercent(context.score)" class="knowledge-source-score">{{ scorePercent(context.score) }}</span>
                        <span v-if="formatDate(context.ingested_at)" class="knowledge-source-date">{{ formatDate(context.ingested_at) }}</span>
                      </span>
                      <span class="knowledge-source-snippet">{{ snippet(context.text) }}</span>
                      <span v-if="context.file_id && context.version_id" class="knowledge-source-open">查看原文 →</span>
                    </button>
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
          ref="inputRef"
          @keydown.enter.exact.prevent="ask"
        />
        <button class="primary-button" type="submit" :disabled="asking || !draft.trim()">
          {{ asking ? '思考中…' : '提问' }}
        </button>
      </form>
      </div>

      <KnowledgeDocumentList
        v-else
        :documents="filteredDocuments"
        :total="documents.length"
        :query="documentsQuery"
        :loading="documentsLoading"
        :error="documentsError"
        @update:query="documentsQuery = $event"
        @refresh="loadDocuments()"
        @open="openDocument"
        @clear-error="documentsError = ''"
      />
    </template>

    <!-- 状态加载中 -->
    <div v-else class="knowledge-loading">正在获取登录状态…</div>

    <Teleport to="body">
      <KnowledgeReader
        v-if="readerOpen"
        :view="readerView"
        :loading="readerLoading"
        :error="readerError"
        :focus-block-ids="readerFocus"
        :downloading="readerDownloading"
        :notice="readerNotice"
        @close="closeReader"
        @download="downloadSource"
      />
    </Teleport>
  </div>
</template>
