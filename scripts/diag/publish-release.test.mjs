import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { spawn } from 'node:child_process'
import { mkdtemp, rm, writeFile } from 'node:fs/promises'
import http from 'node:http'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

const CLIENT_ID = 'e5cd7e4891bf95d1d19206ce24a7b32e'
const INSTALLER = Buffer.from('EasyShare mock installer\n中文发布验证\n')
const ASSET_ID = '9223372036854775806'

async function fixture(t, options = {}) {
  const dir = await mkdtemp(path.join(tmpdir(), 'easyshare-release-'))
  t.after(() => rm(dir, { recursive: true, force: true }))
  const id = options.id ?? ASSET_ID
  const idText = String(id)
  const platform = options.platform ?? 'windows'
  const kind = options.kind ?? 'installer'
  const filename = platform === 'macos' ? 'EasyShare-arm64.dmg' : 'EasyShare-installer.exe'
  const version = options.version ?? '1.2.3'
  const installerPath = path.join(dir, filename)
  await writeFile(installerPath, INSTALLER)
  const calls = []
  let uploaded
  let published = false
  let preparedBody
  let baseUrl
  const asset = () => ({
    id, platform, kind, filename,
    sizeBytes: INSTALLER.length, sha256: createHash('sha256').update(INSTALLER).digest('hex'), status: '1',
  })
  const server = http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url, baseUrl)
      const chunks = []
      for await (const chunk of req) chunks.push(chunk)
      const body = Buffer.concat(chunks)
      calls.push({ method: req.method, path: url.pathname, query: url.search, headers: req.headers, body })
      const json = (data, code = 200) => {
        res.setHeader('Content-Type', 'application/json')
        res.end(JSON.stringify({ code, data, msg: 'response-secret-do-not-log' }))
      }
      if (options.handle?.({ req, res, url, body, json, asset })) return
      if (url.pathname === '/auth/login') {
        assert.equal(req.headers.clientid, CLIENT_ID)
        assert.deepEqual(JSON.parse(body), { clientId: CLIENT_ID, grantType: 'password', username: 'mock-admin', password: 'password-secret-do-not-log' })
        return json({ access_token: 'token-secret-do-not-log' })
      }
      if (url.pathname.includes('/admin/')) {
        assert.equal(req.headers.authorization, 'Bearer token-secret-do-not-log')
        assert.equal(req.headers.clientid, CLIENT_ID)
      } else {
        assert.equal(req.headers.authorization, undefined, '匿名接口与对象请求不得携带凭据')
      }
      if (url.pathname === '/easyshare/app/admin/releases') return json(options.releases ?? [])
      if (url.pathname === '/easyshare/app/admin/uploads') {
        preparedBody = JSON.parse(body)
        assert.equal(preparedBody.notes, '正式发布：修复更新链路')
        return json({ assetId: id, uploadUrl: `${baseUrl}/object?signature=presigned-secret-do-not-log` })
      }
      if (url.pathname === '/object' && req.method === 'PUT') {
        uploaded = body
        res.statusCode = 200
        return res.end()
      }
      if (url.pathname === `/easyshare/app/admin/assets/${idText}/publish`) {
        assert.deepEqual(uploaded, INSTALLER)
        published = true
        return json(null)
      }
      if (url.pathname === '/easyshare/app/latest') {
        assert.equal(url.searchParams.get('platform'), platform)
        return json(published ? { version, assets: [asset()] } : (options.latest ?? null))
      }
      if (url.pathname === `/easyshare/app/assets/${idText}/url`) return json({ url: `${baseUrl}/object?signature=download-secret-do-not-log` })
      if (url.pathname === '/object' && req.method === 'GET') {
        res.setHeader('Content-Length', INSTALLER.length)
        return res.end(INSTALLER)
      }
      res.statusCode = 404
      res.end()
    } catch (error) {
      res.statusCode = 500
      res.end(error.message)
    }
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  baseUrl = `http://127.0.0.1:${server.address().port}`
  t.after(() => new Promise(resolve => { server.closeAllConnections(); server.close(resolve) }))
  const warnings = []
  return {
    calls, asset, warnings,
    args: { platformUrl: baseUrl, username: 'mock-admin', password: 'password-secret-do-not-log', installerPath, version, notes: '正式发布：修复更新链路', platform, kind },
    dependencies: { warn: message => warnings.push(message) },
    get preparedBody() { return preparedBody },
  }
}

