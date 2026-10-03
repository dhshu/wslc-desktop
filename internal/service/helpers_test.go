package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// ---------------------------------------------------------------------------
// Fixtures: real wslc 3.0.1 output, captured on this machine.
// ---------------------------------------------------------------------------

const (
	fixtureVersionJSON = `{"Client":{"Version":"3.0.1.0"}}`

	fixtureInfoJSON = `{"Client":{"Direct3DVersion":"1.611.1-81528511","DxCoreVersion":"10.0.26100.1-240331-1435.ge-release",` +
		`"KernelVersion":"6.18.40.1-1","SettingsFile":"C:\\Users\\dhshu\\AppData\\Local\\wslc\\settings.yaml",` +
		`"Version":"3.0.1.0","WindowsVersion":"10.0.26300.9550"},` +
		`"Server":{"SessionManagerVersion":"3.0.1","Sessions":[{"CreatorPid":23428,"ID":1,"Name":"wslc-cli-dhshu"}]}}`

	// system session list rejects --format, so this is the real table form.
	fixtureSessionTable = "ID   创建者 PID   显示名称\n1    23428    wslc-cli-dhshu\n"

	fixtureContainerListJSON = `[` +
		`{"ID":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Names":["/web1"],"Image":"nginx:latest",` +
		`"ImageID":"sha256:1111","State":"running","Status":"Up 5 minutes","Ports":["0.0.0.0:8080->80/tcp"]},` +
		`{"ID":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","Names":["/db1"],"Image":"postgres:16",` +
		`"State":"exited","Status":"Exited (0) 2 hours ago"},` +
		`{"ID":"cccccccccccccccccccccccccccccccccccccccc","Names":["/web2"],"Image":"nginx:alpine",` +
		`"State":"running","Status":"Up 1 minute"}]`

	fixtureStatsJSON = `[{"ID":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","Name":"web1","CPUPerc":"0.50%",` +
		`"MemUsage":"1MiB / 2GiB","MemPerc":"0.05%","NetIO":"1kB / 2kB","BlockIO":"0B / 0B","PIDs":12}]`
)

func listArgs() []string    { return []string{"container", "list", "--format", "json"} }
func versionArgs() []string { return []string{"version", "--format", "json"} }
func infoArgs() []string    { return []string{"info", "--format", "json"} }
func sessionArgs() []string { return []string{"system", "session", "list"} }
func inspectArgs() []string { return []string{"container", "inspect", "web"} }
func imgInsArgs() []string  { return []string{"image", "inspect", "nginx:1"} }
func pullArgs() []string    { return []string{"image", "pull", "bad:1"} }
func startArgs() []string   { return []string{"container", "start", "ghost"} }
func statsArgs() []string   { return []string{"container", "stats", "--format", "json"} }

// ---------------------------------------------------------------------------
// Doubles
// ---------------------------------------------------------------------------

// recordEmitter captures every broadcast event for assertions.
type recordEmitter struct {
	mu     sync.Mutex
	events []OutputEvent
}

func (r *recordEmitter) Emit(event OutputEvent) {
	r.mu.Lock()
	r.events = append(r.events, event)
	r.mu.Unlock()
}

func (r *recordEmitter) snapshot() []OutputEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]OutputEvent(nil), r.events...)
}

func (r *recordEmitter) forChannel(channel string) []OutputEvent {
	var out []OutputEvent
	for _, event := range r.snapshot() {
		if event.Channel == channel {
			out = append(out, event)
		}
	}
	return out
}

// blockingRunner streams one line and then blocks until its context is
// cancelled, which is what makes CancelTask/StopStream observable.
type blockingRunner struct {
	started chan struct{}
	once    sync.Once
}

func newBlockingRunner() *blockingRunner {
	return &blockingRunner{started: make(chan struct{})}
}

func (b *blockingRunner) Run(context.Context, wslc.Spec) (wslc.Result, error) {
	return wslc.Result{}, nil
}

func (b *blockingRunner) Available(context.Context) error { return nil }

