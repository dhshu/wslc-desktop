# 冻结契约（FROZEN CONTRACT）

> **本文件是跨成员接口的唯一事实来源。**
> 任何需要修改它的成员，必须先通知 Lead；Lead 修改后才算生效。
> 版本：v1（M1 产出后冻结）

---

## A. `internal/wslc` — 适配层契约

### A.1 已存在（M1，勿改签名）

`internal/wslc/runner.go`

```go
type CommandKind string   // 见下方常量表
type Spec struct {
    Kind    CommandKind
    Args    []string      // 不含可执行文件本身
    Env     []string      // 追加到继承环境
    Dir     string
    Timeout time.Duration // 0=默认; <0=不限时(流式用)
    Stream  bool
    Stdin   string
}
type Line struct { Stream string; Text string; Time time.Time }
type Result struct {
    Args []string; Stdout string; Stderr string
    ExitCode int; Duration time.Duration; Truncated bool
}
func (r Result) OK() bool

type Runner interface {
    Run(ctx context.Context, spec Spec) (Result, error)
    Stream(ctx context.Context, spec Spec, sink func(Line)) (Result, error)
    Available(ctx context.Context) error
}

type CommandFactory func(ctx context.Context, exe string, spec Spec) *exec.Cmd
```

`internal/wslc/errors.go`

```go
var ErrNotFound, ErrServiceUnavailable, ErrExecutableNotFound, ErrUnsupportedCommand error
type ExitError struct { Code int; Args []string; Stderr string; Stdout string }
func (e *ExitError) Error() string
func (e *ExitError) Unwrap() error   // 映射到上述 sentinel
```

`CommandKind` 常量（**完整列表，新增必须通知 Lead**）：
`CmdVersion, CmdInfo, CmdSessionList, CmdContainerList, CmdContainerStart,
CmdContainerStop, CmdContainerKill, CmdContainerRst, CmdContainerRm,
CmdContainerPrune, CmdContainerRun, CmdContainerLogs, CmdContainerExec,
CmdContainerStats, CmdContainerIns, CmdImageList, CmdImagePull, CmdImagePush,
CmdImageBuild, CmdImageRm, CmdImageTag, CmdImagePrune, CmdImageIns,
CmdVolumeList, CmdVolumeCreate, CmdVolumeRm, CmdNetworkList, CmdNetworkCreate,
CmdNetworkRm, CmdEvents`

### A.2 M2 需要新增（实现者：**adapter**）

```go
// resolve.go
type Resolver struct { /* ... */ }
func NewResolver() *Resolver
func (r *Resolver) Resolve() (string, error)   // 返回 wslc.exe 绝对路径
// 查找顺序：环境变量 WSLC_PATH -> PATH -> C:\Program Files\WSL\wslc.exe
// 都找不到 -> ErrExecutableNotFound

// exec.go
type ExecRunner struct { /* ... */ }
func NewExecRunner(exe string, opts ...ExecOption) *ExecRunner
func (r *ExecRunner) Run(ctx context.Context, spec Spec) (Result, error)
func (r *ExecRunner) Stream(ctx context.Context, spec Spec, sink func(Line)) (Result, error)
func (r *ExecRunner) Available(ctx context.Context) error

// fake.go  —— 供 service 层与测试使用
type FakeRunner struct { /* ... */ }
func NewFakeRunner() *FakeRunner
// 用 ArgsKey(spec.Args) 作为 key 注册脚本化响应
func (f *FakeRunner) When(args []string, res Result, err error) *FakeRunner
func (f *FakeRunner) WhenStream(args []string, lines []string, res Result, err error) *FakeRunner
func (f *FakeRunner) Calls() []Spec
func (f *FakeRunner) Run(ctx context.Context, spec Spec) (Result, error)
func (f *FakeRunner) Stream(ctx context.Context, spec Spec, sink func(Line)) (Result, error)
func (f *FakeRunner) Available(ctx context.Context) error
// ArgsKey 生成稳定的 key（同名 flag 的多次出现按出现顺序保留）
func ArgsKey(args []string) string

// parse.go
func ParseContainers(stdout string) ([]domain.Container, error)
func ParseImages(stdout string) ([]domain.Image, error)
func ParseVolumes(stdout string) ([]domain.Volume, error)
func ParseNetworks(stdout string) ([]domain.Network, error)
func ParseStats(stdout string) ([]domain.ContainerStats, error)
func ParseSystemInfo(stdout string) (domain.SystemInfo, error)
func ParseSessions(stdout string) ([]domain.Session, error)
```

**解析规则（必须实现）**

1. 若 `stdout` 去掉空白后以 `[` 或 `{` 开头 → 按 JSON 解析。
   字段用 `internal/domain` 的容错类型；**未知字段忽略，缺失字段留零值，绝不报错**。
