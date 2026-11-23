import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'

const defaultRoot = fileURLToPath(new URL('../', import.meta.url))
const semver = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z]+(?:[.-][0-9A-Za-z]+)*)?$/

// tag 只是发布入口，不能替代真正编进桌面壳/Core 和安装包的版本号。
export function checkReleaseVersion({ root = defaultRoot, tag = '' } = {}) {
  const go = readFileSync(resolve(root, 'internal/version/version.go'), 'utf8')
  const match = go.match(/^const\s+Version\s*=\s*"([^"]+)"/m)
  assert.ok(match, 'version.go 未声明产品版本')
  const version = match[1]
  assert.ok(semver.test(version), '产品版本必须是三段 SemVer')
  const wails = JSON.parse(readFileSync(resolve(root, 'wails.json'), 'utf8'))
  const frontend = JSON.parse(readFileSync(resolve(root, 'frontend/package.json'), 'utf8'))
  assert.equal(wails.info?.productVersion, version, 'Wails 安装包版本与 Go 产品版本不一致')
  assert.equal(frontend.version, version, '前端版本与 Go 产品版本不一致')
  if (tag) {
    assert.ok(tag.startsWith('v') && semver.test(tag.slice(1)), '发布 tag 必须是 v 加三段 SemVer')
    assert.equal(tag.slice(1), version, 'tag 与包内产品版本不一致：先同步三处版本，再打新 tag')
  }
  return version
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  try {
    const args = process.argv.slice(2)
    assert.ok(args.length === 0 || (args.length === 2 && args[0] === '--tag'), '用法：node scripts/check-release-version.mjs [--tag v0.5.3]')
    const version = checkReleaseVersion({ tag: args[1] || (process.env.GITHUB_REF_TYPE === 'tag' ? process.env.GITHUB_REF_NAME : '') })
    console.log(`产品版本校验通过：${version}`)
  } catch (error) {
    console.error(`版本校验失败：${error.message}`)
    process.exitCode = 1
  }
}
