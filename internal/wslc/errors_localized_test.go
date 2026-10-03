package wslc

import (
	"errors"
	"testing"
)

// TestExitErrorUnwrapLocalized covers the Simplified Chinese wordings that wslc
// emits on a zh-CN Windows install. These were real classification gaps: the
// sentinel mapping previously only matched English text.
func TestExitErrorUnwrapLocalized(t *testing.T) {
	tests := []struct {
		name   string
		stderr string
		want   error
	}{
		{
			name:   "zh-CN unrecognized container prune",
			stderr: "无法识别的命令:“prune”",
			want:   ErrUnsupportedCommand,
		},
		{
			name:   "zh-CN container not found",
			stderr: "找不到容器 'web'。",
			want:   ErrNotFound,
		},
		{
			name:   "zh-CN volume not found",
			stderr: "找不到卷: 'data'",
			want:   ErrNotFound,
		},
		{
			name:   "zh-CN image not found",
			stderr: "找不到映像 'nginx'",
			want:   ErrNotFound,
		},
		{
			name:   "zh-CN service unavailable",
			stderr: "由于未安装所需的特性，无法启动操作。 \n错误代码： HCS_E_SERVICE_NOT_AVAILABLE",
			want:   ErrServiceUnavailable,
		},
		{
			name:   "zh-CN required feature not installed",
			stderr: "未安装所需的特性",
			want:   ErrServiceUnavailable,
		},
		{
			name:   "english not found still works",
			stderr: "Error: No such container: web",
			want:   ErrNotFound,
		},
		{
			name:   "english unknown flag still works",
			stderr: "unknown flag: --nope",
			want:   ErrUnsupportedCommand,
		},
		{
			name:   "unclassified error returns nil",
			stderr: "some completely unrelated failure",
			want:   nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := &ExitError{Code: 1, Args: []string{"container", "list"}, Stderr: tc.stderr}
			got := errors.Unwrap(err)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("errors.Unwrap() = %v, want nil", got)
				}
				return
			}
			if !errors.Is(got, tc.want) {
				t.Fatalf("errors.Unwrap() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestExitErrorIsThroughWrapper guards that errors.Is works when the ExitError
// is wrapped by an upper layer, which is how the service layer returns failures.
func TestExitErrorIsThroughWrapper(t *testing.T) {
	inner := &ExitError{Code: 1, Args: []string{"list"}, Stderr: "找不到容器 'web'。"}
	wrapped := errors.Join(errors.New("listing containers"), inner)

	if !errors.Is(wrapped, ErrNotFound) {
		t.Fatalf("errors.Is(wrapped, ErrNotFound) = false, want true")
	}
}

// TestExitErrorMessageFallsBackToStdout ensures the human-readable message is
// still useful when wslc writes the failure to stdout instead of stderr.
func TestExitErrorMessageFallsBackToStdout(t *testing.T) {
	err := &ExitError{Code: 125, Args: []string{"run", "alpine"}, Stdout: "boom\nmore"}
	if got, want := err.Error(), "wslc run alpine: exit code 125: boom"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}

	empty := &ExitError{Code: 7, Args: []string{"ps"}}
	if got, want := empty.Error(), "wslc ps: exit code 7: no output"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
