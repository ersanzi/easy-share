// 本地真实 Vue 界面的动效验收；使用独立浏览器上下文和 Wails 替身，不连接业务服务器。
// 先启动 docs/design/oil-ui-redesign/preview 的 Vite，再设置 EASYSHARE_PLAYWRIGHT_MODULE。
import { createRequire } from 'node:module'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
const require = createRequire(import.meta.url)
const { chromium } = require(process.env.EASYSHARE_PLAYWRIGHT_MODULE || 'playwright')
const output = path.resolve(process.argv[2] || 'build/motion-evidence')
await mkdir(output, { recursive: true })
const browser = await chromium.launch(process.env.EASYSHARE_BROWSER
  ? { executablePath: process.env.EASYSHARE_BROWSER, headless: true }
  : { channel: 'chrome', headless: true })
const page = await browser.newPage({ viewport: { width: 1440, height: 960 } })
const errors = []
page.on('pageerror', error => errors.push(error.message))
page.on('console', message => { if (message.type() === 'error') errors.push(message.text()) })
const fixture = String.raw`
const features = { knowledge:true, meeting:true, drive:true, shared:true, plugin:true };
const testUser = { ...user, userId:'1876001234567890123', adminScope:'all', features };
const api = window.go.main.App;
api.CurrentUser = () => Promise.resolve(testUser);
api.Login = () => Promise.resolve(testUser);
api.AdminPermissionMembers = () => Promise.resolve([{userId:'1876001234567890124',userName:'test',nickName:'测试成员',deptId:'101',deptName:'产品部',status:'0',...features,roleIds:[],roleNames:[],adminScope:'none'}]);
api.AdminPermissionRoles = () => Promise.resolve([]);
api.CloudUsage = () => Promise.resolve({usedBytes:12345678,quotaBytes:1073741824});
const handlers = new Map();
const oldRuntime = window.runtime;
window.runtime = new Proxy({}, {get:(_,key) => key === 'EventsOnMultiple'
  ? (name,handler) => { handlers.set(name,handler); return () => handlers.delete(name); }
  : key === 'EventsOff' ? name => handlers.delete(name) : oldRuntime[key]});
window.__motionEmit = (name,data) => handlers.get(name)?.(data);
`
await page.route('**/mock.ts', async route => {
  const response = await route.fetch()
  await route.fulfill({ response, body: (await response.text()) + '\n' + fixture })
})
const frames = count => page.evaluate(count => new Promise(resolve => {
  const next = () => --count > 0 ? requestAnimationFrame(next) : resolve()
  requestAnimationFrame(next)
}), count)
const check = (value, reason) => { if (!value) throw new Error(reason) }
const base = process.env.EASYSHARE_MOTION_URL || 'http://127.0.0.1:5178'
const report = { date: new Date().toISOString(), browser: await browser.version(), viewport: { width:1440, height:960 }, source:'真实 Vue 源码 + 独立浏览器 Wails 替身', checks:[], measurements:[] }
try {
  await page.goto(base + '/?scene=overview')
  await page.locator('.workspace').waitFor()
  await page.evaluate(() => document.fonts.ready)
  await frames(15)
  // 进入、退出、反向复用同一 DOM，退出期立即 inert。
  const reversal = await page.evaluate(async () => {
    const button = document.querySelector('.activity-trigger')
    const wait = count => new Promise(resolve => { const tick=()=>--count?requestAnimationFrame(tick):resolve();requestAnimationFrame(tick) })
    const first = document.querySelector('.activity-backdrop')
    button.click(); await wait(5)
    button.click(); await wait(2)
    const inert = first.inert
    button.click(); await wait(25)
    return { same:first === document.querySelector('.activity-backdrop'), count:document.querySelectorAll('.activity-drawer').length, inert, opacity:getComputedStyle(first).opacity }
  })
  check(reversal.same && reversal.count === 1 && reversal.inert && reversal.opacity === '1', '抽屉反向开合或退出隔离失败')
  report.checks.push({name:'抽屉连续反向/单实例/退出隔离',...reversal})
  await page.screenshot({ path:path.join(output,'activity.png') })
  await page.keyboard.press('Escape')
  await frames(20)
  check(await page.locator('.activity-backdrop').isHidden(), '抽屉退出未完成')
  await page.locator('.shutdown-btn').click()
  await page.locator('.confirm-card').waitFor({ state:'visible' })
  await page.keyboard.press('Shift+Tab')
  check(await page.locator('.confirm-submit').evaluate(element => element === document.activeElement), '确认框焦点未限制')
  await page.keyboard.press('Escape')
  await frames(15)
  check(await page.locator('.confirm-overlay').isHidden(), '确认框关闭未完成')
  report.checks.push({ name:'确认框键盘循环/退出', passed:true })
  // 页面立即提交最新目标；不排队等退出，不保留陈旧界面。
  await page.getByRole('button',{name:/附近设备/}).click()
  await page.getByRole('button',{name:/任务中心/}).click()
  await page.locator('.transfer-panel').waitFor()
  await frames(15)
  report.checks.push({name:'连续页面切换落在最新目标',passed:true})
  await page.evaluate(async () => {
    const original = await window.go.main.App.GetSnapshot()
    const tasks = Array.from({length:1000}, (_,index)=>({...original.tasks[0],id:'perf-'+index,fileName:'企业文件-'+index+'.pdf',status:'running',totalBytes:1000000,transferredBytes:(index%99)*10000,updatedAt:'2026-11-25T00:00:00Z'}))
    window.__motionTasks=tasks
    window.go.main.App.GetSnapshot=()=>Promise.resolve(structuredClone({...original,tasks}))
    window.__motionEmit('core-event', JSON.stringify({type:'status.changed'}))
  })
  await page.locator('[data-task-id="perf-999"]').waitFor({state:'attached'})
  await frames(25)
  const session = await page.context().newCDPSession(page)
  await session.send('Performance.enable')
  const measure = async (name, mode) => {
    const before = await session.send('Performance.getMetrics')
    const result = await page.evaluate(mode => new Promise(resolve => {
      const intervals=[],slowFrames=[];let previous=0,index=0;const start=performance.now()
      const longTasks=[]
      const observed=PerformanceObserver.supportedEntryTypes?.includes('longtask')
      const collect=entries=>entries.forEach(entry=>longTasks.push({startMs:entry.startTime-start,durationMs:entry.duration}))
      const observer=observed?new PerformanceObserver(list=>collect(list.getEntries())):null
      observer?.observe({type:'longtask'})
      const host=document.querySelector('.workspace')
      const fills=[...document.querySelectorAll('.transfer-row .progress-track i')].slice(0,16)
      const tick=now=>{
        if(previous) { intervals.push(now-previous); if(now-previous>25)slowFrames.push({frame:index,elapsedMs:now-start,intervalMs:now-previous}) }
        previous=now; index++
        if(mode==='scroll-realtime')host.scrollTop+=3
        if(index%6===0){
          if(mode==='compositor')fills.forEach((bar,n)=>bar.style.transform='scaleX('+((index+n)%100)/100+')')
          else {const n=index%8;const task={...window.__motionTasks[n],transferredBytes:(index%99)*10000};window.__motionTasks[n]=task;window.__motionEmit('core-event',JSON.stringify({type:'transfer.updated',data:task}))}
        }
        if(now-start<5000)requestAnimationFrame(tick)
        else {if(observer){collect(observer.takeRecords());observer.disconnect()}const sorted=[...intervals].sort((a,b)=>a-b);resolve({rows:document.querySelectorAll('.transfer-row').length,longTasks:observed?longTasks:null,slowFrames,frames:intervals.length,p50Ms:sorted[Math.floor(sorted.length*.5)],p95Ms:sorted[Math.floor(sorted.length*.95)],maxMs:Math.max(...intervals),framesOver25Ms:intervals.filter(n=>n>25).length,durationMs:now-start,overflowPx:document.documentElement.scrollWidth-innerWidth})}
      }
      requestAnimationFrame(tick)
    }),mode)
    const after=await session.send('Performance.getMetrics')
    const delta=name=>after.metrics.find(item=>item.name===name).value-before.metrics.find(item=>item.name===name).value
    const sample={name,...result,layoutCount:delta('LayoutCount'),layoutMs:delta('LayoutDuration')*1000,scriptMs:delta('ScriptDuration')*1000}
    report.measurements.push(sample)
    check(sample.overflowPx===0,'长列表出现横向溢出')
    check(sample.p95Ms<=25,'长列表帧时间 p95 超过 25ms；需复核现场负载')
    return sample
  }
  const compositor = await measure('千行列表16条进度动画','compositor')
  check(compositor.layoutCount<=2,'进度缩放引起逐帧布局')
  await measure('千行列表滚动与真实事件更新','scroll-realtime')
  // 屏外渲染不能破坏纯键盘导航：完成状态含操作按钮，Tab 跨过延迟渲染边界。
  await page.evaluate(() => {
    window.__motionTasks.forEach(task => {
      task.status='completed'; task.localPath='C:/fixture/'+task.fileName
    })
    window.__motionEmit('core-event', JSON.stringify({type:'status.changed'}))
  })
  await frames(30)
  const rowBeforeBoundary=page.locator('.transfer-list > .transfer-row').nth(23)
  const nextRow=page.locator('.transfer-list > .transfer-row').nth(24)
  await rowBeforeBoundary.locator('button').last().focus()
  await page.keyboard.press('Tab')
  check(await nextRow.evaluate(row=>row.contains(document.activeElement)), '屏外行的键盘导航不可达')
  report.checks.push({name:'键盘跨越屏外渲染边界',passed:true})
  await page.screenshot({path:path.join(output,'long-list.png')})
  // 动态更改偏好，确认 JS 和 CSS 都响应，进度保持实际最终值。
  await page.emulateMedia({reducedMotion:'reduce'})
  await page.locator('.activity-trigger').click()
  await frames(15)
  const reduced=await page.evaluate(()=>({
    preference:matchMedia('(prefers-reduced-motion: reduce)').matches,
    drawerTransform:getComputedStyle(document.querySelector('.activity-drawer')).transform,
    progressTransition:getComputedStyle(document.querySelector('.progress-track i')).transitionProperty,
    animations:document.getAnimations().map(a=>a.effect.getKeyframes()).flat().filter(k=>k.transform||k.scale||k.translate).length,
    overflow:document.documentElement.scrollWidth-innerWidth,
  }))
  check(reduced.preference && reduced.drawerTransform==='none' && reduced.progressTransition==='none' && reduced.animations===0,'减少动态效果仍有空间动画')
  report.checks.push({name:'系统减少动态效果',...reduced})
  await page.keyboard.press('Escape')
  await frames(15)
  await page.emulateMedia({reducedMotion:'no-preference'})
  await page.setViewportSize({width:1024,height:720})
  await page.locator('.activity-trigger').click()
  await frames(20)
  const compact = await page.evaluate(()=>({overflow:document.documentElement.scrollWidth-innerWidth,bottom:document.querySelector('.activity-drawer').getBoundingClientRect().bottom,height:innerHeight}))
  check(compact.overflow===0&&compact.bottom<=compact.height,'缩小窗口后抽屉越界')
  report.checks.push({name:'1024×720 尺寸变化',...compact})
  check(errors.length===0, '浏览器错误：'+errors.join('\n'))
  report.passed=true
} catch(error) {
  report.passed=false
  report.failure=String(error)
  process.exitCode=1
} finally {
  report.errors=errors
  await writeFile(path.join(output,'report.json'),JSON.stringify(report,null,2)+'\n')
  console.log(JSON.stringify(report,null,2))
  await browser.close()
}
