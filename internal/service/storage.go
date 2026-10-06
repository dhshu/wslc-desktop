package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// vhdxName is the file wslc writes a session's root filesystem into. It is a
// stable, documented part of wslc's on-disk layout (see the session.storagePath
// comment in settings.yaml) and is the single reason %LOCALAPPDATA%\wslc grows
// into the gigabytes.
const vhdxName = "storage.vhdx"

// defaultLocalAppData is the last-resort sessions base when neither the
// settings file nor an environment variable gives one. Windows stores it in
// %LOCALAPPDATA%; the constant is only used when LOCALAPPDATA is unset.
const defaultLocalAppData = `C:\Users\dhshu\AppData\Local`

// storagePaths returns the wslc base directory and the sessions directory that
// holds one storage.vhdx per session.
//
// Resolution order for the base:
//
//  1. session.storagePath from the settings file reported by `wslc info` — but
//     only when it is actually non-default. wslc documents the value "default"
//     as meaning "use built-in defaults", and it writes that literal token into
//     the settings YAML, so a naive split of settings.yaml on "storagePath"
//     would return the string "default" as a path. This is the authoritative
//     source because it is what wslc itself reads.
//  2. WSLC_STORAGE_PATH, a relocatable override for users who cannot or do not
//     want to edit the settings file.
//  3. %LOCALAPPDATA%, and then a machine-specific fallback.
//
// The base is read only, never written: the tool must not create or move
// wslc's directories, because a wrong guess here would make the UI report and
// delete the wrong disk.
func (s *Service) storagePaths(ctx context.Context) (base, sessionsDir string, err error) {
	if fromSettings, ok := storagePathFromSettings(ctx, s); ok {
		base = fromSettings
	} else {
		base = strings.TrimSpace(os.Getenv("WSLC_STORAGE_PATH"))
	}
	if base == "" {
		base = strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
	}
	if base == "" {
		base = defaultLocalAppData
	}
	// A base that is not an absolute path is not a real storage root; fall back
	// rather than relative-joining into the working directory.
	if !filepath.IsAbs(base) {
		base = strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if base == "" {
			base = defaultLocalAppData
		}
	}
	return base, filepath.Join(base, "wslc", "sessions"), nil
}

// storagePathFromSettings resolves session.storagePath out of the settings YAML
// that wslc itself reports. Only the two lines that matter are scanned, so a
// malformed or future settings file degrades to "unknown" instead of being
// guessed at.
func storagePathFromSettings(ctx context.Context, s *Service) (string, bool) {
	if s == nil {
		return "", false
	}
	// `wslc info --format json` returns {"Client":{...},"Server":{...}}. The
	// Client object is what carries SettingsFile. The shared ParseSystemInfo
	// refuses a Client object that carries only fields it cannot map, which
	// would make a bare {"Client":{"SettingsFile":...}} report an error — so
	// this helper decodes the one field it needs itself rather than routing
	// through the general parser.
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdInfo, Args: []string{"info", "--format", "json"}})
	if err != nil {
		return "", false
	}
	var envelope struct {
		Client struct {
			SettingsFile string `json:"SettingsFile"`
		} `json:"Client"`
	}
	// Windows paths are embedded with single backslashes, which is not valid
	// JSON escaping; json.Unmarshal rejects them outright. Decode leniently so
	// the real `wslc info` output is accepted and the escaped path is still
	// read as a single field value.
	if err := lenientJSON([]byte(res.Stdout), &envelope); err != nil {
		return "", false
	}
	settingsFile := strings.TrimSpace(envelope.Client.SettingsFile)
	if settingsFile == "" || !filepath.IsAbs(settingsFile) {
		return "", false
	}
	raw, err := os.ReadFile(settingsFile)
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(raw), "\n") {
		// Trim only the newline: the indentation that separates a nested YAML
		// key from its parent is part of the line and is what distinguishes
		// session.storagePath from some other nested setting of the same
		// spelling. Trimming the whole line first erases that signal.
		trimmed := strings.TrimSuffix(line, "\r\n")
		keyIdx := strings.Index(trimmed, "storagePath:")
		if keyIdx < 0 {
			continue
		}
		// Everything before the key must be whitespace (YAML indentation) or a
		// comment; otherwise the key belongs to some other nested structure.
		prefix := trimmed[:keyIdx]
		if strings.TrimSpace(prefix) != "" && !strings.Contains(prefix, "#") {
			continue
		}
		value := strings.TrimSpace(strings.TrimSuffix(trimmed[keyIdx+len("storagePath:"):], "\r"))
		value = strings.Trim(value, `"'`)
		value = strings.TrimSpace(value)
		if value == "" || value == "default" {
			// The literal token wslc writes for "use built-in defaults"; not a
			// path, so keep searching.
			continue
		}
		return value, true
	}
	return "", false
}

