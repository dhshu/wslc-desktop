package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// TestStartTerminalWiresDriver checks the interactive seam: argv, requested
// size, the task id contract, stdin writes and resize.
func TestStartTerminalWiresDriver(t *testing.T) {
	svc, _, emitter := newTestService(t)
	driver := &fakeTerminalDriver{lines: []wslc.Line{
		{Stream: "stdout", Text: "$ echo hi"},
		{Stream: "stdout", Text: "hi"},
	}}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web",
		ExecOptions{TTY: true, User: "1000", WorkDir: "/app", Env: []string{"A=1"}}, 120, 40)
	assertNoFailure(t, err)
	if strings.TrimSpace(id) == "" {
		t.Fatal("StartTerminal 必须返回非空 id")
	}

	proc := driver.awaitProcess(t)
	assertArgsEqual(t, driver.lastSpec(),
		[]string{"container", "exec", "--interactive", "--tty", "--user", "1000", "--workdir", "/app", "--env", "A=1", "web", "sh"})
	if size := driver.lastSize(); size != [2]int{120, 40} {
		t.Fatalf("终端尺寸应为 120x40，实际 %v", size)
	}

	// TerminalWrite must reach the process stdin with the id StartTerminal
	// returned — that is the three-way ID contract.
	if err := svc.TerminalWrite(context.Background(), id, "echo hi\n"); err != nil {
		t.Fatalf("TerminalWrite: %v", err)
	}
	if writes := proc.written(); len(writes) != 1 || writes[0] != "echo hi\n" {
		t.Fatalf("stdin 写入不符：%q", writes)
	}
	if err := svc.TerminalResize(context.Background(), id, 100, 30); err != nil {
		t.Fatalf("TerminalResize: %v", err)
	}
	if resizes := proc.resized(); len(resizes) != 1 || resizes[0] != [2]int{100, 30} {
		t.Fatalf("resize 不符：%v", resizes)
	}

	// Let the shell exit; the task must settle as succeeded.
	proc.finish(nil)
	task := waitForTask(t, svc, id)
	if task.State != TaskSucceeded {
		t.Fatalf("终端退出后应为 succeeded，实际 %q（%s）", task.State, task.Error)
	}
	if task.Kind != TaskKindTerminal || task.Ref != "web" {
		t.Fatalf("终端任务元数据不符：%+v", task)
	}
	if task.Output != "$ echo hi\nhi\n" {
		t.Fatalf("终端输出应累积到 Task.Output，实际 %q", task.Output)
	}

	events := emitter.snapshot()
	if len(events) < 4 {
		t.Fatalf("事件过少：%+v", events)
	}
	if events[0].Channel != ChannelTask || events[0].Text != TaskRunning {
		t.Errorf("首个事件应为任务 running：%+v", events[0])
	}
	if events[len(events)-1].Channel != ChannelTask || events[len(events)-1].Text != TaskSucceeded {
		t.Errorf("末个事件应为任务 succeeded：%+v", events[len(events)-1])
	}
	for _, event := range emitter.forChannel(ChannelTerminal) {
		if event.Ref != "web" {
			t.Errorf("终端事件 Ref 应原样回填 %q，实际 %q", "web", event.Ref)
		}
		if event.Seq == 0 {
			t.Error("终端事件必须有 Seq")
		}
	}
}

// TestStartTerminalDefaultSize covers the zero-value size the frontend may send.
func TestStartTerminalDefaultSize(t *testing.T) {
	svc, _, _ := newTestService(t)
	driver := &fakeTerminalDriver{}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web", ExecOptions{}, 0, 0)
	assertNoFailure(t, err)
	proc := driver.awaitProcess(t)
	if size := driver.lastSize(); size != [2]int{80, 24} {
		t.Fatalf("零尺寸应回落到 80x24，实际 %v", size)
	}
	proc.finish(nil)
	waitForTask(t, svc, id)
}

// TestTerminalStartFailureSettlesTask keeps a failed launch from leaving a
// "running" task behind forever.
func TestTerminalStartFailureSettlesTask(t *testing.T) {
	svc, _, emitter := newTestService(t)
	driver := &fakeTerminalDriver{startErr: errors.New("the input device is not a TTY")}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web", ExecOptions{TTY: true}, 80, 24)
	assertNoFailure(t, err)

	task := waitForTask(t, svc, id)
	if task.State != TaskFailed {
		t.Fatalf("启动失败应标记 failed，实际 %q", task.State)
	}
	if !strings.Contains(task.Error, "not a TTY") {
		t.Errorf("Task.Error 应保留原因，实际 %q", task.Error)
	}
	found := false
	for _, event := range emitter.snapshot() {
		if event.Stream == "system" && strings.Contains(event.Text, "终端启动失败") {
			found = true
		}
	}
	if !found {
		t.Error("启动失败应广播 system 事件")
	}
}

