package wslc

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestArgsKeyIsStableAndOrderSensitive(t *testing.T) {
	args := []string{"container", "list", "--all", "--format", "json"}
	if ArgsKey(args) != ArgsKey(append([]string(nil), args...)) {
		t.Error("ArgsKey must be stable for equal argument lists")
	}
	reordered := []string{"container", "list", "--format", "json", "--all"}
	if ArgsKey(args) == ArgsKey(reordered) {
		t.Error("ArgsKey must distinguish a different argument order")
	}
	if ArgsKey(nil) != "" {
		t.Errorf("ArgsKey(nil) = %q, want empty", ArgsKey(nil))
	}
}

func TestArgsKeyKeepsRepeatedFlagsInOrder(t *testing.T) {
	// One flag that legally appears more than once: every occurrence must be
	// preserved, in order, so the scripted response matches the real command.
	first := []string{"container", "run", "-e", "A=1", "-e", "B=2", "--name", "web"}
	same := []string{"container", "run", "-e", "A=1", "-e", "B=2", "--name", "web"}
	swapped := []string{"container", "run", "-e", "B=2", "-e", "A=1", "--name", "web"}
	if ArgsKey(first) != ArgsKey(same) {
		t.Error("ArgsKey must be stable with repeated flags")
	}
	if ArgsKey(first) == ArgsKey(swapped) {
		t.Error("ArgsKey must keep repeated flag order significant")
	}
	if strings.Count(ArgsKey(first), "A=1") != 1 || strings.Count(ArgsKey(first), "B=2") != 1 {
		t.Errorf("ArgsKey dropped a repeated flag: %q", ArgsKey(first))
	}
}

func TestArgsKeyDoesNotCollideOnSpaces(t *testing.T) {
	if ArgsKey([]string{"a b", "c"}) == ArgsKey([]string{"a", "b c"}) {
		t.Error("ArgsKey must not collide when an argument contains a space")
	}
}

