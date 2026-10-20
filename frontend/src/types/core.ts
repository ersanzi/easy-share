export interface ServiceStatus {
  core: boolean
  discovery: boolean
  receiver: boolean
  webdav: boolean
  cloudEnabled: boolean
}

export interface Peer {
  deviceId: string
  deviceName: string
  ip: string
  transferPort: number
  lastSeen: string
}

export type TaskKind =
  | 'lan_send'
  | 'lan_receive'
  | 'cloud_upload'
  | 'cloud_download'
  | 'sync'
  | 'parse'

export type TaskStatus =
  | 'pending'
  | 'accepted'
  | 'running'
  | 'queued'
  | 'paused'
  | 'waiting_network'
  | 'completed'
  | 'rejected'
  | 'failed'
  | 'cancelled'

export type TaskDirection = 'send' | 'receive' | ''

export interface TransferTask {
  id: string
  // 旧 history.json 中没有 kind，前端按 direction 兼容推断。
  kind?: TaskKind
  fileName: string
  localPath?: string
  direction: TaskDirection
  peer: string
  batchId?: string
  totalBytes: number
  transferredBytes: number
  speed: number
  status: TaskStatus
  error?: string
  // 错误三件套（路线图切片 7）：业务码（稳定，工单/埋点用）+ 系统码（HTTP/OS）。
  // error 是人读文本（上面那个字段）；三者都展示才有诊断价值。
  errorCode?: string
  systemCode?: string
  createdAt?: string
  updatedAt?: string
}

export type PreviewKind = 'unsupported' | 'image' | 'pdf' | 'text'

/**
 * 回收站保留天数。
 *
 * ⚠️ 必须与控制面 `RecycleJanitor.RETENTION_DAYS` 保持一致——那边按这个天数
 * 做彻底删除，这边按同一天数告诉用户「还能放多久」。改一处就得改另一处。
 */
export const RECYCLE_RETENTION_DAYS = 30

export interface CloudFile {
  key: string
  name: string
  size: number
  contentType: string
  lastModified: string
  previewKind: PreviewKind
  /**
   * 控制面目录层的稳定文件 ID（2026-09-06 起）；旧控制面为 0。
   *
   * ⚠️ 这是雪花 ID（19 位），JS number **无法无损表示**。
   * 当前前端只透传展示、不用它做寻址，所以尚未出问题；
   * 一旦要用它调后端（如按 fileId 删除/还原），必须先在 Go 侧转成字符串
   * ——参见 RecycleEntry.fileId 与本文件 known-issues KI-21。
   */
  fileId: number
  /**
   * 创建时间（控制面 `es_file.create_time`，ISO 串）。
   * 旧控制面未返回该字段时为 undefined —— 详情面板按「没有就不显示」处理，不编造。
   */
  createdAt?: string
  /** 上传者显示名（控制面 join sys_user.nick_name）；旧控制面缺失 */
  uploadByName?: string
  /** 覆盖上传递增的版本号；0 或缺失表示首次登记 */
  versionId?: number
}

/**
 * 一条文件分享记录（长效、可撤销）。
 *
 * 与预签名 URL 的区别：预签名 10 分钟失效且收不回；分享是一条落库记录，
 * 能撤销、能设有效期、能统计访问次数。
 */
export interface ShareLink {
  /** 雪花 ID，**必须是字符串**：19 位超出 JS 安全整数，数字形态一旦往返就变了 */
  shareId: string
  /** URL 中的随机码 */
  token: string
  /** 完整可复制链接 */
  url: string
  /** 创建时的文件名快照（文件后来被删也能显示） */
  fileName: string
  fileSize: number
  expiresAt?: string
  revoked: boolean
  visitCount: number
  createdAt?: string
}

/** 一个历史版本（覆盖上传时留档的快照） */
export interface VersionEntry {
  /** 版本号；回滚时用 */
  versionId: number
  fileSize: number
  contentMd5?: string
  createdAt?: string
  createdBy?: number
}

