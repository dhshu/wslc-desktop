/* =============================================================================
 * frontend/tools/smoke.mjs —— 无依赖 DOM 冒烟测试（不是构建步骤）
 *
 *   node frontend/tools/smoke.mjs
 *
 * 场景 A：无 Wails 运行时 + 无后端绑定（最坏情况）
 *   · app.js 顶层与 boot() 不抛未捕获异常
 *   · 显示「后端未就绪」横幅（不白屏）
 *   · 六个视图都能激活并渲染各自的错误态
 *   · 错误进入 toast + 状态栏计数（不静默吞掉）
 *
 * 场景 B：注入伪后端 + 伪 Wails 运行时（有绑定）
 *   · 容器/镜像/卷/网络/环境/任务六个视图正常渲染真实行
 *   · 破坏性操作（删除容器 / kill / prune / 删卷）必须先弹二次确认，Esc 取消后不调用后端
 *   · 日志抽屉：StartLogs + 事件流渲染 + StopStream
 *   · 终端抽屉：StartTerminal + TerminalWrite（按键与整行）+ StopStream
 *   · 环境自检：EnvStatus 全字段 + Problems + vmcompute 修复建议
 *
 * 这是一个测试替身，不替代真实浏览器验证；它覆盖的是「逻辑与状态机」。
 * ========================================================================== */

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';
import vm from 'node:vm';

const here = dirname(fileURLToPath(import.meta.url));
const frontend = join(here, '..');
const appJs = readFileSync(join(frontend, 'app.js'), 'utf8');
const indexHtml = readFileSync(join(frontend, 'index.html'), 'utf8');

/* ============================== 极简 DOM ============================== */

class DomNode {}
class DomText extends DomNode {
  constructor(text) { super(); this.nodeType = 3; this.textContent = String(text); this.parentNode = null; }
}

