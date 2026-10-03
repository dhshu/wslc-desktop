# AGENTS.md

面向 AI 编码助手（Codex、Claude Code、Copilot Workspace、Cursor Agent 等）在本仓库工作的约定。这些规则优先于你在别处学到的一般做法；与仓库内其他文档冲突时，以仓库文档为准。

## 项目一句话

`wslc Desktop` 是用 **Go + Wails v2** 为微软 [wslc](https://learn.microsoft.com/en-us/windows/wsl/wsl-container) 写的 Windows 桌面管理工具，补齐 wslc 缺失的镜像源与代理配置能力。前端是**零构建**的纯静态 HTML/CSS/JS。

## 目录

```
main.go / app.go / wails.json   Wails 入口与装配层
frontend/                       纯静态前端（index.html / styles.css / app.js / tools/*.mjs）
internal/domain/                读模型
internal/wslc/                  CLI 适配层（Resolver / ExecRunner / 解析器 / 错误分类）
internal/service/               业务编排（不 import Wails，可纯 Go 测试）
internal/integration/           对真实 wslc 的端到端测试（-tags integration）
internal/verify/                命令构造层的独立对抗性验证
docs/                           PLAN / CONTRACT / ENVIRONMENT / VERIFICATION
```

## 必须遵守的硬约束

1. **永远不要拼接 shell 字符串执行命令。** 一律用 `exec.Command(name, arg1, arg2, …)` 传参数组，任何形如 `exec.Command("sh", "-c", "…")` 的写法都禁止。
2. **`internal/service` 不能 `import` Wails。** 所有 Wails 相关的事都在 `app.go` / `main.go` 里做。
3. **契约冻结在 [`docs/CONTRACT.md`](docs/CONTRACT.md)。** 改任何绑定方法签名，都要同步更新：
   - `docs/CONTRACT.md`
   - `internal/service/binding_test.go`
   - `frontend/tools/selfcheck.mjs`
4. **前端 DOM id 必须登记在 `frontend/index.html` 的自测清单里**，并保证 `frontend/tools/selfcheck.mjs` 通过。
5. **破坏性操作（delete / prune / kill）必须走二次确认框**，不要绕过。
6. **不要新增前端构建链**：不引入 npm / webpack / vite / bundler。前端就是三个文件加 `tools/*.mjs` 自测脚本。
7. **不要新增 Go 依赖注入框架**（Wire、dg 之类）。装配在 `main.go` / `app.go` 手工完成。
8. **改动安全边界（命令构造、proxy URL 注入、设置解析）必须附带单元测试**，并用 `go test ./... -race -count=1` 验证。

## 已知 wslc 事实（不要"自作聪明"改回来）

- `wslc <list> --format json` 输出 **PascalCase JSON**；`wslc system session list` **不支持 `--format`**，必须解析表格。
- `volume list` / `network list` 走 **NDJSON**（每行一个对象）。
- `--tail all` 被 wslc 拒绝：`Tail <= 0` 直接省略 `--tail`。
- `inspect` 的 `-f` 是 format 而不是 file，两个 inspect 命令都不传 `-f`。
- 本版本没有 `wslc system prune`，只有 `container prune` 和 `image prune`（都要 `-f`）。
- 交互式终端先试 `-i -t`，被拒绝时自动回落 `-i` 并发一条 system 事件说明。
- `wslc image pull` 的 registry 请求发生在宿主机侧，**不读** `HTTP_PROXY`；代理注入只作用于 `wslc run`。
- 容器内 `127.0.0.1` 指向容器自身；宿主代理必须监听 `0.0.0.0`，容器内用 `host.wslc.internal`（= `169.254.73.254`）。UI 会自动把用户填的 `127.0.0.1` / `localhost` 改写为 `host.wslc.internal`。
- wslc 的表头会随系统语言本地化，解析器必须带多语言表头别名。

完整事实清单见 [`docs/ENVIRONMENT.md`](docs/ENVIRONMENT.md)。

## 本地验证

改动后至少跑（顺序无所谓）：

```powershell
gofmt -w <changed .go files>
go build ./...
go vet ./...
go test ./... -count=1
node frontend/tools/selfcheck.mjs
node frontend/tools/smoke.mjs
```

需要跑真实 wslc 的集成测试：

```powershell
go test -tags integration ./... -count=1
```

跑竞态检测需要 cgo + C 工具链（LLVM MinGW 之类），**不要**在 `wails build` 前设 `CGO_ENABLED=1`：

```powershell
$env:CGO_ENABLED = "1"
go test ./... -race -count=1
```

构建：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.16.0
wails build -s            # -s 因为本项目没有前端构建步骤
```

产物：`build\bin\wslc-desktop.exe`。

## 提交信息

遵循 Conventional Commits，scope 用模块名：

```
feat(settings): add proxy loopback rewrite
fix(wslc): handle localized Chinese error message
docs(readme): update install instructions
```

scope 常见取值：`wslc` / `service` / `settings` / `binding` / `terminal` / `frontend` / `docs` / `test`。

## 代码风格

- **Go**：`gofmt` + `go vet`。参考 `internal/wslc` 与 `internal/service` 的既有写法，别引入新模式。错误用 `errors.Is` / `errors.As` 判断，不要用字符串匹配。
- **前端**：纯 HTML/CSS/JS，无打包器、无 npm。样式集中在 `frontend/styles.css`，逻辑集中在 `frontend/app.js`。
- **测试**：优先单元测试；`internal/verify` 里的对抗性测试保留，不要删。
- **注释**：只在解释"为什么"时写注释，不写"做了什么"。

## 不要做的事

- 不要给项目加 Dockerfile、docker-compose、Makefile、Taskfile。
- 不要引入前端框架（React / Vue / Svelte …）。
- 不要在 `internal/service` 里放任何 UI 相关类型或 Wails 调用。
- 不要在 `internal/wslc` 里 import 业务模型（`internal/domain` 或 `internal/service`）。
- 不要绕过二次确认框去"简化"破坏性操作。
- 不要为了解决 wslc 缺陷去修改 wslc 二进制，或假设 wslc 未来会支持某个功能后就删除现有绕过逻辑。
- 不要提交 `%APPDATA%\wslc-desktop-settings.json` 或任何用户本地设置。

## 遇到不确定的情况

1. 先读 [`docs/PLAN.md`](docs/PLAN.md) 和 [`docs/CONTRACT.md`](docs/CONTRACT.md)。
2. 再看 [`docs/ENVIRONMENT.md`](docs/ENVIRONMENT.md) 里记录的真实机实测事实。
3. 再看最近的相关测试（尤其是 `internal/service/*_test.go` 与 `internal/verify/*_test.go`）了解既有意图。
4. 仍不确定时，在 PR / commit message 里明确标出你的假设，不要静默改动契约。
