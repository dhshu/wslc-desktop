package service

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// windowsShell returns cmd.exe, skipping the test elsewhere. The desktop app
// only targets Windows, so the production driver can be covered with a real
// (harmless) child process instead of a mock.
func windowsShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only: uses cmd.exe as the interactive child")
	}
	if shell := os.Getenv("ComSpec"); shell != "" {
		return shell
	}
	return `C:\Windows\System32\cmd.exe`
}

// TestExecTerminalDriverLifecycle exercises the production interactive driver
// against a real process, so the pipe plumbing, line pumping, stdin writes and
// the readers-before-Wait ordering are covered without wslc or a container.
func TestExecTerminalDriverLifecycle(t *testing.T) {
	driver := execTerminalDriver{executable: func() string { return windowsShell(t) }}

	var mu sync.Mutex
	var lines []wslc.Line
	sink := func(line wslc.Line) {
		mu.Lock()
		lines = append(lines, line)
		mu.Unlock()
	}

	// `echo first` prints one line; `more` then copies stdin to stdout, which
	// is what lets the test observe a successful Write.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	proc, err := driver.Start(ctx, []string{"/c", "echo first & more"}, 80, 24, sink)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := proc.Resize(100, 30); err != nil {
		t.Errorf("Resize 应为无操作成功，实际：%v", err)
	}
	if err := proc.Write("second\r\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		joined := lineTexts(lines)
		mu.Unlock()
		if strings.Contains(joined, "first") && strings.Contains(joined, "second") {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	joined := lineTexts(lines)
	collected := append([]wslc.Line(nil), lines...)
	mu.Unlock()

	if !strings.Contains(joined, "first") {
		t.Errorf("应收到子进程输出 first，实际：%q", joined)
	}
	if !strings.Contains(joined, "second") {
		t.Errorf("stdin 写入应被 more 回显，实际：%q", joined)
	}
	for _, line := range collected {
		if line.Stream != "stdout" && line.Stream != "stderr" {
			t.Errorf("行必须带 stdout/stderr 标记：%+v", line)
		}
		if line.Time.IsZero() {
			t.Errorf("行必须带时间戳：%+v", line)
		}
	}

	// Cancelling is how a terminal is torn down; Wait must then return.
	cancel()
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("取消后 Wait 未返回（reader/Wait 顺序有问题）")
	}
}

// TestExecTerminalDriverMissingExecutable covers the "no wslc path" branch that
// keeps the terminal feature honest when the executable is unknown.
func TestExecTerminalDriverMissingExecutable(t *testing.T) {
	driver := execTerminalDriver{executable: func() string { return "  " }}
	if _, err := driver.Start(context.Background(), []string{"container", "exec"}, 80, 24, nil); err == nil {
		t.Fatal("缺少可执行文件时应报错")
	}
}