func (b *blockingRunner) Stream(ctx context.Context, spec wslc.Spec, sink func(wslc.Line)) (wslc.Result, error) {
	b.once.Do(func() { close(b.started) })
	if sink != nil {
		sink(wslc.Line{Stream: "stdout", Text: "first line", Time: time.Now()})
	}
	<-ctx.Done()
	return wslc.Result{Args: spec.Args}, ctx.Err()
}

// fakeTerminalProcess records stdin writes and resizes, and blocks in Wait
// until the test releases it.
type fakeTerminalProcess struct {
	mu      sync.Mutex
	writes  []string
	resizes [][2]int
	release chan struct{}
	waitErr error
	closed  bool
}

func newFakeTerminalProcess() *fakeTerminalProcess {
	return &fakeTerminalProcess{release: make(chan struct{})}
}

func (p *fakeTerminalProcess) Write(data string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("closed")
	}
	p.writes = append(p.writes, data)
	return nil
}

func (p *fakeTerminalProcess) Resize(cols, rows int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.resizes = append(p.resizes, [2]int{cols, rows})
	return nil
}

func (p *fakeTerminalProcess) Wait() error {
	<-p.release
	return p.waitErr
}

func (p *fakeTerminalProcess) finish(err error) {
	p.mu.Lock()
	p.waitErr = err
	p.closed = true
	p.mu.Unlock()
	close(p.release)
}

func (p *fakeTerminalProcess) written() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.writes...)
}

func (p *fakeTerminalProcess) resized() [][2]int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([][2]int(nil), p.resizes...)
}

// fakeTerminalPlan scripts one Start attempt. When plans are supplied they are
// consumed in order, which is what makes the TTY fallback observable.
type fakeTerminalPlan struct {
	lines    []wslc.Line
	startErr error
	// waitErr, when set, makes the process exit immediately with that error;
	// otherwise Wait blocks until the test releases it.
	waitErr error
}

// fakeTerminalDriver scripts the interactive seam used by StartTerminal.
type fakeTerminalDriver struct {
	mu       sync.Mutex
	specs    [][]string
	sizes    [][2]int
	lines    []wslc.Line
	proc     *fakeTerminalProcess
	startErr error
	plans    []fakeTerminalPlan
}

func (d *fakeTerminalDriver) Start(_ context.Context, args []string, cols, rows int, sink func(wslc.Line)) (terminalProcess, error) {
	d.mu.Lock()
	d.specs = append(d.specs, append([]string(nil), args...))
	d.sizes = append(d.sizes, [2]int{cols, rows})

	var plan *fakeTerminalPlan
	if len(d.plans) > 0 {
		next := d.plans[0]
		d.plans = d.plans[1:]
		plan = &next
	}
	startErr := d.startErr
	lines := append([]wslc.Line(nil), d.lines...)
	if plan != nil {
		startErr = plan.startErr
		lines = append([]wslc.Line(nil), plan.lines...)
	}

	proc := newFakeTerminalProcess()
	if plan != nil && plan.waitErr != nil {
		// Exiting immediately: release the waiter up front.
		proc.waitErr = plan.waitErr
		proc.closed = true
		close(proc.release)
	}
	if startErr == nil {
		d.proc = proc
	}
	d.mu.Unlock()

	if startErr != nil {
		return nil, startErr
	}
	if sink != nil {
		for _, line := range lines {
			sink(line)
		}
	}
	return proc, nil
}

func (d *fakeTerminalDriver) awaitProcess(t *testing.T) *fakeTerminalProcess {
	t.Helper()
	return d.awaitAttempt(t, 1)
}

// awaitAttempt waits until at least want Start calls have happened and returns
// the process of the latest one.
func (d *fakeTerminalDriver) awaitAttempt(t *testing.T, want int) *fakeTerminalProcess {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		started := len(d.specs)
		proc := d.proc
		d.mu.Unlock()
		if started >= want && proc != nil {
			return proc
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("终端第 %d 次启动未发生", want)
	return nil
}

func (d *fakeTerminalDriver) attempts() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([][]string, 0, len(d.specs))
	for _, spec := range d.specs {
		out = append(out, append([]string(nil), spec...))
	}
	return out
}

func (d *fakeTerminalDriver) lastSpec() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.specs) == 0 {
		return nil
	}
	return append([]string(nil), d.specs[len(d.specs)-1]...)
}

