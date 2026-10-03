package service

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// The service layer validates user input for two reasons:
//
//  1. a malformed flag value must fail with a readable message instead of a
//     localized wslc parse error;
//  2. a user-supplied string that becomes a positional argument must not be
//     silently re-interpreted as an option, because the runner passes arguments
//     as a vector (no shell) but wslc's own parser still sees them.
//
// Validation deliberately stays permissive for *references* (an existing
// container may legitimately carry an unusual name) and strict for names we are
// about to create.

var (
	memoryPattern = regexp.MustCompile(`(?i)^[0-9]+(\.[0-9]+)?(b|k|kb|kib|m|mb|mib|g|gb|gib|t|tb|tib)?$`)
	cpuPattern    = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	signalPattern = regexp.MustCompile(`^[A-Za-z0-9_+-]+$`)
)

// containerStates are the states ContainerFilter.State accepts. "running" maps
// to the container's live state, "exited"/"stopped" to anything not running.
var containerStates = []string{"running", "exited", "created", "paused", "restarting", "removing", "dead", "stopped"}

// requireRef validates a caller-supplied reference that becomes a positional
// argument (container, image, volume, network id or name).
//
// The value is returned unchanged so OutputEvent.Ref can echo it verbatim.
func requireRef(what, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("service: %s 不能为空", what)
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("service: %s 首尾不能有空白: %q", what, value)
	}
	if strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("service: %s 不能以 '-' 开头（会被 wslc 当作选项解析）: %q", what, value)
	}
	if strings.ContainsAny(value, " \t\r\n\x00") {
		return "", fmt.Errorf("service: %s 不能包含空白或控制字符: %q", what, value)
	}
	return value, nil
}

// requireName validates a name the user is about to create (container, volume,
// network, hostname). wslc/Docker accept letters, digits, '_', '.' and '-'.
func requireName(what, value string) (string, error) {
	v, err := requireRef(what, value)
	if err != nil {
		return "", err
	}
	if len(v) > 128 {
		return "", fmt.Errorf("service: %s 过长（最多 128 字符）", what)
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case (c == '_' || c == '.' || c == '-') && i > 0:
		default:
			return "", fmt.Errorf("service: %s 含非法字符 %q（允许字母、数字、'_'、'.'、'-'，且不能以符号开头）", what, string(c))
		}
	}
	return v, nil
}

// requireText validates a free-form value that becomes a flag value.
func requireText(what, value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("service: %s 不能为空", what)
	}
	if strings.TrimSpace(value) != value {
		return "", fmt.Errorf("service: %s 首尾不能有空白: %q", what, value)
	}
	if strings.HasPrefix(value, "-") {
		return "", fmt.Errorf("service: %s 不能以 '-' 开头: %q", what, value)
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("service: %s 含有非法控制字符", what)
	}
	return value, nil
}

// validateDriver accepts an empty driver (wslc uses its default) or a plain name.
func validateDriver(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if strings.HasPrefix(v, "-") || strings.ContainsAny(v, " \t\r\n\x00") {
		return "", fmt.Errorf("service: 驱动名非法: %q", value)
	}
	return v, nil
}

// validateTimeoutSec renders a stop/restart timeout flag value.
//
// 0 means "let wslc use its configured default" and produces no flag at all.
func validateTimeoutSec(what string, seconds int) (string, error) {
	if seconds < 0 {
		return "", fmt.Errorf("service: %s 不能为负数（%d）；0 表示使用 wslc 默认值", what, seconds)
	}
	if seconds == 0 {
		return "", nil
	}
	return strconv.Itoa(seconds), nil
}

// validateSignal accepts an empty signal (wslc defaults to SIGKILL) or a signal
// name/number such as SIGTERM or 9.
//
// The value is trimmed and the *trimmed* form is returned: the caller should
// use the result of this call when building argv, not the original string.
// Without the trim a caller that forgot to use the return value would forward
// " SIGKILL " straight to wslc, where a leading space makes the value
// indistinguishable from an option-looking token.
func validateSignal(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	// Reject any value that starts with '-' after trimming: a signal name is
	// never an option, and passing "--force" as a signal would otherwise let
	// the caller smuggle an arbitrary wslc option into the kill command.
	if strings.HasPrefix(v, "-") {
		return "", fmt.Errorf("service: 信号非法: %q（不能以 - 开头）", value)
	}
	if !signalPattern.MatchString(v) {
		return "", fmt.Errorf("service: 信号非法: %q（示例：SIGKILL、SIGTERM、9）", value)
	}
	return v, nil
}

// validateMemory accepts a size such as 512M, 1G, 512 (bytes).
func validateMemory(value string) error {
	if !memoryPattern.MatchString(value) {
		return fmt.Errorf("service: 内存限制格式非法: %q（示例：512M、1G）", value)
	}
	return nil
}

