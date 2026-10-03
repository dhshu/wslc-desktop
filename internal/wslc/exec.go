package wslc

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// defaultTimeout bounds an invocation whose Spec.Timeout is zero.
	defaultTimeout = 60 * time.Second
	// defaultMaxOutput caps how much output is retained in Result.
	defaultMaxOutput = 4 << 20 // 4 MiB
	// probeTimeout bounds the cheap `wslc version` call used by Available.
	probeTimeout = 15 * time.Second
	// readChunk is the buffered reader size used while streaming.
	readChunk = 64 << 10
)

// ExecOption configures an ExecRunner.
type ExecOption func(*ExecRunner)

// WithTimeout sets the timeout applied when Spec.Timeout is zero.
func WithTimeout(d time.Duration) ExecOption {
	return func(r *ExecRunner) { r.timeout = d }
}

// WithMaxOutput caps how many bytes of stdout and stderr are retained.
func WithMaxOutput(n int) ExecOption {
	return func(r *ExecRunner) { r.maxOutput = n }
}

// WithEnv appends extra environment entries to every invocation.
func WithEnv(env ...string) ExecOption {
	return func(r *ExecRunner) { r.env = append(r.env, env...) }
}

// WithCommandFactory replaces how a Spec becomes an *exec.Cmd. Tests use it to
// assert process wiring without launching a process.
func WithCommandFactory(f CommandFactory) ExecOption {
	return func(r *ExecRunner) { r.factory = f }
}

// WithProbeArgs replaces the arguments Available uses to prove the executable
// runs (default: "version").
func WithProbeArgs(args ...string) ExecOption {
	return func(r *ExecRunner) { r.probeArgs = append([]string(nil), args...) }
}

// ExecRunner runs wslc by starting the executable directly.
//
// Arguments are always passed as a vector: no shell is involved, so values
// containing spaces, quotes or metacharacters cannot be re-interpreted.
type ExecRunner struct {
	exe       string
	timeout   time.Duration
	maxOutput int
	env       []string
	factory   CommandFactory
	probeArgs []string
}

// NewExecRunner returns a runner for the given wslc executable.
func NewExecRunner(exe string, opts ...ExecOption) *ExecRunner {
	r := &ExecRunner{
		exe:       exe,
		timeout:   defaultTimeout,
		maxOutput: defaultMaxOutput,
		probeArgs: []string{"version"},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(r)
		}
	}
	return r
}

// Exe returns the executable this runner invokes.
func (r *ExecRunner) Exe() string {
	if r == nil {
		return ""
	}
	return r.exe
}

// Run executes spec to completion and returns its captured output.
func (r *ExecRunner) Run(ctx context.Context, spec Spec) (Result, error) {
	if r == nil {
		return Result{}, errors.New("wslc: nil ExecRunner")
	}
	ctx, cancel := r.contextWithTimeout(ctx, spec)
	if cancel != nil {
		defer cancel()
	}

	start := time.Now()
	cmd := r.command(ctx, spec)
	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = r.maxOutput, r.maxOutput
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}

	waitErr := cmd.Run()
	res := Result{
		Args:      append([]string(nil), spec.Args...),
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Duration:  time.Since(start),
		Truncated: stdout.truncated || stderr.truncated,
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	return r.finish(ctx, spec, res, waitErr)
}

// Stream executes spec while delivering output line by line to sink.
func (r *ExecRunner) Stream(ctx context.Context, spec Spec, sink func(Line)) (Result, error) {
	if r == nil {
		return Result{}, errors.New("wslc: nil ExecRunner")
	}
	ctx, cancel := r.contextWithTimeout(ctx, spec)
	if cancel != nil {
		defer cancel()
	}

	cmd := r.command(ctx, spec)
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("wslc: capture stdout of %s: %w", r.exe, err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("wslc: capture stderr of %s: %w", r.exe, err)
	}
	if spec.Stdin != "" {
		cmd.Stdin = strings.NewReader(spec.Stdin)
	}

	start := time.Now()
	if err := cmd.Start(); err != nil {
		return Result{}, r.startError(spec, err)
	}

	var stdout, stderr limitedBuffer
	stdout.limit, stderr.limit = r.maxOutput, r.maxOutput
	var sinkMu sync.Mutex
	emit := func(stream, text string) {
		if sink == nil {
			return
		}
		sinkMu.Lock()
		defer sinkMu.Unlock()
		sink(Line{Stream: stream, Text: text, Time: time.Now()})
	}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		streamLines(stdoutPipe, &stdout, "stdout", emit)
	}()
	go func() {
		defer wg.Done()
		streamLines(stderrPipe, &stderr, "stderr", emit)
	}()
	wg.Wait()

	waitErr := cmd.Wait()
	res := Result{
		Args:      append([]string(nil), spec.Args...),
		Stdout:    stdout.String(),
		Stderr:    stderr.String(),
		Duration:  time.Since(start),
		Truncated: stdout.truncated || stderr.truncated,
	}
	if cmd.ProcessState != nil {
		res.ExitCode = cmd.ProcessState.ExitCode()
	}
	return r.finish(ctx, spec, res, waitErr)
}

