import { createHash } from 'node:crypto'
import { createReadStream } from 'node:fs'
import { stat } from 'node:fs/promises'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const CLIENT_ID = 'e5cd7e4891bf95d1d19206ce24a7b32e'
const FORMAL_VERSION = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/
const CONTROL_TIMEOUT_MS = 30_000
const TRANSFER_TIMEOUT_MS = 15 * 60_000

// 错误仅包含本地固定诊断，绝不透传响应正文、请求 URL 或底层异常。
class ReleaseError extends Error {
  constructor(stage, detail) {
    super(`[${stage}] ${detail}`)
    this.name = 'ReleaseError'
    this.stage = stage
  }
}

function fail(stage, detail) {
  throw new ReleaseError(stage, detail)
}

function httpUrl(value, stage) {
  let url
  try { url = new URL(value) } catch { fail(stage, '需要合法的 HTTP/HTTPS URL') }
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password) {
    fail(stage, 'URL 必须使用 HTTP/HTTPS 且不得包含用户名或密码')
  }
  return url
}

function assetId(value, stage) {
  if (typeof value === 'number') {
    if (!Number.isSafeInteger(value) || value <= 0) fail(stage, '资产 ID 数字超出安全整数范围或非法；雪花 ID 必须为字符串')
    return String(value)
  }
  if (typeof value !== 'string' || !/^[1-9]\d*$/.test(value)) fail(stage, '资产 ID 必须为正整数字符串或安全整数')
  return value
}

// 使用任意精度整数段比较，避免字典序和 Number 精度造成旧 tag 误判。
function newerVersion(version, candidate) {
  if (typeof version !== 'string' || !FORMAL_VERSION.test(version)) return false
  const left = version.split('.').map(BigInt)
  const right = candidate.split('.').map(BigInt)
  for (let index = 0; index < 3; index++) {
    if (left[index] !== right[index]) return left[index] > right[index]
  }
  return false
}

function sameMetadata(asset, local) {
  return asset.platform === local.platform && asset.kind === local.kind &&
    asset.filename === local.filename && asset.sizeBytes === local.sizeBytes &&
    typeof asset.sha256 === 'string' && asset.sha256.toLowerCase() === local.sha256
}

async function timed(stage, timeoutMs, action) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  try {
    return await action(controller.signal)
  } catch (error) {
    if (error instanceof ReleaseError) throw error
    fail(stage, controller.signal.aborted ? '请求超时' : '操作失败（网络、文件或响应格式异常）')
  } finally {
    clearTimeout(timer)
  }
}

function checkHttp(response, stage) {
  if (!response.ok) {
    const status = Number.isSafeInteger(response.status) ? response.status : 'invalid'
    fail(stage, stage === 'login' ? `认证失败；请检查发布账号、权限及控制面认证配置（HTTP status=${status}）` : `HTTP status=${status}`)
  }
}

async function localDigest(installerPath) {
  const hash = createHash('sha256')
  let sizeBytes = 0
  for await (const chunk of createReadStream(installerPath)) {
    sizeBytes += chunk.length
    hash.update(chunk)
  }
  return { sha256: hash.digest('hex'), sizeBytes }
}

/**
 * 发布正式安装包；依赖注入仅用于测试与超时预算调整，不读取任何环境文件。
 * 第二参数可传 { fetch, warn, controlTimeoutMs, transferTimeoutMs }。
 */
