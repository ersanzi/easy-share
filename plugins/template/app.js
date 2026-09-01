// 插件模板逻辑：演示 storage 能力的最小用法。
// 所有能力是否可用取决于 manifest.json 声明的 permissions（见 ../README.md 权限表）。
(function () {
  var hint = document.getElementById('hint');
  var save = document.getElementById('save');

  // 启动时读插件私有 KV（按插件 ID 隔离，卸载即清除）
  eshare.storage.get('demo').then(function (value) {
    hint.textContent = value === null || value === undefined
      ? '还没有数据。点下面的按钮写入一条。'
      : '已存数据：' + JSON.stringify(value);
  }).catch(function (err) {
    // 最常见原因：manifest 没声明 storage 权限
    hint.textContent = '读取失败：' + (err.message || err);
  });

  save.addEventListener('click', function () {
    eshare.storage.set('demo', { at: Date.now(), text: '你好插件' })
      .then(function () { return eshare.storage.get('demo'); })
      .then(function (v) { hint.textContent = '已存数据：' + JSON.stringify(v); })
      .catch(function (err) { hint.textContent = '写入失败：' + (err.message || err); });
  });
})();