// 回收站条目（P1：软删后的 es_file recycled 行；deletedAt 为 ISO 时间串）
export interface RecycleEntry {
  /**
   * 雪花 ID（19 位），**必须是字符串**。
   *
   * JS 的 number 只能精确表示到 2^53-1，而雪花 ID 远超该范围：
   * 实测 2110992378747158530 经 JSON number 一次往返即变成 …800（差 30），
   * 再传回后端就查不到任何行。控制面自己对 Long 也是字符串化，
   * 前后端在这件事上必须一致。
   */
  fileId: string
  path: string
  /** 删除前的路径快照（还原落点） */
  originPath: string
  size: number
  /** 覆盖上传递增的版本号（0=首次登记） */
  versionId: number
  deletedAt?: string
}

export interface CloudPreview {
  key: string
  name: string
  kind: PreviewKind
  contentType: string
  size: number
  contentUrl?: string
  text?: string
  truncated?: boolean
}

export interface DroppedFiles {
  files: string[]
  dirs: string[]
}

// --- 知识问答（Core 作网关，令牌不进前端） ---
// 引用片段字段沿用知识服务线格式（snake_case）

export interface KnowledgeStatus {
  configured: boolean
  loggedIn: boolean
  serverUrl: string
  username: string
  role: string
}

/** 登录后下发的服务拓扑：registry=控制面登记，derived=同主机推导 */
export interface ServiceEndpoints {
  knowledgeUrl: string
  knowledgeUrlSource: 'registry' | 'derived'
  platformBaseUrl: string
}

export interface KnowledgeHealth {
  records: number
  documents: number
  llm: string
  watch_dirs: number
}

export interface KnowledgeSourceRef {
  doc_id?: string | null
  score?: number | null
  ingested_at?: string | null
}

export interface KnowledgeContext {
  doc_id?: string | null
  file_id?: string | null
  version_id?: string | null
  filename?: string | null
  score?: number | null
  ingested_at?: string | null
  text: string
  block_ids: string[]
}

export interface KnowledgeAnswer {
  answer: string
  sources: KnowledgeSourceRef[]
  contexts: KnowledgeContext[]
}

// 阅读页：知识服务 document.json 的结构化块；source 用于页码/工作表/幻灯片定位
export interface KnowledgeSourceLocation {
  page?: number
  sheet?: string
  slide?: number
  paragraph?: number
  table?: number
  row?: number
}

export interface KnowledgeDocumentBlock {
  id: string
  type: string
  text?: string
  level?: number
  rows?: string[][]
  source?: KnowledgeSourceLocation
  metadata?: Record<string, unknown>
}

export interface KnowledgeDocument {
  schema_version?: number
  file_id?: string
  version_id?: string
  object_key?: string
  filename?: string
  media_type?: string
  metadata?: Record<string, unknown>
  warnings?: string[]
  blocks: KnowledgeDocumentBlock[]
  source_sha256?: string
  pipeline_version?: string
}

export interface KnowledgeDocumentManifest {
  file_id?: string
  version_id?: string
  filename?: string
  object_key?: string
  media_type?: string
  owner?: string | null
  visible_depts?: string | null
  source_bytes?: number
  blocks?: number
  characters?: number
  chunks?: number
  warnings?: string[]
  processed_at?: string
}

export interface KnowledgeDocumentView {
  manifest: KnowledgeDocumentManifest
  document: KnowledgeDocument
}

// 知识库清单：最新任务状态与“最近一次成功版本”分开表达
export interface KnowledgeDocumentSummary {
  job_id: string
  file_id: string
  version_id: string
  filename: string
  status: 'queued' | 'processing' | 'completed' | 'failed'
  stage: string
  progress: number
  error_code?: string | null
  error_message?: string | null
  owner?: string | null
  updated_at: string
  ready: boolean
  read_version_id?: string | null
  chunks: number
  characters: number
  processed_at?: string | null
  source_bytes: number
}

