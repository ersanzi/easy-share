// 预览专用 Wails 打桩：只在浏览器里跑界面时使用，不参与桌面端打包。
// 场景由 ?scene= 选择（overview / devices / transfers / cloud / knowledge / meetings / settings / admin / empty / login），
// 供截图工具切换状态；真实产品里这些数据全部来自 Core。
const scene = new URLSearchParams(location.search).get('scene') || 'overview'

const now = Date.now()
const iso = (minutesAgo: number) => new Date(now - minutesAgo * 60000).toISOString()

/* eslint-disable @typescript-eslint/no-explicit-any */
const users: Record<string, any> = {
  overview: { loggedIn: true, userName: 'lifeng', nickName: '李丰', avatar: '', isAdmin: true },
  admin: { loggedIn: true, userName: 'lifeng', nickName: '李丰', avatar: '', isAdmin: true },
  empty: { loggedIn: true, userName: 'zhaomin', nickName: '赵敏', avatar: '', isAdmin: false },
  login: { loggedIn: false, userName: '', nickName: '', avatar: '', isAdmin: false },
}
const user = users[scene] ?? users.overview

const fullPeers = [
  { deviceId: 'd1', deviceName: 'DESIGN-STUDIO', ip: '192.168.1.24', transferPort: 19100, lastSeen: iso(0) },
  { deviceId: 'd2', deviceName: 'MacBook Pro · 陈默', ip: '192.168.1.31', transferPort: 19100, lastSeen: iso(1) },
  { deviceId: 'd3', deviceName: '研发部 NAS', ip: '192.168.1.10', transferPort: 19500, lastSeen: iso(2) },
  { deviceId: 'd4', deviceName: '前台平板', ip: '192.168.1.88', transferPort: 19100, lastSeen: iso(6) },
]

const fullTasks = [
  { id: 't1', kind: 'cloud_upload', fileName: 'Q4产品路线图.pdf', direction: 'send', peer: '', totalBytes: 48234496, transferredBytes: 26843546, speed: 3670016, status: 'running', createdAt: iso(3), updatedAt: iso(0) },
  { id: 't2', kind: 'lan_receive', fileName: '设计稿_终版.fig', direction: 'receive', peer: 'MacBook Pro · 陈默', totalBytes: 1288490188, transferredBytes: 424463564, speed: 41943040, status: 'running', createdAt: iso(2), updatedAt: iso(0) },
  { id: 't3', kind: 'lan_receive', fileName: '会议音频_周三.m4a', direction: 'receive', peer: 'DESIGN-STUDIO', totalBytes: 94371840, transferredBytes: 0, speed: 0, status: 'pending', createdAt: iso(1), updatedAt: iso(1) },
  { id: 't4', kind: 'cloud_download', fileName: '客户验收单.xlsx', direction: 'receive', peer: '', totalBytes: 2097152, transferredBytes: 2097152, speed: 0, status: 'completed', createdAt: iso(95), updatedAt: iso(94) },
  { id: 't5', kind: 'lan_send', fileName: '年度预算汇总.xlsx', direction: 'send', peer: '研发部 NAS', totalBytes: 5242880, transferredBytes: 0, speed: 0, status: 'failed', error: '对方已离线，传输中断', errorCode: 'PEER_OFFLINE', systemCode: 'ECONNRESET', createdAt: iso(180), updatedAt: iso(178) },
  { id: 't6', kind: 'cloud_upload', fileName: '品牌视觉/设计规范.pdf', direction: 'send', peer: '', totalBytes: 33554432, transferredBytes: 33554432, speed: 0, status: 'completed', createdAt: iso(300), updatedAt: iso(296) },
  { id: 't7', kind: 'cloud_upload', fileName: '品牌视觉/LOGO_矢量.svg', direction: 'send', peer: '', totalBytes: 262144, transferredBytes: 262144, speed: 0, status: 'completed', createdAt: iso(305), updatedAt: iso(300) },
]

