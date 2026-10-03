package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// startStream registers the task/session pair every streaming method shares and
// announces the task on ChannelTask.
//
// subject is what the task is *about* (container/image reference) and is shown
// in Task.Ref. eventRef is echoed verbatim in OutputEvent.Ref; when it is empty
// the task id is used, which is what task-channel streams (pull, build, events)
// need because the id is the only handle the frontend holds.
func (s *Service) startStream(kind, channel, subject, eventRef string, cancel context.CancelFunc) (*streamSession, *Task) {
	task := s.createTaskRecord(kind, subject)
	if eventRef == "" {
		eventRef = task.ID
	}
	sess := &streamSession{
		id:      task.ID,
		taskID:  task.ID,
		kind:    kind,
		channel: channel,
		ref:     eventRef,
		cancel:  cancel,
	}
	s.registerStream(sess)
	s.emitStream(sess, OutputEvent{Channel: ChannelTask, Ref: task.ID, Stream: "system", Text: TaskRunning})
	return sess, task
}

// lineEmitter converts runner lines into events plus task output.
func (s *Service) lineEmitter(sess *streamSession) func(wslc.Line) {
	return func(line wslc.Line) {
		stream := strings.TrimSpace(line.Stream)
		if stream == "" {
			stream = "stdout"
		}
		s.emitStream(sess, OutputEvent{
			Channel: sess.channel,
			Ref:     sess.ref,
			Stream:  stream,
			Text:    line.Text,
			Time:    line.Time,
		})
		s.appendTaskOutput(sess.taskID, line.Text)
	}
}

// systemLine emits an out-of-band note (the command being run, or an error).
func (s *Service) systemLine(sess *streamSession, text string) {
	s.emitStream(sess, OutputEvent{Channel: sess.channel, Ref: sess.ref, Stream: "system", Text: text})
}

// pumpCommand drives one Runner.Stream invocation to completion.
func (s *Service) pumpCommand(ctx context.Context, sess *streamSession, spec wslc.Spec) {
	s.systemLine(sess, "$ "+formatCommand(spec.Args))
	res, err := s.stream(ctx, spec, s.lineEmitter(sess))
	if err != nil {
		s.systemLine(sess, "错误："+err.Error())
	}
	s.finishTask(sess, res, err, ctx.Err() != nil)
	s.unregisterStream(sess.id)
}

// StartLogs starts a log stream and returns its stream/task id.
//
// The stream is registered as a task, so CancelTask can stop it as well as
// StopStream. Follow uses an unlimited timeout: a followed log legitimately
// stays open until the user closes it.
func (s *Service) StartLogs(ctx context.Context, ref string, opts LogsOptions) (string, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return "", err
	}
	since, err := validateTimeValue("--since", opts.Since)
	if err != nil {
		return "", err
	}
	until, err := validateTimeValue("--until", opts.Until)
	if err != nil {
		return "", err
	}
	opts.Since, opts.Until = since, until
	if err := contextError(ctx); err != nil {
		return "", err
	}

	args := append([]string{"container", "logs"}, opts.Args()...)
	args = append(args, target)

	streamCtx, cancel := context.WithCancel(normalizeContext(ctx))
	sess, task := s.startStream(TaskKindLogs, ChannelLogs, target, target, cancel)
	spec := wslc.Spec{Kind: wslc.CmdContainerLogs, Args: args, Timeout: -1}
	go s.pumpCommand(streamCtx, sess, spec)
	return task.ID, nil
}

// StartTerminal opens an interactive shell inside a container.
//
// The command is always `sh`: the image's default shell is not discoverable
// through this API, and distroless images simply cannot offer a terminal.
func (s *Service) StartTerminal(ctx context.Context, ref string, opts ExecOptions, cols, rows int) (string, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return "", err
	}
	if opts.User != "" {
		if _, err := requireText("运行用户", strings.TrimSpace(opts.User)); err != nil {
			return "", err
		}
	}
	if opts.WorkDir != "" {
		if _, err := requireText("工作目录", strings.TrimSpace(opts.WorkDir)); err != nil {
			return "", err
		}
	}
	for _, entry := range opts.Env {
		if _, err := validateKeyValue("环境变量", entry); err != nil {
			return "", err
		}
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}

	args := append([]string{"container", "exec"}, opts.Args()...)
	args = append(args, target, "sh")

	streamCtx, cancel := context.WithCancel(normalizeContext(ctx))
	sess, task := s.startStream(TaskKindTerminal, ChannelTerminal, target, target, cancel)
	sess.cols, sess.rows = cols, rows
	go s.pumpTerminal(streamCtx, sess, args)
	return task.ID, nil
}

