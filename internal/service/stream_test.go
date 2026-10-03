package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// TestStartLogsEventsAndTask walks the full event sequence of one followed log
// and the final Task state.
func TestStartLogsEventsAndTask(t *testing.T) {
	svc, fake, emitter := newTestService(t)
	args := []string{"container", "logs", "--follow", "web"}
	fake.WhenStream(args, []string{"one", "two"}, wslc.Result{Stdout: "one\ntwo\n"}, nil)

	id, err := svc.StartLogs(context.Background(), "web", LogsOptions{Follow: true})
	assertNoFailure(t, err)
	if strings.TrimSpace(id) == "" {
		t.Fatal("StartLogs 必须返回非空 streamID")
	}

	task := waitForTask(t, svc, id)
	if task.ID != id {
		t.Errorf("Task.ID 必须与返回的 id 一致：%q vs %q", task.ID, id)
	}
	if task.Kind != TaskKindLogs {
		t.Errorf("Task.Kind 不符：%q", task.Kind)
	}
	if task.Ref != "web" {
		t.Errorf("Task.Ref 应为容器引用：%q", task.Ref)
	}
	if task.State != TaskSucceeded {
		t.Errorf("Task.State 不符：%q", task.State)
	}
	if task.Error != "" {
		t.Errorf("成功任务不应有 Error：%q", task.Error)
	}
	if task.Output != "one\ntwo\n" {
		t.Errorf("Task.Output 不符：%q", task.Output)
	}
	if task.StartedAt.IsZero() || task.EndedAt.IsZero() || task.EndedAt.Before(task.StartedAt) {
		t.Errorf("任务时间戳不符：start=%v end=%v", task.StartedAt, task.EndedAt)
	}

	events := emitter.snapshot()
	wantText := []string{"running", "$ wslc container logs --follow web", "one", "two", "succeeded"}
	if len(events) != len(wantText) {
		t.Fatalf("事件数量不符：实际 %d，期望 %d\n%+v", len(events), len(wantText), events)
	}
	for i, want := range wantText {
		if events[i].Text != want {
			t.Errorf("事件 %d 文本不符：实际 %q，期望 %q", i, events[i].Text, want)
		}
	}
	// The command note and the log lines belong to the log channel; the two
	// lifecycle notes belong to the task channel.
	if events[0].Channel != ChannelTask || events[4].Channel != ChannelTask {
		t.Errorf("任务生命周期事件应走 ChannelTask：%q", []string{events[0].Channel, events[4].Channel})
	}
	for _, i := range []int{1, 2, 3} {
		if events[i].Channel != ChannelLogs {
			t.Errorf("事件 %d 应走 ChannelLogs，实际 %q", i, events[i].Channel)
		}
		if events[i].Ref != "web" {
			t.Errorf("事件 %d 的 Ref 应原样回填 %q，实际 %q", i, "web", events[i].Ref)
		}
	}
	// Sequence numbers are per stream and start at 1.
	for i, event := range events {
		if event.Seq != i+1 {
			t.Errorf("Seq 应为每流从 1 递增：事件 %d 实际 %d", i, event.Seq)
		}
		if event.Time.IsZero() {
			t.Errorf("事件 %d 缺少时间戳", i)
		}
	}
}

// TestStartLogsEchoesRefVerbatim pins the lead's ruling: OutputEvent.Ref is the
// caller's string, untouched — no id resolution, no prefix, no case folding.
func TestStartLogsEchoesRefVerbatim(t *testing.T) {
	const raw = "MiXeD-01"
	svc, fake, emitter := newTestService(t)
	args := []string{"container", "logs", raw}
	fake.WhenStream(args, []string{"line"}, wslc.Result{Stdout: "line\n"}, nil)

	id, err := svc.StartLogs(context.Background(), raw, LogsOptions{})
	assertNoFailure(t, err)
	waitForTask(t, svc, id)

	sawLine := false
	for _, event := range emitter.forChannel(ChannelLogs) {
		if event.Ref != raw {
			t.Fatalf("Ref 必须原样回填 %q，实际 %q", raw, event.Ref)
		}
		if event.Text == "line" {
			sawLine = true
		}
	}
	if !sawLine {
		t.Fatal("未观察到日志行事件")
	}
}

