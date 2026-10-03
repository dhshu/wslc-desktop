# wslc-desktop 开发计划

用 Go + Wails 为 **wslc**（WSL 容器 CLI）构建桌面管理工具。

## 1. 调研结论（已实测）

### 1.1 wslc 是什么
`wslc.exe` 是微软随 WSL 一起分发的 Linux 容器 CLI，用于在 Windows 上构建、运行和管理
Linux 容器，无需 Docker Desktop。它需要 **WSL 2.9.3+**。

参考：[WSL container 概览](https://learn.microsoft.com/en-us/windows/wsl/wsl-container)、
[容器入门教程](https://learn.microsoft.com/en-us/windows/wsl/tutorials/wsl-containers)、
[WSL containers GA](https://blogs.windows.com/windowsdeveloper/2026/09/29/wsl-containers-now-generally-available/)、
[endjin 试用报告](https://endjin.com/blog/trying-out-wsl-containers)。

### 1.2 本机实测环境
| 项 | 值 |
| --- | --- |
| WSL | 2.7.13.0 → **已升级到 3.0.1.0** |
| wslc | **3.0.1.0**，位于 `C:\Program Files\WSL\wslc.exe`（不在 PATH） |
| Go | 1.27.0 windows/amd64，`C:\Program Files\Go\bin`（不在 PATH） |
| Node / npm | v24.12.0 / 11.6.2 |
| GOPROXY | `https://goproxy.cn,direct`（可达） |

### 1.3 关键实测发现（决定了架构）
1. **`--format json` 可用**，输出为 **PascalCase** JSON：
   - `wslc version --format json` → `{"Client":{"Version":"3.0.1.0"}}`
   - `wslc info --format json` → `{"Client":{...},"Server":{"SessionManagerVersion":"3.0.1","Sessions":[{"CreatorPid":23428,"ID":1,"Name":"wslc-cli-dhshu"}]}}`
2. **不是所有命令都支持 `--format`**：`wslc system session list` 会报
   `当前命令的选项名称未被识别：'--format'`，必须按表格文本解析。
   → 架构必须“JSON 优先 + 表格回退”。
3. **本版本没有 `wslc system prune`**（报 `无法识别的命令:"prune"`），
   只有 `wslc container prune` 和 `wslc image prune`。
4. **`wslc info` 在没有可用容器服务时仍能成功**，因此可作为“环境自检”探针。
5. **阻塞项**：容器类命令当前全部失败于
   `HCS_E_SERVICE_NOT_AVAILABLE`。根因是宿主计算服务 **`vmcompute` 处于 Stopped/Disabled**，
   而当前进程非管理员无法启动它。修复需在**管理员** PowerShell 中执行：
   ```powershell
   Set-Service vmcompute -StartupType Manual
   Start-Service vmcompute
   ```
   在这些命令工作之前，端到端容器测试无法进行；因此架构中内置**假实现（Fake Runner）**，
   使全部解析/命令构造/业务流程逻辑可在无容器服务时被完整单元测试。

## 2. 目标与非目标

### 目标（第一版全部覆盖，用户已确认）
- 容器：列表 / 启动 / 停止 / 重启 / 删除 / 日志 / exec / stats
- 镜像：列表 / 拉取 / 构建 / 删除 / 打标签
- 卷与网络：列表 / 创建 / 删除
- 系统：wslc 版本、环境自检、prune 清理
- 实时流：日志跟随与长时间操作的流式输出

### 非目标
- 不实现容器运行时本身，只做管理前端
- 不实现注册表凭据的持久化存储（凭据不落盘）
- 不做 Compose 支持（wslc 本版本不支持）

## 3. 架构

```
wslc-desktop/
├── main.go                     # Wails 应用入口
├── app.go                      # Wails 绑定层（薄）：把前端调用转成 Service 调用
├── wails.json
├── internal/
│   ├── wslc/                   # CLI 适配层（核心，可独立测试）
│   │   ├── runner.go           # Runner 接口 + Command/Spec 定义
│   │   ├── exec.go             # 真实实现：定位 wslc.exe、执行、捕获 stdout/stderr
│   │   ├── fake.go             # 假实现：脚本化响应，供测试与无 wslc 环境
│   │   ├── resolve.go          # wslc.exe 定位（PATH → Program Files → 覆盖项）
│   │   ├── errors.go           # 退出码/ErrNotFound/ErrServiceUnavailable 分类
│   │   ├── parse_json.go       # PascalCase JSON 解析
│   │   └── parse_table.go      # 表格文本解析（--format 不可用时的回退）
│   ├── domain/                 # 领域模型（Container/Image/Volume/Network/SystemInfo）
│   └── service/                # 业务编排：参数校验 → 构造命令 → 执行 → 解析 → 建模
└── frontend/                   # Wails + React + TypeScript UI
```

### 分层原则
- `internal/wslc` 不认识 UI，也不认识领域模型，只负责“把命令变成结构化输出”。
- `internal/service` 是前端唯一入口，全部导出方法均可被 Wails 绑定。
- `app.go` 保持极薄，便于将来换 Wails 版本或加 HTTP 接口。

### Runner 抽象
```go
type Runner interface {
    Run(ctx context.Context, spec Spec) (Result, error)
    Stream(ctx context.Context, spec Spec, sink func(Line)) (Result, error)
}
```
- `Spec` 由 **纯函数命令构造器** 生成（`Args()` 返回 `[]string`），因此可以零依赖地断言
  “前端某个操作 → wslc 收到哪条命令”。
- `Fake` 按 `spec.Args` 的稳定 key 返回预置的 stdout/stderr/退出码。

### 流式输出
Wails v2 用 `runtime.EventsEmit` 把每一行推给前端；前端 `EventsOn` 订阅。
日志跟随（`wslc logs -f`）与拉取/构建进度都走同一通道。

## 4. 里程碑与写作用域

| # | 里程碑 | 主要产出 | 写作用域 |
| --- | --- | --- | --- |
| M1 | 契约冻结 | `internal/wslc/runner.go`、`errors.go`、`internal/domain/*` | 独占 |
| M2 | 适配层 | `exec.go`、`fake.go`、`resolve.go`、`parse_json.go`、`parse_table.go` | `internal/wslc/` |
| M3 | 业务层 | `internal/service/*.go` | `internal/service/` |
| M4 | 后端装配 | `main.go`、`app.go`、`wails.json` | 根目录 |
| M5 | 前端 | `frontend/` | `frontend/` |
| M6 | 测试 | `*_test.go` + 真实 wslc 集成测试 | 各自包内 |

## 5. 测试策略
1. **单元测试**：命令构造器、JSON/表格解析、错误分类、服务编排（用 Fake）。
2. **契约测试**：用 real 3.0.1 的真实输出做 fixture 固定解析行为。
3. **集成测试**：`-tags=integration` 直接调用真实 `wslc.exe`
   （`version`、`info`、`system session list` 在本机始终可用；
   容器类用例在 `vmcompute` 可用后自动启用，否则 skip 并给出原因）。
4. **构建校验**：`go vet ./...`、`go build ./...`、`wails build`。
5. **验收**：`go test ./...` 全绿 + 集成测试对真实 wslc 通过。

## 6. 风险
| 风险 | 影响 | 缓解 |
| --- | --- | --- |
| `vmcompute` 禁用 | 无法端到端测试容器 | Fake Runner + fixture；请用户以管理员启动服务 ✅**已解决** |
| 缺少 C 编译器（gcc） | `wails build` 可能失败 | 先确认；必要时改用纯 Go 前端装配或安装 MinGW ✅**已确认 Windows 不需要，`wails build` 成功** |
| wslc 输出字段随版本变化 | 解析脆弱 | JSON 优先；字段缺失容忍；表格回退；pin 3.0.1 fixture |
| 参数中包含 shell 特殊字符 | 命令注入 | 一律用 `exec.Command` 参数数组，绝不拼 shell 字符串 |

---

## 7. 执行结果（最终）

### 7.1 里程碑完成情况

| # | 里程碑 | 结果 |
| --- | --- | --- |
| M1 | 契约冻结 | ✅ `internal/domain` + `Runner`/`Spec`/错误分类 |
| M2 | 适配层 | ✅ `Resolver` / `ExecRunner` / `FakeRunner` / 解析器，**88.8% 覆盖** |
| M2b | NDJSON 修复 | ✅ 真机发现的真实 bug，已修 + 回归测试 |
| M3 | 业务层 | ✅ 全部 32 个方法，**90.7% 覆盖**，每条命令都有 argv 断言 |
| M4 | 后端装配 | ✅ `main.go` / `app.go` / `wails.json`，`wails build` 成功出 11.8 MB exe |
| M5 | 前端 | ✅ 六视图 + 日志/终端抽屉，零构建，DOM 冒烟 24/24 + 78/78 |
| M6 | 测试 | ✅ 单元 / 竞态 / 集成三层 |
| M7 | 独立验证 | ✅ `docs/VERIFICATION.md`：对抗性复核 9 项主张，抓出 3 个中严重度真实缺陷并全部修复 |

### 7.2 真机验证到的部分

- `wslc` 3.0.1.0 定位与版本解析、`info`（JSON + 中文表格）、`system session list`（表格）
- **卷**：创建 → 列出（NDJSON 解析）→ 删除 往返全通
- **网络**：创建 → 列出（3 条内置网络，NDJSON）→ 删除 往返全通
- `container prune -f` / `image prune` 真实执行（"总回收空间: 0B"）
- 空输出（`container/image list`、`stats` 无对象时）正确解析为**非 nil 空切片**
- `EnvCheck` 真机 `Available=true / ServiceReady=true / Problems=[]`
- 真实中文错误 `找不到容器 'x'。` 被正确分类为 `ErrNotFound`
- `wails build` 产出可执行文件，绑定恰好 **32 个导出**，前端与事件名确实内嵌

### 7.3 未能闭环的部分（环境限制，已写入 README「已知限制」）

| 项 | 原因 | 状态 |
| --- | --- | --- |
| GUI 无法在本机渲染 | **本机 WebView2 运行时损坏**：直接跑 `msedgewebview2.exe` 退出码 13 且不建 profile；同机 Edge 154 正常 → 与我们的代码无关 | 需管理员重装 WebView2 Runtime |
| 真实容器生命周期（run/logs/exec/stats） | wslc 会话 VM 的 DNS 绕过了宿主机本地代理，拉不到 Docker Hub 镜像 | 使用本项目的镜像源改写 |
| 交互式终端的 `-t` 行为 | 依赖真实容器 | 已实现 `-i -t` → 失败自动回落 `-i`，并有单测 |

**重要**：以上三项都**不是代码缺陷**，且都已在代码里做了诚实的降级与说明
（环境自检页会明确展示阻塞原因与修复命令），不是静默失败。