async function publisher() {
  return (await import('../publish-release.mjs')).publishRelease
}

test('正式发布经过登录、预签名直传、发布以及匿名清单和下载校验', async t => {
  const f = await fixture(t)
  const result = await (await publisher())(f.args, f.dependencies)
  assert.equal(result.version, '1.2.3')
  assert.equal(result.assetId, ASSET_ID)
  assert.equal(result.idempotent, false)
  assert.equal(result.sha256, createHash('sha256').update(INSTALLER).digest('hex'))
  assert.equal(f.preparedBody.sizeBytes, INSTALLER.length)
  assert.deepEqual(f.calls.map(c => `${c.method} ${c.path}`), [
    'POST /auth/login', 'GET /easyshare/app/admin/releases', 'GET /easyshare/app/latest',
    'POST /easyshare/app/admin/uploads', 'PUT /object',
    `POST /easyshare/app/admin/assets/${ASSET_ID}/publish`, 'GET /easyshare/app/latest',
    `GET /easyshare/app/assets/${ASSET_ID}/url`, 'GET /object',
  ])
  assert.match(f.warnings.join('\n'), /HTTP/)
  assert.doesNotMatch(f.warnings.join('\n'), /127\.0\.0\.1|secret/)
})

function release(asset, version = '1.2.3') {
  return { releaseId: '9223372036854775805', version, notes: '旧说明', assets: [asset] }
}

function noMutation(calls) {
  assert.equal(calls.filter(c => c.path.includes('/admin/') && c.method !== 'GET').length, 0, '预检失败或幂等时不得 prepare/publish')
  assert.equal(calls.filter(c => c.method === 'PUT' || c.method === 'DELETE').length, 0)
}

test('同版本同资产已发布时仅验证匿名清单及下载，幂等且不覆盖', async t => {
  const f = await fixture(t)
  const existing = f.asset()
  const mock = await fixture(t, { releases: [release(existing)], latest: { version: '1.2.3', assets: [existing] } })
  const result = await (await publisher())(mock.args, mock.dependencies)
  assert.equal(result.idempotent, true)
  assert.equal(result.assetId, ASSET_ID)
  noMutation(mock.calls)
  assert.equal(mock.calls.filter(c => c.path === '/object' && c.method === 'GET').length, 1)
})

for (const [name, difference] of [
  ['不同正文摘要', { sha256: 'a'.repeat(64) }],
  ['不同大小', { sizeBytes: 1 }],
  ['不同文件名', { filename: 'other.exe' }],
]) {
  for (const status of ['0', '1']) {
    test(`同版本${status === '1' ? '已发布' : 'pending'}资产${name}时拒绝且不 prepare`, async t => {
      const source = await fixture(t)
      const f = await fixture(t, { releases: [release({ ...source.asset(), status, ...difference })] })
      await assert.rejects((await publisher())(f.args, f.dependencies), /\[preflight-releases\].*冲突/)
      noMutation(f.calls)
    })
  }
}

test('pending 资产元数据相同允许重新上传和发布', async t => {
  const source = await fixture(t)
  const f = await fixture(t, { releases: [release({ ...source.asset(), status: '0' })] })
  const result = await (await publisher())(f.args, f.dependencies)
  assert.equal(result.idempotent, false)
  assert.equal(f.calls.filter(c => c.path === '/easyshare/app/admin/uploads').length, 1)
})

