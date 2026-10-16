package service

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolvePortableDirAdoptsWritableDir pins first-run behaviour: a writable
// exe directory is adopted so the settings JSON and WebView2 user data land
// next to the executable. The injected tempDir is unrelated to exeDir, so the
// ephemeral-binary rule cannot interfere.
func TestResolvePortableDirAdoptsWritableDir(t *testing.T) {
	dir := t.TempDir()
	got, ok := resolvePortableDir(dir, filepath.Join(dir, "elsewhere-temp"))
	if !ok {
		t.Fatal("可写的 exe 目录应启用便携模式")
	}
	if got != dir {
		t.Errorf("PortableDataDir = %q, want %q", got, dir)
	}
}

// TestResolvePortableDirReusesExistingSettings covers the portable-copy case:
// data next to the exe is reused even though the probe would also succeed, and
// a second launch must find the same directory.
func TestResolvePortableDirReusesExistingSettings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, settingsFileName()), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := resolvePortableDir(dir, filepath.Join(dir, "elsewhere-temp"))
	if !ok || got != dir {
		t.Fatalf("已有设置文件时应复用 exe 目录，得到 (%q, %v)", got, ok)
	}
}

// TestResolvePortableDirReusesExistingUserData covers a folder that carries
// only WebView2 state (settings deleted, theme kept): portable mode must stay
// on so the user data folder is not orphaned in %APPDATA%.
func TestResolvePortableDirReusesExistingUserData(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, WebView2UserDataDirName), 0o755); err != nil {
		t.Fatal(err)
	}
	got, ok := resolvePortableDir(dir, filepath.Join(dir, "elsewhere-temp"))
	if !ok || got != dir {
		t.Fatalf("已有 userdata 目录时应复用 exe 目录，得到 (%q, %v)", got, ok)
	}
}

// TestResolvePortableDirRejectsMissingDir guards the read-only install case:
// with no data present and an unwritable directory, the caller must fall back
// to %APPDATA% instead of handing WebView2 a path it cannot create.
func TestResolvePortableDirRejectsMissingDir(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "no-such-dir")
	got, ok := resolvePortableDir(missing, filepath.Join(base, "elsewhere-temp"))
	if ok {
		t.Errorf("不存在的目录不应启用便携模式，得到 (%q, %v)", got, ok)
	}
}

// TestResolvePortableDirRejectsTempDir pins the go run / wails dev guard: the
// binary executes from an ephemeral temp dir, which is writable, so without
// this rule dev builds would write settings into a directory that disappears
// when the process exits.
func TestResolvePortableDirRejectsTempDir(t *testing.T) {
	temp := t.TempDir()
	// The exe sitting directly in the temp dir...
	if _, ok := resolvePortableDir(temp, temp); ok {
		t.Error("临时目录本身不应启用便携模式")
	}
	// ...and one in a subdirectory of it (where `go run` actually lands).
	sub := filepath.Join(temp, "go-build123", "b001")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, ok := resolvePortableDir(sub, temp); ok {
		t.Error("临时目录子目录中的二进制不应启用便携模式")
	}
}

// TestIsUnderDirChecksPathComponents guards the classic prefix bug: /a/b must
// not count as "under" /a/bc, or an install under a temp-named sibling folder
// would silently lose portable mode.
func TestIsUnderDirChecksPathComponents(t *testing.T) {
	for _, tc := range []struct {
		path, dir string
		want      bool
	}{
		{filepath.Join("a", "b"), "a", true},
		{"a", "a", true},
		{filepath.Join("a", "b", "c"), filepath.Join("a", "b"), true},
		{"abc", "a", false},
		{filepath.Join("ab", "c"), "a", false},
		{filepath.Join("x", "y"), "a", false},
		{"", "a", false},
		{"a", "", false},
	} {
		if got := isUnderDir(tc.path, tc.dir); got != tc.want {
			t.Errorf("isUnderDir(%q, %q) = %v, want %v", tc.path, tc.dir, got, tc.want)
		}
	}
}

// TestDirWritableProbesRoundTrip confirms the probe succeeds in a writable
// directory and leaves no file behind.
func TestDirWritableProbesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	if !dirWritable(dir) {
		t.Fatal("临时目录应可写")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		t.Errorf("探测文件残留：%s", e.Name())
	}
	if dirWritable(filepath.Join(dir, "missing")) {
		t.Error("不存在的目录应判定为不可写")
	}
}

// TestPortableDataDirStaysOffForTestBinary is the integration guard: `go test`
// executes the test binary from the OS temp dir, so portable mode must be off.
// Without it, tests could silently start writing next to the test binary.
func TestPortableDataDirStaysOffForTestBinary(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skipf("os.Executable 失败：%v", err)
	}
	exeDir := filepath.Dir(exe)
	if !isUnderDir(exeDir, os.TempDir()) {
		t.Skip("测试二进制不在临时目录中运行（预编译后手动执行），跳过")
	}
	if _, ok := PortableDataDir(); ok {
		t.Error("临时目录中的测试二进制不应启用便携模式")
	}
}

// TestSettingsPathFallsBackToAppData pins the non-portable resolution order:
// with portable mode off (guaranteed by the temp-dir rule under `go test`),
// %APPDATA% wins over the home-directory fallback.
func TestSettingsPathFallsBackToAppData(t *testing.T) {
	if _, ok := PortableDataDir(); ok {
		t.Skip("当前环境处于便携模式，跳过 %APPDATA% 回退测试")
	}
	env := map[string]string{"APPDATA": filepath.Join("C:", "Users", "x", "AppData", "Roaming")}
	got, err := settingsPath(env)
	if err != nil {
		t.Fatalf("settingsPath 返回错误：%v", err)
	}
	want := filepath.Join(env["APPDATA"], settingsFileName())
	if got != want {
		t.Errorf("settingsPath = %q, want %q", got, want)
	}
}

// TestSettingsPathHonoursEnvOverride confirms the explicit override still wins
// over every other rule, including portable mode.
func TestSettingsPathHonoursEnvOverride(t *testing.T) {
	explicit := filepath.Join("D:", "portable", settingsFileName())
	got, err := settingsPath(map[string]string{
		"WSLC_DESKTOP_SETTINGS": explicit,
		"APPDATA":               filepath.Join("C:", "Users", "x", "AppData", "Roaming"),
	})
	if err != nil {
		t.Fatalf("settingsPath 返回错误：%v", err)
	}
	if got != explicit {
		t.Errorf("settingsPath = %q, want %q", got, explicit)
	}
}
