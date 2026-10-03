package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// Problem texts are user-facing and must stay actionable: every one of them
// names the concrete command or setting that fixes the state. They are written
// for the three states EnvCheck distinguishes:
//
//  1. wslc.exe missing            -> ErrExecutableNotFound
//  2. wslc runs, service is down  -> ErrServiceUnavailable (vmcompute stopped)
//  3. everything works            -> Problems stays empty
const (
	problemExecutableNotFound = "未找到 wslc.exe：容器功能不可用。修复：先升级 WSL（wsl --update；wslc 需要 WSL 2.9.3+，本机已知位置 C:\\Program Files\\WSL\\wslc.exe）；" +
		"若已安装，请将环境变量 WSLC_PATH 指向 wslc.exe 后重启应用。"

	problemServiceUnavailable = "容器服务不可用（HCS_E_SERVICE_NOT_AVAILABLE / 0x80370114）：hypervisor 主机计算服务 vmcompute 处于 Stopped 或 Disabled。" +
		"修复（需要管理员 PowerShell）：Set-Service -Name vmcompute -StartupType Manual; Start-Service vmcompute; Get-Service vmcompute（期望 Running）。" +
		"若仍不可用：确认可选功能 VirtualMachinePlatform 已启用（Enable-WindowsOptionalFeature -Online -FeatureName VirtualMachinePlatform -All -NoRestart，必要时重启），" +
		"并检查 bcdedit /enum 中 hypervisorlaunchtype 为 Auto。不需要安装 WSL 发行版，也不需要 Containers / 完整 Hyper-V 角色。"
)

// EnvCheck probes the machine and reports whether wslc, and the container
// service it needs, are usable.
//
// A degraded result is returned with a nil error on purpose: the frontend
// renders EnvStatus.Problems, and Wails turns a non-nil error into a rejected
// promise, which would leave the environment view empty. Only a cancelled
// caller context is reported as an error.
func (s *Service) EnvCheck(ctx context.Context) (EnvStatus, error) {
	status := EnvStatus{
		Sessions:  []domain.Session{},
		Problems:  []string{},
		CheckedAt: s.clock(),
	}
	if s == nil || s.runner == nil {
		status.Problems = append(status.Problems, "未配置 wslc runner：无法执行任何 wslc 命令。")
		return status, nil
	}
	// The path comes from the runner itself (the adapter's ExecRunner exposes
	// Exe), never from a fresh filesystem search, so the reported path is the
	// one actually being executed.
	status.WslcPath = s.runnerExe()

	note := func(prefix string, err error) {
		if errors.Is(err, wslc.ErrExecutableNotFound) {
			status.Available = false
		}
		status.Problems = append(status.Problems, prefix+oneLine(err))
	}

	if err := s.runner.Available(normalizeContext(ctx)); err != nil {
		status.Available = false
		status.Problems = append(status.Problems, describeAvailability(err))
		return status, contextError(ctx)
	}
	status.Available = true

	// `wslc version` only talks to the IPC endpoint, so it works even while the
	// container service is down — which is exactly what makes it a good probe.
	if version, err := s.versionInfo(ctx); err != nil {
		note("无法读取 wslc 版本：", err)
	} else {
		status.WslcVersion = version
	}

	// `wslc info` reports the WSL/kernel versions, the settings file and the
	// session manager's session list.
	if info, err := s.systemInfo(ctx); err != nil {
		note("无法读取 wslc info：", err)
	} else {
		status.WSLVersion = info.Client.Version
		status.KernelVersion = info.Client.KernelVersion
		status.SettingsFile = info.Client.SettingsFile
	}

	// Sessions are read through the table parser: `system session list` is the
	// one list command that rejects --format.
	if sessions, err := s.sessions(ctx); err != nil {
		note("无法读取会话列表：", err)
	} else {
		status.Sessions = sessions
	}

	// Container-class command: this is the only step that needs vmcompute.
	switch err := s.probeContainerService(ctx); {
	case err == nil:
		status.ServiceReady = true
	case errors.Is(err, wslc.ErrServiceUnavailable):
		status.Problems = append(status.Problems, problemServiceUnavailable+"（原始错误："+oneLine(err)+"）")
	case errors.Is(err, wslc.ErrExecutableNotFound):
		status.Available = false
		status.Problems = append(status.Problems, problemExecutableNotFound)
	case errors.Is(err, wslc.ErrUnsupportedCommand):
		status.Problems = append(status.Problems, "当前 wslc 不支持 `container list --format json`："+oneLine(err)+"；请升级 WSL（wsl --update）。")
	default:
		status.Problems = append(status.Problems, "容器服务探测失败："+oneLine(err))
	}

	// Only worth probing when the container service is up: otherwise the user
	// will fix the bigger problem first and never see the pull hint.
	if status.ServiceReady {
		status.PullTip = s.probePullReachability(ctx)
	}

	// Report the mirror currently in effect so the environment view and the
	// settings view can never disagree about what is actually active.
	status.ActiveMirror = s.activeMirror()
	status.SettingsPath = s.settingsPath()

	return status, contextError(ctx)
}

