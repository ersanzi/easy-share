import { createHash } from 'node:crypto'
import { createReadStream, readFileSync, writeFileSync } from 'node:fs'
import { stat } from 'node:fs/promises'
import assert from 'node:assert/strict'
import { resolve } from 'node:path'
import { pathToFileURL } from 'node:url'
import { checkReleaseVersion } from './check-release-version.mjs'

// 构建与发布不共用工作目录，用摘要清单把 tag、提交、目标控制面与唯一安装资产绑在一起。
export async function releaseArtifact({ root, directory, platform, commit, tag, platformUrl, write = false }) {
  const version = checkReleaseVersion({ root, tag })
  assert.ok(/^[0-9a-f]{40}$/.test(commit), '发布提交必须是完整 SHA')
  const url = new URL(platformUrl)
  assert.ok(['http:', 'https:'].includes(url.protocol) && !url.username && !url.password && !url.search && !url.hash, '控制面地址必须是无凭据的 HTTP(S) 基地址')
  const filename = platform === 'windows' ? 'EasyShare-amd64-installer.exe' : platform === 'macos' ? 'EasyShare.dmg' : ''
  assert.ok(filename, '发布平台必须是 windows 或 macos')
  const file = resolve(directory, filename)
  const info = await stat(file)
  assert.ok(info.isFile() && info.size > 0, '安装资产必须是非空文件')
  const hash = createHash('sha256')
  for await (const chunk of createReadStream(file)) hash.update(chunk)
  const metadata = { version, platform, commit, tag, platformUrl: url.href.replace(/\/$/, ''), filename, size: info.size, sha256: hash.digest('hex') }
  const metadataPath = resolve(directory, 'release-metadata.json')
  if (write) writeFileSync(metadataPath, `${JSON.stringify(metadata, null, 2)}\n`)
  else assert.deepEqual(JSON.parse(readFileSync(metadataPath, 'utf8')), metadata, '安装资产与本次 tag 构建证据不一致，禁止发布')
  return metadata
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    const args = process.argv.slice(2)
    assert.ok(args.length === 2 && ['--write', '--verify'].includes(args[0]), '用法：node scripts/check-release-artifact.mjs --write|--verify windows|macos')
    await releaseArtifact({ directory: 'build/bin', platform: args[1], commit: process.env.GITHUB_SHA, tag: process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : '', platformUrl: process.env.PLATFORM_URL, write: args[0] === '--write' })
    console.log('安装资产版本、提交、目标与摘要校验通过')
  } catch (error) {
    // 不输出响应、凭据或地址；底层 JSON/URL 解析错误也不得带出输入值。
    console.error(error instanceof assert.AssertionError ? error.message.split('\n')[0] : '安装资产校验失败：检查文件与构建元数据')
    process.exitCode = 1
  }
}