2. 否则按 `--format table` 文本解析：
   - 丢弃版权头（以 `版权所有` / `Copyright` 开头）与空行；
   - 第一行表头，按 **2 个及以上空格** 切分列；
   - 数据行同样按 2+ 空格切分；
   - **列数与表头不一致时，从左到右贪心填充剩余文本到最后一列**（避免状态文本含空格被截断）；
   - 表头名大小写不敏感，匹配 `ID / NAME(S) / IMAGE / STATUS / STATE / CREATED / PORTS / SIZE / REPOSITORY / TAG / DRIVER / SCOPE / MOUNTPOINT / CPU % / MEM USAGE / NET I/O / BLOCK I/O / PIDS` 等。
3. 空输出 / 只有表头 → 返回空切片 + `nil` error。
4. 无法识别的格式 → 返回错误，错误信息包含原始输出的**前 2 行**，便于诊断。

---

## B. `internal/service` — 业务层契约（对前端冻结）

**实现者：svc**。所有方法必须 `ctx context.Context` 且可被 Wails 绑定
（返回 `(T, error)`，`T` 为可 JSON 序列化的导出类型）。

### B.1 前端可见类型

```go
package service

type EnvStatus struct {
    Available       bool
    WslcPath        string
    WslcVersion     string
    WSLVersion      string
    KernelVersion   string
    SettingsFile    string
    ServiceReady    bool     // 容器类命令是否可用
    Sessions        []domain.Session
    Problems        []string // 人类可读的阻塞原因/修复建议
    CheckedAt       time.Time
}

type ContainerFilter struct {
    All      bool
    Query    string   // 客户端模糊过滤 name/image/id
    State    string   // "", "running", "exited"
    Limit    int      // 0=不限
}

type RunContainerOptions struct {
    Image      string
    Name       string
    Command    []string
    Detach     bool
    Remove     bool
    TTY        bool
    Env        []string
    Ports      []string   // "8080:80"
    Volumes    []string   // "vol:/path" 或 "C:\\x:/path"
    Network    string
    WorkDir    string
    User       string
    Hostname   string
    Memory     string
    CPUs       string
    Entrypoint string
    Labels     []string
    Pull       string     // always|missing|never
}

type LogsOptions struct {
    Follow     bool
    Tail       int      // <0 = all
    Timestamps bool
    Since      string
    Until      string
}
func (o LogsOptions) Args() []string

type ExecOptions struct {
    TTY     bool
    User    string
    WorkDir string
    Env     []string
}
func (o ExecOptions) Args() []string

type BuildOptions struct {
    Context   string
    Dockerfile string
    Tags      []string
    BuildArgs []string
    Target    string
    NoCache   bool
    Pull      bool
    Labels    []string
    Progress  string
}

type PruneResult struct { Stdout string }

// Wails 事件负载（前端 EventsOn 的结构）
type OutputEvent struct {
    Channel string // "container-logs" | "terminal" | "build" | "pull" | "events" | "task"
    Ref     string // 容器 ID/名称或任务 ID
    Stream  string // "stdout" | "stderr" | "system"
    Text    string
    Seq     int
    Time    time.Time
}

type Task struct {
    ID        string
    Kind      string
    Ref       string
    State     string // running | succeeded | failed | canceled
    Output    string
    Error     string
    StartedAt time.Time
    EndedAt   time.Time
}
```

### B.2 方法

```go
// 环境
func (s *Service) EnvCheck(ctx context.Context) (EnvStatus, error)

// 容器
func (s *Service) ListContainers(ctx context.Context, f ContainerFilter) ([]domain.Container, error)
func (s *Service) StartContainer(ctx context.Context, ref string) (string, error)
func (s *Service) StopContainer(ctx context.Context, ref string, timeoutSec int) (string, error)
func (s *Service) RestartContainer(ctx context.Context, ref string, timeoutSec int) (string, error)
func (s *Service) KillContainer(ctx context.Context, ref, signal string) (string, error)
func (s *Service) RemoveContainer(ctx context.Context, ref string, force, volumes bool) (string, error)
func (s *Service) RunContainer(ctx context.Context, opts RunContainerOptions) (string, error)
func (s *Service) InspectContainer(ctx context.Context, ref string) (json.RawMessage, error)
func (s *Service) ContainerStats(ctx context.Context, all bool) ([]domain.ContainerStats, error)

// 日志与终端（流式）
func (s *Service) StartLogs(ctx context.Context, ref string, opts LogsOptions) (string, error) // 返回 streamID
func (s *Service) StopStream(ctx context.Context, streamID string) error
func (s *Service) StartTerminal(ctx context.Context, ref string, opts ExecOptions, cols, rows int) (string, error)
func (s *Service) TerminalWrite(ctx context.Context, streamID, data string) error
func (s *Service) TerminalResize(ctx context.Context, streamID string, cols, rows int) error

// 镜像
func (s *Service) ListImages(ctx context.Context, all bool) ([]domain.Image, error)
func (s *Service) PullImage(ctx context.Context, ref string) (string, error)   // 返回 taskID，输出走事件
func (s *Service) BuildImage(ctx context.Context, opts BuildOptions) (string, error) // 返回 taskID
func (s *Service) RemoveImage(ctx context.Context, ref string, force bool) (string, error)
func (s *Service) TagImage(ctx context.Context, source, target string) (string, error)
func (s *Service) InspectImage(ctx context.Context, ref string) (json.RawMessage, error)

// 卷与网络
func (s *Service) ListVolumes(ctx context.Context) ([]domain.Volume, error)
func (s *Service) CreateVolume(ctx context.Context, name, driver string) (string, error)
func (s *Service) RemoveVolume(ctx context.Context, name, , force bool) (string, error)
func (s *Service) ListNetworks(ctx context.Context) ([]domain.Network, error)
func (s *Service) CreateNetwork(ctx context.Context, name, driver, subnet, gateway string, internal bool) (string, error)
func (s *Service) RemoveNetwork(ctx context.Context, name string, force bool) (string, error)

// 系统
func (s *Service) PruneContainers(ctx context.Context) (PruneResult, error)
func (s *Service) PruneImages(ctx context.Context, all bool) (PruneResult, error)
func (s *Service) ListTasks(ctx context.Context) ([]Task, error)
func (s *Service) CancelTask(ctx context.Context, id string) error
func (s *Service) StreamEvents(ctx context.Context) (string, error)

// 设置（代理 / 镜像源，见 internal/service/settings.go）
func (s *Service) LoadSettings(ctx context.Context) (AppSettings, error)
func (s *Service) SaveSettings(ctx context.Context, in AppSettings) (AppSettings, error)
func (s *Service) TestMirror(ctx context.Context, endpoint string) (MirrorProbe, error)
func (s *Service) TestProxy(ctx context.Context, url string) (ProxyProbe, error)
```