// TestStartLogsFailureMarksTaskFailed covers error propagation for a streamed
// command that exits non-zero.
func TestStartLogsFailureMarksTaskFailed(t *testing.T) {
	svc, fake, emitter := newTestService(t)
	args := []string{"image", "pull", "bad:1"}
	fake.WhenStream(args, nil, wslc.Result{ExitCode: 1, Stderr: "pull access denied"}, exitError(args, 1, "pull access denied"))

	id, err := svc.PullImage(context.Background(), "bad:1")
	assertNoFailure(t, err)
	task := waitForTask(t, svc, id)

	if task.State != TaskFailed {
		t.Fatalf("失败流应标记 failed，实际 %q", task.State)
	}
	if !strings.Contains(task.Error, "pull access denied") {
		t.Errorf("Task.Error 应保留原因，实际 %q", task.Error)
	}
	if task.Kind != TaskKindPull {
		t.Errorf("Task.Kind 不符：%q", task.Kind)
	}
	if task.Ref != "bad:1" {
		t.Errorf("Task.Ref 应为镜像引用：%q", task.Ref)
	}
	// Task-channel streams echo the task id, because that is the only handle
	// the frontend holds for a pull.
	for _, event := range emitter.forChannel(ChannelPull) {
		if event.Ref != id {
			t.Errorf("pull 事件 Ref 应为 taskID %q，实际 %q", id, event.Ref)
		}
	}
	foundError := false
	for _, event := range emitter.snapshot() {
		if event.Stream == "system" && strings.Contains(event.Text, "错误：") {
			foundError = true
		}
	}
	if !foundError {
		t.Error("失败流应广播一条 system 错误事件")
	}
}

// TestBuildImageStreamsOnBuildChannel checks the build wiring end to end.
func TestBuildImageStreamsOnBuildChannel(t *testing.T) {
	svc, fake, emitter := newTestService(t)
	args := []string{"image", "build", "/src"}
	fake.WhenStream(args, []string{"step 1"}, wslc.Result{Stdout: "step 1\n"}, nil)

	id, err := svc.BuildImage(context.Background(), BuildOptions{Context: "/src"})
	assertNoFailure(t, err)
	task := waitForTask(t, svc, id)

	if task.Kind != TaskKindBuild || task.Ref != "/src" {
		t.Errorf("构建任务元数据不符：%+v", task)
	}
	if task.Output != "step 1\n" {
		t.Errorf("Task.Output 不符：%q", task.Output)
	}
	buildEvents := emitter.forChannel(ChannelBuild)
	if len(buildEvents) == 0 {
		t.Fatal("构建输出应走 ChannelBuild")
	}
	for _, event := range buildEvents {
		if event.Ref != id {
			t.Errorf("构建事件 Ref 应为 taskID %q，实际 %q", id, event.Ref)
		}
	}
}

// TestStreamEventsRegistersTask checks `wslc events` becomes a cancellable task.
func TestStreamEventsRegistersTask(t *testing.T) {
	svc, fake, _ := newTestService(t)
	id, err := svc.StreamEvents(context.Background())
	assertNoFailure(t, err)
	task := waitForTask(t, svc, id)
	if task.Kind != TaskKindEvents {
		t.Errorf("Task.Kind 不符：%q", task.Kind)
	}
	assertOnlyCall(t, fake.Calls(), []string{"events"})
}

// TestCancelStreamingTask uses a runner that blocks until cancelled, which is
// the only way to observe cancellation deterministically.
func TestCancelStreamingTask(t *testing.T) {
	runner := newBlockingRunner()
	emitter := &recordEmitter{}
	svc := NewService(runner, emitter)

	id, err := svc.StartLogs(context.Background(), "web", LogsOptions{Follow: true})
	assertNoFailure(t, err)
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("流未启动")
	}

	if err := svc.CancelTask(context.Background(), id); err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	task := waitForTask(t, svc, id)
	if task.State != TaskCanceled {
		t.Fatalf("取消后应为 canceled，实际 %q", task.State)
	}
	if !strings.Contains(task.Error, "canceled") {
		t.Errorf("取消任务应记录原因，实际 %q", task.Error)
	}
	if task.EndedAt.IsZero() {
		t.Error("取消任务应有 EndedAt")
	}

	// Cancelling an already finished task is a no-op; an unknown id is an error.
	if err := svc.CancelTask(context.Background(), id); err != nil {
		t.Errorf("重复取消应静默成功，实际：%v", err)
	}
	if err := svc.CancelTask(context.Background(), "task-does-not-exist"); err == nil {
		t.Error("未知任务应报错")
	}
	if err := svc.CancelTask(context.Background(), "  "); err == nil {
		t.Error("空任务 ID 应报错")
	}
}

