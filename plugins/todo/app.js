// 待办周报插件：待办 CRUD（storage 持久化）+ 本周聚合生成 markdown 周报
// （复制到剪切板 / 存入个人云盘）。
(function () {
  var STORAGE_KEY = 'todos';
  var todos = [];            // {id, text, done, createdAt, doneAt}
  var filter = 'all';

  var listEl = document.getElementById('list');
  var emptyEl = document.getElementById('empty');
  var newEl = document.getElementById('newTodo');
  var weeklyBtn = document.getElementById('weekly');
  var reportPanel = document.getElementById('reportPanel');
  var reportBody = document.getElementById('reportBody');
  var reportHint = document.getElementById('reportHint');
  var toastEl = document.getElementById('toast');

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
    return eshare.storage.get(STORAGE_KEY).then(function (v) {
      todos = Array.isArray(v) ? v : [];
      render();
    }).catch(function () { todos = []; render(); });
  }
  function save() {
    return eshare.storage.set(STORAGE_KEY, todos);
  }

  function render() {
    var shown = todos.filter(function (t) {
      if (filter === 'open') return !t.done;
      if (filter === 'done') return t.done;
      return true;
    });
    listEl.innerHTML = '';
    shown.forEach(function (t) { listEl.appendChild(row(t)); });
    emptyEl.hidden = shown.length > 0;
  }

  function row(t) {
    var div = document.createElement('div');
    div.className = 'row' + (t.done ? ' done' : '');
    var date = new Date(t.createdAt);
    var label = (date.getMonth() + 1) + '/' + date.getDate();
    div.innerHTML =
      '<input type="checkbox"' + (t.done ? ' checked' : '') + '>' +
      '<span class="text">' + esc(t.text) + '</span>' +
      '<time>' + label + '</time>' +
      '<button class="del" title="删除">✕</button>';
    div.querySelector('input').addEventListener('change', function () {
      t.done = this.checked;
      t.doneAt = this.checked ? Date.now() : null;
      save().then(render);
    });
    div.querySelector('.del').addEventListener('click', function () {
      todos = todos.filter(function (x) { return x.id !== t.id; });
      save().then(render);
    });
    return div;
  }

  // ── 添加 ──
  newEl.addEventListener('keydown', function (e) {
    if (e.key !== 'Enter') return;
    var text = newEl.value.trim();
    if (!text) return;
    todos.unshift({ id: String(Date.now()) + Math.random().toString(36).slice(2, 6), text: text, done: false, createdAt: Date.now(), doneAt: null });
    newEl.value = '';
    save().then(render);
  });

  // ── 过滤 ──
  document.querySelectorAll('.chip').forEach(function (chip) {
    chip.addEventListener('click', function () {
      document.querySelectorAll('.chip').forEach(function (c) { c.classList.remove('active'); });
      chip.classList.add('active');
      filter = chip.dataset.filter;
      render();
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

  weeklyBtn.addEventListener('click', function () {
    reportBody.textContent = buildReport();
    reportHint.textContent = '';
    reportPanel.hidden = false;
  });
  document.getElementById('closeReport').addEventListener('click', function () {
    reportPanel.hidden = true;
  });

  document.getElementById('copyReport').addEventListener('click', function () {
    eshare.clipboard.copyText(reportBody.textContent).then(function () {
      toast('周报已复制到剪切板');
    }).catch(function (err) { toast(err.message || '复制失败'); });
  });

  document.getElementById('saveReport').addEventListener('click', function () {
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
    });
  });

  function pad(n) { return n < 10 ? '0' + n : '' + n; }

  load();
})();
