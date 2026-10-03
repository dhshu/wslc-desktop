package wslc

import (
	"context"
	"os/exec"
	"time"
)

// CommandKind identifies a wslc operation. It is used by the fake runner to key
// scripted responses and by tests to assert which command a user action produced.
type CommandKind string

const (
	CmdVersion        CommandKind = "version"
	CmdInfo           CommandKind = "info"
	CmdSessionList    CommandKind = "system session list"
	CmdContainerList  CommandKind = "container list"
	CmdContainerStart CommandKind = "container start"
	CmdContainerStop  CommandKind = "container stop"
	CmdContainerKill  CommandKind = "container kill"
	CmdContainerRst   CommandKind = "container restart"
	CmdContainerRm    CommandKind = "container remove"
	CmdContainerPrune CommandKind = "container prune"
	CmdContainerRun   CommandKind = "container run"
	CmdContainerLogs  CommandKind = "container logs"
	CmdContainerExec  CommandKind = "container exec"
	CmdContainerStats CommandKind = "container stats"
	CmdContainerIns   CommandKind = "container inspect"
	CmdImageList      CommandKind = "image list"
	CmdImagePull      CommandKind = "image pull"
	CmdImagePush      CommandKind = "image push"
	CmdImageBuild     CommandKind = "image build"
	CmdImageRm        CommandKind = "image remove"
	CmdImageTag       CommandKind = "image tag"
	CmdImagePrune     CommandKind = "image prune"
	CmdImageIns       CommandKind = "image inspect"
	CmdVolumeList     CommandKind = "volume list"
	CmdVolumeCreate   CommandKind = "volume create"
	CmdVolumeRm       CommandKind = "volume remove"
	CmdNetworkList    CommandKind = "network list"
	CmdNetworkCreate  CommandKind = "network create"
	CmdNetworkRm      CommandKind = "network remove"
	CmdEvents         CommandKind = "events"
)

// Spec is a fully-formed, non-interactive wslc invocation.
//
// Args carries every argument exactly as it will be passed to the process; the
// runner never builds a shell string, so arguments containing spaces or shell
// metacharacters cannot be re-interpreted.
type Spec struct {
	// Kind is the logical command, used for fake scripting and test assertions.
	Kind CommandKind
	// Args are the wslc arguments, excluding the executable itself.
	Args []string
	// Env appends to the inherited environment.
	Env []string
	// Dir is the working directory; empty means inherit.
	Dir string
	// Timeout bounds the invocation. Zero means "use the runner default"; a
	// negative value means no timeout (used for streaming commands).
	Timeout time.Duration
	// Stream asks for line-by-line delivery while the command runs.
	Stream bool
	// Stdin is optional input, e.g. a password for `registry login --password-stdin`.
	Stdin string
}

// Line is one line of streamed output.
type Line struct {
	Stream string // "stdout" or "stderr"
	Text   string
	Time   time.Time
}

// Result is the outcome of an invocation.
type Result struct {
	Args     []string
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	// Truncated reports that captured output hit the configured cap.
	Truncated bool
}

// OK reports whether the command exited successfully.
func (r Result) OK() bool { return r.ExitCode == 0 }

// Runner executes wslc commands. Implementations must be safe for concurrent use.
type Runner interface {
	// Run executes spec to completion and returns its captured output.
	//
	// A non-zero exit is returned BOTH as a populated Result and as an
	// *ExitError, so callers may either inspect the result or check errors.Is
	// against the sentinel errors in errors.go. A failure to start the process
	// returns an error with a zero-value Result.
	Run(ctx context.Context, spec Spec) (Result, error)

	// Stream executes spec while delivering output line by line to sink.
	// The final Result carries the full captured output and exit code.
	Stream(ctx context.Context, spec Spec, sink func(Line)) (Result, error)

	// Available reports whether the wslc executable can be located and executed.
	Available(ctx context.Context) error
}

// CommandFactory lets the exec runner expose how a Spec becomes an *exec.Cmd.
// It exists so tests can assert process wiring without launching a process.
type CommandFactory func(ctx context.Context, exe string, spec Spec) *exec.Cmd