export interface KnowledgeDocumentList {
  documents: KnowledgeDocumentSummary[]
}

// ─── 会议听记（知识服务 /meetings；音频大文件走 /kapi 同源网关）───

export interface MeetingSegment {
  start: number
  end: number
  text: string
  speaker: string
}

// 待办是一等实体（对标钉钉 MinutesTodoI）：可增删改与勾选完成。
// source: ai=随总结刷新，manual=用户手工（重生成总结时保留）
export interface MeetingTodo {
  id: string
  task: string
  owner_hint: string
  due_hint: string
  done: boolean
  source: 'ai' | 'manual' | string
}

export interface MeetingSection {
  title: string
  text: string
}

// 智能切片章节（start/end 为秒；点击章节条跳转播放）
export interface MeetingChapter {
  title: string
  start: number
  end: number
  gist?: string
}

// 可视化报告（汇报卡）单卡：LLM 结构化输出，导出与前端预览都按 layout 排布
export interface MeetingVisualItem {
  title: string
  tag?: string
  bullets?: string[]
  note?: string
}

export interface MeetingVisualSection {
  title: string
  // cards=并列色卡 duo=两栏 flow=左右进程 list=清单 closing=单条共识
  layout?: 'cards' | 'duo' | 'flow' | 'list' | 'closing' | 'lists' | string
  // rose/blue/teal/amber/green：与后端色带轮转一致
  accent?: string
  items: MeetingVisualItem[]
}

export interface MeetingVisualReport {
  summary?: string
  sections: MeetingVisualSection[]
}

// 核心发言提炼（AI 洞察之外的另一条：谁说了什么、原话引用）
export interface MeetingQuoteKp {
  speaker?: string
  points?: string[]
  quotes?: string[]
}

export interface MeetingSummary {
  // 默认模板（会议纪要）：旧固定字段
  minutes?: string
  key_points?: string[]
  // 其他模板：通用小节结构
  sections?: MeetingSection[]
  // 智能切片：按话题切段的时间锚点
  chapters?: MeetingChapter[]
  // 汇报卡（对标钉钉可视化报告）：色卡结构 + AI 洞察 + 核心发言提炼
  visual?: MeetingVisualReport
  insights?: string[]
  quote_kp?: MeetingQuoteKp
}

// AI 区块独立状态（对标钉钉分任务状态）：某块失败只补跑那块
export type MeetingBlockState = 'pending' | 'processing' | 'ready' | 'failed' | 'skipped'
export type MeetingBlockName = 'transcript' | 'keywords' | 'summary' | 'mindmap'

// status: recording=实时录制中 processing=转写中 completed=可查看 failed=失败（error 给原因）
// speaker 为引擎输出的序号字符串，展示名以 speaker_names 为准（用户改名后解耦）
export interface MeetingRecord {
  id: string
  title: string
  created_at: string
  owner: string | null
  status: 'recording' | 'processing' | 'completed' | 'failed'
  error: string | null
  duration_seconds: number | null
  speaker_count: number
  speaker_names: Record<string, string>
  segments: MeetingSegment[]
  // 用户手工改过的句子下标（重新转写前提示「会被覆盖」的依据）
  edited_segments?: number[]
  summary: MeetingSummary | null
  keywords: string[]
  todos: MeetingTodo[]
  blocks?: Partial<Record<MeetingBlockName, MeetingBlockState>>
  block_errors?: Partial<Record<MeetingBlockName, string>>
  mindmap: string | null
  template_id: string | null
  segment_count?: number
}

export interface MeetingArchiveResult {
  doc_id: string
  filename: string
  chunks: number
  chars: number
}

// 听记 AI 问答（对标钉钉听记问答：单条记录纪要+转写全文直接进上下文）
export interface MeetingChatTurn {
  role: 'user' | 'assistant'
  content: string
}

