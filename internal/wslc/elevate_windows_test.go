package wslc

import (
	"context"
	"errors"
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestStartErrorClassifiesElevationRequired pins the ERROR_ELEVATION_REQUIRED
// classification: diskpart's manifest demands an administrator token, so the
// real machine's failure mode is a PathError carrying Win32 errno 740, which
// callers must be able to detect as ErrElevationRequired without parsing text.
func TestStartErrorClassifiesElevationRequired(t *testing.T) {
	r := NewExecRunner(`C:\Program Files\WSL\wslc.exe`)
	err := r.startError(Spec{Args: []string{"diskpart"}}, &os.PathError{
		Op:   "fork/exec",
		Path: `C:\WINDOWS\system32\diskpart.exe`,
		Err:  windows.ERROR_ELEVATION_REQUIRED,
	})
	if !errors.Is(err, ErrElevationRequired) {
		t.Fatalf("expected ErrElevationRequired, got %v", err)
	}
}

func TestRunToolElevatedRequiresExecutable(t *testing.T) {
	r := NewExecRunner("wslc.exe")
	if _, err := r.RunToolElevated(context.Background(), "  ", nil, ""); !errors.Is(err, ErrExecutableNotFound) {
		t.Fatalf("expected ErrExecutableNotFound for an empty exe, got %v", err)
	}
}
