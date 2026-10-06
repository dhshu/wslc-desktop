// Package service is the business layer behind the Wails bindings.
//
// It owns no GUI code on purpose: docs/CONTRACT.md B forbids importing Wails
// here, so the whole layer stays testable with wslc.FakeRunner and no desktop
// session. It depends on internal/wslc (command construction + parsing) and
// internal/domain (read models) only.
//
// Binding rules the code below obeys:
//
//   - every exported method takes a context first and returns either (T, error)
//     or (error); Wails v2 silently drops methods with 0 or 3+ results;
//   - every cross-boundary struct field carries a named json tag (the generated
//     TypeScript model, and the already-written frontend, read those names);
//   - no anonymous embedded fields.
//
// Nothing here ever builds a shell string: arguments are passed as vectors to
// the runner, and user input that is not a flag value is validated so it cannot
// be re-interpreted as a flag.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// Output channels carried by OutputEvent.Channel. The strings are frozen by
// docs/CONTRACT.md B.3 and match the frontend constants verbatim.
const (
	ChannelLogs     = "container-logs"
	ChannelTerminal = "terminal"
	ChannelBuild    = "build"
	ChannelPull     = "pull"
	ChannelEvents   = "events"
	ChannelTask     = "task"
)

// Event names used with runtime.EventsEmit / EventsOn.
const (
	// EventName carries every OutputEvent.
	EventName = "wslc:output"
	// EnvEventName carries the EnvStatus broadcast at startup and on demand.
	EnvEventName = "env:status"
)

// Task states (lowercase, frozen by docs/CONTRACT.md B.1).
const (
	TaskRunning   = "running"
	TaskSucceeded = "succeeded"
	TaskFailed    = "failed"
	TaskCanceled  = "canceled"
)

// Task kinds reported in Task.Kind.
const (
	TaskKindLogs     = "container-logs"
	TaskKindTerminal = "terminal"
	TaskKindPull     = "image-pull"
	TaskKindBuild    = "image-build"
	TaskKindEvents   = "events"
)

// Emitter is the broadcast outlet, implemented by app.go with
// runtime.EventsEmit. Keeping it an interface is what makes the service layer
// testable without Wails.
type Emitter interface {
	Emit(event OutputEvent)
}

// ToolRunner is the optional capability of a Runner that can execute a
// non-wslc Windows executable with arguments passed as a vector and stdin fed
// from a string. It exists only for storage maintenance: shrinking a session
// VHDX requires `diskpart`, which is not a wslc command and therefore cannot
// travel through the Spec/Runner interface without pretending to be one.
//
// Keeping this as a separate, optional interface means the frozen Runner
// contract stays untouched, and every test fake that implements only Runner
// keeps compiling unchanged.
type ToolRunner interface {
	// RunTool runs exe with args (never a shell string) and stdin, capturing
	// stdout/stderr. dir is the working directory; empty inherits.
	RunTool(ctx context.Context, exe string, args []string, stdin, dir string) (wslc.Result, error)
}

// EmitterFunc adapts a plain function to Emitter.
type EmitterFunc func(OutputEvent)

// Emit calls f, tolerating a nil function.
func (f EmitterFunc) Emit(event OutputEvent) {
	if f != nil {
		f(event)
	}
}

// EnvStatus is the result of EnvCheck and the payload of the "env:status" event.
type EnvStatus struct {
	Available     bool             `json:"Available"`
	WslcPath      string           `json:"WslcPath"`
	WslcVersion   string           `json:"WslcVersion"`
	WSLVersion    string           `json:"WSLVersion"`
	KernelVersion string           `json:"KernelVersion"`
	SettingsFile  string           `json:"SettingsFile"`
	ServiceReady  bool             `json:"ServiceReady"`
	Sessions      []domain.Session `json:"Sessions"`
	Problems      []string         `json:"Problems"`
	// ActiveMirror is the registry endpoint the app will rewrite Docker Hub
	// pulls through ("" when mirroring is disabled). Shown on the settings
	// view so the user can see what is actually taking effect.
	ActiveMirror string `json:"ActiveMirror"`
	// SettingsPath is where the app reads and writes its own settings JSON.
	SettingsPath string    `json:"SettingsPath"`
	CheckedAt    time.Time `json:"CheckedAt"`
}

