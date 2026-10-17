<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import type { CloudFile, CloudPreview as CloudPreviewData } from '../types/core'
import CloudPreviewView from './CloudPreview.vue'
import RecyclePanel from './RecyclePanel.vue'
import { buildDriveView, relativeDir } from '../utils/driveFolder'
import type { DriveSortDir, DriveSortKey } from '../utils/driveFolder'
import { core } from '../services/core'


defineProps<{ enabled: boolean }>()
const emit = defineEmits<{ upload: [targetDir: string]; uploadFolder: [targetDir: string]; download: [key: string]; delete: [key: string]; share: [key: string] }>()

const files = ref<CloudFile[]>([])
const loading = ref(false)
const shareUrl = ref('')
const shareExpires = ref('')
const shareId = ref('')
const shareKey = ref('')
const previewOpen = ref(false)
const previewLoading = ref(false)
const previewError = ref('')
const previewData = ref<CloudPreviewData | null>(null)
// 回收站视图开关（P1）：打开时以覆盖层展示回收站面板
const recycleOpen = ref(false)
// CfApi 占位符目录（切片 5）：登录且系统支持时非空；入口按它隐藏
const cfapiPath = ref('')
// 空间用量（对标 WPS 的侧栏常驻用量条）：网盘页直接可见「还剩多少」，
// 不必跳去设置页。0/-1 分别表示未分配与不限，展示时分别处理。
const spaceUsage = ref<{ usedBytes: number; quotaBytes: number } | null>(null)

const loadSpaceUsage = async () => {
  try {
    const spaces = await core.mySpaces()
    const personal = spaces.find((s) => s.spaceType === 'personal')
    if (personal) {
      spaceUsage.value = { usedBytes: personal.usedBytes, quotaBytes: personal.quotaBytes }
    }
  } catch {
    spaceUsage.value = null
  }
}

// 用量占比（0–100）；未分配或不限返回 null（不画进度条）
const usagePercent = computed(() => {
  const usage = spaceUsage.value
  if (!usage || usage.quotaBytes <= 0) return null
  return Math.min(100, Math.round(usage.usedBytes / usage.quotaBytes * 100))
})

// 用量文案：对标 WPS 的「86.87G / 3.92T」形态
const usageLabel = computed(() => {
  const usage = spaceUsage.value
  if (!usage) return ''
  if (usage.quotaBytes === -1) return `已用 ${formatSize(usage.usedBytes)} / 不限`
  if (usage.quotaBytes === 0) return '尚未分配容量'
  return `已用 ${formatSize(usage.usedBytes)} / ${formatSize(usage.quotaBytes)}`
})

const loadCfapiPath = async () => {
  try {
    cfapiPath.value = await core.cfapiMountPath()
  } catch {
    cfapiPath.value = ''
  }
}

const openCfapiFolder = () => {
  if (cfapiPath.value) {
    core.openCfapiFolder()
  }
}

// 目录导航状态：currentPath 为相对根的目录（'' = 根）；搜索跨当前目录全部后代
const currentPath = ref('')
const searchQuery = ref('')
const sortKey = ref<DriveSortKey>('lastModified')
const sortDir = ref<DriveSortDir>('desc')

const view = computed(() =>
  buildDriveView(files.value, {
    path: currentPath.value,
    query: searchQuery.value,
    sortKey: sortKey.value,
    sortDir: sortDir.value,
  }),
)

const loadFiles = async () => {
  loading.value = true
  try {
    files.value = await core.cloudList()
  } catch {
    files.value = []
  } finally {
    loading.value = false
  }
}

const formatSize = (value: number) => value < 1024
  ? `${value} B`
  : value < 1048576
    ? `${(value / 1024).toFixed(1)} KB`
    : value < 1073741824
      ? `${(value / 1048576).toFixed(1)} MB`
      : `${(value / 1073741824).toFixed(1)} GB`


const formatDate = (value: string) => {
  if (!value) return ''
  const d = new Date(value)
  return `${d.getMonth() + 1}/${d.getDate()} ${d.getHours().toString().padStart(2, '0')}:${d.getMinutes().toString().padStart(2, '0')}`
}

/**
 * 文件类型 → 图标配色档位（对标 WPS 列表的彩色类型图标）。
 *
 * 纯视觉分组，不参与预览/下载判断（那些仍走 previewKind）；
 * 认不出的扩展名一律回落 generic，绝不影响行为。
 * 配色在 style.css 的 .cloud-icon.ftype-* 里，按 WPS 语义色取第 6 阶。
 */
const fileKind = (name: string) => {
  const dot = name.lastIndexOf('.')
  const ext = dot > 0 ? name.slice(dot + 1).toLowerCase() : ''
  if (/^(docx?|rtf|odt|pages)$/.test(ext)) return 'doc'
  if (/^(xlsx?|csv|ods|numbers)$/.test(ext)) return 'xls'
  if (/^(pptx?|odp|key)$/.test(ext)) return 'ppt'
  if (ext === 'pdf') return 'pdf'
  if (/^(png|jpe?g|gif|webp|bmp|svg|ico|heic)$/.test(ext)) return 'img'
  if (/^(zip|rar|7z|tar|gz|bz2|xz)$/.test(ext)) return 'zip'
  if (/^(mp3|wav|flac|aac|m4a|ogg)$/.test(ext)) return 'audio'
  if (/^(mp4|mov|mkv|avi|webm|wmv)$/.test(ext)) return 'video'
  if (/^(md|txt|json|ya?ml|xml|html?|css|js|ts|jsx|tsx|go|py|java|sql|sh|log)$/.test(ext)) return 'code'
  return 'generic'
}

