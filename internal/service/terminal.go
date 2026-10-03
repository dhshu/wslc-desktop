package service

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// terminalProcess is one live interactive process.
type terminalProcess interface {
	// Write sends data to the process stdin.
	Write(data string) error
	// Resize asks for a new terminal size (best effort).
	Resize(cols, rows int) error
	// Wait blocks until the process exits and returns its exit error.
	Wait() error
}

// terminalDriver starts interactive processes.
//
// This seam exists because the frozen Runner contract cannot express an
// interactive session: Spec.Stdin is a one-shot string, so it can feed a
// one-shot `exec` but not a terminal that stays open while the user types.
// Keeping the process handle behind an interface means tests script a terminal
// without spawning anything, and production code launches wslc directly.
type terminalDriver interface {
	Start(ctx context.Context, args []string, cols, rows int, sink func(wslc.Line)) (terminalProcess, error)
}

// execTerminalDriver is the production driver: it starts wslc with piped stdio
// and inherits the environment, like the adapter's ExecRunner does.
type execTerminalDriver struct {
	executable func() string
}

// Start launches wslc and returns a handle for stdin/resize/wait.
func (d execTerminalDriver) Start(ctx context.Context, args []string, cols, rows int, sink func(wslc.Line)) (terminalProcess, error) {
	exe := ""
	if d.executable != nil {
		exe = strings.TrimSpace(d.executable())
	}
	if exe == "" {
		return nil, errors.New("service: 找不到 wslc.exe，无法启动交互终端（可设置 WSLC_PATH）")
	}

	cmd := exec.CommandContext(normalizeContext(ctx), exe, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("service: 终端 stdin 不可用: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("service: 终端 stdout 不可用: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("service: 终端 stderr 不可用: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("service: 启动终端失败: %w", err)
	}

	proc := &execTerminalProcess{
		cmd:     cmd,
		stdin:   stdin,
		readers: make(chan struct{}),
		waited:  make(chan struct{}),
	}
	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); pumpTerminalLines(stdout, "stdout", sink) }()
	go func() { defer readers.Done(); pumpTerminalLines(stderr, "stderr", sink) }()
	go func() { readers.Wait(); close(proc.readers) }()

	// Cancellation must unblock the readers, not just kill the child: a child
	// that spawned a grandchild (or a proxy that outlives it) can keep the pipe
	// handles open, and a blocked reader would keep Wait from ever returning.
	// Closing the read ends makes in-flight reads fail immediately.
	go func() {
		select {
		case <-ctx.Done():
			_ = stdout.Close()
			_ = stderr.Close()
		case <-proc.readers:
		}
	}()
	return proc, nil
}

// execTerminalProcess is the handle returned by execTerminalDriver.
type execTerminalProcess struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	readers chan struct{}
	waited  chan struct{}
	once    sync.Once
	waitErr error
}

// Write forwards data to the child's stdin.
func (p *execTerminalProcess) Write(data string) error {
	if p == nil || p.stdin == nil {
		return errors.New("service: 终端已关闭")
	}
	_, err := io.WriteString(p.stdin, data)
	return err
}

// Resize is a deliberate no-op: the service layer does not allocate a pseudo
// console (that needs a platform PTY API the stdlib does not expose), so there
// is nothing to resize. wslc keeps whatever size its own -t console has, and
// returning an error here would only spam the UI on every debounced resize.
func (p *execTerminalProcess) Resize(cols, rows int) error { return nil }

// Wait blocks until both readers are drained and the process has exited.
//
// cmd.Wait closes the stdio pipes, so the readers must finish first; otherwise
// Wait could race with an in-flight read.
func (p *execTerminalProcess) Wait() error {
	if p == nil {
		return nil
	}
	p.once.Do(func() {
		<-p.readers
		p.waitErr = p.cmd.Wait()
		_ = p.stdin.Close()
		close(p.waited)
	})
	<-p.waited
	return p.waitErr
}

// pumpTerminalLines delivers lines to sink, always draining the reader so the
// child can never block on a full pipe.
func pumpTerminalLines(r io.Reader, stream string, sink func(wslc.Line)) {
	if sink == nil {
		_, _ = io.Copy(io.Discard, r)
		return
	}
	reader := bufio.NewReaderSize(r, 32<<10)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			sink(wslc.Line{Stream: stream, Text: strings.TrimRight(line, "\r\n"), Time: time.Now()})
		}
		if err != nil {
			return
		}
	}
}

// terminalTTYFallbackWindow bounds how long an interactive process may take to
// die for its exit to count as "the console was rejected immediately". A
// TTY-less console is refused before any prompt appears, so the window is
// generous compared with the real latency while still letting a healthy shell
// run indefinitely.
const terminalTTYFallbackWindow = 1500 * time.Millisecond

// ttyRejectionHints are the fragments a Docker-compatible CLI prints when -t is
// used without a real console. The adapter has no sentinel error for this case
// (it is a wslc CLI-level refusal, not an HCS failure), so recognizing it is
// inherently textual — and the check stays in one place, on purpose.
var ttyRejectionHints = []string{
	"not a tty",
	"input device",
	"不是 tty",
	"不是tty",
	"没有 tty",
	"无 tty",
}

// isTTYRejection reports whether a started console was refused because stdin is
// not a terminal.
func isTTYRejection(err error, output string) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(output + "\n" + err.Error())
	for _, hint := range ttyRejectionHints {
		if strings.Contains(text, hint) {
			return true
		}
	}
	return false
}

// withoutTTYFlag returns args with --tty/-t removed, reporting whether a TTY
// flag was present. The long form is what ExecOptions.Args emits; -t is
// accepted too so a manual caller cannot bypass the fallback.
func withoutTTYFlag(args []string) ([]string, bool) {
	for i, arg := range args {
		if arg != "--tty" && arg != "-t" {
			continue
		}
		out := make([]string, 0, len(args)-1)
		out = append(out, args[:i]...)
		out = append(out, args[i+1:]...)
		return out, true
	}
	return args, false
}

// terminalDiagnosis keeps the first stderr lines of one attempt so a refused
// TTY can be recognized from the process output as well as its exit error.
type terminalDiagnosis struct {
	mu   sync.Mutex
	text strings.Builder
}

func (d *terminalDiagnosis) add(line wslc.Line) {
	if d == nil || !strings.EqualFold(strings.TrimSpace(line.Stream), "stderr") {
		return
	}
	d.mu.Lock()
	if d.text.Len() < 4096 {
		d.text.WriteString(line.Text)
		d.text.WriteByte('\n')
	}
	d.mu.Unlock()
}

func (d *terminalDiagnosis) String() string {
	if d == nil {
		return ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.text.String()
}

// awaitTerminalProcess waits for a process outcome, but only up to window for
// the first result: an exit inside the window is what identifies an immediate
// rejection, while a process still alive afterwards is treated as healthy and
// waited on for real. A cancelled context always returns promptly so the task
// cannot stay "running" behind a process that will not come back.
func awaitTerminalProcess(ctx context.Context, proc terminalProcess, window time.Duration) error {
	exited := make(chan error, 1)
	go func() { exited <- proc.Wait() }()

	if window > 0 {
		timer := time.NewTimer(window)
		defer timer.Stop()
		select {
		case err := <-exited:
			return err
		case <-timer.C:
		case <-ctx.Done():
		}
	}

	select {
	case err := <-exited:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
