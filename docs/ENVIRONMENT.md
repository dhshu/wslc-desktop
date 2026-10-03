# 环境与调研事实（已实测 / 已核实）

> 所有成员动手前请先读这份。这里每一行都是**验证过的**，不是猜测。

## 1. PATH（每条命令都要先加）
```powershell
$env:Path = "C:\Program Files\Go\bin;$env:USERPROFILE\go\bin;$env:Path"
```
- `go` 在 `C:\Program Files\Go\bin\go.exe`（go1.27.0），**不在 PATH**
- `wails` 在 `C:\Users\dhshu\go\bin\wails.exe`（**v2.16.0**），**不在 PATH**
- `wslc` 在 `C:\Program Files\WSL\wslc.exe`（**3.0.1.0**），**不在 PATH**

## 2. 构建：Windows 上不需要 C 编译器
- `wails doctor` 结论：`Your system is ready for Wails development!`，且**依赖检查里根本没有 C 编译器这一项**。
- 源码依据：Wails v2 仅在**非 Windows** 目标上强制 `CGO_ENABLED=1`
  （`pkg/commands/build/base.go`：`if options.Platform != "windows" { … }`）。
  所有 `import "C"` 的文件都在 `darwin`/`linux` 目录下。
- 本机 `go env CGO_ENABLED` = `0`，且**没有 gcc**——完全没问题。
- Go 版本要求：Wails v2.16.0 的 `go.mod` 声明 `go 1.25.0`，本机 go1.27.0 满足。
  本仓库 `go.mod` 已设为 `go 1.25.0`。

## 2b. `go test -race` 需要 cgo（已解决）
- 默认 `CGO_ENABLED=0` 且无 C 编译器 → `-race` 会报 `go: -race requires cgo`。
- **已用 winget 安装 LLVM MinGW**，`-race` 现已可用。跑 race 测试前加上：
```powershell
$mingw = "C:\Users\<you>\AppData\Local\Microsoft\WinGet\Packages\MartinStorsjo.LLVM-MinGW.UCRT_*\llvm-mingw-*\bin"
$env:Path = "C:\Program Files\Go\bin;$mingw;$env:Path"
$env:CGO_ENABLED = "1"
go test ./... -race -count=1
```
- 验证结果：`go test ./internal/wslc/ -race -count=1` → `ok ... 2.643s`（通过）。
- 注意：这个 `gcc.exe` 实际是 clang 的 shim，`gcc --version` 会打印 `clang version 22.1.8` —— 这是**正常的**。
- 运行 `wails build` 时**不要**设 `CGO_ENABLED=1`；Windows 上 Wails 用默认的 0 即可。

## 3. Wails v2 绑定规则（踩坑点）
- `runtime` 必须**相对路径**引用生成的源码：`frontend/wailsjs/runtime/runtime.js`。
  - **不要** `npm install @wailsio/runtime` —— 那是 **Wails v3 专用**包，v2 用了必然失败。
  - `@wailsapp/runtime@2.0.0` 也**不在 npm 上**（npm 上只到 v1 的 1.1.1）。
- 方法返回签名只支持：`()`、`(T)`、`(error)`、`(T, error)`。
  **0 个或 ≥3 个返回值会被静默当成 `nil` 结果 + `nil` 错误**——绝不能写出 3 个返回值的方法。
- Go 的 `error` 第二返回值 -> 前端 Promise **reject**；前端要用 `.catch()` 接，不要静默吞掉。
- 结构体跨边界规则：只用**导出字段**；字段要生成 TS 必须有**带名字的 json tag**；
  `chan`/`func` 字段不可序列化；**匿名嵌入字段会被跳过**；不支持匿名嵌套结构体。
  → 所以 `internal/domain` 里的类型字段全部已带 json tag。
- 事件：Go 侧 `runtime.EventsEmit(ctx, name, payload)`；JS 侧 `window.runtime.EventsOn(name, cb)`
  或 `import { EventsOn, EventsOff } from "../wailsjs/runtime/runtime"`。
  - **文档与实现不一致**：文档说 `EventsOn` 返回取消函数，但 v2.16.0 的 `runtime.d.ts` 声明为 `void`。
    **请用 `EventsOff(name)` 取消订阅**，不要依赖返回值。
- `wails init` 不会自动 `npm install`。本项目的 `wails.json` 已刻意**不配置前端构建步骤**（零构建），
  所以不会触发 npm。

## 4. wslc 关键事实（决定命令构造）
- `--format json` 可用，输出 **PascalCase**：
  `{"Client":{"Version":"3.0.1.0"}}`、
  `{"Client":{…},"Server":{"SessionManagerVersion":"3.0.1","Sessions":[{"CreatorPid":23428,"ID":1,"Name":"wslc-cli-dhshu"}]}}`
- **`wslc system session list` 不支持 `--format`**（报 `当前命令的选项名称未被识别`）→ 必须表格解析。
- **本版本没有 `wslc system prune`**（报 `无法识别的命令:"prune"`）；只有
  `wslc container prune [-f] [--filter]` 与 `wslc image prune [-a] [-f] [--filter]`。
- `wslc inspect [-t type] [-s] [-f format]`（注意 `-f` 是 **format**，不是 file）。
- `wslc image inspect [-f format]`。
- `wslc stats [--format json|table] [-a] [--no-trunc]`。
- 命令别名很丰富（`wslc ps` = `wslc container list`），但**我们一律用全称形式**，减少歧义。

## 5. 当前阻塞：vmcompute 未运行
- 症状：`wslc list` / `image list` / `volume list` / `network list` 全部失败于
  `HCS_E_SERVICE_NOT_AVAILABLE`（= 0x80370114）。
- 根因：**Hyper-V 主机计算服务 `vmcompute` 处于 Stopped 且 Disabled**（Disabled 并非出厂状态）。
  仅靠 `wslc version`/`info`/`system session list` 这类只走 IPC 的命令**不会**触发，
  所以“部分命令能跑”完全符合这个根因。
- 修复（需**管理员** PowerShell，`Disabled` 是真正的故障点）：
  ```powershell
  Set-Service -Name vmcompute -StartupType Manual   # 或 Automatic
  Start-Service vmcompute
  Get-Service vmcompute                              # 期望 Running
  ```
  若仍不行：确认 `VirtualMachinePlatform` 可选功能已启用（必要时 `Enable-WindowsOptionalFeature
  -Online -FeatureName VirtualMachinePlatform -All -NoRestart`）并**重启**；
  检查 `bcdedit /enum | findstr hypervisorlaunchtype` 应为 `Auto`。
- 参考资料：[microsoft/WSL#8693](https://github.com/microsoft/WSL/issues/8693)、
  [HCS 错误码表](https://learn.microsoft.com/en-us/virtualization/api/hcs/reference/hcshresult)、
  [WSLC 架构](https://devblogs.microsoft.com/commandline/wslc-architecture-deep-dive/)。
- **不需要**安装 WSL 发行版（`wsl -l -v` 为空是正常的），**不需要** Containers / 完整 Hyper-V 角色。

## 6. 因此的测试策略
在 `vmcompute` 被修复前，任何依赖真实容器的测试都必须能跳过而不是失败：
- 单元测试：一律用 `wslc.FakeRunner`，不依赖真实 wslc。
- 集成测试：加 build tag `integration`；对
  `version` / `info` / `system session list` 可直接跑，
  容器类用例检测到 `ErrServiceUnavailable` 时 **skip 并打印原因**，不算失败。