const emptySnapshot = {
  status: { core: true, discovery: false, receiver: false, webdav: false, cloudEnabled: false },
  peers: [],
  tasks: [],
}
const snapshot = scene === 'empty'
  ? emptySnapshot
  : {
    status: { core: true, discovery: true, receiver: true, webdav: true, cloudEnabled: true },
    peers: scene === 'overview' || scene === 'devices' ? fullPeers : fullPeers,
    tasks: fullTasks,
  }

const cloudFiles = [
  { key: '品牌视觉/', name: '品牌视觉', size: 0, contentType: '', lastModified: iso(305), previewKind: 'unsupported', fileId: 1 },
  { key: '会议纪要/', name: '会议纪要', size: 0, contentType: '', lastModified: iso(1440), previewKind: 'unsupported', fileId: 2 },
  { key: '合同与法务/', name: '合同与法务', size: 0, contentType: '', lastModified: iso(4320), previewKind: 'unsupported', fileId: 3 },
  { key: 'Q4产品路线图.pdf', name: 'Q4产品路线图.pdf', size: 48234496, contentType: 'application/pdf', lastModified: iso(3), previewKind: 'pdf', fileId: 4, uploadByName: '李丰' },
  { key: '客户验收单.xlsx', name: '客户验收单.xlsx', size: 2097152, contentType: 'application/vnd.ms-excel', lastModified: iso(94), previewKind: 'unsupported', fileId: 5, uploadByName: '赵敏' },
  { key: '年度预算汇总.xlsx', name: '年度预算汇总.xlsx', size: 5242880, contentType: 'application/vnd.ms-excel', lastModified: iso(178), previewKind: 'unsupported', fileId: 6, uploadByName: '王强' },
  { key: '竞品分析.md', name: '竞品分析.md', size: 65536, contentType: 'text/markdown', lastModified: iso(1200), previewKind: 'text', fileId: 7, uploadByName: '李丰' },
  { key: '发布会宣传图.png', name: '发布会宣传图.png', size: 8388608, contentType: 'image/png', lastModified: iso(2600), previewKind: 'image', fileId: 8, uploadByName: '陈默' },
  { key: '团队合影.jpg', name: '团队合影.jpg', size: 4194304, contentType: 'image/jpeg', lastModified: iso(5760), previewKind: 'image', fileId: 9, uploadByName: '赵敏' },
]

