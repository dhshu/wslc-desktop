package wslc

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// wslcEnvVar overrides every other lookup when it points at a usable file.
const wslcEnvVar = "WSLC_PATH"

// wslcBinary is the executable name used both for PATH lookup and for the
// well-known install directory.
const wslcBinary = "wslc.exe"

// installPath is where the Windows Subsystem for Linux package installs wslc.
const installPath = `C:\Program Files\WSL\wslc.exe`

// Resolver locates wslc.exe on this machine.
//
// The lookup order is fixed by the frozen contract:
//
//	WSLC_PATH -> PATH -> C:\Program Files\WSL\wslc.exe
//
// Every candidate that does not resolve to an existing file is skipped, so a
// stale WSLC_PATH cannot hide a working installation; if nothing resolves,
// ErrExecutableNotFound is returned with the list of attempted locations.
//
// The function fields exist so tests can drive the search without touching the
// real environment; NewResolver fills them with the real implementations.
type Resolver struct {
	Getenv   func(string) string
	LookPath func(string) (string, error)
	Stat     func(string) (os.FileInfo, error)
	// Paths are the last-resort candidates, tried in order after PATH.
	Paths []string
}

// NewResolver returns a Resolver wired to the real environment.
func NewResolver() *Resolver {
	return &Resolver{
		Getenv:   os.Getenv,
		LookPath: exec.LookPath,
		Stat:     os.Stat,
		Paths:    DefaultSearchPaths(),
	}
}

// DefaultSearchPaths lists the well-known install locations of wslc.exe.
func DefaultSearchPaths() []string {
	paths := []string{installPath}
	if pf := strings.TrimSpace(os.Getenv("ProgramFiles")); pf != "" {
		p := filepath.Join(pf, "WSL", wslcBinary)
		if !strings.EqualFold(p, installPath) {
			paths = append(paths, p)
		}
	}
	if pf86 := strings.TrimSpace(os.Getenv("ProgramFiles(x86)")); pf86 != "" {
		paths = append(paths, filepath.Join(pf86, "WSL", wslcBinary))
	}
	return paths
}

// Resolve returns the absolute path of wslc.exe, or ErrExecutableNotFound.
func (r *Resolver) Resolve() (string, error) {
	var tried []string

	if raw := strings.TrimSpace(strings.Trim(r.getenv(wslcEnvVar), `"`)); raw != "" {
		if p, ok := r.check(raw, true); ok {
			return p, nil
		}
		tried = append(tried, wslcEnvVar+"="+raw)
	} else {
		tried = append(tried, wslcEnvVar+" (unset)")
	}

	if p, err := r.lookPath(wslcBinary); err == nil {
		if abs, ok := r.check(p, false); ok {
			return abs, nil
		}
	}
	tried = append(tried, "PATH")

	for _, candidate := range r.paths() {
		if p, ok := r.check(candidate, false); ok {
			return p, nil
		}
		tried = append(tried, candidate)
	}

	return "", fmt.Errorf("%w: %s not found on this machine (tried: %s)", ErrExecutableNotFound, wslcBinary, strings.Join(tried, ", "))
}

// check validates one candidate and returns its absolute path.
//
// allowDir is only set for WSLC_PATH, where pointing at the install directory
// is a natural thing to do; in that case the directory's wslc.exe is used.
func (r *Resolver) check(path string, allowDir bool) (string, bool) {
	path = strings.TrimSpace(strings.Trim(path, `"`))
	if path == "" {
		return "", false
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	info, err := r.stat(path)
	if err != nil {
		return "", false
	}
	if info.IsDir() {
		if !allowDir {
			return "", false
		}
		candidate := filepath.Join(path, wslcBinary)
		ci, err := r.stat(candidate)
		if err != nil || ci.IsDir() {
			return "", false
		}
		return candidate, true
	}
	return path, true
}

func (r *Resolver) getenv(key string) string {
	if r != nil && r.Getenv != nil {
		return r.Getenv(key)
	}
	return os.Getenv(key)
}

func (r *Resolver) lookPath(name string) (string, error) {
	if r != nil && r.LookPath != nil {
		return r.LookPath(name)
	}
	return exec.LookPath(name)
}

func (r *Resolver) stat(path string) (os.FileInfo, error) {
	if r != nil && r.Stat != nil {
		return r.Stat(path)
	}
	return os.Stat(path)
}

func (r *Resolver) paths() []string {
	if r != nil && len(r.Paths) > 0 {
		return r.Paths
	}
	return DefaultSearchPaths()
}
