// 剪切板内置插件逻辑：历史列表 / 搜索 / 类型过滤 / 点击复制 / 单条删除 /
// 清空 / 暂停记录 / 实时追加新记录。
(function () {
  var PAGE = 60;
  var state = { kind: '', query: '', offset: 0, hasMore: false };

  var listEl = document.getElementById('list');
  var emptyEl = document.getElementById('empty');
  var moreEl = document.getElementById('more');
  var searchEl = document.getElementById('search');
  var pausedEl = document.getElementById('paused');
  var clearEl = document.getElementById('clear');
  var loadMoreBtn = document.getElementById('loadMore');
  var toastEl = document.getElementById('toast');

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;');
  }

  function toast(msg) {
    toastEl.textContent = msg;
    toastEl.hidden = false;
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { toastEl.hidden = true; }, 1600);
  }

  function timeLabel(ms) {
    var d = new Date(ms);
    var now = new Date();
    var hm = ('0' + d.getHours()).slice(-2) + ':' + ('0' + d.getMinutes()).slice(-2);
    if (d.toDateString() === now.toDateString()) return hm;
    return (d.getMonth() + 1) + '/' + d.getDate() + ' ' + hm;
  }

  function sizeLabel(n) {
    if (n < 1024) return n + ' B';
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
    return (n / 1024 / 1024).toFixed(1) + ' MB';
  }

  function renderEntry(e) {
    var div = document.createElement('div');
    div.className = 'item';
    div.dataset.id = e.id;

    var badge = e.kind === 'image'
      ? '<span class="badge img">图片 ' + e.width + '×' + e.height + '</span>'
      : e.kind === 'files'
        ? '<span class="badge file">文件 ×' + (e.files ? e.files.length : 0) + '</span>'
        : '<span class="badge">文本</span>';
    var src = e.source ? ' · ' + esc(e.source) : '';

    var body = '';
    if (e.kind === 'text') {
      body = '<div class="item-text">' + esc(e.text) + '</div>';
    } else if (e.kind === 'image' && e.file) {
      body = '<img loading="lazy" src="/clipboard-files/' + encodeURIComponent(e.file) + '" alt="截图">';
    } else if (e.kind === 'files' && e.files) {
      body = '<div class="item-files">' + e.files.map(function (p) {
        return '<div class="path" title="' + esc(p) + '">' + esc(p) + '</div>';
      }).join('') + '</div>';
    }

    div.innerHTML =
      '<div class="item-head">' + badge +
      '<span>' + timeLabel(e.createdAt) + src + '</span>' +
      (e.kind === 'text' ? '<span>' + sizeLabel(e.size) + '</span>' : '') +
      '<span class="spacer"></span>' +
      '<button class="del" title="删除此条">✕</button></div>' + body;

    // 点击整卡：重新复制
    div.addEventListener('click', function (ev) {
      if (ev.target.classList.contains('del')) return;
      copyEntry(e).then(function () { toast('已重新复制'); })
        .catch(function (err) { toast(err.message || '复制失败'); });
    });
    // 单条删除
    div.querySelector('.del').addEventListener('click', function (ev) {
      ev.stopPropagation();
      eshare.clipboard.remove(e.id).then(function () {
        div.remove();
        refreshEmpty();
      }).catch(function (err) { toast(err.message || '删除失败'); });
    });
    return div;
  }

  function copyEntry(e) {
    if (e.kind === 'text') return eshare.clipboard.copyText(e.text);
    if (e.kind === 'image') return eshare.clipboard.copyImage(e.file);
    if (e.kind === 'files') return eshare.clipboard.copyFiles(e.files);
    return Promise.reject(new Error('未知类型'));
  }

  function refreshEmpty() {
    emptyEl.hidden = listEl.children.length > 0;
  }

  function load(append) {
    if (!append) { state.offset = 0; listEl.innerHTML = ''; }
    return eshare.clipboard.history({
      limit: PAGE, offset: state.offset, kind: state.kind, query: state.query,
    }).then(function (items) {
      (items || []).forEach(function (e) { listEl.appendChild(renderEntry(e)); });
      state.offset += (items || []).length;
      state.hasMore = (items || []).length >= PAGE;
      moreEl.hidden = !state.hasMore;
      refreshEmpty();
    }).catch(function (err) {
      toast(err.message || '加载失败');
    });
  }

  // 类型过滤
  document.querySelectorAll('.chip').forEach(function (chip) {
    chip.addEventListener('click', function () {
      document.querySelectorAll('.chip').forEach(function (c) { c.classList.remove('active'); });
      chip.classList.add('active');
      state.kind = chip.dataset.kind;
      load(false);
    });
  });
  document.querySelector('.chip[data-kind=""]').classList.add('active');

  // 搜索（250ms 防抖）
  var debounce;
  searchEl.addEventListener('input', function () {
    clearTimeout(debounce);
    debounce = setTimeout(function () {
      state.query = searchEl.value.trim();
      load(false);
    }, 250);
  });

  // 加载更多
  loadMoreBtn.addEventListener('click', function () { load(true); });

  // 清空
  clearEl.addEventListener('click', function () {
    if (!confirm('确定清空全部剪切板记录？')) return;
    eshare.clipboard.clear().then(function () {
      listEl.innerHTML = '';
      refreshEmpty();
      toast('已清空');
    }).catch(function (err) { toast(err.message || '清空失败'); });
  });

  // 暂停记录开关（初始状态加载后同步）
  pausedEl.addEventListener('change', function () {
    eshare.clipboard.settings({ paused: pausedEl.checked }).catch(function (err) {
      toast(err.message || '设置失败');
      pausedEl.checked = !pausedEl.checked;
    });
  });

  // 实时新记录：插到列表顶部（与当前过滤条件匹配时）
  eshare.on ? eshare.clipboard.onChanged(function (e) {
    if (state.kind && e.kind !== state.kind) return;
    if (state.query) {
      var hay = ((e.text || '') + ' ' + (e.files || []).join(' ')).toLowerCase();
      if (hay.indexOf(state.query.toLowerCase()) < 0) return;
    }
    emptyEl.hidden = true;
    listEl.prepend(renderEntry(e));
  }) : null;

  // 初始化：先取设置同步暂停开关，再加载首页
  eshare.clipboard.settings().then(function (st) {
    pausedEl.checked = !!st.paused;
  }).catch(function () {});
  load(false);
})();
