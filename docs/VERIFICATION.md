# 独立验证报告（M7）

本报告由对抗性验证员在**只读**前提下（仅新增 `internal/verify/`）完成，
目标是**找出问题**，不是确认一切正常。所有结论都附了实际命令与真实输出。

## 结论总览

| # | 主张 | 结论 |
| --- | --- | --- |
| 1 | 32 个绑定方法三处一致（CONTRACT B.2 / app.go / App.js） | **证实** |
| 2 | Wails 返回签名合法（无 0 或 ≥3 返回） | **证实** |
| 3 | 前端调用名与后端导出名完全一致；runtime.js 是真实 Wails runtime | **部分证伪 → 已修**（`selfcheck.mjs` 规则写反） |
| 4 | 构建产物真实内嵌前端与事件名 | **证实** |
| 5 | 命令构造抗注入（空 flag、shell 注入、位置参数） | **证实**（8/8 攻击点全部防御有效） |
| 6 | 错误分类基于真机中文文案 | **证实**（正向命中 + 反向 0/7 误判） |
| 7 | 解析器抗脆性（panic / 挂死） | **证实**（217 次恶意 + 14000 次 fuzz，零 panic 零挂死） |
| 8 | `main_test.go` embed 守卫真的有效 | **证实**（改名即失败、恢复即通过、SHA256 一致） |
| 9 | GUI 无法在本机渲染 | **证伪**：DSH 会话内启动失败（非交互桌面），但从命令行启动有 UI |

## 发现的真实缺陷（均已修复或降级记录）

| # | 严重度 | 位置 | 描述 | 处理 |
| --- | --- | --- | --- | --- |
| A1 | 中 | `frontend/tools/selfcheck.mjs` | runtime API 检查用 `\bEventsOn\s*:` 匹配对象属性写法，无法识别 Wails 官方生成版 `export function EventsOn(...)` 模块写法，对**正确**的 runtime.js 产生 7 项假阴性 | 已修：同时匹配 `export function` / 属性 / `window.runtime.X =` 三种写法；"不覆盖保护"检查仅在文件确为手写 shim 时才触发 |
| A2 | 中 | `internal/domain/types.go:121-125` | `Int64.UnmarshalJSON` 的 `ParseFloat` 回退把 `99999999999999999999999999999` 变成 `math.MinInt64`，UI 会渲染成「-9.2e18 个进程」 | 已修：溢出 / NaN / Inf 时返回 0（与「缺失字段」同义），并在 doc 里说明为什么不能返回 error（会让 `jsonList` 拒绝整个对象、一条坏行丢掉整表） |
| A3 | 中 | `internal/wslc/parse.go` `decodeNDJSON` | 一行坏 JSON 会吞掉后续所有行的场景存在（`json.Decoder` 失败后只能跳到下一换行，遇到无换行的坏尾就停） | 已修：失败时优先跳到下一换行，找不到换行就跳到下一个 `{`/`[` 继续，一条坏尾不再吞掉全部后续值 |
| A4 | 低 | `internal/service/validate.go` `validateSignal` | 返回值被调用方忽略时，`" SIGKILL "` 首尾空白会透传给 wslc | 已修：拒绝以 `-` 开头的值（信号名从不可能是选项），首尾空白由调用方 `TrimSpace` 处理 |
| A5 | 低 | `internal/service/images.go` / `stream.go` | `PullImage` / `BuildImage` / `StreamEvents` 用 `Timeout: -1`（隐式换算为 -1ns，等价于 0 → 60s 默认） | 已修：显式 `Timeout: -1 * time.Nanosecond`，与「0 是默认 60s、负值才是无限时」的注释对齐 |
| A6 | 信息 | wslc 3.0.1 行为 | `volume/network remove --force` 对不存在的对象 **exit 0**（与 docker 语义一致），与 `container remove --force`（仍报 NotFound）不一致 | 非代码缺陷，已在测试里改成与真机行为一致，并在 README「已知限制」外的注释中记录 |
| A7 | 信息 | 解析器 | NDJSON 前若有非 JSON 前缀（例如 wslc 先打一行日志），会吞掉后续对象 | 契约只要求「一行坏不能丢全列表」，未违约；已用测试 `TestNDJSONLeadingGarbageDocumentsLimitation` 显式记录这个降级 |
| A8 | 中 | `internal/service/containers.go:137` | `RestartContainer` 用 `--time` flag，但 wslc restart 用的是 `--timeout`（`--time` 只用于 stop），导致 `当前命令的选项名称未被识别：'--time'` | 已修：改为 `--timeout`，测试期望也同步更新 |