/* 迷你选择器匹配：支持 tag / .class / #id / [attr] / [attr="v"] / :not(简单选择器) */
function matchesSimple(el, sel) {
  sel = String(sel || '').trim();
  if (!sel) return true;
  let i = 0;
  const tagM = sel.match(/^[a-zA-Z][a-zA-Z0-9-]*/);
  if (tagM) {
    if (el.tagName !== tagM[0].toUpperCase()) return false;
    i = tagM[0].length;
  }
  while (i < sel.length) {
    const rest = sel.slice(i);
    let m;
    if ((m = rest.match(/^\.([A-Za-z0-9_-]+)/))) {
      if (!el._classes.includes(m[1])) return false;
      i += m[0].length;
    } else if ((m = rest.match(/^#([A-Za-z0-9_-]+)/))) {
      if (el.id !== m[1]) return false;
      i += m[0].length;
    } else if ((m = rest.match(/^\[([A-Za-z0-9_-]+)(?:([~^$*|]?=)"([^"]*)")?\]/))) {
      const attr = m[1], op = m[2], val = m[3];
      const base = attr.startsWith('data-') ? attr.slice(5) : attr;
      const camel = base.replace(/-([a-z])/g, (_, c) => c.toUpperCase());
      const has = Object.prototype.hasOwnProperty.call(el.attributes, attr) || (camel in el.dataset);
      if (!has) return false;
      if (op && op.endsWith('=')) {
        const actual = el.attributes[attr] !== undefined ? el.attributes[attr] : el.dataset[camel];
        if (String(actual) !== val) return false;
      }
      i += m[0].length;
    } else if ((m = rest.match(/^:not\(([^)]*)\)/))) {
      if (matchesSimple(el, m[1])) return false;
      i += m[0].length;
    } else break;
  }
  return true;
}
function matchesAny(el, sel) {
  return String(sel).split(',').some((s) => matchesSimple(el, s));
}

class DomEl extends DomNode {
  constructor(tag) {
    super();
    this.nodeType = 1;
    this.tagName = String(tag || 'div').toUpperCase();
    this.childNodes = [];
    this.parentNode = null;
    this.dataset = {};
    this.style = {};
    this.attributes = {};
    this._classes = [];
    this._listeners = {};
    this.className = '';
    this.textContent = '';
    this.value = '';
    this.checked = false;
    this.disabled = false;
    this.hidden = false;
    this.tabIndex = 0;
    this.title = '';
    this.isConnected = true;
    this.scrollTop = 0;
    this.scrollHeight = 0;
    this.clientHeight = 600;
    this.clientWidth = 800;
    this.offsetWidth = 180;
    this.offsetHeight = 120;
    this.classList = {
      add: (...c) => { c.forEach((x) => { if (!this._classes.includes(x)) this._classes.push(x); }); this._sync(); },
      remove: (...c) => { this._classes = this._classes.filter((x) => !c.includes(x)); this._sync(); },
      toggle: (c, force) => {
        const has = this._classes.includes(c);
        const on = force === undefined ? !has : !!force;
        if (on && !has) this._classes.push(c);
        if (!on && has) this._classes = this._classes.filter((x) => x !== c);
        this._sync();
        return on;
      },
      contains: (c) => this._classes.includes(c)
    };
  }
  _sync() { this.className = this._classes.join(' '); }
  /* className 与 classList/_classes 保持同步（app.js 的 h() 直接写 className）。 */
  get className() { return this._className || ''; }
  set className(v) {
    this._className = String(v === null || v === undefined ? '' : v);
    this._classes = this._className.split(/\s+/).filter(Boolean);
  }
  get firstChild() { return this.childNodes[0] || null; }
  get lastChild() { return this.childNodes[this.childNodes.length - 1] || null; }
  get children() { return this.childNodes.filter((n) => n.nodeType === 1); }
  appendChild(node) {
    if (node && node.nodeType === 11) {
      node.childNodes.slice().forEach((c) => this.appendChild(c));
      node.childNodes = [];
      return node;
    }
    if (node) node.parentNode = this;
    this.childNodes.push(node);
    return node;
  }
  removeChild(node) {
    const i = this.childNodes.indexOf(node);
    if (i >= 0) this.childNodes.splice(i, 1);
    if (node) node.parentNode = null;
    return node;
  }
  append(...nodes) { nodes.forEach((n) => this.appendChild(n)); }
  setAttribute(k, v) { this.attributes[k] = String(v); if (k === 'id') this.id = String(v); }
  getAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attributes, k) ? this.attributes[k] : null; }
  removeAttribute(k) { delete this.attributes[k]; }
  hasAttribute(k) { return Object.prototype.hasOwnProperty.call(this.attributes, k); }
  addEventListener(type, fn) { (this._listeners[type] = this._listeners[type] || []).push(fn); }
  removeEventListener(type, fn) {
    const l = this._listeners[type];
    if (!l) return;
    const i = l.indexOf(fn);
    if (i >= 0) l.splice(i, 1);
  }
  querySelector(sel) {
    for (const s of String(sel).split(',')) {
      const found = this._find((el) => matchesSimple(el, s));
      if (found) return found;
    }
    return null;
  }
  querySelectorAll(sel) {
    const out = [];
    this._walk((el) => { if (matchesAny(el, sel)) out.push(el); });
    return out;
  }
  _walk(fn) {
    for (const c of this.childNodes) {
      if (c.nodeType !== 1) continue;
      fn(c);
      c._walk(fn);
    }
  }
  _find(pred) {
    let hit = null;
    this._walk((el) => { if (!hit && pred(el)) hit = el; });
    return hit;
  }
  closest(sel) {
    let cur = this;
    while (cur && cur.nodeType === 1) {
      if (matchesAny(cur, sel)) return cur;
      cur = cur.parentNode;
    }
    return null;
  }
  focus() { this._focused = true; }
  blur() {}
  contains() { return false; }
  getBoundingClientRect() { return { left: 0, top: 0, right: 180, bottom: 24, width: 180, height: 24 }; }
  scrollIntoView() {}
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
}

/* DocumentFragment：复用 DomEl 的树能力，nodeType=11（appendChild 会展开子节点） */
class DomFragment extends DomEl {
  constructor() { super('#fragment'); this.nodeType = 11; this.tagName = '#FRAGMENT'; }
}

/* ============================== 环境工厂 ============================== */

function makeEnv(opts) {
  const allElements = [];
  const byId = new Map();

  const tagRe = /<([a-zA-Z][a-zA-Z0-9-]*)((?:"[^"]*"|[^>"])*)>/g;
  let m;
  while ((m = tagRe.exec(indexHtml)) !== null) {
    const tag = m[1].toLowerCase();
    const attrs = m[2] || '';
    if (['link', 'meta', 'br', 'hr', 'noscript'].includes(tag)) continue;
    const el = new DomEl(tag);

    const idm = attrs.match(/\bid="([^"]*)"/);
    if (idm) { el.setAttribute('id', idm[1]); byId.set(idm[1], el); }

    const cm = attrs.match(/\bclass="([^"]*)"/);
    if (cm) { el._classes = cm[1].split(/\s+/).filter(Boolean); el._sync(); }

    for (const dm of attrs.matchAll(/\bdata-([a-z0-9-]+)="([^"]*)"/g)) {
      el.dataset[dm[1].replace(/-([a-z])/g, (_, c) => c.toUpperCase())] = dm[2];
    }
    if (/\bdata-stats-col\b/.test(attrs) && !('statsCol' in el.dataset)) el.dataset.statsCol = '';

    if (/\bhidden\b/.test(attrs)) el.hidden = true;
    if (tag === 'input') {
      if (/\bchecked\b/.test(attrs)) el.checked = true;
      const tm = attrs.match(/\btype="([^"]*)"/);
      el.type = tm ? tm[1] : 'text';
    }
    if (/\bdisabled\b/.test(attrs)) el.disabled = true;
    const vm2 = attrs.match(/\bvalue="([^"]*)"/);
    if (vm2) el.value = vm2[1];

    allElements.push(el);
  }

  const docListeners = {};

  /* 动态创建（模态内表单）的元素不在静态索引里：按 id 深度搜索静态根节点子树。 */
  function deepFindById(id) {
    for (const root of allElements) {
      if (root.id === id) return root;
      const hit = root._find((e) => e.id === id);
      if (hit) return hit;
    }
    return null;
  }

  const doc = {
    readyState: 'complete',
    visibilityState: 'visible',
    title: '',
    activeElement: null,
    documentElement: new DomEl('html'),
    body: new DomEl('body'),
    createElement: (t) => new DomEl(t),
    createTextNode: (t) => new DomText(t),
    createDocumentFragment: () => new DomFragment(),
    getElementById: (id) => byId.get(id) || deepFindById(id) || null,
    querySelector: (sel) => {
      if (sel.startsWith('#')) return byId.get(sel.slice(1)) || null;
      if (sel.startsWith('.')) return allElements.find((e) => e._classes.includes(sel.slice(1))) || null;
      return allElements.find((e) => matchesAny(e, sel)) || null;
    },
    querySelectorAll: (sel) => allElements.filter((e) => matchesAny(e, sel)),
    addEventListener: (type, fn) => { (docListeners[type] = docListeners[type] || []).push(fn); },
    removeEventListener: (type, fn) => {
      const l = docListeners[type];
      if (!l) return;
      const i = l.indexOf(fn);
      if (i >= 0) l.splice(i, 1);
    }
  };

  const storage = new Map();
  const locationObj = { hash: '', href: 'http://localhost/', reload: () => {} };
  const historyObj = { replaceState: (_a, _b, url) => { if (url) locationObj.hash = String(url); } };

  const win = {
    document: doc,
    innerWidth: 1400,
    innerHeight: 900,
    devicePixelRatio: 1,
    location: locationObj,
    history: historyObj,
    navigator: { clipboard: null, userAgent: 'node-smoke' },
    localStorage: {
      getItem: (k) => (storage.has(k) ? storage.get(k) : null),
      setItem: (k, v) => storage.set(k, String(v)),
      removeItem: (k) => storage.delete(k)
    },
    matchMedia: () => ({ matches: false, addEventListener: () => {}, addListener: () => {} }),
    addEventListener: () => {},
    removeEventListener: () => {},
    getComputedStyle: () => ({ getPropertyValue: () => '' }),
    CSS: { escape: (s) => String(s).replace(/["\\]/g, '\\$&') },
    console
  };
  if (opts && opts.runtime) win.runtime = opts.runtime;
  if (opts && opts.go) win.go = { main: { App: opts.go } };

  const sandbox = {
    window: win,
    document: doc,
    location: locationObj,
    history: historyObj,
    navigator: win.navigator,
    localStorage: win.localStorage,
    CSS: win.CSS,
    console,
    Node: DomNode,
    setTimeout,
    clearTimeout,
    setInterval,
    clearInterval,
    Date,
    JSON,
    Math
  };
  sandbox.globalThis = sandbox;
  sandbox.self = win;

  const context = vm.createContext(sandbox);

  function textOf(node) {
    if (!node) return '';
    if (node.nodeType === 3) return node.textContent;
    let out = node.textContent || '';
    for (const c of node.childNodes || []) out += textOf(c);
    return out;
  }
  function findAll(root, pred) {
    const out = [];
    const walk = (n) => {
      for (const c of n.childNodes || []) {
        if (c.nodeType !== 1) continue;
        if (pred(c)) out.push(c);
        walk(c);
      }
    };
    walk(root);
    return out;
  }
  function byText(root, tag, text) {
    return findAll(root, (e) => e.tagName === tag.toUpperCase() && textOf(e).trim() === text)[0] || null;
  }
  function buttonsWithAct(root, act) {
    return findAll(root, (e) => e.tagName === 'BUTTON' && e.dataset.act === act);
  }
  function fire(el, type, extra) {
    const ev = Object.assign({
      type, target: el, currentTarget: el,
      preventDefault() {}, stopPropagation() {}, stopImmediatePropagation() {},
      key: '', shiftKey: false, ctrlKey: false, altKey: false
    }, extra || {});
    /* 冒泡：从目标向上调用监听器 */
    let cur = el;
    while (cur) {
      ev.currentTarget = cur;
      for (const fn of (cur._listeners[type] || []).slice()) fn(ev);
      cur = cur.parentNode;
    }
  }
  function fireDoc(type, extra) {
    const ev = Object.assign({
      type, target: doc, preventDefault() {}, stopPropagation() {}, stopImmediatePropagation() {},
      key: '', shiftKey: false, ctrlKey: false, altKey: false
    }, extra || {});
    for (const fn of (docListeners[type] || []).slice()) fn(ev);
  }
  function clickTab(view) { fire(byId.get('tab-' + view), 'click'); }

  return { win, doc, byId, allElements, context, textOf, findAll, byText, buttonsWithAct, fire, fireDoc, clickTab, getEl: (id) => doc.getElementById(id) };
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

/* ============================== 断言收集 ============================== */

function makeReporter(title) {
  const checks = [];
  const failures = [];
  return {
    title,
    check(name, cond, extra) {
      checks.push({ name, ok: !!cond, extra });
      if (!cond) failures.push(name + (extra !== undefined ? ` — ${extra}` : ''));
    },
    dump() {
      console.log(`\n=== ${title} ===`);
      for (const c of checks) console.log(`  ${c.ok ? '✓' : '✗'} ${c.name}${c.ok || c.extra === undefined ? '' : '  [' + c.extra + ']'}`);
      if (failures.length) {
        console.log(`  FAIL（${failures.length}/${checks.length}）`);
        for (const f of failures) console.log('    ✗ ' + f);
      } else {
        console.log(`  PASS：${checks.length}/${checks.length} 项`);
      }
      return failures.length;
    }
  };
}

let totalFailures = 0;

/* ====================== 场景 A：无后端 / 无运行时 ====================== */

{
  const env = makeEnv({});
  const rep = makeReporter('场景 A：无 Wails 运行时 + 无后端绑定（最坏情况）');
  let bootError = null;
  try {
    vm.runInContext(appJs, env.context, { filename: 'frontend/app.js' });
  } catch (e) { bootError = e; }
  await sleep(60);

  rep.check('app.js 顶层执行无异常', !bootError, bootError && (bootError.stack || bootError.message));

  const diag = env.win.__wslc && env.win.__wslc.diag ? env.win.__wslc.diag() : null;
  rep.check('window.__wslc.diag() 可用', !!diag);
  rep.check('后端模式 = unavailable', diag && diag.mode === 'unavailable', diag && diag.mode);
  rep.check('期望方法数 = 35', diag && diag.expected && diag.expected.length === 35, diag && diag.expected && diag.expected.length);

  const bannerText = env.textOf(env.byId.get('banner-root'));
  rep.check('显示「后端未就绪」横幅（不白屏）', /后端未就绪/.test(bannerText));
  rep.check('横幅给出启动建议（wails dev/build）', /wails dev|wails build/.test(bannerText));
  rep.check('后端 pill 标记为未就绪', /未就绪/.test(env.byId.get('backend-pill').textContent));
  rep.check('容器视图渲染错误态而非空白', /加载失败/.test(env.textOf(env.byId.get('ct-body'))));

  for (const view of ['images', 'volumes', 'networks', 'env', 'tasks']) {
    const tab = env.byId.get('tab-' + view);
    env.clickTab(view);
    await sleep(20);
    const selected = env.allElements.filter((e) => e._classes.includes('tab') && e.getAttribute('aria-selected') === 'true');
    rep.check(`点击 ${view} 后仅一个 tab 选中`, selected.length === 1 && selected[0] === tab);
    const id = { images: 'img-body', volumes: 'vol-body', networks: 'net-body', tasks: 'task-body', env: 'env-content' }[view];
    const expect = view === 'env' ? /环境自检失败/ : /加载失败/;
    rep.check(`${view} 视图渲染错误态而非空白`, expect.test(env.textOf(env.byId.get(id))));
  }
  rep.check('错误进入 toast（不静默吞掉）', env.byId.get('toast-root').childNodes.length > 0);
  rep.check('状态栏错误计数已显示', env.byId.get('status-err').hidden === false);
  rep.check('启动时未误弹模态', env.byId.get('modal-root').hidden === true);
  rep.check('启动时日志抽屉保持关闭', env.byId.get('drawer-logs').hidden === true);
  rep.check('启动时终端抽屉保持关闭', env.byId.get('drawer-terminal').hidden === true);
  rep.check('主题写入 html[data-theme]', ['dark', 'light'].includes(env.doc.documentElement.getAttribute('data-theme')));

  totalFailures += rep.dump();
}

/* ====================== 场景 B：伪后端 + 伪运行时 ====================== */

{
  const calls = [];
  const emitMap = {};
  const CID = 'abc1234567890abcdef';
  const CID2 = 'def0987654321fedcba';

  const containers = [
    { ID: CID, Names: ['/web1'], Image: 'nginx:latest', State: 'running', Status: 'Up 2 hours', Ports: ['0.0.0.0:8080->80/tcp'], CreatedAt: '2025-01-02 10:00:00' },
    { ID: CID2, Names: ['/db1'], Image: 'postgres:16', State: 'exited', Status: 'Exited (0) 2 hours ago', Ports: [], CreatedAt: '2025-01-01 09:00:00' }
  ];
  const images = [
    { ID: 'sha256:1111222233334444', Repository: 'nginx', Tag: 'latest', Size: '187MB', CreatedSince: '2 weeks ago' },
    { ID: 'sha256:5555666677778888', Repository: '<none>', Tag: '<none>', Size: '0B', CreatedSince: '2 weeks ago' }
  ];
  const volumes = [{ Name: 'data-vol', Driver: 'local', Scope: 'local', Mountpoint: '/var/lib/wslc/volumes/data-vol', CreatedAt: '2025-01-02 09:00:00' }];
  const networks = [{ ID: 'net1234567890ab', Name: 'bridge', Driver: 'bridge', Scope: 'local', CreatedAt: '2025-01-01 08:00:00', Containers: [] }];
  const tasks = [
    { ID: 'task-run-1', Kind: 'image-pull', Ref: 'redis:7', State: 'running', StartedAt: '2025-01-02T10:00:00Z' },
    { ID: 'task-done-1', Kind: 'image-build', Ref: 'D:\\src\\app', State: 'succeeded', StartedAt: '2025-01-02T09:00:00Z', EndedAt: '2025-01-02T09:01:00Z' }
  ];
  const envStatus = {
    Available: true,
    WslcPath: 'C:\\Program Files\\WSL\\wslc.exe',
    WslcVersion: '3.0.1.0',
    WSLVersion: '2.6.1.0',
    KernelVersion: '6.6.87.2-1',
    SettingsFile: 'C:\\Users\\dhshu\\.wslconfig',
    ServiceReady: false,
    Sessions: [{ ID: 1, Name: 'wslc-cli-dhshu', CreatorPid: 23428 }],
    Problems: ['HCS_E_SERVICE_NOT_AVAILABLE (0x80370114)：vmcompute 服务未运行'],
    CheckedAt: '2025-01-02T10:00:00Z'
  };

  const responses = {
    EnvCheck: () => envStatus,
    ListContainers: () => containers,
    ListImages: () => images,
    ListVolumes: () => volumes,
    ListNetworks: () => networks,
    ListTasks: () => tasks,
    ContainerStats: () => [],
    StartContainer: () => CID,
    StopContainer: () => CID,
    RestartContainer: () => CID,
    KillContainer: () => CID,
    RemoveContainer: () => CID,
    StartLogs: () => 'stream-logs-1',
    StartTerminal: () => 'stream-term-1',
    PullImage: () => 'task-pull-9',
    BuildImage: () => 'task-build-9',
    StreamEvents: () => 'stream-events-1',
    InspectContainer: () => ({ ID: CID, State: { Status: 'running' } }),
    LoadSettings: () => ({
      MirrorEnabled: true, MirrorEndpoint: 'docker.m.daocloud.io',
      CustomMirrors: ['docker.io', 'docker.m.daocloud.io', 'docker.1panel.live']
    }),
    SaveSettings: (o) => o,
    TestMirror: () => ({ Endpoint: 'docker.m.daocloud.io', TargetRef: 'hello-world:latest', OK: true, DurationMS: 1200, Message: 'ok' })
  };

  const methods = [
    'EnvCheck', 'ListContainers', 'StartContainer', 'StopContainer', 'RestartContainer',
    'KillContainer', 'RemoveContainer', 'RunContainer', 'InspectContainer', 'ContainerStats',
    'StartLogs', 'StopStream', 'StartTerminal', 'TerminalWrite', 'TerminalResize',
    'ListImages', 'PullImage', 'BuildImage', 'RemoveImage', 'TagImage', 'InspectImage',
    'ListVolumes', 'CreateVolume', 'RemoveVolume', 'ListNetworks', 'CreateNetwork',
    'RemoveNetwork', 'PruneContainers', 'PruneImages', 'ListTasks', 'CancelTask', 'StreamEvents',
    'LoadSettings', 'SaveSettings', 'TestMirror'
  ];
  const go = {};
  for (const name of methods) {
    go[name] = (...args) => {
      calls.push({ method: name, args });
      const r = responses[name];
      return Promise.resolve(typeof r === 'function' ? r(...args) : (r === undefined ? '' : r));
    };
  }
  const runtime = {
    EventsOn: (name, cb) => { (emitMap[name] = emitMap[name] || []).push(cb); },
    EventsOff: (name) => { delete emitMap[name]; },
    EventsEmit: () => {},
    EventsOnMultiple: () => {},
    WindowSetTitle: () => {},
    BrowserOpenURL: () => {}
  };
  const emit = (name, payload) => { for (const cb of (emitMap[name] || []).slice()) cb(payload); };
  const called = (method) => calls.filter((c) => c.method === method);
  const lastArgs = (method) => {
    const l = called(method);
    return l.length ? l[l.length - 1].args : [];
  };

  const env = makeEnv({ go, runtime });
  const rep = makeReporter('场景 B：伪后端 + 伪 Wails 运行时（有绑定）');

  let bootError = null;
  try {
    /* 让 vm 里的动态 import() 走真实模块加载器，从而真正加载
       frontend/wailsjs/go/main/App.js（兜底绑定），而不是只测 window.go 回退路径。
       该 ESM 在 Node 侧访问 window，因此把桩 window 暴露到全局。 */
    globalThis.window = env.win;
    vm.runInContext(appJs, env.context, {
      filename: join(frontend, 'app.js'),
      importModuleDynamically: vm.constants.USE_MAIN_CONTEXT_DEFAULT_LOADER
    });
  } catch (e) { bootError = e; }
  await sleep(80);

  const diag = env.win.__wslc.diag();
  rep.check('启动无异常', !bootError, bootError && bootError.message);
  rep.check('后端模式 = wails（已加载 wailsjs/go/main/App.js 兜底 ESM 绑定）', diag.mode === 'wails', diag.mode);
  rep.check('无缺失绑定', diag.missingBindings.length === 0, diag.missingBindings.join(','));
  rep.check('后端 pill = 就绪', /就绪/.test(env.byId.get('backend-pill').textContent));
  rep.check('无「后端未就绪」横幅', !/后端未就绪/.test(env.textOf(env.byId.get('banner-root'))));
  rep.check('已订阅 wslc:output 与 env:status', Object.keys(emitMap).sort().join(',') === 'env:status,wslc:output', Object.keys(emitMap).join(','));
  rep.check('启动即调用 StreamEvents 与 EnvCheck', called('StreamEvents').length === 1 && called('EnvCheck').length >= 1);

  /* ---- 容器表 ---- */
  const ctText = env.textOf(env.byId.get('ct-body'));
  rep.check('容器行渲染名称/镜像/状态', /web1/.test(ctText) && /nginx:latest/.test(ctText) && /Up 2 hours/.test(ctText));
  rep.check('容器行渲染端口与创建时间', /8080/.test(ctText) && /2025-01-02 10:00:00/.test(ctText));
  rep.check('长 ID 以等宽字体短 ID 呈现', env.findAll(env.byId.get('ct-body'), (e) => e._classes.includes('sid') && e.textContent === 'abc123456789').length === 1);
  rep.check('运行中容器 = 绿点 + badge-running', env.findAll(env.byId.get('ct-body'), (e) => e._classes.includes('badge-running')).length === 1);
  rep.check('已退出容器 = 灰点 + badge-exited', env.findAll(env.byId.get('ct-body'), (e) => e._classes.includes('badge-exited')).length === 1);
  rep.check('表格行可聚焦（tabindex）', env.findAll(env.byId.get('ct-body'), (e) => e.tagName === 'TR' && e.getAttribute('tabindex') === '0').length === 2);
  const actionBtns = env.findAll(env.byId.get('ct-body'), (e) => e.tagName === 'BUTTON');
  rep.check('行操作按钮均有 title + aria-label', actionBtns.length > 0 && actionBtns.every((b) => b.getAttribute('title') && b.getAttribute('aria-label')), String(actionBtns.length));

  /* 启动容器：运行中的行给「停止」，已退出的行给「启动」 */
  const startBtns = env.buttonsWithAct(env.byId.get('ct-body'), 'start');
  const stopBtns = env.buttonsWithAct(env.byId.get('ct-body'), 'stop');
  rep.check('行操作按状态切换（运行→停止 / 退出→启动）', startBtns.length === 1 && stopBtns.length === 1,
    `start=${startBtns.length} stop=${stopBtns.length}`);
  env.fire(startBtns[0], 'click');
  await sleep(30);
  rep.check('点击「启动」调用 StartContainer(已退出容器的 ID)',
    called('StartContainer').length === 1 && lastArgs('StartContainer')[0] === CID2,
    JSON.stringify(lastArgs('StartContainer')));

  /* 停止容器（列表在上一动作后已重绘，必须重新取按钮引用） */
  env.fire(env.buttonsWithAct(env.byId.get('ct-body'), 'stop')[0], 'click');
  await sleep(30);
  rep.check('点击「停止」调用 StopContainer(运行中容器的 ID, 10)',
    called('StopContainer').length === 1 && lastArgs('StopContainer')[0] === CID && lastArgs('StopContainer')[1] === 10,
    JSON.stringify(lastArgs('StopContainer')));

  /* 「⋯」菜单按钮同样需要重新取（列表已重绘） */
  const moreBtn = env.findAll(env.byId.get('ct-body'), (e) => e.tagName === 'BUTTON' && e.getAttribute('aria-haspopup') === 'menu')[0];

  /* ---- 破坏性操作：删除容器需二次确认 ---- */
  rep.check('行「⋯」按钮声明 aria-haspopup=menu', !!moreBtn);
  env.fire(moreBtn, 'click');
  await sleep(10);
  rep.check('打开行操作菜单', env.byId.get('menu-root').hidden === false);
  const delItem = env.byText(env.byId.get('menu-root'), 'button', '删除容器…');
  rep.check('菜单含「删除容器…」', !!delItem);
  env.fire(delItem, 'click');
  await sleep(20);
  rep.check('删除前弹出二次确认模态', env.byId.get('modal-root').hidden === false);
  rep.check('确认框标题为「删除容器」', /删除容器/.test(env.textOf(env.byId.get('modal-root'))));
  rep.check('确认框提供 -f / -v 选项', env.findAll(env.byId.get('modal-root'), (e) => e.tagName === 'INPUT' && e.getAttribute('type') === 'checkbox').length === 2);
  rep.check('未确认前不调用 RemoveContainer', called('RemoveContainer').length === 0);

  /* Esc 取消 */
  env.fireDoc('keydown', { key: 'Escape' });
  await sleep(10);
  rep.check('Esc 关闭确认框', env.byId.get('modal-root').hidden === true);
  rep.check('Esc 取消后仍不调用 RemoveContainer', called('RemoveContainer').length === 0);

  /* 再次打开并确认（勾选 -f / -v） */
  env.fire(moreBtn, 'click');
  await sleep(10);
  env.fire(env.byText(env.byId.get('menu-root'), 'button', '删除容器…'), 'click');
  await sleep(20);
  const boxes = env.findAll(env.byId.get('modal-root'), (e) => e.tagName === 'INPUT' && e.getAttribute('type') === 'checkbox');
  boxes.forEach((b) => { b.checked = true; });
  const okBtn = env.byText(env.byId.get('modal-root'), 'button', '删除');
  env.fire(okBtn, 'click');
  await sleep(40);
  rep.check('确认后调用 RemoveContainer(ref, true, true)', called('RemoveContainer').length === 1 && JSON.stringify(lastArgs('RemoveContainer')) === JSON.stringify([CID, true, true]), JSON.stringify(lastArgs('RemoveContainer')));
  rep.check('确认后模态关闭', env.byId.get('modal-root').hidden === true);

  /* ---- kill 需二次确认 ---- */
  env.fire(moreBtn, 'click');
  await sleep(10);
  env.fire(env.byText(env.byId.get('menu-root'), 'button', '强制终止 (kill)'), 'click');
  await sleep(20);
  rep.check('kill 需二次确认', env.byId.get('modal-root').hidden === false && called('KillContainer').length === 0);
  env.fire(env.byText(env.byId.get('modal-root'), 'button', '强制终止'), 'click');
  await sleep(40);
  rep.check('确认后调用 KillContainer(ref, SIGKILL)', called('KillContainer').length === 1 && lastArgs('KillContainer')[1] === 'SIGKILL');

  /* ---- 日志抽屉 ---- */
  env.fire(env.buttonsWithAct(env.byId.get('ct-body'), 'logs')[0], 'click');
  await sleep(40);
  rep.check('日志抽屉打开', env.byId.get('drawer-logs').hidden === false);
  rep.check('调用 StartLogs(ref, {Follow:true,Tail:200})', called('StartLogs').length === 1 &&
    lastArgs('StartLogs')[0] === CID && lastArgs('StartLogs')[1]?.Follow === true && lastArgs('StartLogs')[1]?.Tail === 200,
    JSON.stringify(lastArgs('StartLogs')));
  emit('wslc:output', { Channel: 'container-logs', Ref: CID, Stream: 'stdout', Text: 'hello-from-container', Seq: 1 });
  emit('wslc:output', { Channel: 'container-logs', Ref: CID, Stream: 'stderr', Text: 'oops-stderr', Seq: 2 });
  await sleep(10);
  const logsText = env.textOf(env.byId.get('logs-body'));
  rep.check('事件流渲染到日志抽屉', /hello-from-container/.test(logsText) && /oops-stderr/.test(logsText));
  rep.check('stderr 行带 ln-stderr 类', env.findAll(env.byId.get('logs-body'), (e) => e._classes.includes('ln-stderr')).length === 1);
  emit('wslc:output', { Channel: 'container-logs', Ref: 'other-container-id', Text: 'should-not-appear' });
  await sleep(10);
  rep.check('其它容器的日志不会串入', !/should-not-appear/.test(env.textOf(env.byId.get('logs-body'))));
  env.fire(env.byId.get('logs-pause'), 'click');
  emit('wslc:output', { Channel: 'container-logs', Ref: CID, Text: 'paused-line' });
  await sleep(10);
  rep.check('暂停后不再渲染新行', !/paused-line/.test(env.textOf(env.byId.get('logs-body'))));
  env.fire(env.byId.get('logs-pause'), 'click');
  await sleep(10);
  rep.check('继续后补齐缓冲行', /paused-line/.test(env.textOf(env.byId.get('logs-body'))));
  env.fire(env.byId.get('logs-stop'), 'click');
  await sleep(20);
  rep.check('「停止流」调用 StopStream(streamID)', called('StopStream').length === 1 && lastArgs('StopStream')[0] === 'stream-logs-1');
  env.fire(env.byId.get('logs-close'), 'click');
  await sleep(10);
  rep.check('关闭日志抽屉', env.byId.get('drawer-logs').hidden === true);

  /* ---- 终端抽屉 ---- */
  env.fire(env.buttonsWithAct(env.byId.get('ct-body'), 'terminal')[0], 'click');
  await sleep(40);
  rep.check('终端抽屉打开', env.byId.get('drawer-terminal').hidden === false);
  rep.check('调用 StartTerminal(ref, {TTY:true}, 100, 30)', called('StartTerminal').length === 1 &&
    lastArgs('StartTerminal')[0] === CID && lastArgs('StartTerminal')[1]?.TTY === true &&
    lastArgs('StartTerminal')[2] === 100 && lastArgs('StartTerminal')[3] === 30,
    JSON.stringify(lastArgs('StartTerminal')));
  emit('wslc:output', { Channel: 'terminal', Ref: CID, Stream: 'stdout', Text: 'root@web1:/# ' });
  await sleep(10);
  rep.check('终端事件流渲染', /root@web1/.test(env.textOf(env.byId.get('term-surface'))));
  env.byId.get('term-line').value = 'echo hi';
  env.fire(env.byId.get('term-form'), 'submit');
  await sleep(20);
  rep.check('整行发送调用 TerminalWrite(streamID, "echo hi\\r")',
    called('TerminalWrite').length === 1 && JSON.stringify(lastArgs('TerminalWrite')) === JSON.stringify(['stream-term-1', 'echo hi\r']),
    JSON.stringify(lastArgs('TerminalWrite')));
  env.fire(env.byId.get('term-surface'), 'keydown', { key: 'Enter' });
  await sleep(20);
  rep.check('终端按键 Enter 发送 \\r',
    called('TerminalWrite').length === 2 && lastArgs('TerminalWrite')[1] === '\r',
    JSON.stringify(lastArgs('TerminalWrite')));
  env.fire(env.byId.get('term-ctrlc'), 'click');
  await sleep(20);
  rep.check('Ctrl+C 按钮发送 \\u0003', lastArgs('TerminalWrite')[1] === '\u0003');
  env.fire(env.byId.get('term-stop'), 'click');
  await sleep(20);
  rep.check('停止终端流调用 StopStream(streamID)', called('StopStream').length === 2 && lastArgs('StopStream')[0] === 'stream-term-1');
  env.fire(env.byId.get('term-close'), 'click');
  await sleep(10);
  rep.check('关闭终端抽屉', env.byId.get('drawer-terminal').hidden === true);

  /* ---- 镜像视图 ---- */
  env.clickTab('images');
  await sleep(30);
  const imgText = env.textOf(env.byId.get('img-body'));
  rep.check('镜像行渲染 repository:tag 与短 ID', /nginx:latest/.test(imgText) && /sha256:111122/.test(imgText));
  rep.check('<none>:<none> 回退为短 ID', /sha256:55556/.test(imgText));
  env.byId.get('img-pull-ref').value = 'redis:7';
  env.fire(env.byId.get('btn-img-pull'), 'click');
  await sleep(40);
  rep.check('拉取调用 PullImage("redis:7")', called('PullImage').length === 1 && lastArgs('PullImage')[0] === 'redis:7');
  rep.check('拉取成功后打开任务输出抽屉', env.byId.get('drawer-logs').hidden === false);
  env.fire(env.byId.get('logs-close'), 'click');
  await sleep(10);

  /* 打标签 */
  const tagBtn = env.byText(env.byId.get('img-body'), 'button', '打标签');
  env.fire(tagBtn, 'click');
  await sleep(20);
  rep.check('打标签弹出表单模态', env.byId.get('modal-root').hidden === false && /TagImage/.test(env.textOf(env.byId.get('modal-root'))));
  env.getEl('tag-target').value = 'myrepo/nginx:dev';
  env.fire(env.byText(env.byId.get('modal-root'), 'button', '打标签'), 'click');
  await sleep(40);
  rep.check('提交调用 TagImage(source, "myrepo/nginx:dev")',
    called('TagImage').length === 1 && lastArgs('TagImage')[1] === 'myrepo/nginx:dev',
    JSON.stringify(lastArgs('TagImage')));

  /* 删除镜像需确认 */
  env.fire(env.byText(env.byId.get('img-body'), 'button', '删除'), 'click');
  await sleep(20);
  rep.check('删除镜像需二次确认', env.byId.get('modal-root').hidden === false && called('RemoveImage').length === 0);
  env.fire(env.byText(env.byId.get('modal-root'), 'button', '删除'), 'click');
  await sleep(40);
  rep.check('确认后调用 RemoveImage(ref, force)', called('RemoveImage').length === 1 && lastArgs('RemoveImage')[0] === 'nginx:latest');

  /* ---- 卷视图 ---- */
  env.clickTab('volumes');
  await sleep(30);
  rep.check('卷行渲染名称/驱动/挂载点', /data-vol/.test(env.textOf(env.byId.get('vol-body'))) && /\/var\/lib\/wslc/.test(env.textOf(env.byId.get('vol-body'))));
  env.byId.get('vol-name').value = 'new-vol';
  env.byId.get('vol-driver').value = 'local';
  env.fire(env.byId.get('vol-form'), 'submit');
  await sleep(30);
  rep.check('创建卷调用 CreateVolume("new-vol","local")', JSON.stringify(lastArgs('CreateVolume')) === JSON.stringify(['new-vol', 'local']), JSON.stringify(lastArgs('CreateVolume')));
  env.fire(env.byText(env.byId.get('vol-body'), 'button', '删除'), 'click');
  await sleep(20);
  rep.check('删卷需二次确认（并要求输入卷名）', env.byId.get('modal-root').hidden === false && called('RemoveVolume').length === 0);
  env.fire(env.byText(env.byId.get('modal-root'), 'button', '取消'), 'click');
  await sleep(10);
  rep.check('取消后不调用 RemoveVolume', called('RemoveVolume').length === 0);

  /* ---- 网络视图 ---- */
  env.clickTab('networks');
  await sleep(30);
  rep.check('网络行渲染名称/驱动', /bridge/.test(env.textOf(env.byId.get('net-body'))));
  env.byId.get('net-name').value = 'app-net';
  env.byId.get('net-driver').value = 'bridge';
  env.byId.get('net-subnet').value = '172.20.0.0/16';
  env.byId.get('net-internal').checked = true;
  env.fire(env.byId.get('net-form'), 'submit');
  await sleep(30);
  rep.check('创建网络调用 CreateNetwork(name,driver,subnet,gateway,internal)',
    JSON.stringify(lastArgs('CreateNetwork')) === JSON.stringify(['app-net', 'bridge', '172.20.0.0/16', '', true]),
    JSON.stringify(lastArgs('CreateNetwork')));

  /* ---- 环境自检视图 ---- */
  env.clickTab('env');
  await sleep(30);
  const envText = env.textOf(env.byId.get('env-content'));
  rep.check('EnvStatus 全字段展示', ['Available', 'ServiceReady', 'WslcPath', 'WslcVersion', 'WSLVersion', 'KernelVersion', 'SettingsFile', 'CheckedAt', 'Sessions']
    .every((k) => envText.includes(k)));
  rep.check('ServiceReady=false 显著提示', /ServiceReady = false/.test(envText));
  rep.check('展示 Problems 内容', /HCS_E_SERVICE_NOT_AVAILABLE/.test(envText));
  rep.check('给出 vmcompute 修复命令', /Set-Service -Name vmcompute -StartupType Manual/.test(envText) && /Start-Service vmcompute/.test(envText));
  rep.check('Sessions 表格渲染会话行', /wslc-cli-dhshu/.test(envText) && /23428/.test(envText));
  rep.check('ServiceReady=false 时顶部横幅提示', /ServiceReady = false/.test(env.textOf(env.byId.get('banner-root'))));

  /* env:status 事件推送 */
  emit('env:status', Object.assign({}, envStatus, { ServiceReady: true, Problems: [] }));
  await sleep(20);
  rep.check('env:status 事件更新视图', /ServiceReady = true/.test(env.textOf(env.byId.get('env-content'))));
  rep.check('ServiceReady=true 后横幅消失', !/ServiceReady/.test(env.textOf(env.byId.get('banner-root'))));

  /* ---- 任务视图 ---- */
  env.clickTab('tasks');
  await sleep(30);
  const taskText = env.textOf(env.byId.get('task-body'));
  rep.check('任务行渲染 ID/类型/引用/状态', /task-run-1/.test(taskText) && /image-pull/.test(taskText) && /redis:7/.test(taskText) && /运行中/.test(taskText) && /成功/.test(taskText));
  const cancelBtn = env.byText(env.byId.get('task-body'), 'button', '取消');
  rep.check('仅运行中的任务有「取消」', !!cancelBtn && env.findAll(env.byId.get('task-body'), (e) => e.tagName === 'BUTTON' && env.textOf(e).trim() === '取消').length === 1);
  env.fire(cancelBtn, 'click');
  await sleep(30);
  rep.check('取消任务调用 CancelTask(taskID)', called('CancelTask').length === 1 && lastArgs('CancelTask')[0] === 'task-run-1');

  /* ---- 顶栏清理菜单 ---- */
  env.fire(env.byId.get('btn-prune'), 'click');
  await sleep(10);
  rep.check('清理菜单打开且有 3 项', env.byId.get('menu-root').hidden === false &&
    env.findAll(env.byId.get('menu-root'), (e) => e.getAttribute('role') === 'menuitem').length === 3);
  env.fire(env.byText(env.byId.get('menu-root'), 'button', '清理停止的容器'), 'click');
  await sleep(20);
  rep.check('清理容器需二次确认', env.byId.get('modal-root').hidden === false && called('PruneContainers').length === 0);
  env.fire(env.byText(env.byId.get('modal-root'), 'button', '开始清理'), 'click');
  await sleep(40);
  rep.check('确认后调用 PruneContainers()', called('PruneContainers').length === 1);

  /* 全部镜像清理需要输入 PRUNE */
  env.fire(env.byId.get('btn-prune'), 'click');
  await sleep(10);
  env.fire(env.byText(env.byId.get('menu-root'), 'button', '清理全部未使用镜像…'), 'click');
  await sleep(20);
  rep.check('全部镜像清理需输入 PRUNE 确认', /PRUNE/.test(env.textOf(env.byId.get('modal-root'))) && called('PruneImages').length === 0);
  env.fire(env.byText(env.byId.get('modal-root'), 'button', '全部清理'), 'click');
  await sleep(20);
  rep.check('未输入 PRUNE 时不调用 PruneImages', called('PruneImages').length === 0);

  /* ---- F5 刷新（当前视图 = 任务）---- */
  const before = called('ListTasks').length;
  env.fireDoc('keydown', { key: 'F5' });
  await sleep(60);
  rep.check('F5 刷新当前视图（任务视图再次取数）', called('ListTasks').length > before,
    `${before} -> ${called('ListTasks').length}`);

  totalFailures += rep.dump();
}

console.log('');
if (totalFailures) {
  console.log(`SMOKE FAILED：共 ${totalFailures} 项断言未通过。`);
  process.exit(1);
}
console.log('SMOKE PASS：场景 A + 场景 B 全部断言通过。');
process.exit(0);