// TestStopStreamIsIdempotent checks the drawer-close path.
func TestStopStreamIsIdempotent(t *testing.T) {
	runner := newBlockingRunner()
	svc := NewService(runner, &recordEmitter{})

	id, err := svc.StartLogs(context.Background(), "web", LogsOptions{Follow: true})
	assertNoFailure(t, err)
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("流未启动")
	}
	if err := svc.StopStream(context.Background(), id); err != nil {
		t.Fatalf("StopStream: %v", err)
	}
	if task := waitForTask(t, svc, id); task.State != TaskCanceled {
		t.Fatalf("停止后任务应为 canceled，实际 %q", task.State)
	}
	// A stream that already ended must not make the UI report a failure.
	if err := svc.StopStream(context.Background(), id); err != nil {
		t.Errorf("重复 StopStream 应返回 nil，实际：%v", err)
	}
	if err := svc.StopStream(context.Background(), "unknown-stream"); err != nil {
		t.Errorf("未知流应返回 nil（幂等），实际：%v", err)
	}
	if err := svc.StopStream(context.Background(), ""); err == nil {
		t.Error("空流 ID 应报错")
	}
}

// TestListTasksOrderAndIsolation checks newest-first ordering and that the
// snapshot cannot mutate service state.
func TestListTasksOrderAndIsolation(t *testing.T) {
	svc, fake, _ := newTestService(t)
	first, err := svc.PullImage(context.Background(), "first:1")
	assertNoFailure(t, err)
	waitForTask(t, svc, first)
	fake.WhenStream([]string{"image", "pull", "second:1"}, []string{"x"}, wslc.Result{Stdout: "x\n"}, nil)
	second, err := svc.PullImage(context.Background(), "second:1")
	assertNoFailure(t, err)
	waitForTask(t, svc, second)

	tasks, err := svc.ListTasks(context.Background())
	assertNoFailure(t, err)
	if len(tasks) != 2 {
		t.Fatalf("应有 2 个任务，实际 %d", len(tasks))
	}
	if tasks[0].ID != second || tasks[1].ID != first {
		t.Fatalf("任务应按最新在前排序，实际：%s, %s", tasks[0].ID, tasks[1].ID)
	}
	tasks[0].State = "tampered"
	again, err := svc.ListTasks(context.Background())
	assertNoFailure(t, err)
	if again[0].State == "tampered" {
		t.Fatal("ListTasks 必须返回快照，外部修改不得影响内部状态")
	}
}

func TestListTasksEmptyIsNonNil(t *testing.T) {
	svc, _, _ := newTestService(t)
	tasks, err := svc.ListTasks(context.Background())
	assertNoFailure(t, err)
	if tasks == nil {
		t.Fatal("ListTasks 应返回非 nil 空切片")
	}
	if len(tasks) != 0 {
		t.Fatalf("新服务不应有任务，实际 %d", len(tasks))
	}
}

// TestStreamsWithoutEmitter proves a nil emitter is tolerated (backend-not-ready
// mode) and only the silencing is lost.
func TestStreamsWithoutEmitter(t *testing.T) {
	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	svc := NewService(fake, nil)
	id, err := svc.StartLogs(context.Background(), "web", LogsOptions{})
	assertNoFailure(t, err)
	task := waitForTask(t, svc, id)
	if task.State != TaskSucceeded {
		t.Fatalf("无 emitter 时任务仍应结束，实际 %q", task.State)
	}
}

// TestStreamContextCancellationIsReportedAsCanceled keeps a cancelled parent
// context from looking like a crash.
func TestStreamContextCancellationIsReportedAsCanceled(t *testing.T) {
	runner := newBlockingRunner()
	svc := NewService(runner, &recordEmitter{})
	ctx, cancel := context.WithCancel(context.Background())

	id, err := svc.StartLogs(ctx, "web", LogsOptions{Follow: true})
	assertNoFailure(t, err)
	select {
	case <-runner.started:
	case <-time.After(3 * time.Second):
		t.Fatal("流未启动")
	}
	cancel()

	task := waitForTask(t, svc, id)
	if task.State != TaskCanceled {
		t.Fatalf("父 context 取消应标记 canceled，实际 %q", task.State)
	}
	if !strings.Contains(task.Error, "context canceled") {
		t.Errorf("应记录 context canceled，实际 %q", task.Error)
	}
}
