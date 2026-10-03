package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// ListContainers lists containers and applies Query/State/Limit client-side.
//
// `--format json` is safe here: wslc system session list is the only list
// command that rejects it.
func (s *Service) ListContainers(ctx context.Context, filter ContainerFilter) ([]domain.Container, error) {
	if err := validateContainerFilter(filter); err != nil {
		return nil, err
	}
	args := []string{"container", "list", "--format", "json"}
	if filter.All {
		args = append(args, "--all")
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerList, Args: args})
	if err != nil {
		return nil, err
	}
	items, err := wslc.ParseContainers(res.Stdout)
	if err != nil {
		return nil, err
	}
	wantState := strings.ToLower(strings.TrimSpace(filter.State))
	out := make([]domain.Container, 0, len(items))
	for _, container := range items {
		if !matchContainerQuery(container, filter.Query) {
			continue
		}
		if !matchContainerState(container, wantState) {
			continue
		}
		out = append(out, container)
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
	}
	return out, nil
}

// matchContainerQuery is the client-side fuzzy match over name/image/id.
func matchContainerQuery(container domain.Container, query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	if q == "" {
		return true
	}
	if strings.Contains(strings.ToLower(container.ID), q) {
		return true
	}
	if strings.Contains(strings.ToLower(container.Image), q) {
		return true
	}
	if strings.Contains(strings.ToLower(container.ImageID), q) {
		return true
	}
	for _, name := range container.Names {
		if strings.Contains(strings.ToLower(strings.TrimPrefix(name, "/")), q) {
			return true
		}
	}
	return false
}

// matchContainerState maps the filter's state onto the parsed row.
func matchContainerState(container domain.Container, want string) bool {
	switch want {
	case "":
		return true
	case "running":
		return container.IsRunning()
	case "exited", "stopped":
		return !container.IsRunning()
	}
	got := strings.ToLower(strings.TrimSpace(container.State))
	if got == "" {
		got = strings.ToLower(strings.TrimSpace(container.Status))
	}
	return strings.Contains(got, want)
}

// StartContainer starts one container and returns wslc's own output.
func (s *Service) StartContainer(ctx context.Context, ref string) (string, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return "", err
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerStart, Args: []string{"container", "start", target}})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已启动 "+target), nil
}

// StopContainer stops one container, optionally with an explicit timeout.
func (s *Service) StopContainer(ctx context.Context, ref string, timeoutSec int) (string, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return "", err
	}
	timeout, err := validateTimeoutSec("停止超时", timeoutSec)
	if err != nil {
		return "", err
	}
	args := []string{"container", "stop"}
	if timeout != "" {
		args = append(args, "--time", timeout)
	}
	args = append(args, target)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerStop, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已停止 "+target), nil
}

// RestartContainer restarts one container, optionally with an explicit timeout.
func (s *Service) RestartContainer(ctx context.Context, ref string, timeoutSec int) (string, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return "", err
	}
	timeout, err := validateTimeoutSec("重启超时", timeoutSec)
	if err != nil {
		return "", err
	}
	// wslc 对 restart 的超时 flag 名是 --timeout（不是 --time；--time 只用于
	// stop）。用错 flag 名会让 wslc 直接报「当前命令的选项名称未被识别」。
	args := []string{"container", "restart"}
	if timeout != "" {
		args = append(args, "--timeout", timeout)
	}
	args = append(args, target)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerRst, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已重启 "+target), nil
}

// KillContainer sends a signal to one container (SIGKILL through wslc when the
// caller passes an empty signal).
func (s *Service) KillContainer(ctx context.Context, ref, signal string) (string, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return "", err
	}
	sig, err := validateSignal(signal)
	if err != nil {
		return "", err
	}
	args := []string{"container", "kill"}
	if sig != "" {
		args = append(args, "--signal", sig)
	}
	args = append(args, target)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerKill, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已终止 "+target), nil
}

// RemoveContainer removes one container, optionally forcing a running one and
// dropping its anonymous volumes.
func (s *Service) RemoveContainer(ctx context.Context, ref string, force, volumes bool) (string, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return "", err
	}
	args := []string{"container", "remove"}
	if force {
		args = append(args, "--force")
	}
	if volumes {
		args = append(args, "--volumes")
	}
	args = append(args, target)
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerRm, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已删除 "+target), nil
}

