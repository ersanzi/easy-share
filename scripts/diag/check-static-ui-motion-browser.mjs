// 六组静态 UI 的真实浏览器验收。复用已有 Playwright，禁止连接真实业务接口。
// EASYSHARE_PLAYWRIGHT_MODULE 指向现有模块；EASYSHARE_BROWSER 可指定浏览器 exe。
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFile, mkdtemp } from 'node:fs/promises';
import path from 'node:path';
import os from 'node:os';
import { fileURLToPath } from 'node:url';
const require = createRequire(import.meta.url);
const { chromium } = require(process.env.EASYSHARE_PLAYWRIGHT_MODULE || 'playwright');
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const output = await mkdtemp(path.join(os.tmpdir(), 'easyshare-static-motion-'));
const browser = await chromium.launch(process.env.EASYSHARE_BROWSER
  ? {executablePath:process.env.EASYSHARE_BROWSER,headless:true} : {channel:'chrome',headless:true});
const checks = [];
const errors = [];
const aliases = {
  '/lab/assets/lab.css':'knowledge/app/lab/lab.css', '/lab/assets/lab.js':'knowledge/app/lab/lab.js',
  '/lab/cockpit/cockpit.css':'knowledge/app/debug/cockpit.css',
  '/lab/cockpit/cockpit.js':'knowledge/app/debug/cockpit.js',
};
async function open(file, reducedMotion = 'no-preference') {
  const page = await browser.newPage({viewport:{width:1200,height:900}, reducedMotion});
  page.setDefaultTimeout(6000);
  page.on('pageerror', error => errors.push(file + ': ' + error.message));
  await page.route('**/*', async route => {
    const pathname = new URL(route.request().url()).pathname;
    if (pathname === '/plugins/_sdk/eshare.js') return route.fulfill({contentType:'text/javascript',body:''});
    const local = aliases[pathname] || pathname.slice(1);
    if (!/^(plugins\/(clipboard|todo|template)\/|knowledge\/app\/(lab|debug|wps_addon)\/)/.test(local) || local.includes('..')) {
      return route.fulfill({status:404,body:'fixture only'});
    }
    const body = await readFile(path.join(root,local));
    const contentType = local.endsWith('.css') ? 'text/css' : local.endsWith('.js') ? 'text/javascript' : 'text/html';
    await route.fulfill({contentType,body});
  });
  await page.addInitScript(() => {
    window.__requests = [];
    const job = {id:'job1',status:'completed',stage:'completed',progress:100,filename:'示例.pdf',file_id:'doc1',version_id:'v1',created_at:'2026-11-25T00:00:00Z'};
    window.__jobs = [job];
    window.__queue = (name, payload, type = 'sdk') => new Promise((resolve,reject) => window.__requests.push({name,payload,type,resolve,reject}));
    window.__respond = (name,value,status = 200) => {
      const index = window.__requests.findIndex(item => item.name === name);
      if (index < 0) throw new Error('没有等待中的请求：' + name);
      const item = window.__requests.splice(index,1)[0];
      if (item.type === 'xhr') {
        item.xhr.status = status; item.xhr.responseText = JSON.stringify(value); item.xhr.readyState = 4;
        item.xhr.onreadystatechange?.();
      } else if (item.type === 'fetch') {
        item.resolve(new Response(typeof value === 'string' ? value : JSON.stringify(value), {status}));
      } else if (status >= 400) item.reject(new Error(String(value)));
      else item.resolve(value);
    };
    window.fetch = (url,options = {}) => {
      if (String(url).startsWith('/lab/api/jobs')) return Promise.resolve(new Response(JSON.stringify(window.__jobs)));
      if (url === '/health') return Promise.resolve(new Response(JSON.stringify({auth:false,records:1,llm:'configured',embedder:'fixture'})));
      if (url === '/debug/documents') return Promise.resolve(new Response(JSON.stringify({documents:[{file_id:'A',filename:'A.pdf'},{file_id:'B',filename:'B.pdf'}]})));
      return window.__queue(String(url), options, 'fetch');
    };
    window.eshare = {
      storage: {
        get: key => Promise.resolve(key === 'todos' ? [{id:'one',text:'第一条待办',done:false,createdAt:Date.now()}, {id:'two',text:'完成事项',done:true,createdAt:Date.now(),doneAt:Date.now()}] : null),
        set: (key,value) => window.__queue('storage.set', {key,value}),
      },
      clipboard: {
        history: options => window.__queue('history', options),
        stats: () => Promise.resolve({total:20,text:20,image:0,files:0,url:0}),
        settings: patch => Promise.resolve({maxEntries:100,paused:false,autoStartSupported:true,...patch}),
        onChanged: callback => {window.__clipboardChanged = callback;},
        copyText: () => {window.__copies = (window.__copies || 0) + 1;return Promise.resolve();}, copyImage: () => Promise.resolve(), copyFiles: () => Promise.resolve(),
        remove: id => window.__queue('remove', id), clear: () => Promise.resolve(),
      },
      uploadToDrive: payload => window.__queue('upload',payload),
      window: {dismiss() {},onShown(callback) {window.__showPanel = callback;}},
    };
    window.XMLHttpRequest = function () {
      this.open = (_,url) => {this.url = url;};
      this.setRequestHeader = () => {};
      this.send = body => {
        if (this.url === '/health') {
          this.readyState = 4; this.status = 200; this.responseText = '{"auth":true}';
          queueMicrotask(() => this.onreadystatechange());
        } else window.__requests.push({name:this.url,payload:body,type:'xhr',xhr:this});
      };
      this.abort = () => {this.readyState = 4;this.status = 0;this.responseText = '';this.onreadystatechange?.();};
    };
  });
  await page.goto('http://static-ui.test/' + file);
  return page;
}
const frame = (page,count = 32) => page.evaluate(count => new Promise(resolve => {
  const tick = () => --count ? requestAnimationFrame(tick) : resolve(); requestAnimationFrame(tick);
}),count);
const respond = (page,name,value,status = 200) => page.evaluate(([name,value,status]) => window.__respond(name,value,status),[name,value,status]);
const pending = (page,name,count = 1) => page.waitForFunction(([name,count]) => window.__requests.filter(item=>item.name === name).length >= count,[name,count]);
const record = name => {checks.push(name);console.log('PASS ' + name);};
try {
  const todo = await open('plugins/todo/index.html');
  await todo.locator('.row').first().waitFor();
  assert.equal(await todo.locator('#reportPanel').isVisible(),false);
  assert.equal(await todo.locator('#empty').isVisible(),false);
  await todo.locator('.row input').first().check();
  await pending(todo,'storage.set');
  assert.equal(await todo.locator('#list').getAttribute('aria-busy'),'true');
  await respond(todo,'storage.set','模拟磁盘错误',500);
  await todo.waitForFunction(() => document.querySelector('#list').getAttribute('aria-busy') === 'false');
  assert.equal(await todo.locator('.row input').first().isChecked(),false);
  assert.match(await todo.locator('#saveStatus').textContent(),/保存失败/);
  record('待办完成保存失败恢复/空态互斥');
  await todo.locator('#weekly').click();
  await todo.keyboard.press('Shift+Tab');
  assert.equal(await todo.evaluate(()=>document.activeElement.id),'saveReport');
  await todo.evaluate(() => { document.querySelector('#closeReport').click(); document.querySelector('#weekly').click(); });
  await todo.waitForFunction(()=>getComputedStyle(document.querySelector('#reportPanel')).opacity === '1');
  assert.equal(await todo.locator('#reportPanel').evaluate(el=>getComputedStyle(el).opacity),'1');
  await todo.screenshot({path:path.join(output,'todo-report.png')});
  await todo.locator('#closeReport').click();
  assert.equal(await todo.locator('#reportPanel').getAttribute('aria-hidden'),'true');
  assert.equal(await todo.locator('#reportPanel').getAttribute('inert'),'');
  await todo.locator('#reportPanel').waitFor({state:'hidden'});
  await todo.emulateMedia({reducedMotion:'reduce'});
  await todo.locator('#weekly').click();
  await todo.locator('#closeReport').click();
  assert.equal(await todo.locator('#reportPanel').isVisible(),false);
  record('周报焦点循环/关闭即隔离/快速反向/减少动态效果');
  await todo.locator('#newTodo').fill('新增任务');
  await todo.locator('#newTodo').press('Enter');
  await respond(todo,'storage.set',null);
  await todo.waitForFunction(()=>document.querySelector('#newTodo').value === '');
  assert.equal(await todo.locator('.row').count(),3);
  assert.equal(await todo.evaluate(()=>document.activeElement.id),'newTodo');
  record('待办新增/保存成功最终态');
  await todo.locator('#weekly').click();
  await todo.locator('#saveReport').click();
  await todo.locator('#closeReport').click();
  await todo.locator('#weekly').click();
  assert.equal(await todo.locator('#saveReport').isEnabled(),false);
  await respond(todo,'upload','模拟上传错误',500);
  await todo.waitForFunction(()=>!document.querySelector('#saveReport').disabled);
  assert.match(await todo.locator('#reportHint').textContent(),/模拟上传错误/);
  record('周报上传中可关闭重开/失败恢复按钮');
  await todo.close();

  const template = await open('plugins/template/index.html','reduce');
  await template.waitForFunction(()=>!document.querySelector('#save').disabled);
  await template.locator('#save').click();
  assert.equal(await template.locator('main').getAttribute('data-state'),'loading');
  await respond(template,'storage.set','写入失败',500);
  await template.waitForFunction(()=>document.querySelector('main').dataset.state === 'error');
  assert.equal(await template.locator('#save').isEnabled(),true);
  assert.equal(await template.locator('#hint').evaluate(el=>getComputedStyle(el).opacity),'1');
  record('模板读写忙碌/错误恢复/无动效可见');
  await template.close();

  const clip = await open('plugins/clipboard/index.html');
  await pending(clip,'history');
  await clip.locator('[data-navkind="image"]').click();
  await pending(clip,'history',2);
  // 第二个请求先完成，再让旧请求返回。
  await clip.evaluate(() => {const a=window.__requests.shift();window.__requests.push(a);});
  await respond(clip,'history',[{id:'img',kind:'image',file:'x.png',createdAt:Date.now()}]);
  await respond(clip,'history',[{id:'old',kind:'text',text:'过期内容',createdAt:Date.now()}]);
  assert.equal(await clip.locator('.card').count(),1);
  assert.equal(await clip.locator('.card').first().getAttribute('data-id'),'img');
  await clip.waitForFunction(()=>getComputedStyle(document.querySelector('#quotaFill')).transform === 'matrix(0.2, 0, 0, 1, 0, 0)');
  assert.equal(await clip.locator('#quotaFill').evaluate(el=>getComputedStyle(el).transform),'matrix(0.2, 0, 0, 1, 0, 0)');
  await clip.locator('#navAll').click();
  await respond(clip,'history','读取失败',500);
  await clip.waitForFunction(()=>document.querySelector('#flow').getAttribute('aria-busy') === 'false');
  assert.equal(await clip.locator('#flow').getAttribute('inert'),null);
  assert.match(await clip.locator('#emptyTitle').textContent(),/无法读取/);
  record('剪切板分类响应乱序/容量 transform/错误解除忙碌');
  await clip.close();

  const panel = await open('plugins/clipboard/index.html?panel=1','reduce');
  await pending(panel,'history');
  await respond(panel,'history',[{id:'p',kind:'text',text:'面板记录',createdAt:Date.now()}]);
  await panel.locator('[data-chipkind="image"]').click();
  await panel.locator('#panelSearch').focus();
  await panel.keyboard.press('Enter');
  assert.equal(await panel.evaluate(()=>window.__copies || 0),0);
  await respond(panel,'history',[]);
  await panel.waitForFunction(()=>document.querySelector('#panelList').getAttribute('aria-busy') === 'false');
  assert.equal(await panel.locator('#panelEmpty').isVisible(),true);
  await panel.evaluate(()=>window.__showPanel());
  await respond(panel,'history',[{id:'p2',kind:'text',text:'最新面板记录',createdAt:Date.now()}]);
  await panel.waitForFunction(()=>document.querySelector('#panelList').getAttribute('aria-busy') === 'false');
  await panel.locator('[data-chipkind="image"]').focus();
  await panel.keyboard.press('Enter');
  assert.equal(await panel.evaluate(()=>window.__copies || 0),0);
  await respond(panel,'history',[{id:'p3',kind:'text',text:'键盘选择记录',createdAt:Date.now()}]);
  await panel.waitForFunction(()=>document.querySelector('#panelList').getAttribute('aria-busy') === 'false');
  await panel.locator('#panelSearch').focus();
  await panel.keyboard.press('ArrowDown');
  await panel.keyboard.press('Enter');
  assert.equal(await panel.evaluate(()=>window.__copies),1);
  record('剪切板速取面板加载期间阻止复制旧内容/重开后键盘选择');
  await panel.close();

  const lab = await open('knowledge/app/lab/index.html');
  await pending(lab,'/documents/doc1/versions/v1/artifacts/clean.md');
  await respond(lab,'/documents/doc1/versions/v1/artifacts/clean.md','初始文档');
  await respond(lab,'/documents/doc1/versions/v1/artifacts/manifest.json',{blocks:2,chunks:3});
  await lab.waitForFunction(()=>document.querySelector('#refresh-button').disabled === false);
  assert.equal(await lab.locator('#login-bar').isVisible(),false);
  assert.equal(await lab.locator('#ask-result').isVisible(),false);
  await lab.locator('.job-item').focus();
  await lab.evaluate(async () => {window.__jobNode=document.querySelector('.job-item');await refreshJobs();});
  assert.equal(await lab.evaluate(()=>window.__jobNode === document.querySelector('.job-item') && document.activeElement === window.__jobNode),true);
  await lab.evaluate(() => {loadArtifact('document.json');loadArtifact('clean.md');});
  await respond(lab,'/documents/doc1/versions/v1/artifacts/clean.md','最新文档');
  await respond(lab,'/documents/doc1/versions/v1/artifacts/document.json','{"old":true}');
  assert.equal(await lab.locator('#artifact-viewer code').textContent(),'最新文档');
  await lab.evaluate(() => {
    const zone=document.querySelector('#drop-zone'),child=document.querySelector('#drop-title');
    zone.dispatchEvent(new Event('dragenter',{bubbles:true,cancelable:true}));
    child.dispatchEvent(new Event('dragenter',{bubbles:true,cancelable:true}));
    child.dispatchEvent(new Event('dragleave',{bubbles:true,cancelable:true}));
  });
  assert.equal(await lab.locator('#drop-zone').evaluate(el=>el.classList.contains('is-dragging')),true);
  assert.equal(await lab.locator('#drop-zone').evaluate(el=>getComputedStyle(el).transform),'none');
  await lab.locator('#drop-zone').dispatchEvent('dragleave');
  assert.equal(await lab.locator('#drop-zone').evaluate(el=>el.classList.contains('is-dragging')),false);
  await lab.locator('#ask-input').fill('测试问题');
  await lab.locator('#ask-button').click();
  await respond(lab,'/query',{answer:'测试答案',contexts:[]});
  await lab.locator('#ask-result').waitFor();
  await lab.emulateMedia({reducedMotion:'reduce'});
  await frame(lab,2);
  assert.equal(await lab.locator('#ask-result').evaluate(el=>el.getAnimations().length),0);
  assert.equal(await lab.locator('#ask-result').evaluate(el=>getComputedStyle(el).opacity),'1');
  record('实验室轮询保焦点/产物乱序/拖入子元素不闪/回答/动态减少偏好');
  await lab.screenshot({path:path.join(output,'lab.png'),fullPage:true});
  await lab.close();

  const cockpit = await open('knowledge/app/debug/cockpit.html');
  await cockpit.locator('#doc-select option[value="B"]').waitFor({state:'attached'});
  await cockpit.locator('#doc-select').selectOption('A');
  await cockpit.locator('#doc-select').selectOption('B');
  const data = {clean_markdown:'B 最终内容',chunks:[],document:{blocks:[]},cleaning_actions:[],stats:{},manifest:null};
  await respond(cockpit,'/debug/document/B',data);
  await respond(cockpit,'/debug/document/A',{...data,clean_markdown:'A 旧内容'});
  assert.equal(await cockpit.locator('#clean-md').textContent(),'B 最终内容');
  await cockpit.locator('[data-tab="retrieve"]').click();
  await cockpit.locator('#query-input').fill('问题');
  await cockpit.locator('#query-input').press('Enter');
  await cockpit.locator('#query-input').press('Enter');
  assert.equal(await cockpit.evaluate(()=>window.__requests.filter(item=>item.name === '/debug/query').length),1);
  await respond(cockpit,'/debug/query',{detail:'服务器错误'},500);
  await cockpit.waitForFunction(()=>!document.querySelector('#btn-search').disabled);
  assert.match(await cockpit.locator('#retrieve-empty').textContent(),/检索失败/);
  await cockpit.locator('[data-tab="generate"]').click();
  await cockpit.locator('#gen-input').fill('生成问题');
  await cockpit.locator('#btn-generate').click();
  assert.match(await cockpit.locator('#gen-empty').textContent(),/正在生成/);
  await respond(cockpit,'/debug/generate',{answer:'完成回答',contexts:[],faithfulness:[],llm_configured:true});
  await cockpit.locator('#gen-content').waitFor();
  assert.equal(await cockpit.locator('#btn-generate').isEnabled(),true);
  await cockpit.locator('[data-tab="health"]').click();
  await cockpit.locator('#btn-health').click();
  await respond(cockpit,'/debug/health',{},503);
  await cockpit.waitForFunction(()=>!document.querySelector('#btn-health').disabled);
  assert.match(await cockpit.locator('#health-content').textContent(),/加载失败/);
  await cockpit.emulateMedia({reducedMotion:'reduce'});
  await cockpit.locator('[data-tab="inspect"]').click();
  assert.equal(await cockpit.locator('#tab-inspect').evaluate(el=>getComputedStyle(el).animationName),'none');
  record('驾驶舱文档乱序/检索防重入/生成成功/健康错误/减少动态效果 tab');
  await cockpit.close();

  const wps = await open('knowledge/app/wps_addon/taskpane.html');
  await wps.locator('#username').fill('tester');
  await wps.locator('#password').fill('fixture');
  await wps.locator('#loginBtn').click();
  await respond(wps,'/auth/login',{token:'fixture',username:'测试用户'});
  await wps.locator('#question').fill('问题');
  await wps.locator('#askBtn').click();
  assert.equal(await wps.locator('.spinner').evaluate(el=>getComputedStyle(el).animationName),'spin');
  await respond(wps,'/query',{answer:'可见回答',contexts:[{filename:'引用.pdf',text:'来源'}]});
  assert.equal(await wps.locator('#answerText').textContent(),'可见回答');
  await wps.locator('#sourcesSummary').click();
  assert.equal(await wps.locator('#sourcesBox').getAttribute('open'),'');
  await wps.emulateMedia({reducedMotion:'reduce'});
  await wps.locator('#askBtn').click();
  assert.equal(await wps.locator('.spinner').evaluate(el=>getComputedStyle(el).animationName),'none');
  assert.match(await wps.locator('#answerText').textContent(),/正在检索/);
  await respond(wps,'/query',{detail:'服务错误'},500);
  assert.equal(await wps.locator('#askBtn').isEnabled(),true);
  assert.match(await wps.locator('#askError').textContent(),/服务错误/);
  await wps.locator('#askBtn').click();
  await wps.locator('#logoutBtn').click();
  await respond(wps,'/auth/logout',{logged_out:true});
  await respond(wps,'/query',{answer:'迟到的回答'});
  assert.equal(await wps.locator('#mainView').isVisible(),false);
  assert.equal(await wps.locator('#answerText').textContent(),'');
  record('WPS 登录/加载/完成/来源展开/静态加载提示/错误恢复/退出中止旧回答');
  await wps.screenshot({path:path.join(output,'wps-logged-out.png')});
  await wps.close();
  assert.deepEqual(errors,[], '页面不得产生未捕获异常');
  console.log(JSON.stringify({browser:await browser.version(),checks:checks.length,errors,output},null,2));
} finally {
  await browser.close();
}