// TestStartTerminalFallsBackWithoutTTY covers the lead's ruling: when wslc
// refuses `-t` because stdin is a pipe, the service retries once without it,
// announces the retry, and the retry's outcome is what the task reports.
func TestStartTerminalFallsBackWithoutTTY(t *testing.T) {
	svc, _, emitter := newTestService(t)
	driver := &fakeTerminalDriver{plans: []fakeTerminalPlan{
		{
			// First attempt: stdout is a pipe, so wslc rejects the console.
			lines:   []wslc.Line{{Stream: "stderr", Text: "the input device is not a TTY"}},
			waitErr: exitError([]string{"container", "exec"}, 1, "the input device is not a TTY"),
		},
		{
			// Retry without -t: the shell starts and stays open.
			lines: []wslc.Line{{Stream: "stdout", Text: "shell ready"}},
		},
	}}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web", ExecOptions{TTY: true}, 80, 24)
	assertNoFailure(t, err)

	proc := driver.awaitAttempt(t, 2)
	proc.finish(nil)

	task := waitForTask(t, svc, id)
	if task.State != TaskSucceeded {
		t.Fatalf("回退后应成功，实际 %q（%s）", task.State, task.Error)
	}
	if task.Output == "" || !strings.Contains(task.Output, "shell ready") {
		t.Errorf("回退后的输出应记录到任务，实际 %q", task.Output)
	}

	attempts := driver.attempts()
	if len(attempts) != 2 {
		t.Fatalf("应恰好尝试两次，实际 %d 次：%q", len(attempts), attempts)
	}
	assertArgsEqual(t, attempts[0],
		[]string{"container", "exec", "--interactive", "--tty", "web", "sh"})
	assertArgsEqual(t, attempts[1],
		[]string{"container", "exec", "--interactive", "web", "sh"})

	// Exactly one explanatory system event, and it must not be a duplicate of
	// the ordinary command echo.
	notes := 0
	for _, event := range emitter.snapshot() {
		if event.Channel == ChannelTerminal && event.Stream == "system" && strings.Contains(event.Text, "重试一次") {
			notes++
		}
	}
	if notes != 1 {
		t.Fatalf("应恰好产生 1 条回退说明事件，实际 %d：%+v", notes, emitter.snapshot())
	}
}

// TestStartTerminalDoesNotRetryOtherFailures keeps the fallback narrow: a
// first attempt that fails for any other reason must not be repeated.
func TestStartTerminalDoesNotRetryOtherFailures(t *testing.T) {
	svc, _, _ := newTestService(t)
	driver := &fakeTerminalDriver{plans: []fakeTerminalPlan{
		{
			lines:   []wslc.Line{{Stream: "stderr", Text: "permission denied"}},
			waitErr: exitError([]string{"container", "exec"}, 1, "permission denied"),
		},
	}}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web", ExecOptions{TTY: true}, 80, 24)
	assertNoFailure(t, err)

	task := waitForTask(t, svc, id)
	if task.State != TaskFailed {
		t.Fatalf("非 TTY 失败不应重试，任务应为 failed，实际 %q", task.State)
	}
	if attempts := driver.attempts(); len(attempts) != 1 {
		t.Fatalf("应只尝试一次，实际 %d 次：%q", len(attempts), attempts)
	}
}

// TestStartTerminalRetryAlsoFails marks the task failed when the TTY-less
// retry fails too.
func TestStartTerminalRetryAlsoFails(t *testing.T) {
	svc, _, _ := newTestService(t)
	driver := &fakeTerminalDriver{plans: []fakeTerminalPlan{
		{
			lines:   []wslc.Line{{Stream: "stderr", Text: "the input device is not a TTY"}},
			waitErr: exitError([]string{"container", "exec"}, 1, "the input device is not a TTY"),
		},
		{
			lines:   []wslc.Line{{Stream: "stderr", Text: "container is not running"}},
			waitErr: exitError([]string{"container", "exec"}, 1, "container is not running"),
		},
	}}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web", ExecOptions{TTY: true}, 80, 24)
	assertNoFailure(t, err)

	task := waitForTask(t, svc, id)
	if task.State != TaskFailed {
		t.Fatalf("两次都失败时任务应为 failed，实际 %q", task.State)
	}
	if !strings.Contains(task.Error, "container is not running") {
		t.Errorf("Task.Error 应是最后一次失败原因，实际 %q", task.Error)
	}
	if attempts := driver.attempts(); len(attempts) != 2 {
		t.Fatalf("应恰好尝试两次，实际 %d 次", len(attempts))
	}
}

