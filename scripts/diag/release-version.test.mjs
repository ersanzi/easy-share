import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { test } from 'node:test'
import { checkReleaseVersion } from '../check-release-version.mjs'
import { releaseArtifact } from '../check-release-artifact.mjs'

function fixture(t, version = '0.5.3') {
  const root = mkdtempSync(join(tmpdir(), 'easyshare-release-version-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  mkdirSync(join(root, 'internal/version'), { recursive: true })
  mkdirSync(join(root, 'frontend'))
  mkdirSync(join(root, 'build/bin'), { recursive: true })
  writeFileSync(join(root, 'internal/version/version.go'), `package version\nconst Version = "${version}"\n`)
  writeFileSync(join(root, 'wails.json'), JSON.stringify({ info: { productVersion: version } }))
  writeFileSync(join(root, 'frontend/package.json'), JSON.stringify({ version }))
  return root
}

test('正式 tag 与三个产品版本一致', t => {
  assert.equal(checkReleaseVersion({ root: fixture(t), tag: 'v0.5.3' }), '0.5.3')
})

test('错误 tag、二段版本与带前导零版本拒绝', t => {
  const root = fixture(t)
  for (const tag of ['v0.5.2', '0.5.3', 'v0.5', 'v00.5.3', 'latest']) {
    assert.throws(() => checkReleaseVersion({ root, tag }))
  }
  assert.throws(() => checkReleaseVersion({ root: fixture(t, '0.5') }))
})

test('预发布 tag 可以构建，但必须与产品版本一致', t => {
  assert.equal(checkReleaseVersion({ root: fixture(t, '0.5.3-preview.1'), tag: 'v0.5.3-preview.1' }), '0.5.3-preview.1')
})

test('Wails 与前端版本漂移分别阻断', t => {
  const root = fixture(t)
  writeFileSync(join(root, 'wails.json'), JSON.stringify({ info: { productVersion: '0.5.2' } }))
  assert.throws(() => checkReleaseVersion({ root }), /Wails/)
  writeFileSync(join(root, 'wails.json'), JSON.stringify({ info: { productVersion: '0.5.3' } }))
  writeFileSync(join(root, 'frontend/package.json'), JSON.stringify({ version: '0.5.2' }))
  assert.throws(() => checkReleaseVersion({ root }), /前端/)
})

for (const platform of ['windows', 'macos']) {
  test(`${platform}：资产绑定 tag/提交/目标/正文，篡改或换包拒绝`, async t => {
    const root = fixture(t)
    const directory = join(root, 'build/bin')
    const filename = platform === 'windows' ? 'EasyShare-amd64-installer.exe' : 'EasyShare.dmg'
    writeFileSync(join(directory, filename), '模拟安装资产')
    const options = { root, directory, platform, tag: 'v0.5.3', commit: 'a'.repeat(40), platformUrl: 'https://release.example.test' }
    await releaseArtifact({ ...options, write: true })
    await releaseArtifact(options)
    for (const changed of [{ commit: 'b'.repeat(40) }, { platformUrl: 'https://other.example.test' }, { tag: 'v0.5.4' }]) {
      await assert.rejects(releaseArtifact({ ...options, ...changed }))
    }
    writeFileSync(join(directory, filename), '替换成另一个安装资产')
    await assert.rejects(releaseArtifact(options), /不一致/)
  })
}

test('空包、缺包、非法提交及控制面地址拒绝', async t => {
  const root = fixture(t)
  const directory = join(root, 'build/bin')
  const options = { root, directory, platform: 'windows', tag: 'v0.5.3', commit: 'a'.repeat(40), platformUrl: 'https://release.example.test', write: true }
  await assert.rejects(releaseArtifact(options))
  writeFileSync(join(directory, 'EasyShare-amd64-installer.exe'), '')
  await assert.rejects(releaseArtifact(options), /非空/)
  writeFileSync(join(directory, 'EasyShare-amd64-installer.exe'), 'ok')
  await assert.rejects(releaseArtifact({ ...options, commit: 'abc' }), /完整 SHA/)
  for (const platformUrl of ['ftp://example.test', 'https://user:pass@example.test', 'https://example.test/?token=secret']) {
    await assert.rejects(releaseArtifact({ ...options, platformUrl }))
  }
})
