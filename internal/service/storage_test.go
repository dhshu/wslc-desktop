package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// TestFormatBytes covers the size rendering the storage view shows. It is the
// only user-facing formatting in this file, so it earns a table test.
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{-5, "0 B"},
		{1, "1 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{571 * 1024 * 1024, "571.0 MB"},
		{11*1024*1024*1024 + 275*1024*1024, "11.3 GB"},
		{2 * int64(1024) * 1024 * 1024 * 1024, "2.0 TB"},
	}
	for _, tc := range cases {
		if got := formatBytes(tc.in); got != tc.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestValidateSessionName pins the path-traversal defence. Session names are
// used to build filesystem paths, so anything that could escape the sessions
// directory must be rejected rather than sanitised.
func TestValidateSessionName(t *testing.T) {
	cases := []struct {
		in   string
		ok   bool
		want string
	}{
		{"default", true, "default"},
		{"wslc-cli-dhshu", true, "wslc-cli-dhshu"},
		{"wslc-cli-admin-dhshu", true, "wslc-cli-admin-dhshu"},
		{"  padded  ", true, "padded"},
		{"", false, ""},
		{"   ", false, ""},
		{".", false, ""},
		{"..", false, ""},
		{"../escape", false, ""},
		{".\\escape", false, ""},
		{"a/b", false, ""},
		{"a\\b", false, ""},
		{"a/b/c", false, ""},
		{"a|b", false, ""},
		{"a<b", false, ""},
		{"a*c", false, ""},
		{"a", true, "a"},
	}
	for _, tc := range cases {
		got, err := validateSessionName(tc.in)
		if tc.ok {
			if err != nil {
				t.Errorf("validateSessionName(%q) unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("validateSessionName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		} else {
			if err == nil {
				t.Errorf("validateSessionName(%q) = %q, want error", tc.in, got)
			}
		}
	}
}

func TestValidateSessionNameTooLong(t *testing.T) {
	_, err := validateSessionName(strings.Repeat("a", 201))
	if err == nil {
		t.Fatal("validateSessionName: expected an error for a name over 200 characters")
	}
}

// TestIsValidSessionDirName is the weaker enumeration check: it never fails, it
// only filters out names that cannot be a session directory.
func TestIsValidSessionDirName(t *testing.T) {
	cases := map[string]bool{
		"default":        true,
		"wslc-cli-dhshu": true,
		"":               false,
		".":              false,
		"..":             false,
		".hidden":        false,
		"a/b":            false,
	}
	for name, want := range cases {
		if got := isValidSessionDirName(name); got != want {
			t.Errorf("isValidSessionDirName(%q) = %v, want %v", name, got, want)
		}
	}
}

// storageFakeRunner is a wslc.Runner that also implements ToolRunner, so the
// storage tests can assert the exact diskpart invocation without launching it.
type storageFakeRunner struct {
	*Faked
	calls  []toolCall
	result wslc.Result
	err    error
}

// Faked wraps the package's FakeRunner so the tool runner below keeps the
// ordinary Runner behaviour while adding a RunTool recording.
type Faked struct {
	fake *wslc.FakeRunner
}

type toolCall struct {
	exe   string
	args  []string
	stdin string
	dir   string
}

func (f *storageFakeRunner) Run(ctx context.Context, spec wslc.Spec) (wslc.Result, error) {
	return f.fake.Run(ctx, spec)
}

func (f *storageFakeRunner) Stream(ctx context.Context, spec wslc.Spec, sink func(wslc.Line)) (wslc.Result, error) {
	return f.fake.Stream(ctx, spec, sink)
}

func (f *storageFakeRunner) Available(ctx context.Context) error {
	return f.fake.Available(ctx)
}

func (f *storageFakeRunner) RunTool(ctx context.Context, exe string, args []string, stdin, dir string) (wslc.Result, error) {
	f.calls = append(f.calls, toolCall{exe: exe, args: args, stdin: stdin, dir: dir})
	return f.result, f.err
}

func TestListSessionStorageReportsEverySessionDir(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	mustMkdirAll(t, sessionsDir)

	// Two terminated sessions with real files, one running session, one
	// directory that has no storage file, and one entry that is a file rather
	// than a directory.
	mustWriteFile(t, filepath.Join(sessionsDir, "wslc-cli-dhshu", vhdxName), 1024*1024*1024)
	mustWriteFile(t, filepath.Join(sessionsDir, "wslc-cli-admin-dhshu", vhdxName), 571*1024*1024)
	mustWriteFile(t, filepath.Join(sessionsDir, "wslc-cli-running", vhdxName), 128*1024*1024)
	mustMkdirAll(t, filepath.Join(sessionsDir, "empty-session"))
	mustWriteFile(t, filepath.Join(sessionsDir, "stray.txt"), 8)
	mustMkdirAll(t, filepath.Join(sessionsDir, ".hidden"))

	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	// `wslc system session list` reports only the running session, so the join
	// marks exactly one directory as active.
	fake.When(sessionArgs(), wslc.Result{Stdout: sessionListFixture("wslc-cli-running")}, nil)
	// `wslc info` supplies the settings file path; it does not exist on disk,
	// so storagePathFromSettings falls through to the env override.
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{"SettingsFile":""}}`}, nil)

	t.Setenv("WSLC_STORAGE_PATH", base)
	svc := NewService(fake, nil)

	got, err := svc.ListSessionStorage(context.Background())
	if err != nil {
		t.Fatalf("ListSessionStorage: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 session directories (file and .hidden skipped), got %d: %+v", len(got), got)
	}

	byName := map[string]domain.SessionStorage{}
	for _, s := range got {
		byName[s.SessionName] = s
	}

	dhshu := byName["wslc-cli-dhshu"]
	if !dhshu.Exists || dhshu.BytesOnDisk != 1024*1024*1024 {
		t.Errorf("wslc-cli-dhshu: Exists=%v BytesOnDisk=%d", dhshu.Exists, dhshu.BytesOnDisk)
	}
	if dhshu.SizeText != "1.0 GB" {
		t.Errorf("wslc-cli-dhshu: SizeText=%q, want %q", dhshu.SizeText, "1.0 GB")
	}
	if dhshu.Active {
		t.Error("wslc-cli-dhshu: Active should be false (terminated session)")
	}
	if dhshu.Path != filepath.Join(sessionsDir, "wslc-cli-dhshu", vhdxName) {
		t.Errorf("wslc-cli-dhshu: Path=%q", dhshu.Path)
	}

	admin := byName["wslc-cli-admin-dhshu"]
	if admin.SizeText != "571.0 MB" {
		t.Errorf("admin: SizeText=%q, want %q", admin.SizeText, "571.0 MB")
	}

	running := byName["wslc-cli-running"]
	if !running.Active {
		t.Error("wslc-cli-running: Active should be true")
	}

	empty := byName["empty-session"]
	if empty.Exists || empty.BytesOnDisk != 0 || empty.SizeText != "0 B" {
		t.Errorf("empty-session: Exists=%v Bytes=%d SizeText=%q", empty.Exists, empty.BytesOnDisk, empty.SizeText)
	}
}

func TestListSessionStorageMissingDirectoryIsNotAnError(t *testing.T) {
	t.Setenv("WSLC_STORAGE_PATH", filepath.Join(t.TempDir(), "nowhere"))
	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	svc := NewService(fake, nil)

	got, err := svc.ListSessionStorage(context.Background())
	if err != nil {
		t.Fatalf("expected no error for a missing sessions directory, got %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected an empty list, got %d entries", len(got))
	}
}

func TestListSessionStorageEnvOverrideIsAuthoritative(t *testing.T) {
	base := t.TempDir()
	mustWriteFile(t, filepath.Join(base, "wslc", "sessions", "env-session", vhdxName), 4096)

	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(fake, nil)
	got, err := svc.ListSessionStorage(context.Background())
	if err != nil {
		t.Fatalf("ListSessionStorage: %v", err)
	}
	if len(got) != 1 || got[0].SessionName != "env-session" {
		t.Fatalf("expected the env override to be used, got %+v", got)
	}
}

func TestStoragePathFromSettingsReadsSessionStoragePath(t *testing.T) {
	// storagePathFromSettings is the one piece of this feature that touches a
	// real file on disk, so it earns a direct unit test rather than riding on
	// ListSessionStorage.
	base := t.TempDir()
	settingsDir := t.TempDir()
	settingsFile := filepath.Join(settingsDir, "settings.yaml")
	// The key is indented under the session: mapping, which is wslc's own shape.
	mustWriteFileContent(t, settingsFile, "# wslc user settings\nsession:\n    storagePath: "+base+"\n")

	tool := &storageFakeRunner{Faked: &Faked{fake: wslc.NewFakeRunner().Default(wslc.Result{}, nil)}}
	tool.fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{"SettingsFile":"` + settingsFile + `"}}`}, nil)
	svc := NewService(tool, nil)

	got, ok := storagePathFromSettings(context.Background(), svc)
	if !ok {
		t.Fatal("storagePathFromSettings: expected ok=true")
	}
	if got != base {
		t.Fatalf("storagePathFromSettings = %q, want %q", got, base)
	}
}