// ContainerFilter narrows ListContainers. Query/State/Limit are applied in the
// service layer because wslc's own --filter syntax is not portable across its
// versions.
type ContainerFilter struct {
	All   bool   `json:"All"`
	Query string `json:"Query"`
	State string `json:"State"`
	Limit int    `json:"Limit"`
}

// RunContainerOptions describes one `wslc container run` invocation.
type RunContainerOptions struct {
	Image      string   `json:"Image"`
	Name       string   `json:"Name"`
	Command    []string `json:"Command"`
	Detach     bool     `json:"Detach"`
	Remove     bool     `json:"Remove"`
	TTY        bool     `json:"TTY"`
	Env        []string `json:"Env"`
	Ports      []string `json:"Ports"`
	Volumes    []string `json:"Volumes"`
	Network    string   `json:"Network"`
	WorkDir    string   `json:"WorkDir"`
	User       string   `json:"User"`
	Hostname   string   `json:"Hostname"`
	Memory     string   `json:"Memory"`
	CPUs       string   `json:"CPUs"`
	Entrypoint string   `json:"Entrypoint"`
	Labels     []string `json:"Labels"`
	Pull       string   `json:"Pull"`
}

// LogsOptions describes a `wslc container logs` invocation.
type LogsOptions struct {
	Follow     bool   `json:"Follow"`
	Tail       int    `json:"Tail"`
	Timestamps bool   `json:"Timestamps"`
	Since      string `json:"Since"`
	Until      string `json:"Until"`
}

// Args renders the flag vector for `wslc container logs`.
//
// The order is frozen so tests can assert the exact argv: follow, tail,
// timestamps, since, until.
//
// Tail < 0 means "the whole log". It is expressed by omitting --tail, because
// wslc 3.0.1 answers `--tail all` with `tail 选项值无效: all`; the flag's own
// default already prints the entire log.
func (o LogsOptions) Args() []string {
	args := make([]string, 0, 8)
	if o.Follow {
		args = append(args, "--follow")
	}
	if o.Tail > 0 {
		args = append(args, "--tail", strconv.Itoa(o.Tail))
	}
	if o.Timestamps {
		args = append(args, "--timestamps")
	}
	if since := strings.TrimSpace(o.Since); since != "" {
		args = append(args, "--since", since)
	}
	if until := strings.TrimSpace(o.Until); until != "" {
		args = append(args, "--until", until)
	}
	return args
}

// ExecOptions describes a `wslc container exec` invocation.
type ExecOptions struct {
	TTY     bool     `json:"TTY"`
	User    string   `json:"User"`
	WorkDir string   `json:"WorkDir"`
	Env     []string `json:"Env"`
}

// Args renders the flag vector for `wslc container exec`.
//
// -i is always present: the service layer feeds stdin through a pipe, so
// without it the terminal would be read-only. -t is only added when the caller
// asked for a TTY, so a zero-value ExecOptions stays minimal.
func (o ExecOptions) Args() []string {
	args := make([]string, 0, 8)
	args = append(args, "--interactive")
	if o.TTY {
		args = append(args, "--tty")
	}
	if user := strings.TrimSpace(o.User); user != "" {
		args = append(args, "--user", user)
	}
	if workdir := strings.TrimSpace(o.WorkDir); workdir != "" {
		args = append(args, "--workdir", workdir)
	}
	for _, entry := range o.Env {
		if value := strings.TrimSpace(entry); value != "" {
			args = append(args, "--env", value)
		}
	}
	return args
}

// BuildOptions describes one `wslc image build` invocation.
type BuildOptions struct {
	Context    string   `json:"Context"`
	Dockerfile string   `json:"Dockerfile"`
	Tags       []string `json:"Tags"`
	BuildArgs  []string `json:"BuildArgs"`
	Target     string   `json:"Target"`
	NoCache    bool     `json:"NoCache"`
	Pull       bool     `json:"Pull"`
	Labels     []string `json:"Labels"`
	Progress   string   `json:"Progress"`
}

// PruneResult is the textual summary printed by a prune command.
type PruneResult struct {
	Stdout string `json:"Stdout"`
}