// pumpTerminal drives one interactive process to completion.
//
// When the caller asked for a TTY, the first attempt uses `--interactive --tty`.
// A wslc (like Docker) that insists on a real console for -t rejects the pipe
// immediately, so that case is retried once without -t, and the retry is
// announced on the stream so the user is not left guessing why the prompt
// behaves differently.
func (s *Service) pumpTerminal(ctx context.Context, sess *streamSession, args []string) {
	s.systemLine(sess, "$ "+formatCommand(args))
	driver := s.terminal
	if driver == nil {
		err := errors.New("service: 未配置终端驱动")
		s.systemLine(sess, "终端启动失败："+err.Error())
		s.finishTask(sess, wslc.Result{Args: args}, err, false)
		s.unregisterStream(sess.id)
		return
	}

	fallbackArgs, canFallback := withoutTTYFlag(args)

	proc, diag := s.startTerminalAttempt(ctx, driver, sess, args)
	if proc == nil {
		s.unregisterStream(sess.id)
		return
	}
	waitErr := awaitTerminalProcess(ctx, proc, terminalTTYFallbackWindow)

	if canFallback && isTTYRejection(waitErr, diag.String()) {
		s.systemLine(sess, "wslc 拒绝 --tty（stdin 不是终端），已自动去掉 -t 重试一次："+formatCommand(fallbackArgs))
		retryProc, _ := s.startTerminalAttempt(ctx, driver, sess, fallbackArgs)
		if retryProc == nil {
			s.unregisterStream(sess.id)
			return
		}
		waitErr = awaitTerminalProcess(ctx, retryProc, 0)
		s.finishTask(sess, wslc.Result{Args: fallbackArgs}, waitErr, ctx.Err() != nil)
		s.unregisterStream(sess.id)
		return
	}

	s.finishTask(sess, wslc.Result{Args: args}, waitErr, ctx.Err() != nil)
	s.unregisterStream(sess.id)
}

// startTerminalAttempt launches one interactive attempt and wires its output to
// the stream while collecting the stderr needed to recognize a TTY rejection.
//
// It returns nil after settling the task itself when the process cannot be
// started at all.
func (s *Service) startTerminalAttempt(ctx context.Context, driver terminalDriver, sess *streamSession, args []string) (terminalProcess, *terminalDiagnosis) {
	diag := &terminalDiagnosis{}
	emit := s.lineEmitter(sess)
	sink := func(line wslc.Line) {
		diag.add(line)
		emit(line)
	}

	proc, err := driver.Start(ctx, args, sess.cols, sess.rows, sink)
	if err != nil {
		s.systemLine(sess, "终端启动失败："+err.Error())
		// A failure to launch is never retried: the fallback exists only for a
		// console that started and then refused the TTY.
		s.finishTask(sess, wslc.Result{Args: args}, err, ctx.Err() != nil)
		return nil, diag
	}
	sess.setProcess(proc)
	return proc, diag
}

// TerminalWrite forwards keystrokes/commands to the interactive stdin.
func (s *Service) TerminalWrite(ctx context.Context, streamID, data string) error {
	if strings.TrimSpace(streamID) == "" {
		return errors.New("service: 流 ID 不能为空")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	sess := s.lookupStream(streamID)
	if sess == nil {
		return fmt.Errorf("service: 未知流 %q（会话可能已结束）", streamID)
	}
	proc := sess.process()
	if proc == nil {
		return fmt.Errorf("service: 流 %q 不是交互终端", streamID)
	}
	if err := proc.Write(data); err != nil {
		return fmt.Errorf("service: 写入终端失败: %w", err)
	}
	return nil
}

// TerminalResize reports a new terminal size. It is a no-op for a stream that
// has already ended, and a best-effort call otherwise (no PTY is allocated, see
// execTerminalProcess.Resize).
func (s *Service) TerminalResize(ctx context.Context, streamID string, cols, rows int) error {
	if strings.TrimSpace(streamID) == "" {
		return errors.New("service: 流 ID 不能为空")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	sess := s.lookupStream(streamID)
	if sess == nil {
		return nil
	}
	proc := sess.process()
	if proc == nil {
		return nil
	}
	if cols <= 0 {
		cols = 80
	}
	if rows <= 0 {
		rows = 24
	}
	return proc.Resize(cols, rows)
}

// StopStream cancels a live stream.
//
// Stopping an already finished stream returns nil: streams end on their own
// (a non-followed log, a shell exit), and the UI closes the drawer in both
// cases, so an error here would only produce noise.
func (s *Service) StopStream(ctx context.Context, streamID string) error {
	if strings.TrimSpace(streamID) == "" {
		return errors.New("service: 流 ID 不能为空")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	sess := s.lookupStream(streamID)
	if sess == nil {
		return nil
	}
	if sess.cancel != nil {
		sess.cancel()
	}
	return nil
}

// StreamEvents starts `wslc events` and returns its task id.
func (s *Service) StreamEvents(ctx context.Context) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	streamCtx, cancel := context.WithCancel(normalizeContext(ctx))
	sess, task := s.startStream(TaskKindEvents, ChannelEvents, "events", "", cancel)
	// Timeout: -1ns is the canonical "unlimited" for streaming commands (see
	// the runner's Spec.Timeout comment: 0 falls back to the 60s default).
	spec := wslc.Spec{Kind: wslc.CmdEvents, Args: []string{"events"}, Timeout: -1 * time.Nanosecond}
	go s.pumpCommand(streamCtx, sess, spec)
	return task.ID, nil
}