> 注意：上面的 `RemoveVolume` 形参表里有一个笔误 `name, , force bool`，
> 正确签名是 `RemoveVolume(ctx context.Context, name string, force bool) (string, error)`。
> 以本行为准。

### B.3 事件

- 通道名常量：`service.ChannelLogs`、`ChannelTerminal`、`ChannelBuild`、`ChannelPull`、`ChannelEvents`、`ChannelTask`。
- 广播出口是一个接口，便于测试：

```go
type Emitter interface { Emit(event OutputEvent) }
func NewService(r wslc.Runner, e Emitter) *Service
```

- 生产实现由 `app.go` 提供，内部调用 `runtime.EventsEmit(ctx, service.EventName, event)`。
- **service 包不得 import Wails**，以保证可纯 Go 测试。

#### B.3.1 `OutputEvent.Ref` 与 `Seq` 的约定（**已冻结，前端已按此实现**）

- `Ref`：**必须回填调用方传入的那个 ref 原样字符串**。
  即 UI 调 `StartLogs(ctx, "web", opts)`，则该流所有事件的 `Ref` 都必须是 `"web"`；
  调 `StartTerminal(ctx, "a1b2c3d4e5f6", opts)`，则 `Ref` 为 `"a1b2c3d4e5f6"`。
  **不要**替换成容器 ID、不要加 `/` 前缀、不要规范化大小写。
  前端已经按“宽松别名匹配（含 ≥8 字符前缀）”容错，但契约以“原样回填”为准。
- `Seq`：**每个流独立、从 1 开始单调递增**，用于前端排序与丢弃过期数据。必须填写。
- `Task.ID`：`StartLogs` / `StartTerminal` / `PullImage` / `BuildImage` / `StreamEvents`
  返回的 `streamID`/`taskID` 必须与后续事件中的 `Task.ID`（以及 `StopStream`/`CancelTask`
  接收的 id）**完全一致**。


---

## C. `app.go` — Wails 装配契约（实现者：**backend**）

- `app.go` 只做三件事：持有 `*service.Service`、实现 `service.Emitter`、把 Wails 方法转发。
- 每个 Service 方法在 App 上暴露**同名方法**，签名直接转发。
- 启动时调用 `service.EnvCheck`，并通过 `runtime.EventsEmit` 发送一次 `"env:status"`。
- 事件名常量在 `app.go`：`service.EventName = "wslc:output"`。

## D. `frontend/` — 前端契约（实现者：**ui**）

- **零构建步骤**：仅 `index.html` + `styles.css` + `app.js`，通过
  `<script src="wailsjs/runtime/runtime.js">` 使用 Wails 运行时。
- 数据来源：由 `wails dev`/`wails build` 生成的
  `frontend/wailsjs/go/main/App.js` 与 `.../App.d.ts`。
- 订阅输出：`window.runtime.EventsOn("wslc:output", cb)`。
- 若绑定期望的文件尚未生成，**必须提供 `frontend/wailsjs/go/main/App.js` 的兜底实现**，
  使页面在浏览器里也能加载并给出明确提示；不得因为缺失绑定而白屏。
- 视图：容器 / 镜像 / 卷 / 网络 / 环境自检 / 任务 六个标签页 + 日志与终端抽屉。
- 所有破坏性操作（删除、prune、kill）必须二次确认。