for (const [candidate, newer] of [['1.9.0', '1.10.0'], ['1.2.3', '2.0.0'], ['999999999999999999.0.0', '1000000000000000000.0.0']]) {
  test(`管理列表较新正式版本 ${newer} 阻止旧 tag ${candidate}，按整数段比较`, async t => {
    const source = await fixture(t)
    const f = await fixture(t, { releases: [release(source.asset(), newer)] })
    await assert.rejects((await publisher())({ ...f.args, version: candidate }, f.dependencies), /\[preflight-releases\].*较新/)
    noMutation(f.calls)
  })
}

test('匿名 latest 较新时即使管理列表为空也拒绝旧 tag', async t => {
  const source = await fixture(t)
  const f = await fixture(t, { latest: { version: '1.10.0', assets: [source.asset()] } })
  await assert.rejects((await publisher())({ ...f.args, version: '1.9.0' }, f.dependencies), /\[preflight-latest\].*较新/)
  noMutation(f.calls)
})

for (const [name, difference] of [
  ['版本', { version: '1.2.4' }], ['资产 ID', { id: '42' }],
  ['平台', { platform: 'macos' }], ['类型', { kind: 'dmg' }],
  ['大小', { sizeBytes: INSTALLER.length + 1 }], ['摘要', { sha256: 'a'.repeat(64) }],
  ['文件名', { filename: 'different.exe' }], ['状态', { status: '0' }],
]) {
  test(`发布后匿名清单${name}不匹配不得报成功`, async t => {
    let reads = 0
    const f = await fixture(t, { handle: ({ url, json, asset }) => {
      if (url.pathname !== '/easyshare/app/latest') return false
      json(++reads === 1 ? null : {
        version: difference.version ?? '1.2.3', assets: [{ ...asset(), ...difference }],
      })
      return true
    } })
    await assert.rejects((await publisher())(f.args, f.dependencies), /\[verify-manifest\]/)
    assert.equal(f.calls.filter(c => c.method === 'PUT').length, 1)
    assert.equal(f.calls.filter(c => c.path.endsWith('/publish')).length, 1)
    assert.equal(f.calls.filter(c => c.path.endsWith('/url')).length, 0)
  })
}

for (const mode of ['hash', 'short', 'long', 'content-length']) {
  test(`下载${mode}不匹配不得只凭清单版本报成功`, async t => {
    const f = await fixture(t, { handle: ({ req, res, url }) => {
      if (url.pathname !== '/object' || req.method !== 'GET') return false
      const bytes = mode === 'hash' ? Buffer.alloc(INSTALLER.length, 33) :
        mode === 'short' ? INSTALLER.subarray(1) : mode === 'long' ? Buffer.concat([INSTALLER, Buffer.from('!')]) : INSTALLER
      if (mode === 'content-length') res.setHeader('Content-Length', INSTALLER.length + 1)
      else res.setHeader('Transfer-Encoding', 'chunked')
      res.end(bytes)
      return true
    } })
    await assert.rejects((await publisher())(f.args, f.dependencies), /\[verify-download\].*(SHA256|大小|Content-Length)/)
  })
}

test('幂等验证也检查下载正文 SHA256，不覆盖已发布对象', async t => {
  const source = await fixture(t)
  const existing = source.asset()
  const f = await fixture(t, { releases: [release(existing)], latest: { version: '1.2.3', assets: [existing] }, handle: ({ req, res, url }) => {
    if (url.pathname !== '/object' || req.method !== 'GET') return false
    res.end(Buffer.alloc(INSTALLER.length, 33))
    return true
  } })
  await assert.rejects((await publisher())(f.args, f.dependencies), /\[verify-download\].*SHA256/)
  noMutation(f.calls)
})

