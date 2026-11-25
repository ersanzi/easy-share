//go:build windows

package main

// 宠物面板复用既有 WebView2：顶部当前用户个人容量、原拖放中心、任务 footer。
// Token 镜像 frontend/src/style.css 的语义层；动画只由 Win32 宿主处理，不新增框架。
const (
	hoverPopupWidth        = 400
	hoverPopupHeightHover  = 360 // 悬停与固定共用放大后的完整面板。
	hoverPopupHeightPinned = 360
)

const hoverPopupHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>EasyShare 空间与上传</title>
<style>
  :root {
    --kd-blue-1: #edf4ff;
    --kd-blue-5: #458bfa;
    --kd-blue-6: #1f69e0;
    --kd-blue-7: #1e5fc7;
    --kd-text-2: #262e3e;
    --kd-text-3: #444e63;
    --blue: var(--kd-blue-6);
    --blue-rgb: 31, 105, 224;
    --blue-hover: var(--kd-blue-5);
    --blue-active: var(--kd-blue-7);
    --blue-light: var(--kd-blue-1);
    --text: var(--kd-text-2);
    --muted: var(--kd-text-3);
    --green-text: #1f7a3d;
    --green-tint: #ecf9ef;
    --red-text: #a7241e;
    --red-tint: #fff0ef;
    --line-rgb: 38, 46, 62;
    --fill-rgb: 38, 46, 62;
    --border: rgba(var(--line-rgb), .12);
    --soft-bg: rgba(var(--fill-rgb), .08);
    --bg: #ffffff;
    --bg-chrome: #f8f9fb;
    --r-md: 6px;
    --r-lg: 8px;
    --r-xl: 12px;
  }
  * { box-sizing: border-box; }
  html, body { height: 100%; margin: 0; user-select: none; -webkit-user-select: none; }
  body {
    display: flex; flex-direction: column; overflow: hidden;
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", sans-serif;
    font-size: 14px; color: var(--text); background: var(--bg);
    border: 1px solid var(--border); border-radius: var(--r-xl);
  }
  button { font: inherit; cursor: pointer; }
  button:disabled { cursor: default; opacity: .55; }
  button:focus-visible { outline: 2px solid var(--blue); outline-offset: 1px; }
  svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }
  [hidden] { display: none !important; }
  .bar { display: flex; align-items: center; gap: 8px; min-height: 36px; padding: 6px 12px; background: var(--bg-chrome); border-bottom: 1px solid var(--border); }
  .account { flex: 1; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pin-state { font-size: 13px; color: var(--muted); }
  .icon-btn { display: grid; place-items: center; width: 32px; height: 32px; border: 0; border-radius: var(--r-md); background: transparent; color: var(--muted); }
  .icon-btn:hover { background: var(--soft-bg); color: var(--text); }
  .body { flex: 1; min-height: 0; padding: 12px; overflow-y: auto; scrollbar-width: thin; }
  .capacity { padding: 10px 12px; background: var(--bg-chrome); border-bottom: 1px solid var(--border); }
  .quota-row { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
  .quota { color: var(--text); font-size: 13px; line-height: 1.5; font-variant-numeric: tabular-nums; }
  .capacity-hint { margin-top: 5px; font-size: 13px; color: var(--muted); line-height: 1.5; }
  .capacity-track { height: 5px; margin-top: 7px; overflow: hidden; border-radius: var(--r-md); background: var(--soft-bg); }
  .capacity-fill { display: block; width: 100%; height: 100%; background: var(--blue); transform: scaleX(0); transform-origin: left; transition: transform 180ms cubic-bezier(.2,0,0,1); }
  .capacity[data-state="overquota"] .capacity-fill { background: var(--red-text); }
  .capacity[data-stale="true"] .capacity-fill { opacity: .6; }
  .capacity[data-state="overquota"] .capacity-hint { color: var(--red-text); }
  .open-btn { display: inline-flex; align-items: center; gap: 4px; padding: 4px 8px; white-space: nowrap; border: 1px solid var(--border); border-radius: var(--r-md); background: var(--bg); color: var(--text); }
  .open-btn:hover:not(:disabled) { color: var(--blue); background: var(--blue-light); }
  .dropzone { display: flex; flex-direction: column; align-items: center; justify-content: center; gap: 6px; min-height: 104px; height: 100%; padding: 12px; text-align: center; border: 1px dashed rgba(var(--blue-rgb), .4); border-radius: var(--r-lg); background: var(--blue-light); }
  .dropzone svg { width: 22px; height: 22px; color: var(--blue); }
  .dropzone b { font-weight: 600; font-size: 18px; }
  .bar, .capacity, .footer { flex-shrink: 0; }
  .open-btn, .task-btn { min-height: 32px; }
  .dropzone span { font-size: 13px; color: var(--muted); overflow-wrap: anywhere; line-height: 1.5; }
  .dropzone.over, .dropzone.busy { border-style: solid; border-color: var(--blue); }
  .dropzone.ok { background: var(--green-tint); border-color: var(--green-text); }
  .dropzone.ok svg { color: var(--green-text); }
  .dropzone.err { background: var(--red-tint); border-color: var(--red-text); }
  .dropzone.err svg { color: var(--red-text); }
  .footer { display: flex; align-items: center; justify-content: space-between; gap: 8px; padding: 9px 12px; border-top: 1px solid var(--border); background: var(--bg-chrome); }
  .task-summary { flex: 1; min-width: 0; color: var(--muted); font-size: 13px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .task-btn { border: 0; background: transparent; color: var(--blue); padding: 2px; white-space: nowrap; }
  button, .dropzone { transition: background-color 120ms cubic-bezier(.2,0,0,1), color 120ms cubic-bezier(.2,0,0,1), border-color 120ms cubic-bezier(.2,0,0,1); }
  .body { overscroll-behavior: contain; }
  @media (prefers-reduced-motion: reduce) { .capacity-fill { transition: none; } }
</style>
</head>
<body>
  <div class="bar">
    <span class="account" id="account">EasyShare · 未登录</span>
    <span class="pin-state" id="pinState">双击宠物固定</span>
    <button class="icon-btn" id="settings" type="button" aria-label="打开主窗口设置" title="打开主窗口设置"><svg viewBox="0 0 24 24"><path d="M12 8v8M8 12h8"/><circle cx="12" cy="12" r="9"/></svg></button>
  </div>
  <section class="capacity" id="capacity" aria-label="当前账号个人云盘容量">
    <div class="quota-row"><span class="quota" id="quota">请先登录客户端</span><button class="open-btn" id="openSpace" type="button" disabled><svg viewBox="0 0 24 24"><path d="M3 7h6l2 2h10v11H3Z"/></svg>打开网盘</button></div>
    <div class="capacity-track" id="capacityTrack" role="progressbar" aria-label="个人容量使用比例" aria-valuemin="0" aria-valuemax="100" hidden><span class="capacity-fill" id="capacityFill"></span></div>
    <div class="capacity-hint" id="capacityHint">共享云盘暂不支持此入口</div>
  </section>
  <main class="body">
    <div class="dropzone" id="dropzone" role="region" aria-label="文件上传到个人云盘">
      <svg viewBox="0 0 24 24"><path d="M12 15V3m0 0-4 4m4-4 4 4M4 16v5h16v-5"/></svg>
      <b id="dropTitle">拖文件到这里</b><span id="dropHint">上传到当前账号的个人云盘</span>
    </div>
  </main>
  <div class="footer"><span class="task-summary" id="taskSummary">正在读取任务状态</span><button class="task-btn" id="tasks" type="button">任务中心 →</button></div>
<script>
  function send(msg) { if (window.chrome && window.chrome.webview) { window.chrome.webview.postMessage(msg); } }
  document.getElementById('settings').addEventListener('click', function () { send('open-settings'); });
  document.getElementById('tasks').addEventListener('click', function () { send('open-tasks'); });
  document.getElementById('openSpace').addEventListener('click', function () { send('open-space'); });
  // 面板任何按钮、空白、滚动条和上传区都不发送 begin-drag；窗口移动只属于原生宠物。
  document.documentElement.addEventListener('mouseenter', function () { send('pointer-enter'); });
  document.documentElement.addEventListener('mouseleave', function () { send('pointer-leave'); });

  var zone = document.getElementById('dropzone');
  var externalDrag = false;
  function setExternalDrag(active) {
    if (externalDrag !== active) { externalDrag = active; send(active ? 'external-drag-enter' : 'external-drag-leave'); }
    zone.classList.toggle('over', active);
  }
  function allowDrop(e) { e.preventDefault(); e.stopPropagation(); }
  document.addEventListener('dragover', function (e) { allowDrop(e); setExternalDrag(true); });
  document.addEventListener('dragenter', function (e) { allowDrop(e); setExternalDrag(true); });
  document.addEventListener('dragleave', function (e) {
    allowDrop(e);
    if (e.relatedTarget === null) { setExternalDrag(false); }
  });
  document.addEventListener('dragend', function () { setExternalDrag(false); });
  document.addEventListener('drop', function (e) {
    allowDrop(e); setExternalDrag(false);
    var files = e.dataTransfer && e.dataTransfer.files;
    if (!files || files.length === 0) { return; }
    if (!uploadEnabled) {
      window.applyDropStatus('暂不能上传', document.getElementById('capacityHint').textContent, 'err'); return;
    }
    if (!window.chrome || !window.chrome.webview || !window.chrome.webview.postMessageWithAdditionalObjects) {
      window.applyDropStatus('无法上传', '当前 WebView2 运行时不支持拖放取路径', 'err'); return;
    }
    window.applyDropStatus('正在读取…', files.length + ' 项', 'busy');
    // 保留原附加 File 对象通道，真实路径仅由原生 GetAdditionalObjects/GetPath 提取。
    window.chrome.webview.postMessageWithAdditionalObjects('file:drop', files);
  });

  window.applyPinned = function (pinned) {
    document.getElementById('pinState').textContent = pinned ? '已固定 · 双击解除' : '双击宠物固定';
  };
  var uploadEnabled = false;
  var accountVersion = '';
  var capacityVersion = 0;
  var dropStatusActive = false;
  window.applyCapacity = function (view) {
    view = view || {};
    capacityVersion = Number(view.accountVersion) || 0;
    var version = String(capacityVersion) + ':' + (view.userId || '');
    if (version !== accountVersion) {
      accountVersion = version; dropStatusActive = false;
      document.getElementById('dropTitle').textContent = '拖文件到这里';
      document.getElementById('dropHint').textContent = '上传到当前账号的个人云盘';
      zone.classList.remove('busy', 'ok', 'err');
    }
    var account = document.getElementById('account');
    account.textContent = view.loggedIn ? (view.name || '当前账号') + ' · 个人云盘' : 'EasyShare · 未登录';
    account.title = view.name || '';
    var capacity = document.getElementById('capacity');
    capacity.setAttribute('data-state', view.state || 'loading');
    capacity.setAttribute('data-stale', view.stale ? 'true' : 'false');
    document.getElementById('quota').textContent = view.usageText || '正在读取个人容量…';
    document.getElementById('capacityHint').textContent = view.hint || '共享云盘暂不支持此入口';
    var track = document.getElementById('capacityTrack');
    var percent = Math.max(0, Math.min(100, Number(view.percent) || 0));
    track.hidden = !view.barVisible || !view.known;
    track.setAttribute('aria-valuenow', String(percent));
    track.setAttribute('aria-valuetext', view.usageText || '');
    document.getElementById('capacityFill').style.transform = 'scaleX(' + percent / 100 + ')';
    document.getElementById('openSpace').disabled = !view.openEnabled;
    uploadEnabled = !!view.uploadEnabled;
    zone.setAttribute('aria-disabled', uploadEnabled ? 'false' : 'true');
    if (!dropStatusActive) {
      document.getElementById('dropHint').textContent = uploadEnabled ? '上传到当前账号的个人云盘' : (view.hint || '请等待个人云盘连接完成');
    }
  };
  window.applyDropStatus = function (title, hint, kind, version) {
    if (version !== undefined && Number(version) !== capacityVersion) { return; }
    dropStatusActive = !!title;
    document.getElementById('dropTitle').textContent = title || '拖文件到这里';
    document.getElementById('dropHint').textContent = hint || '上传到当前账号的个人云盘';
    zone.classList.remove('busy', 'ok', 'err');
    if (kind === 'busy' || kind === 'ok' || kind === 'err') { zone.classList.add(kind); }
  };
  window.applyTasks = function (summary) {
    var el = document.getElementById('taskSummary');
    el.textContent = summary.label || '任务状态不可用';
    el.title = summary.detail || '';
  };

  // 不设置多空间选择器：本面板始终指向个人服务器入口，共享明确不支持。
  // 页面初始化完成后回放最新快照；隐藏期间的任务/上传状态不会丢失。
  send('page-ready');
</script>
</body>
</html>`