// 听记详情样例：发言人区块、章节、待办、问答都需要真实形状的数据才能点验
const meetingDetailMock: any = {
  id: 'm1',
  title: 'Q4 产品规划评审',
  created_at: iso(120),
  owner: '李丰',
  status: 'completed',
  error: null,
  duration_seconds: 3725,
  speaker_count: 4,
  speaker_names: { '0': '李丰', '1': '赵敏', '2': '王强', '3': '陈默' },
  keywords: ['路线图', '定价', '灰度发布', '企业版'],
  template_id: 'meeting-minutes',
  blocks: { transcript: 'ready', keywords: 'ready', summary: 'ready', mindmap: 'ready' },
  block_errors: {},
  summary: {
    minutes: '本次评审确认 Q4 主线是知识库与网盘联动，10 月底完成灰度，11 月进入企业客户试用。',
    key_points: ['Q4 主线锁定知识库与网盘联动', '10 月底完成灰度发布', '企业版定价按席位计价'],
    sections: [
      { title: '路线图与节奏', text: '10 月底完成灰度发布，11 月进入企业客户试用；移动端跟随桌面端之后排期。' },
      { title: '定价与商业化', text: '企业版按席位计价，首批客户给三个月试用期；具体价格由销售在评审后确认。' },
      { title: '风险与依赖', text: '知识服务容量需要在灰度前扩容，依赖运维在本周内完成压测。' },
    ],
    chapters: [
      { title: '开场与目标', start: 0, end: 320, gist: '确认本次评审范围' },
      { title: '路线图排期', start: 320, end: 1500, gist: '灰度与企业版节奏' },
      { title: '定价讨论', start: 1500, end: 2600, gist: '按席位计价' },
      { title: '风险与待办', start: 2600, end: 3725, gist: '容量压测与跟进人' },
    ],
    insights: ['会议对灰度时间有明确共识，但企业版定价仍待销售确认。', '容量压测是当前唯一硬依赖，负责人已明确。'],
    quote_kp: { speaker: '赵敏', points: ['先灰度再放量，避免一次性铺开'], quotes: ['我们先把 10 月底的灰度守住，企业版可以晚两周。'] },
  },
  todos: [
    { id: 't1', task: '完成知识服务容量压测', owner_hint: '王强', due_hint: '本周五', done: false, source: 'ai' },
    { id: 't2', task: '确认企业版席位报价', owner_hint: '赵敏', due_hint: '下周一', done: false, source: 'ai' },
    { id: 't3', task: '输出灰度发布检查单', owner_hint: '陈默', due_hint: '10 月 25 日', done: true, source: 'manual' },
  ],
  segments: [
    { start: 12.4, end: 26.8, speaker: '0', text: '今天主要确认 Q4 的路线图和灰度节奏，先把结论对齐，细节会后跟进。' },
    { start: 27.2, end: 48.6, speaker: '1', text: '我建议 10 月底先完成灰度发布，企业版往后放两周，避免同时开两条战线。' },
    { start: 49.0, end: 71.5, speaker: '2', text: '容量这块需要先压测，知识服务的检索和转写都会吃资源，我本周五前给结果。' },
    { start: 72.1, end: 96.3, speaker: '0', text: '可以，灰度清单谁来出？发布前需要把回滚方案也写进去。' },
    { start: 96.8, end: 121.2, speaker: '3', text: '我来出检查单，包含回滚和监控项，10 月 25 日发群里。' },
    { start: 122.0, end: 149.4, speaker: '1', text: '企业版定价按席位走，首批客户给三个月试用，销售下周确认具体价格。' },
  ],
  mindmap: '# Q4 产品规划\n## 路线图\n### 10 月底灰度\n### 11 月企业试用\n## 定价\n### 按席位计价\n## 风险\n### 容量压测',
}

const ok = (data: unknown) => Promise.resolve(data)
const noop = () => Promise.resolve(undefined)