for (const [route, method, stage] of [
  ['/auth/login', 'POST', 'login'],
  ['/easyshare/app/admin/releases', 'GET', 'preflight-releases'],
  ['/easyshare/app/admin/uploads', 'POST', 'prepare'],
  ['/object', 'PUT', 'upload'],
  [`/easyshare/app/admin/assets/${ASSET_ID}/publish`, 'POST', 'publish'],
  [`/easyshare/app/assets/${ASSET_ID}/url`, 'GET', 'resolve-download'],
  ['/object', 'GET', 'verify-download'],
]) {
  test(`${stage} HTTP 失败只输出阶段和 status，不自动重试或泄露响应正文`, async t => {
    const f = await fixture(t, { handle: ({ req, res, url }) => {
      if (url.pathname !== route || req.method !== method) return false
      res.statusCode = 503
      res.end('response-secret-do-not-log password-secret-do-not-log token-secret-do-not-log')
      return true
    } })
    await assert.rejects((await publisher())(f.args, f.dependencies), error => {
      assert.equal(error.stage, stage)
      assert.match(error.message, /status=503/)
      assert.doesNotMatch(error.message, /secret|127\.0\.0\.1|signature/)
      if (stage === 'login') assert.match(error.message, /认证失败/)
      return true
    })
    assert.equal(f.calls.filter(c => c.path === route && c.method === method).length, 1)
    assert.equal(f.calls.filter(c => c.method === 'DELETE').length, 0)
    if (stage === 'upload') assert.equal(f.calls.filter(c => c.path.endsWith('/publish')).length, 0)
  })
}

for (const code of [500, '200', 'password-secret-do-not-log']) {
  test(`登录 R.code=${typeof code === 'number' ? code : '非数字'} 严格失败且只输出安全错误码`, async t => {
    const f = await fixture(t, { handle: ({ url, json }) => {
      if (url.pathname !== '/auth/login') return false
      json({ access_token: 'token-secret-do-not-log' }, code)
      return true
    } })
    await assert.rejects((await publisher())(f.args, f.dependencies), error => {
      assert.match(error.message, /\[login\].*认证失败.*code=/)
      assert.doesNotMatch(error.message, /secret/)
      return true
    })
    noMutation(f.calls)
  })
}

test('上传准备业务错误不会上传对象，响应正文不透传', async t => {
  const f = await fixture(t, { handle: ({ url, json }) => {
    if (url.pathname !== '/easyshare/app/admin/uploads') return false
    json({ secret: 'token-secret-do-not-log' }, 403)
    return true
  } })
  await assert.rejects((await publisher())(f.args, f.dependencies), /\[prepare\].*code=403/)
  assert.equal(f.calls.filter(c => c.method === 'PUT').length, 0)
})

for (const id of [9007199254740992, 1.5, -1, '1/secret', '0', null]) {
  test(`prepare 拒绝非法或 unsafe 资产 ID（${typeof id}）`, async t => {
    const f = await fixture(t, { handle: ({ url, json }) => {
      if (url.pathname !== '/easyshare/app/admin/uploads') return false
      json({ assetId: id, uploadUrl: 'http://127.0.0.1/object?secret=token-secret-do-not-log' })
      return true
    } })
    await assert.rejects((await publisher())(f.args, f.dependencies), /\[prepare\].*ID/)
    assert.equal(f.calls.filter(c => c.method === 'PUT').length, 0)
  })
}

test('管理已发布资产 unsafe ID 在预检阶段拒绝', async t => {
  const source = await fixture(t)
  const f = await fixture(t, { releases: [release({ ...source.asset(), id: 9007199254740992 })] })
  await assert.rejects((await publisher())(f.args, f.dependencies), /\[preflight-releases\].*安全整数/)
  noMutation(f.calls)
})

test('匿名清单 unsafe ID 在验证阶段拒绝', async t => {
  let reads = 0
  const f = await fixture(t, { handle: ({ url, json, asset }) => {
    if (url.pathname !== '/easyshare/app/latest') return false
    json(++reads === 1 ? null : { version: '1.2.3', assets: [{ ...asset(), id: 9007199254740992 }] })
    return true
  } })
  await assert.rejects((await publisher())(f.args, f.dependencies), /\[verify-manifest\].*安全整数/)
})