export interface MeetingAskResult {
  answer: string
}

// 个性化热词（对标钉钉 hotwords：转写时优先识别，提高专名准确率）
export interface MeetingHotwords {
  words: string[]
  max: number
}

// 已登记声纹（轻口径：改名即沉淀；向量本体不出服务端，此处只有元数据）
export interface MeetingVoiceprint {
  name: string
  sample_seconds: number
  updated_at: string
}

// 实时上屏（边说边出字）：会话快照 = 已定行 + 进行中的一句
export interface MeetingLiveLine {
  text: string
  at: number
}

// last_line_at：最后一行上屏时的录音秒数（0=还没出过字），用于「N 秒前出过字」的等待提示；
// active_seconds：这一片音频里确实有声音，静音片段不算卡住
export interface MeetingLiveView {
  lines: MeetingLiveLine[]
  current: string
  elapsed_seconds: number
  last_line_at?: number
  active_seconds?: boolean
}

// 会中快检（只看刚上屏的最近几行）：
// attention=发现可核对的问题 ok=没问题 unclear=内容不足 unavailable=AI 不可用
export type MeetingLiveVerdict = 'attention' | 'ok' | 'unclear' | 'unavailable'

export interface MeetingLiveCheck {
  verdict: MeetingLiveVerdict
  note: string
  points: string[]
  last_line_at: number
  checked_at: string
}

// 录制会话状态（轮询兜底：推流断了字也不会停在半路）
export interface MeetingLiveSession {
  recording: boolean
  lines: MeetingLiveLine[]
  current: string
  elapsed_seconds: number
}

// 听记 MCP（AI 工具连接）：地址+配对令牌粘进 MCP 客户端即接入，只读
export interface McpConnection {
  url: string
  token: string
}

// ─── AI 能力配置（控制面管理端；密钥掩码回显，推送结果随保存返回）───

export interface AiCapabilityConfig {
  llmBaseUrl: string
  llmModel: string
  apiKeySet: boolean
  apiKeyMasked: string
  // null = 未设置（知识服务沿用部署 .env）
  asrEnabled: boolean | null
  asrSpeakerEnabled: boolean | null
  pushed: boolean
  pushError: string
}



export interface CoreSnapshot {
  status: ServiceStatus
  peers: Peer[]
  tasks: TransferTask[]
}

// 账号控制面（RuoYi）登录态。token 只在桌面端进程保管，不下发前端。
export interface AuthUser {
  loggedIn: boolean
  userName: string
  nickName: string
  avatar: string
  // isAdmin 只控制「管理」入口显隐，不是鉴权：后台接口自己按 Sa-Token 角色拒绝。
  isAdmin: boolean
}

// 管理页的账号行。userId 是雪花 ID，必须按字符串透传——超出 JS 安全整数范围。
export interface ManagedUser {
  userId: string
  userName: string
  nickName: string
  deptName: string
  // "0" 正常、"1" 停用（沿用控制面口径，不转 bool）
  status: string
  createTime: string
  loginDate: string
}

export interface ManagedUserPage {
  total: number
  rows: ManagedUser[]
}

// 知识服务聚合统计（管理员汇总页；days 为观察窗口）
export interface KnowledgeStats {
  days: number
  total_queries: number
  recent_queries: number
  blind_spot_count: number
  generation: {
    total: number
    avg_faithfulness: number | null
    avg_unsupported_ratio: number | null
  }
  most_cited_docs: { file_id: string; count: number }[]
  documents: number
  llm: string
}

// 共享空间授权主体（部门级权限：user=按账号、dept=按部门）
export interface AdminSharedMember {
  memberType: string
  memberId: string
  permission: string
  name: string
}

// 管理页部门下拉条目（控制面只读投影 sys_dept）
export interface AdminDept {
  deptId: string
  deptName: string
}