// validateCPUs accepts a decimal CPU count such as 0.5, 1 or 2.5.
func validateCPUs(value string) error {
	if !cpuPattern.MatchString(value) {
		return fmt.Errorf("service: CPU 数量格式非法: %q（示例：0.5、1、2.5）", value)
	}
	return nil
}

// validatePullPolicy accepts empty (wslc default) or always|missing|never.
func validatePullPolicy(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "", "always", "missing", "never":
		return v, nil
	default:
		return "", fmt.Errorf("service: --pull 只能是 always、missing 或 never，得到 %q", value)
	}
}

// validateProgress accepts empty (wslc default) or auto|tty|plain|quiet.
func validateProgress(value string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(value))
	switch v {
	case "", "auto", "tty", "plain", "quiet":
		return v, nil
	default:
		return "", fmt.Errorf("service: --progress 只能是 auto、tty、plain 或 quiet，得到 %q", value)
	}
}

// validateKeyValue accepts a KEY=VALUE pair (environment variable, label,
// build arg) and rejects anything that could be read as an option.
func validateKeyValue(what, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", fmt.Errorf("service: %s 不能为空", what)
	}
	if strings.HasPrefix(v, "-") {
		return "", fmt.Errorf("service: %s 不能以 '-' 开头: %q", what, value)
	}
	key, _, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return "", fmt.Errorf("service: %s 需要 KEY=VALUE 形式: %q", what, value)
	}
	if strings.ContainsAny(v, "\x00\r\n") {
		return "", fmt.Errorf("service: %s 含有非法控制字符", what)
	}
	return v, nil
}

// validatePort accepts "80", "8080:80" or "127.0.0.1:8080:80".
func validatePort(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("service: 端口映射不能为空")
	}
	if strings.HasPrefix(v, "-") || strings.ContainsAny(v, " \t\r\n\x00") {
		return "", fmt.Errorf("service: 端口映射非法: %q", value)
	}
	host, container, ok := strings.Cut(v, ":")
	if !ok || strings.TrimSpace(host) == "" || strings.TrimSpace(container) == "" {
		return "", fmt.Errorf("service: 端口映射需要 host:container 形式: %q", value)
	}
	return v, nil
}

// validateMount accepts "volume:/path", "C:\\dir:/path" or a bind with a mode
// suffix such as "vol:/path:ro".
func validateMount(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", errors.New("service: 卷映射不能为空")
	}
	if strings.HasPrefix(v, "-") || strings.ContainsAny(v, " \t\r\n\x00") {
		return "", fmt.Errorf("service: 卷映射非法: %q", value)
	}
	source, target, ok := strings.Cut(v, ":")
	if !ok || strings.TrimSpace(source) == "" || strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("service: 卷映射需要 source:target 形式: %q", value)
	}
	// A mode suffix ("vol:/path:ro") or a Windows drive letter ("C:\\x:/path")
	// both put additional colons on either side; requiring a non-empty target
	// after the *last* colon catches the common typos without guessing wslc's
	// full grammar.
	if strings.HasSuffix(v, ":") {
		return "", fmt.Errorf("service: 卷映射缺少容器内路径: %q", value)
	}
	return v, nil
}

// validateTimeValue accepts the since/until grammar (Unix epoch seconds or
// RFC3339), which never contains whitespace.
func validateTimeValue(what, value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if strings.HasPrefix(v, "-") || strings.ContainsAny(v, " \t\r\n\x00") {
		return "", fmt.Errorf("service: %s 非法: %q（应为 Unix 秒或 RFC3339 时间戳）", what, value)
	}
	return v, nil
}

// validateSubnet accepts an empty subnet or a CIDR block.
func validateSubnet(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if strings.HasPrefix(v, "-") || strings.ContainsAny(v, " \t\r\n\x00") {
		return "", fmt.Errorf("service: 子网非法: %q", value)
	}
	if _, _, err := net.ParseCIDR(v); err != nil {
		return "", fmt.Errorf("service: 子网需要 CIDR 形式（例如 172.20.0.0/16）: %q", value)
	}
	return v, nil
}

// validateGateway accepts an empty gateway or an IPv4/IPv6 address.
func validateGateway(value string) (string, error) {
	v := strings.TrimSpace(value)
	if v == "" {
		return "", nil
	}
	if net.ParseIP(v) == nil {
		return "", fmt.Errorf("service: 网关不是合法 IP: %q", value)
	}
	return v, nil
}

// validateContainerFilter checks the client-side list filter.
func validateContainerFilter(f ContainerFilter) error {
	if f.Limit < 0 {
		return fmt.Errorf("service: Limit 不能为负数（%d）；0 表示不限制", f.Limit)
	}
	state := strings.ToLower(strings.TrimSpace(f.State))
	if state == "" {
		return nil
	}
	for _, known := range containerStates {
		if state == known {
			return nil
		}
	}
	return fmt.Errorf("service: 未知的容器状态 %q（可用：%s）", f.State, strings.Join(containerStates, ", "))
}