func (d *fakeTerminalDriver) lastSize() [2]int {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.sizes) == 0 {
		return [2]int{}
	}
	return d.sizes[len(d.sizes)-1]
}

// ---------------------------------------------------------------------------
// Builders
// ---------------------------------------------------------------------------

// newTestService returns a service over a FakeRunner whose default script
// succeeds with empty output, which every parser turns into "no rows".
//
// The settings store is disabled so these tests exercise command construction
// exactly as it was before registry mirroring existed; the settings tests in
// settings_test.go enable mirroring explicitly where they need it.
func newTestService(t *testing.T) (*Service, *wslc.FakeRunner, *recordEmitter) {
	t.Helper()
	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	emitter := &recordEmitter{}
	svc := NewService(fake, emitter)
	svc.settings = nil
	return svc, fake, emitter
}

// newEnvFake scripts the four probes EnvCheck performs, in a healthy state.
func newEnvFake(t *testing.T) (*Service, *wslc.FakeRunner, *recordEmitter) {
	t.Helper()
	svc, fake, emitter := newTestService(t)
	fake.When(versionArgs(), wslc.Result{Stdout: fixtureVersionJSON}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: fixtureInfoJSON}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: fixtureSessionTable}, nil)
	fake.When(listArgs(), wslc.Result{Stdout: "[]"}, nil)
	return svc, fake, emitter
}

// newContainerFake scripts a three-row container list.
func newContainerFake(t *testing.T) (*Service, *wslc.FakeRunner, *recordEmitter) {
	t.Helper()
	svc, fake, emitter := newTestService(t)
	fake.When(listArgs(), wslc.Result{Stdout: fixtureContainerListJSON}, nil)
	return svc, fake, emitter
}

// ---------------------------------------------------------------------------
// Assertions
// ---------------------------------------------------------------------------

func exitError(args []string, code int, stderr string) error {
	return &wslc.ExitError{Code: code, Args: args, Stderr: stderr}
}

// serviceUnavailableErr reproduces what wslc 3.0.1 prints while vmcompute is
// stopped; the adapter maps it onto wslc.ErrServiceUnavailable.
func serviceUnavailableErr(args []string) error {
	return exitError(args, 1, "由于未安装所需的特性，无法启动操作。 \n错误代码： HCS_E_SERVICE_NOT_AVAILABLE")
}

func notFoundErr(args []string) error {
	return exitError(args, 1, "Error: No such container: ghost")
}

func unsupportedErr(args []string) error {
	return exitError(args, 1, "当前命令的选项名称未被识别：'--format'")
}

func callArgs(calls []wslc.Spec) [][]string {
	out := make([][]string, 0, len(calls))
	for _, call := range calls {
		out = append(out, call.Args)
	}
	return out
}

func assertOnlyCall(t *testing.T, calls []wslc.Spec, want []string) {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("期望恰好 1 次 wslc 调用，实际 %d 次：%q", len(calls), callArgs(calls))
	}
	assertArgsEqual(t, calls[0].Args, want)
}

func assertArgsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("参数个数不同：\n实际 %q\n期望 %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个参数不同：\n实际 %q\n期望 %q", i, got, want)
		}
	}
}

// waitForTask blocks until a task leaves the running state.
func waitForTask(t *testing.T, svc *Service, id string) Task {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := svc.ListTasks(context.Background())
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		for _, task := range tasks {
			if task.ID == id {
				if task.State != TaskRunning {
					return task
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("任务 %q 未在 5s 内结束", id)
	return Task{}
}

// waitForStreamCall returns the single streamed Spec the fake recorded.
func waitForStreamCall(t *testing.T, fake *wslc.FakeRunner) wslc.Spec {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, call := range fake.Calls() {
			if call.Stream {
				return call
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("未观察到流式调用")
	return wslc.Spec{}
}

func assertNoFailure(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，实际错误：%v", err)
	}
}

func problemsContain(problems []string, needles ...string) bool {
	joined := strings.Join(problems, "\n")
	for _, needle := range needles {
		if !strings.Contains(joined, needle) {
			return false
		}
	}
	return true
}