// describeAvailability turns a failed Available probe into an actionable line.
func describeAvailability(err error) string {
	switch {
	case errors.Is(err, wslc.ErrExecutableNotFound):
		return problemExecutableNotFound
	case errors.Is(err, wslc.ErrServiceUnavailable):
		return problemServiceUnavailable + "（原始错误：" + oneLine(err) + "）"
	default:
		return "wslc 无法执行：" + oneLine(err) + "。修复：检查环境变量 WSLC_PATH 是否指向 wslc.exe，以及当前用户是否有执行权限。"
	}
}

// versionInfo reads the client version reported by `wslc version --format json`.
//
// That JSON is `{"Client":{"Version":"…"}}`, the same shape ParseSystemInfo
// already understands, so no version-specific parser is needed.
func (s *Service) versionInfo(ctx context.Context) (string, error) {
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdVersion, Args: []string{"version", "--format", "json"}})
	if err != nil {
		return "", err
	}
	info, err := wslc.ParseSystemInfo(res.Stdout)
	if err != nil {
		return "", err
	}
	return info.Client.Version, nil
}

// systemInfo reads `wslc info --format json`.
func (s *Service) systemInfo(ctx context.Context) (domain.SystemInfo, error) {
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdInfo, Args: []string{"info", "--format", "json"}})
	if err != nil {
		return domain.SystemInfo{}, err
	}
	return wslc.ParseSystemInfo(res.Stdout)
}

// sessions reads `wslc system session list`, which has no --format option.
func (s *Service) sessions(ctx context.Context) ([]domain.Session, error) {
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdSessionList, Args: []string{"system", "session", "list"}})
	if err != nil {
		return nil, err
	}
	return wslc.ParseSessions(res.Stdout)
}

// probeContainerService runs the cheapest container-class command there is.
func (s *Service) probeContainerService(ctx context.Context) error {
	_, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerList, Args: []string{"container", "list", "--format", "json"}})
	return err
}

// probePullReachability checks whether the machine can reach Docker Hub's
// /v2/ endpoint without going through a proxy. A 401 is the expected answer
// (it means the registry answered and only asked for a token); a timeout or
// connection failure means the host cannot reach Docker Hub directly, which
// is the common case in mainland China.
//
// The hint text is deliberately specific: it names the working mirror that
// was verified against this machine, so the user can copy-paste it.
//
// The probe is bounded to 5s and never fails: a nil return just means
// "no hint", not "docker hub works".
func (s *Service) probePullReachability(ctx context.Context) string {
	const dockerHub = "https://registry-1.docker.io/v2/"
	const daocloud = "https://docker.m.daocloud.io/v2/"

	// Try a real HTTP request first. Some networks block DNS but a direct
	// IP still fails; both cases surface as a non-200/non-401.
	reachable := func(u string) bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return false
		}
		client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		resp, err := client.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 512))
		// 401 is a healthy registry answering its auth challenge.
		return resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusOK
	}

	if reachable(dockerHub) {
		return "" // Docker Hub is reachable, nothing to suggest.
	}

	// Docker Hub is not reachable. Check whether a well-known mirror is, so
	// we can point the user at a name that is verified working rather than
	// asking them to guess.
	mirror := "docker.m.daocloud.io"
	if !reachable(daocloud) {
		mirror = "" // neither works; the user has a deeper network problem
	}
	if mirror == "" {
		return "容器服务可用，但本机直连 Docker Hub 与常用镜像站均失败，请检查网络/代理。"
	}

	return "本机无法直连 Docker Hub（wslc 无 registry mirror 配置）。已验证可用的镜像：" +
		mirror + "/library/alpine:3.20。" +
		"完整镜像路径用法示例：wslc pull " + mirror + "/library/alpine:3.20。" +
		"上游议题：microsoft/WSL#40951。"
}

// oneLine flattens an error into a single readable line.
func oneLine(err error) string {
	if err == nil {
		return ""
	}
	return truncate(strings.TrimSpace(err.Error()), 240)
}