const app: Record<string, (arg?: any) => Promise<any>> = {
  GetSnapshot: () => ok(snapshot),
  CurrentUser: () => ok(user),
  Login: (u: string, p: string) => (p === 'bad' ? Promise.reject(new Error('账号或密码不正确')) : ok(user)),
  Logout: noop,
  GetLogDirectory: () => ok('C:\\Users\\lifeng\\AppData\\Local\\EasyShare\\logs'),
  AppVersion: () => ok('1.2.0'),
  CheckUpdate: () => ok({ currentVersion: '1.2.0', latestVersion: '1.2.0', hasUpdate: false, notes: '', publishedAt: '', installedMode: true, canAutoInstall: false }),
  GetSettings: () => ok({ deviceName: 'DESIGN-STUDIO', receiveDir: 'D:\\EasyShare\\接收', webdavRoot: 'D:\\EasyShare\\共享' }),
  ServiceEndpoints: () => ok({ knowledgeUrl: 'http://192.168.1.10:8000', knowledgeUrlSource: 'registry', platformBaseUrl: 'https://office.example.com' }),
  KnowledgeStatus: () => ok({ configured: true, loggedIn: true, serverUrl: 'http://192.168.1.10:8000', username: 'lifeng', role: 'user' }),
  KnowledgeHealth: () => ok({ records: 1284, llm: 'configured', watch_dirs: 3 }),
  KnowledgeAsk: (q: string) => ok({
    answer: `按照《差旅管理办法（2026 修订）》规定，一线城市住宿标准为每人每晚 600 元，其他城市为 450 元；超标部分需部门负责人审批。报销时须附住宿发票与会务统筹单。\n\n关于「${q}」的补充：出差审批在 EasyShare 任务中心发起后，会同步到 HR 系统。`,
    sources: [
      { doc_id: 'doc-11', score: 0.92, ingested_at: iso(4000) },
      { doc_id: 'doc-27', score: 0.81, ingested_at: iso(9000) },
    ],
    contexts: [
      { doc_id: 'doc-11', filename: '差旅管理办法（2026 修订）.pdf', score: 0.92, ingested_at: iso(4000), text: '第三章 住宿标准：一线城市（北京、上海、广州、深圳）每人每晚不超过 600 元，其他城市不超过 450 元。超标部分须在出差结束后 3 个工作日内提交部门负责人审批单，随报销材料一并提交。', block_ids: ['b1'] },
      { doc_id: 'doc-27', filename: '费用报销操作手册.docx', score: 0.81, ingested_at: iso(9000), text: '报销材料清单：住宿发票原件、会务统筹单、出差审批单。缺少任一项材料将退回补充，退回后 5 个工作日内未补齐的，顺延至下个报销周期。', block_ids: ['b2'] },
    ],
  }),
  KnowledgeLogin: noop,
  KnowledgeLogout: noop,
  MeetingsList: () => ok([
    { id: 'm1', title: 'Q4 产品规划评审', created_at: iso(120), owner: '李丰', status: 'completed', error: null, duration_seconds: 3725, speaker_count: 4, speaker_names: { '0': '李丰', '1': '赵敏', '2': '王强', '3': '陈默' }, segments: [], summary: null, keywords: ['路线图', '定价', '灰度'], todos: [], mindmap: null, template_id: 'minutes', segment_count: 214 },
    { id: 'm2', title: '设计系统周会', created_at: iso(1500), owner: '陈默', status: 'completed', error: null, duration_seconds: 1820, speaker_count: 3, speaker_names: { '0': '陈默', '1': '李丰', '2': '赵敏' }, segments: [], summary: null, keywords: ['组件', 'Token'], todos: [], mindmap: null, template_id: 'minutes', segment_count: 96 },
    { id: 'm3', title: '客户需求恳谈', created_at: iso(4300), owner: '赵敏', status: 'processing', error: null, duration_seconds: 2410, speaker_count: 2, speaker_names: { '0': '赵敏', '1': '客户代表' }, segments: [], summary: null, keywords: [], todos: [], mindmap: null, template_id: 'minutes' },
  ]),
  MeetingDetail: (id: string) => ok(id === 'm1' ? { ...meetingDetailMock } : { ...meetingDetailMock, id, title: '设计系统周会' }),
  MeetingSpeakerRename: (_id: string, speaker: string, name: string) => {
    meetingDetailMock.speaker_names = { ...meetingDetailMock.speaker_names, [speaker]: name }
    return ok({ ...meetingDetailMock })
  },
  MeetingHotwordsGet: () => ok({ words: ['EasyShare', '听记'], max: 100 }),
  MeetingVoiceprintsGet: () => ok([{ name: '赵敏', sample_seconds: 35.1, updated_at: iso(1440) }]),
  MeetingLivePoints: () => ok([]),
  MeetingStreamWarm: noop,
  MySpaces: () => ok([{ spaceId: 's1', spaceType: 'personal', ownerId: 'u1', spaceName: '李丰的个人空间', quotaBytes: 1099511627776, usedBytes: 489626271744, status: '0', permission: 'owner' }]),
  CloudList: () => ok(cloudFiles),
  CloudStars: () => ok(['Q4产品路线图.pdf']),
  CloudRecents: () => ok(['Q4产品路线图.pdf', '客户验收单.xlsx']),
  CloudRecycleList: () => ok([]),
  CloudShares: () => ok([]),
  PluginList: () => ok([
    { id: 'diag', name: '网络诊断', version: '1.0.0', description: '查看局域网探测与端口连通性', icon: '🛰', entry: 'index.html', builtin: true, disabled: false, permissions: ['net.status'] },
    { id: 'notes', name: '速记', version: '0.4.2', description: '随手记，随文件一起同步', icon: 'notes.png', entry: 'index.html', builtin: false, disabled: false, permissions: [] },
  ]),
  PluginMarketList: () => ok([
    { id: 'pdf', name: 'PDF 工具箱', description: '拆分、合并、压缩与加水印，处理结果直接进网盘', icon: '📄', author: 'EasyShare 团队', version: '2.1.0', notes: '新增批量压缩', publishedAt: iso(3000), updateAvailable: false },
    { id: 'ocr', name: '扫描件识别', description: '把扫描件转成可检索的文字，进入知识库自动建索引', icon: '🔍', author: '北岸工作室', version: '1.3.2', notes: '识别速度提升', publishedAt: iso(7000), updateAvailable: true },
  ]),
  CfapiMountPath: () => ok('D:\\EasyShare\\网盘'),
  AdminListUsers: () => ok({ total: 46, rows: [
    { userId: '1876001234567890123', userName: 'lifeng', nickName: '李丰', deptName: '产品部', status: '0', createTime: '2025-03-11', loginDate: iso(2) },
    { userId: '1876001234567890124', userName: 'zhaomin', nickName: '赵敏', deptName: '设计部', status: '0', createTime: '2025-04-02', loginDate: iso(600) },
    { userId: '1876001234567890125', userName: 'wangqiang', nickName: '王强', deptName: '研发部', status: '1', createTime: '2025-05-19', loginDate: iso(20000) },
  ] }),
  AdminCapacity: () => ok({ enabled: true, usableBytes: 2199023255552, poolBytes: 2199023255552, reservedBytes: 0, committedBytes: 1099511627776, usedBytes: 489626271744, unlimitedCount: 0 }),
  AdminListSpaces: () => ok([
    { spaceId: 's1', spaceType: 'personal', ownerId: 'u1', spaceName: '李丰的个人空间', quotaBytes: 1099511627776, usedBytes: 489626271744, status: '0', permission: 'owner' },
    { spaceId: 's2', spaceType: 'shared', ownerId: 'd1', spaceName: '产品部共享', quotaBytes: 2199023255552, usedBytes: 1125899906842, status: '0', permission: 'write' },
  ]),
  AdminSharedMembers: () => ok([{ memberType: 'dept', memberId: '101', permission: 'write', name: '产品部' }]),
  AdminRegisterEnabled: () => ok(false),
  AdminAiConfigGet: () => ok({ llmBaseUrl: 'https://api.example.com/v1', llmModel: 'deepseek-chat', apiKeySet: true, apiKeyMasked: 'sk-••••••••3f2a', asrEnabled: true, asrSpeakerEnabled: true, pushed: true, pushError: '' }),
  AdminKnowledgeStats: () => ok({ days: 30, total_queries: 1864, recent_queries: 312, blind_spot_count: 41, generation: { total: 1743, avg_faithfulness: 0.89, avg_unsupported_ratio: 0.07 }, most_cited_docs: [{ file_id: '11', count: 87 }, { file_id: '27', count: 54 }], documents: 1284, llm: 'configured' }),
  AdminListDepts: () => ok([{ deptId: '101', deptName: '产品部' }, { deptId: '102', deptName: '设计部' }, { deptId: '103', deptName: '研发部' }]),
}

window.go = { main: { App: new Proxy(app, { get: (target, key: string) => target[key] ?? noop }) } }
window.runtime = new Proxy({}, {
  get: (_t, key: string) => {
    if (key === 'WindowIsMaximised') return () => Promise.resolve(false)
    if (key === 'Environment') return () => Promise.resolve({ platform: 'windows', buildType: 'dev' })
    return () => undefined
  },
}) as any
