// EasyShare 插件 SDK（宿主统一分发，插件 HTML 内引用：
//   <script src="/plugins/_sdk/eshare.js"></script>）
//
// 通信协议（同一套 {__eshare:1,...} 消息，两种运行时通道）：
//   ① 沙箱 iframe（主窗口插件页）：iframe → 宿主页面，window.parent.postMessage；
//   ② 原生面板（快捷面板独立窗口，?panel=1）：
//      - Windows（WebView2）：window.chrome.webview.postMessage ↔ 宿主 Eval 分发；
//      - macOS（WKWebView）：window.webkit.messageHandlers.espanel ↔ evaluateJavaScript。
//   请求：{ __eshare: 1, id, api, args }         插件 → 宿主
//   响应：{ __eshare: 1, id, ok, data, error }   宿主 → 插件
//   事件：{ __eshare: 1, event, payload }        宿主 → 插件
// 能力是否可用取决于插件 manifest 声明的 permissions（未授权调用返回错误）。
(function () {
  var seq = 0;
  var pending = new Map();
  var listeners = {};

  // ── 通道判定：iframe 优先（保持既有行为），其次原生面板，最后纯浏览器调试（不可用）──
  var inIframe = window.parent !== window;
  var webview2 = !inIframe && window.chrome && window.chrome.webview && window.chrome.webview.postMessage;
  var wkwebview = !inIframe && window.webkit && window.webkit.messageHandlers &&
    window.webkit.messageHandlers.espanel;

  function postNative(msg) {
    if (webview2) {
      // WebView2 侧必须发字符串：go-webview2 的 MessageReceived 用
      // TryGetWebMessageAsString 读取，对象消息会直接 E_INVALIDARG。
      window.chrome.webview.postMessage(JSON.stringify(msg));
    } else if (wkwebview) {
      window.webkit.messageHandlers.espanel.postMessage(msg);
    }
  }

  // 宿主 → 插件的统一入口：响应与事件都走这里。
  // iframe 模式由 message 事件调进来；原生模式由宿主 Eval 调 window.__eshareNative.deliver。
  function handleInbound(m) {
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
  }

  window.addEventListener('message', function (e) { handleInbound(e.data); });

  // 原生面板的宿主分发口（Go 侧 Eval("window.__eshareNative.deliver(<json>)") 调用）。
  window.__eshareNative = { deliver: handleInbound };

  if (webview2) {
    window.chrome.webview.addEventListener('message', function (e) { handleInbound(e.data); });
  }
  // WKWebView 无事件监听：宿主一律走 deliver。

  function call(api, args) {
    return new Promise(function (resolve, reject) {
      var id = ++seq;
      pending.set(id, { resolve: resolve, reject: reject });
      var msg = { __eshare: 1, id: id, api: api, args: args || {} };
      if (inIframe) window.parent.postMessage(msg, '*');
      else postNative(msg);
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
      stats: function () { return call('clipboard.stats'); },
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
    // 上传到个人云盘（权限 drive.upload）
    uploadToDrive: function (opt) { return call('drive.upload', opt); },
    // 快捷面板窗口控制（仅面板运行时生效；主窗口 iframe 模式为 no-op，无需权限）：
    //   dismiss() 请求关闭面板（Esc 等插件侧发起的关闭）；
    //   onShown(cb) 面板每次弹出时回调（插件借此重置状态并聚焦搜索框）。
    // 宿主侧约定：面板内插件成功执行 clipboard.write = 用户选中该条，
    // 由宿主收起面板并自动粘贴，插件不需要也不应该再调 dismiss。
    window: {
      dismiss: function () {
        if (inIframe) return Promise.resolve(false);
        postNative({ __esharePanel: 1, cmd: 'dismiss' });
        return Promise.resolve(true);
      },
      onShown: function (cb) { on('panel:shown', cb); },
    },
  };
})();