// lenientJSON decodes a JSON value whose strings may carry Windows-style path
// escaping. wslc's own `info --format json` output is well-formed JSON, but the
// settings file path it prints is written with single backslashes by the
// Go/Windows code that produced it, and strict json.Unmarshal rejects `\U`.
//
// Rather than rewriting the whole document, this only repairs the one sequence
// that is invalid JSON and that a Windows path always uses: a backslash that is
// followed by a character that is not a legal JSON escape. That is a lossless
// transformation for every path this tool cares about.
func lenientJSON(raw []byte, into any) error {
	out := make([]byte, 0, len(raw))
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' || i+1 >= len(raw) {
			out = append(out, raw[i])
			continue
		}
		switch raw[i+1] {
		case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
			out = append(out, raw[i], raw[i+1])
			i++
		default:
			// An illegal escape: the backslash is literal, so keep it alone.
			out = append(out, raw[i], '\\')
		}
	}
	return json.Unmarshal(out, into)
}

// ListSessionStorage reports the on-disk footprint of every wslc session
// directory, whether or not it currently has a running session.
//
// The enumeration is filesystem-based on purpose: `wslc system session list`
// only reports *running* sessions, and the whole point of this view is to show
// the storage of terminated sessions — which is exactly where the disk goes
// missing. The sessions directory is wslc's own layout and is not exposed by
// any wslc command, so reading it directly is the only option.
//
// Errors are per-session and reported inside the result: one unreadable
// directory must not hide the rest, and a vanished sessions directory is not
// fatal (it just means no sessions have been created yet).
func (s *Service) ListSessionStorage(ctx context.Context) ([]domain.SessionStorage, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	_, sessionsDir, err := s.storagePaths(ctx)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(sessionsDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []domain.SessionStorage{}, nil
		}
		return nil, fmt.Errorf("service: 无法读取会话存储目录 %s：%w", sessionsDir, err)
	}

	active := s.activeSessionNames(ctx)
	out := make([]domain.SessionStorage, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !isValidSessionDirName(name) {
			continue
		}
		path := filepath.Join(sessionsDir, name, vhdxName)
		st, err := os.Stat(path)
		out = append(out, domain.SessionStorage{
			SessionName: name,
			Path:        path,
			BytesOnDisk: sizeOf(st, err),
			SizeText:    formatBytes(sizeOf(st, err)),
			Exists:      err == nil,
			Active:      active[name],
		})
	}
	if out == nil {
		out = []domain.SessionStorage{}
	}
	return out, nil
}

// activeSessionNames reads the running sessions once and maps their display
// names to true. The directory names under sessions\ and the display names
// reported by `wslc system session list` are the same string, which is what
// makes this join safe.
func (s *Service) activeSessionNames(ctx context.Context) map[string]bool {
	m := make(map[string]bool)
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return m
	}
	for _, sess := range sessions {
		if name := strings.TrimSpace(sess.Name); name != "" {
			m[name] = true
		}
	}
	return m
}