for (const version of ['1.2.3-preview.1', '1.2.3+meta', 'v1.2.3', '01.2.3', '1.2', '1.2.3\n', '']) {
  test(`非法或预发布版本不会访问控制面（${JSON.stringify(version)}）`, async t => {
    const f = await fixture(t)
    await assert.rejects((await publisher())({ ...f.args, version }, f.dependencies), /\[validation\].*SemVer/)
    assert.equal(f.calls.length, 0)
  })
}

for (const difference of [
  { username: '' }, { password: '' }, { password: undefined },
  { platform: 'macos', kind: 'installer' }, { platform: 'windows', kind: 'dmg' },
  { platformUrl: 'ftp://127.0.0.1' }, { platformUrl: 'http://user:password-secret-do-not-log@127.0.0.1' },
  { platformUrl: 'not-a-url' }, { platformUrl: 'http://127.0.0.1/?token=secret' },
  { installerPath: 'missing-installer-file.exe' },
]) {
  test('凭据、平台类型、URL 或文件不合法时不发 HTTP 请求', async t => {
    const f = await fixture(t)
    await assert.rejects((await publisher())({ ...f.args, ...difference }, f.dependencies), error => {
      assert.equal(error.stage, 'validation')
      assert.doesNotMatch(error.message, /password-secret|token-secret|127\.0\.0\.1/)
      return true
    })
    assert.equal(f.calls.length, 0)
  })
}

test('空文件和目录不是可发布安装包', async t => {
  const f = await fixture(t)
  await writeFile(f.args.installerPath, '')
  await assert.rejects((await publisher())(f.args, f.dependencies), /\[validation\].*非空普通文件/)
  await assert.rejects((await publisher())({ ...f.args, installerPath: path.dirname(f.args.installerPath) }, f.dependencies), /\[validation\].*非空普通文件/)
  assert.equal(f.calls.length, 0)
})

for (const timeoutStage of ['login', 'upload', 'verify-download']) {
  test(`${timeoutStage} 超时预算包含请求及响应流读取且无敏感错误`, async t => {
    const f = await fixture(t, { handle: ({ req, res, url }) => {
      if (timeoutStage === 'login' && url.pathname === '/auth/login') return true
      if (timeoutStage === 'upload' && url.pathname === '/object' && req.method === 'PUT') return true
      if (timeoutStage === 'verify-download' && url.pathname === '/object' && req.method === 'GET') {
        res.writeHead(200, { 'Transfer-Encoding': 'chunked' })
        res.write(INSTALLER.subarray(0, 1))
        return true
      }
      return false
    } })
    await assert.rejects((await publisher())(f.args, { ...f.dependencies,
      controlTimeoutMs: timeoutStage === 'login' ? 100 : 30_000, transferTimeoutMs: 100,
    }), error => {
      assert.equal(error.stage, timeoutStage)
      assert.match(error.message, /超时/)
      assert.doesNotMatch(error.message, /secret|127\.0\.0\.1/)
      return true
    })
    if (timeoutStage === 'upload') assert.equal(f.calls.filter(c => c.path.endsWith('/publish')).length, 0)
  })
}

async function cli(args, env) {
  const child = spawn(process.execPath, [fileURLToPath(new URL('../publish-release.mjs', import.meta.url)), ...args], {
    env: { ...process.env, EASYSHARE_RELEASE_URL: '', PLATFORM_URL: '', EASYSHARE_RELEASE_USERNAME: '', EASYSHARE_RELEASE_PASSWORD: '', ...env },
    stdio: ['ignore', 'pipe', 'pipe'],
  })
  let stdout = ''
  let stderr = ''
  child.stdout.on('data', chunk => { stdout += chunk })
  child.stderr.on('data', chunk => { stderr += chunk })
  const code = await new Promise((resolve, reject) => { child.on('error', reject); child.on('close', resolve) })
  return { code, stdout, stderr }
}