export async function publishRelease({ platformUrl, username, password, installerPath, version,
  notes = '', platform = 'windows', kind = 'installer' } = {}, dependencies = {}) {
  let stage = 'validation'
  try {
    if (typeof version !== 'string' || !FORMAL_VERSION.test(version)) fail(stage, '仅允许正式 SemVer x.y.z；预发布版本不得进入匿名更新源')
    if (!((platform === 'windows' && kind === 'installer') || (platform === 'macos' && kind === 'dmg'))) fail(stage, '平台与资产类型必须为 windows/installer 或 macos/dmg')
    if (typeof username !== 'string' || !username.trim() || typeof password !== 'string' || !password.trim()) fail(stage, '缺少发布凭据；CLI 仅接受 EASYSHARE_RELEASE_USERNAME/PASSWORD 环境变量')
    if (typeof installerPath !== 'string' || !installerPath) fail(stage, '必须提供安装包路径')
    if (typeof notes !== 'string') fail(stage, '更新说明必须为字符串')
    const base = httpUrl(platformUrl, stage)
    if (base.search || base.hash) fail(stage, '控制面 URL 不得包含查询参数或片段')
    base.pathname = `${base.pathname.replace(/\/+$/, '')}/`
    const fetchImpl = dependencies.fetch ?? globalThis.fetch
    const warn = dependencies.warn ?? (message => console.error(message))
    const controlTimeoutMs = dependencies.controlTimeoutMs ?? CONTROL_TIMEOUT_MS
    const transferTimeoutMs = dependencies.transferTimeoutMs ?? TRANSFER_TIMEOUT_MS
    for (const value of [controlTimeoutMs, transferTimeoutMs]) {
      if (!Number.isSafeInteger(value) || value <= 0 || value > 2_147_483_647) fail(stage, '超时预算必须为正的安全毫秒整数')
    }
    const info = await stat(installerPath)
    if (!info.isFile() || !Number.isSafeInteger(info.size) || info.size <= 0) fail(stage, '安装包必须为存在的非空普通文件且大小为安全整数')
    const filename = path.basename(installerPath)
    if (!/^[A-Za-z0-9._-]{1,120}$/.test(filename)) fail(stage, '安装包文件名仅允许字母数字及 . _ -，长度最多 120')
    stage = 'hash-local'
    const digest = await localDigest(installerPath)
    if (digest.sizeBytes !== info.size) fail(stage, '计算摘要时安装包大小变化')
    const local = { version, platform, kind, filename, sizeBytes: info.size, sha256: digest.sha256, notes }
    if (base.protocol === 'http:') warn('安全警告：HTTP 控制面连接未加密；请仅在可信网络使用，公网部署建议配置 HTTPS。')

    let token
    const endpoint = suffix => new URL(suffix, base)
    const request = (requestStage, suffix, { method = 'GET', body, authenticated = false } = {}) =>
      timed(requestStage, controlTimeoutMs, async signal => {
        const headers = {}
        if (requestStage === 'login' || authenticated) headers.clientid = CLIENT_ID
        if (authenticated) headers.Authorization = `Bearer ${token}`
        if (body !== undefined) headers['Content-Type'] = 'application/json; charset=utf-8'
        const response = await fetchImpl(endpoint(suffix), {
          method, headers, body: body === undefined ? undefined : JSON.stringify(body), signal, redirect: 'manual',
        })
        checkHttp(response, requestStage)
        const envelope = await response.json()
        if (envelope?.code !== 200) {
          const code = Number.isSafeInteger(envelope?.code) ? `code=${envelope.code}` : 'code=invalid'
          fail(requestStage, requestStage === 'login' ? `认证失败；请检查发布账号、权限及控制面认证配置（${code}）` : `控制面返回 ${code}`)
        }
        return envelope.data
      })

    stage = 'login'
    const login = await request(stage, 'auth/login', {
      method: 'POST', body: { clientId: CLIENT_ID, grantType: 'password', username, password },
    })
    token = login?.access_token
    if (typeof token !== 'string' || !token.trim() || /[\r\n]/.test(token)) fail(stage, '认证失败；控制面未返回有效令牌，请检查认证配置')

    stage = 'preflight-releases'
    const releases = await request(stage, 'easyshare/app/admin/releases', { authenticated: true })
    if (!Array.isArray(releases)) fail(stage, '管理版本列表格式非法')
    let existing
    for (const release of releases) {
      if (!release || typeof release.version !== 'string' || !Array.isArray(release.assets)) fail(stage, '管理版本列表格式非法')
      const relevant = release.assets.filter(a => a?.platform === platform && a?.kind === kind)
      if (relevant.some(a => a.status === '1') && newerVersion(release.version, version)) fail(stage, '存在较新已发布正式版本，拒绝旧 tag 替换更新源')
      if (release.version !== version) continue
      for (const asset of relevant) {
        if (existing || !sameMetadata(asset, local)) fail(stage, '同版本同平台同类型资产元数据冲突，禁止覆盖')
        if (!['0', '1'].includes(asset.status)) fail(stage, '同版本资产状态非法')
        existing = { id: assetId(asset.id, stage), status: asset.status }
      }
    }
    stage = 'preflight-latest'
    const latest = await request(stage, `easyshare/app/latest?platform=${platform}`)
    if (latest !== null) {
      if (!latest || typeof latest.version !== 'string' || !Array.isArray(latest.assets)) fail(stage, '匿名 latest 清单格式非法')
      if (newerVersion(latest.version, version)) fail(stage, '匿名 latest 已有较新正式版本，拒绝旧 tag 替换更新源')
    }

    const idempotent = existing?.status === '1'
    let id = existing?.id
    if (!idempotent) {
      stage = 'prepare'
      const prepared = await request(stage, 'easyshare/app/admin/uploads', { method: 'POST', body: local, authenticated: true })
      id = assetId(prepared?.assetId, stage)
      if (existing && id !== existing.id) fail(stage, '重试上传返回的资产 ID 与 pending 记录不匹配')
      const uploadUrl = httpUrl(prepared?.uploadUrl, stage)
      stage = 'upload'
      await timed(stage, transferTimeoutMs, async signal => {
        const stream = createReadStream(installerPath)
        try {
          const response = await fetchImpl(uploadUrl, {
            method: 'PUT', headers: { 'Content-Type': 'application/octet-stream', 'Content-Length': String(local.sizeBytes) },
            body: stream, duplex: 'half', signal, redirect: 'manual',
          })
          checkHttp(response, stage)
          await response.body?.cancel()
        } finally {
          stream.destroy()
        }
      })
      stage = 'publish'
      await request(stage, `easyshare/app/admin/assets/${id}/publish`, { method: 'POST', authenticated: true })
    }

    stage = 'verify-manifest'
    const manifest = await request(stage, `easyshare/app/latest?platform=${platform}`)
    if (manifest?.version !== version || !Array.isArray(manifest.assets)) fail(stage, '匿名 latest 版本或资产清单不匹配')
    const matches = manifest.assets.filter(a => a?.platform === platform && a?.kind === kind)
    if (matches.length !== 1 || assetId(matches[0].id, stage) !== id || !sameMetadata(matches[0], local) || matches[0].status !== '1') fail(stage, '匿名 latest 资产 ID、平台、类型、大小、文件名或 SHA256 不匹配')
    stage = 'resolve-download'
    const download = await request(stage, `easyshare/app/assets/${id}/url`)
    const downloadUrl = httpUrl(download?.url, stage)
    stage = 'verify-download'
    await timed(stage, transferTimeoutMs, async signal => {
      const response = await fetchImpl(downloadUrl, { signal, redirect: 'manual' })
      checkHttp(response, stage)
      const length = response.headers.get('content-length')
      if (length !== null && (!/^\d+$/.test(length) || BigInt(length) !== BigInt(local.sizeBytes))) {
        await response.body?.cancel()
        fail(stage, '下载 Content-Length 与安装包大小不匹配')
      }
      if (!response.body) fail(stage, '下载响应没有文件内容')
      const hash = createHash('sha256')
      let bytes = 0
      for await (const chunk of response.body) {
        bytes += chunk.length
        if (bytes > local.sizeBytes) fail(stage, '下载文件大小不匹配')
        hash.update(chunk)
      }
      if (bytes !== local.sizeBytes) fail(stage, '下载文件大小不匹配')
      if (hash.digest('hex') !== local.sha256) fail(stage, '下载文件 SHA256 不匹配')
    })
    return { version, assetId: id, platform, kind, filename, sizeBytes: local.sizeBytes, sha256: local.sha256, idempotent }
  } catch (error) {
    if (error instanceof ReleaseError) throw error
    fail(stage, stage === 'validation' ? '安装包或发布参数不可用' : '操作失败（网络、文件或响应格式异常）')
  }
}