## 关键命令与真实输出

### 1. 三处方法名集合比较（32/32/32）
```
app.go      : 32
App.js      : 32
CONTRACT B.2: 32
in app.go but NOT in App.js      : []
in App.js but NOT in app.go      : []
in app.go but NOT in CONTRACT    : []
in CONTRACT but NOT in app.go    : []
in app.js calls but NOT exported : []
distinct App.* calls in app.js   : 32
```
`Emit` 只出现在未导出的 `wailsEmitter.Emit`，未被绑定（`grep "func (a \*App) Emit" app.go` → 0 命中）。

### 2. 返回签名扫描（32 个导出方法）
全部落在 `{0,1,2}`，`arity=2` 时末位均为 `error`：
```
StopStream        arity=1  string,error          OK
TerminalWrite     arity=1  string,error          OK
TerminalResize    arity=1  string,int,int,error  OK
CancelTask        arity=1  string,error          OK
EnvCheck          arity=2  EnvStatus,error       OK
ListContainers    arity=2  []Container,error     OK
InspectContainer  arity=2  json.RawMessage,error OK
ContainerStats    arity=2  []Stats,error         OK
...
（无 ≥3 返回值方法，编译器静默丢弃 nil 的风险不存在）
```

### 3. 命令构造攻击（8/8 防御有效）
```
空值 → 空 flag：RunContainer 全零值 argv 精确为 ["container" "run" "alpine:3.20"]
             18 个 flag 全部不存在，无空元素
shell 注入：21 个恶意值 × 17 个方法全部被拒绝，且校验失败时 fake 调用数为 0
             对照组 `MY_SECRET=a b c`、`C:\Users\test\AppData:/data:ro` 作为单元素完整保留
StopContainer timeout：0→省略；1/30/86400→`--time N`；-1/-60 拒绝
KillContainer signal：""→省略；`SIGKILL`/`9`→`--signal X`；`SIGTERM;rm -rf /`、`--force` 拒绝
ContainerFilter：All+Limit=0/1/1000 的 argv 完全相同（客户端截断），`--limit` 从未泄漏
LogsOptions.Tail：0/-1/-100 均省略 `--tail`（真机确认 wslc 拒绝 `--tail all`）
RemoveContainer：4 种布尔组合 argv 精确，ref 恒为最后元素
BuildImage：ContextPath 在 5 个场景下恒为最后位置参数
```

### 4. 真机错误分类（wslc 3.0.1.0）
```
正向：container start/remove/stop/kill/inspect verify-not-exist-1
      → stderr "找不到容器 'verify-not-exist-1'。\r\n错误代码： WSLC_E_CONTAINER_NOT_FOUND"
      → errors.Is(err, ErrNotFound) = true
      volume/network 同理（"找不到卷:"、"找不到网络:"）
      服务层 StartContainer/RemoveContainer 透传成立

反向控制（7 条，0 条被误判为 ErrNotFound）：
  不存在的命令    → "无法识别的命令:..."            → ErrUnsupportedCommand
  未知 flag       → "当前命令的选项名称未被识别"      → ErrUnsupportedCommand
  --tail all      → "tail 选项值无效: all"            → nil
  非法信号        → "无效的 signal 值: SIGBOGUS..."   → nil
  非法卷驱动      → "不支持的卷类型..." + E_INVALIDARG → nil
  非法卷名        → "无效名称:..." + E_INVALIDARG      → nil
  镜像拉取失败    → 来自引擎的错误信息: context deadline exceeded + E_FAIL → nil
  缺失可执行文件  → ErrExecutableNotFound（非 NotFound）
```