func TestFakeRunnerReturnsScriptedResultAndRecordsCalls(t *testing.T) {
	f := NewFakeRunner()
	want := Result{Stdout: "[]\n", ExitCode: 0}
	f.When([]string{"container", "list", "--all", "--format", "json"}, want, nil)

	res, err := f.Run(context.Background(), Spec{Kind: CmdContainerList, Args: []string{"container", "list", "--all", "--format", "json"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.Stdout != want.Stdout || res.ExitCode != 0 {
		t.Errorf("Run = %+v, want %+v", res, want)
	}
	if len(res.Args) != 5 {
		t.Errorf("Result.Args = %v, want the spec args echoed back", res.Args)
	}

	// A second, differently-shaped call must still record.
	if _, err := f.Run(context.Background(), Spec{Kind: CmdVersion, Args: []string{"version"}}); err == nil {
		t.Error("unscripted args must fail loudly")
	}
	calls := f.Calls()
	if len(calls) != 2 {
		t.Fatalf("Calls() = %d entries, want 2", len(calls))
	}
	if calls[0].Kind != CmdContainerList || calls[1].Kind != CmdVersion {
		t.Errorf("recorded kinds = %v, %v", calls[0].Kind, calls[1].Kind)
	}
	if strings.Join(calls[0].Args, " ") != "container list --all --format json" {
		t.Errorf("recorded args = %v", calls[0].Args)
	}
}

func TestFakeRunnerUnscriptedErrorNamesTheCommand(t *testing.T) {
	f := NewFakeRunner()
	_, err := f.Run(context.Background(), Spec{Kind: CmdImageList, Args: []string{"image", "list"}})
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "image") {
		t.Errorf("error should name the unmatched args, got: %v", err)
	}
}

func TestFakeRunnerScriptedFailurePassesThrough(t *testing.T) {
	f := NewFakeRunner()
	exit := &ExitError{Code: 1, Args: []string{"container", "list"}, Stderr: "错误代码： HCS_E_SERVICE_NOT_AVAILABLE"}
	f.When([]string{"container", "list"}, Result{ExitCode: 1, Stderr: exit.Stderr}, exit)

	res, err := f.Run(context.Background(), Spec{Kind: CmdContainerList, Args: []string{"container", "list"}})
	if res.ExitCode != 1 {
		t.Errorf("Result.ExitCode = %d, want 1", res.ExitCode)
	}
	if err != exit {
		t.Fatalf("err = %v, want the scripted error", err)
	}
}

func TestFakeRunnerStreamDeliversLinesInOrder(t *testing.T) {
	f := NewFakeRunner()
	f.WhenStream([]string{"container", "logs", "-f", "web"}, []string{"one", "two", "three"}, Result{}, nil)

	var got []Line
	res, err := f.Stream(context.Background(), Spec{Kind: CmdContainerLogs, Args: []string{"container", "logs", "-f", "web"}}, func(l Line) {
		got = append(got, l)
	})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("lines = %d, want 3", len(got))
	}
	for i, want := range []string{"one", "two", "three"} {
		if got[i].Text != want || got[i].Stream != "stdout" {
			t.Errorf("line %d = %+v, want stdout %q", i, got[i], want)
		}
		if got[i].Time.IsZero() {
			t.Errorf("line %d has zero time", i)
		}
	}
	if !strings.Contains(res.Stdout, "one") || !strings.Contains(res.Stdout, "three") {
		t.Errorf("Result.Stdout = %q, want the joined lines", res.Stdout)
	}
}

func TestFakeRunnerStreamFallsBackToRunScript(t *testing.T) {
	f := NewFakeRunner()
	f.When([]string{"container", "logs", "web"}, Result{Stdout: "alpha\nbeta\n"}, nil)

	var got []string
	if _, err := f.Stream(context.Background(), Spec{Kind: CmdContainerLogs, Args: []string{"container", "logs", "web"}}, func(l Line) {
		got = append(got, l.Text)
	}); err != nil {
		t.Fatalf("Stream: %v", err)
	}
	if strings.Join(got, ",") != "alpha,beta" {
		t.Errorf("lines = %v, want alpha,beta", got)
	}
}

func TestFakeRunnerNilSinkIsSafe(t *testing.T) {
	f := NewFakeRunner()
	f.WhenStream([]string{"events"}, []string{"a"}, Result{}, nil)
	if _, err := f.Stream(context.Background(), Spec{Kind: CmdEvents, Args: []string{"events"}}, nil); err != nil {
		t.Fatalf("Stream with nil sink: %v", err)
	}
}

func TestFakeRunnerDefaultAndAvailable(t *testing.T) {
	f := NewFakeRunner()
	if err := f.Available(context.Background()); err != nil {
		t.Errorf("Available() = %v, want nil by default", err)
	}
	sentinel := &ExitError{Code: 1, Stderr: "missing"}
	f.SetAvailable(sentinel)
	if err := f.Available(context.Background()); err != sentinel {
		t.Errorf("Available() = %v, want the scripted error", err)
	}

	f.Default(Result{Stdout: "fallback\n"}, nil)
	res, err := f.Run(context.Background(), Spec{Kind: CmdVersion, Args: []string{"version"}})
	if err != nil || res.Stdout != "fallback\n" {
		t.Errorf("Default: got %+v, %v", res, err)
	}
	if len(f.Calls()) != 1 {
		t.Errorf("Calls() = %d, want 1", len(f.Calls()))
	}
	f.Reset()
	if len(f.Calls()) != 0 {
		t.Errorf("Reset did not clear calls")
	}
}

func TestFakeRunnerCallsReturnsCopies(t *testing.T) {
	f := NewFakeRunner()
	args := []string{"container", "list"}
	f.When(args, Result{}, nil)
	if _, err := f.Run(context.Background(), Spec{Args: args}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	calls := f.Calls()
	calls[0].Args[0] = "mutated"
	if got := f.Calls()[0].Args[0]; got != "container" {
		t.Errorf("Calls() leaked internal state: %q", got)
	}

	// Scripted results must be copies too.
	f.When([]string{"x"}, Result{Args: []string{"a"}}, nil)
	res, err := f.Run(context.Background(), Spec{Args: []string{"x"}})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	res.Args[0] = "changed"
	res2, _ := f.Run(context.Background(), Spec{Args: []string{"x"}})
	if res2.Args[0] != "a" {
		t.Errorf("scripted result was mutated through the returned copy: %v", res2.Args)
	}
}

func TestFakeRunnerIsSafeForConcurrentUse(t *testing.T) {
	f := NewFakeRunner()
	f.Default(Result{Stdout: "ok"}, nil)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			args := []string{"container", "list", fmt.Sprint(i)}
			f.When(args, Result{Stdout: fmt.Sprint(i)}, nil)
			if _, err := f.Run(context.Background(), Spec{Args: args}); err != nil {
				t.Errorf("Run(%d): %v", i, err)
			}
			if _, err := f.Stream(context.Background(), Spec{Args: args}, func(Line) {}); err != nil {
				t.Errorf("Stream(%d): %v", i, err)
			}
			_ = f.Calls()
			_ = f.Available(context.Background())
		}(i)
	}
	wg.Wait()
	if got := len(f.Calls()); got != 64 {
		t.Errorf("Calls() = %d, want 64", got)
	}
}

func TestZeroValueFakeRunnerIsUsable(t *testing.T) {
	var f FakeRunner
	f.Default(Result{Stdout: "zero"}, nil)
	res, err := f.Run(context.Background(), Spec{Args: []string{"version"}})
	if err != nil || res.Stdout != "zero" {
		t.Errorf("zero value runner: got %+v, %v", res, err)
	}
}
