// 剪切板插件逻辑（2.0）：同一份代码跑两种形态——
//   完整形态（主窗口插件页）：按天分组卡片 + 侧栏分类 + 收藏 + 搜索 + 统计 + 实时追加；
//   面板形态（?panel=1，Win+V / ⌘⇧V 唤起）：紧凑速取列表 + 键盘导航（↑↓ 选择、Enter 复制、Esc 关闭）。
// 数据全部来自宿主能力 API（clipboard.*），收藏列表存插件私有 KV（storage）。
(function () {
  'use strict';

  if (typeof eshare === 'undefined') {
    // 纯浏览器调试（无宿主）：按完整形态渲染布局，数据区空转。
    document.body.classList.add('mode-full');
    return;
  }

  // ── 形态判定（渲染前设置，防闪错形态）──
  var IS_PANEL = new URLSearchParams(location.search).has('panel');
  document.body.classList.add(IS_PANEL ? 'mode-panel' : 'mode-full');

  var PAGE = 60;          // 完整形态分页大小
  var FETCH_CAP = 20;     // 过滤视图（链接/收藏）最多翻的页数，防极端数据下无限翻

  var state = {
    kind: '',        // '' | text | image | url | files（url 是 text 的客户端再分类）
    favOnly: false,
    query: '',
    entries: [],     // 当前过滤条件下已加载的条目（新在前）
    offset: 0,
    hasMore: false,
    favs: [],        // 收藏条目 id 列表（KV 持久化）
    sel: -1          // 面板键盘选中行下标
  };
  var stats = { total: 0, text: 0, image: 0, files: 0, url: 0 };
  var maxEntries = 1000;
  var favSet = {};     // id → true（查询用）
  var fetchPages = 0;  // 过滤视图已翻页数

  var $ = function (id) { return document.getElementById(id); };

  // ══════════ 通用工具 ══════════

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }

  function isUrlEntry(e) {
    return e.kind === 'text' && /^https?:\/\/\S+$/i.test((e.text || '').trim());
  }

  // 条目在分类导航上的归属：链接单独一类（从文字里分出来），其余按原类型。
  function kindOf(e) { return isUrlEntry(e) ? 'url' : e.kind; }

  function matchesFilter(e) {
    if (state.favOnly) {
      if (!favSet[e.id]) return false;
    } else if (state.kind) {
      var k = kindOf(e);
      if (state.kind === 'url' && k !== 'url') return false;
      if (state.kind === 'text' && e.kind !== 'text') return false; // 文字不含链接
      if (state.kind === 'image' && e.kind !== 'image') return false;
      if (state.kind === 'files' && e.kind !== 'files') return false;
    }
    if (state.query) {
      var hay = ((e.text || '') + ' ' + (e.files || []).join(' ')).toLowerCase();
      if (hay.indexOf(state.query.toLowerCase()) < 0) return false;
    }
    return true;
  }

  // 分页查询的类型下推：链接类在宿主侧就是 text。
  function fetchKind() { return state.kind === 'url' ? 'text' : state.kind; }

  function fmtClock(ms) {
    var d = new Date(ms);
    return ('0' + d.getHours()).slice(-2) + ':' + ('0' + d.getMinutes()).slice(-2);
  }

  function fmtTime(e) {
    var d = new Date(e.createdAt);
    var now = new Date();
    var sameDay = d.toDateString() === now.toDateString();
    if (IS_PANEL) {
      return sameDay ? fmtClock(e.createdAt) : (d.getMonth() + 1) + '/' + d.getDate();
    }
    if (sameDay) return fmtClock(e.createdAt);
    var yest = new Date(now.getTime() - 86400000);
    if (d.toDateString() === yest.toDateString()) return '昨天 ' + fmtClock(e.createdAt);
    return (d.getMonth() + 1) + '/' + d.getDate() + ' ' + fmtClock(e.createdAt);
  }

  function dayLabel(ms) {
    var d = new Date(ms);
    var now = new Date();
    if (d.toDateString() === now.toDateString()) return '今天';
    var yest = new Date(now.getTime() - 86400000);
    if (d.toDateString() === yest.toDateString()) return '昨天';
    if (d.getFullYear() === now.getFullYear()) return (d.getMonth() + 1) + '月' + d.getDate() + '日';
    return d.getFullYear() + '年' + (d.getMonth() + 1) + '月' + d.getDate() + '日';
  }

  function sizeLabel(e) {
    if (e.kind === 'image') return e.width ? e.width + '×' + e.height : '';
    if (e.kind === 'text') {
      var n = (e.text || '').length;
      return n >= 1000 ? Math.round(n / 100) / 10 + 'k 字' : n + ' 字';
    }
    if (e.kind === 'files') return (e.files || []).length + ' 项';
    return '';
  }

  var KIND_META = {
    text: { ico: 'T', label: '文字' },
    image: { ico: '🖼', label: '图片' },
    url: { ico: '🔗', label: '链接' },
    files: { ico: '📁', label: '文件' }
  };

  function kindMeta(e) { return KIND_META[kindOf(e)] || KIND_META.text; }

  var toastEl = $('toast');
  function toast(msg) {
    toastEl.textContent = msg;
    toastEl.hidden = false;
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { toastEl.hidden = true; }, 1600);
  }

  // ══════════ 收藏（插件 KV）══════════

  function loadFavs() {
    return eshare.storage.get('favIds').then(function (v) {
      state.favs = Array.isArray(v) ? v : [];
      favSet = {};
      state.favs.forEach(function (id) { favSet[id] = true; });
    }).catch(function () { state.favs = []; });
  }

  function saveFavs() {
    favSet = {};
    state.favs.forEach(function (id) { favSet[id] = true; });
    return eshare.storage.set('favIds', state.favs).catch(function () {});
  }

  function isFav(e) { return !!favSet[e.id]; }

  function toggleFav(e, btn) {
    var idx = state.favs.indexOf(e.id);
    if (idx >= 0) state.favs.splice(idx, 1); else state.favs.push(e.id);
    saveFavs().then(function () {
      if (btn) btn.classList.toggle('on', isFav(e));
      updateSidebar();
      // 收藏视图里取消收藏：条目应从视图消失
      if (state.favOnly && !isFav(e)) {
        state.entries = state.entries.filter(function (x) { return x.id !== e.id; });
        renderCurrent();
      }
    });
  }

  // ══════════ 动作 ══════════

  function copyEntry(e) {
    if (e.kind === 'text') return eshare.clipboard.copyText(e.text);
    if (e.kind === 'image') return eshare.clipboard.copyImage(e.file);
    if (e.kind === 'files') return eshare.clipboard.copyFiles(e.files);
    return Promise.reject(new Error('未知类型'));
  }

  // 面板形态里，成功的 clipboard.write 由宿主面板运行时解释为「选中」：
  // 收起面板并自动粘贴回之前的焦点窗口（Win+V 语义），插件无需额外调用。
  function pickEntry(e) {
    copyEntry(e).then(function () {
      if (!IS_PANEL) toast('已复制到剪切板');
    }).catch(function (err) {
      toast(err && err.message || '复制失败');
    });
  }

  function deleteEntry(e, el) {
    eshare.clipboard.remove(e.id).then(function () {
      state.entries = state.entries.filter(function (x) { return x.id !== e.id; });
      var fi = state.favs.indexOf(e.id);
      if (fi >= 0) { state.favs.splice(fi, 1); saveFavs(); }
      if (el && el.parentNode) el.parentNode.removeChild(el);
      renderCurrent();
      refreshStats();
    }).catch(function (err) { toast(err && err.message || '删除失败'); });
  }

  // ══════════ 数据加载 ══════════

  // 常规视图：宿主分页直出。过滤视图（链接/收藏）：客户端过滤 + 自动续页。
  function load(append) {
    if (!append) {
      state.offset = 0;
      state.entries = [];
      fetchPages = 0;
    }
    var filtered = state.favOnly || state.kind === 'url';
    if (!filtered) {
      return eshare.clipboard.history({
        limit: PAGE, offset: state.offset, kind: state.kind, query: state.query
      }).then(function (items) {
        items = items || [];
        state.entries = append ? state.entries.concat(items) : items;
        state.offset += items.length;
        state.hasMore = items.length >= PAGE;
        renderCurrent();
      }).catch(function (err) { toast(err && err.message || '加载失败'); });
    }
    return loadFiltered(append);
  }

  function loadFiltered(append) {
    var want = (append ? state.entries.length : 0) + PAGE * 2;
    function step() {
      fetchPages++;
      return eshare.clipboard.history({
        limit: PAGE, offset: state.offset, kind: fetchKind(), query: state.query
      }).then(function (items) {
        items = items || [];
        state.offset += items.length;
        state.hasMore = items.length >= PAGE;
        items.forEach(function (e) {
          if (matchesFilter(e)) state.entries.push(e);
        });
        var exhausted = !state.hasMore || fetchPages >= FETCH_CAP;
        if (state.entries.length < want && !exhausted) return step();
        renderCurrent();
      });
    }
    return step().catch(function (err) { toast(err && err.message || '加载失败'); });
  }

  function loadMore() {
    if (state.hasMore) load(true);
  }

  // ══════════ 统计与侧栏 ══════════

  function refreshStats() {
    eshare.clipboard.stats().then(function (s) {
      if (s) stats = s;
      updateSidebar();
    }).catch(function () {});
    eshare.clipboard.settings().then(function (st) {
      if (st && st.maxEntries) maxEntries = st.maxEntries;
      updateSidebar();
      setStatus(!!(st && st.paused));
      setAutoStart(!!(st && st.autoStart), !!(st && st.autoStartSupported));
    }).catch(function () {});
  }

  // 开机自动记录：OS 自启（HKCU Run）为真相源，开关直读直写宿主；
  // 平台不支持（autoStartSupported=false）时整行隐藏
  function setAutoStart(enabled, supported) {
    var row = $('autoStartRow');
    if (!row) return;
    if (!supported) { row.hidden = true; return; }
    row.hidden = false;
    var sw = $('autoStartSwitch');
    sw.classList.toggle('on', enabled);
    sw.setAttribute('aria-checked', enabled ? 'true' : 'false');
  }

  function toggleAutoStart() {
    var sw = $('autoStartSwitch');
    var next = !sw.classList.contains('on');
    eshare.clipboard.settings({ autoStart: next }).then(function (st) {
      setAutoStart(!!(st && st.autoStart), true);
      toast(next ? '已开启开机自动记录' : '已关闭开机自动记录');
    }).catch(function (err) { toast(err && err.message || '设置失败'); });
  }

  function setStatus(paused) {
    var el = $('status');
    el.classList.toggle('paused', paused);
    el.innerHTML = '<i></i>' + (paused ? '已暂停' : '记录中');
    $('pauseBtn').textContent = paused ? '恢复' : '暂停';
    $('pauseBtn').classList.toggle('on', paused);
  }

  function updateSidebar() {
    $('countAll').textContent = stats.total || '';
    $('countText').textContent = (stats.text - stats.url) || '';
    $('countImage').textContent = stats.image || '';
    $('countUrl').textContent = stats.url || '';
    $('countFiles').textContent = stats.files || '';
    $('countFav').textContent = state.favs.length || '';
    $('quotaUsed').textContent = stats.total || 0;
    $('quotaMax').textContent = maxEntries;
    $('quotaFill').style.width = Math.min(100, (stats.total / maxEntries) * 100) + '%';
  }

  // ══════════ 渲染：完整形态 ══════════

  var flowEl = $('flow'), emptyEl = $('empty'), moreBtn = $('moreBtn');

  function metaButtons(e) {
    return '<button class="icon-btn star' + (isFav(e) ? ' on' : '') + '" data-act="fav" title="收藏">' +
      (isFav(e) ? '💙' : '🤍') + '</button>' +
      '<button class="icon-btn del" data-act="del" title="删除此条">✕</button>';
  }

  function cardBody(e) {
    if (e.kind === 'text') {
      var url = isUrlEntry(e);
      return '<p class="card-text' + (url ? ' is-url' : '') + '">' + esc(e.text) + '</p>';
    }
    if (e.kind === 'image' && e.file) {
      return '<img class="card-img" loading="lazy" src="/clipboard-files/' + encodeURIComponent(e.file) + '" alt="图片">';
    }
    if (e.kind === 'files' && e.files) {
      var rows = e.files.slice(0, 4).map(function (p) {
        return '<div class="path" title="' + esc(p) + '">' + esc(p) + '</div>';
      }).join('');
      if (e.files.length > 4) rows += '<div class="more">… 等 ' + e.files.length + ' 项</div>';
      return '<div class="card-files">' + rows + '</div>';
    }
    return '';
  }

  function renderCard(e) {
    var div = document.createElement('article');
    div.className = 'card';
    div.dataset.id = e.id;
    var m = kindMeta(e);
    div.innerHTML =
      '<div class="card-body">' + cardBody(e) + '</div>' +
      '<div class="card-meta">' +
      '<span>' + m.ico + ' ' + m.label + '</span>' +
      '<span>' + sizeLabel(e) + '</span>' +
      '<span class="src" title="来源：' + esc(e.source || '') + '">' + esc(e.source || '') + '</span>' +
      '<span class="grow"></span>' +
      '<span>' + fmtTime(e) + '</span>' +
      metaButtons(e) +
      '</div>';

    div.addEventListener('click', function (ev) {
      var btn = ev.target.closest('[data-act]');
      if (btn) {
        ev.stopPropagation();
        if (btn.dataset.act === 'fav') toggleFav(e, btn);
        else deleteEntry(e, div);
        return;
      }
      pickEntry(e);
    });
    return div;
  }

  function renderFull() {
    flowEl.innerHTML = '';
    var groups = [];
    var cur = null;
    state.entries.forEach(function (e) {
      var label = dayLabel(e.createdAt);
      if (!cur || cur.label !== label) {
        cur = { label: label, ts: e.createdAt, items: [] };
        groups.push(cur);
      }
      cur.items.push(e);
    });

    groups.forEach(function (g) {
      var sec = document.createElement('section');
      sec.className = 'day';
      sec.innerHTML = '<div class="day-head"><b>' + esc(g.label) + '</b><span>' +
        g.items.length + ' 条内容</span></div>';
      var grid = document.createElement('div');
      grid.className = 'grid';
      g.items.forEach(function (e) { grid.appendChild(renderCard(e)); });
      sec.appendChild(grid);
      flowEl.appendChild(sec);
    });

    var none = state.entries.length === 0;
    emptyEl.hidden = !none;
    if (none) {
      $('emptyTitle').textContent = state.query || state.kind || state.favOnly
        ? '没有匹配的记录' : '还没有记录';
      $('emptyHint').textContent = state.query || state.kind || state.favOnly
        ? '换个关键词或分类试试'
        : '复制的文字、图片与文件会自动出现在这里';
    }
    moreBtn.hidden = !state.hasMore;
  }

  function renderCurrent() {
    if (IS_PANEL) renderPanel(); else renderFull();
  }

  // ══════════ 渲染：面板形态 ══════════

  var panelListEl = $('panelList'), panelEmptyEl = $('panelEmpty');

  function rowMain(e) {
    if (e.kind === 'image' && e.file) {
      return '<span class="row-text">' + esc(e.width ? e.width + '×' + e.height + ' 图片' : '图片') + '</span>';
    }
    if (e.kind === 'files' && e.files) {
      return '<span class="row-text">' + esc(e.files.length + ' 项 · ' + (e.files[e.files.length - 1] || '')) + '</span>';
    }
    var url = isUrlEntry(e);
    return '<span class="row-text' + (url ? ' is-url' : '') + '">' + esc((e.text || '').trim()) + '</span>';
  }

  function renderRow(e, idx) {
    var div = document.createElement('div');
    div.className = 'prow' + (idx === state.sel ? ' sel' : '');
    div.dataset.idx = idx;
    var m = kindMeta(e);
    var ico = e.kind === 'image' && e.file
      ? '<img loading="lazy" src="/clipboard-files/' + encodeURIComponent(e.file) + '" alt="">'
      : m.ico;
    div.innerHTML =
      '<span class="row-ico">' + ico + '</span>' +
      '<span class="row-main">' + rowMain(e) +
      '<span class="row-sub">' + m.label + (e.source ? ' · ' + esc(e.source) : '') + '</span></span>' +
      '<span class="row-time">' + fmtTime(e) + '</span>' +
      '<button class="icon-btn star' + (isFav(e) ? ' on' : '') + '" data-act="fav">' + (isFav(e) ? '💙' : '🤍') + '</button>' +
      '<button class="icon-btn del" data-act="del">✕</button>';

    div.addEventListener('click', function (ev) {
      var btn = ev.target.closest('[data-act]');
      if (btn) {
        ev.stopPropagation();
        if (btn.dataset.act === 'fav') { toggleFav(e, btn); return; }
        deleteEntry(e, div);
        return;
      }
      state.sel = indexOfEntry(e);
      pickEntry(e);
    });
    return div;
  }

  function indexOfEntry(e) {
    for (var i = 0; i < state.entries.length; i++) {
      if (state.entries[i].id === e.id) return i;
    }
    return -1;
  }

  function renderPanel() {
    panelListEl.innerHTML = '';
    state.entries.forEach(function (e, i) {
      panelListEl.appendChild(renderRow(e, i));
    });
    panelEmptyEl.hidden = state.entries.length > 0;
    $('panelEmpty').textContent = state.query || state.kind
      ? '暂无匹配记录' : '暂无记录，去复制点什么吧';
  }

  function moveSel(delta) {
    if (!state.entries.length) return;
    state.sel += delta;
    if (state.sel < 0) state.sel = 0;
    if (state.sel >= state.entries.length) state.sel = state.entries.length - 1;
    var rows = panelListEl.children;
    for (var i = 0; i < rows.length; i++) {
      rows[i].classList.toggle('sel', i === state.sel);
    }
    if (rows[state.sel]) rows[state.sel].scrollIntoView({ block: 'nearest' });
  }

  function copySel() {
    var e = state.entries[state.sel];
    if (e) pickEntry(e);
  }

  // ══════════ 实时新条目 ══════════

  function onRealtime(e) {
    if (!matchesFilter(e)) return;
    state.entries.unshift(e);
    state.offset += 1;
    refreshStats();
    renderCurrent();
  }

  // ══════════ 完整形态初始化 ══════════

  function initFull() {
    var searchEl = $('search'), debounce;
    searchEl.addEventListener('input', function () {
      clearTimeout(debounce);
      debounce = setTimeout(function () {
        state.query = searchEl.value.trim();
        load(false);
      }, 250);
    });

    var navAll = $('navAll'), navFav = $('navFav');
    var kindNavs = Array.prototype.slice.call(document.querySelectorAll('[data-navkind]'));

    function paintNav() {
      navAll.classList.toggle('on', !state.favOnly && !state.kind);
      navFav.classList.toggle('on', state.favOnly);
      kindNavs.forEach(function (n) {
        n.classList.toggle('on', !state.favOnly && n.dataset.navkind === state.kind);
      });
    }

    navAll.addEventListener('click', function () {
      state.favOnly = false; state.kind = ''; paintNav(); load(false);
    });
    navFav.addEventListener('click', function () {
      state.favOnly = true; state.kind = ''; paintNav(); load(false);
    });
    kindNavs.forEach(function (n) {
      n.addEventListener('click', function () {
        state.favOnly = false; state.kind = n.dataset.navkind; paintNav(); load(false);
      });
    });

    moreBtn.addEventListener('click', loadMore);

    $('pauseBtn').addEventListener('click', function () {
      var paused = !$('status').classList.contains('paused');
      eshare.clipboard.settings({ paused: paused }).then(function () {
        setStatus(paused);
        toast(paused ? '已暂停记录' : '已恢复记录');
      }).catch(function (err) { toast(err && err.message || '设置失败'); });
    });

    var asRow = $('autoStartRow');
    if (asRow) {
      $('autoStartSwitch').addEventListener('click', toggleAutoStart);
      asRow.addEventListener('keydown', function (ev) {
        if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); toggleAutoStart(); }
      });
    }

    $('clearBtn').addEventListener('click', function () {
      if (!confirm('确定清空全部剪切板记录？收藏与图片将一并删除。')) return;
      eshare.clipboard.clear().then(function () {
        state.entries = [];
        state.favs = [];
        saveFavs();
        renderCurrent();
        refreshStats();
        toast('已清空');
      }).catch(function (err) { toast(err && err.message || '清空失败'); });
    });

    eshare.clipboard.onChanged(onRealtime);
    paintNav();
    loadFavs().then(function () {
      load(false);
      refreshStats();
    });
  }

  // ══════════ 面板形态初始化 ══════════

  function initPanel() {
    // 宿主把实际注册到的全局热键放 URL 的 hk 参数（Win+V 被占时会静默回退，
    // 这里展示出来，用户才找得到唤起入口；纯浏览器调试/无热键时隐藏）。
    var hk = new URLSearchParams(location.search).get('hk');
    if (hk) {
      var hkEl = $('panelHotkey');
      hkEl.querySelector('kbd').textContent = hk;
      hkEl.hidden = false;
    }

    var searchEl = $('panelSearch'), debounce;
    searchEl.addEventListener('input', function () {
      clearTimeout(debounce);
      debounce = setTimeout(function () {
        state.query = searchEl.value.trim();
        state.sel = -1;
        load(false);
      }, 160);
    });

    var chips = Array.prototype.slice.call(document.querySelectorAll('[data-chipkind]'));
    chips.forEach(function (chip) {
      chip.addEventListener('click', function () {
        chips.forEach(function (c) { c.classList.remove('on'); });
        chip.classList.add('on');
        state.kind = chip.dataset.chipkind;
        state.sel = -1;
        load(false);
      });
    });

    document.addEventListener('keydown', function (ev) {
      if (ev.key === 'ArrowDown') { ev.preventDefault(); moveSel(1); }
      else if (ev.key === 'ArrowUp') { ev.preventDefault(); moveSel(-1); }
      else if (ev.key === 'Enter') {
        // 焦点在搜索框时 Enter 也复制当前选中项（无选中则选第一项）
        if (state.sel < 0 && state.entries.length) state.sel = 0;
        copySel();
      } else if (ev.key === 'Escape') {
        // 关闭面板是宿主窗口行为：经面板运行时通道通知宿主（iframe 模式为 no-op）
        if (eshare.window && eshare.window.dismiss) eshare.window.dismiss();
      }
    });

    eshare.clipboard.onChanged(onRealtime);

    // 每次面板弹出：重置搜索与选中、重拉数据、聚焦搜索框（Win+V 体验的关键）
    if (eshare.window && eshare.window.onShown) {
      eshare.window.onShown(function () {
        searchEl.value = '';
        state.query = '';
        state.sel = -1;
        chips.forEach(function (c) { c.classList.toggle('on', !c.dataset.chipkind); });
        state.kind = '';
        load(false);
        searchEl.focus();
      });
    }

    loadFavs().then(function () { load(false); });
    searchEl.focus();
  }

  // ══════════ 启动 ══════════

  if (IS_PANEL) initPanel(); else initFull();
})();
