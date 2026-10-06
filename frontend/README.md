# frontend/ —— Wslc Desktop 单页界面（零构建）

仅 `index.html` + `styles.css` + `app.js`（classic script，无打包器、无 npm 依赖、无 CDN、无外部字体）。
`wailsjs/` 下是**兜底**绑定与运行时实现，正常会被 `wails dev` / `wails build` 覆盖生成。

```
frontend/
├── index.html                     界面骨架：顶栏 / 6 标签页 / 状态栏 / 日志抽屉 / 终端抽屉 / 模态与 toast 容器
├── styles.css                     设计令牌（深色默认 + [data-theme=light] 浅色）+ 高密度表格
├── app.js                         全部逻辑（视图渲染、事件路由、抽屉、确认框、键盘可达）
├── wailsjs/
│   ├── go/main/App.js             ← 兜底 ESM 绑定（32 个方法，导出名 == CONTRACT.md B.2）
│   ├── go/main/App.d.ts           兜底类型声明
│   └── runtime/runtime.js         ← 运行时最小可用实现（EventsOn/EventsOff/EventsEmit/EventsOnMultiple/…）
└── tools/
    ├── selfcheck.mjs              静态自检：方法名 / 导出名 / DOM id / 视图 / 运行时 API 面 / 破坏性确认
    └── smoke.mjs                  DOM 冒烟：无后端 24 项 + 伪后端 78 项
```

## 后端调用链（三级回退）

1. `import('./wailsjs/go/main/App.js')` —— Wails 生成的 ESM 包装（正常路径）；
2. 失败则 `window.go.main.App[Method]` —— 运行时注入的 Go 绑定；
3. 都没有 → **「后端未就绪」横幅 + 每个视图的明确错误态**，页面不白屏、不抛未捕获异常。
   若只有兜底 shim（`window.runtime.__wslcShim`）则视为「无运行时」并显示事件流不可用的提示。

## 视图 → 后端方法对照

| 视图 | 后端方法（CONTRACT.md B.2） |
| --- | --- |
| 容器 | `ListContainers` `StartContainer` `StopContainer` `RestartContainer` `KillContainer` `RemoveContainer` `RunContainer` `InspectContainer` `ContainerStats` `StartLogs` `StartTerminal` `StopStream` |
| 镜像 | `ListImages` `PullImage` `BuildImage` `RemoveImage` `TagImage` `InspectImage` `PruneImages` |
| 卷 | `ListVolumes` `CreateVolume` `RemoveVolume` |
| 网络 | `ListNetworks` `CreateNetwork` `RemoveNetwork` |
| 环境自检 | `EnvCheck`（+ 事件 `env:status`） |
| 任务 | `ListTasks` `CancelTask`（+ 事件 `wslc:output` 的 `build`/`pull`/`task` 通道） |
| 全局（顶栏「清理 ▾」/ 事件流） | `PruneContainers` `PruneImages` `StreamEvents` |

终端抽屉：`StartTerminal` → `TerminalWrite` → `TerminalResize` → `StopStream`（事件通道 `terminal`）。
日志抽屉：`StartLogs` → 事件通道 `container-logs` → `StopStream`。

## 事件

- `window.runtime.EventsOn("wslc:output", cb)` → `OutputEvent{Channel,Ref,Stream,Text,Seq,Time}`，
  按 `channel|ref` 落缓冲；容器日志按「ID/短ID/名称」别名宽松匹配，任务输出按任务 ID 匹配。
- `window.runtime.EventsOn("env:status", cb)` → `EnvStatus`（`app.go` 启动时推送一次）。
- 取消订阅统一用 `EventsOff(name)`：v2.16.0 的 `runtime.d.ts` 把 `EventsOn` 声明为 `void`，不依赖其返回值。
- 兼容字段大小写（`Channel`/`channel`、`Ref`/`ref`、`Text`/`text`、`Seq`/`seq`）。

## 键盘可达 / 无障碍

- 表格行 `tabindex="0"`：`Enter` 打开详情，`Shift+F10` / 右键打开行操作菜单。
- 所有按钮都有 `title` + `aria-label`；标签页是 `role="tablist"`，支持 ←/→/Home/End。
- 模态：`role="dialog"` `aria-modal`，`Esc` 关闭、Tab 焦点环内循环、关闭后焦点回收到触发元素。
- 破坏性操作二次确认：删除容器/镜像/卷/网络、`KillContainer`、`PruneContainers`、`PruneImages`；
  删除卷需**输入卷名**、清理全部镜像需输入 `PRUNE`。
- Toast：错误用 `role="alert"`；错误同时进入状态栏计数（点击查看历史），不静默吞掉。
- 快捷键：`F5` 刷新当前视图，`Esc` 关闭最上层的菜单/模态/抽屉。

## 自检命令（无网络、无依赖）

```powershell
cd wslc-desktop
node --check frontend/app.js            # 语法
node frontend/tools/selfcheck.mjs       # 契约一致性（方法名/导出名/DOM id/视图/确认覆盖）
node --no-warnings frontend/tools/smoke.mjs   # DOM 冒烟：24 + 78 = 102 项断言
```

- `selfcheck.mjs`：32 个方法名与 CONTRACT.md B.2 双向一致、DOM id 无缺失、6 视图对齐、
  运行时 API 面齐全、`@wailsio/runtime` 未被引用、7 类破坏性操作都有确认框。
- `smoke.mjs` 场景 A（无后端/无运行时）24 项：不白屏、六视图有明确状态、错误进 toast 与状态栏。
- `smoke.mjs` 场景 B（伪后端 + 伪运行时 + 真实兜底 `App.js` ESM）78 项：
  表格渲染与色点/badge、行操作按状态切换、启动/停止/删除/kill 的参数、二次确认与 Esc 取消、
  日志抽屉（StartLogs → 事件流 → 暂停/补齐 → StopStream）、终端抽屉（StartTerminal →
  TerminalWrite 整行/按键/Ctrl+C → StopStream）、EnvCheck 全字段与 vmcompute 修复建议、
  任务取消、清理菜单、F5 刷新。

> `--no-warnings` 只是为了压掉 Node 在 vm 里用 `USE_MAIN_CONTEXT_DEFAULT_LOADER`
> 加载真实 ESM 兜底绑定时的 experimental 提示，与断言无关。

## 备注

- 表格横向滚动由 `.table-wrap{overflow:auto}` + `table{min-width:900px}` 提供；ID 等长字段用等宽字体。
- 主题：`localStorage['wslc.theme']` 优先，其次 `prefers-color-scheme`，写入 `html[data-theme]`。
- 若 `app.go` 用 `//go:embed all:frontend` 整体嵌入，`tools/` 与 `README.md` 也会一起进二进制
  （约 30 KB，无副作用）；若想排除，改为按文件嵌入即可。