// OutputEvent is the payload broadcast on every output channel. It is what the
// frontend receives from EventsOn("wslc:output").
type OutputEvent struct {
	Channel string    `json:"Channel"`
	Ref     string    `json:"Ref"`
	Stream  string    `json:"Stream"`
	Text    string    `json:"Text"`
	Seq     int       `json:"Seq"`
	Time    time.Time `json:"Time"`
}

// Task is one long-running wslc operation (log follow, terminal, pull, build,
// event stream) tracked so the UI can list, inspect and cancel it.
type Task struct {
	ID        string    `json:"ID"`
	Kind      string    `json:"Kind"`
	Ref       string    `json:"Ref"`
	State     string    `json:"State"`
	Output    string    `json:"Output"`
	Error     string    `json:"Error"`
	StartedAt time.Time `json:"StartedAt"`
	EndedAt   time.Time `json:"EndedAt"`
}

// maxTaskOutput caps how much streamed text is retained per task.
const maxTaskOutput = 256 << 10

// streamSession tracks one live stream: its task, its cancel func, the event
// reference it echoes, and its own sequence counter.
type streamSession struct {
	id      string
	taskID  string
	kind    string
	channel string
	// ref is echoed verbatim in every OutputEvent.Ref: the caller's container
	// reference for logs/terminal, or the task id for task-channel streams.
	ref    string
	cancel context.CancelFunc
	cols   int
	rows   int
	seq    int64
	procMu sync.Mutex
	proc   terminalProcess
}

// nextSeq returns the stream's next 1-based sequence number.
func (sess *streamSession) nextSeq() int {
	return int(atomic.AddInt64(&sess.seq, 1))
}

func (sess *streamSession) setProcess(p terminalProcess) {
	sess.procMu.Lock()
	sess.proc = p
	sess.procMu.Unlock()
}

func (sess *streamSession) process() terminalProcess {
	sess.procMu.Lock()
	defer sess.procMu.Unlock()
	return sess.proc
}

// Service orchestrates wslc for the desktop UI. It is safe for concurrent use.
type Service struct {
	runner   wslc.Runner
	emitter  Emitter
	terminal terminalDriver
	// settings owns the user's registry-mirror and proxy preferences. It is
	// consulted by PullImage/RunContainer so those commands honour them.
	settings *Settings

	now   func() time.Time
	newID func(prefix string) string

	mu      sync.Mutex
	streams map[string]*streamSession
	tasks   map[string]*Task
	order   []string
}

// NewService wires the wslc runner and the broadcast outlet together.
//
// A nil runner or nil emitter is tolerated: every call then fails or stays
// silent with a clear message instead of panicking, which keeps the "backend
// not ready" fallback of the frontend honest.
func NewService(r wslc.Runner, e Emitter) *Service {
	s := &Service{
		runner:   r,
		emitter:  e,
		settings: NewSettings(),
		now:      time.Now,
		newID:    defaultID,
		streams:  make(map[string]*streamSession),
		tasks:    make(map[string]*Task),
	}
	s.terminal = execTerminalDriver{executable: s.executable}
	return s
}

// DisableSettingsForTest turns off the registry-mirror and proxy rewrite so a
// caller can assert argv construction in isolation. It exists only so that the
// argv contract tests (internal/verify) keep asserting the pre-mirror baseline;
// the mirror behaviour itself is covered by internal/service/settings_test.go.
// Production code must not call this.
func (s *Service) DisableSettingsForTest() {
	if s == nil {
		return
	}
	s.settings = nil
}

// clock is a nil-safe wrapper around the injectable clock.
func (s *Service) clock() time.Time {
	if s != nil && s.now != nil {
		return s.now()
	}
	return time.Now()
}

// id mints a stream/task id with the injectable generator.
func (s *Service) id(prefix string) string {
	if s != nil && s.newID != nil {
		return s.newID(prefix)
	}
	return defaultID(prefix)
}

var idCounter atomic.Uint64

func defaultID(prefix string) string {
	return fmt.Sprintf("%s-%d-%d", prefix, time.Now().UnixMilli(), idCounter.Add(1))
}