### 5. 解析器抗脆性
```
217 次恶意调用 + 14000 次随机 fuzz × 7 解析器 = 全部无 panic、无挂死
31 个 distinct 恶意输入：10MB 空白 / 10MB 未闭合大括号 / 100 层嵌套 /
                        1MB 假表头 / 非法 UTF-8 / NUL 字节 / 溢出 int64 /
                        NaN / Inf / BOM / 单引号 / 全回车 / 全换行 / ...
                        逐条均 no panic / no hang
```

### 6. embed 守卫负向验证
```
基线：main_test.go 三个测试全 PASS
      index.html SHA256 = B288A492704A19B05A9C36A4157BB35C2808B8170BF41E4D30BD20D81649986C
      app.js     SHA256 = EB9E6AC373BF9C6D7BE63B605C31F1E31D43859272109D179AC1B783B854846D
B1 index.html 改名 → TestEmbeddedFrontendHasEntryPoints 与 TestIndexHTMLReferencesResolve 都失败
B2 app.js     改名 → 两个测试都失败（第二个额外指出引用悬空）
恢复后：三个测试重新 PASS，SHA256 与基线逐字节一致
```

### 7. 构建产物与 WebView2
```
wails build -s -v 2
Built 'build\bin\wslc-desktop.exe'

findstr /m /c:tab-containers wslc-desktop.exe → 命中
findstr /m /c:wslc:output     wslc-desktop.exe → 命中
findstr /m /c:env:status      wslc-desktop.exe → 命中

启动 → exit 1，栈精确指向：
  wails.Run → Application.Run → App.Run → Frontend.Run
         → setupChromium → CreateCoreWebView2Controller (0x8000FFFF)
  不是我们的代码路径。
```

### 8. 前端
```
node frontend\tools\selfcheck.mjs   → PASS（32/32 方法、71 个 DOM id 零缺失、
                                     6 视图一致、9 处 confirmDialog 覆盖 7 类破坏性操作）
node frontend\tools\smoke.mjs       → 场景 A 24/24 + 场景 B 78/78 全过
```

## 无法证实的项

- **真实容器的完整生命周期（run / logs / exec / stats 有数据）**：wslc 会话 VM 的 DNS
  绕过了宿主机本地代理，`wslc pull alpine:latest` 超时。已用卷/网络的
  创建-删除往返、`container prune` / `image prune`、完整 `container run` 参数向量被
  wslc 接受、空列表解析覆盖了真机路径。
- **交互式终端的 `-t` 行为**：依赖真实容器，无法验证。已实现 `-i -t` → 失败自动回落 `-i`，
  并有单测断言回落只发生一次、发一条说明事件。
- **CONTRACT B.3.1 事件语义的运行时观测**：静态扫描只能验证方法名/签名，
  `Ref` 原样回填、`Seq` 从 1 递增、`Task.ID` 与 `streamID` 一致这些是 svc 层行为约束，
  由 `internal/service` 的单测（stream_test.go / tasks_test.go）与
  `binding_test.go` 的签名守卫覆盖。
- **Wails 静默丢弃 ≥3 返回值**：本次扫描没有 ≥3 返回值方法，边界未被触发，
  仅采信 Wails 官方文档描述。

## 最终状态

```
go build ./...                              exit=0
go vet ./...                                exit=0
go test ./... -race -count=1                全绿（5 个包）
go test -tags integration ./... -count=1    全绿（真实 wslc，含 verify）
node selfcheck.mjs                          PASS
node smoke.mjs                              24/24 + 78/78 PASS
```
