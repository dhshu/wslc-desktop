package wslc

import (
	"errors"
	"fmt"
	"strings"
)

// Sentinel errors returned by the adapter layer so upper layers never have to
// inspect exit codes or localized stderr text themselves.
var (
	// ErrNotFound is returned when wslc resolves and runs but reports that the
	// referenced object does not exist.
	ErrNotFound = errors.New("wslc: object not found")

	// ErrServiceUnavailable is returned when the container service required by
	// wslc is not running. On Windows this surfaces as HCS_E_SERVICE_NOT_AVAILABLE
	// and is commonly caused by the Hyper-V Host Compute Service (vmcompute)
	// being stopped or disabled.
	ErrServiceUnavailable = errors.New("wslc: container service unavailable")

	// ErrExecutableNotFound is returned when wslc.exe could not be located on
	// this machine (usually because WSL is older than 2.9.3).
	ErrExecutableNotFound = errors.New("wslc: executable not found")

	// ErrUnsupportedCommand is returned when the installed wslc build does not
	// implement the requested command or option.
	ErrUnsupportedCommand = errors.New("wslc: unsupported command or option")
)

// ExitError reports a non-zero exit from a wslc invocation.
type ExitError struct {
	Code   int
	Args   []string
	Stderr string
	Stdout string
}

func (e *ExitError) Error() string {
	msg := strings.TrimSpace(e.Stderr)
	if msg == "" {
		msg = strings.TrimSpace(e.Stdout)
	}
	if msg == "" {
		msg = "no output"
	}
	return fmt.Sprintf("wslc %s: exit code %d: %s", strings.Join(e.Args, " "), e.Code, firstLine(msg))
}

// Unwrap maps a non-zero exit onto the closest sentinel error so callers can use
// errors.Is without parsing localized text.
//
// wslc localizes its messages, so both the English and Simplified Chinese
// wordings of each condition are matched.
func (e *ExitError) Unwrap() error {
	text := strings.ToUpper(e.Stderr + "\n" + e.Stdout)
	// E_ACCESSDENIED is a Windows-side access refusal (vmcompute stopped, or a
	// non-container command run without a session). It is NOT an "object not
	// found": classifying it as ErrNotFound would mislead the UI and the tests,
	// because every volume/network/container probe on a locked-down machine
	// would start to look like a missing object.
	switch {
	case strings.Contains(text, "HCS_E_SERVICE_NOT_AVAILABLE"),
		strings.Contains(text, "无法启动操作"),
		strings.Contains(text, "COULD NOT BE STARTED BECAUSE A REQUIRED FEATURE"),
		strings.Contains(text, "未安装所需的特性"):
		return ErrServiceUnavailable
	case strings.Contains(text, "NO SUCH CONTAINER"),
		strings.Contains(text, "NO SUCH IMAGE"),
		strings.Contains(text, "NO SUCH VOLUME"),
		strings.Contains(text, "NO SUCH NETWORK"),
		strings.Contains(text, "NOT FOUND"),
		// zh-CN: "找不到容器 'web'。" / "找不到卷: 'x'" / "找不到映像 ..."
		strings.Contains(text, "找不到"),
		strings.Contains(text, "不存在"):
		return ErrNotFound
	case strings.Contains(text, "未被识别"),
		strings.Contains(text, "无法识别"),
		strings.Contains(text, "UNRECOGNIZED"),
		strings.Contains(text, "UNKNOWN COMMAND"),
		strings.Contains(text, "UNKNOWN FLAG"):
		return ErrUnsupportedCommand
	}
	return nil
}

// firstLine keeps error messages to a single readable line.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		return s[:i]
	}
	return s
}
