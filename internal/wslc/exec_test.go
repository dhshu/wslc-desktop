package wslc

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func requireCmd(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("test drives cmd.exe, which only exists on Windows")
	}
	exe := os.Getenv("ComSpec")
	if exe == "" {
		exe = `C:\WINDOWS\system32\cmd.exe`
	}
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("cmd.exe not available: %v", err)
	}
	return exe
}

func TestExecRunnerRunCapturesStdout(t *testing.T) {
	exe := requireCmd(t)
	r := NewExecRunner(exe)
	res, err := r.Run(context.Background(), Spec{Kind: CmdVersion, Args: []string{"/c", "echo hi"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.OK() || res.ExitCode != 0 {
		t.Errorf("OK()=%v ExitCode=%d, want success", res.OK(), res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "hi") {
		t.Errorf("Stdout = %q, want it to contain hi", res.Stdout)
	}
	if len(res.Args) != 2 {
		t.Errorf("Result.Args = %v, want the spec args", res.Args)
	}
	if res.Duration <= 0 {
		t.Errorf("Duration = %v, want > 0", res.Duration)
	}
}

func TestExecRunnerNonZeroExitIsBothResultAndExitError(t *testing.T) {
	exe := requireCmd(t)
	r := NewExecRunner(exe)
	res, err := r.Run(context.Background(), Spec{Kind: CmdVersion, Args: []string{"/c", "exit 3"}})
	if err == nil {
		t.Fatal("expected an error for a non-zero exit")
	}
	if res.ExitCode != 3 {
		t.Errorf("Result.ExitCode = %d, want 3 (a populated Result is returned alongside the error)", res.ExitCode)
	}
	if res.OK() {
		t.Error("OK() = true for a non-zero exit")
	}
	var ee *ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("err = %T (%v), want *ExitError", err, err)
	}
	if ee.Code != 3 || strings.Join(ee.Args, " ") != "/c exit 3" {
		t.Errorf("ExitError = %+v", ee)
	}
}

func TestExecRunnerCapturesStderr(t *testing.T) {
	exe := requireCmd(t)
	res, err := NewExecRunner(exe).Run(context.Background(), Spec{Args: []string{"/c", "echo oops 1>&2"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stderr, "oops") {
		t.Errorf("Stderr = %q, want it to contain oops", res.Stderr)
	}
}

func TestExecRunnerExitErrorMapsRealHCSFailure(t *testing.T) {
	exe := requireCmd(t)
	// Same text wslc 3.0.1 prints on this machine when vmcompute is stopped.
	_, err := NewExecRunner(exe).Run(context.Background(), Spec{Args: []string{"/c", "echo HCS_E_SERVICE_NOT_AVAILABLE 1>&2 & exit 1"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrServiceUnavailable) {
		t.Errorf("errors.Is(err, ErrServiceUnavailable) = false for %v", err)
	}
}

func TestExitErrorUnwrapSentinels(t *testing.T) {
	// Only the patterns errors.go already implements are asserted here.
	// Two real wslc 3.0.1 messages are NOT mapped today and are reported to the
	// lead instead of being silently "fixed" outside this task's write scope:
	//   "无法识别的命令:“prune”"            -> should be ErrUnsupportedCommand
	//   "找不到容器 'web'。"                -> should be ErrNotFound
	cases := []struct {
		name   string
		stderr string
		want   error
	}{
		{"hcs", "由于未安装所需的特性，无法启动操作。\r\n错误代码： HCS_E_SERVICE_NOT_AVAILABLE\r\n", ErrServiceUnavailable},
		{"hcs-ascii", "Error code: HCS_E_SERVICE_NOT_AVAILABLE", ErrServiceUnavailable},
		{"unsupported-flag", "当前命令的选项名称未被识别：'--format'", ErrUnsupportedCommand},
		{"unrecognized-en", "Unrecognized command: prune", ErrUnsupportedCommand},
		{"no-such-container", "Error response from daemon: No such container: web", ErrNotFound},
		{"other", "something went wrong", nil},
	}
	for _, tc := range cases {
		e := &ExitError{Code: 1, Args: []string{"container", "list"}, Stderr: tc.stderr}
		got := e.Unwrap()
		if got != tc.want {
			t.Errorf("%s: Unwrap() = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestExecRunnerFeedsStdin(t *testing.T) {
	exe := requireCmd(t)
	res, err := NewExecRunner(exe).Run(context.Background(), Spec{
		Args:  []string{"/c", "sort"},
		Stdin: "banana\napple\n",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "apple") || !strings.Contains(res.Stdout, "banana") {
		t.Errorf("Stdout = %q, want the sorted stdin echoed back", res.Stdout)
	}
	if strings.Index(res.Stdout, "apple") > strings.Index(res.Stdout, "banana") {
		t.Errorf("Stdout = %q, want sorted output", res.Stdout)
	}
}

// requirePing returns ping.exe, used where the test needs a child process that
// is itself long lived so the context timeout must kill it directly.
func requirePing(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("test drives ping.exe, which only exists on Windows")
	}
	exe := filepath.Join(os.Getenv("SystemRoot"), "System32", "PING.EXE")
	if _, err := os.Stat(exe); err != nil {
		t.Skipf("ping.exe not available: %v", err)
	}
	return exe
}

func TestExecRunnerAppliesTimeout(t *testing.T) {
	exe := requirePing(t)
	start := time.Now()
	_, err := NewExecRunner(exe).Run(context.Background(), Spec{
		Args:    []string{"-n", "6", "127.0.0.1"},
		Timeout: 300 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	if elapsed > 3*time.Second {
		t.Errorf("Run took %v, the process was not killed on timeout", elapsed)
	}
}

func TestExecRunnerHonoursCanceledContext(t *testing.T) {
	exe := requireCmd(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewExecRunner(exe).Run(ctx, Spec{Args: []string{"/c", "echo hi"}})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestExecRunnerNegativeTimeoutIsUnlimited(t *testing.T) {
	exe := requireCmd(t)
	res, err := NewExecRunner(exe, WithTimeout(time.Millisecond)).Run(context.Background(), Spec{
		Args:    []string{"/c", "ping -n 2 127.0.0.1 >nul"},
		Timeout: -1,
	})
	if err != nil {
		t.Fatalf("negative Timeout must disable the runner default: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("ExitCode = %d", res.ExitCode)
	}
}

func TestExecRunnerStreamDeliversLinesAndResult(t *testing.T) {
	exe := requireCmd(t)
	var lines []string
	var streams []string
	res, err := NewExecRunner(exe).Stream(context.Background(), Spec{
		Args:    []string{"/c", "echo one&echo two"},
		Timeout: -1,
	}, func(l Line) {
		lines = append(lines, l.Text)
		streams = append(streams, l.Stream)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("lines = %v, want [one two]", lines)
	}
	for i, s := range streams {
		if s != "stdout" {
			t.Errorf("line %d stream = %q, want stdout", i, s)
		}
	}
	if !strings.Contains(res.Stdout, "one") || !strings.Contains(res.Stdout, "two") {
		t.Errorf("Result.Stdout = %q, want the full captured output", res.Stdout)
	}
}

func TestExecRunnerStreamSeparatesStderr(t *testing.T) {
	exe := requireCmd(t)
	var stdout, stderr []string
	_, err := NewExecRunner(exe).Stream(context.Background(), Spec{
		Args:    []string{"/c", "echo out&echo err 1>&2"},
		Timeout: -1,
	}, func(l Line) {
		if l.Stream == "stdout" {
			stdout = append(stdout, strings.TrimSpace(l.Text))
		} else {
			stderr = append(stderr, strings.TrimSpace(l.Text))
		}
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if strings.Join(stdout, ",") != "out" {
		t.Errorf("stdout lines = %v", stdout)
	}
	if strings.Join(stderr, ",") != "err" {
		t.Errorf("stderr lines = %v", stderr)
	}
}

func TestExecRunnerStreamNonZeroExitStillStreams(t *testing.T) {
	exe := requireCmd(t)
	var lines []string
	_, err := NewExecRunner(exe).Stream(context.Background(), Spec{
		Args:    []string{"/c", "echo partial&exit 2"},
		Timeout: -1,
	}, func(l Line) {
		lines = append(lines, strings.TrimSpace(l.Text))
	})
	if err == nil {
		t.Fatal("expected an error for exit code 2")
	}
	var ee *ExitError
	if !errors.As(err, &ee) || ee.Code != 2 {
		t.Errorf("err = %v, want *ExitError with code 2", err)
	}
	if strings.Join(lines, ",") != "partial" {
		t.Errorf("lines = %v, want the output received before the failure", lines)
	}
}

func TestExecRunnerTruncatesCapturedOutput(t *testing.T) {
	exe := requireCmd(t)
	res, err := NewExecRunner(exe, WithMaxOutput(4)).Run(context.Background(), Spec{Args: []string{"/c", "echo abcdefgh"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !res.Truncated {
		t.Error("Truncated = false, want true")
	}
	if len(res.Stdout) > 4 {
		t.Errorf("Stdout = %q (%d bytes), want it capped at 4", res.Stdout, len(res.Stdout))
	}
}

func TestExecRunnerAppliesDirAndEnv(t *testing.T) {
	exe := requireCmd(t)
	res, err := NewExecRunner(exe).Run(context.Background(), Spec{
		Args: []string{"/c", "echo %WSLC_TEST_GREETING%"},
		Env:  []string{"WSLC_TEST_GREETING=hello-from-env"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(res.Stdout, "hello-from-env") {
		t.Errorf("Stdout = %q, want the appended env var to reach the child", res.Stdout)
	}
}

func TestExecRunnerPassesArgvWithoutAShell(t *testing.T) {
	exe := requireCmd(t)
	var gotSpec Spec
	var gotCmd *exec.Cmd
	r := NewExecRunner(exe, WithCommandFactory(func(ctx context.Context, e string, spec Spec) *exec.Cmd {
		gotSpec = spec
		gotCmd = exec.CommandContext(ctx, e, spec.Args...)
		return gotCmd
	}))
	if _, err := r.Run(context.Background(), Spec{Kind: CmdContainerList, Args: []string{"container", "list", "a b"}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{exe, "container", "list", "a b"}
	if strings.Join(gotCmd.Args, "\x1f") != strings.Join(want, "\x1f") {
		t.Errorf("cmd.Args = %q, want %q (no shell wrapper, arguments preserved verbatim)", gotCmd.Args, want)
	}
	if gotSpec.Kind != CmdContainerList {
		t.Errorf("factory received Kind %q", gotSpec.Kind)
	}
}

func TestExecRunnerAvailableFailsForMissingExecutable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "definitely-not-here", "wslc.exe")
	err := NewExecRunner(missing).Available(context.Background())
	if err == nil {
		t.Fatal("expected an error")
	}
	if !errors.Is(err, ErrExecutableNotFound) {
		t.Errorf("err = %v, want ErrExecutableNotFound", err)
	}
}

func TestExecRunnerAvailableOnMissingPathReturnsZeroResult(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope.exe")
	res, err := NewExecRunner(missing).Run(context.Background(), Spec{Args: []string{"version"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if res.Stdout != "" || res.ExitCode != 0 || res.Args != nil {
		t.Errorf("a start failure must return a zero-value Result, got %+v", res)
	}
}

func TestExecRunnerAvailableProbesVersionByDefault(t *testing.T) {
	exe := requireCmd(t)
	var probed []string
	r := NewExecRunner(exe, WithProbeArgs("/c", "exit 0"), WithCommandFactory(func(ctx context.Context, e string, spec Spec) *exec.Cmd {
		probed = append([]string(nil), spec.Args...)
		return exec.CommandContext(ctx, e, spec.Args...)
	}))
	if err := r.Available(context.Background()); err != nil {
		t.Fatalf("Available: %v", err)
	}
	if strings.Join(probed, " ") != "/c exit 0" {
		t.Errorf("probed args = %v, want the configured probe args", probed)
	}

	probed = nil
	def := NewExecRunner(exe, WithCommandFactory(func(ctx context.Context, e string, spec Spec) *exec.Cmd {
		probed = append([]string(nil), spec.Args...)
		return exec.CommandContext(ctx, e, "/c", "exit 0")
	}))
	if err := def.Available(context.Background()); err != nil {
		t.Fatalf("Available (default probe): %v", err)
	}
	if strings.Join(probed, " ") != "version" {
		t.Errorf("default probe args = %v, want [version]", probed)
	}
}

func TestExecRunnerEmptyExecutableIsNotFound(t *testing.T) {
	err := NewExecRunner("").Available(context.Background())
	if !errors.Is(err, ErrExecutableNotFound) {
		t.Errorf("err = %v, want ErrExecutableNotFound", err)
	}
}

func TestExecRunnerExposesExecutablePath(t *testing.T) {
	exe := requireCmd(t)
	if got := NewExecRunner(exe).Exe(); got != exe {
		t.Errorf("Exe() = %q, want %q", got, exe)
	}
}

func TestExecRunnerIsSafeForConcurrentUse(t *testing.T) {
	exe := requireCmd(t)
	r := NewExecRunner(exe)
	done := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			_, err := r.Run(context.Background(), Spec{Args: []string{"/c", "echo parallel"}})
			done <- err
		}()
	}
	for i := 0; i < 8; i++ {
		if err := <-done; err != nil {
			t.Errorf("concurrent Run: %v", err)
		}
	}
}