function cliArgs(f) {
  return ['--installer-path', f.args.installerPath, '--version', '1.2.3', '--notes', f.args.notes, '--platform', 'windows', '--kind', 'installer']
}

function cliEnv(f) {
  return { EASYSHARE_RELEASE_URL: f.args.platformUrl, EASYSHARE_RELEASE_USERNAME: f.args.username, EASYSHARE_RELEASE_PASSWORD: f.args.password }
}

function assertSecretFree(output) {
  assert.doesNotMatch(output, /secret-do-not-log|Bearer|signature=|127\.0\.0\.1|response-secret|mock-admin/)
}

for (const urlEnv of ['EASYSHARE_RELEASE_URL', 'PLATFORM_URL']) {
  test(`CLI 从 ${urlEnv} 与环境凭据发布，输出不包含口令、令牌或预签名 URL`, async t => {
    const f = await fixture(t)
    const env = cliEnv(f)
    delete env.EASYSHARE_RELEASE_URL
    env[urlEnv] = f.args.platformUrl
    const result = await cli(cliArgs(f), env)
    assert.equal(result.code, 0, result.stderr)
    assert.match(result.stdout, /SHA256 验证通过/)
    assert.match(result.stderr, /HTTP/)
    assertSecretFree(result.stdout + result.stderr)
  })
}

test('CLI --platform-url 优先于环境 URL', async t => {
  const f = await fixture(t)
  const result = await cli([...cliArgs(f), '--platform-url', f.args.platformUrl], { ...cliEnv(f), EASYSHARE_RELEASE_URL: 'invalid' })
  assert.equal(result.code, 0, result.stderr)
  assertSecretFree(result.stdout + result.stderr)
})

for (const flag of ['--password', '--username', '--password=password-secret-do-not-log', '--token']) {
  test(`CLI 拒绝凭据参数且不回显参数（${flag.split('=')[0]}）`, async t => {
    const f = await fixture(t)
    const result = await cli([...cliArgs(f), flag, 'password-secret-do-not-log'], cliEnv(f))
    assert.equal(result.code, 1)
    assert.match(result.stderr, /只能通过环境变量/)
    assertSecretFree(result.stdout + result.stderr)
    assert.equal(f.calls.length, 0)
  })
}

test('CLI version 必传；凭据缺失也必须失败且不得访问控制面', async t => {
  const f = await fixture(t)
  const noVersion = await cli(['--installer-path', f.args.installerPath], cliEnv(f))
  assert.equal(noVersion.code, 1)
  assert.match(noVersion.stderr, /必须提供 --version/)
  const noCredentials = await cli(cliArgs(f), { EASYSHARE_RELEASE_URL: f.args.platformUrl })
  assert.equal(noCredentials.code, 1)
  assert.match(noCredentials.stderr, /缺少发布凭据/)
  assert.equal(f.calls.length, 0)
})

test('CLI 认证错误不输出 R.msg、口令或响应正文', async t => {
  const f = await fixture(t, { handle: ({ url, json }) => {
    if (url.pathname !== '/auth/login') return false
    json({ token: 'token-secret-do-not-log' }, 500)
    return true
  } })
  const result = await cli(cliArgs(f), cliEnv(f))
  assert.equal(result.code, 1)
  assert.match(result.stderr, /\[login\].*认证失败.*code=500/)
  assertSecretFree(result.stdout + result.stderr)
})

test('macos/dmg 正式发布同样完成匿名清单和实际下载验证', async t => {
  const f = await fixture(t, { platform: 'macos', kind: 'dmg' })
  const result = await (await publisher())(f.args, f.dependencies)
  assert.equal(result.platform, 'macos')
  assert.equal(result.kind, 'dmg')
  assert.equal(result.filename, 'EasyShare-arm64.dmg')
  assert.equal(f.preparedBody.platform, 'macos')
  assert.equal(f.preparedBody.kind, 'dmg')
  assert.equal(f.calls.filter(c => c.path === '/object' && c.method === 'GET').length, 1)
})

