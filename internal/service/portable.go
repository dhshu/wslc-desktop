package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Portable (exe-adjacent) data storage.
//
// The app owns two kinds of on-disk state on Windows:
//
//  1. its own settings JSON (settings.go), and
//  2. WebView2's user data folder — localStorage (the theme preference
//     wslc.theme lives there), cookies, IndexedDB and the renderer cache.
//
// Both default to per-user locations (%APPDATA%). Portable mode moves both
// next to the executable so a copied folder — USB stick, another drive, a
// re-extracted zip — carries its configuration with it.
//
// The rules, in order:
//
//   - data that already lives next to the exe is reused, even if the directory
//     is read-only (a pre-seeded install must still read its settings);
//   - an otherwise writable exe directory is adopted on first run;
//   - the OS temp directory is never adopted: `go run` and `wails dev` execute
//     from an ephemeral temp dir, so data written there would vanish with the
//     process;
//   - anything else (read-only install, e.g. under C:\Program Files) falls
//     back to the per-user locations.
//
// The fallback matters most for WebView2: Wails forwards
// options.Windows.WebviewUserDataPath verbatim to
// CreateCoreWebView2EnvironmentWithOptions, and an unwritable path makes
// environment creation fail fatally (message box + non-zero exit) — there is
// no graceful degradation inside WebView2 itself.

// WebView2UserDataDirName is the subdirectory of the portable data directory
// that holds WebView2's user data. main.go passes it to Wails as
// options.Windows.WebviewUserDataPath.
const WebView2UserDataDirName = "userdata"

// PortableDataDir returns the directory that hosts this app's on-disk data
// when the app runs in portable mode — the executable's own directory — and
// reports ok=false when that directory cannot host data, in which case callers
// fall back to per-user locations (%APPDATA%, or WebView2's own default).
func PortableDataDir() (dir string, ok bool) {
	exe, err := os.Executable()
	if err != nil {
		return "", false
	}
	return resolvePortableDir(filepath.Dir(exe), os.TempDir())
}

// resolvePortableDir is the pure core of PortableDataDir. exeDir is the
// directory of the running executable; tempDir is the OS temp directory,
// injected so tests can exercise the ephemeral-binary rule deterministically.
func resolvePortableDir(exeDir, tempDir string) (string, bool) {
	if exeDir == "" {
		return "", false
	}
	// An ephemeral binary directory is never a portable install.
	if isUnderDir(exeDir, tempDir) {
		return "", false
	}
	// Data that already lives next to the exe wins, read-only or not: this is
	// what makes a portable copy keep working across machines, and a
	// pre-seeded read-only install still readable.
	if fileExists(filepath.Join(exeDir, settingsFileName())) ||
		dirExists(filepath.Join(exeDir, WebView2UserDataDirName)) {
		return exeDir, true
	}
	// First run: adopt the directory only when it is writable. The probe
	// mirrors exactly what the two writers need — Settings.Save creates the
	// settings file here, WebView2 creates its user data subdirectory here.
	if dirWritable(exeDir) {
		return exeDir, true
	}
	return "", false
}

// dirWritable reports whether a file can be created in (and removed from) dir.
// The probe name carries the pid so two concurrently launching instances can
// never fight over the same file.
func dirWritable(dir string) bool {
	probe := filepath.Join(dir, fmt.Sprintf(".wslc-desktop-probe-%d", os.Getpid()))
	f, err := os.OpenFile(probe, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return false
	}
	if err := f.Close(); err != nil {
		os.Remove(probe)
		return false
	}
	os.Remove(probe)
	return true
}

// isUnderDir reports whether path is dir itself or lives inside it. The
// comparison is path-component aware, so /a/b is not "under" /a/bc.
func isUnderDir(path, dir string) bool {
	if dir == "" || path == "" {
		return false
	}
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// fileExists reports whether name is an existing regular file (not a dir).
func fileExists(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular()
}

// dirExists reports whether name is an existing directory.
func dirExists(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.IsDir()
}
