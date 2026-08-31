//go:build windows

package main

// 浮窗页面。内容通过 WebView2 的 NavigateToString 直接加载，因此不接入 Vite
// 构建，frontend/dist 仍保持单入口产物。
//
// 视觉 token（配色、字体族、圆角）取自 frontend/src/style.css，图标沿用
// frontend/src/App.vue 的线性描边 24x24 风格，保证与主界面观感一致。
//
// 布局用 flex 自适应：窗口高度由 Go 侧按状态（悬停/固定）控制，页面 body 用
// height:100% 填满，因此尺寸只在 Go 侧定义一处，CSS 不重复写死高度。
//
// 与 Go 的通信：
//   点设置图标  -> postMessage("open-main")   等效双击 easyshare.exe
//   点固定图标  -> postMessage("pin-toggle")  切换固定态
//   指针进出    -> postMessage("pointer-enter"/"pointer-leave")  配合收起判定
// Go 侧通过 Eval("applyPinned(bool)") 回写固定态，驱动按钮高亮与拖放区显隐。

// 浮窗逻辑尺寸（96 DPI 下的像素），实际创建时按显示器 DPI 缩放。
// 宽度固定；高度分两档：悬停态紧凑，固定态加高以容纳拖放区。
const (
	hoverPopupWidth        = 320
	hoverPopupHeightHover  = 184
	hoverPopupHeightPinned = 360
)

const hoverPopupHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>私人云盘</title>
<style>
  :root {
    --blue: #007aff;
    --blue-dark: #0067d8;
    --border: rgba(60, 60, 67, 0.12);
    --muted: #6e6e73;
    --soft-bg: rgba(118, 118, 128, 0.08);
    --text: #1d1d1f;
    --surface: #ffffff;
    --drop-bg: rgba(0, 122, 255, 0.05);
  }

  * { box-sizing: border-box; }

  html, body {
    margin: 0; padding: 0; height: 100%;
    overflow: hidden;
    user-select: none; -webkit-user-select: none;
  }

  body {
    font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Segoe UI", sans-serif;
    color: var(--text);
    background: var(--surface);
    border: 1px solid var(--border);
    border-radius: 10px;
    display: flex;
    flex-direction: column;
  }

  /* ── 标题栏 ── */
  .bar {
    flex: none;
    display: flex;
    align-items: center;
    height: 56px;
    padding: 0 10px;
    gap: 10px;
    border-bottom: 1px solid var(--border);
  }

  .brand { display: flex; align-items: center; gap: 10px; min-width: 0; }

  .brand-icon {
    width: 26px; height: 26px; flex: none;
    display: grid; place-items: center;
    border-radius: 7px;
    background: linear-gradient(140deg, #4c9bff, var(--blue));
    color: #fff;
  }
  .brand-icon svg { width: 16px; height: 16px; fill: none; stroke: currentColor; stroke-width: 1.9; stroke-linecap: round; stroke-linejoin: round; }

  .brand-name { font-size: 14px; font-weight: 600; letter-spacing: 0.3px; white-space: nowrap; }

  /* 标题栏空白处：作为后续「拖动窗口」的抓取区预留（切片 2 暂不接拖动） */
  .spacer { flex: 1 1 auto; align-self: stretch; }

  .actions { display: flex; align-items: center; gap: 6px; flex: none; }

  /* 头像目前是占位：不请求任何用户数据 */
  .avatar {
    width: 26px; height: 26px; border-radius: 50%;
    display: grid; place-items: center;
    background: linear-gradient(140deg, #9f8bff, #7c6cf2);
    color: #fff; font-size: 12px; font-weight: 600;
  }

  .icon-btn {
    width: 28px; height: 28px; border: none; border-radius: 8px;
    background: transparent; color: var(--muted);
    display: grid; place-items: center; cursor: pointer;
    transition: background 0.12s ease, color 0.12s ease;
  }
  .icon-btn:hover { background: var(--soft-bg); color: var(--text); }
  .icon-btn:active { background: rgba(118, 118, 128, 0.16); }
  .icon-btn:focus-visible { outline: 2px solid var(--blue); outline-offset: 1px; }
  .icon-btn svg { width: 17px; height: 17px; fill: none; stroke: currentColor; stroke-width: 1.7; stroke-linecap: round; stroke-linejoin: round; }

  /* 固定按钮激活态：高亮表示已固定 */
  body.pinned .pin-btn { background: rgba(0, 122, 255, 0.14); color: var(--blue); }

  /* 拖动把手：只在固定态有意义——未固定时窗口本就随托盘图标定位 */
  .drag-btn { display: none; cursor: grab; }
  .drag-btn svg { fill: currentColor; stroke: none; }
  body.pinned .drag-btn { display: grid; }
  .drag-btn:active { cursor: grabbing; background: var(--soft-bg); }

  /* ── 主体 ── */
  .body {
    flex: 1 1 auto;
    display: flex;
    flex-direction: column;
    padding: 12px;
    min-height: 0;
  }

  /* 空间切换：拖入的文件进哪个空间。仅固定态且有可用空间时显示 */
  .switcher { display: none; flex: 0 0 auto; margin-bottom: 10px; }
  body.pinned .switcher.ready { display: block; }

  /* 无可用空间的说明：只在固定态显示，且与切换器互斥 */
  .space-empty { display: none; flex: 0 0 auto; align-items: center; gap: 7px; margin-bottom: 10px; padding: 8px 10px; border: 1px solid var(--border); border-radius: 8px; background: var(--soft-bg); }
  body.pinned .space-empty:not([hidden]) { display: flex; }
  .space-empty svg { width: 15px; height: 15px; flex: 0 0 auto; fill: none; stroke: var(--muted); stroke-width: 1.7; stroke-linecap: round; }
  .space-empty span { font-size: 11px; color: var(--muted); line-height: 1.4; }
  .seg {
    position: relative;
    display: grid;
    grid-auto-flow: column;
    grid-auto-columns: 1fr;
    padding: 2px;
    background: var(--soft-bg);
    border-radius: 8px;
  }
  /* 滑块：靠 transform 平移，切换时有滑动感而不是硬跳 */
  .seg-thumb {
    position: absolute;
    top: 2px;
    left: 2px;
    height: calc(100% - 4px);
    background: var(--surface);
    border-radius: 6px;
    box-shadow: 0 1px 3px rgba(0, 0, 0, 0.12);
    transition: transform 0.2s ease, width 0.2s ease;
    pointer-events: none;
  }
  .seg-btn {
    position: relative;
    z-index: 1;
    padding: 6px 4px;
    border: 0;
    background: transparent;
    color: var(--muted);
    font: inherit;
    font-size: 12px;
    font-weight: 600;
    border-radius: 6px;
    cursor: pointer;
    transition: color 0.2s ease;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .seg-btn[aria-selected="true"] { color: var(--text); }
  .seg-btn:disabled { opacity: 0.45; cursor: default; }
  .seg-btn:focus-visible { outline: 2px solid var(--blue); outline-offset: 1px; }
  /* 配额那行：由 Go 侧写入「已用 x / y」，右侧是打开按钮 */
  .quota-row { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 6px; }
  .quota { font-size: 11px; color: var(--muted); }
  .open-btn {
    display: inline-flex; align-items: center; gap: 4px;
    padding: 3px 9px; border: 1px solid var(--border); border-radius: 6px;
    background: transparent; color: var(--muted);
    font: inherit; font-size: 11px; font-weight: 600; cursor: pointer;
    transition: all .15s ease; white-space: nowrap;
  }
  .open-btn svg { width: 13px; height: 13px; fill: none; stroke: currentColor; stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; }
  .open-btn:hover { color: var(--blue); border-color: rgba(0,122,255,.4); background: rgba(0,122,255,.06); }
  .open-btn:focus-visible { outline: 2px solid var(--blue); outline-offset: 1px; }

  /* 悬停态提示：仅未固定时显示 */
  .hint {
    flex: 1 1 auto;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    text-align: center;
    gap: 8px;
    color: var(--muted);
  }
  .hint svg { width: 26px; height: 26px; fill: none; stroke: currentColor; stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; opacity: 0.8; }
  .hint b { font-size: 13px; font-weight: 600; color: var(--text); }
  .hint span { font-size: 12px; }

  /* 拖放区：仅固定态显示（拖入的真实处理是后续切片） */
  .dropzone {
    flex: 1 1 auto;
    display: none;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 10px;
    border: 1.5px dashed rgba(0, 122, 255, 0.4);
    border-radius: 10px;
    background: var(--drop-bg);
    color: var(--muted);
    text-align: center;
    padding: 12px;
  }
  .dropzone svg { width: 30px; height: 30px; fill: none; stroke: var(--blue); stroke-width: 1.6; stroke-linecap: round; stroke-linejoin: round; }
  /* 上传三态：进行中 / 成功 / 失败。失败必须显眼，静默失败会让人以为传成功了 */
  .dropzone.busy { border-style: solid; border-color: rgba(0,122,255,.45); }
  .dropzone.ok { border-color: rgba(52,199,89,.5); background: rgba(52,199,89,.07); }
  .dropzone.ok svg { stroke: #34c759; }
  .dropzone.err { border-color: rgba(255,59,48,.5); background: rgba(255,59,48,.07); }
  .dropzone.err svg { stroke: #ff3b30; }
  /* 拖到窗口上方时高亮，让人确信这里能放 */
  .dropzone.over { border-style: solid; border-color: var(--blue); background: rgba(0,122,255,.12); }
  .dropzone b { font-size: 13px; font-weight: 600; color: var(--text); }
  .dropzone span { font-size: 12px; }

  body.pinned .hint { display: none; }
  body.pinned .dropzone { display: flex; }

  @media (prefers-color-scheme: dark) {
    :root { --text: #f5f5f7; --border: rgba(255,255,255,0.14); --muted: #9b9ba1; --soft-bg: rgba(255,255,255,0.10); --surface: #1f1f22; --drop-bg: rgba(10,132,255,0.10); }
  }
</style>
</head>
<body>
  <div class="bar">
    <div class="brand">
      <span class="brand-icon" aria-hidden="true">
        <svg viewBox="0 0 32 32"><path d="M8.5 12a6.5 6.5 0 0 1 12.6-2.2A5.5 5.5 0 1 1 22.5 20H8a5 5 0 0 1 .5-8Z"/><path d="m12 15 4-4 4 4M16 11v11"/></svg>
      </span>
      <span class="brand-name">私人云盘</span>
    </div>

    <div class="spacer"></div>

    <div class="actions">
      <span class="avatar" id="avatar" aria-hidden="true">我</span>
      <!--
        拖动把手：仅固定态显示。按下即把拖动交给系统的拖窗循环（见 winui.BeginWindowDrag），
        因此贴边吸附与多显示器都由 Windows 处理，我们不自己算坐标。
      -->
      <button id="drag" class="icon-btn drag-btn" type="button" aria-label="拖动窗口" title="按住拖动到任意位置">
        <svg viewBox="0 0 24 24"><circle cx="9" cy="6" r="1.4"/><circle cx="15" cy="6" r="1.4"/><circle cx="9" cy="12" r="1.4"/><circle cx="15" cy="12" r="1.4"/><circle cx="9" cy="18" r="1.4"/><circle cx="15" cy="18" r="1.4"/></svg>
      </button>
      <button id="pin" class="icon-btn pin-btn" type="button" aria-label="固定窗口" aria-pressed="false" title="固定窗口">
        <svg viewBox="0 0 24 24"><path d="M9 4h6l-1 6 3 3v2H7v-2l3-3-1-6Z"/><path d="M12 18v3"/></svg>
      </button>
      <button id="settings" class="icon-btn" type="button" aria-label="打开主窗口" title="打开主窗口">
        <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1Z"/></svg>
      </button>
    </div>
  </div>

  <div class="body">
    <!--
      空间切换：决定拖入的文件上传到哪个空间。
      只列出该账号真正拥有的空间——没配额的个人空间、没授权的共享空间不会出现，
      免得选了之后才被控制面拒掉。
    -->
    <!--
      无可用空间时的说明。刻意**默认显示**：未登录时 applySpaces 可能从未被调用，
      若默认隐藏，浮窗就是一块什么都不说的空白（分不清是没登录还是坏了）。
      有空间时由 applySpaces 隐藏它。
    -->
    <div class="space-empty" id="spaceEmpty">
      <svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="9"/><path d="M12 8v4M12 16h.01"/></svg>
      <span>请先登录客户端；已登录则说明管理员尚未分配空间</span>
    </div>

    <div class="switcher" id="switcher">
      <div class="seg" id="seg" role="tablist" aria-label="选择上传空间">
        <span class="seg-thumb" id="segThumb"></span>
      </div>
      <div class="quota-row">
        <span class="quota" id="quota"></span>
        <!-- 打开当前选中的空间：滑到哪个就打开哪个，可直接在资源管理器里取放文件 -->
        <button id="openSpace" class="open-btn" type="button" title="在文件管理器中打开这个空间">
          <svg viewBox="0 0 24 24"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V7Z"/></svg>
          打开
        </button>
      </div>
    </div>

    <!-- 未固定：提示如何进入固定态 -->
    <div class="hint">
      <svg viewBox="0 0 24 24"><path d="M9 4h6l-1 6 3 3v2H7v-2l3-3-1-6Z"/><path d="M12 18v3"/></svg>
      <b>点击固定按钮</b>
      <span>固定后窗口常驻，可拖入文件</span>
    </div>

    <!--
      拖放区。真实的拖入由 Win32 的 WM_DROPFILES 接收（HTML5 拿不到完整路径），
      这里只负责显示进度与结果，由 Go 侧回写。
    -->
    <div class="dropzone" id="dropzone">
      <svg viewBox="0 0 24 24"><path d="M12 15V3m0 12-4-4m4 4 4-4"/><path d="M4 17v2a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2v-2"/></svg>
      <b id="dropTitle">拖文件到这里</b>
      <span id="dropHint">上传到当前选中的空间</span>
    </div>
  </div>

<script>
  function send(msg) {
    if (window.chrome && window.chrome.webview) { window.chrome.webview.postMessage(msg); }
  }

  document.getElementById('settings').addEventListener('click', function () { send('open-main'); });
  document.getElementById('pin').addEventListener('click', function () { send('pin-toggle'); });

  // 拖动把手：mousedown 就交给原生，不等 click。
  // 等 click 就晚了——那时鼠标已经松开，系统的拖窗循环需要在按键仍按下时启动。
  document.getElementById('drag').addEventListener('mousedown', function (e) {
    if (e.button !== 0) { return; }
    e.preventDefault();
    send('begin-drag');
  });

  document.getElementById('openSpace').addEventListener('click', function () { send('open-space'); });

  // --- 拖入文件 ---
  //
  // 必须用 postMessageWithAdditionalObjects 把 File 对象交给原生侧：
  // DataTransfer 出于安全只给文件名，拿不到完整路径；而宿主窗口的 WM_DROPFILES
  // 也收不到（WebView2 的子窗口盖住了整个客户区）。只有这条通路能拿到真实路径。
  var zone = document.getElementById('dropzone');

  // dragover 必须阻止默认行为，否则浏览器会把拖放当成"导航到该文件"
  function allowDrop(e) { e.preventDefault(); e.stopPropagation(); }
  document.addEventListener('dragover', allowDrop);
  document.addEventListener('dragenter', function (e) {
    allowDrop(e);
    if (zone) { zone.classList.add('over'); }
  });
  document.addEventListener('dragleave', function (e) {
    allowDrop(e);
    // 只在真正离开窗口时移除高亮：子元素间移动也会触发 dragleave
    if (e.relatedTarget === null && zone) { zone.classList.remove('over'); }
  });

  document.addEventListener('drop', function (e) {
    e.preventDefault();
    e.stopPropagation();
    if (zone) { zone.classList.remove('over'); }

    var files = e.dataTransfer && e.dataTransfer.files;
    if (!files || files.length === 0) { return; }
    if (!window.chrome || !window.chrome.webview || !window.chrome.webview.postMessageWithAdditionalObjects) {
      window.applyDropStatus('无法上传', '当前 WebView2 运行时不支持拖放取路径', 'err');
      return;
    }
    // 先给即时反馈：上传要回控制面，没有这一步用户会以为拖了没反应
    window.applyDropStatus('正在读取…', files.length + ' 项', 'busy');
    window.chrome.webview.postMessageWithAdditionalObjects('file:drop', files);
  });

  document.addEventListener('mouseenter', function () { send('pointer-enter'); });
  document.addEventListener('mouseleave', function () { send('pointer-leave'); });

  // 由 Go 侧回写固定态，驱动按钮高亮与拖放区显隐。
  window.applyPinned = function (pinned) {
    document.body.classList.toggle('pinned', !!pinned);
    var btn = document.getElementById('pin');
    btn.setAttribute('aria-pressed', pinned ? 'true' : 'false');
    btn.setAttribute('title', pinned ? '取消固定' : '固定窗口');
    btn.setAttribute('aria-label', pinned ? '取消固定' : '固定窗口');
  };

  // 启动即固定（B 形态）：Go 侧通过 Init 在本脚本运行前注入 __esStartPinned，
  // 页面加载即套用固定样式，无需等待 Go 的 Eval，避开回调时序问题。
  if (window.__esStartPinned) { window.applyPinned(true); }

  // 由 Go 侧回写登录账号（登录/登出时头像与昵称跟随）。
  window.applyUser = function (avatarChar, name) {
    var a = document.getElementById('avatar');
    if (a) { a.textContent = avatarChar || '我'; a.setAttribute('title', name || ''); }
  };

  // 由 Go 侧回写上传状态。文案分三态：进行中 / 成功 / 失败。
  // 失败必须让用户看见——静默失败会让人以为文件已经传上去了。
  window.applyDropStatus = function (title, hint, kind) {
    var t = document.getElementById('dropTitle');
    var h = document.getElementById('dropHint');
    if (t) { t.textContent = title; }
    if (h) { h.textContent = hint; }
    var zone = document.getElementById('dropzone');
    if (zone) {
      zone.classList.remove('busy', 'ok', 'err');
      if (kind) { zone.classList.add(kind); }
    }
  };

  // --- 空间切换 ---
  var spaces = [];
  var activeKind = '';

  // 把滑块移到选中项上方。宽度按项数均分，与 grid 的 1fr 一致。
  function moveThumb() {
    var thumb = document.getElementById('segThumb');
    var index = -1;
    for (var i = 0; i < spaces.length; i++) { if (spaces[i].kind === activeKind) { index = i; break; } }
    if (!thumb || index < 0 || spaces.length === 0) { if (thumb) { thumb.style.width = '0'; } return; }
    var pct = 100 / spaces.length;
    thumb.style.width = 'calc(' + pct + '% - 2px)';
    thumb.style.transform = 'translateX(calc(' + (index * 100) + '% + ' + (index * 2) + 'px))';
  }

  function renderQuota() {
    var el = document.getElementById('quota');
    if (!el) { return; }
    for (var i = 0; i < spaces.length; i++) {
      if (spaces[i].kind === activeKind) { el.textContent = spaces[i].quota || ''; return; }
    }
    el.textContent = '';
  }

  function selectSpace(kind) {
    if (kind === activeKind) { return; }
    activeKind = kind;
    var btns = document.querySelectorAll('.seg-btn');
    for (var i = 0; i < btns.length; i++) {
      btns[i].setAttribute('aria-selected', btns[i].getAttribute('data-kind') === kind ? 'true' : 'false');
    }
    moveThumb();
    renderQuota();
    // 告诉 Go 侧改了目标空间，后续拖入按这个空间上传
    send('space-select:' + kind);
  }

  // Go 侧传入 [{kind, label, quota, readOnly}]，未登录或无可用空间时传空数组。
  window.applySpaces = function (list, selected) {
    spaces = Array.isArray(list) ? list : [];
    var seg = document.getElementById('seg');
    var switcher = document.getElementById('switcher');
    if (!seg || !switcher) { return; }

    // 只清按钮，留下滑块
    var old = seg.querySelectorAll('.seg-btn');
    for (var i = 0; i < old.length; i++) { seg.removeChild(old[i]); }

    // 只要有空间就显示这一块——它同时承载配额与「打开」按钮，
    // 不能因为「只有一个空间没得切」就把整块藏掉（那会让配额和打开一起消失）。
    switcher.classList.toggle('ready', spaces.length > 0);
    // 没有任何空间：明确说明原因，而不是显示一块空白让人猜是没登录还是坏了
    var empty = document.getElementById('spaceEmpty');
    if (empty) { empty.hidden = spaces.length > 0; }

    for (var j = 0; j < spaces.length; j++) {
      var s = spaces[j];
      var btn = document.createElement('button');
      btn.className = 'seg-btn';
      btn.type = 'button';
      btn.setAttribute('role', 'tab');
      btn.setAttribute('data-kind', s.kind);
      btn.textContent = s.label || s.kind;
      // 只读空间不能作为上传目标：能选中反而误导
      if (s.readOnly) {
        btn.disabled = true;
        btn.title = s.label + '（只读，不能上传）';
      }
      btn.addEventListener('click', (function (kind) {
        return function () { selectSpace(kind); };
      })(s.kind));
      seg.appendChild(btn);
    }

    // 选中项：优先用 Go 指定的，否则第一个可写的
    var want = selected || '';
    var ok = false;
    for (var k = 0; k < spaces.length; k++) {
      if (spaces[k].kind === want && !spaces[k].readOnly) { ok = true; break; }
    }
    if (!ok) {
      want = '';
      for (var m = 0; m < spaces.length; m++) {
        if (!spaces[m].readOnly) { want = spaces[m].kind; break; }
      }
    }
    activeKind = '';
    if (want) { selectSpace(want); } else { moveThumb(); renderQuota(); }
  };
</script>
</body>
</html>`