// 容量总览。逐空间配额看不出「承诺总量是否超过物理磁盘」，这个视图专为此存在。
export interface Capacity {
  // enabled 为 false 表示控制面未配置容量探测路径，池上限不生效
  enabled: boolean
  // usableBytes/poolBytes 未启用时为 -1
  usableBytes: number
  poolBytes: number
  reservedBytes: number
  // committedBytes 是各空间配额之和，不含「不限」的空间
  committedBytes: number
  usedBytes: number
  // unlimitedCount > 0 时 committedBytes 是不完整的
  unlimitedCount: number
}

export type SpaceType = 'personal' | 'shared'

// 共享空间权限。个人空间的持有者是 'owner'，不来自成员表。
export type SpacePermission = 'owner' | 'write' | 'read'

// 一个空间的配额与实时用量。
// spaceId/ownerId 是雪花 ID，按字符串透传——超出 JS 安全整数范围。
export interface Space {
  spaceId: string
  spaceType: SpaceType
  ownerId: string
  spaceName: string
  // quotaBytes：0 未分配（显示「待开空间」）、-1 不限
  quotaBytes: number
  // usedBytes 由控制面实时聚合对象存储得出，不是库里的镜像值
  usedBytes: number
  status: string
  permission: SpacePermission | null
}

// 配额哨兵值，与 Go/Java 侧保持一致。
export const QUOTA_UNSET = 0
export const QUOTA_UNLIMITED = -1
// ─── 在线升级（appupdate.go / internal/update）───

// 一个可下载资产（安装包/DMG/zip）。URL 不在清单里，下载前现取。
export interface UpdateAssetInfo {
  id: string
  kind: string
  filename: string
  size: number
  sha256: string
}

// 一次升级检查的结果。hasUpdate 由 Go 侧 semver 比较得出。
export interface UpdateCheckResult {
  currentVersion: string
  latestVersion: string
  hasUpdate: boolean
  notes: string
  publishedAt: string
  asset?: UpdateAssetInfo
  // 是否 NSIS 安装版（Windows）；绿色版与 macOS 为 false
  installedMode: boolean
  // 能否「重启并更新」：Windows 安装版且有 installer 资产
  canAutoInstall: boolean
}

// update:progress 事件载荷（200ms 节流，Go 侧已算好速度）
export interface UpdateProgress {
  received: number
  total: number
  speed: number
}

// ─── 插件系统（appplugin.go / internal/plugin）───

// 一个已安装插件的信息（含内置/禁用状态与授权权限）。
export interface PluginInfo {
  id: string
  name: string
  version: string
  description: string
  icon: string
  entry: string
  builtin: boolean
  disabled: boolean
  permissions: string[]
}

// PluginInvoke 统一返回：ok=false 时 error 给出原因（未授权/未知能力等）。
export interface PluginInvokeResult {
  ok: boolean
  data?: unknown
  error?: string
}

// ─── 插件商城（/easyshare/plugins，MarketItem/MarketAsset）───

// 商城里一个插件包资产。
export interface MarketAsset {
  id: string
  filename: string
  sizeBytes: number
  sha256: string
}

// 商城里一个插件的最新清单（updateAvailable 由 Go 侧按本地版本回填）。
export interface MarketItem {
  id: string
  name: string
  description: string
  icon: string
  author: string
  version: string
  notes: string
  publishedAt: string
  asset?: MarketAsset
  updateAvailable?: boolean
}

// 商城安装预览（第一步）：返回需用户确认的权限，同意后带集合走安装。
export interface PluginPreview {
  id: string
  name: string
  version: string
  installedVersion: string
  isUpdate: boolean
  newPermissions: string[]
}

// 启动检查发现的可更新插件（plugin:updates-available 事件载荷，插件中心红点用）。
export interface PluginUpdateNotice {
  id: string
  name: string
  version: string
}