test('安全数字资产 ID 转为字符串并正确发布验证', async t => {
  const f = await fixture(t, { id: 42 })
  const result = await (await publisher())(f.args, f.dependencies)
  assert.equal(result.assetId, '42')
  assert.equal(f.calls.filter(c => c.path === '/easyshare/app/admin/assets/42/publish').length, 1)
  assert.equal(f.calls.filter(c => c.path === '/easyshare/app/assets/42/url').length, 1)
})

for (const route of ['/auth/login', '/object']) {
  test(`${route} 重定向不会转发口令、令牌或预签名请求`, async t => {
    const f = await fixture(t, { handle: ({ req, res, url }) => {
      if (url.pathname !== route || (route === '/object' && req.method !== 'PUT')) return false
      res.writeHead(307, { Location: '/redirect-secret-do-not-log' })
      res.end('response-secret-do-not-log')
      return true
    } })
    await assert.rejects((await publisher())(f.args, f.dependencies), error => {
      assert.match(error.message, /status=307/)
      assertSecretFree(error.message)
      return true
    })
    assert.equal(f.calls.filter(c => c.path === '/redirect-secret-do-not-log').length, 0)
  })
}

for (const [name, route, data] of [
  ['管理版本列表', '/easyshare/app/admin/releases', null],
  ['匿名 latest', '/easyshare/app/latest', { version: '1.2.3', assets: null }],
]) {
  test(`${name} 非法响应格式不进行 prepare`, async t => {
    const f = await fixture(t, { handle: ({ url, json }) => {
      if (url.pathname !== route) return false
      json(data)
      return true
    } })
    await assert.rejects((await publisher())(f.args, f.dependencies), /格式非法/)
    noMutation(f.calls)
  })
}

// 只在 Windows 执行真实兼容入口；异步子进程不阻塞 loopback 控制面。
test('Windows PowerShell 5.1 wrapper 通过环境凭据调用 Node 并完成匿名回验', {
  skip: process.platform !== 'win32', timeout: 30_000,
}, async t => {
  const { checkReleaseVersion } = await import('../check-release-version.mjs')
  const version = checkReleaseVersion()
  const f = await fixture(t, { version })
  const executable = path.join(process.env.SystemRoot ?? 'C:\\Windows', 'System32', 'WindowsPowerShell', 'v1.0', 'powershell.exe')
  const wrapper = fileURLToPath(new URL('../publish-release.ps1', import.meta.url))
  const child = spawn(executable, [
    '-NoProfile', '-ExecutionPolicy', 'Bypass', '-File', wrapper,
    '-PlatformUrl', f.args.platformUrl, '-InstallerPath', f.args.installerPath,
    '-Version', version, '-Notes', f.args.notes,
  ], { env: { ...process.env, ...cliEnv(f), GITHUB_REF_TYPE: '', GITHUB_REF_NAME: '' }, stdio: ['ignore', 'pipe', 'pipe'] })
  t.after(() => { if (child.exitCode === null) child.kill() })
  let stdout = ''
  let stderr = ''
  child.stdout.on('data', chunk => { stdout += chunk })
  child.stderr.on('data', chunk => { stderr += chunk })
  const code = await new Promise((resolve, reject) => { child.on('error', reject); child.on('close', resolve) })
  assert.equal(code, 0, stderr)
  assertSecretFree(stdout + stderr)
  assert.equal(f.preparedBody.version, version)
  assert.equal(f.calls.filter(c => c.path.endsWith('/publish')).length, 1)
  assert.equal(f.calls.filter(c => c.path === '/object' && c.method === 'GET').length, 1)
})