// TestStartTerminalWithoutTTYDoesNotRetry proves the fallback is only wired to
// a requested TTY.
func TestStartTerminalWithoutTTYDoesNotRetry(t *testing.T) {
	svc, _, _ := newTestService(t)
	driver := &fakeTerminalDriver{plans: []fakeTerminalPlan{
		{
			lines:   []wslc.Line{{Stream: "stderr", Text: "the input device is not a TTY"}},
			waitErr: exitError([]string{"container", "exec"}, 1, "the input device is not a TTY"),
		},
	}}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web", ExecOptions{TTY: false}, 80, 24)
	assertNoFailure(t, err)
	if task := waitForTask(t, svc, id); task.State != TaskFailed {
		t.Fatalf("未请求 TTY 时不应回退重试，实际 %q", task.State)
	}
	if attempts := driver.attempts(); len(attempts) != 1 {
		t.Fatalf("应只尝试一次，实际 %d 次", len(attempts))
	}
}

// TestTTYRejectionDetection pins the recognized wording, including the Chinese
// form wslc may print on a zh-CN machine.
func TestTTYRejectionDetection(t *testing.T) {
	cases := []struct {
		output string
		err    error
		want   bool
	}{
		{"the input device is not a TTY", exitError(nil, 1, "exit status 1"), true},
		{"不是 TTY", exitError(nil, 1, "exit status 1"), true},
		{"", exitError(nil, 1, "the input device is not a TTY"), true},
		{"", exitError(nil, 1, "不是 TTY"), true},
		{"permission denied", exitError(nil, 1, "exit status 1"), false},
		{"", nil, false},
	}
	for i, tc := range cases {
		if got := isTTYRejection(tc.err, tc.output); got != tc.want {
			t.Errorf("case %d: isTTYRejection(%q, %v) = %v, want %v", i, tc.output, tc.err, got, tc.want)
		}
	}
}

// TestWithoutTTYFlagRemovesOnlyTTY checks the flag surgery.
func TestWithoutTTYFlagRemovesOnlyTTY(t *testing.T) {
	got, ok := withoutTTYFlag([]string{"container", "exec", "--interactive", "--tty", "web", "sh"})
	if !ok {
		t.Fatal("应识别出 --tty")
	}
	assertArgsEqual(t, got, []string{"container", "exec", "--interactive", "web", "sh"})

	got, ok = withoutTTYFlag([]string{"container", "exec", "--interactive", "web", "sh"})
	if ok {
		t.Fatal("没有 TTY flag 时不应报告删除")
	}
	assertArgsEqual(t, got, []string{"container", "exec", "--interactive", "web", "sh"})

	if _, ok := withoutTTYFlag([]string{"container", "exec", "-t", "web"}); !ok {
		t.Error("短形式 -t 也应被识别")
	}
}

// TestTerminalNonZeroExitMarksFailed records that a shell exiting non-zero is
// not silently reported as success.
func TestTerminalNonZeroExitMarksFailed(t *testing.T) {
	svc, _, _ := newTestService(t)
	driver := &fakeTerminalDriver{}
	svc.terminal = driver

	id, err := svc.StartTerminal(context.Background(), "web", ExecOptions{}, 80, 24)
	assertNoFailure(t, err)
	proc := driver.awaitProcess(t)
	proc.finish(exitError([]string{"container", "exec"}, 1, "exit status 1"))

	task := waitForTask(t, svc, id)
	if task.State != TaskFailed {
		t.Fatalf("非零退出应标记 failed，实际 %q", task.State)
	}
}

// TestTerminalWriteValidation covers the error paths the UI can hit.
func TestTerminalWriteValidation(t *testing.T) {
	svc, _, _ := newTestService(t)
	if err := svc.TerminalWrite(context.Background(), "", "x"); err == nil {
		t.Error("空流 ID 应报错")
	}
	if err := svc.TerminalWrite(context.Background(), "unknown", "x"); err == nil {
		t.Error("未知流应报错")
	}
	// A log stream is not a terminal: writing to it must fail instead of being
	// silently dropped.
	logID, err := svc.StartLogs(context.Background(), "web", LogsOptions{})
	assertNoFailure(t, err)
	waitForTask(t, svc, logID)
	if err := svc.TerminalWrite(context.Background(), logID, "x"); err == nil {
		t.Error("向非终端流写入应报错")
	}
	if err := svc.TerminalResize(context.Background(), "unknown", 10, 10); err != nil {
		t.Errorf("对未知流的 resize 应静默成功，实际：%v", err)
	}
}
