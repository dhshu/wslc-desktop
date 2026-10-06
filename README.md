# Wslc Desktop

**用 Go + Wails 为 [wslc](https://learn.microsoft.com/en-us/windows/wsl/wsl-container) 写的 Windows 桌面管理工具。**

`wslc.exe` 是微软随 WSL ≥ 2.9.3 分发的 Linux 容器 CLI，可以在 Windows 上构建、运行和管理 Linux 容器，**无需 Docker Desktop**。本项目把它的核心操作包装成图形界面，补齐 wslc 缺失的镜像源配置能力。

界面采用深色主题、圆角卡片、状态胶囊、iOS 风格切换开关。截图见 [docs/screenshot.png](docs/screenshot.png)。

> **说明**：本项目的全部代码（含前端 HTML/CSS/JS、后端 Go、文档、测试脚本、构建脚本、GitHub Actions 工作流等）均由 AI 助手在人工提示与审阅下生成，未经人工独立手写。作者负责需求澄清、事实核验（本机实测命令输出、wslc 行为、错误分类）、代码审阅与最终发布。

## 亮点

- **零前端构建**：纯静态 HTML/CSS/JS，没有 npm、没有 webpack。产物约 12 MB 单文件 exe。
- **零 Go 依赖注入**：所有后端代码都是标准库 + Wails v2，`internal/service` 完全不 import Wails，可纯 Go 测试。
- **卡片墙风格设置页**：镜像源卡片墙（可点击激活 + 独立探测）、常用镜像预设编辑器（30 条内置、可增删改）。
- **完整能力**：容器 / 镜像 / 卷 / 网络 / 环境自检 / 任务 / 设置 七页视图；日志跟随 + 交互式终端抽屉。
- **破坏性操作二次确认**：删除、prune、kill 一律弹确认框。
- **JSON 优先 + 表格回退**解析器，带多语言表头别名（wslc 的表头会随系统语言本地化）。

## 功能一览

| 视图 | 能力 |
| --- | --- |
| **容器** | 列表（全部 / 仅运行、搜索、状态过滤）、启动 / 停止 / 重启 / 强杀 / 删除、运行新容器、inspect、stats、日志跟随、交互式终端 |
| **镜像** | 列表、拉取、从 Containerfile 构建、删除、打标签、inspect、prune |
| **卷** | 列表、创建、删除 |
| **网络** | 列表、创建（驱动 / 子网 / 网关 / 内部）、删除 |
| **环境自检** | wslc 路径与版本、WSL 版本、内核版本、会话列表、容器服务是否就绪、可执行的修复建议 |
| **任务** | 后台任务（拉取 / 构建 / 流）状态、输出、取消 |
| **设置** | 镜像源改写（卡片墙 + 探测）、常用镜像预设编辑 |
| **日志 / 终端抽屉** | 流式日志（可暂停）与交互式 `exec` 会话 |

## 系统要求

| 组件 | 最低要求 |
| --- | --- |
| Windows | 11（含 WebView2 Runtime） |
| WSL | ≥ 2.9.3（提供 `wslc.exe`） |
| Go（构建） | ≥ 1.25 |
| Wails CLI（构建） | v2.16.0 |

**运行前提：容器服务必须可用。** wslc 通过 **HCS（Host Compute System）** 创建会话 VM，因此必须让 Hyper-V 主机计算服务 `vmcompute` 处于运行状态，否则所有容器类命令都会失败于 `HCS_E_SERVICE_NOT_AVAILABLE`：

```powershell
# 需要【以管理员身份运行】的 PowerShell
Set-Service -Name vmcompute -StartupType Manual
Start-Service vmcompute
Get-Service vmcompute                              # 期望 Running
```

若 `vmcompute` 已被设为 `Disabled`（非出厂状态，常见于第三方"清理 / 优化"类软件），或 `Start-Service` 失败，还需要：

```powershell
Enable-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -All -NoRestart
bcdedit /set hypervisorlaunchtype Auto   # 仅当 bcdedit /enum 显示 Off
Restart-Computer
```

> 不需要安装任何 WSL **发行版**（`wsl -l -v` 为空是正常的），也不需要完整 Hyper-V 角色或 Docker Desktop。

## 构建

```powershell
# 安装 Wails CLI（Windows 上不需要 C 编译器）
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0

# 克隆 & 构建
git clone https://github.com/dhshu/wslc-desktop.git
cd wslc-desktop
wails build -s            # -s 跳过前端构建步骤（本项目无前端构建，等价）
```

产物：`build\bin\wslc-desktop.exe`（约 12 MB，内嵌前端资源）。

**运行建议**：把 exe 放到标准程序目录并创建开始菜单快捷方式，避免 SmartScreen 拦截：

```powershell
powershell -ExecutionPolicy Bypass -File .\install.ps1
```

然后从开始菜单启动，或直接双击 `%LOCALAPPDATA%\Programs\wslc-desktop\wslc-desktop.exe`。

> Windows SmartScreen / Defender 对未签名 exe 在「工作区 / 临时目录」上的双击启动有拦截，对标准程序目录则放行。命令行启动不受影响，可随时用于调试。

## 测试

```powershell
go build ./... ; go vet ./...
go test ./... -count=1                         # 单元测试
go test -tags integration ./... -count=1       # 对真实 wslc 的集成测试
```

竞态检测需要 cgo + C 编译器（可用 LLVM MinGW）：

```powershell
$mingw = "C:\Users\<you>\AppData\Local\Microsoft\WinGet\Packages\MartinStorsjo.LLVM-MinGW.UCRT_*\llvm-mingw-*\bin"
$env:Path = "C:\Program Files\Go\bin;$mingw;$env:Path"
$env:CGO_ENABLED = "1"
go test ./... -race -count=1
```

> `wails build` 本身**不要**设 `CGO_ENABLED=1`：Windows 上 Wails 用默认的 0，且不需要 C 编译器。

前端自测：

```powershell
node frontend/tools/selfcheck.mjs     # 方法签名、DOM id、视图一致性
node frontend/tools/smoke.mjs         # 无 Wails 运行时 + 伪后端 2 个场景
```

## 架构

```
main.go              Wails 入口：窗口、内嵌前端资源、绑定 App
app.go               装配层：持有 service，未导出 emitter，36 个同名转发方法
wails.json           零前端构建配置
frontend/            纯静态 HTML/CSS/JS（无打包器、无 npm 依赖）
  index.html  styles.css  app.js
  wailsjs/           由 wails build 生成的绑定与运行时（构建产物）
  tools/             selfcheck.mjs / smoke.mjs（Node 侧自测脚本）
internal/
  domain/            读模型（Container / Image / Volume / Network / Stats / SystemInfo）
  wslc/              CLI 适配层：Resolver / ExecRunner / FakeRunner / 解析器 / 错误分类
  service/           业务编排：校验 → 构造命令 → 执行 → 解析 → 建模 → 事件广播
  integration/       对真实 wslc 的端到端测试（-tags integration）
  verify/            命令构造层的独立对抗性验证
docs/                PLAN.md / CONTRACT.md / ENVIRONMENT.md / VERIFICATION.md
```

**分层原则**

- `internal/wslc` 不认识 UI 也不认识业务模型，只把命令变成结构化输出。
- `internal/service` **不 import Wails**，因此可以纯 Go 测试。
- 所有命令都通过参数数组交给 `exec.Command`，**从不拼接 shell 字符串**，所以含空格 / 引号 / 分号的参数不会被重新解释。
- 前后端契约冻结在 [docs/CONTRACT.md](docs/CONTRACT.md)，`frontend/tools/selfcheck.mjs` 在每次前端改动后自动核对方法签名与 DOM id。

## 关键设计决策（都基于真机实测）

1. **JSON 优先 + 表格回退**：`wslc <list> --format json` 输出 **PascalCase JSON**；但 `wslc system session list` **不支持 `--format`**，必须解析表格文本。解析器同时支持数组、包装对象和 **NDJSON**（`volume list` / `network list` 是每行一个对象），并带多语言表头别名。
2. **错误分类而非文本匹配**：`internal/wslc/errors.go` 把退出码 / 输出映射为 `ErrNotFound` / `ErrServiceUnavailable` / `ErrUnsupportedCommand` / `ErrExecutableNotFound`，同时匹配英文与简中文案，上层用 `errors.Is` 判断。
3. **本版本没有 `wslc system prune`**，只有 `container prune` 与 `image prune`（都要 `-f` 否则卡在确认）。
4. **`--tail all` 被 wslc 拒绝**，所以 `Tail <= 0` 直接省略 `--tail`（默认即全量）。
5. **`inspect` 的 `-f` 是 format 而不是 file**，两个 inspect 命令都不传 `-f`。
6. **交互式终端先试 `-i -t`，若被拒绝（非 TTY）自动回落 `-i`** 并给出一条 system 事件说明。
7. **Wails 不向绑定方法注入 `context.Context`**，所以 `app.go` 的转发方法不带 ctx，统一使用 `OnStartup` 捕获的应用 ctx。

## wslc 缺什么，本项目补什么

wslc 当前**没有 registry mirror 配置项**（[microsoft/WSL#40951](https://github.com/microsoft/WSL/issues/40951)，仍 open），并且会话 VM 通常走独立网络路径、不走宿主机的本地代理。所以在国内网络环境下，`wslc image pull alpine:3.20` 会直连失败。

本项目通过两种**不改 wslc 二进制**的方式弥补：

### 1. 镜像源改写

改写镜像名后执行 `wslc image pull <mirror>/library/<name>:<tag>`：

| 输入 | 改写后 |
| --- | --- |
| `alpine:3.20` | `<mirror>/library/alpine:3.20` |
| `bitnami/redis:7` | `<mirror>/bitnami/redis:7` |
| `quay.io/foo/bar:1` | 不改写（自有 registry） |
| `alpine:3.20@sha256:...` | 不改写（digest 引用） |

`internal/service/settings.go` 内置 12 个镜像源，其中 `docker.m.daocloud.io` / `docker.xuanyuan.me` / `docker.1panel.live` 已在本机（中国大陆家庭宽带，2026-10 实测）跑通；其余作为已下线或需要专属 id 的备选列出。UI 允许追加自定义镜像源。`TestMirror` 用 `hello-world:latest` 探测，返回真实耗时。

### 2. 常用镜像预设

`AppSettings.PresetImages []PresetImage{Label, Ref}` 持久化在设置文件中，默认播种 30 个开发常用镜像（MySQL / Oracle XE / PostgreSQL / Redis / Nacos / Kafka / Elasticsearch / Kibana / MinIO / Jenkins / Harbor / Nginx / Node.js / Python / Golang / Temurin JDK / Consul / etcd / RabbitMQ / Airflow / Grafana / Prometheus / OpenSearch / Doris / ClickHouse / TiDB …）。UI 允许增删改与上移；保存时后端去重、去空、cap 200 条，Label 为空时回填 Ref。镜像页拉取下拉从这份清单实时读取。

### AppSettings 字段

持久化到 `%APPDATA%\wslc-desktop-settings.json`（可用 `WSLC_DESKTOP_SETTINGS` 环境变量覆盖）：

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `SchemaVersion` | int | 目前固定 1 |
| `MirrorEnabled` | bool | 是否启用镜像名改写 |
| `MirrorEndpoint` | string | 生效的镜像源 host（如 `docker.m.daocloud.io`） |
| `CustomMirrors` | []string | 内置 + 用户追加的镜像源列表 |
| `PresetImages` | []PresetImage | 常用镜像预设（`{Label, Ref}`），最多 200 条 |

## 视觉风格

设置页采用卡片墙风格：

- 圆角卡片（`--r-lg` = 10px）+ 顶部渐变条（激活时蓝→青→绿）
- iOS 风格切换开关（`.switch`）用于「镜像改写」
- 状态胶囊（`.status-pill`）区分 `Currently Active` / `Normal` / `Off`
- 每卡自带图标 badge、标签、描述、内联探测按钮与探测结果
- 表格视图沿用原设计，只把圆角从 3px 提升到 4–6px

## 已知限制

1. **终端不是完整终端仿真**：抽屉是"整行发送 + 单键 raw"的混合实现，不解析 ANSI 光标移动；`TerminalResize` 是有意的 no-op（未分配 PTY）。日志与终端都走 Wails 事件流。
2. **`internal/domain` 没有单元测试**：纯数据结构，其行为由 `internal/wslc` 的解析测试与 `internal/service` 的真实输出 fixture 覆盖。
3. **wslc 上游问题**：镜像源是 wslc 上游缺失的能力，本项目的绕过方案依赖 wslc CLI 的 image-name 参数。若未来 wslc 官方支持 `settings.yaml` 的 mirror 配置，本项目的镜像源改写会自动失效（可通过关闭开关恢复）。

## 文档

- [docs/PLAN.md](docs/PLAN.md) — 调研结论、架构、里程碑、测试策略、风险
- [docs/CONTRACT.md](docs/CONTRACT.md) — 冻结的跨模块契约（含事件 Ref/Seq 约定）
- [docs/ENVIRONMENT.md](docs/ENVIRONMENT.md) — 实测的环境事实与踩坑点
- [docs/VERIFICATION.md](docs/VERIFICATION.md) — 独立对抗性验证报告

## 参考

- [WSL container 概览](https://learn.microsoft.com/en-us/windows/wsl/wsl-container)
- [容器入门教程](https://learn.microsoft.com/en-us/windows/wsl/tutorials/wsl-containers)
- [WSLC 架构深入](https://devblogs.microsoft.com/commandline/wslc-architecture-deep-dive/)
- [HCS 错误码表](https://learn.microsoft.com/en-us/virtualization/api/hcs/reference/hcshresult)
- [microsoft/WSL#40951 — 请求支持 registry mirror](https://github.com/microsoft/WSL/issues/40951)
- [microsoft/WSL#8693 — HCS_E_SERVICE_NOT_AVAILABLE](https://github.com/microsoft/WSL/issues/8693)
- [Wails v2 文档](https://wails.io/docs/gettingstarted/installation/)

## 贡献

欢迎 issue 和 PR。贡献前请：

1. 阅读 [docs/CONTRACT.md](docs/CONTRACT.md) 了解前后端契约（改动契约需同步更新契约测试 `internal/service/binding_test.go` 与 `frontend/tools/selfcheck.mjs`）。
2. 本地跑 `go test ./...` 与 `node frontend/tools/selfcheck.mjs`。
3. 破坏性操作必须走二次确认框，不要绕过。
4. 涉及安全边界（命令拼接、设置解析）的改动，请写单元测试并用 `-race` 验证。

## 许可

[MIT](LICENSE)。

## 作者

[dhshu](https://github.com/dhshu)