// emitStream broadcasts an event on behalf of one stream. Sequence numbers are
// per stream and start at 1, which is what the frontend sorts on.
func (s *Service) emitStream(sess *streamSession, event OutputEvent) {
	if s == nil || s.emitter == nil || sess == nil {
		return
	}
	if event.Time.IsZero() {
		event.Time = s.clock()
	}
	if event.Seq == 0 {
		event.Seq = sess.nextSeq()
	}
	s.emitter.Emit(event)
}

// run executes a one-shot command. The Runner contract returns a populated
// Result together with an *ExitError on a non-zero exit, so callers get both.
func (s *Service) run(ctx context.Context, spec wslc.Spec) (wslc.Result, error) {
	if s == nil || s.runner == nil {
		return wslc.Result{}, errors.New("service: 未配置 wslc runner")
	}
	return s.runner.Run(normalizeContext(ctx), spec)
}

// stream executes a streaming command, forcing Spec.Stream on.
func (s *Service) stream(ctx context.Context, spec wslc.Spec, sink func(wslc.Line)) (wslc.Result, error) {
	if s == nil || s.runner == nil {
		return wslc.Result{}, errors.New("service: 未配置 wslc runner")
	}
	spec.Stream = true
	return s.runner.Stream(normalizeContext(ctx), spec, sink)
}

func normalizeContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// contextError reports a cancelled or expired caller context so a method can
// fail fast instead of doing work whose result nobody will read.
func contextError(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	return ctx.Err()
}

// executable resolves the wslc binary: the one the runner was built with when
// it exposes it, otherwise the resolver's search order.
func (s *Service) executable() string {
	if exe := s.runnerExe(); exe != "" {
		return exe
	}
	if path, err := wslc.NewResolver().Resolve(); err == nil {
		return path
	}
	return ""
}

// runnerExe returns the executable the runner exposes, if any.
//
// The Runner interface itself has no Exe method (the frozen contract does not
// include one), so the adapter's ExecRunner is detected through an optional
// interface. A FakeRunner simply has no path, which keeps EnvCheck
// deterministic in tests.
func (s *Service) runnerExe() string {
	if s == nil || s.runner == nil {
		return ""
	}
	if withExe, ok := s.runner.(interface{ Exe() string }); ok {
		return strings.TrimSpace(withExe.Exe())
	}
	return ""
}

// outputOr picks the text a method returns to the UI: stdout, then stderr, then
// a caller-supplied fallback message.
func outputOr(res wslc.Result, fallback string) string {
	if out := strings.TrimSpace(res.Stdout); out != "" {
		return out
	}
	if out := strings.TrimSpace(res.Stderr); out != "" {
		return out
	}
	return fallback
}

// inspectJSON validates that an inspect command produced JSON.
//
// `wslc container inspect` / `wslc image inspect` print (indented) JSON by
// default; -f on those commands is a *format template*, not a file, so we never
// pass it. Anything that is not JSON is reported with a short excerpt.
func inspectJSON(res wslc.Result) (json.RawMessage, error) {
	out := strings.TrimSpace(res.Stdout)
	if out == "" {
		return nil, errors.New("service: inspect 没有任何输出")
	}
	if !json.Valid([]byte(out)) {
		return nil, fmt.Errorf("service: inspect 的输出不是合法 JSON（前 120 字符：%s）", truncate(out, 120))
	}
	return json.RawMessage(out), nil
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(strings.ReplaceAll(s, "\r", " "), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// formatCommand renders an argv vector for the human-readable "system" event.
func formatCommand(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		if arg == "" || strings.ContainsAny(arg, " \t\"'") {
			parts = append(parts, strconv.Quote(arg))
			continue
		}
		parts = append(parts, arg)
	}
	return "wslc " + strings.Join(parts, " ")
}

// registerStream remembers a live stream so StopStream/CancelTask can reach it.
func (s *Service) registerStream(sess *streamSession) {
	s.mu.Lock()
	if s.streams == nil {
		s.streams = make(map[string]*streamSession)
	}
	s.streams[sess.id] = sess
	s.mu.Unlock()
}

func (s *Service) lookupStream(id string) *streamSession {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

func (s *Service) unregisterStream(id string) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}
