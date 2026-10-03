package wslc

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ArgsKey builds a stable map key for an argument vector.
//
// The separator is a unit separator, not a space, so two different argument
// vectors that differ only in where a space sits cannot collide. Argument order
// and repeated flags (for example several `-e` pairs) are preserved verbatim.
func ArgsKey(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.Join(args, "\x1f")
}

// fakeScript is one scripted response.
type fakeScript struct {
	result Result
	err    error
	lines  []string
	stream bool
}

// FakeRunner is an in-memory Runner for service-layer tests.
//
// Responses are registered against ArgsKey(spec.Args); the zero value is
// usable. All methods are safe for concurrent use, mirroring the Runner
// contract.
type FakeRunner struct {
	mu           sync.Mutex
	scripts      map[string]fakeScript
	fallback     *fakeScript
	calls        []Spec
	availableErr error
}

// NewFakeRunner returns an empty FakeRunner.
func NewFakeRunner() *FakeRunner {
	return &FakeRunner{scripts: make(map[string]fakeScript)}
}

// When registers the response returned by Run (and by Stream when no streaming
// script was registered) for an argument vector.
func (f *FakeRunner) When(args []string, res Result, err error) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	f.scripts[ArgsKey(args)] = fakeScript{result: cloneResult(res), err: err}
	return f
}

// WhenStream registers the lines delivered to the sink by Stream for an
// argument vector. The final Result is returned unchanged.
func (f *FakeRunner) WhenStream(args []string, lines []string, res Result, err error) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	f.scripts[ArgsKey(args)] = fakeScript{
		result: cloneResult(res),
		err:    err,
		lines:  append([]string(nil), lines...),
		stream: true,
	}
	return f
}

// Default registers a response used when no exact argument match is found.
func (f *FakeRunner) Default(res Result, err error) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fallback = &fakeScript{result: cloneResult(res), err: err}
	return f
}

// SetAvailable scripts the result of Available.
func (f *FakeRunner) SetAvailable(err error) *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.availableErr = err
	return f
}

// Reset forgets every recorded call and scripted response.
func (f *FakeRunner) Reset() *FakeRunner {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = make(map[string]fakeScript)
	f.fallback = nil
	f.calls = nil
	f.availableErr = nil
	return f
}

// Calls returns a copy of every Spec passed to Run and Stream, in order.
func (f *FakeRunner) Calls() []Spec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Spec, 0, len(f.calls))
	for _, c := range f.calls {
		c.Args = append([]string(nil), c.Args...)
		c.Env = append([]string(nil), c.Env...)
		out = append(out, c)
	}
	return out
}

// Run returns the scripted response, or a descriptive error when none matches.
func (f *FakeRunner) Run(_ context.Context, spec Spec) (Result, error) {
	if f == nil {
		return Result{}, fmt.Errorf("wslc: nil FakeRunner")
	}
	script, ok := f.record(spec)
	if !ok {
		return Result{}, fmt.Errorf("wslc: fake runner: no scripted response for args %q", ArgsKey(spec.Args))
	}
	res := cloneResult(script.result)
	if len(res.Args) == 0 {
		res.Args = append([]string(nil), spec.Args...)
	}
	if script.stream && res.Stdout == "" && len(script.lines) > 0 {
		res.Stdout = strings.Join(script.lines, "\n") + "\n"
	}
	return res, script.err
}

// Stream delivers the scripted lines to sink, then returns the scripted Result.
func (f *FakeRunner) Stream(_ context.Context, spec Spec, sink func(Line)) (Result, error) {
	if f == nil {
		return Result{}, fmt.Errorf("wslc: nil FakeRunner")
	}
	script, ok := f.record(spec)
	if !ok {
		return Result{}, fmt.Errorf("wslc: fake runner: no scripted response for args %q", ArgsKey(spec.Args))
	}
	lines := script.lines
	if !script.stream {
		lines = splitOutputLines(script.result.Stdout)
	}
	for _, line := range lines {
		if sink == nil {
			continue
		}
		sink(Line{Stream: "stdout", Text: line, Time: time.Now()})
	}
	res := cloneResult(script.result)
	if len(res.Args) == 0 {
		res.Args = append([]string(nil), spec.Args...)
	}
	if res.Stdout == "" && len(lines) > 0 {
		res.Stdout = strings.Join(lines, "\n") + "\n"
	}
	return res, script.err
}

// Available returns the scripted error (nil unless SetAvailable was called).
func (f *FakeRunner) Available(context.Context) error {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.availableErr
}

// record stores spec and returns the matching script.
func (f *FakeRunner) record(spec Spec) (fakeScript, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensure()
	recorded := spec
	recorded.Args = append([]string(nil), spec.Args...)
	recorded.Env = append([]string(nil), spec.Env...)
	f.calls = append(f.calls, recorded)
	if script, ok := f.scripts[ArgsKey(spec.Args)]; ok {
		return script, true
	}
	if f.fallback != nil {
		return *f.fallback, true
	}
	return fakeScript{}, false
}

func (f *FakeRunner) ensure() {
	if f.scripts == nil {
		f.scripts = make(map[string]fakeScript)
	}
}

// cloneResult deep-copies the slice fields so callers cannot mutate a script.
func cloneResult(res Result) Result {
	out := res
	out.Args = append([]string(nil), res.Args...)
	return out
}

// splitOutputLines splits captured output into lines, dropping a trailing newline.
func splitOutputLines(s string) []string {
	if s == "" {
		return nil
	}
	trimmed := strings.TrimRight(s, "\r\n")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "\n")
}