// CLI 不接受用户名/口令参数，也不打印任何调用方输入或底层异常。
export async function runCli(argv = process.argv.slice(2), env = process.env, dependencies = {}) {
  try {
    const names = new Map([
      ['--platform-url', 'platformUrl'], ['--installer-path', 'installerPath'], ['--version', 'version'],
      ['--notes', 'notes'], ['--platform', 'platform'], ['--kind', 'kind'],
    ])
    const options = {}
    for (let index = 0; index < argv.length; index++) {
      const key = names.get(argv[index])
      if (!key) fail('cli', '不支持的 CLI 参数；用户名和密码只能通过环境变量提供')
      if (Object.hasOwn(options, key)) fail('cli', 'CLI 参数重复')
      const value = argv[++index]
      if (value === undefined || value.startsWith('--')) fail('cli', 'CLI 参数缺少值')
      options[key] = value
    }
    if (!options.version) fail('cli', '必须提供 --version；调用前请完成产品版本与正式 tag 校验')
    options.platformUrl ??= env.EASYSHARE_RELEASE_URL || env.PLATFORM_URL
    options.username = env.EASYSHARE_RELEASE_USERNAME
    options.password = env.EASYSHARE_RELEASE_PASSWORD
    const result = await publishRelease(options, dependencies)
    ;(dependencies.stdout ?? (message => console.log(message)))(result.idempotent ? '发布验证通过（已有相同资产，未覆盖）。' : '发布完成；匿名清单及下载 SHA256 验证通过。')
    return 0
  } catch (error) {
    ;(dependencies.stderr ?? (message => console.error(message)))(error instanceof ReleaseError ? error.message : '[cli] 发布失败')
    return 1
  }
}

if (process.argv[1] && pathToFileURL(path.resolve(process.argv[1])).href === import.meta.url) {
  process.exitCode = await runCli()
}
