/* =============================================================================
 * frontend/tools/selfcheck.mjs —— 零依赖静态自检（不是构建步骤）
 *
 *   node frontend/tools/selfcheck.mjs
 *
 * 校验项：
 *   1. app.js 调用的后端方法名 ⊆ docs/CONTRACT.md B.2（冻结契约）
 *   2. frontend/wailsjs/go/main/App.js 的导出名 == B.2 全集（兜底绑定完整性）
 *   3. app.js 引用的 DOM id 全部存在于 index.html；index.html 无重复 id
 *   4. app.js 的 VIEWS 与 index.html 的 tab / panel 一致
 *   5. 未引用 @wailsio/runtime（v3）或 @wailsapp/runtime（v1）
 *   6. wailsjs/runtime/runtime.js 暴露 v2 最小 API 面，且不覆盖已存在的 window.runtime
 * ========================================================================== */

import { readFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
import { dirname, join } from 'node:path';

const here = dirname(fileURLToPath(import.meta.url));
const frontend = join(here, '..');
const root = join(frontend, '..');

const read = (p) => readFileSync(p, 'utf8');

const appJs = read(join(frontend, 'app.js'));
const indexHtml = read(join(frontend, 'index.html'));
const appBindings = read(join(frontend, 'wailsjs', 'go', 'main', 'App.js'));
const runtimeShim = read(join(frontend, 'wailsjs', 'runtime', 'runtime.js'));
const contract = read(join(root, 'docs', 'CONTRACT.md'));

const failures = [];
const notes = [];
const fail = (m) => failures.push(m);

/* ---------------------------------------------------------------- 1. 契约方法名 */
const contractMethods = [...contract.matchAll(/^func \(s \*Service\) ([A-Za-z0-9_]+)\(/gm)]
  .map((m) => m[1]);
const contractSet = new Set(contractMethods);
notes.push(`CONTRACT.md B.2 方法数：${contractMethods.length}`);

/* app.js 里所有 invoke('Method' 调用 */
const invoked = [...appJs.matchAll(/\binvoke\(\s*'([A-Za-z0-9_]+)'/g)].map((m) => m[1]);
const invokedSet = new Set(invoked);
notes.push(`app.js 调用的后端方法数：${invokedSet.size}（去重后）`);

for (const m of invokedSet) {
  if (!contractSet.has(m)) fail(`app.js 调用了契约外的方法：${m}()`);
}
const neverCalled = contractMethods.filter((m) => !invokedSet.has(m));
if (neverCalled.length) fail(`以下契约方法在 app.js 中从未调用：${neverCalled.join(', ')}`);

/* ---------------------------------------------------------------- 2. 兜底绑定导出名 */
const exported = [...appBindings.matchAll(/^export function ([A-Za-z0-9_]+)\(/gm)].map((m) => m[1]);
const exportSet = new Set(exported);
notes.push(`兜底 App.js 导出数：${exportSet.size}`);
for (const m of contractMethods) {
  if (!exportSet.has(m)) fail(`兜底 App.js 缺少导出：${m}`);
}
for (const m of exportSet) {
  if (!contractSet.has(m)) fail(`兜底 App.js 导出了契约外的方法：${m}`);
}
if (exported.length !== exportSet.size) fail('兜底 App.js 存在重复导出');
if (exported.length !== contractMethods.length) {
  fail(`导出数量 ${exported.length} != 契约方法数 ${contractMethods.length}`);
}

/* ---------------------------------------------------------------- 3. DOM id */
const htmlIds = [...indexHtml.matchAll(/\bid="([^"]+)"/g)].map((m) => m[1]);
const htmlIdSet = new Set(htmlIds);
const dupes = htmlIds.filter((id, i) => htmlIds.indexOf(id) !== i);
if (dupes.length) fail(`index.html 存在重复 id：${[...new Set(dupes)].join(', ')}`);

const refs = new Set();
for (const m of appJs.matchAll(/\$\('([^']+)'\)/g)) refs.add(m[1]);
for (const m of appJs.matchAll(/getElementById\('([^']+)'\)/g)) refs.add(m[1]);
for (const m of appJs.matchAll(/querySelector\('#([A-Za-z0-9_-]+)/g)) refs.add(m[1]);
const missingIds = [...refs].filter((id) => !htmlIdSet.has(id));
if (missingIds.length) fail(`app.js 引用了不存在的 DOM id：${missingIds.join(', ')}`);
notes.push(`app.js 引用的 DOM id：${refs.size} 个，缺失 ${missingIds.length} 个`);

/* ---------------------------------------------------------------- 4. 视图一致性 */
const viewsBlock = appJs.match(/var VIEWS = \[([^\]]+)\]/);
const views = viewsBlock ? viewsBlock[1].split(',').map((s) => s.trim().replace(/['"]/g, '')).filter(Boolean) : [];
if (!views.length) fail('无法从 app.js 解析 VIEWS');
const tabViews = [...indexHtml.matchAll(/id="tab-([a-z]+)"/g)].map((m) => m[1]);
const panelViews = [...indexHtml.matchAll(/id="view-([a-z]+)"/g)].map((m) => m[1]);
if (JSON.stringify(tabViews) !== JSON.stringify(views)) {
  fail(`app.js VIEWS=[${views}] 与 index.html tab 不一致：[${tabViews}]`);
}
if (JSON.stringify(panelViews) !== JSON.stringify(views)) {
  fail(`app.js VIEWS=[${views}] 与 index.html panel 不一致：[${panelViews}]`);
}
notes.push(`视图（tab = panel = VIEWS）：${views.join(' / ')}`);

/* ---------------------------------------------------------------- 5. v3/v1 包 */
/* 只在“可执行代码”里查，注释里提到包名属于文档说明，不算引用。 */
const stripJsComments = (src) => src.replace(/\/\*[\s\S]*?\*\//g, '').replace(/(^|[^:])\/\/[^\n]*/g, '$1');
const codeSources = [
  ['app.js', stripJsComments(appJs)],
  ['index.html', indexHtml],
  ['App.js', stripJsComments(appBindings)],
  ['runtime.js', stripJsComments(runtimeShim)]
];
for (const bad of ['@wailsio/runtime', '@wailsapp/runtime']) {
  const hits = codeSources
    .filter(([, src]) => src.includes(bad))
    .map(([n]) => n);
  if (hits.length) fail(`出现了禁止的引用 ${bad}（位于 ${hits.join(', ')}）`);
}
/* 显式禁止 import / require / <script src> 指向任何 npm 运行时包 */
for (const [name, src] of codeSources) {
  const m = src.match(/(?:import[^\n]*from|require\()\s*['"]([^'"]*runtime[^'"]*)['"]/);
  if (m && /^[@a-z]/.test(m[1]) && !m[1].startsWith('.')) {
    fail(`${name} 用裸模块名引用了运行时包 ${m[1]}（必须是相对路径的生成源码）`);
  }
}

/* runtime 必须相对路径引用 */
if (!indexHtml.includes('src="wailsjs/runtime/runtime.js"')) {
  fail('index.html 必须用相对路径引用 wailsjs/runtime/runtime.js');
}

/* ---------------------------------------------------------------- 6. 运行时 API 面 */
/*
 * wailsjs/runtime/runtime.js 由 `wails build` 自动生成，是 ES module 形式
 * （`export function Xxx(...)`）。项目自己不会手写它，只可能在构建之前有一份
 * 手写 shim 作为兜底。所以这里要同时接受两种写法：
 *   - 官方生成版：  export function EventsOn(...)
 *   - 手写 shim：   EventsOn: function(...)   /   EventsOn(...) 挂载到 window.runtime
 * 只检测“有没有暴露”，不关心形态。
 */
for (const api of ['EventsOn', 'EventsOff', 'EventsEmit', 'EventsOnMultiple', 'WindowSetTitle', 'BrowserOpenURL']) {
  const asExport = new RegExp(`\\bexport\\s+function\\s+${api}\\s*\\(`);
  const asMember = new RegExp(`\\b${api}\\s*:`);
  const asAssign = new RegExp(`\\bwindow\\s*\\.\\s*runtime\\s*\\.\\s*${api}\\s*=`);
  if (!(asExport.test(runtimeShim) || asMember.test(runtimeShim) || asAssign.test(runtimeShim))) {
    fail(`runtime.js 未暴露 ${api}`);
  }
}
/* 只有手写 shim 才需要“不覆盖已存在的 window.runtime”保护；官方生成版不存在
 * 这个问题（Wails 自己加载它，页面不会先定义 window.runtime）。所以这条只在
 * 文件确实是手写 shim（存在 window.runtime 赋值且没有 export 语句）时才检查。
 */
const isShim = /\bwindow\s*\.\s*runtime\s*=/.test(runtimeShim) && !/\bexport\s+function\b/.test(runtimeShim);
if (isShim && !/window\.runtime\s*&&\s*typeof\s+window\.runtime\.EventsOn\s*===\s*['"]function['"]/.test(runtimeShim)) {
  fail('runtime.js 是手写 shim，缺少「window.runtime 已存在则不覆盖」的保护');
}
/* 不能依赖 EventsOn 的返回值取消订阅：app.js 中不得把 EventsOn 的返回值当函数调用 */
if (/=\s*(?:rt\(\)|window\.runtime)\.EventsOn\(/.test(appJs)) {
  fail('app.js 不应依赖 EventsOn 的返回值（v2.16.0 声明为 void，请用 EventsOff）');
}

/* ---------------------------------------------------------------- 7. 破坏性操作二次确认 */
const destructive = ['RemoveContainer', 'RemoveImage', 'RemoveVolume', 'RemoveNetwork', 'PruneContainers', 'PruneImages', 'KillContainer'];
const confirmCount = (appJs.match(/confirmDialog\(/g) || []).length;
notes.push(`confirmDialog 调用点：${confirmCount} 处（覆盖：${destructive.join(', ')}）`);
for (const m of destructive) {
  if (!apiIsGuarded(appJs, m)) fail(`${m} 未通过 confirmDialog 二次确认`);
}
function apiIsGuarded(src, method) {
  /* 在该方法名出现处向上回溯 1600 字符，看是否有 confirmDialog（模态构建在同一函数内） */
  let idx = -1;
  while ((idx = src.indexOf(method, idx + 1)) >= 0) {
    const from = Math.max(0, idx - 1600);
    const window = src.slice(from, idx + 200);
    if (window.includes('confirmDialog(')) return true;
  }
  return false;
}

/* ---------------------------------------------------------------- 输出 */
console.log('=== wslc frontend selfcheck ===');
for (const n of notes) console.log('  · ' + n);
console.log('');
if (failures.length) {
  console.log(`FAIL（${failures.length} 项）`);
  for (const f of failures) console.log('  ✗ ' + f);
  process.exit(1);
}
console.log('PASS：app.js 引用名、兜底绑定导出名、DOM id、视图、运行时 API 面、破坏性确认 全部一致。');