// Available reports whether the configured executable can be started.
func (r *ExecRunner) Available(ctx context.Context) error {
	if r == nil || strings.TrimSpace(r.exe) == "" {
		return fmt.Errorf("%w: no wslc executable configured", ErrExecutableNotFound)
	}
	args := r.probeArgs
	if len(args) == 0 {
		args = []string{"version"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := r.Run(ctx, Spec{Kind: CmdVersion, Args: append([]string(nil), args...), Timeout: probeTimeout})
	return err
}

// command builds the *exec.Cmd for one invocation.
func (r *ExecRunner) command(ctx context.Context, spec Spec) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	factory := r.factory
	if factory == nil {
		factory = defaultCommandFactory
	}
	cmd := factory(ctx, r.exe, spec)
	if cmd == nil {
		cmd = defaultCommandFactory(ctx, r.exe, spec)
	}
	cmd.Dir = spec.Dir
	if env := append(append([]string(nil), r.env...), spec.Env...); len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	return cmd
}

// defaultCommandFactory starts the executable directly with an argument vector.
// On Windows, CREATE_NO_WINDOW is set so that wslc.exe (a console app) does not
// flash a console window on the desktop.
func defaultCommandFactory(ctx context.Context, exe string, spec Spec) *exec.Cmd {
	cmd := exec.CommandContext(ctx, exe, spec.Args...)
	// Hide the console window when running wslc.exe on Windows.
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	return cmd
}

// contextWithTimeout implements the Spec.Timeout contract: zero uses the
// runner default, a negative value means "no timeout" (used for streaming).
func (r *ExecRunner) contextWithTimeout(ctx context.Context, spec Spec) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := spec.Timeout
	if timeout == 0 {
		timeout = r.timeout
	}
	if timeout <= 0 {
		return ctx, nil
	}
	return context.WithTimeout(ctx, timeout)
}

// finish converts the process outcome into the documented Result/error pair.
func (r *ExecRunner) finish(ctx context.Context, spec Spec, res Result, waitErr error) (Result, error) {
	if waitErr == nil {
		return res, nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		// Killed by our timeout or by the caller: report the context error so
		// callers can use errors.Is(err, context.DeadlineExceeded).
		return res, fmt.Errorf("wslc %s: %w", strings.Join(spec.Args, " "), ctxErr)
	}
	var exitErr *exec.ExitError
	if errors.As(waitErr, &exitErr) {
		return res, &ExitError{Code: res.ExitCode, Args: append([]string(nil), spec.Args...), Stderr: res.Stderr, Stdout: res.Stdout}
	}
	return Result{}, r.startError(spec, waitErr)
}

// startError classifies a failure to launch the process.
func (r *ExecRunner) startError(spec Spec, err error) error {
	var execErr *exec.Error
	switch {
	case errors.Is(err, exec.ErrNotFound), errors.Is(err, fs.ErrNotExist), errors.As(err, &execErr):
		return fmt.Errorf("%w: cannot start %s: %v", ErrExecutableNotFound, r.exe, err)
	default:
		return fmt.Errorf("wslc: cannot start %s: %w", r.exe, err)
	}
}

// streamLines emits every line of r as it arrives while retaining the raw bytes
// (up to the configured cap) for the final Result.
func streamLines(r io.Reader, buf *limitedBuffer, stream string, emit func(stream, text string)) {
	br := bufio.NewReaderSize(r, readChunk)
	for {
		line, err := br.ReadString('\n')
		if line != "" {
			_, _ = buf.Write([]byte(line))
			emit(stream, strings.TrimRight(line, "\r\n"))
		}
		if err != nil {
			return
		}
	}
}

// limitedBuffer accumulates output up to limit bytes and records truncation.
type limitedBuffer struct {
	buf       bytes.Buffer
	limit     int
	truncated bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.limit <= 0 {
		return b.buf.Write(p)
	}
	remaining := b.limit - b.buf.Len()
	if remaining <= 0 {
		b.truncated = true
		return len(p), nil
	}
	if len(p) > remaining {
		_, _ = b.buf.Write(p[:remaining])
		b.truncated = true
		return len(p), nil
	}
	return b.buf.Write(p)
}

func (b *limitedBuffer) String() string { return b.buf.String() }