// TestExecTerminalDriverNilSinkDrains proves a nil sink still drains the pipes
// (a child that fills a pipe would otherwise deadlock Wait).
func TestExecTerminalDriverNilSinkDrains(t *testing.T) {
	driver := execTerminalDriver{executable: func() string { return windowsShell(t) }}
	proc, err := driver.Start(context.Background(), []string{"/c", "echo drained"}, 80, 24, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := proc.Wait(); err != nil {
		t.Errorf("无 sink 时仍应正常结束，实际：%v", err)
	}
}

// TestExecTerminalProcessNilHandle covers the defensive branches of the handle.
func TestExecTerminalProcessNilHandle(t *testing.T) {
	var proc *execTerminalProcess
	if err := proc.Write("x"); err == nil {
		t.Error("nil 句柄的 Write 应返回错误")
	}
	if err := proc.Wait(); err != nil {
		t.Errorf("nil 句柄的 Wait 应返回 nil，实际：%v", err)
	}
	if err := proc.Resize(10, 10); err != nil {
		t.Errorf("Resize 应始终成功，实际：%v", err)
	}
}

func lineTexts(lines []wslc.Line) string {
	texts := make([]string, 0, len(lines))
	for _, line := range lines {
		texts = append(texts, line.Text)
	}
	return strings.Join(texts, "\n")
}

// staticRunner exposes Exe() to cover the optional-interface branch.
type staticRunner struct {
	exe string
}

func (r staticRunner) Run(context.Context, wslc.Spec) (wslc.Result, error) {
	return wslc.Result{}, nil
}

func (r staticRunner) Stream(context.Context, wslc.Spec, func(wslc.Line)) (wslc.Result, error) {
	return wslc.Result{}, nil
}

func (r staticRunner) Available(context.Context) error { return nil }

func (r staticRunner) Exe() string { return r.exe }

// TestExecutableAndRunnerExe covers both ways the wslc path is discovered.
func TestExecutableAndRunnerExe(t *testing.T) {
	// A runner that exposes Exe() wins and reports the configured path.
	svc := NewService(staticRunner{exe: `C:\tools\wslc.exe`}, nil)
	if got := svc.runnerExe(); got != `C:\tools\wslc.exe` {
		t.Errorf("runnerExe 应返回 runner 的路径，实际 %q", got)
	}
	if got := svc.executable(); got != `C:\tools\wslc.exe` {
		t.Errorf("executable 应优先用 runner 的路径，实际 %q", got)
	}

	// A runner without Exe() falls back to the real resolver (or to "" when
	// wslc is not installed; either way it must not panic).
	plain := NewService(wslc.NewFakeRunner(), nil)
	if got := plain.runnerExe(); got != "" {
		t.Errorf("FakeRunner 不应报告路径，实际 %q", got)
	}
	_ = plain.executable()

	// No runner at all is tolerated.
	empty := NewService(nil, nil)
	if got := empty.runnerExe(); got != "" {
		t.Errorf("无 runner 时应返回空路径，实际 %q", got)
	}
}

// TestDescribeAvailabilityDefaultBranch covers the non-sentinel failure (for
// example a permission problem).
func TestDescribeAvailabilityDefaultBranch(t *testing.T) {
	got := describeAvailability(errors.New("Access is denied."))
	if !strings.Contains(got, "Access is denied") || !strings.Contains(got, "WSLC_PATH") {
		t.Fatalf("兜底文案应包含原始错误与建议，实际：%q", got)
	}
	if !strings.Contains(describeAvailability(wslc.ErrServiceUnavailable), "vmcompute") {
		t.Error("服务不可用分支应给出 vmcompute 修复步骤")
	}
}

// TestEnvCheckUnexpectedAvailableError uses the default branch through EnvCheck.
func TestEnvCheckUnexpectedAvailableError(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.SetAvailable(errors.New("Access is denied."))

	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if status.Available {
		t.Error("Available 探测失败时不应判为可用")
	}
	if !problemsContain(status.Problems, "Access is denied", "WSLC_PATH") {
		t.Fatalf("应给出兜底建议，实际：%v", status.Problems)
	}
}

// TestEmitterFunc covers the small adapter app.go can use.
func TestEmitterFunc(t *testing.T) {
	var got []OutputEvent
	emit := EmitterFunc(func(event OutputEvent) { got = append(got, event) })
	emit.Emit(OutputEvent{Text: "x"})
	if len(got) != 1 || got[0].Text != "x" {
		t.Fatalf("EmitterFunc 未转发事件：%+v", got)
	}
	var nilEmitter EmitterFunc
	nilEmitter.Emit(OutputEvent{}) // must not panic
}

// TestTruncateAndFormatCommand covers the two small text helpers.
func TestTruncateAndFormatCommand(t *testing.T) {
	if got := truncate("a\nb", 10); got != "a b" {
		t.Errorf("换行应折叠成空格，实际 %q", got)
	}
	if got := truncate("0123456789", 3); got != "012…" {
		t.Errorf("超长应截断并加省略号，实际 %q", got)
	}
	got := formatCommand([]string{"container", "logs", "--tail", "hello world", "web"})
	if got != `wslc container logs --tail "hello world" web` {
		t.Errorf("含空格的参数应加引号，实际 %q", got)
	}
	if got := formatCommand([]string{"events"}); got != "wslc events" {
		t.Errorf("普通参数不应加引号，实际 %q", got)
	}
}

// TestClockAndIDFallbacks covers the zero-value Service guards.
func TestClockAndIDFallbacks(t *testing.T) {
	zero := &Service{}
	if zero.clock().IsZero() {
		t.Error("零值 Service 的 clock 应返回当前时间")
	}
	if got := zero.id("task"); !strings.HasPrefix(got, "task-") {
		t.Errorf("零值 Service 的 id 应可用，实际 %q", got)
	}
	if got := defaultID("task"); !strings.HasPrefix(got, "task-") {
		t.Errorf("defaultID 前缀不符：%q", got)
	}
	if first, second := defaultID("task"), defaultID("task"); first == second {
		t.Error("id 必须唯一")
	}
}

// TestZeroValueTolerance covers a Service that was built without NewService:
// the zero value must not panic, and must fail with a clear message instead.
// (A nil *Service is a programming error; only EnvCheck is documented to
// degrade gracefully there, because that is the call the backend makes first.)
func TestZeroValueTolerance(t *testing.T) {
	zero := &Service{}
	if _, err := zero.ListContainers(context.Background(), ContainerFilter{}); err == nil {
		t.Error("零值 Service 应返回明确的 runner 缺失错误")
	}
	if _, err := zero.PruneContainers(context.Background()); err == nil {
		t.Error("零值 Service 应返回明确错误")
	}
	tasks, err := zero.ListTasks(context.Background())
	if err != nil || len(tasks) != 0 {
		t.Errorf("零值 Service 的 ListTasks 应为空列表：%v %v", tasks, err)
	}
	status, err := zero.EnvCheck(context.Background())
	if err != nil {
		t.Fatalf("零值 Service 的 EnvCheck 应返回 Problems 而不是错误：%v", err)
	}
	if len(status.Problems) == 0 {
		t.Error("零值 Service 应报告问题")
	}
	// A streaming call registers its task synchronously, so the missing runner
	// shows up in the task instead of the return value.
	id, err := zero.StartLogs(context.Background(), "web", LogsOptions{})
	if err != nil {
		t.Fatalf("StartLogs 应登记任务并返回 id：%v", err)
	}
	if strings.TrimSpace(id) == "" {
		t.Error("StartLogs 必须返回 id")
	}
	if task := waitForTask(t, zero, id); task.State != TaskFailed {
		t.Errorf("缺少 runner 时任务应为 failed，实际 %q", task.State)
	}

	// A nil *Service still renders the first screen instead of panicking.
	var nilService *Service
	status, err = nilService.EnvCheck(context.Background())
	if err != nil {
		t.Fatalf("nil Service 的 EnvCheck 应返回 Problems 而不是错误：%v", err)
	}
	if len(status.Problems) == 0 {
		t.Error("nil Service 应报告问题")
	}
}
