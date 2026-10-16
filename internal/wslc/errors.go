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

	// ErrAccessDenied is returned when wslc itself runs but every
	// session-touching call is refused with 拒绝访问 / E_ACCESSDENIED. Real
	// machines show this does NOT imply vmcompute is down: wslc keeps two
	// independent sessions per elevation level (wslc-cli-<user> and
	// wslc-cli-admin-<user>), so a client can be refused while all services
	// are Running — typically because of a stale session created at a
	// different elevation level, or a wedged session service handle.
	ErrAccessDenied = errors.New("wslc: access denied")

	// ErrExecutableNotFound is returned when wslc.exe could not be located on
	// this machine (usually because WSL is older than 2.9.3).
	ErrExecutableNotFound = errors.New("wslc: executable not found")

	// ErrUnsupportedCommand is returned when the installed wslc build does not
	// implement the requested command or option.
	ErrUnsupportedCommand = errors.New("wslc: unsupported command or option")

	// ErrElevationRequired is returned when a process could not be started at
	// all because Windows refused with ERROR_ELEVATION_REQUIRED (740). Real
	// machines show this is the fate of every diskpart invocation launched
	// from a non-elevated wslc Desktop: diskpart's manifest demands an
	// administrator token, so fork/exec fails before any command runs.
	ErrElevationRequired = errors.New("wslc: elevation required")

	// ErrElevationDeclined is returned when the user dismisses the UAC consent
	// dialog of an elevated launch (ShellExecuteEx reports ERROR_CANCELLED).
	// It is distinct from ErrElevationRequired so the UI can tell "needs
	// permission" apart from "permission was refused".
	ErrElevationDeclined = errors.New("wslc: elevation declined")
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
	switch {
	case strings.Contains(text, "HCS_E_SERVICE_NOT_AVAILABLE"),
		strings.Contains(text, "无法启动操作"),
		strings.Contains(text, "COULD NOT BE STARTED BECAUSE A REQUIRED FEATURE"),
		strings.Contains(text, "未安装所需的特性"):
		return ErrServiceUnavailable
	// 拒绝访问 must be classified on its own: it reads like a permission
	// problem but on real machines it also appears with every service up,
	// when the elevation level of the client and the surviving session
	// disagree. It is deliberately NOT ErrServiceUnavailable, so the UI can
	// suggest `wsl --shutdown` instead of blaming vmcompute.
	case strings.Contains(text, "拒绝访问"),
		strings.Contains(text, "E_ACCESSDENIED"),
		strings.Contains(text, "ACCESS IS DENIED"):
		return ErrAccessDenied
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
