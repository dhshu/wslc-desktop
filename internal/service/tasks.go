package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// createTaskRecord registers a new running task. The caller keeps ownership of
// the returned pointer but must mutate it through the helpers below, which hold
// the service lock.
func (s *Service) createTaskRecord(kind, subject string) *Task {
	task := &Task{
		ID:        s.id("task"),
		Kind:      kind,
		Ref:       subject,
		State:     TaskRunning,
		StartedAt: s.clock(),
	}
	s.mu.Lock()
	if s.tasks == nil {
		s.tasks = make(map[string]*Task)
	}
	if s.order == nil {
		s.order = []string{}
	}
	s.tasks[task.ID] = task
	s.order = append(s.order, task.ID)
	s.mu.Unlock()
	return task
}

// appendTaskOutput accumulates streamed text on a task so the tasks view can
// show output even when the frontend missed the live events. Growth is capped.
func (s *Service) appendTaskOutput(taskID, text string) {
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[taskID]
	if task == nil || len(task.Output) >= maxTaskOutput {
		return
	}
	task.Output += text + "\n"
}

// finishTask moves a task to its terminal state and announces it on
// ChannelTask. Output that arrived through the event sink wins over Result so a
// fake runner that only scripts lines still produces a complete log.
func (s *Service) finishTask(sess *streamSession, res wslc.Result, err error, canceled bool) {
	if sess == nil {
		return
	}
	s.mu.Lock()
	task := s.tasks[sess.taskID]
	if task == nil {
		s.mu.Unlock()
		return
	}
	switch {
	case canceled || errors.Is(err, context.Canceled):
		task.State = TaskCanceled
	case err != nil:
		task.State = TaskFailed
	default:
		task.State = TaskSucceeded
	}
	task.EndedAt = s.clock()
	if err != nil {
		task.Error = err.Error()
	}
	if task.Output == "" {
		if out := outputOr(res, ""); out != "" {
			task.Output = out
		}
	}
	state := task.State
	s.mu.Unlock()

	s.emitStream(sess, OutputEvent{Channel: ChannelTask, Ref: sess.taskID, Stream: "system", Text: state})
}

// ListTasks returns a snapshot of every task, newest first.
func (s *Service) ListTasks(ctx context.Context) ([]Task, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Task, 0, len(s.order))
	for i := len(s.order) - 1; i >= 0; i-- {
		if task := s.tasks[s.order[i]]; task != nil {
			out = append(out, *task)
		}
	}
	return out, nil
}

// CancelTask cancels a running task. Cancelling an already finished task is a
// no-op so the UI never reports a spurious failure; an unknown id is an error.
func (s *Service) CancelTask(ctx context.Context, id string) error {
	if strings.TrimSpace(id) == "" {
		return errors.New("service: 任务 ID 不能为空")
	}
	if err := contextError(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	task := s.tasks[id]
	sess := s.streams[id]
	if task == nil {
		s.mu.Unlock()
		return fmt.Errorf("service: 未知任务 %q", id)
	}
	running := task.State == TaskRunning
	s.mu.Unlock()

	if !running {
		return nil
	}
	if sess != nil && sess.cancel != nil {
		sess.cancel()
		return nil
	}
	// No live stream: settle it here so the task cannot stay "running" forever.
	s.finishTask(&streamSession{taskID: id}, wslc.Result{}, nil, true)
	return nil
}
