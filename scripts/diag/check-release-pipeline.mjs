import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

const root = fileURLToPath(new URL('../../', import.meta.url))
const normalize = text => text.replaceAll('\r\n', '\n')

// 守卫调用位置、正式 tag 条件和同一次构建资产，不能用注释里的脚本名冒充已接通。
export function checkReleasePipeline(workflow, platform) {
  const text = normalize(workflow)
  const split = text.indexOf('\n  publish-update:\n')
  assert.ok(split > 0, `${platform}: 正式 tag 构建后没有独立控制面发布任务`)
  const build = text.slice(0, split)
  const publish = text.slice(split)
  const buildJob = platform === 'windows' ? 'build-windows' : 'build-mac'
  const artifact = platform === 'windows' ? 'EasyShare-Windows' : 'EasyShare-macOS'
  const checks = [
    [build.includes('run: node scripts/check-release-version.mjs'), '未校验 tag 与产品版本'],
    [build.includes('node scripts/check-release-artifact.mjs --write'), '构建没有生成资产证据'],
    [build.includes('build/bin/release-metadata.json'), '构建证据没有随资产上传'],
    [publish.includes(`needs: ${buildJob}`), '发布不依赖对应平台构建成功'],
    [publish.includes("github.event_name == 'push'") && publish.includes("startsWith(github.ref, 'refs/tags/v')") && publish.includes("!contains(github.ref_name, '-')"), '发布未隔离正式 tag/预发布/手动构建'],
    [publish.includes('actions/download-artifact@') && publish.includes(`name: ${artifact}-\${{ github.run_number }}`), '发布未绑定本次构建资产'],
    [publish.includes('node scripts/check-release-artifact.mjs --verify'), '发布前没有复核资产证据'],
    [publish.includes('node scripts/publish-release.mjs --installer-path'), '发布器未执行'],
    [publish.indexOf('--verify') < publish.indexOf('node scripts/publish-release.mjs'), '资产校验必须先于外部发布'],
    [publish.includes('EASYSHARE_RELEASE_USERNAME: ${{ secrets.EASYSHARE_RELEASE_USERNAME }}') && publish.includes('EASYSHARE_RELEASE_PASSWORD: ${{ secrets.EASYSHARE_RELEASE_PASSWORD }}'), '发布凭据没有通过 secrets 注入'],
    [!build.includes('secrets.EASYSHARE_RELEASE_'), '发布凭据泄漏到构建任务'],
    [publish.includes('group: control-plane-update') && publish.includes('cancel-in-progress: false'), '双平台发布没有串行保护'],
    [!publish.includes('continue-on-error: true'), '发布失败不能静默忽略'],
    [publish.includes('PLATFORM_URL:') && build.includes('PLATFORM_URL:'), '发布目标与构建目标未绑定'],
  ]
  for (const [ok, message] of checks) assert.ok(ok, `${platform}: ${message}`)
}

for (const platform of ['windows', 'mac']) {
  const workflow = readFileSync(`${root}.github/workflows/build-${platform}.yml`, 'utf8')
  checkReleasePipeline(workflow, platform)
  // 检查器本身必须能抓住回退：删除调用、绕过 tag 门槛或解开本次资产绑定都应报红。
  for (const broken of [
    workflow.replace('node scripts/publish-release.mjs', 'node scripts/no-publish.mjs'),
    workflow.replaceAll("!contains(github.ref_name, '-')", 'true'),
    workflow.replaceAll('${{ github.run_number }}', 'old-run'),
    workflow.replace('--verify', '--write'),
  ]) assert.throws(() => checkReleasePipeline(broken, platform))
}
console.log('正式 tag → 版本/资产校验 → 控制面发布链路检查与回退自检通过')
