// 待办周报插件：待办 CRUD（storage 持久化）+ 本周聚合生成 markdown 周报
// （复制到剪切板 / 存入个人云盘）。
(function () {
  // 内容先落到最终态；动画只作提示，可取消，宿主不支持 WAAPI 时直接可见。
  var stateMotionQuery = window.matchMedia ? window.matchMedia('(prefers-reduced-motion: no-preference)') : null;
  var stateMotions = [];
  function revealState(element) {
    stateMotions.slice().forEach(function (entry) {
      if (entry.element === element) entry.animation.cancel();
    });
    if (!stateMotionQuery || !stateMotionQuery.matches || !element.animate) return;
    var tokens = getComputedStyle(document.documentElement);
    var animation = element.animate([{ opacity: .55 }, { opacity: 1 }], {
      duration: parseFloat(tokens.getPropertyValue('--motion-enter')) || 200,
      easing: tokens.getPropertyValue('--motion-ease').trim() || 'cubic-bezier(.2,0,0,1)'
    });
    var entry = { element: element, animation: animation };
    stateMotions.push(entry);
    animation.onfinish = animation.oncancel = function () {
      var index = stateMotions.indexOf(entry);
      if (index >= 0) stateMotions.splice(index, 1);
    };
  }
  if (stateMotionQuery) {
    var stopStateMotion = function () {
      if (!stateMotionQuery.matches) stateMotions.slice().forEach(function (entry) { entry.animation.cancel(); });
    };
    if (stateMotionQuery.addEventListener) stateMotionQuery.addEventListener('change', stopStateMotion);
    else if (stateMotionQuery.addListener) stateMotionQuery.addListener(stopStateMotion);
  }
  var STORAGE_KEY = 'todos';
  var todos = [];            // {id, text, done, createdAt, doneAt}
  var filter = 'all';
  var saving = false, loaded = false, reportUpload = false;
  var returnFocus = null;

  var listEl = document.getElementById('list');
  var emptyEl = document.getElementById('empty');
  var newEl = document.getElementById('newTodo');
  var weeklyBtn = document.getElementById('weekly');
  var reportPanel = document.getElementById('reportPanel');
  var reportBody = document.getElementById('reportBody');
  var reportHint = document.getElementById('reportHint');
  var toastEl = document.getElementById('toast');
  var saveStatus = document.getElementById('saveStatus');
  var retryLoad = document.getElementById('retryLoad');

  function esc(s) {
    return String(s == null ? '' : s)
      .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  function toast(msg) {
    toastEl.textContent = msg;
    toastEl.hidden = false;
    clearTimeout(toast._t);
    toast._t = setTimeout(function () { toastEl.hidden = true; }, 1800);
  }

  // ── 持久化（storage 能力：按插件隔离的 KV）──
  function load() {
    setBusy(true);
    saveStatus.textContent = '正在读取待办…';
    retryLoad.hidden = true;
    return eshare.storage.get(STORAGE_KEY).then(function (v) {
      todos = Array.isArray(v) ? v : [];
      loaded = true;
      saveStatus.textContent = '';
      setBusy(false);
      render();
    }).catch(function (err) {
      saveStatus.textContent = '读取失败：' + (err.message || '请重试');
      retryLoad.hidden = false;
      setBusy(false);
    });
  }
  function setBusy(busy) {
    saving = busy;
    listEl.setAttribute('aria-busy', String(busy));
    newEl.disabled = weeklyBtn.disabled = busy || !loaded;
    listEl.querySelectorAll('input, button').forEach(function (el) { el.disabled = busy; });
  }
  // 保存失败恢复原状态；一次写入期间不叠加写入，避免反馈与持久化顺序相反。
  function saveChange(next, message, onSuccess) {
    if (saving || !loaded) return;
    var previousFocus = document.activeElement;
    var previous = todos;
    todos = next;
    render();
    setBusy(true);
    saveStatus.textContent = '正在保存…';
    eshare.storage.set(STORAGE_KEY, next).then(function () {
      saveStatus.textContent = message;
      if (onSuccess) onSuccess();
    }).catch(function (err) {
      todos = previous;
      render();
      saveStatus.textContent = '保存失败：' + (err.message || '请重试');
      toast(saveStatus.textContent);
    }).then(function () {
      setBusy(false);
      if (reportPanel.hidden && document.activeElement === document.body) {
        if (previousFocus && previousFocus.isConnected) previousFocus.focus();
        else newEl.focus();
      }
    });
  }

  function render() {
    var shown = todos.filter(function (t) {
      if (filter === 'open') return !t.done;
      if (filter === 'done') return t.done;
      return true;
    });
    var rows = Object.create(null);
    listEl.querySelectorAll('.row').forEach(function (el) { rows[el.dataset.id] = el; });
    var cursor = listEl.firstElementChild;
    shown.forEach(function (t) {
      var el = rows[t.id] || row(t);
      el.classList.toggle('done', t.done);
      el.querySelector('input').checked = t.done;
      if (el !== cursor) listEl.insertBefore(el, cursor);
      cursor = el.nextElementSibling;
      delete rows[t.id];
    });
    Object.keys(rows).forEach(function (id) { rows[id].remove(); });
    listEl.querySelectorAll('input, button').forEach(function (el) { el.disabled = saving; });
    emptyEl.hidden = shown.length > 0;
  }

  function row(t) {
    var div = document.createElement('div');
    div.className = 'row' + (t.done ? ' done' : '');
    div.dataset.id = t.id;
    var date = new Date(t.createdAt);
    var label = (date.getMonth() + 1) + '/' + date.getDate();
    div.innerHTML =
      '<input type="checkbox"' + (t.done ? ' checked' : '') + '>' +
      '<span class="text">' + esc(t.text) + '</span>' +
      '<time>' + label + '</time>' +
      '<button class="del" title="删除">✕</button>';
    div.querySelector('input').addEventListener('change', function () {
      var done = this.checked;
      saveChange(todos.map(function (item) {
        return item.id === t.id ? Object.assign({}, item, { done: done, doneAt: done ? Date.now() : null }) : item;
      }), done ? '已完成' : '已恢复为未完成');
    });
    div.querySelector('.del').addEventListener('click', function () {
      saveChange(todos.filter(function (x) { return x.id !== t.id; }), '已删除');
    });
    return div;
  }

  // ── 添加 ──
  newEl.addEventListener('keydown', function (e) {
    if (e.key !== 'Enter' || e.isComposing || saving || !loaded) return;
    var text = newEl.value.trim();
    if (!text) return;
    var item = { id: String(Date.now()) + Math.random().toString(36).slice(2, 6), text: text, done: false, createdAt: Date.now(), doneAt: null };
    saveChange([item].concat(todos), '已添加', function () { newEl.value = ''; });
  });

  // ── 过滤 ──
  document.querySelectorAll('.chip').forEach(function (chip) {
    chip.addEventListener('click', function () {
      document.querySelectorAll('.chip').forEach(function (c) { c.classList.remove('active'); });
      chip.classList.add('active');
      filter = chip.dataset.filter;
      render();
      revealState(listEl);
    });
  });
  document.querySelector('.chip[data-filter="all"]').classList.add('active');

  // ── 周报 ──
  function weekRange(now) {
    var d = new Date(now);
    var day = (d.getDay() + 6) % 7; // 周一=0
    var monday = new Date(d.getFullYear(), d.getMonth(), d.getDate() - day);
    return { start: monday.getTime(), end: now };
  }
  function fmt(ms) {
    var d = new Date(ms);
    return (d.getMonth() + 1) + '月' + d.getDate() + '日';
  }

  function buildReport() {
    var range = weekRange(Date.now());
    var inWeek = function (ms) { return ms >= range.start && ms <= range.end; };
    var doneThisWeek = todos.filter(function (t) { return t.done && t.doneAt && inWeek(t.doneAt); });
    var createdThisWeek = todos.filter(function (t) { return inWeek(t.createdAt); });
    var openAll = todos.filter(function (t) { return !t.done; });

    var lines = [];
    lines.push('# 工作周报（' + fmt(range.start) + ' - ' + fmt(range.end) + '）');
    lines.push('');
    lines.push('## 本周完成');
    if (doneThisWeek.length) doneThisWeek.forEach(function (t) { lines.push('- ' + t.text); });
    else lines.push('-（本周暂无已完成事项）');
    lines.push('');
    lines.push('## 进行中');
    var doing = createdThisWeek.filter(function (t) { return !t.done; });
    if (doing.length) doing.forEach(function (t) { lines.push('- ' + t.text); });
    else if (openAll.length) { lines.push('-（本周无新增进行中事项；历史遗留未完成 ' + openAll.length + ' 条）'); }
    else lines.push('-（无）');
    lines.push('');
    lines.push('## 下周计划');
    lines.push('- ');
    return lines.join('\n');
  }

  // 固定蒙层通过 visibility/opacity 过渡；hidden、焦点与交互立即切换，不等结束事件。
  function openReport() {
    returnFocus = document.activeElement;
    reportBody.textContent = buildReport();
    reportHint.textContent = reportUpload ? '上传中…' : '';
    reportPanel.hidden = false;
    reportPanel.removeAttribute('inert');
    reportPanel.setAttribute('aria-hidden', 'false');
    document.getElementById('closeReport').focus();
  }
  function closeReport() {
    reportPanel.hidden = true;
    reportPanel.setAttribute('inert', '');
    reportPanel.setAttribute('aria-hidden', 'true');
    if (returnFocus && returnFocus.isConnected) returnFocus.focus();
  }
  weeklyBtn.addEventListener('click', openReport);
  document.addEventListener('click', function (e) {
    if (e.target.closest && e.target.closest('#closeReport')) closeReport();
  });
  reportPanel.addEventListener('click', function (e) {
    if (e.target === reportPanel) closeReport();   // 点背景（蒙层）关闭
  });
  document.addEventListener('keydown', function (e) {
    if (reportPanel.hidden) return;
    if (e.key === 'Escape') closeReport();
    if (e.key === 'Tab') {
      var controls = reportPanel.querySelectorAll('button:not(:disabled)');
      var first = controls[0], last = controls[controls.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
  });

  document.getElementById('copyReport').addEventListener('click', function () {
    eshare.clipboard.copyText(reportBody.textContent).then(function () {
      toast('周报已复制到剪切板');
    }).catch(function (err) { toast(err.message || '复制失败'); });
  });

  document.getElementById('saveReport').addEventListener('click', function () {
    if (reportUpload) return;
    reportUpload = true;
    var button = this;
    button.disabled = true;
    button.setAttribute('aria-busy', 'true');
    var d = new Date();
    var stamp = d.getFullYear() + pad(d.getMonth() + 1) + pad(d.getDate());
    reportHint.textContent = '上传中…';
    eshare.uploadToDrive({
      filename: '周报-' + stamp + '.md',
      content: reportBody.textContent,
    }).then(function () {
      reportHint.textContent = '已存入「我的网盘」';
      toast('周报已上传到个人云盘');
    }).catch(function (err) {
      reportHint.textContent = err.message || '上传失败';
    }).then(function () {
      reportUpload = false;
      button.disabled = false;
      button.setAttribute('aria-busy', 'false');
    });
  });

  function pad(n) { return n < 10 ? '0' + n : '' + n; }

  retryLoad.addEventListener('click', load);
  load();
})();
