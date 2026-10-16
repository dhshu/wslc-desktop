//go:build windows

package wslc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// seeMaskNOCLOSEPROCESS makes ShellExecuteExW hand back the process handle
	// of the elevated child so its exit code can be waited on and read.
	seeMaskNOCLOSEPROCESS = 0x00000040
	// seeMaskFlagNoUI suppresses shell error dialogs (e.g. "file not found").
	// The UAC consent prompt is not shell error UI and is still shown.
	seeMaskFlagNoUI = 0x00000400
	// swHide keeps the elevated console child from flashing a window.
	swHide = 0
)

// shellexecuteinfoW mirrors the Win32 SHELLEXECUTEINFOW structure. Field order
// and natural alignment must match exactly; on amd64 Go pads the same way C
// does for this sequence, so no manual padding fields are needed.
type shellexecuteinfoW struct {
	CbSize    uint32
	FMask     uint32
	Hwnd      windows.HWND
	LpVerb    *uint16
	LpFile    *uint16
	LpParams  *uint16
	LpDir     *uint16
	NShow     int32
	HInstApp  windows.Handle
	LpIDList  unsafe.Pointer
	LpClass   *uint16
	HkeyClass windows.Handle
	DwHotKey  uint32
	// HIconOrMonitor is the documented union (hIcon / hMonitor); every field
	// of the union is pointer-sized, so one Handle covers it.
	HIconOrMonitor windows.Handle
	HProcess       windows.Handle
}

var (
	shell32             = windows.NewLazySystemDLL("shell32.dll")
	procShellExecuteExW = shell32.NewProc("ShellExecuteExW")
)

// RunToolElevated runs exe with args through a UAC elevation prompt and waits
// for it to exit. It is the fallback for executables whose manifest demands an
// administrator token (diskpart), which Windows refuses to fork/exec from a
// non-elevated process with ERROR_ELEVATION_REQUIRED.
//
// The elevated process runs hidden and its console output cannot be captured
// across the integrity-level boundary (handles are not inherited through UAC),
// so only the exit code travels back in Result. The script is therefore handed
// over with /s <file> by the caller, never through stdin.
//
// Context cancellation is honoured only before the launch: once UAC is
// accepted the elevated process cannot be killed by this (non-elevated)
// process, so the wait is unconditional. A dismissed consent prompt surfaces
// as ErrElevationDeclined.
func (r *ExecRunner) RunToolElevated(ctx context.Context, exe string, args []string, dir string) (Result, error) {
	if r == nil {
		return Result{}, errors.New("wslc: nil ExecRunner")
	}
	if strings.TrimSpace(exe) == "" {
		return Result{}, fmt.Errorf("%w: no executable configured", ErrExecutableNotFound)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	exePtr, err := syscall.UTF16PtrFromString(exe)
	if err != nil {
		return Result{}, fmt.Errorf("wslc: %s: %w", exe, err)
	}
	verbPtr, err := syscall.UTF16PtrFromString("runas")
	if err != nil {
		return Result{}, fmt.Errorf("wslc: runas verb: %w", err)
	}
	// The parameter string is Windows' native process argument string, not a
	// shell command line: each element is escaped individually so the values
	// survive verbatim.
	var params string
	for _, a := range args {
		params += syscall.EscapeArg(a) + " "
	}
	params = strings.TrimSuffix(params, " ")
	var paramsPtr *uint16
	if params != "" {
		if paramsPtr, err = syscall.UTF16PtrFromString(params); err != nil {
			return Result{}, fmt.Errorf("wslc: parameters %q: %w", params, err)
		}
	}
	var dirPtr *uint16
	if dir != "" {
		if dirPtr, err = syscall.UTF16PtrFromString(dir); err != nil {
			return Result{}, fmt.Errorf("wslc: dir %q: %w", dir, err)
		}
	}

	sei := shellexecuteinfoW{
		CbSize:   uint32(unsafe.Sizeof(shellexecuteinfoW{})),
		FMask:    seeMaskNOCLOSEPROCESS | seeMaskFlagNoUI,
		LpVerb:   verbPtr,
		LpFile:   exePtr,
		LpDir:    dirPtr,
		LpParams: paramsPtr,
		NShow:    swHide,
	}
	start := time.Now()
	r1, _, callErr := procShellExecuteExW.Call(uintptr(unsafe.Pointer(&sei)))
	if r1 == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return Result{}, fmt.Errorf("%w: user dismissed the UAC prompt", ErrElevationDeclined)
		}
		return Result{}, fmt.Errorf("wslc: cannot elevate %s: %v", exe, callErr)
	}
	if sei.HProcess == 0 {
		return Result{}, fmt.Errorf("wslc: cannot elevate %s: no process handle returned", exe)
	}
	defer windows.CloseHandle(sei.HProcess)

	if _, err := windows.WaitForSingleObject(sei.HProcess, windows.INFINITE); err != nil {
		return Result{}, fmt.Errorf("wslc: waiting for elevated %s: %w", exe, err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(sei.HProcess, &code); err != nil {
		return Result{}, fmt.Errorf("wslc: exit code of elevated %s: %w", exe, err)
	}
	return Result{
		Args:     append([]string(nil), args...),
		ExitCode: int(code),
		Duration: time.Since(start),
	}, nil
}
