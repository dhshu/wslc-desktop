package wslc

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func writeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("stub"), 0o600); err != nil {
		t.Fatalf("write stub executable: %v", err)
	}
	return p
}

func notFound(string) (string, error) { return "", errors.New("not found") }

func TestNewResolverDefaultsArePopulated(t *testing.T) {
	r := NewResolver()
	if r == nil {
		t.Fatal("NewResolver returned nil")
	}
	if r.Getenv == nil || r.LookPath == nil || r.Stat == nil {
		t.Errorf("NewResolver must populate all seams: %+v", r)
	}
	if len(r.Paths) == 0 {
		t.Error("NewResolver must carry install-dir fallbacks")
	}
}

func TestResolverPrefersWSLCEnv(t *testing.T) {
	dir := t.TempDir()
	envExe := writeExecutable(t, dir, "env-wslc.exe")
	pathExe := writeExecutable(t, dir, "path-wslc.exe")

	r := &Resolver{
		Getenv:   func(string) string { return envExe },
		LookPath: func(string) (string, error) { return pathExe, nil },
		Stat:     os.Stat,
	}
	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want, _ := filepath.Abs(envExe); got != want {
		t.Errorf("Resolve() = %q, want %q (WSLC_PATH must win)", got, want)
	}
}

func TestResolverEnvAcceptsQuotedPathAndDirectory(t *testing.T) {
	dir := t.TempDir()
	exe := writeExecutable(t, dir, wslcBinary)

	quoted := &Resolver{Getenv: func(string) string { return `"` + exe + `"` }, LookPath: notFound, Stat: os.Stat}
	if got, err := quoted.Resolve(); err != nil || got != exe {
		t.Errorf("quoted WSLC_PATH: got %q, %v; want %q", got, err, exe)
	}

	asDir := &Resolver{Getenv: func(string) string { return dir }, LookPath: notFound, Stat: os.Stat}
	if got, err := asDir.Resolve(); err != nil || got != filepath.Join(dir, wslcBinary) {
		t.Errorf("directory WSLC_PATH: got %q, %v; want %q", got, err, filepath.Join(dir, wslcBinary))
	}
}

func TestResolverFallsBackToPath(t *testing.T) {
	dir := t.TempDir()
	exe := writeExecutable(t, dir, "from-path.exe")

	r := &Resolver{
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, error) { return exe, nil },
		Stat:     os.Stat,
	}
	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if want, _ := filepath.Abs(exe); got != want {
		t.Errorf("Resolve() = %q, want %q", got, want)
	}
}

func TestResolverSkipsInvalidEnvAndPathEntries(t *testing.T) {
	dir := t.TempDir()
	exe := writeExecutable(t, dir, "install.exe")

	r := &Resolver{
		Getenv:   func(string) string { return filepath.Join(dir, "missing.exe") },
		LookPath: func(string) (string, error) { return "", errors.New("no wslc") },
		Stat:     os.Stat,
		Paths:    []string{filepath.Join(dir, "also-missing.exe"), exe},
	}
	got, err := r.Resolve()
	if err != nil {
		t.Fatalf("Resolve should fall through invalid candidates: %v", err)
	}
	if got != exe {
		t.Errorf("Resolve() = %q, want %q", got, exe)
	}
}

func TestResolverReportsErrExecutableNotFound(t *testing.T) {
	r := &Resolver{
		Getenv:   func(string) string { return filepath.Join(t.TempDir(), "nope.exe") },
		LookPath: notFound,
		Stat:     os.Stat,
		Paths:    []string{filepath.Join(t.TempDir(), "nope2.exe")},
	}
	got, err := r.Resolve()
	if got != "" {
		t.Errorf("Resolve() = %q, want empty path", got)
	}
	if !errors.Is(err, ErrExecutableNotFound) {
		t.Fatalf("Resolve() error = %v, want ErrExecutableNotFound", err)
	}
	if !containsAny(err.Error(), "WSLC_PATH", "PATH") {
		t.Errorf("error should explain what was tried, got: %v", err)
	}
}

func TestResolverDefaultInstallDirIsUsedLast(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("install dir is Windows specific")
	}
	paths := NewResolver().Paths
	if len(paths) == 0 {
		t.Fatal("no fallback paths")
	}
	want := `C:\Program Files\WSL\wslc.exe`
	found := false
	for _, p := range paths {
		if p == want {
			found = true
		}
	}
	if !found {
		t.Errorf("fallback paths = %v, want %q included", paths, want)
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) > 0 && len(s) >= len(sub) {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
		}
	}
	return false
}