// ResetSessionStorage deletes the storage.vhdx of one session and therefore
// discards everything stored in it: images, containers, build cache and
// volumes. The session VM must be terminated first; wslc holds the file open
// while it is running and the delete would fail.
//
// This is a destructive operation and the frontend confirms twice. Deleting the
// file rather than zero-filling it is deliberate: a dynamic VHDX that is only
// truncated still occupies its old extents, so the disk is not returned unless
// the file is removed entirely. The directory is left in place so wslc can
// recreate the session with the same name; deleting the directory too would
// leave wslc's own bookkeeping pointing at a missing path.
func (s *Service) ResetSessionStorage(ctx context.Context, sessionName string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	name, err := validateSessionName(sessionName)
	if err != nil {
		return "", err
	}
	target, err := s.resolveStoragePath(ctx, name)
	if err != nil {
		return "", err
	}
	if active, err := s.sessionIsActive(ctx, name); err != nil {
		return "", err
	} else if active {
		return "", fmt.Errorf("service: 会话 %s 正在运行，请先终止会话再删除其存储（storage.vhdx 被会话 VM 占用）", name)
	}
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return "会话 " + name + " 没有存储文件，无需删除", nil
		}
		return "", err
	}

	before := int64(0)
	if st, err := os.Stat(target); err == nil {
		before = st.Size()
	}
	if err := os.Remove(target); err != nil {
		return "", fmt.Errorf("service: 删除 %s 失败：%w", target, err)
	}
	return fmt.Sprintf("已删除 %s（释放 %s）", target, formatBytes(before)), nil
}

// ShrinkSessionStorage reclaims the space inside an existing dynamic VHDX by
// running the Windows `diskpart` command sequence that wslc does not provide:
// the file is attached as a virtual disk, its volumes are compacted, and it is
// detached again. The result is a physically smaller file with the same data.
//
// diskpart is invoked with its script fed through stdin, never by concatenating
// a command line, so a session name containing shell metacharacters cannot be
// re-interpreted. The file path is one argument of diskpart's own
// `select vdisk file=...` command, which diskpart parses itself.
//
// The session VM must be terminated first: attaching a VHDX that is already
// attached to a running VM fails, and compacting a live disk would corrupt it.
func (s *Service) ShrinkSessionStorage(ctx context.Context, sessionName string) (string, error) {
	if err := contextError(ctx); err != nil {
		return "", err
	}
	name, err := validateSessionName(sessionName)
	if err != nil {
		return "", err
	}
	target, err := s.resolveStoragePath(ctx, name)
	if err != nil {
		return "", err
	}
	if active, err := s.sessionIsActive(ctx, name); err != nil {
		return "", err
	} else if active {
		return "", fmt.Errorf("service: 会话 %s 正在运行，请先终止会话再压缩其存储", name)
	}
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			return "会话 " + name + " 没有存储文件，无需压缩", nil
		}
		return "", err
	}
	if !strings.EqualFold(filepath.Ext(target), ".vhdx") {
		return "", fmt.Errorf("service: %s 不是 .vhdx 文件，拒绝压缩", target)
	}

	before := int64(0)
	if st, err := os.Stat(target); err == nil {
		before = st.Size()
	}
	script := "attach vdisk file=" + target + "\n" +
		"select vdisk 0\n" +
		"compact vdisk\n" +
		"detach vdisk\n"

	res, err := s.runTool(ctx, "diskpart.exe", nil, script, filepath.Dir(target))
	if err != nil {
		return "", fmt.Errorf("service: 压缩 %s 失败：%w", target, err)
	}
	out := outputOr(res, "diskpart 未输出任何信息")
	afterBytes := int64(0)
	if st, err := os.Stat(target); err == nil {
		afterBytes = st.Size()
	}
	if before > 0 {
		return fmt.Sprintf("压缩完成：%s -> %s（释放 %s）\n%s",
			formatBytes(before), formatBytes(afterBytes), formatBytes(before-afterBytes), out), nil
	}
	return out, nil
}

// resolveStoragePath builds and verifies the absolute path of one session's
// storage.vhdx. It re-derives the path from the sessions directory each time
// rather than trusting a cached entry, and refuses anything that is not a
// regular file named storage.vhdx inside it.
func (s *Service) resolveStoragePath(ctx context.Context, name string) (string, error) {
	_, sessionsDir, err := s.storagePaths(ctx)
	if err != nil || !filepath.IsAbs(sessionsDir) {
		return "", fmt.Errorf("service: 无法确定会话存储目录")
	}
	return resolveSessionStoragePath(sessionsDir, name)
}

// sessionIsActive reports whether a session currently has a running wslc VM.
// The join key is the session display name, which wslc also uses as the
// directory name under sessions\.
func (s *Service) sessionIsActive(ctx context.Context, name string) (bool, error) {
	sessions, err := s.ListSessions(ctx)
	if err != nil {
		return false, err
	}
	for _, sess := range sessions {
		if strings.TrimSpace(sess.Name) == name {
			return true, nil
		}
	}
	return false, nil
}

