package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// Image export / import uses `wslc image save -o FILE img` and
// `wslc image load -i FILE`. They are the pair that behaves like
// docker save / docker load: they preserve layers, tags and manifests so an
// image round-trips through a tar file unchanged. wslc's top-level `import` /
// `export` aliases are a different shape (import turns a raw tarball into a
// root-filesystem image and there is no `export`), so those are not what the
// desktop UI wires up.
//
// Both commands can take minutes for a large image. They run through s.run
// with the runner's default timeout, which is generous (60s) and covers
// typical desktop-sized images; a caller that needs more can raise it via
// ExecRunner.WithTimeout at wiring time. The service layer deliberately does
// not set an explicit timeout here: -1 would disable it entirely, and any
// finite smaller value than the default would be strictly worse than the
// default.

// ExportImage saves an image to a tar file at dstPath.
//
// It refuses to write over an existing file unless overwrite is true: the
// frontend already asks for confirmation before calling this, so a second
// guard here catches the case where the user typed a path that already
// exists and simply wants to refresh the export.
func (s *Service) ExportImage(ctx context.Context, ref, dstPath string, overwrite bool) (string, error) {
	target, err := requireRef("镜像引用", ref)
	if err != nil {
		return "", err
	}
	dst, err := validateFilePath("导出路径", dstPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(dst); err == nil && !overwrite {
		return "", errors.New("service: 目标文件已存在，如需覆盖请重新确认: " + dst)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return "", err
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdImageSave, Args: []string{"image", "save", "-o", dst, target}})
	if err != nil {
		// A failed save may leave a partially written file; remove it so the
		// next attempt does not start with stale bytes.
		if _, serr := os.Stat(dst); serr == nil {
			_ = os.Remove(dst)
		}
		return "", err
	}
	return outputOr(res, "已导出镜像 "+target+" -> "+dst), nil
}

// ImportImage loads an image from a tar file at srcPath.
//
// A missing file is reported as a friendly error instead of forwarding to
// wslc, because wslc's own message is localised and less actionable than
// telling the user the path they picked does not exist.
func (s *Service) ImportImage(ctx context.Context, srcPath string) (string, error) {
	src, err := validateFilePath("导入路径", srcPath)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(src); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", errors.New("service: 文件不存在: " + src)
		}
		return "", err
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdImageLoad, Args: []string{"image", "load", "-i", src}})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已从 "+src+" 导入镜像"), nil
}

// validateFilePath normalises and checks a user-supplied filesystem path that
// becomes a positional argument to a wslc command.
//
// The value must be non-empty, free of control characters and either absolute
// or resolve to an absolute path through the current working directory: wslc
// does not document what a relative path means for `image save -o`, and the
// desktop app always wants the file where the user picked it. Relative input
// is rejected so a stray "C:\x.tar" cannot be interpreted relative to the
// service process's cwd.
func validateFilePath(what, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("service: " + what + " 不能为空")
	}
	if strings.ContainsAny(v, "\x00\r\n") {
		return "", errors.New("service: " + what + " 含有非法控制字符")
	}
	// Strip a stray pair of surrounding quotes: users paste paths like
	// "C:\x.tar" directly from the Explorer address bar.
	v = strings.Trim(v, `"`)
	if !filepath.IsAbs(v) {
		return "", errors.New("service: " + what + " 必须是绝对路径: " + value)
	}
	if err := validatePathComponents(what, v); err != nil {
		return "", err
	}
	return filepath.Clean(v), nil
}

// validatePathComponents rejects the small set of path shapes that could
// surprise a user: a drive-relative path on Windows, a UNC share (whose
// credentials are not visible in the UI), or a path with an empty component.
func validatePathComponents(what, value string) error {
	// Reject UNC paths (\\server\share): wslc may still write to them, but the
	// desktop UI has no way to surface the credentials that would be needed.
	if strings.HasPrefix(value, `\\`) || strings.HasPrefix(value, `//`) {
		return errors.New("service: " + what + " 不支持 UNC 路径: " + value)
	}
	cleaned := filepath.Clean(value)
	for _, part := range strings.FieldsFunc(cleaned, func(r rune) bool { return r == '\\' || r == '/' }) {
		if part == ".." || part == "." {
			return errors.New("service: " + what + " 含可疑的相对片段: " + value)
		}
	}
	return nil
}
