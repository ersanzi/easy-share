// EasyShare 插件 SDK（宿主统一分发，插件 HTML 内引用：
//   <script src="/plugins/_sdk/eshare.js"></script>）
//
// 通信协议（沙箱 iframe ↔ 宿主）：
//   请求：{ __eshare: 1, id, api, args }         iframe → 宿主
//   响应：{ __eshare: 1, id, ok, data, error }   宿主 → iframe
//   事件：{ __eshare: 1, event, payload }        宿主 → iframe
// 能力是否可用取决于插件 manifest 声明的 permissions（未授权调用返回错误）。
(function () {
  var seq = 0;
  var pending = new Map();
  var listeners = {};

  window.addEventListener('message', function (e) {
    var m = e.data;
    if (!m || m.__eshare !== 1) return;
    if (m.event) {
      var cbs = listeners[m.event];
      if (cbs) cbs.forEach(function (cb) { try { cb(m.payload); } catch (err) { console.error(err); } });
      return;
    }
    var p = pending.get(m.id);
    if (!p) return;
    pending.delete(m.id);
    if (m.ok) p.resolve(m.data);
    else p.reject(new Error(m.error || '调用失败'));
  });

  function call(api, args) {
    return new Promise(function (resolve, reject) {
      var id = ++seq;
      pending.set(id, { resolve: resolve, reject: reject });
      window.parent.postMessage({ __eshare: 1, id: id, api: api, args: args || {} }, '*');
    });
  }

  function on(event, cb) {
    (listeners[event] = listeners[event] || []).push(cb);
  }

  window.eshare = {
    call: call,
    on: on,
    // 插件私有 KV（权限 storage；按插件 ID 隔离，卸载即清除）
    storage: {
      get: function (key) { return call('storage.get', { key: key }); },
      set: function (key, value) { return call('storage.set', { key: key, value: value }); },
      remove: function (key) { return call('storage.remove', { key: key }); },
      keys: function () { return call('storage.keys'); },
    },
    // 剪切板（权限 clipboard.read / clipboard.write / clipboard.events）
    clipboard: {
      history: function (opt) { return call('clipboard.history', opt); },
      remove: function (id) { return call('clipboard.delete', { id: id }); },
      clear: function () { return call('clipboard.clear'); },
      copyText: function (text) { return call('clipboard.write', { kind: 'text', text: text }); },
      copyImage: function (file) { return call('clipboard.write', { kind: 'image', imageFile: file }); },
      copyFiles: function (files) { return call('clipboard.write', { kind: 'files', files: files }); },
      settings: function (opt) { return call('clipboard.settings', opt); },
      onChanged: function (cb) { on('clipboard:changed', cb); },
    },
    // 系统通知（权限 notification）
    notify: function (title, body) { return call('notification.show', { title: title, body: body }); },
    // 上传到个人云盘（权限 drive.upload；批次 2 开通）
    uploadToDrive: function (opt) { return call('drive.upload', opt); },
  };
})();