// RunContainer creates (and by default detaches) a container.
//
// Argument order is frozen so the regression tests can assert the exact argv:
//
//	container run [-d] [--rm] [-t] [--name N] [-e K=V…] [-p …] [-v …]
//	              [--network N] [-w DIR] [-u USER] [-h HOST] [-m MEM]
//	              [--cpus N] [--entrypoint E] [-l K=V…] [--pull P]
//	              IMAGE [COMMAND…]
//
// Every empty option is omitted rather than emitted as `--flag ""`.
func (s *Service) RunContainer(ctx context.Context, opts RunContainerOptions) (string, error) {
	image, err := requireRef("镜像引用", opts.Image)
	if err != nil {
		return "", err
	}
	image = s.rewriteImageRef(image)

	args := []string{"container", "run"}
	if opts.Detach {
		args = append(args, "-d")
	}
	if opts.Remove {
		args = append(args, "--rm")
	}
	if opts.TTY {
		args = append(args, "-t")
	}

	if name := strings.TrimSpace(opts.Name); name != "" {
		value, err := requireName("容器名称", name)
		if err != nil {
			return "", err
		}
		args = append(args, "--name", value)
	}
	// Proxy env vars are injected before the user's own so an explicit -e
	// HTTP_PROXY= wins, which is the expected override semantics.
	args = append(args, s.proxyEnvArgs()...)
	for _, entry := range opts.Env {
		value, err := validateKeyValue("环境变量", entry)
		if err != nil {
			return "", err
		}
		args = append(args, "-e", value)
	}
	for _, port := range opts.Ports {
		value, err := validatePort(port)
		if err != nil {
			return "", err
		}
		args = append(args, "-p", value)
	}
	for _, volume := range opts.Volumes {
		value, err := validateMount(volume)
		if err != nil {
			return "", err
		}
		args = append(args, "-v", value)
	}
	if network := strings.TrimSpace(opts.Network); network != "" {
		value, err := requireName("网络名", network)
		if err != nil {
			return "", err
		}
		args = append(args, "--network", value)
	}
	if workdir := strings.TrimSpace(opts.WorkDir); workdir != "" {
		value, err := requireText("工作目录", workdir)
		if err != nil {
			return "", err
		}
		args = append(args, "-w", value)
	}
	if user := strings.TrimSpace(opts.User); user != "" {
		value, err := requireText("运行用户", user)
		if err != nil {
			return "", err
		}
		args = append(args, "-u", value)
	}
	if hostname := strings.TrimSpace(opts.Hostname); hostname != "" {
		value, err := requireName("主机名", hostname)
		if err != nil {
			return "", err
		}
		args = append(args, "-h", value)
	}
	if memory := strings.TrimSpace(opts.Memory); memory != "" {
		if err := validateMemory(memory); err != nil {
			return "", err
		}
		args = append(args, "-m", memory)
	}
	if cpus := strings.TrimSpace(opts.CPUs); cpus != "" {
		if err := validateCPUs(cpus); err != nil {
			return "", err
		}
		args = append(args, "--cpus", cpus)
	}
	if entrypoint := strings.TrimSpace(opts.Entrypoint); entrypoint != "" {
		value, err := requireText("entrypoint", entrypoint)
		if err != nil {
			return "", err
		}
		args = append(args, "--entrypoint", value)
	}
	for _, label := range opts.Labels {
		value, err := validateKeyValue("标签", label)
		if err != nil {
			return "", err
		}
		args = append(args, "-l", value)
	}
	pull, err := validatePullPolicy(opts.Pull)
	if err != nil {
		return "", err
	}
	if pull != "" {
		args = append(args, "--pull", pull)
	}

	args = append(args, image)
	for _, part := range opts.Command {
		// Command words are intentionally passed through verbatim: wslc stops
		// option parsing at the image, so `sh -c "echo hi"` reaches the
		// container untouched (verified against wslc 3.0.1).
		if part == "" {
			continue
		}
		if strings.ContainsRune(part, 0) {
			return "", errors.New("service: 命令参数含有非法 NUL 字符")
		}
		args = append(args, part)
	}

	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerRun, Args: args})
	if err != nil {
		return "", err
	}
	return outputOr(res, "已创建容器 "+image), nil
}

// InspectContainer returns the raw JSON `wslc container inspect` prints.
func (s *Service) InspectContainer(ctx context.Context, ref string) (json.RawMessage, error) {
	target, err := requireRef("容器引用", ref)
	if err != nil {
		return nil, err
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerIns, Args: []string{"container", "inspect", target}})
	if err != nil {
		return nil, err
	}
	return inspectJSON(res)
}

// ContainerStats returns a one-shot resource snapshot.
func (s *Service) ContainerStats(ctx context.Context, all bool) ([]domain.ContainerStats, error) {
	args := []string{"container", "stats", "--format", "json"}
	if all {
		args = append(args, "--all")
	}
	res, err := s.run(ctx, wslc.Spec{Kind: wslc.CmdContainerStats, Args: args})
	if err != nil {
		return nil, err
	}
	return wslc.ParseStats(res.Stdout)
}