func TestStoragePathFromSettingsRejectsDefaultToken(t *testing.T) {
	// settings.yaml literally contains "storagePath: default" for the built-in
	// default; that token is not a path and must not be used as one.
	settingsDir := t.TempDir()
	settingsFile := filepath.Join(settingsDir, "settings.yaml")
	mustWriteFileContent(t, settingsFile, "# wslc user settings\nsession:\n    storagePath: default\n")

	tool := &storageFakeRunner{Faked: &Faked{fake: wslc.NewFakeRunner().Default(wslc.Result{}, nil)}}
	tool.fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{"SettingsFile":"` + settingsFile + `"}}`}, nil)
	svc := NewService(tool, nil)

	got, ok := storagePathFromSettings(context.Background(), svc)
	if ok {
		t.Fatalf("expected the 'default' token to be rejected, got %q", got)
	}
}

func TestStoragePathFromSettingsSkipsMissingInfo(t *testing.T) {
	tool := &storageFakeRunner{Faked: &Faked{fake: wslc.NewFakeRunner().Default(wslc.Result{}, nil)}}
	tool.fake.When(infoArgs(), wslc.Result{ExitCode: 1}, exitError(infoArgs(), 1, "boom"))
	svc := NewService(tool, nil)

	if got, ok := storagePathFromSettings(context.Background(), svc); ok || got != "" {
		t.Fatalf("expected (\"\", false) when wslc info fails, got (%q, %v)", got, ok)
	}
}
func TestListSessionStorageIgnoresDefaultToken(t *testing.T) {
	// settings.yaml literally contains "storagePath: default" for the built-in
	// default; that token is not a path and must not be used as one.
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	mustWriteFile(t, filepath.Join(sessionsDir, "default", vhdxName), 4096)

	settingsDir := t.TempDir()
	settingsFile := filepath.Join(settingsDir, "settings.yaml")
	mustWriteFileContent(t, settingsFile, "# wslc user settings\nsession:\n    storagePath: default\n")

	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{"SettingsFile":"` + settingsFile + `"}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(fake, nil)
	got, err := svc.ListSessionStorage(context.Background())
	if err != nil {
		t.Fatalf("ListSessionStorage: %v", err)
	}
	if len(got) != 1 || got[0].SessionName != "default" {
		t.Fatalf("expected the base fallback to be used when storagePath is 'default', got %+v", got)
	}
}

func TestResetSessionStorageDeletesTheFile(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	target := filepath.Join(sessionsDir, "wslc-cli-dhshu", vhdxName)
	mustWriteFile(t, target, 1024*1024*1024)

	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil) // no running sessions
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(fake, nil)
	out, err := svc.ResetSessionStorage(context.Background(), "wslc-cli-dhshu")
	if err != nil {
		t.Fatalf("ResetSessionStorage: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Errorf("storage.vhdx should be gone, but Stat succeeded (err=%v)", err)
	}
	if !strings.Contains(out, "已删除") || !strings.Contains(out, "1.0 GB") {
		t.Errorf("unexpected output: %q", out)
	}
	// The directory must survive so wslc can recreate the session.
	if _, err := os.Stat(filepath.Dir(target)); err != nil {
		t.Errorf("the session directory must be kept: %v", err)
	}
}

func TestResetSessionStorageRefusesRunningSession(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	target := filepath.Join(sessionsDir, "wslc-cli-running", vhdxName)
	mustWriteFile(t, target, 4096)

	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: sessionListFixture("wslc-cli-running")}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(fake, nil)
	_, err := svc.ResetSessionStorage(context.Background(), "wslc-cli-running")
	if err == nil {
		t.Fatal("expected an error while the session VM is running")
	}
	if _, statErr := os.Stat(target); statErr != nil {
		t.Errorf("the file must not be deleted when the session is running: %v", statErr)
	}
	if !strings.Contains(err.Error(), "正在运行") {
		t.Errorf("expected the error to name the running session, got %v", err)
	}
}

func TestResetSessionStorageMissingFileIsNotAnError(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	mustMkdirAll(t, filepath.Join(sessionsDir, "ghost"))

	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(fake, nil)
	out, err := svc.ResetSessionStorage(context.Background(), "ghost")
	if err != nil {
		t.Fatalf("expected no error when the file is already gone, got %v", err)
	}
	if !strings.Contains(out, "无需删除") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestResetSessionStorageRejectsTraversal(t *testing.T) {
	base := t.TempDir()
	t.Setenv("WSLC_STORAGE_PATH", base)
	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	svc := NewService(fake, nil)

	for _, name := range []string{"../escape", "..\\escape", "a/b", "..", "."} {
		if _, err := svc.ResetSessionStorage(context.Background(), name); err == nil {
			t.Errorf("ResetSessionStorage(%q): expected an error", name)
		}
	}
}

func TestShrinkSessionStorageInvokesDiskpart(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	target := filepath.Join(sessionsDir, "wslc-cli-dhshu", vhdxName)
	mustWriteFile(t, target, 5*1024*1024)

	tool := &storageFakeRunner{
		Faked:  &Faked{fake: wslc.NewFakeRunner().Default(wslc.Result{}, nil)},
		result: wslc.Result{Stdout: "磁盘压缩完成", ExitCode: 0},
	}
	tool.fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	tool.fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(tool, nil)
	out, err := svc.ShrinkSessionStorage(context.Background(), "wslc-cli-dhshu")
	if err != nil {
		t.Fatalf("ShrinkSessionStorage: %v", err)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("expected exactly one tool invocation, got %d", len(tool.calls))
	}
	call := tool.calls[0]
	if call.exe != "diskpart.exe" {
		t.Errorf("exe = %q, want diskpart.exe", call.exe)
	}
	if len(call.args) != 0 {
		t.Errorf("args = %v, want empty (diskpart reads its script from stdin)", call.args)
	}
	if call.dir != filepath.Dir(target) {
		t.Errorf("dir = %q, want %q", call.dir, filepath.Dir(target))
	}
	// The script must be the four fixed diskpart commands, in order, with the
	// absolute path embedded as one argument of select vdisk.
	want := "attach vdisk file=" + target + "\nselect vdisk 0\ncompact vdisk\ndetach vdisk\n"
	if call.stdin != want {
		t.Errorf("stdin mismatch:\ngot  %q\nwant %q", call.stdin, want)
	}
	for _, line := range []string{"attach vdisk", "select vdisk 0", "compact vdisk", "detach vdisk"} {
		if !strings.Contains(call.stdin, line) {
			t.Errorf("stdin is missing %q", line)
		}
	}
	if !strings.Contains(out, "压缩完成") || !strings.Contains(out, "磁盘压缩完成") {
		t.Errorf("unexpected output: %q", out)
	}
}

func TestShrinkSessionStorageRefusesRunningSession(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	mustWriteFile(t, filepath.Join(sessionsDir, "wslc-cli-running", vhdxName), 4096)

	tool := &storageFakeRunner{Faked: &Faked{fake: wslc.NewFakeRunner().Default(wslc.Result{}, nil)}}
	tool.fake.When(sessionArgs(), wslc.Result{Stdout: sessionListFixture("wslc-cli-running")}, nil)
	tool.fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(tool, nil)
	_, err := svc.ShrinkSessionStorage(context.Background(), "wslc-cli-running")
	if err == nil || !strings.Contains(err.Error(), "正在运行") {
		t.Fatalf("expected a running-session error, got %v", err)
	}
	if len(tool.calls) != 0 {
		t.Errorf("diskpart must not be invoked while the session is running: %+v", tool.calls)
	}
}

func TestShrinkSessionStorageReportsDiskpartFailure(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	mustWriteFile(t, filepath.Join(sessionsDir, "wslc-cli-dhshu", vhdxName), 4096)

	tool := &storageFakeRunner{
		Faked:  &Faked{fake: wslc.NewFakeRunner().Default(wslc.Result{}, nil)},
		result: wslc.Result{ExitCode: 1, Stderr: "必须提升权限"},
		err:    &wslc.ExitError{Code: 1, Stderr: "必须提升权限"},
	}
	tool.fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	tool.fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(tool, nil)
	_, err := svc.ShrinkSessionStorage(context.Background(), "wslc-cli-dhshu")
	if err == nil {
		t.Fatal("expected an error when diskpart fails")
	}
	if !strings.Contains(err.Error(), "必须提升权限") {
		t.Errorf("expected the diskpart stderr in the error, got %v", err)
	}
}

func TestShrinkSessionStorageWithoutToolRunner(t *testing.T) {
	base := t.TempDir()
	sessionsDir := filepath.Join(base, "wslc", "sessions")
	mustWriteFile(t, filepath.Join(sessionsDir, "wslc-cli-dhshu", vhdxName), 4096)

	// A plain FakeRunner implements Runner but not ToolRunner.
	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: ""}, nil)
	fake.When(infoArgs(), wslc.Result{Stdout: `{"Client":{}}`}, nil)
	t.Setenv("WSLC_STORAGE_PATH", base)

	svc := NewService(fake, nil)
	_, err := svc.ShrinkSessionStorage(context.Background(), "wslc-cli-dhshu")
	if err == nil {
		t.Fatal("expected an error when the runner cannot run external tools")
	}
	if !strings.Contains(err.Error(), "不支持执行外部工具") {
		t.Errorf("unexpected error: %v", err)
	}
}

// sessionListFixture renders the table wslc 3.0.1 prints for
// `system session list` for the given session names.
func sessionListFixture(names ...string) string {
	out := "ID   创建者 PID   显示名称\n"
	for i, name := range names {
		out += "2\t44220\t" + name + "\n"
		_ = i
	}
	return out
}

func mustMkdirAll(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}

// mustWriteFile creates a file with exactly size bytes (sparse write) so the
// tests stay fast even for multi-gigabyte sizes.
func mustWriteFile(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncate %s: %v", path, err)
	}
}

func mustWriteFileContent(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