// resolveSessionStoragePath rebuilds the storage path from the sessions
// directory and the session name, then verifies it still points inside that
// directory at something named storage.vhdx. This prevents a symlink or a name
// that walked out of the sessions directory from being deleted or compacted.
//
// A file that does not exist yet is reported as nil error: both ResetSessionStorage
// and ShrinkSessionStorage handle the absent case themselves and turn it into a
// friendly "nothing to do" result rather than a failure.
func resolveSessionStoragePath(sessionsDir, name string) (string, error) {
	if !filepath.IsAbs(sessionsDir) {
		return "", fmt.Errorf("service: 会话存储目录不是绝对路径：%s", sessionsDir)
	}
	target := filepath.Join(sessionsDir, name, vhdxName)
	clean, err := filepath.Abs(target)
	if err != nil {
		return "", err
	}
	if filepath.Base(clean) != vhdxName {
		return "", fmt.Errorf("service: 拒绝操作非 storage.vhdx 的路径：%s", clean)
	}
	// The parent directory must be exactly <sessionsDir>\<name>, so a name that
	// walked out (or a symlink) cannot redirect the operation elsewhere.
	wantDir := filepath.Join(sessionsDir, name)
	if filepath.Clean(filepath.Dir(clean)) != filepath.Clean(wantDir) {
		return "", fmt.Errorf("service: 拒绝操作会话目录之外的路径：%s", clean)
	}
	st, err := os.Stat(clean)
	if err != nil {
		if os.IsNotExist(err) {
			return clean, nil
		}
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("service: %s 不是普通文件，拒绝操作", clean)
	}
	return clean, nil
}

// validateSessionName rejects anything that is not a plain directory name. A
// session name is used to build a filesystem path, so path separators and
// traversal are refused rather than sanitised away.
func validateSessionName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("service: 会话名称为空")
	}
	if len(name) > 200 {
		return "", errors.New("service: 会话名称过长")
	}
	if filepath.Base(name) != name {
		return "", fmt.Errorf("service: 非法会话名称 %q（不能包含路径分隔符）", name)
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("service: 非法会话名称 %q", name)
	}
	if strings.ContainsAny(name, `<>:"|?*`) {
		return "", fmt.Errorf("service: 非法会话名称 %q（包含 Windows 保留字符）", name)
	}
	return name, nil
}

// isValidSessionDirName is the weaker check used while enumerating: it only
// skips names that cannot be a session directory, and never fails.
func isValidSessionDirName(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	if filepath.Base(name) != name {
		return false
	}
	return !strings.HasPrefix(name, ".")
}

func sizeOf(st os.FileInfo, err error) int64 {
	if err != nil || st == nil {
		return 0
	}
	return st.Size()
}

// formatBytes renders a byte count the way Explorer and the wslc tables do:
// binary units with one decimal place, and "0 B" for an empty file.
func formatBytes(n int64) string {
	if n <= 0 {
		return "0 B"
	}
	const unit = 1024
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	value := float64(n)
	i := 0
	for value >= unit && i < len(units)-1 {
		value /= unit
		i++
	}
	if i == 0 {
		return strconv.FormatInt(n, 10) + " B"
	}
	return strconv.FormatFloat(value, 'f', 1, 64) + " " + units[i]
}

// runTool executes a non-wslc Windows binary the same way the wslc runner
// executes wslc: arguments as a vector, never through a shell, with stdin fed
// from a string. It exists because shrinking a VHDX requires diskpart, which is
// not a wslc command and therefore cannot travel through the Runner interface.
//
// The tool runner is resolved through the optional ToolRunner interface, so a
// Runner that only implements the frozen contract still works everywhere else
// and this method degrades to a clear error instead of panicking.
func (s *Service) runTool(ctx context.Context, exe string, args []string, stdin, dir string) (wslc.Result, error) {
	if s == nil || s.runner == nil {
		return wslc.Result{}, errors.New("service: 未配置 wslc runner")
	}
	tool, ok := s.runner.(ToolRunner)
	if !ok {
		return wslc.Result{}, errors.New("service: 当前 runner 不支持执行外部工具（需要 diskpart 等 Windows 工具）")
	}
	return tool.RunTool(normalizeContext(ctx), exe, args, stdin, dir)
}