// 类型档位 → 中文名（详情面板「类型」一行）
const FILE_KIND_LABEL: Record<string, string> = {
  doc: 'Word 文档', xls: 'Excel 表格', ppt: '演示文稿', pdf: 'PDF 文档',
  img: '图片', zip: '压缩包', audio: '音频', video: '视频',
  code: '文本 / 代码', generic: '文件',
}
const fileTypeLabel = (name: string) => FILE_KIND_LABEL[fileKind(name)] ?? '文件'

/** 详情面板用的完整时间（列表里为了紧凑只显示 M/D HH:mm） */
const formatDateTime = (value: string) => {
  if (!value) return ''
  const d = new Date(value)
  if (Number.isNaN(d.getTime())) return ''
  const p = (n: number) => n.toString().padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}`
}

// ─── 详情面板（对标 WPS 右侧「研发资料存放」属性卡 / 钉钉右侧详情）───
// 点行展开、再点同一行收起；关闭按钮直接收起。默认不占位，列表保持满宽。
const selectedFile = ref<CloudFile | null>(null)
const detailOpen = computed(() => selectedFile.value !== null)
// 模板里 v-if 已保证非空；这个 computed 只是把「可能为 null」从模板里赶走，
// 避免每个属性访问都写可选链。
const detail = computed(() => selectedFile.value as CloudFile)
const selectFile = (file: CloudFile) => {
  selectedFile.value = selectedFile.value?.key === file.key ? null : file
}
const closeDetail = () => { selectedFile.value = null }

// ─── 重命名（详情面板内联编辑）───
// 只改同目录末段名，跨目录移动不在这里做（Go 侧 CloudRename 会拒绝路径分隔符）。
const renaming = ref(false)
const renameDraft = ref('')
const renameBusy = ref(false)
const renameError = ref('')
const renameInput = ref<HTMLInputElement | null>(null)

const startRename = () => {
  if (!selectedFile.value) return
  renameDraft.value = selectedFile.value.name
  renameError.value = ''
  renaming.value = true
  // 聚焦并预选主文件名（不含扩展名）：改「叫什么」时通常不想连后缀一起删
  void nextTick(() => {
    const el = renameInput.value
    if (!el) return
    el.focus()
    const dot = renameDraft.value.lastIndexOf('.')
    el.setSelectionRange(0, dot > 0 ? dot : renameDraft.value.length)
  })
}

const cancelRename = () => {
  renaming.value = false
  renameError.value = ''
}

const commitRename = async () => {
  const current = selectedFile.value
  if (!current || renameBusy.value) return
  const next = renameDraft.value.trim()
  // 没改动或清空 → 当作取消，不打扰服务端
  if (!next || next === current.name) {
    cancelRename()
    return
  }
  renameBusy.value = true
  try {
    await core.cloudRename(current.key, next)
    await loadFiles()
    // 路径变了，旧的 key 已失效：按新名字重新定位选中项
    selectedFile.value = files.value.find(f => f.name === next) ?? null
    renaming.value = false
  } catch (e) {
    renameError.value = e instanceof Error ? e.message : String(e)
  } finally {
    renameBusy.value = false
  }
}

// ─── 移动（详情面板 → 目录选择浮层）───
// 目录清单从扁平文件列表推导：/objects 不返回目录实体，但每条 key 的
// 前几段就是目录，顺路把中间层级补齐即可，不必再加一个「列目录」接口。
const moveOpen = ref(false)
const moveTarget = ref('')
const moveBusy = ref(false)

const allDirs = computed(() => {
  const set = new Set<string>()
  for (const f of files.value) {
    const parts = f.key.split('/')
    parts.pop() // 末段是文件名
    let acc = ''
    for (const part of parts) {
      if (!part) continue
      acc = acc ? `${acc}/${part}` : part
      set.add(acc)
    }
  }
  return [...set].sort((a, b) => a.localeCompare(b, 'zh-Hans-CN'))
})

// 平铺 + 缩进，比可折叠树少一次点击；目录多时靠滚动
const dirOptions = computed(() => allDirs.value.map(path => ({
  path,
  name: path.split('/').pop() ?? path,
  depth: path.split('/').length - 1,
})))

// 文件所在目录：从 key 推导，而不是用当前浏览路径。
// 搜索结果里的文件可能在子目录，用面包屑会把「位置」指错，
// 移动时判断「是否原地不动」也会跟着错。
const detailDir = computed(() => {
  const key = selectedFile.value?.key ?? ''
  const cut = key.lastIndexOf('/')
  return cut >= 0 ? key.slice(0, cut) : ''
})

const openMove = () => {
  moveTarget.value = ''
  moveMode.value = 'single'
  moveOpen.value = true
}

// ─── 批量选择 ───
// 只对文件生效：目录的移动/删除后端仍是单路径操作，递归搬子树的事务边界
// 没解决之前，不给用户一个「看起来能用、实际会留半截状态」的入口。
const checkedKeys = ref<Set<string>>(new Set())
const checkedCount = computed(() => checkedKeys.value.size)
const checkedHas = (key: string) => checkedKeys.value.has(key)

const toggleCheck = (key: string) => {
  const next = new Set(checkedKeys.value)
  if (next.has(key)) next.delete(key)
  else next.add(key)
  checkedKeys.value = next
}
const clearChecked = () => { checkedKeys.value = new Set() }

// 换目录就作废勾选：否则「已选 3 项」里可能混着看不见的文件，
// 后续批量操作会作用到用户当前根本看不到的对象上。
watch(() => currentPath.value, () => clearChecked())

const allFileKeys = computed(() => view.value.files.map(f => f.key))
const allChecked = computed(() =>
  allFileKeys.value.length > 0 && allFileKeys.value.every(k => checkedKeys.value.has(k)))
const toggleCheckAll = () => {
  checkedKeys.value = allChecked.value ? new Set() : new Set(allFileKeys.value)
}

// 批量移动复用同一个目录选择浮层，用 moveMode 区分单条/多条
const moveMode = ref<'single' | 'batch'>('single')
const openBatchMove = () => {
  if (!checkedCount.value) return
  moveTarget.value = ''
  moveMode.value = 'batch'
  moveOpen.value = true
}

const batchConfirmOpen = ref(false)
const batchBusy = ref(false)
const batchProgress = ref('')

// 批量操作里单条失败不会中断整批，但结果必须让用户看见——
// 否则「点了删除、列表里还剩两个」会被当成 bug。
const notice = ref('')
let noticeTimer: number | undefined
const showNotice = (message: string) => {
  notice.value = message
  window.clearTimeout(noticeTimer)
  noticeTimer = window.setTimeout(() => { notice.value = '' }, 4000)
}

const doBatchDelete = async () => {
  const keys = [...checkedKeys.value]
  if (!keys.length || batchBusy.value) return
  batchBusy.value = true
  batchConfirmOpen.value = false
  let done = 0
  // 逐个删：控制面没有批量接口，但单个失败不该中断整批——
  // 记录成败后照常刷新，用户能看到「哪些还在」。
  for (const key of keys) {
    batchProgress.value = `${done + 1}/${keys.length}`
    try {
      await core.cloudDelete(key)
      done++
    } catch { /* 单条失败留给列表体现 */ }
  }
  batchBusy.value = false
  batchProgress.value = ''
  if (done < keys.length) {
    showNotice(`${keys.length - done} 个文件删除失败，请稍后重试`)
  }
  clearChecked()
  selectedFile.value = null
  await loadFiles()
}

// ─── 星标 ───
// 星标挂在「用户 × 路径」上（控制面 es_file_mark），不跟 fileId 走：
// 同名文件删掉重建会换 fileId，但用户认知里星标的还是那个位置。
const starredKeys = ref<Set<string>>(new Set())
const onlyStarred = ref(false)

const loadStars = async () => {
  try {
    const paths = await core.cloudStars()
    starredKeys.value = new Set(paths)
  } catch {
    // 星标拉不到不该拖垮主列表：降级为空集合，星标功能暂时不可见
    starredKeys.value = new Set()
  }
}

const isStarred = (key: string) => starredKeys.value.has(key)

const toggleStar = async (file: CloudFile) => {
  const next = !isStarred(file.key)
  // 先改本地再发请求：星星是即时反馈，等一个往返会让点击显得迟钝
  const optimistic = new Set(starredKeys.value)
  if (next) optimistic.add(file.key)
  else optimistic.delete(file.key)
  starredKeys.value = optimistic
  try {
    await core.cloudStar(file.key, next)
  } catch {
    // 写失败就回滚，不能让界面停在一个没存下来的状态
    const rollback = new Set(starredKeys.value)
    if (next) rollback.delete(file.key)
    else rollback.add(file.key)
    starredKeys.value = rollback
    showNotice('星标保存失败，请稍后重试')
  }
}

// 「只看星标」时文件夹一并隐藏：文件夹没有星标概念，留着会让筛选结果自相矛盾
const displayedFiles = computed(() =>
  onlyStarred.value ? view.value.files.filter(f => isStarred(f.key)) : view.value.files)
const displayedFolders = computed(() => (onlyStarred.value ? [] : view.value.folders))
const visibleCount = computed(() => displayedFolders.value.length + displayedFiles.value.length)

// 最近访问埋点：失败静默，记录「最近」不该阻断用户正在做的事
const touchRecent = (key: string) => { void core.cloudTouch(key) }

// ─── 打开文件（双击 / Enter）───
// 可预览的直接开预览；其余类型不做隐式下载——下载会弹保存对话框，
// 误触的代价比「没反应」高，所以改成明确告知。
const openFile = (file: CloudFile) => {
  // 双击前浏览器会先触发两次 click，而 selectFile 是「同 key 反选」——
  // 第二次点击已经把它取消了，这里纠回选中态，否则打开后详情面板会莫名收起。
  selectedFile.value = file
  touchRecent(file.key)
  if (file.previewKind !== 'unsupported') {
    openPreview(file)
    return
  }
  showNotice('该类型暂不支持预览，请先下载')
}

// ─── 右键菜单 ───
// Web 应用的操作靠悬停浮出的按钮，桌面用户的第一反应却是右键。
// 菜单里的动作与行尾按钮同源，只是入口不同。
const ctxMenu = ref<{ x: number; y: number; file: CloudFile } | null>(null)

const openCtxMenu = (event: MouseEvent, file: CloudFile) => {
  event.preventDefault()
  event.stopPropagation()
  // 右键即选中：菜单里的操作都针对「我刚点的那一个」
  selectedFile.value = file
  // 贴边翻转，避免菜单被窗口边缘裁掉
  const width = 184
  const height = 236
  ctxMenu.value = {
    x: Math.min(event.clientX, window.innerWidth - width - 8),
    y: Math.min(event.clientY, window.innerHeight - height - 8),
    file,
  }
}
const closeCtxMenu = () => { ctxMenu.value = null }
// 菜单动作统一从「当前右键对象」取，避免模板里到处写非空断言
const runCtx = (action: (file: CloudFile) => void) => {
  const file = ctxMenu.value?.file
  closeCtxMenu()
  if (file) action(file)
}

const ctxPreview = () => runCtx(file => openFile(file))
const ctxDownload = () => runCtx(file => emit('download', file.key))
const ctxShare = () => runCtx(file => { void requestShare(file.key) })
const ctxStar = () => runCtx(file => { void toggleStar(file) })
const ctxRename = () => runCtx(() => startRename())
const ctxMove = () => runCtx(() => {
  moveMode.value = 'single'
  moveTarget.value = ''
  moveOpen.value = true
})
const ctxDelete = () => runCtx(file => emit('delete', file.key))

// ─── 键盘导航 ───
// 只遍历文件：文件夹没有详情面板，选中它没有可展示的内容。
const selectByOffset = (delta: number) => {
  const list = view.value.files
  if (!list.length) return
  const current = list.findIndex(f => f.key === selectedFile.value?.key)
  const start = current < 0 ? (delta > 0 ? -1 : list.length) : current
  const next = Math.min(list.length - 1, Math.max(0, start + delta))
  selectedFile.value = list[next]
  void nextTick(() => {
    document.querySelector('.cloud-row.on')?.scrollIntoView({ block: 'nearest' })
  })
}

const onGlobalKeydown = (event: KeyboardEvent) => {
  const target = event.target as HTMLElement | null
  // 输入框 / 可编辑区内的按键不归列表管
  if (target && (/^(INPUT|TEXTAREA|SELECT)$/.test(target.tagName) || target.isContentEditable)) return
  // 有浮层时让浮层优先
  if (moveOpen.value || batchConfirmOpen.value || previewOpen.value || recycleOpen.value) return

  // 右键菜单打开时只接 Esc：此时方向键属于菜单，不该同时把列表选中项挪走
  if (ctxMenu.value) {
    if (event.key === 'Escape') {
      event.preventDefault()
      closeCtxMenu()
    }
    return
  }

  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'a') {
    event.preventDefault()
    toggleCheckAll()
    return
  }

  switch (event.key) {
    case 'ArrowDown':
      event.preventDefault()
      selectByOffset(1)
      break
    case 'ArrowUp':
      event.preventDefault()
      selectByOffset(-1)
      break
    case 'Enter': {
      const file = selectedFile.value
      if (!file) return
      event.preventDefault()
      openFile(file)
      break
    }
    case 'F2':
      if (!selectedFile.value) return
      event.preventDefault()
      startRename()
      break
    case 'Delete': {
      const file = selectedFile.value
      if (!file) return
      event.preventDefault()
      emit('delete', file.key)
      break
    }
    case 'Escape':
      closeCtxMenu()
      closeDetail()
      break
    default:
      break
  }
}

const commitMove = async () => {
  if (moveBusy.value) return
  const keys = moveMode.value === 'batch'
    ? [...checkedKeys.value]
    : (selectedFile.value ? [selectedFile.value.key] : [])
  if (!keys.length) return
  // 单条时「目标 = 源目录」直接收场；批量下各文件源目录不同，这个判断不成立
  if (moveMode.value === 'single' && moveTarget.value === detailDir.value) {
    moveOpen.value = false
    return
  }
  moveBusy.value = true
  let failed = 0
  try {
    for (const key of keys) {
      try {
        await core.cloudMove(key, moveTarget.value)
      } catch {
        // 单条失败不中断整批：部分成功的也要如实反映到列表上
        failed++
      }
    }
    await loadFiles()
    // 无论成败都收场：成功的已经搬走，勾选与选中态都失效；
    // 失败数用顶部提示告知，而不是把弹窗留着让用户以为整批没动
    moveOpen.value = false
    selectedFile.value = null
    clearChecked()
    if (failed > 0) {
      showNotice(`${failed} / ${keys.length} 个文件移动失败`)
    }
  } finally {
    moveBusy.value = false
  }
}
// 切目录 / 刷新后原选中项可能已不在当前列表里，避免面板挂着过期对象
watch([() => currentPath.value, files], () => {
  if (!selectedFile.value) return
  const still = files.value.some(f => f.key === selectedFile.value!.key)
  if (!still) selectedFile.value = null
})


const requestShare = async (key: string) => {
  try {
    // 走长效分享（落库、可撤销），不是 10 分钟的预签名地址——
    // 后者发出去就收不回，只适合「马上下一份」的场景
    const link = await core.cloudShareLink(key, 24 * 7)
    shareUrl.value = link.url
    shareExpires.value = link.expiresAt ?? ''
    shareId.value = link.shareId
    shareKey.value = key
  } catch {
    shareUrl.value = ''
  }
}

// 撤销刚生成的那条链接。撤销立即生效，不等自然过期——这是分享层相对预签名
// 唯一真正的增量能力，入口必须给出来，否则「可撤销」只是接口里的一个字段。
const revokeShare = async () => {
  if (!shareId.value) return
  try {
    await core.cloudRevokeShare(shareId.value)
    shareUrl.value = ''
    shareId.value = ''
    showNotice('分享链接已撤销')
  } catch {
    showNotice('撤销失败，请稍后重试')
  }
}
// 分享条上的有效期：只显示到日，不必精确到分钟
const shareExpiryLabel = computed(() => {
  if (!shareExpires.value) return '有效'
  const d = new Date(shareExpires.value)
  if (Number.isNaN(d.getTime())) return '有效'
  return `至 ${d.getMonth() + 1}/${d.getDate()}`
})
const closePreview = () => {
  previewOpen.value = false
  previewLoading.value = false
  previewError.value = ''
  previewData.value = null
}

const openPreview = async (file: CloudFile) => {
  // 预览也算一次访问：从行内按钮进来时不经过 openFile，这里补埋点
  touchRecent(file.key)
  previewOpen.value = true
  previewLoading.value = true
  previewError.value = ''
  previewData.value = null
  try {
    const result = await core.cloudPreview(file.key)
    previewData.value = result
    if (result.kind === 'unsupported') {
      previewError.value = '此格式暂不支持在线预览，请下载后打开。'
    }
  } catch (error) {
    previewError.value = error instanceof Error ? error.message : '预览服务暂不可用，请稍后重试。'
  } finally {
    previewLoading.value = false
  }
}

const copyShare = () => {
  if (shareUrl.value) {
    navigator.clipboard.writeText(shareUrl.value)
    shareUrl.value = ''
    shareKey.value = ''
  }
}

// sortBy 点列头排序（对标 WPS：排序控件就是列头本身）。
//
// 交互约定与桌面文件管理器一致：
//   - 点**同一列** → 切换升/降序；
//   - 点**不同列** → 换列并给该列的默认方向（时间/大小默认降序=大到小、新到旧；
//     名称默认升序=字典序），而不是沿用上一列的方向——那会让用户困惑。
const sortBy = (key: DriveSortKey) => {
  if (sortKey.value === key) {
    sortDir.value = sortDir.value === 'asc' ? 'desc' : 'asc'
    return
  }
  sortKey.value = key
  sortDir.value = key === 'name' ? 'asc' : 'desc'
}
const onSearchInput = (event: Event) => {
  searchQuery.value = (event.target as HTMLInputElement).value
}
const enterFolder = (path: string) => {
  currentPath.value = path
  searchQuery.value = ''
}

onMounted(() => {
  loadFiles()
  loadCfapiPath()
  loadSpaceUsage()
  loadStars()
  document.addEventListener('keydown', onGlobalKeydown)
  document.addEventListener('click', closeCtxMenu)
  window.addEventListener('resize', closeCtxMenu)
  // capture=true：列表自身滚动时也要关掉菜单，否则菜单会飘在原地不动
  window.addEventListener('scroll', closeCtxMenu, true)
})
onBeforeUnmount(() => {
  document.removeEventListener('keydown', onGlobalKeydown)
  document.removeEventListener('click', closeCtxMenu)
  window.removeEventListener('resize', closeCtxMenu)
  window.removeEventListener('scroll', closeCtxMenu, true)
})
defineExpose({ refresh: loadFiles })
</script>

<template>
  <section class="card cloud-panel" :class="{ 'detail-open': detailOpen }">
    <!-- 左栏：工具条 + 列表。详情面板展开时本列收窄，列表仍吃满剩余宽度 -->
    <div class="cloud-browser">
    <header class="card-header">
      <div>
        <span class="section-label">云端</span>
        <h2>网盘</h2>
      </div>
      <div class="header-actions">
        <button v-if="enabled && cfapiPath" class="text-button" type="button" title="在资源管理器中打开按需占位符目录（文件不占磁盘，打开时才下载）" @click="openCfapiFolder">本地挂载</button>
        <button v-if="enabled" class="text-button" type="button" @click="recycleOpen = true">回收站</button>
        <button class="text-button" type="button" @click="loadFiles">刷新</button>
        <span class="count-badge">{{ view.folderCount }} 个文件夹 · {{ view.fileCount }} 个文件</span>
      </div>
    </header>

    <div v-if="!enabled" class="empty-state">
      <span class="empty-illustration cloud-off">
        <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/><path d="M3 3l18 18"/></svg>
      </span>
      <strong>网盘未启用</strong>
      <p>云端服务暂不可用，请确认后台服务已正常启动。</p>
    </div>

    <template v-else>
      <!-- 批量条：有选中项时顶掉常规工具条，避免两套操作同时占着顶部 -->
      <div v-if="checkedCount" class="cloud-batch-bar">
        <button class="batch-clear" type="button" title="取消选择" @click="clearChecked">
          <svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg>
        </button>
        <span class="batch-count">已选 <b>{{ checkedCount }}</b> 项</span>
        <span v-if="batchProgress" class="batch-progress">{{ batchProgress }}</span>
        <span class="batch-spacer"></span>
        <button class="secondary-button compact" type="button" :disabled="batchBusy" @click="openBatchMove">移动</button>
        <button class="secondary-button compact destructive-text" type="button" :disabled="batchBusy" @click="batchConfirmOpen = true">删除</button>
      </div>

      <div v-else class="cloud-toolbar">
        <button class="primary-button compact" type="button" @click="$emit('upload', currentPath)">
          <svg viewBox="0 0 24 24"><path d="M12 16V4m0 0L8 8m4-4 4 4M5 14v5h14v-5"/></svg>
          上传文件
        </button>
        <button class="secondary-button compact" type="button" @click="$emit('uploadFolder', currentPath)">
          <svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
          上传文件夹
        </button>
        <div class="cloud-nav-tools">
          <button
            class="star-filter"
            type="button"
            :class="{ on: onlyStarred }"
            :title="onlyStarred ? '显示全部文件' : '只看已星标'"
            @click="onlyStarred = !onlyStarred"
          >
            <svg viewBox="0 0 24 24"><path d="m12 3.5 2.6 5.4 5.9.8-4.3 4.2 1 5.9-5.2-2.8-5.2 2.8 1-5.9-4.3-4.2 5.9-.8Z"/></svg>
            星标
          </button>
          <div class="cloud-search">
            <svg viewBox="0 0 24 24"><circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/></svg>
            <input
              :value="searchQuery"
              type="text"
              placeholder="在当前目录搜索（含子文件夹）"
              autocomplete="off"
              spellcheck="false"
              @input="onSearchInput"
            >
          </div>
        </div>
      </div>

      <div v-if="notice" class="cloud-notice">{{ notice }}</div>

      <!-- 列头：与列表行共用同一套 grid 模板，点哪列按哪列排。
           排序做成列头而不是工具栏下拉框——用户视线在文件列表上，操作就在视线上。 -->
      <div v-if="!loading" class="cloud-view-bar">
        <button
          class="view-check"
          type="button"
          :class="{ on: allChecked }"
          :title="allChecked ? '取消全选' : '全选当前目录的文件'"
          @click="toggleCheckAll"
        ><i></i></button>
        <button
          class="view-col view-name"
          type="button"
          :class="{ on: sortKey === 'name' }"
          @click="sortBy('name')"
        >名称{{ sortKey === 'name' ? (sortDir === 'asc' ? ' ↑' : ' ↓') : '' }}</button>
        <button
          class="view-col"
          type="button"
          :class="{ on: sortKey === 'lastModified' }"
          @click="sortBy('lastModified')"
        >最后修改{{ sortKey === 'lastModified' ? (sortDir === 'asc' ? ' ↑' : ' ↓') : '' }}</button>
        <button
          class="view-col col-size"
          type="button"
          :class="{ on: sortKey === 'size' }"
          @click="sortBy('size')"
        >大小{{ sortKey === 'size' ? (sortDir === 'asc' ? ' ↑' : ' ↓') : '' }}</button>
      </div>

      <div class="cloud-crumbs">
        <template v-for="(crumb, index) in view.breadcrumbs" :key="crumb.path || '__root__'">
          <span v-if="index > 0" class="crumb-sep">/</span>
          <button
            class="crumb"
            type="button"
            :class="{ on: index === view.breadcrumbs.length - 1 }"
            @click="enterFolder(crumb.path)"
          >{{ crumb.name }}</button>
        </template>
        <!-- 项数与总大小合到一处：列头让位给真正的三列，这里做上下文摘要 -->
        <span class="crumb-size">
          {{ visibleCount }} 项<template v-if="view.totalSize && !onlyStarred"> · {{ formatSize(view.totalSize) }}</template>
        </span>
        <!-- 用量常驻（对标 WPS 侧栏底部用量条）：放在面包屑右侧当作「这个盘的体量」上下文 -->
        <div v-if="spaceUsage" class="cloud-usage" :title="usageLabel">
          <span class="usage-text">{{ usageLabel }}</span>
          <span v-if="usagePercent !== null" class="usage-bar">
            <i :style="{ width: usagePercent + '%' }" :class="{ warn: usagePercent >= 90 }"></i>
          </span>
        </div>
      </div>

      <div v-if="loading" class="empty-state compact-empty">
        <strong>加载中…</strong>
      </div>

      <div v-else-if="!visibleCount" class="empty-state compact-empty">
        <span class="empty-illustration cloud-empty">
          <svg viewBox="0 0 24 24"><path d="M6 19a4 4 0 0 1-.78-7.93A7 7 0 0 1 18.78 11 4 4 0 0 1 18 19H6z"/></svg>
        </span>
        <strong>{{ onlyStarred ? '还没有星标文件' : (view.searching ? '没有匹配的文件' : '此文件夹为空') }}</strong>
        <p v-if="onlyStarred">在文件上右键，或打开右侧详情点星星，即可加入星标。</p>
        <p v-else>{{ view.searching ? '换个关键词，或回到上级目录再试。' : '点击上方"上传文件"将文件存储到云端。' }}</p>
      </div>

      <div v-else class="cloud-list">
        <article v-for="folder in displayedFolders" :key="folder.path" class="cloud-row folder-row" role="button" tabindex="0" @click="enterFolder(folder.path)" @keydown.enter="enterFolder(folder.path)">
          <div class="cloud-icon folder-icon" aria-hidden="true">
            <svg viewBox="0 0 24 24"><path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/></svg>
          </div>
          <div class="cloud-info">
            <strong>{{ folder.name }}</strong>
            <span>文件夹</span>
          </div>
          <!-- 文件夹没有可比的修改时间与大小，两列留空占位（grid 需要） -->
          <div class="cloud-modified"></div>
          <div class="cloud-size"></div>
          <div class="cloud-actions">
            <button class="secondary-button compact" type="button" title="打开" @click.stop="enterFolder(folder.path)">打开</button>
          </div>
        </article>

        <article
          v-for="file in displayedFiles"
          :key="file.key"
          class="cloud-row has-check"
          :class="{ on: selectedFile?.key === file.key, checked: checkedHas(file.key) }"
          @click="selectFile(file)"
          @contextmenu.prevent="openCtxMenu($event, file)"
          @dblclick="openFile(file)"
        >
          <!-- 勾选框复用图标位置：悬停时图标淡出、方框淡入，不额外占一列宽度 -->
          <div class="cloud-icon" :class="`ftype-${fileKind(file.name)}`">
            <svg class="icon-glyph" aria-hidden="true" viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
            <button
              class="row-check"
              type="button"
              :title="checkedHas(file.key) ? '取消选择' : '选择'"
              @click.stop="toggleCheck(file.key)"
            ><i></i></button>
            <!-- 星标徽章钉在图标右上角：不进任何一列，三列对齐不会被它破坏 -->
            <svg v-if="isStarred(file.key)" class="star-badge" aria-hidden="true" viewBox="0 0 24 24"><path d="m12 3.5 2.6 5.4 5.9.8-4.3 4.2 1 5.9-5.2-2.8-5.2 2.8 1-5.9-4.3-4.2 5.9-.8Z"/></svg>
          </div>
          <div class="cloud-info">
            <strong>{{ file.name }}</strong>
            <!-- 平时只有文件名（时间和大小各有专列）；搜索命中时才补一行相对目录，
                 否则同名文件散落在不同目录里根本分不清 -->
            <span v-if="view.searching && relativeDir(file, currentPath)">{{ relativeDir(file, currentPath) }}/</span>
          </div>
          <div class="cloud-modified">{{ formatDate(file.lastModified) }}</div>
          <div class="cloud-size">{{ formatSize(file.size) }}</div>
          <div class="cloud-actions">
            <button v-if="file.previewKind !== 'unsupported'" class="secondary-button compact preview-action" type="button" title="预览" @click.stop="openPreview(file)">预览</button>
            <button class="secondary-button compact" type="button" title="下载" @click.stop="$emit('download', file.key)">下载</button>
            <button class="secondary-button compact" type="button" title="分享链接" @click.stop="requestShare(file.key)">分享</button>
            <button class="secondary-button compact destructive-text" type="button" title="删除" @click.stop="$emit('delete', file.key)">删除</button>
          </div>
        </article>

        <!-- 列表终止标记（对标 WPS 的「没有更多了」）：
             明确告诉用户列表到底了，而不是让他怀疑「是不是还有没加载的」 -->
        <div class="cloud-list-end">{{ view.searching ? '搜索完毕' : '没有更多了' }}</div>
      </div>

      <div v-if="shareUrl" class="share-bar">
        <span class="share-label">{{ shareKey }} 分享链接（{{ shareExpiryLabel }} 有效，可撤销）：</span>
        <code class="share-url">{{ shareUrl }}</code>
        <button class="primary-button compact" type="button" @click="copyShare">复制</button>
        <button class="text-button" type="button" @click="revokeShare">撤销</button>
      </div>
    </template>
    </div>

    <!-- 右栏：文件详情（对标 WPS 右侧属性卡 / 钉钉右侧详情）。
         点行展开、再点收起；字段缺失就整行不渲染，不用占位符充数。 -->
    <aside v-if="detailOpen" class="cloud-detail">
      <header class="detail-head">
        <b>文件详情</b>
        <button type="button" title="关闭" @click="closeDetail">
          <svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg>
        </button>
      </header>

      <div class="detail-body">
        <div class="detail-thumb" :class="`ftype-${fileKind(detail.name)}`">
          <svg viewBox="0 0 24 24"><path d="M6 2h8l5 5v15H6z"/><path d="M14 2v6h6"/></svg>
        </div>

        <div class="detail-title">
          <template v-if="!renaming">
            <h2 class="detail-name" :title="detail.name">{{ detail.name }}</h2>
            <button
              class="detail-star"
              :class="{ on: isStarred(detail.key) }"
              type="button"
              :title="isStarred(detail.key) ? '取消星标' : '添加星标'"
              @click="toggleStar(detail)"
            ><svg viewBox="0 0 24 24"><path d="m12 3.5 2.6 5.4 5.9.8-4.3 4.2 1 5.9-5.2-2.8-5.2 2.8 1-5.9-4.3-4.2 5.9-.8Z"/></svg></button>
            <button class="detail-rename" type="button" title="重命名" @click="startRename">
              <svg viewBox="0 0 24 24"><path d="M4 19.5h4L19 8.5a2.1 2.1 0 0 0-3-3L5 16.5Z"/></svg>
            </button>
          </template>
          <input
            v-else
            ref="renameInput"
            v-model="renameDraft"
            class="detail-rename-input"
            type="text"
            :disabled="renameBusy"
            @keydown.enter.prevent="commitRename"
            @keydown.esc.prevent="cancelRename"
            @blur="commitRename"
          >
        </div>
        <p v-if="renameError" class="detail-rename-error">{{ renameError }}</p>
        <p class="detail-sub">{{ fileTypeLabel(detail.name) }} · {{ formatSize(detail.size) }}</p>

        <div class="detail-actions">
          <button v-if="detail.previewKind !== 'unsupported'" class="secondary-button compact" type="button" @click="openPreview(detail)">预览</button>
          <button class="secondary-button compact" type="button" @click="$emit('download', detail.key)">下载</button>
          <button class="secondary-button compact" type="button" @click="requestShare(detail.key)">分享</button>
          <button class="secondary-button compact" type="button" @click="openMove">移动</button>
        </div>

        <dl class="detail-meta">
          <div><dt>类型</dt><dd>{{ fileTypeLabel(detail.name) }}</dd></div>
          <div><dt>大小</dt><dd>{{ formatSize(detail.size) }}</dd></div>
          <div><dt>位置</dt><dd :title="detailDir || '根目录'">{{ detailDir || '根目录' }}</dd></div>
          <div><dt>修改时间</dt><dd>{{ formatDateTime(detail.lastModified) }}</dd></div>
          <div v-if="detail.createdAt"><dt>创建时间</dt><dd>{{ formatDateTime(detail.createdAt) }}</dd></div>
          <div v-if="detail.uploadByName"><dt>上传者</dt><dd>{{ detail.uploadByName }}</dd></div>
          <div v-if="detail.versionId"><dt>版本</dt><dd>v{{ detail.versionId }}</dd></div>
        </dl>

        <button class="detail-danger" type="button" @click="$emit('delete', detail.key)">删除此文件</button>
      </div>
    </aside>
  </section>

  <!-- 右键菜单：位置贴鼠标，动作与行尾按钮同源，只是入口不同 -->
  <div
    v-if="ctxMenu"
    class="ctx-menu"
    :style="{ left: `${ctxMenu.x}px`, top: `${ctxMenu.y}px` }"
    @contextmenu.prevent
    @click.stop
  >
    <button v-if="ctxMenu.file.previewKind !== 'unsupported'" class="ctx-item" type="button" @click="ctxPreview">预览</button>
    <button class="ctx-item" type="button" @click="ctxDownload">下载</button>
    <button class="ctx-item" type="button" @click="ctxShare">分享链接</button>
    <button class="ctx-item" type="button" @click="ctxStar">{{ ctxMenu.file && isStarred(ctxMenu.file.key) ? '取消星标' : '添加星标' }}</button>
    <div class="ctx-sep"></div>
    <button class="ctx-item" type="button" @click="ctxRename">重命名</button>
    <button class="ctx-item" type="button" @click="ctxMove">移动到…</button>
    <div class="ctx-sep"></div>
    <button class="ctx-item danger" type="button" @click="ctxDelete">删除</button>
  </div>

  <!-- 移动到：目录选择浮层。目录清单由文件 key 前缀推导，不额外请求后端 -->
  <div v-if="moveOpen" class="move-overlay" @click.self="moveOpen = false">
    <div class="move-dialog" role="dialog" aria-label="移动文件">
      <header class="move-head">
        <b>{{ moveMode === 'batch' ? `移动 ${checkedCount} 个文件` : '移动到' }}</b>
        <button type="button" title="关闭" @click="moveOpen = false">
          <svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6 6 18"/></svg>
        </button>
      </header>

      <div class="move-target">
        <span class="move-file" :title="moveMode === 'batch' ? '' : selectedFile?.name">
          {{ moveMode === 'batch' ? `${checkedCount} 个文件` : selectedFile?.name }}
        </span>
        <span class="move-arrow">→</span>
        <code class="move-dest">{{ moveTarget || '根目录' }}</code>
      </div>

      <div class="move-list">
        <button
          class="move-item"
          type="button"
          :class="{ on: moveTarget === '' }"
          @click="moveTarget = ''"
        >根目录</button>
        <button
          v-for="dir in dirOptions"
          :key="dir.path"
          class="move-item"
          type="button"
          :class="{ on: moveTarget === dir.path }"
          :style="{ paddingLeft: `${12 + dir.depth * 14}px` }"
          @click="moveTarget = dir.path"
        >{{ dir.name }}</button>
      </div>


      <footer class="move-foot">
        <button class="secondary-button compact" type="button" @click="moveOpen = false">取消</button>
        <button
          class="primary-button compact"
          type="button"
          :disabled="moveBusy || (moveMode === 'single' && moveTarget === detailDir)"
          @click="commitMove"
        >{{ moveMode === 'single' && moveTarget === detailDir ? '已在目标目录' : (moveBusy ? '移动中…' : '移动到这里') }}</button>
      </footer>
    </div>
  </div>

  <!-- 批量删除确认：走自己的浮层，不用 window.confirm（原生弹窗在 WebView 里样式与产品割裂） -->
  <div v-if="batchConfirmOpen" class="move-overlay" @click.self="batchConfirmOpen = false">
    <div class="move-dialog confirm-dialog" role="dialog" aria-label="删除文件">
      <header class="move-head"><b>删除 {{ checkedCount }} 个文件</b></header>
      <p class="confirm-text">删除后可在回收站还原。是否继续？</p>
      <footer class="move-foot">
        <button class="secondary-button compact" type="button" @click="batchConfirmOpen = false">取消</button>
        <button class="primary-button compact danger-button" type="button" @click="doBatchDelete">删除</button>
      </footer>
    </div>
  </div>

  <CloudPreviewView v-if="previewOpen" :preview="previewData" :loading="previewLoading" :error="previewError" @close="closePreview" />
  <!-- 回收站覆盖网盘区；返回时刷新主列表确认还原落点 -->
  <RecyclePanel v-if="recycleOpen" class="recycle-overlay" @back="recycleOpen = false; loadFiles()" />
</template>
