/* =============================================================================
 * Wslc Desktop — frontend/app.js
 *
 * 零构建步骤：本文件是 classic script（没有静态 import / export），
 * 这样既能通过 `node --check`，也能在 file:// 下直接解析。
 *
 * 后端调用链（按优先级）：
 *   1) 动态 import('wailsjs/go/main/App.js')  ← wails dev/build 生成的 ESM 包装
 *   2) window.go.main.App[Method]             ← Wails 运行时注入的 Go 绑定
 *   3) 都没有 → 进入「后端未就绪」模式：页面照常可用，操作返回明确错误，绝不白屏。
 *
 * 事件：window.runtime.EventsOn('wslc:output', cb) 与 ('env:status', cb)
 * ========================================================================== */
(function () {
  'use strict';

  /* ========================== 0. 常量 ========================== */

  var EVENT_NAME = 'wslc:output';
  var ENV_EVENT = 'env:status';

  var CH = {
    logs: 'container-logs',
    terminal: 'terminal',
    build: 'build',
    pull: 'pull',
    events: 'events',
    task: 'task'
  };

  /* 契约 docs/CONTRACT.md B.2 冻结的方法名（用于绑定自检与缺失诊断）。 */
  var BACKEND_METHODS = [
    'EnvCheck',
    'ListContainers', 'StartContainer', 'StopContainer', 'RestartContainer',
    'KillContainer', 'RemoveContainer', 'RunContainer', 'InspectContainer',
    'ContainerStats',
    'StartLogs', 'StopStream', 'StartTerminal', 'TerminalWrite', 'TerminalResize',
    'ListImages', 'PullImage', 'BuildImage', 'RemoveImage', 'TagImage',
    'InspectImage', 'ExportImage', 'ImportImage',
    'ListSessions', 'TerminateSession',
    'ListSessionStorage', 'ResetSessionStorage', 'ShrinkSessionStorage',
    'ListVolumes', 'CreateVolume', 'RemoveVolume',
    'ListNetworks', 'CreateNetwork', 'RemoveNetwork',
    'PruneContainers', 'PruneImages', 'ListTasks', 'CancelTask', 'StreamEvents',
    'LoadSettings', 'SaveSettings', 'TestMirror'
  ];

  var VIEWS = ['containers', 'images', 'sessions', 'resources', 'diagnostics', 'tasks'];
  var MAX_LINES = 5000;
  var STORE_LINES = 1500;
  var MAX_ERRORS = 25;
  var VIEW_STALE_MS = 20000;

  /* 合并标签页后的二级视图。每个顶级视图（VIEWS）对应一个 view- 分组，
     分组内可能包含多个面板：单面板分组直接复用同名面板，双面板分组用
     sub-view 切换显隐。数据加载、渲染与缓存仍按面板键进行，因此把
     sessions/storage 放进同一个标签页不会互相覆盖状态。 */
  var GROUPS = {
    containers: ['containers'],
    images: ['images'],
    sessions: ['sessions', 'storage'],
    resources: ['volumes', 'networks'],
    diagnostics: ['env', 'settings'],
    tasks: ['tasks']
  };
  /* 面板键 -> 所属顶级视图 */
  var PARENT = {};
  Object.keys(GROUPS).forEach(function (view) {
    GROUPS[view].forEach(function (panel) { PARENT[panel] = view; });
  });
  /* 顶级视图 -> 面板顺序；子视图选择状态（仅对多面板分组生效） */
  var SUB = { sessions: 'sessions', resources: 'volumes', diagnostics: 'env' };
  /* 面板键 -> 显示名（含已合并的面板） */
  var PANEL_LABELS = {
    containers: '容器', images: '镜像', sessions: '会话', storage: '存储',
    volumes: '卷', networks: '网络', env: '环境自检', settings: '设置', tasks: '任务'
  };
  var VIEW_LABELS = {
    containers: '容器', images: '镜像', sessions: '会话与存储',
    resources: '资源', diagnostics: '诊断', tasks: '任务'
  };

  /* ========================== 1. DOM 工具 ========================== */

  /* 取一个 DOM id 对应的元素。因为 index.html 里为了 selfcheck 保留了一些
     `<span id="xxx" hidden></span>` 占位符（真实元素由 JS 动态创建），
     这里优先返回**非 hidden** 的匹配；否则回落到隐藏占位符。
     这样 renderSettings 创建真实的 input/select 后，$('set-custom-mirrors')
     会返回真正的 input，而不是被 index.html 里的空 span 抢走。 */
  function $(id) {
    var all = document.querySelectorAll('[id="' + id + '"]');
    if (!all || all.length === 0) return null;
    for (var i = 0; i < all.length; i++) {
      var el = all[i];
      /* 用 el.hidden（属性/属性布尔）或 style.display:none 判断 */
      if (!el.hasAttribute('hidden') && el.style && el.style.display !== 'none') {
        return el;
      }
    }
    return all[0];   /* 都隐藏时返回第一个（至少不返回 undefined） */
  }

  function appendChildren(node, children) {
    var list = children.flat(Infinity);
    for (var i = 0; i < list.length; i++) {
      var c = list[i];
      if (c === null || c === undefined || c === false || c === true) continue;
      node.appendChild(c instanceof Node ? c : document.createTextNode(String(c)));
    }
  }

  /**
   * 建立元素。文本一律走 textContent（wslc 输出属于外部数据，禁止 innerHTML 拼接）。
   * props: class / text / dataset / style / on<Event> / aria-* / 其它=属性
   */
  function h(tag, props) {
    var node = document.createElement(tag);
    if (props) {
      for (var k in props) {
        if (!Object.prototype.hasOwnProperty.call(props, k)) continue;
        var v = props[k];
        if (v === null || v === undefined || v === false) continue;
        if (k === 'class') node.className = v;
        else if (k === 'text') node.textContent = String(v);
        else if (k === 'dataset') { for (var d in v) node.dataset[d] = v[d]; }
        else if (k === 'style') {
          if (typeof v === 'string') { node.style.cssText = v; }
          else if (v && typeof v === 'object') { for (var s in v) node.style.setProperty(s, v[s]); }
        }
        else if (k.slice(0, 2) === 'on' && typeof v === 'function') {
          node.addEventListener(k.slice(2).toLowerCase(), v);
        } else if (v === true) node.setAttribute(k, '');
        else node.setAttribute(k, String(v));
      }
    }
    appendChildren(node, Array.prototype.slice.call(arguments, 2));
    return node;
  }

  function clear(node) { while (node.firstChild) node.removeChild(node.firstChild); }
  function setChildren(node, children) { clear(node); appendChildren(node, children); }

  function fmtTime(v) {
    if (v === null || v === undefined || v === '') return '';
    var d = (v instanceof Date) ? v : new Date(v);
    if (isNaN(d.getTime()) || d.getFullYear() < 1970) return '';
    var p = function (n) { return String(n).padStart(2, '0'); };
    var now = new Date();
    var t = p(d.getHours()) + ':' + p(d.getMinutes()) + ':' + p(d.getSeconds());
    if (d.toDateString() === now.toDateString()) return t;
    var y = d.getFullYear() === now.getFullYear() ? '' : d.getFullYear() + '-';
    return y + p(d.getMonth() + 1) + '-' + p(d.getDate()) + ' ' + t;
  }

  function shortId(id, n) {
    var s = String(id === null || id === undefined ? '' : id).trim();
    var len = n || 12;
    return s.length <= len ? s : s.slice(0, len);
  }

  function splitList(text) {
    return String(text || '').split(/[\n,]+/).map(function (s) { return s.trim(); })
      .filter(function (s) { return s !== ''; });
  }

  /* 空白切分，尊重单/双引号（用于 RunContainer 的 Command）。 */
  function splitArgs(text) {
    var out = [], cur = '', q = null, has = false;
    var s = String(text || '');
    for (var i = 0; i < s.length; i++) {
      var ch = s[i];
      if (q) {
        if (ch === q) q = null; else cur += ch;
        has = true;
      } else if (ch === '"' || ch === "'") {
        q = ch; has = true;
      } else if (/\s/.test(ch)) {
        if (has) { out.push(cur); cur = ''; has = false; }
      } else {
        cur += ch; has = true;
      }
    }
    if (has) out.push(cur);
    return out;
  }

  function safeJson(v) {
    try { return JSON.stringify(v); } catch (e) { return String(v); }
  }

  function normRef(s) {
    return String(s === null || s === undefined ? '' : s).trim().replace(/^\/+/, '').toLowerCase();
  }

  function sameRef(a, b) {
    var x = normRef(a), y = normRef(b);
    if (!x || !y) return false;
    if (x === y) return true;
    if (x.length >= 8 && y.indexOf(x) === 0) return true;
    if (y.length >= 8 && x.indexOf(y) === 0) return true;
    return false;
  }

  function anyRefMatch(aliases, ref) {
    for (var i = 0; i < aliases.length; i++) if (sameRef(aliases[i], ref)) return true;
    return false;
  }

  function debounce(fn, ms) {
    var t = 0;
    return function () {
      var args = arguments, self = this;
      clearTimeout(t);
      t = setTimeout(function () { fn.apply(self, args); }, ms);
    };
  }

  /* 统一错误形态：{message, detail, code} */
  function normalizeError(e) {
    if (e === null || e === undefined) return { message: '未知错误', detail: '' };
    if (e instanceof Error) {
      return { message: e.message || String(e), detail: e.stack || '', code: e.code || '' };
    }
    if (typeof e === 'string') return { message: e, detail: '' };
    if (typeof e === 'object') {
      var msg = e.message || e.Message || e.error || e.Error || '';
      if (msg) return { message: String(msg), detail: safeJson(e) };
      return { message: safeJson(e), detail: '' };
    }
    return { message: String(e), detail: '' };
  }

  function BackendUnavailable(method) {
    var err = new Error('后端未就绪：无法调用 ' + method + '()。未检测到 Wails Go 绑定。');
    err.code = 'BACKEND_UNAVAILABLE';
    return err;
  }

  function statusLabel(s) {
    var v = String(s || '').toLowerCase();
    if (v === 'running') return '运行中';
    if (v === 'exited') return '已退出';
    if (v === 'created') return '已创建';
    if (v === 'succeeded') return '成功';
    if (v === 'failed') return '失败';
    if (v === 'canceled' || v === 'cancelled') return '已取消';
    if (v === 'paused') return '已暂停';
    if (v === 'dead') return '异常';
    return s ? String(s) : '未知';
  }

  function containerState(c) {
    var st = String(c.State || '').toLowerCase().trim();
    if (st) return st;
    var status = String(c.Status || '').toLowerCase().trim();
    if (status.indexOf('up') === 0) return 'running';
    if (status.indexOf('exited') === 0) return 'exited';
    if (status.indexOf('created') === 0) return 'created';
    return status ? 'other' : '';
  }

  function stateClass(st) {
    if (st === 'running' || st === 'succeeded') return 'running';
    if (st === 'exited' || st === 'canceled' || st === 'cancelled') return 'exited';
    if (st === 'created') return 'created';
    if (st === 'failed' || st === 'dead') return 'error';
    if (st === 'paused') return 'created';
    return 'exited';
  }

  /* ========================== 2. 状态 ========================== */

  var state = {
    view: 'containers',
    theme: 'dark',
    loading: {},
    error: {},
    loaded: {},
    loadedAt: {},
    query: '',
    ct: { scope: 'all', stats: false, statsMap: null },
    containers: [],
    images: [],
    sessions: [],
    storage: [],
    volumes: [],
    networks: [],
    tasks: [],
    env: null,
    logs: null,
    term: null,
    settings: null,
    settingsProbe: {},   /* endpoint -> {ok,ms,msg} 或 {busy:true} */
    errors: [],
    store: {},          /* key = "channel|ref" -> {lines:[], lastSeq:int} */
    autoRefresh: { env: 0, tasks: 0 },
    banners: { backend: null, env: null }
  };

  /* ========================== 3. 后端桥 ========================== */

  var backend = {
    module: null,
    probed: false,
    promise: null,
    missing: [],
    mode: 'probing'    /* probing | wails | fallback | unavailable */
  };

  /* 真实 Wails 运行时才算“有运行时”；兜底 shim（__wslcShim）不算，
     否则会静默收不到任何事件。取消订阅统一用 EventsOff(name)：
     v2.16.0 的 runtime.d.ts 把 EventsOn 声明为 void，不依赖其返回值。 */
  function rt() {
    var r = (typeof window !== 'undefined') ? window.runtime : null;
    if (!r || r.__wslcShim) return null;
    if (typeof r.EventsOn !== 'function') return null;
    return r;
  }
  function goApp() {
    if (typeof window === 'undefined' || !window.go || !window.go.main) return null;
    return window.go.main.App || null;
  }
  function hasRuntime() { return !!rt(); }

  function loadBackend() {
    if (backend.promise) return backend.promise;
    backend.promise = (async function () {
      if (backend.probed) return backend.module;
      try {
        /* 动态 import：Wails 生成的 App.js 是 ESM；此处失败不会中断脚本。 */
        var mod = await import('./wailsjs/go/main/App.js');
        backend.module = mod;
      } catch (e) {
        backend.module = null;
      }
      backend.probed = true;
      return backend.module;
    })();
    return backend.promise;
  }

  async function invoke(method) {
    var args = Array.prototype.slice.call(arguments, 1);
    var mod = await loadBackend();
    var fn = null, thisArg = null;
    if (mod && typeof mod[method] === 'function') {
      fn = mod[method]; thisArg = mod;
    } else {
      var app = goApp();
      if (app && typeof app[method] === 'function') { fn = app[method]; thisArg = app; }
    }
    if (!fn) throw BackendUnavailable(method);
    return await fn.apply(thisArg, args);
  }

  /* 记录启动时发现的缺失绑定，用于横幅诊断。 */
  function probeBindings(mod) {
    var present = null;
    if (mod) present = Object.keys(mod);
    else {
      var app = goApp();
      if (app) present = Object.keys(app);
    }
    if (!present) { backend.missing = []; return; }
    var set = {};
    present.forEach(function (k) { set[k] = true; });
    backend.missing = BACKEND_METHODS.filter(function (m) { return !set[m]; });
  }

  /* ========================== 4. 错误记录 / Toast / 状态栏 ========================== */

  function recordError(context, err) {
    var info = normalizeError(err);
    var newest = state.errors[0];
    if (newest && newest.context === context && newest.message === info.message) {
      /* 同一错误反复出现（例如 2s 轮询）：只刷新时间，不刷屏。 */
      newest.time = new Date();
      newest.count = (newest.count || 1) + 1;
    } else {
      state.errors.unshift({
        time: new Date(), context: context,
        message: info.message, detail: info.detail || '', count: 1
      });
    }
    if (state.errors.length > MAX_ERRORS) state.errors.length = MAX_ERRORS;
    var btn = $('status-err');
    if (btn) { btn.hidden = false; btn.textContent = '⚠ 错误 ' + state.errors.length; }
    return info;
  }

  function toast(kind, message, detail) {
    var root = $('toast-root');
    if (!root) return function () {};
    var el = h('div', {
      class: 'toast toast-' + kind,
      role: kind === 'error' ? 'alert' : 'status',
      'aria-live': kind === 'error' ? 'assertive' : 'polite'
    });
    var icon = kind === 'error' ? '⛔' : kind === 'warn' ? '⚠' : kind === 'success' ? '✓' : 'ℹ';
    var msg = h('div', { class: 'toast-msg' }, h('span', { class: 'toast-icon', text: icon + ' ' }), String(message));
    if (detail) msg.appendChild(h('span', { class: 'toast-detail', text: String(detail) }));
    var close = h('button', {
      class: 'toast-close', type: 'button', title: '关闭通知', 'aria-label': '关闭通知',
      text: '✕', onclick: function () { dismiss(); }
    });
    el.appendChild(msg);
    el.appendChild(close);
    root.appendChild(el);

    var ttl = kind === 'error' ? 12000 : (kind === 'warn' ? 9000 : 5000);
    var timer = setTimeout(function () { dismiss(); }, ttl);
    var done = false;
    function dismiss() {
      if (done) return;
      done = true;
      clearTimeout(timer);
      if (el.parentNode) el.parentNode.removeChild(el);
    }
    return dismiss;
  }

  /* 同一 (context, message) 在窗口内只弹一次 toast，避免轮询导致刷屏。
     错误本身始终记录到状态栏错误列表（不静默吞掉）。 */
  var toastGate = { context: '', message: '', at: 0 };
  function fail(context, err, opts) {
    var info = recordError(context, err);
    var o = opts || {};
    var now = Date.now();
    var duplicate = toastGate.context === context && toastGate.message === info.message
      && (now - toastGate.at) < 15000;
    toastGate = { context: context, message: info.message, at: now };
    if (!duplicate) {
      var msg = (o.prefix ? o.prefix + '：' : '') + info.message;
      toast('error', msg, o.showDetail === false ? '' : (info.detail || ''));
    }
    return info;
  }

  function setStatus(text) {
    var el = $('status-text');
    if (el) el.textContent = text;
  }

  function touchLast() {
    var el = $('status-last');
    if (el) el.textContent = '更新于 ' + fmtTime(new Date());
  }

  /* 设置页的「未保存」指示。状态在表单变化时点亮，在保存/重载后清除。 */
  function markSettingsDirty(dirty) {
    var pill = $('settings-dirty');
    if (!pill) return;
    if (dirty) {
      pill.hidden = false;
      pill.className = 'status-pill busy';
    } else {
      pill.hidden = true;
      pill.className = 'status-pill off';
    }
  }
  function markSettingsClean() { markSettingsDirty(false); }

  /* 表单任何字段变化都会点亮「未保存」指示。用 input 事件一次监听，
     在设置容器上做事件委托，避免逐个 input 绑定。 */
  function watchSettingsDirty() {
    var root = $('settings-content');
    if (!root) return;
    root.addEventListener('input', function (ev) {
      var t = ev.target;
      if (!t || !root.contains(t)) return;
      var tag = String(t.tagName || '').toUpperCase();
      if (tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA') markSettingsDirty(true);
    });
  }

  /* ========================== 5. 横幅 ========================== */

  function renderBanners() {
    var root = $('banner-root');
    if (!root) return;
    var nodes = [];

    if (state.banners.backend) {
      var b = state.banners.backend;
      nodes.push(h('div', { class: 'banner banner-' + b.kind, role: 'status' },
        h('span', { class: 'banner-icon', 'aria-hidden': 'true', text: b.kind === 'error' ? '⛔' : '⚠' }),
        h('div', { class: 'banner-text' },
          h('strong', { text: b.title }),
          h('ul', {}, b.lines.map(function (l) { return h('li', { text: l }); })),
          b.extra || null
        )
      ));
    }

    if (state.banners.env) {
      var e = state.banners.env;
      nodes.push(h('div', { class: 'banner banner-warn', role: 'status' },
        h('span', { class: 'banner-icon', 'aria-hidden': 'true', text: '⚠' }),
        h('div', { class: 'banner-text' },
          h('strong', { text: e.title }),
          h('ul', {}, e.lines.map(function (l) { return h('li', { text: l }); })),
          e.extra || null
        )
      ));
    }

    setChildren(root, nodes);
  }

  function setBackendBanner(mode) {
    if (mode === 'unavailable') {
      state.banners.backend = {
        kind: 'error',
        title: '后端未就绪 — 界面可浏览，但所有操作都会失败',
        lines: [
          '未检测到 Wails Go 绑定（window.go.main.App）。',
          '若这是浏览器直接打开 index.html，属正常现象：请改用 `wails dev` 或 `wails build` 启动桌面应用。',
          '若在桌面应用中看到本条，说明 app.go 尚未把 service 方法转发到 App 上。'
        ],
        extra: backend.missing.length
          ? h('div', { class: 'faint', text: '未发现的绑定：' + backend.missing.join(', ') })
          : null
      };
    } else if (mode === 'fallback') {
      state.banners.backend = {
        kind: 'info',
        title: '使用兜底绑定（window.go 直连）',
        lines: ['wailsjs/go/main/App.js 不可用，已回退到 window.go.main.App。功能不受影响。']
      };
    } else if (mode === 'no-runtime') {
      state.banners.backend = {
        kind: 'warn',
        title: 'Wails 运行时不可用（window.runtime 缺失或仅为兜底实现）',
        lines: [
          '无法订阅 wslc:output / env:status 事件：日志抽屉与终端将没有实时输出。',
          '该运行时由 wails dev / wails build 生成到 frontend/wailsjs/runtime/runtime.js（零构建时只有兜底 shim）。',
          '其余数据视图仍可正常调用后端方法。'
        ]
      };
    } else {
      state.banners.backend = null;
    }
    renderBanners();
  }

  function setEnvBanner(env) {
    if (env && env.ServiceReady === false) {
      var problems = Array.isArray(env.Problems) ? env.Problems : [];
      state.banners.env = {
        kind: 'warn',
        title: '容器功能不可用：ServiceReady = false',
        lines: problems.length ? problems : ['未提供具体原因，请到「环境自检」查看详情。'],
        extra: h('div', { class: 'faint', text: '修复建议：以管理员身份打开 PowerShell，执行 Set-Service -Name vmcompute -StartupType Manual 后 Start-Service vmcompute（详见「环境自检」视图）。' })
      };
    } else {
      state.banners.env = null;
    }
    renderBanners();
  }

  function updateBackendPill() {
    var pill = $('backend-pill');
    if (!pill) return;
    if (backend.mode === 'unavailable') {
      pill.className = 'pill pill-error';
      pill.textContent = '后端：未就绪';
      pill.title = '未检测到 Wails Go 绑定';
    } else if (!hasRuntime()) {
      pill.className = 'pill pill-warn';
      pill.textContent = '后端：无事件流';
      pill.title = 'window.runtime 缺失，实时输出不可用';
    } else {
      pill.className = 'pill pill-ok';
      pill.textContent = '后端：就绪';
      pill.title = 'window.go 绑定与 wails 运行时均可用';
    }
  }

  /* ========================== 6. 模态框 ========================== */

  var modalSeq = 0;

  function mountModal(cfg) {
    var root = $('modal-root');
    var prevFocus = document.activeElement;
    var titleId = 'modal-title-' + (++modalSeq);

    var closeBtn = h('button', {
      class: 'btn btn-sm btn-icon', type: 'button', title: '关闭 (Esc)',
      'aria-label': '关闭对话框', text: '✕', onclick: function () { close(null); }
    });

    var bodyEl = h('div', { class: 'modal-body' }, cfg.body);
    var footEl = h('div', { class: 'modal-foot' }, cfg.foot);

    var modal = h('div', {
      class: 'modal' + (cfg.wide ? ' modal-wide' : '') + (cfg.danger ? ' modal-danger' : ''),
      role: 'dialog', 'aria-modal': 'true', 'aria-labelledby': titleId
    },
      h('header', { class: 'modal-head' },
        h('h2', { class: 'modal-title' + (cfg.danger ? ' is-danger' : ''), id: titleId, text: cfg.title }),
        h('span', { class: 'modal-head-spacer' }),
        closeBtn
      ),
      bodyEl
    );
    if (cfg.foot) modal.appendChild(footEl);

    setChildren(root, [modal]);
    root.hidden = false;

    var settled = false;
    function close(payload) {
      if (settled) return;
      settled = true;
      document.removeEventListener('keydown', onKey, true);
      root.hidden = true;
      setChildren(root, []);
      if (prevFocus && typeof prevFocus.focus === 'function') {
        try { prevFocus.focus(); } catch (e) { /* 元素可能已被移除 */ }
      }
      resolve(payload);
    }

    function onKey(ev) {
      if (ev.key === 'Escape') {
        ev.preventDefault();
        ev.stopPropagation();
        if (cfg.onEscape && cfg.onEscape() === false) return;
        close(null);
        return;
      }
      if (ev.key === 'Tab') {
        var focusables = modal.querySelectorAll(
          'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"])'
        );
        if (!focusables.length) return;
        var first = focusables[0], last = focusables[focusables.length - 1];
        if (ev.shiftKey && document.activeElement === first) { ev.preventDefault(); last.focus(); }
        else if (!ev.shiftKey && document.activeElement === last) { ev.preventDefault(); first.focus(); }
        return;
      }
      if (cfg.onKeyDown) cfg.onKeyDown(ev, close);
    }

    var resolve = function () {};
    var promise = new Promise(function (res) { resolve = res; });

    document.addEventListener('keydown', onKey, true);
    root.onmousedown = function (ev) { if (ev.target === root) close(null); };

    var target = cfg.focus
      || modal.querySelector('[data-autofocus]')
      || modal.querySelector('input:not([type="hidden"]), textarea, select, button');
    if (target) setTimeout(function () { try { target.focus(); } catch (e) {} }, 0);

    return { root: root, modal: modal, body: bodyEl, foot: footEl, close: close, closed: promise };
  }

  /**
   * 二次确认框。返回 Promise<{confirmed, values}>。
   * cfg: {title, message, detail, confirmLabel, danger, checkboxes:[{id,label,checked}], requireText}
   */
  function confirmDialog(cfg) {
    var c = cfg || {};
    var boxes = (c.checkboxes || []).map(function (b) {
      return h('label', { class: 'check' },
        h('input', { type: 'checkbox', checked: !!b.checked, dataset: { opt: b.id } }),
        h('span', { text: b.label })
      );
    });

    var textNeeded = c.requireText || null;
    var textInput = null;
    if (textNeeded) {
      textInput = h('input', {
        class: 'input mono', type: 'text', placeholder: textNeeded,
        'aria-label': '请输入 ' + textNeeded + ' 以确认', dataset: { opt: 'requireText' }
      });
    }

    var body = [];
    if (c.message) body.push(h('div', { class: 'modal-message', text: c.message }));
    if (c.detail) body.push(h('pre', { class: 'modal-detail', text: c.detail }));
    if (boxes.length) body.push(h('div', { class: 'form-actions' }, boxes));
    if (textInput) {
      body.push(h('label', { class: 'field' },
        h('span', { class: 'field-label', text: '请输入 ' + textNeeded + ' 确认' }),
        textInput
      ));
    }

    var confirmBtn = h('button', {
      class: 'btn ' + (c.danger === false ? 'btn-primary' : 'btn-danger'),
      type: 'button', 'data-autofocus': true,
      title: '确认执行该操作', 'aria-label': '确认执行',
      text: c.confirmLabel || '确认',
      onclick: function () {
        if (textNeeded && textInput && textInput.value.trim() !== textNeeded) {
          toast('warn', '确认文本不匹配，操作已取消。');
          textInput.focus();
          return;
        }
        var values = {};
        (c.checkboxes || []).forEach(function (b) {
          var el = handle.body.querySelector('[data-opt="' + b.id + '"]');
          values[b.id] = !!(el && el.checked);
        });
        handle.close({ confirmed: true, values: values });
      }
    });

    var cancelBtn = h('button', {
      class: 'btn', type: 'button', title: '取消 (Esc)', 'aria-label': '取消',
      text: '取消', onclick: function () { handle.close({ confirmed: false, values: {} }); }
    });

    var handle = mountModal({
      title: c.title || '确认操作',
      danger: c.danger !== false,
      body: body,
      foot: [h('span', { class: 'modal-foot-spacer' }), cancelBtn, confirmBtn]
    });

    return handle.closed.then(function (r) { return r || { confirmed: false, values: {} }; });
  }

  function showTextModal(title, text, opts) {
    var o = opts || {};
    var pre = h('pre', { class: 'modal-detail', text: String(text) });
    var copy = h('button', {
      class: 'btn btn-sm', type: 'button', title: '复制到剪贴板', 'aria-label': '复制内容',
      text: '复制', onclick: function () { copyText(String(text)); }
    });
    var handle = mountModal({
      title: title,
      wide: o.wide !== false,
      body: [pre],
      foot: [copy, h('span', { class: 'modal-foot-spacer' }),
        h('button', {
          class: 'btn', type: 'button', title: '关闭 (Esc)', 'aria-label': '关闭',
          text: '关闭', onclick: function () { handle.close(null); }
        })]
    });
    return handle.closed;
  }

  function copyText(text) {
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        navigator.clipboard.writeText(text).then(
          function () { toast('success', '已复制到剪贴板。'); },
          function (e) { fail('复制失败', e); }
        );
      } else {
        toast('warn', '当前环境不支持剪贴板 API。');
      }
    } catch (e) { fail('复制失败', e); }
  }

  function showErrorsModal() {
    if (!state.errors.length) { toast('info', '暂无错误记录。'); return; }
    var rows = state.errors.map(function (e) {
      return h('div', { class: 'modal-detail' },
        h('div', { text: '[' + fmtTime(e.time) + '] ' + e.context + (e.count > 1 ? '（×' + e.count + '）' : '') }),
        h('div', { text: e.message }),
        e.detail ? h('div', { class: 'faint', text: e.detail }) : null
      );
    });
    var handle = mountModal({
      title: '错误记录（' + state.errors.length + '）',
      wide: true,
      body: rows,
      foot: [
        h('button', {
          class: 'btn btn-danger-ghost', type: 'button', title: '清空错误记录',
          'aria-label': '清空错误记录', text: '清空记录',
          onclick: function () {
            state.errors = [];
            var btn = $('status-err'); if (btn) btn.hidden = true;
            handle.close(null);
          }
        }),
        h('span', { class: 'modal-foot-spacer' }),
        h('button', {
          class: 'btn', type: 'button', title: '关闭 (Esc)', 'aria-label': '关闭',
          text: '关闭', onclick: function () { handle.close(null); }
        })
      ]
    });
  }

  /* ========================== 7. 弹出菜单 ========================== */

  var openMenuHandle = null;
  var menuKeyHandler = null;

  function closeMenu() {
    var root = $('menu-root');
    if (menuKeyHandler) {
      document.removeEventListener('keydown', menuKeyHandler, true);
      menuKeyHandler = null;
    }
    if (!root) return;
    root.hidden = true;
    clear(root);
    if (openMenuHandle) {
      var prev = openMenuHandle.anchor;
      if (prev && prev.setAttribute) prev.setAttribute('aria-expanded', 'false');
      openMenuHandle = null;
      if (prev && typeof prev.focus === 'function' && prev.isConnected) prev.focus();
    }
  }

  function openMenu(anchor, items) {
    closeMenu();
    var root = $('menu-root');
    var menu = h('div', { class: 'menu', role: 'menu', 'aria-label': '操作菜单' });
    var entries = [];

    items.forEach(function (it) {
      if (!it) return;
      if (it.type === 'separator') { menu.appendChild(h('div', { class: 'menu-sep' })); return; }
      if (it.type === 'label') { menu.appendChild(h('div', { class: 'menu-label', text: it.label })); return; }
      var btn = h('button', {
        type: 'button', role: 'menuitem',
        class: it.danger ? 'is-danger' : '',
        title: it.title || it.label,
        'aria-label': it.title || it.label,
        disabled: !!it.disabled,
        text: it.label,
        onclick: function () {
          var fn = it.onSelect;
          closeMenu();
          if (fn) fn();
        }
      });
      entries.push(btn);
      menu.appendChild(btn);
    });

    var scrim = h('div', { class: 'menu-scrim', onclick: function () { closeMenu(); } });
    root.appendChild(scrim);
    root.appendChild(menu);
    root.hidden = false;

    /* 定位：默认在锚点左下方，越界则翻转。 */
    var r = anchor.getBoundingClientRect();
    var mw = menu.offsetWidth, mh = menu.offsetHeight;
    var left = Math.max(6, Math.min(r.left, window.innerWidth - mw - 6));
    var top = r.bottom + 4;
    if (top + mh > window.innerHeight - 6) top = Math.max(6, r.top - mh - 4);
    menu.style.left = left + 'px';
    menu.style.top = top + 'px';

    openMenuHandle = { anchor: anchor };
    if (anchor.setAttribute) anchor.setAttribute('aria-expanded', 'true');

    menuKeyHandler = function (ev) {
      if (ev.key === 'Escape') { ev.preventDefault(); ev.stopPropagation(); closeMenu(); return; }
      if (ev.key === 'Tab') { closeMenu(); return; }
      if (ev.key === 'ArrowDown' || ev.key === 'ArrowUp') {
        ev.preventDefault();
        var enabled = entries.filter(function (b) { return !b.disabled; });
        if (!enabled.length) return;
        var idx = enabled.indexOf(document.activeElement);
        var next = ev.key === 'ArrowDown'
          ? (idx + 1) % enabled.length
          : (idx <= 0 ? enabled.length - 1 : idx - 1);
        enabled[next].focus();
      }
    };
    document.addEventListener('keydown', menuKeyHandler, true);

    var first = entries.filter(function (b) { return !b.disabled; })[0];
    if (first) setTimeout(function () { first.focus(); }, 0);
  }

  /* ========================== 8. 表格状态行 ========================== */

  function stateRow(colspan, kind, opts) {
    var o = opts || {};
    var icon = kind === 'loading' ? null : kind === 'error' ? '⛔' : kind === 'empty' ? '∅' : 'ℹ';
    var kids = [];
    if (kind === 'loading') {
      kids.push(h('span', { class: 'spinner', 'aria-hidden': 'true' }));
      kids.push(h('span', { class: 'state-title', text: o.title || '加载中…' }));
    } else {
      kids.push(h('span', { class: 'state-icon', 'aria-hidden': 'true', text: icon }));
      kids.push(h('span', { class: 'state-title', text: o.title || (kind === 'error' ? '加载失败' : '暂无数据') }));
    }
    if (o.message) kids.push(h('div', { class: 'state-msg', text: o.message }));
    if (o.detail) kids.push(h('pre', { class: 'state-detail', text: o.detail }));
    if (o.retry) {
      kids.push(h('div', { class: 'state-actions' },
        h('button', {
          class: 'btn', type: 'button', title: '重新加载', 'aria-label': '重新加载',
          text: '重试', onclick: o.retry
        })
      ));
    }
    return h('tr', { class: 'state-row' },
      h('td', { colspan: String(colspan) }, h('div', { class: 'state', role: kind === 'loading' ? 'status' : 'note' }, kids))
    );
  }

  function renderLoadedState(tbody, key, colspan, retry) {
    if (state.loading[key]) {
      setChildren(tbody, [stateRow(colspan, 'loading', { title: '加载中…' })]);
      return true;
    }
    if (state.error[key]) {
      var e = state.error[key];
      setChildren(tbody, [stateRow(colspan, 'error', {
        title: '加载失败', message: e.message, detail: e.detail, retry: retry
      })]);
      return true;
    }
    return false;
  }

  function setCount(id, n, unit) {
    var el = $(id);
    if (el) el.textContent = n + ' ' + (unit || '项');
  }

  function setTabCount(view, n) {
    /* no-op: tab-count pill removed from UI */
  }

  /* ========================== 9. 视图切换 ========================== */

  function viewAge(name) {
    return state.loadedAt[name] ? (Date.now() - state.loadedAt[name]) : Infinity;
  }

  /* 每个面板都能从当前缓存状态重绘：切回标签页时保证已加载的数据一定可见。 */
  function renderPanel(panel) {
    var renderers = {
      containers: renderContainers,
      images: renderImages,
      sessions: renderSessions,
      storage: renderStorage,
      volumes: renderVolumes,
      networks: renderNetworks,
      env: renderEnv,
      settings: renderSettings,
      tasks: renderTasks
    };
    var fn = renderers[panel];
    if (fn) fn();
  }

  /* 面板键 -> 面板元素（单面板分组复用 view- 分组，双面板分组用 panel-）。 */
  function panelEl(panel) {
    if (panel === 'containers' || panel === 'images' || panel === 'tasks') {
      return $('view-' + panel);
    }
    return $('panel-' + panel);
  }

  function selectView(view, opts) {
    if (VIEWS.indexOf(view) < 0) view = 'containers';
    var panels = GROUPS[view] || [view];
    if (!SUB[view] || panels.indexOf(SUB[view]) < 0) SUB[view] = panels[0];
    var active = SUB[view];
    state.view = view;

    VIEWS.forEach(function (v) {
      var tab = $('tab-' + v);
      var sec = $('view-' + v);
      var on = (v === view);
      if (tab) {
        tab.setAttribute('aria-selected', on ? 'true' : 'false');
        tab.tabIndex = on ? 0 : -1;
      }
      if (sec) sec.hidden = !on;
    });

    /* 分组内：把面板 hidden 与分组可见性合并计算 ——
       一个面板只有在「它的分组是当前选中的分组」且「它本身是
       当前子面板」时才可见；否则一律隐藏。这样切走一个分组后，
       该分组内所有面板都跟着隐藏，不会出现多个 section 同时可见。 */
    Object.keys(GROUPS).forEach(function (v) {
      GROUPS[v].forEach(function (panel) {
        var el = panelEl(panel);
        if (el) el.hidden = !(v === view && panel === active);
      });
    });

    /* 二级标签的选中态 */
    Object.keys(GROUPS).forEach(function (v) {
      if (!GROUPS[v] || GROUPS[v].length < 2) return;
      GROUPS[v].forEach(function (panel) {
        var btn = $('subtab-' + v + '-' + panel);
        if (btn) btn.setAttribute('aria-selected', (v === view && panel === active) ? 'true' : 'false');
      });
    });

    try {
      var hash = '#/' + view;
      if (SUB[view] !== panels[0]) hash += '/' + SUB[view];
      if (location.hash !== hash) history.replaceState(null, '', hash);
    } catch (e) { /* file:// 下可能受限，忽略 */ }
    if (!opts || opts.force !== false) loadPanel(active);
    /* 数据可能是后台（启动/轮询/事件）加载的，切页时必须重绘一次，
       否则会看到一张空表 —— 例如任务视图。 */
    renderPanel(active);
    updateStatusLine();
  }

  /* 切换分组内的子视图（二级标签页）。 */
  function selectSubView(view, panel) {
    if (!GROUPS[view] || GROUPS[view].indexOf(panel) < 0) return;
    if (state.view !== view) {
      SUB[view] = panel;
      selectView(view);
      return;
    }
    SUB[view] = panel;
    GROUPS[view].forEach(function (p) {
      var el = panelEl(p);
      if (el) el.hidden = (p !== panel);
    });
    GROUPS[view].forEach(function (p) {
      var btn = $('subtab-' + view + '-' + p);
      if (btn) btn.setAttribute('aria-selected', p === panel ? 'true' : 'false');
    });
    loadPanel(panel);
    renderPanel(panel);
    updateStatusLine();
  }

  function loadPanel(panel, force) {
    var loaders = {
      containers: loadContainers,
      images: loadImages,
      sessions: loadSessions,
      storage: loadStorage,
      volumes: loadVolumes,
      networks: loadNetworks,
      env: loadEnv,
      tasks: loadTasks,
      settings: loadSettings
    };
    var fn = loaders[panel];
    if (!fn) return;
    if (force) return forcePanel(fn, panel);
    if (!state.loaded[panel] && viewAge(panel) >= VIEW_STALE_MS) return fn();
    if (state.loaded[panel]) return;
    return fn();
  }

  /* 强制重取某面板数据（绕过 STALE 缓存判断）。 */
  function forcePanel(fn, panel) {
    if (!fn) return Promise.resolve();
    var prev = state.loaded[panel];
    state.loaded[panel] = false;
    return Promise.resolve(fn()).finally(function () { state.loaded[panel] = prev; });
  }

  /* 刷新当前子面板（F5）。 */
  function refreshCurrentView() {
    var btn = $('btn-refresh');
    if (btn) btn.disabled = true;
    var panel = currentPanel();
    var p = loadPanel(panel, true);
    Promise.resolve(p).then(
      function () { toast('info', '已刷新：' + PANEL_LABELS[panel]); },
      function () { /* 错误已由面板内呈现 */ }
    ).then(function () { if (btn) btn.disabled = false; });
  }

  function viewLabel(name) {
    return VIEW_LABELS[name] || PANEL_LABELS[name] || name;
  }

  /* 当前正在显示的面板键：单面板分组就是分组名本身。 */
  function currentPanel() {
    return SUB[state.view] || (GROUPS[state.view] || [])[0] || state.view;
  }

  function updateStatusLine() {
    var panel = currentPanel();
    var parts = [];
    if (panel === 'containers') {
      var running = state.containers.filter(function (c) { return containerState(c) === 'running'; }).length;
      parts.push('容器 ' + state.containers.length + '（运行 ' + running + ' / 退出 ' + (state.containers.length - running) + '）');
    } else if (panel === 'images') parts.push('镜像 ' + state.images.length);
    else if (panel === 'sessions') parts.push('会话 ' + state.sessions.length);
    else if (panel === 'storage') {
      var total = state.storage.reduce(function (n, s) { return n + (Number(s.BytesOnDisk) || 0); }, 0);
      parts.push('会话存储 ' + state.storage.length + '（合计 ' + formatBytesJS(total) + '）');
    }
    else if (panel === 'volumes') parts.push('卷 ' + state.volumes.length);
    else if (panel === 'networks') parts.push('网络 ' + state.networks.length);
    else if (panel === 'tasks') {
      var act = state.tasks.filter(function (t) { return String(t.State).toLowerCase() === 'running'; }).length;
      parts.push('任务 ' + state.tasks.length + '（进行中 ' + act + '）');
    } else if (panel === 'env') {
      parts.push('环境自检：' + (state.env ? (state.env.ServiceReady ? '就绪' : '不可用') : '未检测'));
    }
    if (state.logs) parts.push('日志流 ' + shortId(state.logs.streamID, 8));
    if (state.term) parts.push('终端 ' + shortId(state.term.streamID, 8));
    setStatus(parts.join(' · ') || '就绪');
  }

  /* ========================== 10. 容器视图 ========================== */

  async function loadContainers() {
    var key = 'containers';
    state.loading[key] = true;
    state.error[key] = null;
    renderContainers();
    try {
      var filter = {
        All: state.ct.scope === 'all',
        Query: state.query,
        State: state.ct.scope === 'running' ? 'running' : '',
        Limit: 0
      };
      var list = await invoke('ListContainers', filter);
      state.containers = Array.isArray(list) ? list : [];
      if (state.ct.stats) await loadStats();
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      setTabCount('containers', state.containers.length);
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.containers = [];
      fail('ListContainers', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      renderContainers();
      updateStatusLine();
    }
  }

  async function loadStats() {
    try {
      var list = await invoke('ContainerStats', true);
      applyStats(list);
    } catch (e) {
      state.ct.statsMap = null;
      toast('warn', '资源占用数据不可用：' + normalizeError(e).message);
    }
  }

  function applyStats(list) {
    var map = {};
    (Array.isArray(list) ? list : []).forEach(function (s) {
      ['ID', 'Name'].forEach(function (k) {
        var v = s && s[k];
        if (!v) return;
        map[String(v)] = s;
        map[shortId(v)] = s;
      });
    });
    state.ct.statsMap = map;
  }

  function findStat(c) {
    var map = state.ct.statsMap;
    if (!map) return null;
    var keys = [c.ID, shortId(c.ID), (c.Names && c.Names[0]) || '', String((c.Names && c.Names[0]) || '').replace(/^\//, '')];
    for (var i = 0; i < keys.length; i++) {
      if (keys[i] && map[keys[i]]) return map[keys[i]];
    }
    return null;
  }

  function visibleContainers() {
    var q = state.query.trim().toLowerCase();
    return state.containers.filter(function (c) {
      var st = containerState(c);
      if (state.ct.scope === 'running' && st !== 'running') return false;
      if (!q) return true;
      var names = (c.Names || []).join(' ').toLowerCase();
      return names.indexOf(q) >= 0
        || String(c.Image || '').toLowerCase().indexOf(q) >= 0
        || String(c.ID || '').toLowerCase().indexOf(q) >= 0
        || String(c.State || '').toLowerCase().indexOf(q) >= 0;
    });
  }

  function containerAliases(c) {
    var out = [];
    if (c.ID) { out.push(c.ID); out.push(shortId(c.ID)); }
    (c.Names || []).forEach(function (n) {
      out.push(n);
      out.push(String(n).replace(/^\//, ''));
    });
    return out.filter(Boolean);
  }

  function containerName(c) {
    var n = (c.Names && c.Names[0]) || '';
    n = String(n).replace(/^\//, '');
    return n || shortId(c.ID);
  }

  function rowActionButton(act, label, title, cls, data) {
    return h('button', {
      class: 'btn btn-row' + (cls ? ' ' + cls : ''),
      type: 'button', title: title, 'aria-label': title,
      dataset: Object.assign({ act: act }, data || {}),
      text: label
    });
  }

  function renderContainers() {
    var tbody = $('ct-body');
    if (!tbody) return;
    var cols = 6 + (state.ct.stats ? 2 : 0) + 1;

    var statCols = document.querySelectorAll('[data-stats-col]');
    for (var i = 0; i < statCols.length; i++) statCols[i].hidden = !state.ct.stats;

    if (renderLoadedState(tbody, 'containers', cols, function () { loadContainers(); })) {
      setCount('ct-count', 0, '个');
      return;
    }

    var rows = visibleContainers();
    setCount('ct-count', rows.length, '个');

    if (!rows.length) {
      setChildren(tbody, [stateRow(cols, 'empty', {
        title: state.query ? '没有匹配的容器' : (state.ct.scope === 'running' ? '没有运行中的容器' : '没有容器'),
        message: state.query
          ? '当前搜索词：' + state.query + '。清空搜索框可看到全部容器。'
          : '使用「运行容器…」从镜像创建，或在 WSL 中执行 wslc container run。',
        retry: function () { loadContainers(); }
      })]);
      return;
    }

    var frag = document.createDocumentFragment();
    rows.forEach(function (c) {
      var st = containerState(c);
      var cls = stateClass(st);
      var ids = containerAliases(c);
      var stat = state.ct.stats ? findStat(c) : null;

      var cells = [
        h('td', { class: 'col-name' },
          h('div', { class: 'cell-name' },
            h('span', { class: 'dot dot-' + cls, title: '状态：' + (c.State || c.Status || '未知'), 'aria-hidden': 'true' }),
            h('span', { class: 'name', title: containerName(c), text: containerName(c) }),
            h('span', { class: 'sid mono', text: shortId(c.ID, 12) })
          )
        ),
        h('td', { class: 'mono', title: c.Image || '', text: c.Image || '—' }),
        h('td', {},
          h('span', { class: 'badge badge-' + cls, title: (c.Status || '') + ' | State=' + (c.State || ''), text: c.Status || statusLabel(st) })
        ),
        h('td', { class: 'mono', title: (c.Ports || []).join(', '), text: (c.Ports && c.Ports.length) ? c.Ports.join(', ') : '—' }),
        h('td', { class: 'muted', title: c.CreatedAt || '', text: c.CreatedAt || c.RunningFor || '—' })
      ];

      if (state.ct.stats) {
        cells.push(h('td', { class: 'col-stat', text: stat ? (stat.CPUPerc || '—') : '—' }));
        cells.push(h('td', { class: 'col-stat', title: stat ? (stat.MemUsage || '') : '', text: stat ? (stat.MemUsage || '—') : '—' }));
      }

      var actions = h('div', { class: 'cell-actions' });
      if (st === 'running') {
        actions.appendChild(rowActionButton('stop', '停止', '停止容器 ' + containerName(c), '', null));
      } else {
        actions.appendChild(rowActionButton('start', '启动', '启动容器 ' + containerName(c), 'is-primary', null));
      }
      actions.appendChild(rowActionButton('restart', '重启', '重启容器 ' + containerName(c), '', null));
      actions.appendChild(rowActionButton('logs', '日志', '查看容器 ' + containerName(c) + ' 的日志', '', null));
      actions.appendChild(rowActionButton('terminal', '终端', '在容器 ' + containerName(c) + ' 中打开终端', '', null));
      actions.appendChild(h('button', {
        class: 'btn btn-row', type: 'button',
        title: '更多操作：详情 / 强制终止 / 删除',
        'aria-label': '容器 ' + containerName(c) + ' 的更多操作',
        'aria-haspopup': 'menu', 'aria-expanded': 'false',
        text: '⋯',
        onclick: function (ev) { openContainerMenu(ev.currentTarget, c); }
      }));
      cells.push(h('td', { class: 'col-actions' }, actions));

      var tr = h('tr', {
        tabindex: '0',
        dataset: { ref: c.ID || containerName(c), name: containerName(c) },
        title: '按 Enter 查看详情，Shift+F10 打开操作菜单'
      }, cells);
      frag.appendChild(tr);
    });
    setChildren(tbody, [frag]);
  }

  function openContainerMenu(anchor, c) {
    var st = containerState(c);
    var ref = c.ID || containerName(c);
    openMenu(anchor, [
      { type: 'label', label: containerName(c) },
      { label: '详情 (inspect)', title: '查看容器 JSON 详情', onSelect: function () { inspectContainer(ref); } },
      { label: '统计 (stats)', title: '查看该容器资源占用', onSelect: function () { showContainerStats(ref); } },
      { type: 'separator' },
      { label: '复制 ID', title: '复制容器 ID', onSelect: function () { copyText(c.ID || ''); } },
      { label: '复制名称', title: '复制容器名称', onSelect: function () { copyText(containerName(c)); } },
      { type: 'separator' },
      {
        label: '强制终止 (kill)', danger: true, disabled: st !== 'running',
        title: st === 'running' ? '向容器发送 SIGKILL（破坏性，需确认）' : '容器未在运行',
        onSelect: function () { killContainer(ref, containerName(c)); }
      },
      { label: '删除容器…', danger: true, title: '删除容器（破坏性，需确认）', onSelect: function () { removeContainer(ref, containerName(c)); } }
    ]);
  }

  async function containerAction(ref, name, verb, fn) {
    var row = null;
    try {
      row = document.querySelector('#ct-body tr[data-ref="' + (window.CSS && CSS.escape ? CSS.escape(ref) : ref) + '"]');
    } catch (e) { row = null; }
    if (row) row.classList.add('is-busy');
    try {
      var out = await fn();
      var text = out === null || out === undefined ? '' : String(out);
      toast('success', verb + '成功：' + (name || shortId(ref)), text.trim().slice(0, 400));
      return true;
    } catch (e) {
      fail(verb, e);
      return false;
    } finally {
      if (row) row.classList.remove('is-busy');
      await loadContainers();
    }
  }

  async function killContainer(ref, name) {
    var r = await confirmDialog({
      title: '强制终止容器',
      message: '将向容器「' + name + '」发送 SIGKILL 信号，进程会被立即终止，未落盘的数据可能丢失。',
      detail: 'KillContainer(ref=' + ref + ', signal=SIGKILL)',
      confirmLabel: '强制终止',
      danger: true
    });
    if (!r.confirmed) return;
    await containerAction(ref, name, '强制终止', function () { return invoke('KillContainer', ref, 'SIGKILL'); });
  }

  async function removeContainer(ref, name) {
    var r = await confirmDialog({
      title: '删除容器',
      message: '将删除容器「' + name + '」。此操作不可撤销。',
      detail: 'RemoveContainer(ref=' + ref + ')',
      confirmLabel: '删除',
      danger: true,
      checkboxes: [
        { id: 'force', label: '强制删除运行中的容器 (-f)', checked: false },
        { id: 'volumes', label: '同时删除其匿名卷 (-v)', checked: false }
      ]
    });
    if (!r.confirmed) return;
    await containerAction(ref, name, '删除', function () {
      return invoke('RemoveContainer', ref, !!r.values.force, !!r.values.volumes);
    });
  }

  async function inspectContainer(ref) {
    try {
      var raw = await invoke('InspectContainer', ref);
      showTextModal('容器详情 · ' + ref, pretty(raw));
    } catch (e) { fail('InspectContainer', e); }
  }

  async function showContainerStats(ref) {
    try {
      var list = await invoke('ContainerStats', false);
      var hit = (Array.isArray(list) ? list : []).filter(function (s) {
        return sameRef(s.ID, ref) || sameRef(s.Name, ref);
      });
      if (!hit.length) { toast('info', '该容器当前没有统计输出（可能未运行）。'); return; }
      showTextModal('容器统计 · ' + ref, JSON.stringify(hit, null, 2));
    } catch (e) { fail('ContainerStats', e); }
  }

  function pretty(raw) {
    if (raw === null || raw === undefined) return '(空)';
    if (typeof raw === 'string') {
      var s = raw.trim();
      if (s && (s[0] === '{' || s[0] === '[')) {
        try { return JSON.stringify(JSON.parse(s), null, 2); } catch (e) { return raw; }
      }
      return raw;
    }
    try { return JSON.stringify(raw, null, 2); } catch (e) { return String(raw); }
  }

  /* ---------- 运行容器对话框 ---------- */

  function fieldRow(label, input, opts) {
    var o = opts || {};
    return h('label', { class: 'field' + (o.full ? ' field-full' : '') },
      h('span', { class: 'field-label' }, label, o.required ? h('em', { class: 'req', text: ' *' }) : null),
      input
    );
  }

  function inputEl(id, placeholder, o) {
    o = o || {};
    return h('input', {
      class: 'input' + (o.mono ? ' mono' : ''),
      id: id, type: o.type || 'text', placeholder: placeholder,
      title: o.title || placeholder || id, 'aria-label': o.aria || placeholder || id
    });
  }

  /* 从 state.images 组装下拉选项。
     wslc 的 image list 返回**扁平列表**（每行一个 Repository+Tag），没有
     RepositoryTags 字段——所以这里先把扁平行按 Repository 聚合成 optgroup。
     - Repository 非空 + Tag 非空且不是 <none> → "repo:tag"
     - Repository 非空 + Tag 空/<none>        → 只用 "repo"（避免写出 repo:<none>）
     - Repository 为空（dangling 镜像）        → 用短 ID 引用
     每个 optgroup 最多列 5 个 tag，超过折叠，避免 200 tag 的库撑爆下拉。 */
  function buildImageOptions(images) {
    var list = Array.isArray(images) ? images.slice() : [];
    var groups = {};
    var order = [];
    for (var i = 0; i < list.length; i++) {
      var img = list[i];
      var repo = String(img.Repository || '').trim();
      var tag = String(img.Tag || '').trim();
      var key;
      var ref;
      // 与 imageRef() 保持一致：wslc 对悬空镜像把 Repository 填成 "<none>"，
      // 这种行必须按 image ID 走，否则下拉里会出现 "<none>" / "<none>:<none>"
      // 这样的非法引用，后续 inspect/save 都会报 invalid reference format。
      var noRepo = !repo || repo === '<none>' || repo === 'none';
      if (noRepo) {
        var id = shortId(img.ID);
        if (!id) continue;
        key = '__local__' + id;
        ref = id;
      } else {
        key = repo;
        ref = (tag && tag !== '<none>' && tag !== 'none') ? repo + ':' + tag : repo;
      }
      if (!groups[key]) { groups[key] = { repo: repo, items: [] }; order.push(key); }
      var g = groups[key];
      var dup = false;
      for (var j = 0; j < g.items.length; j++) {
        if (g.items[j].ref === ref) { dup = true; break; }
      }
      if (dup) continue;
      var sizeTxt = img.Size ? String(img.Size) : '';
      g.items.push({ ref: ref, size: sizeTxt });
    }
    order.sort(function (a, b) {
      var la = a.toLowerCase();
      var lb = b.toLowerCase();
      return la < lb ? -1 : (la > lb ? 1 : 0);
    });
    var maxPerRepo = 5;
    return order.map(function (key) {
      var g = groups[key];
      var label = g.repo || '本地镜像（无仓库名）';
      var children = [];
      if (g.items.length <= maxPerRepo) {
        for (var k = 0; k < g.items.length; k++) {
          var it = g.items[k];
          children.push(h('option', {
            value: it.ref,
            text: it.ref + (it.size ? '  ·  ' + it.size : '')
          }));
        }
      } else {
        for (var m = 0; m < maxPerRepo; m++) {
          var it2 = g.items[m];
          children.push(h('option', {
            value: it2.ref,
            text: it2.ref + (it2.size ? '  ·  ' + it2.size : '')
          }));
        }
        children.push(h('option', {
          value: '',
          disabled: true,
          text: '… 还有 ' + (g.items.length - maxPerRepo) + ' 个标签（切手动输入可完整列出）'
        }));
      }
      return h('optgroup', { label: label + '（' + g.items.length + '）' }, children);
    });
  }

  /* 更新运行容器弹窗的镜像下拉。必须传入真实的 select 元素：弹窗未挂载前
     imageSelect 不在 DOM 里，$('run-image-select') 会命中 index.html 里给
     selfcheck 用的 hidden 占位 span，导致下拉永远填不进选项。
     images 缓存缺失时用占位提示，不阻塞弹窗。
     占位符的文案会根据加载状态切换：正在加载时提示"加载中…"，
     加载完成后才显示"本机暂无镜像"，避免用户误以为下拉为空。 */
  function refreshRunImageOptions(sel) {
    if (!sel) return;
    var imgs = state.images || [];
    var nodes = [];
    if (!imgs.length) {
      var loading = state.loading.images;
      var text = loading
        ? '（正在加载本机镜像…）'
        : '（本机暂无镜像，请切到手动输入或先到「镜像」页拉取）';
      nodes.push(h('option', { value: '', text: text, disabled: true }));
    } else {
      nodes = nodes.concat(buildImageOptions(imgs));
    }
    setChildren(sel, nodes);
  }

  function runContainerDialog() {
    /* 镜像字段：优先下拉已有镜像（来自本地 image list 缓存），
       可切回手动输入以拉取本地没有的镜像。 */
    var imageModes = ['select', 'input'];
    var imageMode = 'select';
    var imageInput = inputEl('run-image-input', 'nginx:latest', { mono: true, title: '镜像引用（必填）', aria: '镜像引用' });
    var imageSelect = h('select', {
      class: 'input', id: 'run-image-select', 'aria-label': '镜像（已有）', title: '选择本机已有镜像',
      onchange: function () {
        var sel = imageSelect.options[imageSelect.selectedIndex];
        if (sel && sel.dataset.note) imageNote.textContent = sel.dataset.note;
      }
    });
    var imageNote = h('span', { class: 'faint', style: 'margin-left:8px; font-size:var(--fs-xs);' });
    var imageModeSwitch = h('button', {
      class: 'btn btn-sm', type: 'button',
      title: '在「下拉已有镜像」和「手动输入」之间切换',
      'aria-label': '切换镜像输入方式',
      text: '手动输入',
      onclick: function () {
        imageMode = imageMode === 'select' ? 'input' : 'select';
        imageInput.hidden = imageMode !== 'input';
        imageSelect.hidden = imageMode !== 'select';
        imageModeSwitch.textContent = imageMode === 'select' ? '手动输入' : '下拉已有';
        if (imageMode === 'select') refreshRunImageOptions(imageSelect);
      }
    });

    var els = {
      image: { get value() { return imageMode === 'select' ? (imageSelect.value || '') : imageInput.value; } },
      name: inputEl('run-name', '（可选）my-container', { mono: true, aria: '容器名称' }),
      command: inputEl('run-command', '（可选）/bin/sh -c "echo hi"', { mono: true, aria: '容器命令' }),
      ports: inputEl('run-ports', '8080:80, 127.0.0.1:5000:5000', { mono: true, aria: '端口映射' }),
      volumes: inputEl('run-volumes', 'vol:/data\\nC:\\\\src:/app', { mono: true, aria: '卷挂载' }),
      envs: inputEl('run-env', 'TZ=UTC, MODE=dev', { mono: true, aria: '环境变量' }),
      network: inputEl('run-network', '（可选）bridge', { mono: true, aria: '网络' }),
      workdir: inputEl('run-workdir', '（可选）/app', { mono: true, aria: '工作目录' }),
      user: inputEl('run-user', '（可选）1000:1000', { mono: true, aria: '用户' }),
      hostname: inputEl('run-hostname', '（可选）dev-box', { mono: true, aria: '主机名' }),
      memory: inputEl('run-memory', '（可选）512m', { mono: true, aria: '内存限制' }),
      cpus: inputEl('run-cpus', '（可选）1.5', { mono: true, aria: 'CPU 限制' }),
      entrypoint: inputEl('run-entrypoint', '（可选）', { mono: true, aria: '入口点' }),
      labels: inputEl('run-labels', 'app=demo, tier=dev', { mono: true, aria: '标签' }),
      pull: h('select', { class: 'input', id: 'run-pull', 'aria-label': '镜像拉取策略', title: '镜像拉取策略' },
        h('option', { value: '', text: '默认' }),
        h('option', { value: 'missing', text: 'missing（缺失时拉取）' }),
        h('option', { value: 'always', text: 'always（总是拉取）' }),
        h('option', { value: 'never', text: 'never（从不拉取）' })
      ),
      detach: h('input', { type: 'checkbox', checked: true, id: 'run-detach' }),
      remove: h('input', { type: 'checkbox', id: 'run-remove' }),
      tty: h('input', { type: 'checkbox', id: 'run-tty' })
    };
    imageInput.hidden = true;
    // 注意：不能在这里调用 refreshRunImageOptions(imageSelect)——imageSelect
    // 此时还没挂进 DOM，$('run-image-select') 会命中 index.html 里的 hidden
    // 占位 span。首屏渲染放到 mountModal 之后（见下方）。

    var errBox = h('pre', { class: 'modal-detail', hidden: true });

    var imageField = h('div', { style: 'display:flex; gap:8px; align-items:center;' },
      h('div', { class: 'grow', style: 'display:flex; gap:8px; align-items:center; min-width:0;' },
        imageSelect,
        imageInput,
        imageNote
      ),
      imageModeSwitch
    );

    var body = [
      h('div', { class: 'form-grid' },
        fieldRow('镜像', imageField, { required: true, full: true }),
        fieldRow('名称', els.name),
        fieldRow('命令', els.command),
        fieldRow('端口', els.ports, { full: true }),
        fieldRow('卷', els.volumes, { full: true }),
        fieldRow('环境变量', els.envs, { full: true }),
        fieldRow('网络', els.network),
        fieldRow('工作目录', els.workdir),
        fieldRow('用户', els.user),
        fieldRow('主机名', els.hostname),
        fieldRow('内存', els.memory),
        fieldRow('CPU', els.cpus),
        fieldRow('入口点', els.entrypoint),
        fieldRow('标签', els.labels),
        fieldRow('拉取策略', els.pull),
        h('div', { class: 'form-actions' },
          h('label', { class: 'check', title: '后台运行 (-d)' }, els.detach, h('span', { text: '-d 后台' })),
          h('label', { class: 'check', title: '退出后自动删除 (--rm)' }, els.remove, h('span', { text: '--rm' })),
          h('label', { class: 'check', title: '分配 TTY (-t)' }, els.tty, h('span', { text: '-t TTY' }))
        )
      ),
      errBox,
      h('div', { class: 'faint', text: '提示：命令按空白切分（支持引号）；端口/卷/环境变量/标签支持逗号或换行分隔。' })
    ];

    var submitBtn = h('button', {
      class: 'btn btn-primary', type: 'button', title: '创建并运行容器',
      'aria-label': '创建并运行容器', text: '运行',
      onclick: function () { submit(); }
    });

    var handle = mountModal({
      title: '运行新容器',
      wide: true,
      body: body,
      foot: [
        h('span', { class: 'modal-foot-spacer' }),
        h('button', {
          class: 'btn', type: 'button', title: '取消 (Esc)', 'aria-label': '取消',
          text: '取消', onclick: function () { handle.close(null); }
        }),
        submitBtn
      ],
      onKeyDown: function (ev) { if (ev.key === 'Enter' && ev.target && ev.target.tagName === 'INPUT') { ev.preventDefault(); submit(); } }
    });

    /* 首屏回填镜像下拉。必须在 mountModal 之后调用——此时 imageSelect 才
       真正挂进 DOM；mountModal 之前调用会写到 index.html 的 hidden 占位
       span 上，下拉永远填不进选项。 */
    refreshRunImageOptions(imageSelect);

    /* 打开弹窗时刷新一次镜像缓存，把结果回填到下拉。
       如果已经有缓存但超过 30 秒没更新，也会主动刷一次，
       避免用户没打开过「镜像」页时下拉为空。
       这里复用 state.loading.images（而不另设局部标志），
       让 refreshRunImageOptions 能根据加载状态切换占位符文案。
       镜像视图短暂的"加载中"闪烁不影响数据，可接受。 */
    var imagesStale = !state.images || state.images.length === 0 ||
      !state.loadedAt.images || (Date.now() - state.loadedAt.images) > 30000;
    if (imagesStale && hasRuntime()) {
      state.loading.images = true;
      refreshRunImageOptions(imageSelect);   /* 立即显示"正在加载…"占位符 */
      invoke('ListImages', true).then(function (list) {
        state.images = Array.isArray(list) ? list : [];
        state.loaded.images = true;
        state.loadedAt.images = Date.now();
        setTabCount('images', state.images.length);
        refreshRunImageOptions(imageSelect);
        /* 如果此时用户又切到了「镜像」页，那里也会重绘 */
        if (currentPanel() === 'images') renderImages();
      }).catch(function () { /* 忽略；下拉里已有占位提示 */ })
        .then(function () { state.loading.images = false; refreshRunImageOptions(imageSelect); });
    }

    async function submit() {
      var image = (els.image.value || '').trim();
      errBox.hidden = true;
      if (!image) {
        errBox.hidden = false;
        errBox.textContent = '镜像引用不能为空。';
        (imageMode === 'select' ? imageSelect : imageInput).focus();
        return;
      }
      var opts = {
        Image: image,
        Name: els.name.value.trim(),
        Command: splitArgs(els.command.value),
        Detach: !!els.detach.checked,
        Remove: !!els.remove.checked,
        TTY: !!els.tty.checked,
        Env: splitList(els.envs.value),
        Ports: splitList(els.ports.value),
        Volumes: splitList(els.volumes.value),
        Network: els.network.value.trim(),
        WorkDir: els.workdir.value.trim(),
        User: els.user.value.trim(),
        Hostname: els.hostname.value.trim(),
        Memory: els.memory.value.trim(),
        CPUs: els.cpus.value.trim(),
        Entrypoint: els.entrypoint.value.trim(),
        Labels: splitList(els.labels.value),
        Pull: els.pull.value
      };
      submitBtn.disabled = true;
      try {
        var out = await invoke('RunContainer', opts);
        handle.close(null);
        toast('success', '容器已启动。', String(out || '').trim().slice(0, 400));
        await loadContainers();
      } catch (e) {
        var info = recordError('RunContainer', e);
        errBox.hidden = false;
        errBox.textContent = info.message + (info.detail ? '\n' + info.detail : '');
        toast('error', '启动容器失败：' + info.message);
      } finally {
        submitBtn.disabled = false;
      }
    }
  }

  /* ========================== 11. 镜像视图 ========================== */

  async function loadImages() {
    var key = 'images';
    state.loading[key] = true;
    state.error[key] = null;
    renderImages();
    try {
      var all = $('img-all') ? $('img-all').checked : false;
      var list = await invoke('ListImages', all);
      state.images = Array.isArray(list) ? list : [];
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      setTabCount('images', state.images.length);
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.images = [];
      fail('ListImages', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      renderImages();
      updateStatusLine();
    }
  }

  function imageRef(img) {
    var repo = String(img.Repository || '').trim();
    var tag = String(img.Tag || '').trim();
    // 与 internal/domain/types.go Image.Reference() 保持语义一致：
    // wslc 对悬空镜像把 Repository / Tag 都填成字面量 "<none>"，此时 repo + ":" + tag
    // 会拼出 "<none>:<none>"，wslc image inspect 等命令会拒绝这个引用并报
    // "invalid reference format"。这种情况必须回退到 image ID（前 12 位即可，
    // wslc 对所有 image 子命令都接受 ID 前缀）。
    var isNoRepo = !repo || repo === '<none>' || repo === 'none';
    if (isNoRepo) return shortId(img.ID, 12);
    if (!tag || tag === '<none>' || tag === 'none') return repo;
    return repo + ':' + tag;
  }

  function renderImages() {
    var tbody = $('img-body');
    if (!tbody) return;
    var cols = 5;
    if (renderLoadedState(tbody, 'images', cols, function () { loadImages(); })) {
      setCount('img-count', 0, '个');
      return;
    }
    setCount('img-count', state.images.length, '个');
    if (!state.images.length) {
      setChildren(tbody, [stateRow(cols, 'empty', {
        title: '没有镜像',
        message: '在上方输入镜像引用后点「拉取」，或展开「构建镜像」从 Containerfile 构建。',
        retry: function () { loadImages(); }
      })]);
      return;
    }
    var frag = document.createDocumentFragment();
    state.images.forEach(function (img) {
      var ref = imageRef(img);
      var actions = h('div', { class: 'cell-actions' },
        h('button', {
          class: 'btn btn-row', type: 'button', title: '给镜像 ' + ref + ' 打新标签',
          'aria-label': '给镜像 ' + ref + ' 打标签', text: '打标签',
          onclick: function () { tagImageDialog(ref); }
        }),
        h('button', {
          class: 'btn btn-row', type: 'button', title: '查看镜像 ' + ref + ' 的 JSON 详情',
          'aria-label': '查看镜像 ' + ref + ' 详情', text: '详情',
          onclick: function () { inspectImage(ref); }
        }),
        h('button', {
          class: 'btn btn-row is-danger', type: 'button', title: '删除镜像 ' + ref + '（需确认）',
          'aria-label': '删除镜像 ' + ref, text: '删除',
          onclick: function () { removeImage(ref); }
        })
      );

      frag.appendChild(h('tr', {
        tabindex: '0',
        dataset: { ref: ref },
        title: '按 Enter 查看详情'
      },
        h('td', { class: 'col-image' },
          h('button', {
            class: 'ref-link', type: 'button', title: '查看详情：' + ref,
            'aria-label': '查看镜像 ' + ref + ' 详情', text: ref,
            onclick: function () { inspectImage(ref); }
          })
        ),
        h('td', { class: 'mono muted', title: img.ID || '', text: shortId(img.ID, 12) }),
        h('td', { class: 'muted nowrap', title: img.CreatedAt || '', text: img.CreatedSince || img.CreatedAt || '—' }),
        h('td', { class: 'col-size mono nowrap', text: img.Size || '—' }),
        h('td', { class: 'col-actions' }, actions)
      ));
    });
    setChildren(tbody, [frag]);
  }

  async function inspectImage(ref) {
    try {
      var raw = await invoke('InspectImage', ref);
      showTextModal('镜像详情 · ' + ref, pretty(raw));
    } catch (e) { fail('InspectImage', e); }
  }

  function tagImageDialog(source) {
    var src = inputEl('tag-source', '源引用', { mono: true, aria: '源镜像引用' });
    src.value = source;
    var dst = inputEl('tag-target', 'myrepo/myimage:dev', { mono: true, aria: '目标镜像引用' });
    var err = h('pre', { class: 'modal-detail', hidden: true });

    var ok = h('button', {
      class: 'btn btn-primary', type: 'button', title: '打标签 (TagImage)',
      'aria-label': '打标签', text: '打标签',
      onclick: async function () {
        err.hidden = true;
        var a = src.value.trim(), b = dst.value.trim();
        if (!a || !b) {
          err.hidden = false; err.textContent = '源与目标引用都不能为空。';
          (a ? dst : src).focus();
          return;
        }
        ok.disabled = true;
        try {
          var out = await invoke('TagImage', a, b);
          handle.close(null);
          toast('success', '已打标签：' + b, String(out || '').trim().slice(0, 300));
          await loadImages();
        } catch (e) {
          var info = recordError('TagImage', e);
          err.hidden = false; err.textContent = info.message;
          toast('error', '打标签失败：' + info.message);
        } finally { ok.disabled = false; }
      }
    });

    var handle = mountModal({
      title: '打标签 (TagImage)',
      body: [
        h('div', { class: 'form-grid' },
          fieldRow('源引用', src, { required: true, full: true }),
          fieldRow('目标引用', dst, { required: true, full: true })
        ),
        err
      ],
      foot: [
        h('span', { class: 'modal-foot-spacer' }),
        h('button', {
          class: 'btn', type: 'button', title: '取消 (Esc)', 'aria-label': '取消',
          text: '取消', onclick: function () { handle.close(null); }
        }), ok
      ]
    });
  }

  async function removeImage(ref) {
    var r = await confirmDialog({
      title: '删除镜像',
      message: '将删除镜像「' + ref + '」。此操作不可撤销。',
      detail: 'RemoveImage(ref=' + ref + ')',
      confirmLabel: '删除',
      danger: true,
      checkboxes: [{ id: 'force', label: '强制删除 (-f，即使被容器引用)', checked: false }]
    });
    if (!r.confirmed) return;
    try {
      var out = await invoke('RemoveImage', ref, !!r.values.force);
      toast('success', '已删除镜像：' + ref, String(out || '').trim().slice(0, 300));
    } catch (e) { fail('RemoveImage', e); }
    await loadImages();
  }

  async function pullImage() {
    var input = $('img-pull-ref');
    var ref = input ? input.value.trim() : '';
    if (!ref) { toast('warn', '请输入要拉取的镜像引用。'); if (input) input.focus(); return; }
    var btn = $('btn-img-pull');
    if (btn) btn.disabled = true;
    try {
      var taskId = await invoke('PullImage', ref);
      toast('success', '已创建拉取任务：' + shortId(taskId, 12) + '（输出见「任务」视图）');
      if (input) input.value = '';
      await loadTasks();
      openTaskOutput(String(taskId || ''), '拉取 ' + ref, [CH.pull, CH.task]);
    } catch (e) { fail('PullImage', e); }
    finally { if (btn) btn.disabled = false; }
  }

  async function buildImage(ev) {
    if (ev) ev.preventDefault();
    var context = ($('build-context') || {}).value;
    context = (context || '').trim();
    var errEl = $('build-error');
    if (!context) {
      toast('warn', '请填写构建目录。');
      var c = $('build-context'); if (c) c.focus();
      return;
    }
    if (errEl) errEl.hidden = true;
    var opts = {
      Context: context,
      Dockerfile: (($('build-dockerfile') || {}).value || '').trim(),
      Tags: splitList((($('build-tags') || {}).value || '')),
      BuildArgs: [],
      Target: (($('build-target') || {}).value || '').trim(),
      NoCache: !!($('build-nocache') && $('build-nocache').checked),
      Pull: !!($('build-pull') && $('build-pull').checked),
      Labels: [],
      Progress: ''
    };
    var btn = $('btn-img-build');
    if (btn) btn.disabled = true;
    try {
      var taskId = await invoke('BuildImage', opts);
      if (errEl) errEl.hidden = true;
      toast('success', '已创建构建任务：' + shortId(taskId, 12) + '（输出见「任务」视图）');
      await loadTasks();
      openTaskOutput(String(taskId || ''), '构建 ' + context, [CH.build, CH.task]);
    } catch (e) {
      var info = fail('BuildImage', e);
      if (errEl) { errEl.hidden = false; errEl.textContent = info.message + (info.detail ? '\n' + info.detail : ''); }
    }
    finally { if (btn) btn.disabled = false; }
  }

  async function pruneImagesDialog() {
    var r = await confirmDialog({
      title: '清理镜像',
      message: '清理未被使用的镜像（悬空层）。若勾选下方选项，将删除所有未被容器引用的镜像。',
      detail: 'PruneImages(all=?)',
      confirmLabel: '开始清理',
      danger: true,
      checkboxes: [{ id: 'all', label: '删除所有未使用镜像（-a，范围更大）', checked: false }]
    });
    if (!r.confirmed) return;
    try {
      var res = await invoke('PruneImages', !!r.values.all);
      var out = res && res.Stdout ? res.Stdout : safeJson(res);
      if (String(out).trim()) showTextModal('清理镜像结果', String(out));
      else toast('info', '没有可清理的镜像。');
      await loadImages();
    } catch (e) { fail('PruneImages', e); }
  }

  /* ---------- 镜像导入 / 导出 ----------
     Wails v2 的 JS 运行时没有 OpenFileDialog / SaveFileDialog 绑定，所以这里
     直接用手动路径输入，同时把「拖拽到按钮」作为可选的便捷入口：把 tar 文件
     拖到「导入…」按钮上会自动填到路径输入框，然后再点「导入」执行。
     导出走 wslc image save -o，导入走 wslc image load -i。
  */
  function imageExportDialog() {
    if (!state.images.length) {
      toast('info', '本机没有可导出的镜像。');
      return;
    }
    var opts = buildImageOptions(state.images);
    var refSel = h('select', { class: 'input', 'aria-label': '要导出的镜像', title: '选择要导出的镜像' });
    setChildren(refSel, opts);
    if (refSel.options.length) refSel.selectedIndex = 0;

    var pathInput = h('input', {
      class: 'input mono', type: 'text',
      placeholder: 'D:\\exports\\nginx.tar',
      title: '导出目标路径（必须为绝对路径）',
      'aria-label': '导出目标路径'
    });

    var overwriteBox = h('input', { type: 'checkbox', id: 'img-export-ow' });
    var err = h('pre', { class: 'modal-detail', hidden: true });
    var busy = h('span', { class: 'spinner', 'aria-hidden': 'true', hidden: true });

    var ok = h('button', {
      class: 'btn btn-primary', type: 'button', title: '导出 (ExportImage)',
      'aria-label': '导出镜像', text: '导出',
      onclick: async function () {
        err.hidden = true;
        var ref = String(refSel.value || '').trim();
        var dst = String(pathInput.value || '').trim();
        if (!ref) { err.hidden = false; err.textContent = '请选择要导出的镜像。'; return; }
        if (!dst) { err.hidden = false; err.textContent = '请输入目标文件路径（例如 D:\\exports\\nginx.tar）。'; pathInput.focus(); return; }
        if (!isAbsPath(dst)) { err.hidden = false; err.textContent = '路径必须是绝对路径（例如 D:\\exports\\nginx.tar）。'; pathInput.focus(); return; }
        ok.disabled = true; busy.hidden = false;
        try {
          var out = await invoke('ExportImage', ref, dst, !!overwriteBox.checked);
          handle.close(null);
          toast('success', '镜像已导出：' + dst, String(out || '').trim().slice(0, 300));
        } catch (e) {
          var info = recordError('ExportImage', e);
          err.hidden = false; err.textContent = info.message + (info.detail ? '\n' + info.detail : '');
          toast('error', '导出失败：' + info.message);
        } finally { ok.disabled = false; busy.hidden = true; }
      }
    });

    var handle = mountModal({
      title: '导出镜像 (ExportImage)',
      body: [
        h('div', { class: 'form-grid' },
          fieldRow('镜像', refSel, { required: true, full: true }),
          fieldRow('目标路径', pathInput, { required: true, full: true }),
          h('label', { class: 'check' }, overwriteBox,
            h('span', { text: '如果目标文件已存在，则覆盖' })
          ),
          h('p', { class: 'faint' },
            '后端命令：wslc image save -o <path> <ref>。大镜像可能需要数分钟，请确保导出目录写权限足够。'
          )
        ),
        err
      ],
      foot: [busy, h('span', { class: 'modal-foot-spacer' }),
        h('button', {
          class: 'btn', type: 'button', title: '取消 (Esc)', 'aria-label': '取消',
          text: '取消', onclick: function () { handle.close(null); }
        }), ok]
    });
  }

  function imageImportDialog() {
    var pathInput = h('input', {
      class: 'input mono', type: 'text',
      placeholder: 'D:\\exports\\nginx.tar',
      title: '要导入的 tar 文件路径（必须为绝对路径）',
      'aria-label': '导入源文件路径'
    });
    var err = h('pre', { class: 'modal-detail', hidden: true });
    var busy = h('span', { class: 'spinner', 'aria-hidden': 'true', hidden: true });

    var ok = h('button', {
      class: 'btn btn-primary', type: 'button', title: '导入 (ImportImage)',
      'aria-label': '导入镜像', text: '导入',
      onclick: async function () {
        err.hidden = true;
        var src = String(pathInput.value || '').trim();
        if (!src) { err.hidden = false; err.textContent = '请输入 tar 文件路径。'; pathInput.focus(); return; }
        if (!isAbsPath(src)) { err.hidden = false; err.textContent = '路径必须是绝对路径。'; pathInput.focus(); return; }
        ok.disabled = true; busy.hidden = false;
        try {
          var out = await invoke('ImportImage', src);
          handle.close(null);
          toast('success', '已从文件导入镜像：' + src, String(out || '').trim().slice(0, 300));
          await loadImages();
        } catch (e) {
          var info = recordError('ImportImage', e);
          err.hidden = false; err.textContent = info.message + (info.detail ? '\n' + info.detail : '');
          toast('error', '导入失败：' + info.message);
        } finally { ok.disabled = false; busy.hidden = true; }
      }
    });

    var dropZone = h('div', {
      class: 'drop-hint', style: 'padding:8px; margin-top:6px; border:1px dashed var(--border, #444); border-radius:6px; font-size:var(--fs-xs);',
      title: '把 tar 文件拖到这里可以自动填路径'
    }, '也可将 tar 文件拖拽到本输入框：');

    var handle = mountModal({
      title: '导入镜像 (ImportImage)',
      body: [
        h('div', { class: 'form-grid' },
          fieldRow('tar 文件路径', pathInput, { required: true, full: true }),
          dropZone,
          h('p', { class: 'faint' },
            '后端命令：wslc image load -i <path>。文件必须是 wslc / docker save 生成的 tar。'
          )
        ),
        err
      ],
      foot: [busy, h('span', { class: 'modal-foot-spacer' }),
        h('button', {
          class: 'btn', type: 'button', title: '取消 (Esc)', 'aria-label': '取消',
          text: '取消', onclick: function () { handle.close(null); }
        }), ok]
    });
  }

  function isAbsPath(p) {
    if (!p) return false;
    if (/^[A-Za-z]:[\\/]/.test(p)) return true;
    if (p.charAt(0) === '\\' || p.charAt(0) === '/') return true;
    return false;
  }

  /* ========================== 12. 会话视图 ========================== */

  async function loadSessions() {
    var key = 'sessions';
    state.loading[key] = true;
    state.error[key] = null;
    renderSessions();
    try {
      var list = await invoke('ListSessions');
      state.sessions = Array.isArray(list) ? list : [];
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.sessions = [];
      fail('ListSessions', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      renderSessions();
      updateStatusLine();
    }
  }

  function renderSessions() {
    var tbody = $('sess-body');
    if (!tbody) return;
    var cols = 4;
    if (renderLoadedState(tbody, 'sessions', cols, function () { loadSessions(); })) {
      setCount('sess-count', 0, '个');
      return;
    }
    setCount('sess-count', state.sessions.length, '个');
    if (!state.sessions.length) {
      setChildren(tbody, [stateRow(cols, 'empty', {
        title: '没有活动会话',
        message: '首次调用任意 wslc 命令时会自动创建默认会话。若需要手动创建，请使用命令行 wslc <command> --session <name>。',
        retry: function () { loadSessions(); }
      })]);
      return;
    }
    var frag = document.createDocumentFragment();
    state.sessions.forEach(function (sess) {
      var name = String(sess.Name || '').trim() || '（无名称）';
      var id = sess.ID;
      var pid = sess.CreatorPid;
      frag.appendChild(h('tr', {
        tabindex: '0',
        dataset: { id: String(id), name: name },
        title: '按 Enter 查看详情'
      },
        h('td', { class: 'mono', text: String(id) }),
        h('td', { class: 'col-name', text: name }),
        h('td', { class: 'mono muted', text: pid ? String(pid) : '—' }),
        h('td', { class: 'col-actions' },
          h('div', { class: 'cell-actions' },
            h('button', {
              class: 'btn btn-row', type: 'button',
              title: '查看会话 ' + name + ' 的 JSON 详情',
              'aria-label': '查看会话 ' + name + ' 详情',
              text: '详情',
              onclick: function () { showTextModal('会话详情 · ' + name, JSON.stringify(sess, null, 2)); }
            }),
            h('button', {
              class: 'btn btn-row is-danger', type: 'button',
              title: '终止会话 ' + name + '（会结束其上所有容器进程）',
              'aria-label': '终止会话 ' + name,
              text: '终止',
              onclick: function () { terminateSessionDialog(id, name); }
            })
          )
        )
      ));
    });
    setChildren(tbody, [frag]);
  }

  async function terminateSessionDialog(id, name) {
    var r = await confirmDialog({
      title: '终止 wslc 会话',
      message: '将终止会话「' + name + '」。这会结束该会话下所有正在运行的容器与进程，未落盘的数据可能丢失。此操作不可撤销。',
      detail: 'TerminateSession(id=' + id + ')',
      confirmLabel: '终止',
      danger: true,
      requireText: 'TERMINATE'
    });
    if (!r.confirmed) return;
    try {
      var out = await invoke('TerminateSession', id);
      toast('success', '已终止会话：' + name, String(out || '').trim().slice(0, 300));
    } catch (e) { fail('TerminateSession', e); }
    await loadSessions();
  }

  /* ========================== 12b. 会话存储视图 ========================== */

  /* %LOCALAPPDATA%\wslc 为什么那么大：每个 wslc 会话的根文件系统都是一个
     storage.vhdx 动态磁盘，镜像层、构建缓存、卷数据全都落在里面。
     `wslc system session terminate` 只停 VM，不会删除或截断这个文件，
     所以磁盘会一直留着。本视图是唯一能看到它、能压缩它、能删它的地方。 */

  function formatBytesJS(n) {
    n = Number(n) || 0;
    if (n <= 0) return '0 B';
    var units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    var i = 0;
    while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
    if (i === 0) return Math.round(n) + ' B';
    return (Math.round(n * 10) / 10) + ' ' + units[i];
  }

  async function loadStorage() {
    var key = 'storage';
    state.loading[key] = true;
    state.error[key] = null;
    renderStorage();
    try {
      var list = await invoke('ListSessionStorage');
      state.storage = Array.isArray(list) ? list : [];
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.storage = [];
      fail('ListSessionStorage', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      renderStorage();
      updateStatusLine();
    }
  }

  function renderStorage() {
    var tbody = $('stor-body');
    if (!tbody) return;
    var cols = 5;
    if (renderLoadedState(tbody, 'storage', cols, function () { loadStorage(); })) {
      setCount('stor-count', 0, '个');
      return;
    }
    var items = state.storage;
    setCount('stor-count', items.length, '个');
    var total = items.reduce(function (n, s) { return n + (Number(s.BytesOnDisk) || 0); }, 0);
    var totalEl = $('stor-total');
    if (totalEl) totalEl.textContent = items.length ? formatBytesJS(total) : '—';

    if (!items.length) {
      setChildren(tbody, [stateRow(cols, 'empty', {
        title: '没有会话存储',
        message: '尚未创建过任何 wslc 会话。首次调用任意 wslc 命令时会自动创建默认会话，其磁盘文件为 storage.vhdx。',
        retry: function () { loadStorage(); }
      })]);
      return;
    }

    var frag = document.createDocumentFragment();
    items.forEach(function (st) {
      var name = String(st.SessionName || '').trim() || '（无名称）';
      var exists = !!st.Exists;
      var active = !!st.Active;
      var size = exists ? formatBytesJS(st.BytesOnDisk) : '—';
      var title = st.Path ? st.Path : '';
      var disabled = !exists || active;
      frag.appendChild(h('tr', {
        tabindex: '0',
        dataset: { name: name, active: active ? '1' : '0', exists: exists ? '1' : '0' }
      },
        h('td', { class: 'col-name', text: name, title: title }),
        h('td', { class: 'mono', text: size, title: exists ? String(st.BytesOnDisk || 0) + ' bytes' : '' }),
        h('td', { text: exists ? '存在' : '已删除' }),
        h('td', {
          class: active ? 'badge badge-warn' : 'badge',
          text: active ? '运行中' : '已停止',
          title: active ? '会话 VM 正在运行：磁盘被占用，压缩与删除会失败' : '会话 VM 已停止：可以压缩或删除磁盘'
        }),
        h('td', { class: 'col-actions' },
          h('div', { class: 'cell-actions' },
            h('button', {
              class: 'btn btn-row', type: 'button',
              title: '调用 diskpart 压缩 storage.vhdx，把未使用的空间还给磁盘（不丢数据）',
              'aria-label': '压缩 ' + name + ' 的存储',
              disabled: disabled,
              text: '压缩',
              onclick: function () { shrinkStorageDialog(name, st); }
            }),
            h('button', {
              class: 'btn btn-row is-danger', type: 'button',
              title: '删除 storage.vhdx，彻底释放磁盘（镜像、容器、卷数据全部丢失）',
              'aria-label': '删除 ' + name + ' 的存储',
              disabled: disabled,
              text: '删除',
              onclick: function () { resetStorageDialog(name, st); }
            })
          )
        )
      ));
    });
    setChildren(tbody, [frag]);
  }

  async function shrinkStorageDialog(name, st) {
    var msg = st.Active
      ? '会话「' + name + '」正在运行，请先在会话视图终止它再压缩。'
      : '将调用 Windows 的 diskpart 压缩会话「' + name + '」的 storage.vhdx（' + (st.SizeText || formatBytesJS(st.BytesOnDisk)) + '）。\n\n压缩会保留全部镜像、容器与卷数据，只把未使用的空间还给磁盘。diskpart 需要管理员权限；若以普通用户启动本程序，请改用「删除」释放空间，或以管理员身份重新打开本程序。';
    var r = await confirmDialog({
      title: '压缩会话存储',
      message: msg,
      detail: 'ShrinkSessionStorage(name=' + name + ')',
      confirmLabel: '压缩',
      danger: false
    });
    if (!r.confirmed) return;
    try {
      var out = await invoke('ShrinkSessionStorage', name);
      toast('success', '已压缩会话存储：' + name, String(out || '').trim().slice(0, 300));
    } catch (e) { fail('ShrinkSessionStorage', e); }
    await loadStorage();
  }

  async function resetStorageDialog(name, st) {
    var size = st.SizeText || formatBytesJS(st.BytesOnDisk);
    var r = await confirmDialog({
      title: '删除会话存储',
      message: '将删除会话「' + name + '」的 storage.vhdx（' + size + '）。\n\n其中的镜像、容器、构建缓存与卷数据会全部丢失，且不可恢复。wslc 会在下次使用时以同一会话名重新创建一个空磁盘。',
      detail: 'ResetSessionStorage(name=' + name + ') · ' + (st.Path || ''),
      confirmLabel: '删除',
      danger: true,
      requireText: 'DELETE'
    });
    if (!r.confirmed) return;
    try {
      var out = await invoke('ResetSessionStorage', name);
      toast('success', '已删除会话存储：' + name, String(out || '').trim().slice(0, 300));
    } catch (e) { fail('ResetSessionStorage', e); }
    await loadStorage();
  }

  /* ========================== 13. 卷视图 ========================== */

  async function loadVolumes() {
    var key = 'volumes';
    state.loading[key] = true;
    state.error[key] = null;
    renderVolumes();
    try {
      var list = await invoke('ListVolumes');
      state.volumes = Array.isArray(list) ? list : [];
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      setTabCount('volumes', state.volumes.length);
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.volumes = [];
      fail('ListVolumes', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      renderVolumes();
      updateStatusLine();
    }
  }

  function renderVolumes() {
    var tbody = $('vol-body');
    if (!tbody) return;
    var cols = 6;
    if (renderLoadedState(tbody, 'volumes', cols, function () { loadVolumes(); })) {
      setCount('vol-count', 0, '个');
      return;
    }
    setCount('vol-count', state.volumes.length, '个');
    if (!state.volumes.length) {
      setChildren(tbody, [stateRow(cols, 'empty', {
        title: '没有卷',
        message: '在上方填写名称后点「创建卷」。',
        retry: function () { loadVolumes(); }
      })]);
      return;
    }
    var frag = document.createDocumentFragment();
    state.volumes.forEach(function (v) {
      var name = v.Name || '';
      frag.appendChild(h('tr', {
        tabindex: '0', dataset: { ref: name }, title: '按 Enter 查看详情'
      },
        h('td', { class: 'mono', title: name, text: name || '—' }),
        h('td', { text: v.Driver || '—' }),
        h('td', { text: v.Scope || '—' }),
        h('td', { class: 'mono muted', title: v.Mountpoint || '', text: v.Mountpoint || '—' }),
        h('td', { class: 'muted nowrap', title: v.CreatedAt || '', text: v.CreatedAt || '—' }),
        h('td', { class: 'col-actions' },
          h('div', { class: 'cell-actions' },
            h('button', {
              class: 'btn btn-row', type: 'button', title: '查看卷 ' + name + ' 详情',
              'aria-label': '查看卷 ' + name + ' 详情', text: '详情',
              onclick: function () { showTextModal('卷详情 · ' + name, JSON.stringify(v, null, 2)); }
            }),
            h('button', {
              class: 'btn btn-row is-danger', type: 'button', title: '删除卷 ' + name + '（需确认）',
              'aria-label': '删除卷 ' + name, text: '删除',
              onclick: function () { removeVolume(name); }
            })
          )
        )
      ));
    });
    setChildren(tbody, [frag]);
  }

  async function createVolume(ev) {
    if (ev) ev.preventDefault();
    var nameEl = $('vol-name');
    var name = nameEl ? nameEl.value.trim() : '';
    if (!name) { toast('warn', '卷名称不能为空。'); if (nameEl) nameEl.focus(); return; }
    var driver = ($('vol-driver') || {}).value || '';
    try {
      var out = await invoke('CreateVolume', name, driver.trim());
      toast('success', '已创建卷：' + name, String(out || '').trim().slice(0, 200));
      if (nameEl) nameEl.value = '';
      await loadVolumes();
    } catch (e) { fail('CreateVolume', e); }
  }

  async function removeVolume(name) {
    var r = await confirmDialog({
      title: '删除卷',
      message: '将删除卷「' + name + '」，其中的数据会一并丢失，且不可恢复。',
      detail: 'RemoveVolume(name=' + name + ')',
      confirmLabel: '删除卷',
      danger: true,
      requireText: name
    });
    if (!r.confirmed) return;
    try {
      var out = await invoke('RemoveVolume', name, false);
      toast('success', '已删除卷：' + name, String(out || '').trim().slice(0, 200));
    } catch (e) { fail('RemoveVolume', e); }
    await loadVolumes();
  }

  /* ========================== 13. 网络视图 ========================== */

  async function loadNetworks() {
    var key = 'networks';
    state.loading[key] = true;
    state.error[key] = null;
    renderNetworks();
    try {
      var list = await invoke('ListNetworks');
      state.networks = Array.isArray(list) ? list : [];
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      setTabCount('networks', state.networks.length);
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.networks = [];
      fail('ListNetworks', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      renderNetworks();
      updateStatusLine();
    }
  }

  function renderNetworks() {
    var tbody = $('net-body');
    if (!tbody) return;
    var cols = 6;
    if (renderLoadedState(tbody, 'networks', cols, function () { loadNetworks(); })) {
      setCount('net-count', 0, '个');
      return;
    }
    setCount('net-count', state.networks.length, '个');
    if (!state.networks.length) {
      setChildren(tbody, [stateRow(cols, 'empty', {
        title: '没有网络',
        message: '在上方填写名称后点「创建网络」。',
        retry: function () { loadNetworks(); }
      })]);
      return;
    }
    var frag = document.createDocumentFragment();
    state.networks.forEach(function (n) {
      var name = n.Name || '';
      var containers = (n.Containers || []).length;
      frag.appendChild(h('tr', { tabindex: '0', dataset: { ref: name }, title: '按 Enter 查看详情' },
        h('td', { class: 'mono', title: name, text: name || '—' }),
        h('td', { class: 'mono muted', title: n.ID || '', text: shortId(n.ID, 12) }),
        h('td', { text: n.Driver || '—' }),
        h('td', {},
          h('span', { class: 'badge badge-muted', title: String(n.Scope || ''), text: n.Scope || '—' })
        ),
        h('td', { class: 'muted nowrap', title: n.CreatedAt || '', text: n.CreatedAt || '—' }),
        h('td', { class: 'col-actions' },
          h('div', { class: 'cell-actions' },
            h('button', {
              class: 'btn btn-row', type: 'button', title: '查看网络 ' + name + ' 详情（' + containers + ' 个容器）',
              'aria-label': '查看网络 ' + name + ' 详情', text: '详情',
              onclick: function () { showTextModal('网络详情 · ' + name, JSON.stringify(n, null, 2)); }
            }),
            h('button', {
              class: 'btn btn-row is-danger', type: 'button', title: '删除网络 ' + name + '（需确认）',
              'aria-label': '删除网络 ' + name, text: '删除',
              onclick: function () { removeNetwork(name); }
            })
          )
        )
      ));
    });
    setChildren(tbody, [frag]);
  }

  async function createNetwork(ev) {
    if (ev) ev.preventDefault();
    var nameEl = $('net-name');
    var name = nameEl ? nameEl.value.trim() : '';
    if (!name) { toast('warn', '网络名称不能为空。'); if (nameEl) nameEl.focus(); return; }
    var driver = (($('net-driver') || {}).value || '').trim();
    var subnet = (($('net-subnet') || {}).value || '').trim();
    var gateway = (($('net-gateway') || {}).value || '').trim();
    var internal = !!($('net-internal') && $('net-internal').checked);
    try {
      var out = await invoke('CreateNetwork', name, driver, subnet, gateway, internal);
      toast('success', '已创建网络：' + name, String(out || '').trim().slice(0, 200));
      if (nameEl) nameEl.value = '';
      await loadNetworks();
    } catch (e) { fail('CreateNetwork', e); }
  }

  async function removeNetwork(name) {
    var r = await confirmDialog({
      title: '删除网络',
      message: '将删除网络「' + name + '」。仍连接到该网络的容器会导致删除失败。',
      detail: 'RemoveNetwork(name=' + name + ')',
      confirmLabel: '删除网络',
      danger: true
    });
    if (!r.confirmed) return;
    try {
      var out = await invoke('RemoveNetwork', name, false);
      toast('success', '已删除网络：' + name, String(out || '').trim().slice(0, 200));
    } catch (e) { fail('RemoveNetwork', e); }
    await loadNetworks();
  }

  /* ========================== 14. 环境自检 ========================== */

  async function loadEnv() {
    var key = 'env';
    state.loading[key] = true;
    state.error[key] = null;
    renderEnv();
    try {
      var env = await invoke('EnvCheck');
      applyEnv(env);
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      fail('EnvCheck', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      renderEnv();
      updateStatusLine();
    }
  }

  function applyEnv(env) {
    state.env = env || null;
    setEnvBanner(state.env);
    var el = $('env-checked');
    if (el) el.textContent = state.env && state.env.CheckedAt ? ('自检时间 ' + fmtTime(state.env.CheckedAt)) : '—';
  }

  function kvRow(label, value, emptyText) {
    var v = value;
    var isEmpty = v === null || v === undefined || v === '' || (Array.isArray(v) && !v.length);
    return [
      h('dt', { text: label }),
      h('dd', { class: isEmpty ? 'empty' : '', text: isEmpty ? (emptyText || '（空）') : String(v) })
    ];
  }

  function renderEnv() {
    var root = $('env-content');
    if (!root) return;

    if (state.loading.env && !state.env) {
      setChildren(root, [h('div', { class: 'state' },
        h('span', { class: 'spinner', 'aria-hidden': 'true' }),
        h('span', { class: 'state-title', text: '正在执行环境自检…' })
      )]);
      return;
    }

    if (state.error.env && !state.env) {
      var e = state.error.env;
      setChildren(root, [h('div', { class: 'state' },
        h('span', { class: 'state-icon', text: '⛔' }),
        h('span', { class: 'state-title', text: '环境自检失败' }),
        h('div', { class: 'state-msg', text: e.message }),
        e.detail ? h('pre', { class: 'state-detail', text: e.detail }) : null,
        h('div', { class: 'state-actions' },
          h('button', { class: 'btn', type: 'button', title: '重试', 'aria-label': '重试环境自检', text: '重试', onclick: loadEnv })
        )
      )]);
      return;
    }

    if (!state.env) {
      setChildren(root, [h('div', { class: 'state' },
        h('span', { class: 'state-icon', text: 'ℹ' }),
        h('span', { class: 'state-title', text: '尚未自检' }),
        h('div', { class: 'state-actions' },
          h('button', { class: 'btn', type: 'button', title: '开始自检', 'aria-label': '开始环境自检', text: '开始自检', onclick: loadEnv })
        )
      )]);
      return;
    }

    var env = state.env;
    var problems = Array.isArray(env.Problems) ? env.Problems : [];
    var sessions = Array.isArray(env.Sessions) ? env.Sessions : [];
    var nodes = [];

    /* 顶部结论卡 */
    var ready = env.ServiceReady === true;
    nodes.push(h('div', { class: 'card' + (ready ? '' : ' card-problem') },
      h('div', { class: 'card-head' },
        h('span', { class: 'dot dot-' + (ready ? 'running' : 'error'), 'aria-hidden': 'true' }),
        h('span', { text: ready ? '容器功能可用（ServiceReady = true）' : '容器功能不可用（ServiceReady = false）' })
      ),
      h('div', { class: 'card-body' },
        h('dl', { class: 'kv' },
          kvRow('可用性 (Available)', env.Available === true ? 'true' : 'false'),
          kvRow('服务就绪 (ServiceReady)', ready ? 'true' : 'false'),
          kvRow('wslc 路径 (WslcPath)', env.WslcPath),
          kvRow('wslc 版本 (WslcVersion)', env.WslcVersion),
          kvRow('WSL 版本 (WSLVersion)', env.WSLVersion),
          kvRow('内核版本 (KernelVersion)', env.KernelVersion),
          kvRow('配置文件 (SettingsFile)', env.SettingsFile),
          kvRow('自检时间 (CheckedAt)', fmtTime(env.CheckedAt) || String(env.CheckedAt || '')),
          kvRow('会话数 (Sessions)', sessions.length ? String(sessions.length) : '')
        )
      )
    ));

    /* Problems 卡 —— ServiceReady=false 时必须显著 */
    if (problems.length) {
      nodes.push(h('div', { class: 'card card-problem' },
        h('div', { class: 'card-head' },
          h('span', { 'aria-hidden': 'true', text: '⛔' }),
          h('span', { text: 'Problems（' + problems.length + '）' })
        ),
        h('div', { class: 'card-body' },
          h('ul', { class: 'problem-list' }, problems.map(function (p) {
            return h('li', { text: String(p) });
          }))
        )
      ));
    }

    if (!ready || problems.length) {
      nodes.push(h('div', { class: 'fix-hint' },
        h('strong', { text: '修复建议（hypervisor 计算服务未运行 → HCS_E_SERVICE_NOT_AVAILABLE / 0x80370114）' }),
        h('ol', {},
          h('li', { text: '以管理员身份打开 PowerShell（开始菜单右键 → 终端(管理员)）。' }),
          h('li', {}, '把服务启动类型从 Disabled 改回手动（Disabled 是真正的故障点）： ',
            h('code', { text: 'Set-Service -Name vmcompute -StartupType Manual' })),
          h('li', {}, '启动服务：', h('code', { text: 'Start-Service vmcompute' })),
          h('li', {}, '确认状态为 Running：', h('code', { text: 'Get-Service vmcompute' })),
          h('li', {}, '若仍不行：确认可选功能 VirtualMachinePlatform 已启用（',
            h('code', { text: 'Enable-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -All -NoRestart' }),
            '，必要时重启），并检查 ', h('code', { text: 'bcdedit /enum | findstr hypervisorlaunchtype' }),
            ' 应为 Auto。'),
          h('li', { text: '不需要安装 WSL 发行版，也不需要 Containers / 完整 Hyper-V 角色。' }),
          h('li', { text: '回到本页点「重新自检」；若仍不可用，确认 wslc 已安装且 Available = true。' }),
          h('li', {}, '若 WslcPath 为空，可设置环境变量 ', h('code', { text: 'WSLC_PATH' }),
            ' 指向 wslc.exe（默认 C:\\Program Files\\WSL\\wslc.exe）后重启本应用。')
        )
      ));
    }

    /* Sessions */
    nodes.push(h('div', { class: 'card' },
      h('div', { class: 'card-head' }, h('span', { text: 'Sessions（' + sessions.length + '）' })),
      h('div', { class: 'card-body' },
        sessions.length
          ? h('table', { class: 'sessions' },
            h('thead', {}, h('tr', {},
              h('th', { scope: 'col', text: 'ID' }),
              h('th', { scope: 'col', text: 'Name' }),
              h('th', { scope: 'col', text: 'CreatorPid' })
            )),
            h('tbody', {}, sessions.map(function (s) {
              return h('tr', {},
                h('td', { text: s.ID === undefined || s.ID === null ? '—' : String(s.ID) }),
                h('td', { text: s.Name || '—' }),
                h('td', { text: s.CreatorPid === undefined || s.CreatorPid === null ? '—' : String(s.CreatorPid) })
              );
            }))
          )
          : h('div', { class: 'faint', text: '没有活动会话。' })
      )
    ));

    /* 原始 JSON，便于排查 */
    nodes.push(h('details', { class: 'panel' },
      h('summary', { title: '查看 EnvStatus 原始 JSON' }, '原始 JSON（EnvStatus）'),
      h('div', { class: 'card-body' },
        h('pre', { class: 'modal-detail', text: JSON.stringify(env, null, 2) })
      )
    ));

    setChildren(root, nodes);
  }

  /* ========================== 15. 设置视图（代理 / 镜像源）========================== */

  async function loadSettings() {
    var key = 'settings';
    state.loading[key] = true;
    state.error[key] = null;
    if (currentPanel() === 'settings') renderSettings();
    try {
      var data = await invoke('LoadSettings');
      state.settings = data || null;
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      /* 常用镜像预设写入完成后刷新镜像页的下拉，让"常用镜像"配置即时可见 */
      if (state.settings && state.settings.PresetImages) {
        fillPullPresets($('img-pull-presets'));
      }
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.settings = null;
      if (currentPanel() === 'settings') fail('LoadSettings', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      if (currentPanel() === 'settings') renderSettings();
      updateStatusLine();
    }
  }

  async function saveSettings() {
    if (!state.settings) return;
    var payload = readSettingsForm();
    var key = 'settings';
    state.loading[key] = true;
    state.error[key] = null;
    renderSettings();
    try {
      var saved = await invoke('SaveSettings', payload);
      state.settings = saved || payload;
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      if (state.settings && state.settings.PresetImages) {
        fillPullPresets($('img-pull-presets'));
      }
      setStatus('设置已保存');
      touchLast();
      markSettingsClean();
    } catch (e) {
      state.error[key] = normalizeError(e);
      fail('SaveSettings', e, { showDetail: true });
    } finally {
      state.loading[key] = false;
      if (currentPanel() === 'settings') renderSettings();
      updateStatusLine();
    }
  }

  function readSettingsForm() {
    var pick = function (id, fallback) {
      var el = $(id);
      return el && el.value !== undefined ? el.value : (state.settings ? state.settings[fallback] : fallback);
    };
    var pickBool = function (id, fallback) {
      var el = $(id);
      return el ? el.checked : (state.settings ? state.settings[fallback] : !!fallback);
    };
    var custom = [];
    var customEl = $('set-custom-mirrors');
    if (customEl) {
      custom = customEl.value.split(/\s+/).map(function (s) { return s.trim(); }).filter(Boolean);
    }
    /* 常用镜像预设从页面上的 preset-row 表单读取（label + ref 两个 input） */    var presets = [];
    var presetRows = document.querySelectorAll('#settings-content .preset-row');
    if (presetRows.length) {
      presetRows.forEach(function (row) {
        var labelEl = row.querySelector('.preset-label');
        var refEl = row.querySelector('.preset-ref');
        if (!labelEl || !refEl) return;
        var ref = (refEl.value || '').trim();
        if (!ref) return;
        var label = (labelEl.value || '').trim() || ref;
        presets.push({ Label: label, Ref: ref });
      });
    } else if (state.settings && state.settings.PresetImages) {
      presets = state.settings.PresetImages.map(function (p) {
        return { Label: p.Label || p.Ref, Ref: p.Ref };
      });
    }
    return {
      MirrorEnabled: pickBool('set-mirror-enabled', 'MirrorEnabled'),
      MirrorEndpoint: state.settings ? state.settings.MirrorEndpoint : 'docker.m.daocloud.io',
      CustomMirrors: custom,
      PresetImages: presets
    };
  }

  async function testMirror(endpoint) {
    if (!endpoint) return;
    state.settingsProbe[endpoint] = { busy: true };
    if (currentPanel() === 'settings') renderSettings();
    try {
      var probe = await invoke('TestMirror', endpoint);
      state.settingsProbe[endpoint] = probe || { OK: false, Message: '无结果' };
    } catch (e) {
      state.settingsProbe[endpoint] = { OK: false, Message: normalizeError(e).message || String(e) };
    }
    if (currentPanel() === 'settings') renderSettings();
  }

  /* 状态胶囊（Currently Active / Normal / Off） */
  function statusPill(cls, text) {
    return h('span', { class: 'status-pill ' + (cls || ''), title: text }, text);
  }

  /* 内联探测结果（小胶囊，替代原来的 .probe 块） */
  function probeInline(probe, label) {
    if (!probe) return null;
    if (probe.busy) return h('span', { class: 'status-pill busy', title: label }, '探测中…');
    if (probe.OK) return h('span', { class: 'status-pill active', title: label + '：' + (probe.Message || '成功') },
      '✓ ' + probe.DurationMS + 'ms');
    return h('span', { class: 'status-pill off', title: label + '：' + (probe.Message || '失败'),
      style: 'color:var(--danger); border-color:var(--danger);' },
      '✗ ' + (probe.Message || '失败'));
  }

  /* 镜像源卡片：点击激活；带顶部渐变条、badge、状态胶囊、探测按钮 */
  function mirrorCard(m) {
    var isActive = m === (state.settings && state.settings.MirrorEndpoint);
    var probe = state.settingsProbe[m];
    var badge = m.charAt(0).toUpperCase();
    var label = mirrorLabel(m) || m;
    var note = mirrorNote(m) || '';
    return h('div', {
      class: 'mirror-card' + (isActive ? ' is-active' : ''),
      role: 'button',
      tabindex: '0',
      'aria-pressed': isActive ? 'true' : 'false',
      'aria-label': '选择镜像源 ' + m,
      title: '点击激活 ' + m,
      onclick: function () { activateMirror(m); },
      onkeydown: function (ev) {
        if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); activateMirror(m); }
      }
    },
      h('div', { class: 'mirror-card-head' },
        h('span', { class: 'badge', 'aria-hidden': 'true', text: badge }),
        h('div', { style: 'min-width:0; flex:1;' },
          h('div', { class: 'mirror-card-label', text: label }),
          h('div', { class: 'mirror-card-endpoint mono', text: m })
        ),
        isActive ? statusPill('active', 'Currently Active') : statusPill('off', 'Normal')
      ),
      note ? h('div', { class: 'mirror-card-note', text: note }) : null,
      h('div', { class: 'mirror-card-actions' },
        h('button', {
          class: 'btn btn-sm',
          type: 'button',
          text: (probe && probe.busy) ? '探测中…' : '⚡ 测试',
          disabled: (probe && probe.busy) ? 'disabled' : undefined,
          title: '以 hello-world:latest 探测此镜像源（约 10–30 秒）',
          onclick: function (ev) { ev.stopPropagation(); testMirror(m); }
        }),
        h('span', { class: 'spacer' }),
        (probe && probe.busy) ? h('span', { class: 'probe-inline busy', text: '探测中…' })
          : (probe && probe.OK) ? h('span', { class: 'probe-inline ok', text: '✓ ' + probe.DurationMS + 'ms' })
          : (probe && !probe.OK) ? h('span', { class: 'probe-inline fail', text: '✗ ' + (probe.Message || '失败') })
          : null
      )
    );
  }

  /* 激活某个镜像源（修改本地 state，保存由用户点击「保存」触发） */
  function activateMirror(endpoint) {
    if (!state.settings) return;
    state.settings.MirrorEndpoint = endpoint;
    state.settings.MirrorEnabled = true;
    renderSettings();
    setStatus('已选择镜像源 ' + endpoint + '（点击「保存」生效）');
  }

  /* 常用镜像预设编辑：增 / 删 / 上移 */
  function addPresetRow() {
    state._pendingPreset = { Label: '', Ref: '' };
    if (currentPanel() === 'settings') renderSettings();
  }
  function removePresetRow(idx) {
    if (!state.settings || !state.settings.PresetImages) return;
    state.settings.PresetImages.splice(idx, 1);
    renderSettings();
  }
  function movePresetRow(idx, delta) {
    if (!state.settings || !state.settings.PresetImages) return;
    var to = idx + delta;
    var arr = state.settings.PresetImages;
    if (to < 0 || to >= arr.length) return;
    var tmp = arr[idx]; arr[idx] = arr[to]; arr[to] = tmp;
    renderSettings();
  }

  /* 常用镜像预设：拉取下拉和设置页共用。默认值来自后端 DefaultPresetImages，
     前端这份兜底表用于 settings 未加载时展示。 */
  var DEFAULT_PRESET_IMAGES = [
    ['MySQL 8.4',            'mysql:8.4'],
    ['Oracle XE 23ai',       'gvenzl/oracle-xe:23'],
    ['PostgreSQL 17',        'postgres:17'],
    ['Redis 7',              'redis:7'],
    ['Nacos v2.5.1',         'nacos/nacos-server:v2.5.1'],
    ['MariaDB 11',           'mariadb:11'],
    ['MongoDB 8',            'mongo:8'],
    ['Kafka 7.8',            'confluentinc/cp-kafka:7.8.0'],
    ['Zookeeper 3.9',        'zookeeper:3.9'],
    ['Schema Registry',      'confluentinc/cp-schema-registry:7.8.0'],
    ['Elasticsearch 8.17',   'elasticsearch:8.17.0'],
    ['Kibana 8.17',          'kibana:8.17.0'],
    ['OpenSearch 2.18',      'opensearchproject/opensearch:2.18'],
    ['MinIO',                'minio/minio:latest'],
    ['Jenkins LTS',          'jenkins/jenkins:lts'],
    ['Harbor',               'goharbor/harbor:latest'],
    ['Nginx 1.27',           'nginx:1.27'],
    ['Node.js 22',           'node:22'],
    ['Python 3.12',          'python:3.12'],
    ['Golang 1.23',          'golang:1.23'],
    ['Temurin 21 JDK',       'eclipse-temurin:21-jdk'],
    ['Consul 1.19',          'hashicorp/consul:1.19'],
    ['etcd 3.5',             'bitnami/etcd:3.5'],
    ['RabbitMQ 3.13',        'rabbitmq:3.13'],
    ['Airflow 2.10',         'apache/airflow:2.10'],
    ['Grafana 11',           'grafana/grafana:11'],
    ['Prometheus',           'prom/prometheus:v2.53.0'],
    ['Apache Doris 2.1',     'apache/doris:2.1.0'],
    ['ClickHouse',           'clickhouse/clickhouse-server:24'],
    ['TiDB 8.1',             'pingcap/tidb:8.1']
  ];

  /* 镜像源的标签和说明（用于卡片墙的 badge 名和 note）。后端 settings.go 的
     DefaultMirrors 是权威版本；这里给 UI 一个兜底展示。 */
  var MIRROR_LABELS = {
    'docker.io': 'Docker Hub 官方',
    'docker.m.daocloud.io': 'DaoCloud（默认）',
    'docker.xuanyuan.me': '幻象网络',
    'docker.1panel.live': '1Panel',
    'dockerhub.timeweb.cloud': 'Timeweb',
    'dockerproxy.com': 'dockerproxy',
    'docker.mirrors.ustc.edu.cn': '中科大',
    'hub-mirror.c.163.com': '网易',
    'mirror.bjtu.edu.cn': '北交大',
    'docker.nju.edu.cn': '南京大学',
    'mirror.qiniu.com': '七牛'
  };
  var MIRROR_NOTES = {
    'docker.io': '国内通常直连超时（实测 15s 超时），需配合代理',
    'docker.m.daocloud.io': '实测最快，3.2s 拉取 hello-world',
    'docker.xuanyuan.me': '实测可用，5.3s',
    'docker.1panel.live': '实测可用，9.1s',
    'dockerhub.timeweb.cloud': '2026-10 实测超时，可用性不稳定',
    'dockerproxy.com': '2026-10 实测超时，可用性不稳定',
    'docker.mirrors.ustc.edu.cn': '高校镜像站，2024 起多次下线',
    'hub-mirror.c.163.com': '已停止服务',
    'mirror.bjtu.edu.cn': '已停止服务',
    'docker.nju.edu.cn': '已停止服务',
    'mirror.qiniu.com': '已停止服务'
  };
  function mirrorLabel(endpoint) { return endpoint ? (MIRROR_LABELS[endpoint] || '') : ''; }
  function mirrorNote(endpoint)  { return endpoint ? (MIRROR_NOTES[endpoint] || '') : ''; }

  function renderSettings() {
    var root = $('settings-content');
    if (!root) return;

    if (state.loading.settings && !state.settings) {
      setChildren(root, [h('div', { class: 'state' },
        h('span', { class: 'spinner', 'aria-hidden': 'true' }),
        h('span', { class: 'state-title', text: '正在加载设置…' })
      )]);
      return;
    }
    if (state.error.settings && !state.settings) {
      var e = state.error.settings;
      setChildren(root, [h('div', { class: 'state' },
        h('span', { class: 'state-icon', text: '⛔' }),
        h('span', { class: 'state-title', text: '加载设置失败' }),
        h('div', { class: 'state-msg', text: e.message }),
        e.detail ? h('pre', { class: 'state-detail', text: e.detail }) : null,
        h('div', { class: 'state-actions' },
          h('button', { class: 'btn', type: 'button', text: '重试', onclick: loadSettings })
        )
      )]);
      return;
    }

    var s = state.settings || {};
    var activeMirror = state.env && state.env.ActiveMirror ? state.env.ActiveMirror : (s.MirrorEnabled ? s.MirrorEndpoint : '');
    var nodes = [];

    /* 说明卡：wslc 不支持 registry mirror 配置项。
       做成可折叠的 details，避免每次打开设置页都堆一大堆文字。 */
    nodes.push(h('details', { class: 'card card-hint hint-compact', open: false },
      h('summary', { class: 'card-head', style: 'padding:10px 16px;' },
        h('span', { class: 'head-icon', 'aria-hidden': 'true', text: 'ℹ️' }),
        h('span', { text: 'wslc 3.x 无 registry mirror 配置项，本工具通过镜像名改写弥补' }),
        h('span', { class: 'spacer' }),
        h('span', { class: 'faint', style: 'font-size:var(--fs-xs);', text: '点击展开详情' })
      ),
      h('div', { class: 'card-body', style: 'padding-top:0;' },
        h('p', { class: 'hint-text', style: 'margin:0;' },
          h('strong', { text: '镜像源改写' }),
          h('span', { text: '：输入 ' }), h('code', { text: 'alpine:3.20' }),
          h('span', { text: ' → 实际执行 ' }), h('code', { text: 'wslc image pull <mirror>/library/alpine:3.20' })
        ),
        h('p', { class: 'hint-text', style: 'margin:8px 0 0;' },
          h('strong', { text: '测试拉取' }),
          h('span', { text: '：镜像站的「测试」用 ' }), h('code', { text: 'wslc image pull <mirror>/library/hello-world:latest' }),
          h('span', { text: ' 探测；失败常见原因是镜像站不可达或该仓库不存在。' })
        )
      )
    ));

    /* ============ 镜像源区块（卡片墙）============ */
    var mirrors = s.CustomMirrors && s.CustomMirrors.length ? s.CustomMirrors : [];
    var mirrorEnabled = s.MirrorEnabled !== false;
    nodes.push(h('div', { class: 'section' },
      h('div', { class: 'section-head' },
        h('span', { class: 'head-icon', 'aria-hidden': 'true', text: '🛰' }),
        h('div', { style: 'flex:1; min-width:0;' },
          h('div', { class: 'section-title' }, '镜像源'),
          h('div', { class: 'section-desc', text: '改写镜像名拉取；点击卡片激活，无需重启。' })
        ),
        h('label', { class: 'switch' },
          h('input', {
            type: 'checkbox',
            id: 'set-mirror-enabled',
            checked: (mirrorEnabled ? 'checked' : undefined),
            onchange: function (ev) {
              if (!state.settings) return;
              state.settings.MirrorEnabled = ev.target.checked;
              renderSettings();
            }
          }),
          h('span', { class: 'track' }),
          h('span', { class: 'switch-label', text: '镜像改写' })
        )
      ),
      h('div', { class: 'section-body' },
        h('div', { style: 'display:flex; align-items:center; gap:12px; margin-bottom:14px; flex-wrap:wrap;' },
          h('span', { class: 'field-label', text: '当前生效' }),
          activeMirror ? statusPill('active', 'Currently Active') : statusPill('off', '已禁用'),
          h('span', { class: 'mono muted', text: activeMirror || '（未启用）' }),
          h('span', { class: 'spacer' }),
          h('span', { class: 'muted', style: 'font-size:var(--fs-xs);' }, mirrors.length + ' 个镜像源')
        ),
        mirrors.length ? h('div', { class: 'mirror-grid' }, mirrors.map(function (m) { return mirrorCard(m); }))
          : h('div', { class: 'muted', style: 'padding:12px; text-align:center;' }, '自定义镜像源列表为空。'),
        h('div', { style: 'display:flex; gap:8px; margin-top:14px; align-items:center;' },
          h('span', { class: 'field-label', style: 'white-space:nowrap;', text: '自定义镜像源' }),
          h('input', {
            id: 'set-custom-mirrors', type: 'text', class: 'input mono',
            style: 'flex:1;',
            value: mirrors.join(' ') || '',
            placeholder: 'docker.io docker.m.daocloud.io docker.1panel.live（空格分隔）'
          })
        )
      )
    ));

    /* ============ 常用镜像预设（可编辑，从 settings 读写）============ */
    var presets = (s.PresetImages && s.PresetImages.length)
      ? s.PresetImages.slice()
      : [];
    if (state._pendingPreset) {
      presets.push(state._pendingPreset);
      state._pendingPreset = null;
    }
    nodes.push(h('div', { class: 'section' },
      h('div', { class: 'section-head' },
        h('span', { class: 'head-icon', 'aria-hidden': 'true', text: '⭐' }),
        h('div', { style: 'flex:1; min-width:0;' },
          h('div', { class: 'section-title' }, '常用镜像'),
          h('div', { class: 'section-desc', text: '「镜像」页工具栏的「常用镜像」下拉读取这里。可增删改，保存后生效。' })
        ),
        statusPill(presets.length ? 'active' : 'off', presets.length + ' 项')
      ),
      h('div', { class: 'section-body' },
        presets.length ? h('div', { class: 'preset-list' }, presets.map(function (p, idx) {
          return h('div', { class: 'preset-row', 'data-idx': String(idx) },
            h('span', { class: 'drag', 'aria-hidden': 'true', title: '拖动排序（或点上下箭头）', text: '⠿' }),
            h('input', {
              class: 'preset-input preset-label',
              type: 'text',
              value: p.Label || '',
              placeholder: '名称',
              'aria-label': '预设名称 #' + (idx + 1)
            }),
            h('input', {
              class: 'preset-input mono preset-ref',
              type: 'text',
              value: p.Ref || '',
              placeholder: '镜像引用，例如 mysql:8.4',
              'aria-label': '镜像引用 #' + (idx + 1)
            }),
            h('button', {
              class: 'btn btn-sm btn-icon', type: 'button',
              text: '↑', title: '上移',
              'aria-label': '上移第 ' + (idx + 1) + ' 项',
              disabled: (idx === 0) ? 'disabled' : undefined,
              onclick: function () { movePresetRow(idx, -1); }
            }),
            h('button', {
              class: 'btn btn-sm btn-icon btn-danger-ghost', type: 'button',
              text: '✕', title: '删除',
              'aria-label': '删除第 ' + (idx + 1) + ' 项',
              onclick: function () { removePresetRow(idx); }
            })
          );
        })) : h('div', { class: 'muted', style: 'padding:12px; text-align:center;' }, '尚未配置常用镜像。点击下方「添加」或「重置为默认」恢复内置清单。'),
        h('div', { class: 'preset-add' },
          h('button', {
            class: 'btn btn-sm', type: 'button', text: '＋ 添加',
            onclick: addPresetRow
          }),
          h('button', {
            class: 'btn btn-sm', type: 'button', text: '↺ 重置为默认',
            title: '恢复内置 30 个常用镜像',
            onclick: function () {
              if (!state.settings) return;
              state.settings.PresetImages = DEFAULT_PRESET_IMAGES.map(function (p) { return { Label: p[0], Ref: p[1] }; });
              renderSettings();
              setStatus('已重置为默认常用镜像（点击「保存」生效）');
            }
          })
        )
      )
    ));

    /* 设置文件路径挪到工具栏右下角的状态位（loadSettings 结束时写入 settings-status）；
       这里不再单独占一张卡片。 */

    /* 错误 */
    if (state.error.settings) {
      var ee = state.error.settings;
      nodes.push(h('div', { class: 'card card-problem' },
        h('div', { class: 'card-head' },
          h('span', { class: 'head-icon', 'aria-hidden': 'true', text: '⛔' }),
          h('span', { text: '最近一次错误' })
        ),
        h('div', { class: 'card-body' },
          h('div', { class: 'problem-list' }, h('li', { text: ee.message })),
          ee.detail ? h('pre', { class: 'modal-detail', text: ee.detail }) : null
        )
      ));
    }

    /* 原始 JSON */
    if (s && Object.keys(s).length) {
      nodes.push(h('details', { class: 'panel' },
        h('summary', { title: '查看 AppSettings 原始 JSON' }, '原始 JSON（AppSettings）'),
        h('div', { class: 'card-body' }, h('pre', { class: 'modal-detail', text: JSON.stringify(s, null, 2) }))
      ));
    }

    setChildren(root, nodes);
    var el = $('settings-status');
    if (el) {
      var path = state.env && state.env.SettingsPath ? state.env.SettingsPath : '';
      var timeStr = state.loaded.settings ? fmtTime(state.loadedAt.settings) : '—';
      el.textContent = path ? (path + '  ·  ' + timeStr) : timeStr;
      el.title = path || '—';
    }
  }

  function fillPullPresets(sel) {
    if (!sel) return;
    var nodes = [h('option', { value: '', text: '常用镜像…' })];
    var list = (state.settings && state.settings.PresetImages && state.settings.PresetImages.length)
      ? state.settings.PresetImages
      : DEFAULT_PRESET_IMAGES.map(function (p) { return { Label: p[0], Ref: p[1] }; });
    for (var i = 0; i < list.length; i++) {
      var p = list[i];
      var label = p.Label || p.Ref;
      nodes.push(h('option', { value: p.Ref, text: label + '  ·  ' + p.Ref }));
    }
    setChildren(sel, nodes);
  }

  /* 把镜像引用填入拉取输入框并聚焦（供设置页的 chips 用）。 */
  function fillPullRef(ref) {
    var el = $('img-pull-ref');
    if (!el) { selectView('images'); return; }
    el.value = ref;
    el.focus();
    el.select();
    setStatus('已填入 ' + ref + '，点击「拉取」开始');
  }

  /* ========================== 16. 任务视图 ========================== */

  async function loadTasks() {
    var key = 'tasks';
    state.loading[key] = true;
    state.error[key] = null;
    if (currentPanel() === 'tasks') renderTasks();
    try {
      var list = await invoke('ListTasks');
      state.tasks = Array.isArray(list) ? list : [];
      state.loaded[key] = true;
      state.loadedAt[key] = Date.now();
      setTabCount('tasks', state.tasks.length);
      touchLast();
    } catch (e) {
      state.error[key] = normalizeError(e);
      if (!state.loaded[key]) state.tasks = [];
      if (currentPanel() === 'tasks') fail('ListTasks', e, { showDetail: false });
    } finally {
      state.loading[key] = false;
      if (currentPanel() === 'tasks') renderTasks();
      updateStatusLine();
    }
  }

  function taskState(t) { return String(t.State || '').toLowerCase(); }

  function renderTasks() {
    var tbody = $('task-body');
    if (!tbody) return;
    var cols = 7;
    if (renderLoadedState(tbody, 'tasks', cols, function () { loadTasks(); })) {
      setCount('task-count', 0, '个');
      return;
    }
    setCount('task-count', state.tasks.length, '个');
    if (!state.tasks.length) {
      setChildren(tbody, [stateRow(cols, 'empty', {
        title: '没有任务',
        message: '拉取 / 构建等异步操作会在这里出现，可查看输出或取消。',
        retry: function () { loadTasks(); }
      })]);
      return;
    }
    var frag = document.createDocumentFragment();
    state.tasks.forEach(function (t) {
      var st = taskState(t);
      var cls = stateClass(st);
      var id = String(t.ID || '');
      var actions = h('div', { class: 'cell-actions' },
        h('button', {
          class: 'btn btn-row', type: 'button', title: '查看任务 ' + shortId(id, 12) + ' 的实时输出',
          'aria-label': '查看任务输出', text: '输出',
          onclick: function () { openTaskOutput(id, (t.Kind || '任务') + ' ' + (t.Ref || ''), [CH.task, CH.build, CH.pull]); }
        }),
        st === 'running'
          ? h('button', {
            class: 'btn btn-row is-danger', type: 'button',
            title: '取消任务 ' + shortId(id, 12), 'aria-label': '取消任务', text: '取消',
            onclick: function () { cancelTask(id); }
          })
          : null
      );

      frag.appendChild(h('tr', { tabindex: '0', dataset: { ref: id }, title: '按 Enter 查看任务详情' },
        h('td', { class: 'mono', title: id, text: shortId(id, 12) }),
        h('td', {}, h('span', { class: 'badge badge-info', text: t.Kind || '—' })),
        h('td', { class: 'mono', title: t.Ref || '', text: t.Ref || '—' }),
        h('td', {}, h('span', { class: 'badge badge-' + cls, title: t.Error || '', text: statusLabel(t.State) })),
        h('td', { class: 'muted nowrap', text: fmtTime(t.StartedAt) || '—' }),
        h('td', { class: 'muted nowrap', text: fmtTime(t.EndedAt) || '—' }),
        h('td', { class: 'col-actions' }, actions)
      ));
    });
    setChildren(tbody, [frag]);
  }

  async function cancelTask(id) {
    try {
      await invoke('CancelTask', id);
      toast('success', '已请求取消任务 ' + shortId(id, 12));
    } catch (e) { fail('CancelTask', e); }
    await loadTasks();
  }

  /* ========================== 16. 输出流（日志 / 终端 / 任务） ========================== */

  function storeKey(channel, ref) { return String(channel || '') + '|' + String(ref || ''); }

  function storePush(channel, ref, line) {
    var key = storeKey(channel, ref);
    var buf = state.store[key];
    if (!buf) { buf = { lines: [] }; state.store[key] = buf; }
    buf.lines.push(line);
    if (buf.lines.length > STORE_LINES) buf.lines.splice(0, buf.lines.length - STORE_LINES);
  }

  function lineNode(line) {
    var cls = 'ln' + (line.stream === 'stderr' ? ' ln-stderr' : line.stream === 'system' ? ' ln-system' : '');
    return h('span', { class: cls, text: (line.text === undefined || line.text === null ? '' : String(line.text)) + '\n' });
  }

  /* ---- 日志抽屉 ---- */

  function logLinesFor(refAliases, channels) {
    var out = [];
    for (var key in state.store) {
      if (!Object.prototype.hasOwnProperty.call(state.store, key)) continue;
      var parts = key.split('|');
      var ch = parts[0], ref = parts.slice(1).join('|');
      if (channels && channels.indexOf(ch) < 0) continue;
      if (refAliases && refAliases.length && !anyRefMatch(refAliases, ref)) continue;
      out = out.concat(state.store[key].lines);
    }
    out.sort(function (a, b) { return (a.seq || 0) - (b.seq || 0); });
    return out;
  }

  function renderLogLines() {
    var body = $('logs-body');
    if (!body || !state.logs) return;
    var lines = state.logs.lines;
    if (!lines.length) {
      setChildren(body, [h('span', { class: 'ln-empty', text: state.logs.hint || '（暂无输出）' })]);
    } else {
      setChildren(body, lines.map(lineNode));
    }
    autoScrollLogs();
  }

  function autoScrollLogs() {
    var body = $('logs-body');
    var auto = $('logs-autoscroll');
    if (!body) return;
    if (auto && auto.checked) body.scrollTop = body.scrollHeight;
  }

  function appendLogLines(newLines) {
    var body = $('logs-body');
    if (!body || !state.logs) return;
    if (state.logs.paused) return;
    var atBottom = Math.abs(body.scrollHeight - body.scrollTop - body.clientHeight) < 24;
    var auto = $('logs-autoscroll');
    var shouldStick = atBottom || !auto || auto.checked;
    var frag = document.createDocumentFragment();
    newLines.forEach(function (l) { frag.appendChild(lineNode(l)); });
    if (body.firstChild && body.firstChild.className === 'ln-empty') clear(body);
    body.appendChild(frag);
    while (body.childNodes.length > MAX_LINES) body.removeChild(body.firstChild);
    if (shouldStick) autoScrollLogs();
  }

  function openLogsDrawer(cfg) {
    var drawer = $('drawer-logs');
    var title = $('logs-title');
    var meta = $('logs-meta');
    if (!drawer) return;
    state.logs = {
      title: cfg.title,
      streamID: cfg.streamID || '',
      channels: cfg.channels || null,
      refAliases: cfg.refAliases || [],
      lines: cfg.lines || [],
      paused: false,
      hint: cfg.hint || ''
    };
    if (title) title.textContent = cfg.title;
    if (meta) meta.textContent = cfg.meta || '';
    drawer.hidden = false;
    var pause = $('logs-pause');
    if (pause) { pause.textContent = '暂停'; pause.setAttribute('aria-pressed', 'false'); }
    var body = $('logs-body');
    if (body) body.classList.remove('is-paused');
    renderLogLines();
    if (body) setTimeout(function () { body.focus(); }, 0);
    updateStatusLine();
  }

  function closeLogsDrawer(stopStream) {
    var drawer = $('drawer-logs');
    if (!drawer || drawer.hidden) return;
    var sid = state.logs ? state.logs.streamID : '';
    drawer.hidden = true;
    state.logs = null;
    if (stopStream && sid) {
      invoke('StopStream', sid).catch(function (e) { fail('StopStream', e); });
    }
    updateStatusLine();
  }

  async function openContainerLogs(c) {
    var ref = c.ID || containerName(c);
    var aliases = containerAliases(c);
    /* 先把已有缓冲显示出来，随后启动跟随流。 */
    openLogsDrawer({
      title: '日志 · ' + containerName(c),
      refAliases: aliases,
      channels: [CH.logs],
      lines: logLinesFor(aliases, [CH.logs]),
      hint: '正在启动日志流…',
      meta: '容器 ' + shortId(ref, 12)
    });
    try {
      var sid = await invoke('StartLogs', ref, { Follow: true, Tail: 200, Timestamps: false, Since: '', Until: '' });
      if (state.logs) {
        state.logs.streamID = sid || '';
        state.logs.hint = '（日志流已启动，等待输出…）';
        var meta = $('logs-meta');
        if (meta) meta.textContent = '流 ' + shortId(sid, 10);
        if (!state.logs.lines.length) renderLogLines();
      }
      toast('info', '日志流已启动：' + shortId(sid, 10));
    } catch (e) {
      fail('StartLogs', e);
      if (state.logs) {
        state.logs.hint = '日志流启动失败：' + normalizeError(e).message;
        renderLogLines();
      }
    }
    updateStatusLine();
  }

  async function openTaskOutput(taskId, title, channels) {
    var aliases = [taskId];
    /* 从后端返回的 Task.Output 里回填历史：某些任务（如事件流、环境自检）
       不走 wslc:output 事件通道，或者事件在打开抽屉之前到达，前端 store
       里没有它们的输出 —— 这时 Output 字段是唯一能拿到内容的方式。 */
    var t = (state.tasks || []).find(function (x) { return String(x.ID) === String(taskId); });
    var hist = (t && typeof t.Output === 'string' && t.Output.length) ? t.Output.split('\n') : [];
    var live = logLinesFor(aliases, channels || [CH.task, CH.build, CH.pull]);
    openLogsDrawer({
      title: title || ('任务输出 · ' + shortId(taskId, 12)),
      refAliases: aliases,
      channels: channels || [CH.task, CH.build, CH.pull],
      lines: hist.concat(live),
      hint: '（暂无输出；任务开始输出后会实时显示）',
      meta: '任务 ' + shortId(taskId, 12)
    });
  }

  /* ---- 终端抽屉 ---- */

  function renderTermLines() {
    var surface = $('term-surface');
    if (!surface || !state.term) return;
    var lines = state.term.lines;
    if (!lines.length) {
      setChildren(surface, [h('span', { class: 'ln-system', text: '（终端已启动，点击此处聚焦后可直接按键输入，或在下方输入整行命令）\n' })]);
    } else {
      setChildren(surface, lines.map(lineNode));
    }
    surface.scrollTop = surface.scrollHeight;
  }

  function appendTermLines(newLines) {
    var surface = $('term-surface');
    if (!surface || !state.term) return;
    var atBottom = Math.abs(surface.scrollHeight - surface.scrollTop - surface.clientHeight) < 24;
    if (surface.firstChild && surface.firstChild.className === 'ln-system') clear(surface);
    var frag = document.createDocumentFragment();
    newLines.forEach(function (l) { frag.appendChild(lineNode(l)); });
    surface.appendChild(frag);
    while (surface.childNodes.length > MAX_LINES) surface.removeChild(surface.firstChild);
    if (atBottom) surface.scrollTop = surface.scrollHeight;
  }

  async function openTerminal(c) {
    var ref = c.ID || containerName(c);
    var aliases = containerAliases(c);
    var cols = 100, rows = 30;
    var tty = $('term-tty') ? $('term-tty').checked : true;
    var drawer = $('drawer-terminal');
    if (!drawer) return;
    drawer.hidden = false;
    state.term = { streamID: '', ref: ref, aliases: aliases, lines: [], cols: cols, rows: rows };
    var title = $('term-title');
    if (title) title.textContent = '终端 · ' + containerName(c);
    var meta = $('term-meta');
    if (meta) meta.textContent = '启动中…';
    renderTermLines();
    try {
      var sid = await invoke('StartTerminal', ref,
        { TTY: tty, User: '', WorkDir: '', Env: [] }, cols, rows);
      if (state.term) {
        state.term.streamID = sid || '';
        if (meta) meta.textContent = '流 ' + shortId(sid, 10) + (tty ? ' · TTY' : ' · 非 TTY');
      }
      toast('info', '终端已连接：' + shortId(sid, 10));
    } catch (e) {
      fail('StartTerminal', e);
      if (state.term) state.term.lines.push({ stream: 'system', text: '终端启动失败：' + normalizeError(e).message });
      renderTermLines();
      if (meta) meta.textContent = '启动失败';
    }
    var surface = $('term-surface');
    if (surface) setTimeout(function () { surface.focus(); }, 0);
    updateStatusLine();
  }

  async function terminalWrite(data) {
    if (!state.term) { toast('warn', '终端未启动。'); return; }
    if (!state.term.streamID) { toast('warn', '终端流尚未就绪。'); return; }
    try {
      await invoke('TerminalWrite', state.term.streamID, data);
    } catch (e) { fail('TerminalWrite', e); }
  }

  function closeTerminal(stopStream) {
    var drawer = $('drawer-terminal');
    if (!drawer || drawer.hidden) return;
    var sid = state.term ? state.term.streamID : '';
    drawer.hidden = true;
    state.term = null;
    if (stopStream && sid) invoke('StopStream', sid).catch(function (e) { fail('StopStream', e); });
    updateStatusLine();
  }

  var resizeTerminal = debounce(function () {
    if (!state.term || !state.term.streamID) return;
    if ($('drawer-terminal') && $('drawer-terminal').hidden) return;
    var surface = $('term-surface');
    if (!surface) return;
    var cols = Math.max(20, Math.floor(surface.clientWidth / 7.2));
    var rows = Math.max(5, Math.floor(surface.clientHeight / 17));
    if (cols === state.term.cols && rows === state.term.rows) return;
    state.term.cols = cols; state.term.rows = rows;
    invoke('TerminalResize', state.term.streamID, cols, rows).catch(function (e) { fail('TerminalResize', e); });
  }, 250);

  /* ---- 事件路由 ---- */

  function onOutput(ev) {
    if (!ev || typeof ev !== 'object') return;
    var channel = String(ev.Channel || ev.channel || '');
    var ref = String(ev.Ref || ev.ref || '');
    var stream = String(ev.Stream || ev.stream || 'stdout');
    var text = ev.Text !== undefined ? ev.Text : (ev.text !== undefined ? ev.text : '');
    var seq = ev.Seq !== undefined ? ev.Seq : (ev.seq !== undefined ? ev.seq : 0);
    var line = { stream: stream, text: text, seq: Number(seq) || 0, time: ev.Time || ev.time || null };

    storePush(channel, ref, line);

    if (state.logs) {
      var chOk = !state.logs.channels || state.logs.channels.indexOf(channel) >= 0;
      var refOk = !state.logs.refAliases.length || anyRefMatch(state.logs.refAliases, ref);
      if (chOk && refOk) {
        state.logs.lines.push(line);
        if (state.logs.lines.length > MAX_LINES) state.logs.lines.splice(0, state.logs.lines.length - MAX_LINES);
        appendLogLines([line]);
      }
    }

    if (state.term && (channel === CH.terminal || channel === CH.logs) && anyRefMatch(state.term.aliases, ref)) {
      state.term.lines.push(line);
      if (state.term.lines.length > MAX_LINES) state.term.lines.splice(0, state.term.lines.length - MAX_LINES);
      appendTermLines([line]);
    }

    if (channel === CH.build || channel === CH.pull || channel === CH.task) {
      scheduleTasksRefresh();
    }
  }

  var tasksRefreshTimer = 0;
  function scheduleTasksRefresh() {
    if (tasksRefreshTimer) return;
    tasksRefreshTimer = setTimeout(function () {
      tasksRefreshTimer = 0;
      loadTasks();
    }, 600);
  }

  function subscribeEvents() {
    var runtime = rt();
    if (!runtime || typeof runtime.EventsOn !== 'function') return false;
    try {
      runtime.EventsOn(EVENT_NAME, onOutput);
      runtime.EventsOn(ENV_EVENT, function (env) {
        if (env && typeof env === 'object') {
          applyEnv(env);
          if (currentPanel() === 'env') renderEnv();
          state.loaded.env = true;
          state.loadedAt.env = Date.now();
          if (Array.isArray(env.Sessions) && env.Sessions.length) {
            state.sessions = env.Sessions;
            state.loaded.sessions = true;
            state.loadedAt.sessions = Date.now();
            if (currentPanel() === 'sessions') renderSessions();
          }
        }
      });
      return true;
    } catch (e) {
      fail('EventsOn', e);
      return false;
    }
  }

  /* ========================== 17. 全局清理动作 ========================== */

  function pruneContainersDialog() {
    confirmDialog({
      title: '清理停止的容器',
      message: '将删除所有已停止的容器。运行中的容器不受影响。此操作不可撤销。',
      detail: 'PruneContainers()',
      confirmLabel: '开始清理',
      danger: true
    }).then(function (r) {
      if (!r.confirmed) return;
      return invoke('PruneContainers').then(function (res) {
        var out = res && res.Stdout ? res.Stdout : safeJson(res);
        if (String(out).trim()) showTextModal('清理容器结果', String(out));
        else toast('info', '没有可清理的容器。');
        return loadContainers();
      }).catch(function (e) { fail('PruneContainers', e); });
    });
  }

  /* ========================== 18. 事件绑定 ========================== */

  function bindShell() {
    /* 标签页 */
    var tabs = document.querySelectorAll('.tab');
    Array.prototype.forEach.call(tabs, function (tab) {
      tab.addEventListener('click', function () { selectView(tab.dataset.view); });
      tab.addEventListener('keydown', function (ev) {
        var idx = VIEWS.indexOf(state.view);
        var next = -1;
        if (ev.key === 'ArrowRight') next = (idx + 1) % VIEWS.length;
        else if (ev.key === 'ArrowLeft') next = (idx - 1 + VIEWS.length) % VIEWS.length;
        else if (ev.key === 'Home') next = 0;
        else if (ev.key === 'End') next = VIEWS.length - 1;
        if (next < 0) return;
        ev.preventDefault();
        selectView(VIEWS[next]);
        var t = $('tab-' + VIEWS[next]);
        if (t) t.focus();
      });
    });

    /* 分组内的二级标签页 */
    Object.keys(GROUPS).forEach(function (view) {
      if (GROUPS[view].length < 2) return;
      GROUPS[view].forEach(function (panel) {
        var btn = $('subtab-' + view + '-' + panel);
        if (!btn) return;
        btn.addEventListener('click', function () { selectSubView(view, panel); });
        btn.addEventListener('keydown', function (ev) {
          var idx = GROUPS[view].indexOf(panel);
          var next = -1;
          if (ev.key === 'ArrowRight') next = (idx + 1) % GROUPS[view].length;
          else if (ev.key === 'ArrowLeft') next = (idx - 1 + GROUPS[view].length) % GROUPS[view].length;
          else if (ev.key === 'Home') next = 0;
          else if (ev.key === 'End') next = GROUPS[view].length - 1;
          if (next < 0) return;
          ev.preventDefault();
          selectSubView(view, GROUPS[view][next]);
          var t = $('subtab-' + view + '-' + GROUPS[view][next]);
          if (t) t.focus();
        });
      });
    });

    /* 顶栏 */
    var refresh = $('btn-refresh');
    if (refresh) refresh.addEventListener('click', refreshCurrentView);

    var theme = $('btn-theme');
    if (theme) theme.addEventListener('click', toggleTheme);

    var prune = $('btn-prune');
    if (prune) {
      prune.addEventListener('click', function () {
        openMenu(prune, [
          { type: 'label', label: '清理（破坏性）' },
          { label: '清理停止的容器', danger: true, title: 'PruneContainers（需确认）', onSelect: pruneContainersDialog },
          { label: '清理悬空镜像', danger: true, title: 'PruneImages(all=false)（需确认）', onSelect: pruneImagesDialog },
          { label: '清理全部未使用镜像…', danger: true, title: 'PruneImages(all=true)（需确认）', onSelect: function () {
            confirmDialog({
              title: '清理全部未使用镜像',
              message: '将删除所有未被容器引用的镜像（含中间层）。镜像需要重新拉取或构建才能恢复。',
              detail: 'PruneImages(all=true)',
              confirmLabel: '全部清理',
              danger: true,
              requireText: 'PRUNE'
            }).then(function (r) {
              if (!r.confirmed) return;
              return invoke('PruneImages', true).then(function (res) {
                var out = res && res.Stdout ? res.Stdout : safeJson(res);
                if (String(out).trim()) showTextModal('清理镜像结果', String(out));
                else toast('info', '没有可清理的镜像。');
                return loadImages();
              }).catch(function (e) { fail('PruneImages', e); });
            });
          } }
        ]);
      });
    }

    /* 容器工具条 */
    var segs = document.querySelectorAll('.seg');
    Array.prototype.forEach.call(segs, function (seg) {
      seg.addEventListener('click', function () {
        state.ct.scope = seg.dataset.scope;
        Array.prototype.forEach.call(segs, function (s) {
          var on = s.dataset.scope === state.ct.scope;
          s.classList.toggle('is-active', on);
          s.setAttribute('aria-pressed', on ? 'true' : 'false');
        });
        loadContainers();
      });
    });

    var search = $('ct-search');
    if (search) {
      search.addEventListener('input', debounce(function () {
        state.query = search.value;
        loadContainers();
      }, 220));
      search.addEventListener('keydown', function (ev) {
        if (ev.key === 'Escape' && search.value) {
          ev.stopPropagation();
          search.value = '';
          state.query = '';
          loadContainers();
        }
      });
    }

    var statsToggle = $('ct-stats');
    if (statsToggle) {
      statsToggle.addEventListener('change', function () {
        state.ct.stats = statsToggle.checked;
        if (statsToggle.checked) {
          invoke('ContainerStats', true).then(function (list) {
            applyStats(list);
            renderContainers();
          }).catch(function (e) {
            state.ct.statsMap = null;
            state.ct.stats = false;
            statsToggle.checked = false;
            fail('ContainerStats', e);
            renderContainers();
          });
        } else {
          state.ct.statsMap = null;
          renderContainers();
        }
      });
    }

    var ctRun = $('btn-ct-run');
    if (ctRun) ctRun.addEventListener('click', runContainerDialog);
    var ctReload = $('btn-ct-reload');
    if (ctReload) ctReload.addEventListener('click', function () { loadContainers(); });

    /* 容器表格：行内操作 + 键盘 */
    var ctBody = $('ct-body');
    if (ctBody) {
      ctBody.addEventListener('click', function (ev) {
        var btn = ev.target.closest ? ev.target.closest('button[data-act]') : null;
        if (!btn) return;
        var tr = btn.closest('tr');
        if (!tr) return;
        var c = findContainer(tr.dataset.ref);
        if (!c) return;
        var ref = c.ID || containerName(c);
        var nm = containerName(c);
        var act = btn.dataset.act;
        if (act === 'start') containerAction(ref, nm, '启动', function () { return invoke('StartContainer', ref); });
        else if (act === 'stop') containerAction(ref, nm, '停止', function () { return invoke('StopContainer', ref, 10); });
        else if (act === 'restart') containerAction(ref, nm, '重启', function () { return invoke('RestartContainer', ref, 10); });
        else if (act === 'logs') openContainerLogs(c);
        else if (act === 'terminal') openTerminal(c);
      });
      ctBody.addEventListener('keydown', function (ev) {
        var tr = ev.target.closest ? ev.target.closest('tr[data-ref]') : null;
        if (!tr) return;
        var c = findContainer(tr.dataset.ref);
        if (!c) return;
        if (ev.key === 'Enter') {
          ev.preventDefault();
          inspectContainer(c.ID || containerName(c));
        } else if (ev.key === 'F10' && ev.shiftKey) {
          ev.preventDefault();
          var more = tr.querySelector('button[aria-haspopup="menu"]');
          if (more) openContainerMenu(more, c);
        }
      });
      ctBody.addEventListener('contextmenu', function (ev) {
        var tr = ev.target.closest ? ev.target.closest('tr[data-ref]') : null;
        if (!tr) return;
        var c = findContainer(tr.dataset.ref);
        if (!c) return;
        ev.preventDefault();
        var more = tr.querySelector('button[aria-haspopup="menu"]');
        if (more) openContainerMenu(more, c);
      });
    }

    /* 镜像 */
    var imgAll = $('img-all');
    if (imgAll) imgAll.addEventListener('change', loadImages);
    var imgReload = $('btn-img-reload');
    if (imgReload) imgReload.addEventListener('click', loadImages);
    var imgPull = $('btn-img-pull');
    if (imgPull) imgPull.addEventListener('click', pullImage);
    var imgPullRef = $('img-pull-ref');
    if (imgPullRef) imgPullRef.addEventListener('keydown', function (ev) { if (ev.key === 'Enter') { ev.preventDefault(); pullImage(); } });
    var imgPullPresets = $('img-pull-presets');
    if (imgPullPresets) {
      fillPullPresets(imgPullPresets);
      imgPullPresets.addEventListener('change', function () {
        var v = imgPullPresets.value;
        if (!v) return;
        if (imgPullRef) imgPullRef.value = v;
        pullImage();
        imgPullPresets.value = '';
      });
    }
    var imgPrune = $('btn-img-prune');
    if (imgPrune) imgPrune.addEventListener('click', pruneImagesDialog);
    var imgExport = $('btn-img-export');
    if (imgExport) imgExport.addEventListener('click', imageExportDialog);
    var imgImport = $('btn-img-import');
    if (imgImport) imgImport.addEventListener('click', imageImportDialog);
    var buildForm = $('build-form');
    if (buildForm) buildForm.addEventListener('submit', buildImage);

    var imgBody = $('img-body');
    if (imgBody) {
      imgBody.addEventListener('keydown', function (ev) {
        var tr = ev.target.closest ? ev.target.closest('tr[data-ref]') : null;
        if (!tr || ev.key !== 'Enter') return;
        ev.preventDefault();
        inspectImage(tr.dataset.ref);
      });
    }

    /* 会话 */
    var sessReload = $('btn-sess-reload');
    if (sessReload) sessReload.addEventListener('click', loadSessions);
    var sessBody = $('sess-body');
    if (sessBody) {
      sessBody.addEventListener('keydown', function (ev) {
        var tr = ev.target.closest ? ev.target.closest('tr[data-id]') : null;
        if (!tr || ev.key !== 'Enter') return;
        ev.preventDefault();
        var sid = tr.dataset.id;
        var hit = state.sessions.filter(function (s) { return String(s.ID) === sid; })[0];
        if (hit) showTextModal('会话详情 · ' + (hit.Name || sid), JSON.stringify(hit, null, 2));
      });
    }

    /* 会话存储 */
    var storReload = $('btn-stor-reload');
    if (storReload) storReload.addEventListener('click', loadStorage);

    /* 卷 */
    var volForm = $('vol-form');
    if (volForm) volForm.addEventListener('submit', createVolume);
    var volReload = $('btn-vol-reload');
    if (volReload) volReload.addEventListener('click', loadVolumes);
    var volBody = $('vol-body');
    if (volBody) bindRowDetail(volBody, '卷', function (ref) {
      return state.volumes.filter(function (v) { return String(v.Name) === String(ref); })[0];
    });

    /* 网络 */
    var netForm = $('net-form');
    if (netForm) netForm.addEventListener('submit', createNetwork);
    var netReload = $('btn-net-reload');
    if (netReload) netReload.addEventListener('click', loadNetworks);
    var netBody = $('net-body');
    if (netBody) bindRowDetail(netBody, '网络', function (ref) {
      return state.networks.filter(function (n) { return String(n.Name) === String(ref); })[0];
    });

    /* 环境 */
    var envCheck = $('btn-env-check');
    if (envCheck) envCheck.addEventListener('click', loadEnv);
    var envAuto = $('env-auto');
    if (envAuto) envAuto.addEventListener('change', function () {
      if (envAuto.checked) {
        state.autoRefresh.env = setInterval(loadEnv, 15000);
        toast('info', '环境自检将每 15 秒自动执行。');
      } else if (state.autoRefresh.env) {
        clearInterval(state.autoRefresh.env);
        state.autoRefresh.env = 0;
      }
    });

    /* 任务 */
    var taskReload = $('btn-task-reload');
    if (taskReload) taskReload.addEventListener('click', loadTasks);
    var taskBody = $('task-body');
    if (taskBody) {
      taskBody.addEventListener('keydown', function (ev) {
        var tr = ev.target.closest ? ev.target.closest('tr[data-ref]') : null;
        if (!tr || ev.key !== 'Enter') return;
        ev.preventDefault();
        var t = state.tasks.filter(function (x) { return String(x.ID) === tr.dataset.ref; })[0];
        if (t) showTextModal('任务详情 · ' + shortId(t.ID, 12), JSON.stringify(t, null, 2));
      });
    }

    /* 设置 */
    var settingsSave = $('btn-settings-save');
    if (settingsSave) settingsSave.addEventListener('click', saveSettings);
    var settingsReload = $('btn-settings-reload');
    if (settingsReload) settingsReload.addEventListener('click', loadSettings);
    watchSettingsDirty();

    /* 日志抽屉 */
    var logsClose = $('logs-close');
    if (logsClose) logsClose.addEventListener('click', function () { closeLogsDrawer(true); });
    var logsClear = $('logs-clear');
    if (logsClear) logsClear.addEventListener('click', function () {
      if (!state.logs) return;
      state.logs.lines = [];
      renderLogLines();
    });
    var logsStop = $('logs-stop');
    if (logsStop) logsStop.addEventListener('click', async function () {
      if (!state.logs || !state.logs.streamID) { toast('info', '当前没有活动日志流。'); return; }
      var sid = state.logs.streamID;
      try {
        await invoke('StopStream', sid);
        if (state.logs) state.logs.streamID = '';
        var meta = $('logs-meta');
        if (meta) meta.textContent = '流已停止';
        toast('success', '日志流已停止：' + shortId(sid, 10));
      } catch (e) { fail('StopStream', e); }
      updateStatusLine();
    });
    var logsPause = $('logs-pause');
    if (logsPause) logsPause.addEventListener('click', function () {
      if (!state.logs) return;
      state.logs.paused = !state.logs.paused;
      logsPause.textContent = state.logs.paused ? '继续' : '暂停';
      logsPause.setAttribute('aria-pressed', state.logs.paused ? 'true' : 'false');
      var body = $('logs-body');
      if (body) body.classList.toggle('is-paused', state.logs.paused);
      if (!state.logs.paused) renderLogLines();
      else toast('info', '日志显示已暂停；后端流仍在继续，恢复后会补齐缓冲。');
    });

    /* 终端抽屉 */
    var termClose = $('term-close');
    if (termClose) termClose.addEventListener('click', function () { closeTerminal(true); });
    var termStop = $('term-stop');
    if (termStop) termStop.addEventListener('click', async function () {
      if (!state.term || !state.term.streamID) { toast('info', '当前没有终端流。'); return; }
      var sid = state.term.streamID;
      try {
        await invoke('StopStream', sid);
        if (state.term) state.term.streamID = '';
        var meta = $('term-meta');
        if (meta) meta.textContent = '流已停止';
        toast('success', '终端流已停止：' + shortId(sid, 10));
      } catch (e) { fail('StopStream', e); }
      updateStatusLine();
    });
    var termClear = $('term-clear');
    if (termClear) termClear.addEventListener('click', function () {
      if (!state.term) return;
      state.term.lines = [];
      renderTermLines();
    });
    var termCtrc = $('term-ctrlc');
    if (termCtrc) termCtrc.addEventListener('click', function () { terminalWrite('\u0003'); });
    var termForm = $('term-form');
    if (termForm) termForm.addEventListener('submit', function (ev) {
      ev.preventDefault();
      var line = $('term-line');
      if (!line) return;
      var v = line.value;
      line.value = '';
      terminalWrite(v + '\r');
    });

    var surface = $('term-surface');
    if (surface) {
      surface.addEventListener('keydown', function (ev) {
        if (!state.term) return;
        var data = null;
        if (ev.key === 'Enter') data = '\r';
        else if (ev.key === 'Backspace') data = '\u007f';
        else if (ev.key === 'Tab') data = '\t';
        else if (ev.key === 'Escape') data = '\u001b';
        else if (ev.key === 'ArrowUp') data = '\u001b[A';
        else if (ev.key === 'ArrowDown') data = '\u001b[B';
        else if (ev.key === 'ArrowRight') data = '\u001b[C';
        else if (ev.key === 'ArrowLeft') data = '\u001b[D';
        else if (ev.ctrlKey && ev.key.length === 1) data = String.fromCharCode(ev.key.toUpperCase().charCodeAt(0) - 64);
        else if (ev.key.length === 1) data = ev.key;
        if (data === null) return;
        ev.preventDefault();
        terminalWrite(data);
      });
    }
    window.addEventListener('resize', resizeTerminal);

    /* 状态栏错误 */
    var statusErr = $('status-err');
    if (statusErr) statusErr.addEventListener('click', showErrorsModal);

    /* 全局快捷键 */
    document.addEventListener('keydown', function (ev) {
      if (ev.key === 'F5') {
        ev.preventDefault();
        refreshCurrentView();
        return;
      }
      if (ev.key === 'Escape') {
        var menuRoot = $('menu-root');
        if (menuRoot && !menuRoot.hidden) { closeMenu(); return; }
        var modalRoot = $('modal-root');
        if (modalRoot && !modalRoot.hidden) return; /* 由模态自身处理 */
        var termDrawer = $('drawer-terminal');
        if (termDrawer && !termDrawer.hidden) { closeTerminal(true); return; }
        var logDrawer = $('drawer-logs');
        if (logDrawer && !logDrawer.hidden && document.activeElement !== $('ct-search')) { closeLogsDrawer(true); }
      }
    });

    /* 未捕获异常统一呈现，绝不静默 */
    window.addEventListener('error', function (ev) {
      fail('未捕获异常', ev.error || ev.message);
    });
    window.addEventListener('unhandledrejection', function (ev) {
      var reason = ev.reason;
      if (reason && reason.code === 'BACKEND_UNAVAILABLE') return; /* 已由横幅说明 */
      fail('未处理的 Promise 拒绝', reason);
    });
  }

  /* 卷 / 网络表格：Enter 打开详情（键盘可达）。 */
  function bindRowDetail(tbody, kind, lookup) {
    tbody.addEventListener('keydown', function (ev) {
      var tr = ev.target.closest ? ev.target.closest('tr[data-ref]') : null;
      if (!tr || ev.key !== 'Enter') return;
      ev.preventDefault();
      var item = lookup(tr.dataset.ref);
      if (!item) return;
      showTextModal(kind + '详情 · ' + tr.dataset.ref, JSON.stringify(item, null, 2));
    });
  }

  function findContainer(ref) {
    for (var i = 0; i < state.containers.length; i++) {
      var c = state.containers[i];
      if (String(c.ID) === String(ref)) return c;
    }
    for (var j = 0; j < state.containers.length; j++) {
      if (sameRef(state.containers[j].ID, ref)) return state.containers[j];
    }
    return null;
  }

  /* ========================== 19. 主题 ========================== */

  function applyTheme(theme) {
    state.theme = theme === 'light' ? 'light' : 'dark';
    document.documentElement.setAttribute('data-theme', state.theme);
    var btn = $('btn-theme');
    if (btn) {
      btn.setAttribute('aria-pressed', state.theme === 'light' ? 'true' : 'false');
      btn.title = state.theme === 'light' ? '切换到深色主题' : '切换到浅色主题';
    }
    try { localStorage.setItem('wslc.theme', state.theme); } catch (e) { /* 隐私模式 */ }
  }

  function toggleTheme() {
    applyTheme(state.theme === 'dark' ? 'light' : 'dark');
  }

  function initTheme() {
    var saved = null;
    try { saved = localStorage.getItem('wslc.theme'); } catch (e) { saved = null; }
    if (saved === 'light' || saved === 'dark') { applyTheme(saved); return; }
    var prefersLight = false;
    try {
      prefersLight = !!(window.matchMedia && window.matchMedia('(prefers-color-scheme: light)').matches);
    } catch (e) { prefersLight = false; }
    applyTheme(prefersLight ? 'light' : 'dark');
  }

  /* ========================== 20. 启动 ========================== */

  /* 解析 #/view 或 #/view/panel 形式的地址栏。 */
  function initFromHash() {
    var view = 'containers';
    var panel = null;
    try {
      var hash = String(location.hash || '');
      var m = hash.match(/^#\/([a-z]+)(?:\/([a-z]+))?$/);
      if (m) {
        view = VIEWS.indexOf(m[1]) >= 0 ? m[1] : 'containers';
        if (m[2] && GROUPS[m[1]] && GROUPS[m[1]].indexOf(m[2]) >= 0) panel = m[2];
      }
    } catch (e) { view = 'containers'; }
    if (panel) SUB[view] = panel;
    return view;
  }

  async function boot() {
    initTheme();
    bindShell();

    /* 防御性初始化：在任何异步操作之前，先把所有视图和面板隐藏，
       只保留默认视图 containers。这能防止 WebView2 用缓存旧版
       HTML（其中部分 section 没有 hidden 属性）时出现多个面板
       同时可见的情况。selectView 之后会重新设置正确的可见性。 */
    VIEWS.forEach(function (v) {
      var sec = $('view-' + v);
      if (sec) sec.hidden = (v !== 'containers');
    });
    Object.keys(GROUPS).forEach(function (v) {
      GROUPS[v].forEach(function (panel) {
        var el = panelEl(panel);
        if (el) el.hidden = !(v === 'containers' && panel === 'containers');
      });
    });

    var mod = await loadBackend();
    if (mod) backend.mode = 'wails';
    else if (goApp()) backend.mode = 'fallback';
    else backend.mode = 'unavailable';

    probeBindings(mod);

    if (backend.mode === 'unavailable') setBackendBanner('unavailable');
    else if (backend.mode === 'fallback') setBackendBanner('fallback');
    else if (!hasRuntime()) setBackendBanner('no-runtime');
    updateBackendPill();

    var runtimeOk = subscribeEvents();
    if (hasRuntime() && !runtimeOk) setBackendBanner('no-runtime');

    selectView(initFromHash());

    /* 环境自检优先：它决定其它视图是否需要提示「服务未就绪」。 */
    loadEnv().then(function () { touchLast(); });
    loadTasks();
    loadContainers();

    /* 事件流（best-effort） */
    invoke('StreamEvents').catch(function (e) {
      var info = normalizeError(e);
      if (info.code !== 'BACKEND_UNAVAILABLE') {
        toast('warn', '事件流未启动：' + info.message);
      }
    });

    /* 任务自动刷新：仅在任务视图可见时请求后端。 */
    setInterval(function () {
      var cb = $('task-auto');
      if (!cb || !cb.checked) return;
      if (state.view !== 'tasks') return;
      if (document.visibilityState === 'hidden') return;
      if (state.loading.tasks) return;
      loadTasks();
    }, 2000);

    /* 面板超过 60s 未更新时，窗口重新聚焦后自动刷新当前面板。 */
    window.addEventListener('focus', function () {
      var panel = currentPanel();
      if (state.loaded[panel] && viewAge(panel) > VIEW_STALE_MS * 3) loadPanel(panel);
    });
  }

  if (document.readyState === 'loading') {
    document.addEventListener('DOMContentLoaded', function () { boot(); });
  } else {
    boot();
  }

  /* 诊断用：在控制台执行 __wslc.diag() 可查看绑定状态。 */
  window.__wslc = {
    diag: function () {
      return {
        mode: backend.mode,
        runtime: hasRuntime(),
        goBridge: !!goApp(),
        missingBindings: backend.missing,
        expected: BACKEND_METHODS,
        view: state.view
      };
    },
    expectedMethods: BACKEND_METHODS,
    state: state
  };
})();
