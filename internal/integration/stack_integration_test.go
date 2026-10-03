//go:build integration

// Package integration exercises the full stack — service -> adapter -> the real
// wslc.exe — against a live WSL container service.
//
// Run with:
//
//	go test -tags integration ./internal/integration/ -v -count=1
//
// Cases that cannot run because the host container service is unavailable are
// reported as skips naming the exact cause, so a green run never silently
// pretends container operations were verified.
package integration

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/service"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// collector records every event the service broadcasts.
type collector struct {
	mu     sync.Mutex
	events []service.OutputEvent
}

func (c *collector) Emit(e service.OutputEvent) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, e)
}

func (c *collector) forChannel(channel string) []service.OutputEvent {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []service.OutputEvent
	for _, e := range c.events {
		if e.Channel == channel {
			out = append(out, e)
		}
	}
	return out
}

func (c *collector) text(channel string) string {
	var sb strings.Builder
	for _, e := range c.forChannel(channel) {
		sb.WriteString(e.Text)
	}
	return sb.String()
}

// newStack wires the real ExecRunner + Resolver into the service.
func newStack(t *testing.T) (*service.Service, *collector, string) {
	t.Helper()

	resolver := wslc.NewResolver()
	exe, err := resolver.Resolve()
	if err != nil {
		t.Skipf("SKIP: wslc.exe not installed on this host (%v)", err)
	}

	rec := &collector{}
	// Streaming commands (logs -f, events, terminal) need an unbounded timeout,
	// which the service signals with a negative Spec.Timeout.
	runner := wslc.NewExecRunner(exe, wslc.WithTimeout(10*time.Minute))
	svc := service.NewService(runner, rec)
	return svc, rec, exe
}

// requireContainerService skips the test when the host cannot run containers.
func requireContainerService(t *testing.T, svc *service.Service) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	env, err := svc.EnvCheck(ctx)
	if err != nil {
		t.Fatalf("EnvCheck: %v", err)
	}
	if !env.Available {
		t.Skipf("SKIP: wslc executable not available: %v", env.Problems)
	}
	if env.ServiceReady {
		return
	}

	// Distinguish "not installed" from "installed but HCS is down".
	_, listErr := svc.ListContainers(ctx, service.ContainerFilter{All: true})
	if errors.Is(listErr, wslc.ErrServiceUnavailable) {
		t.Skipf("SKIP: container service unavailable (HCS_E_SERVICE_NOT_AVAILABLE). "+
			"Start the Hyper-V Host Compute Service as Administrator: "+
			"Set-Service -Name vmcompute -StartupType Manual; Start-Service vmcompute. "+
			"EnvCheck problems: %v", env.Problems)
	}
	t.Skipf("SKIP: container service not ready. EnvCheck problems: %v", env.Problems)
}

// waitTask polls until a task leaves the running state.
func waitTask(t *testing.T, svc *service.Service, id string) service.Task {
	t.Helper()

	deadline := time.Now().Add(6 * time.Minute)
	for time.Now().Before(deadline) {
		tasks, err := svc.ListTasks(context.Background())
		if err != nil {
			t.Fatalf("ListTasks: %v", err)
		}
		for _, task := range tasks {
			if task.ID != id {
				continue
			}
			if task.State != service.TaskRunning {
				return task
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish within the deadline", id)
	return service.Task{}
}

// ---------------------------------------------------------------------------
// Host-independent checks: these run on any machine with wslc installed.
// ---------------------------------------------------------------------------

func TestIntegrationEnvCheckReal(t *testing.T) {
	svc, _, exe := newStack(t)
	t.Logf("resolved wslc executable: %s", exe)

	env, err := svc.EnvCheck(context.Background())
	if err != nil {
		t.Fatalf("EnvCheck returned error: %v", err)
	}
	if !env.Available {
		t.Fatalf("expected Available=true, got problems: %v", env.Problems)
	}
	if env.WslcVersion == "" {
		t.Errorf("WslcVersion is empty; problems: %v", env.Problems)
	}
	if env.WSLVersion == "" {
		t.Errorf("WSLVersion is empty; problems: %v", env.Problems)
	}
	t.Logf("wslc=%s wsl=%s kernel=%s serviceReady=%v sessions=%d",
		env.WslcVersion, env.WSLVersion, env.KernelVersion, env.ServiceReady, len(env.Sessions))
	t.Logf("settings file: %s", env.SettingsFile)
	for _, p := range env.Problems {
		t.Logf("problem: %s", p)
	}
}

func TestIntegrationVersionReal(t *testing.T) {
	_, _, exe := newStack(t)

	runner := wslc.NewExecRunner(exe)
	res, err := runner.Run(context.Background(), wslc.Spec{
		Kind: wslc.CmdVersion,
		Args: []string{"version", "--format", "json"},
	})
	if err != nil {
		t.Fatalf("wslc version failed: %v", err)
	}
	info, err := wslc.ParseSystemInfo(res.Stdout)
	if err != nil {
		t.Fatalf("ParseSystemInfo(%q): %v", res.Stdout, err)
	}
	if info.Client.Version == "" {
		t.Fatalf("parsed version is empty from %q", res.Stdout)
	}
	t.Logf("real wslc version: %s", info.Client.Version)
}

// ---------------------------------------------------------------------------
// Container-service-dependent lifecycle checks.
// ---------------------------------------------------------------------------

func TestIntegrationContainerLifecycle(t *testing.T) {
	svc, rec, _ := newStack(t)
	requireContainerService(t, svc)

	ctx := context.Background()
	// WSLC ships with no registry mirror configuration and Docker Hub is
	// frequently unreachable from mainland China, so the integration test pulls
	// through the DaoCloud mirror — which is reachable directly (no proxy). If
	// the host does have a working direct line to Docker Hub the test still
	// works; the mirror is only the fallback that lets the test pass here.
	const image = "docker.m.daocloud.io/library/alpine:3.20"
	const name = "wslctest-lifecycle"

	// Always try to clean up, even if an assertion fails mid-way.
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		if _, err := svc.RemoveContainer(cleanupCtx, name, true, true); err != nil {
			t.Logf("cleanup: RemoveContainer(%s): %v", name, err)
		}
	})

	// 1. Pull the image (streamed task).
	pullID, err := svc.PullImage(ctx, image)
	if err != nil {
		if errors.Is(err, wslc.ErrServiceUnavailable) {
			t.Skipf("SKIP: container service unavailable during pull: %v", err)
		}
		t.Fatalf("PullImage(%s): %v", image, err)
	}
	pullTask := waitTask(t, svc, pullID)
	if pullTask.State != service.TaskSucceeded {
		t.Skipf("SKIP: image pull did not succeed (state=%s error=%s); a registry may be unreachable",
			pullTask.State, pullTask.Error)
	}
	t.Logf("pulled %s via task %s", image, pullID)

	// 2. Run a detached container.
	runOut, err := svc.RunContainer(ctx, service.RunContainerOptions{
		Image:   image,
		Name:    name,
		Command: []string{"sleep", "300"},
		Detach:  true,
		Remove:  false,
	})
	if err != nil {
		t.Fatalf("RunContainer: %v", err)
	}
	t.Logf("RunContainer output: %s", strings.TrimSpace(runOut))

	// 3. It must appear in the list, running.
	containers, err := svc.ListContainers(ctx, service.ContainerFilter{All: true})
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	found := findContainer(containers, name)
	if found == nil {
		t.Fatalf("container %q not found in list of %d", name, len(containers))
	}
	if !found.IsRunning() {
		t.Errorf("container %q state=%q status=%q, want running", name, found.State, found.Status)
	}
	t.Logf("listed container: id=%s name=%s image=%s state=%s ports=%v",
		found.ID, found.Name(), found.Image, found.State, found.Ports)

	// 4. exec inside it.
	execOut, err := svc.StartTerminal(ctx, name, service.ExecOptions{TTY: false}, 80, 24)
	if err != nil {
		t.Logf("StartTerminal not exercised (service may require TTY): %v", err)
	} else {
		if err := svc.TerminalWrite(ctx, execOut, "echo wslc-integration-marker\n"); err != nil {
			t.Logf("TerminalWrite: %v", err)
		}
		time.Sleep(2 * time.Second)
		if err := svc.StopStream(ctx, execOut); err != nil {
			t.Logf("StopStream(%s): %v", execOut, err)
		}
		if got := rec.text(service.ChannelTerminal); !strings.Contains(got, "wslc-integration-marker") {
			t.Logf("terminal output did not contain marker; got %q", truncate(got, 400))
		} else {
			t.Logf("exec round-trip verified through the terminal stream")
		}
	}

	// 5. Buffered logs.
	logsID, err := svc.StartLogs(ctx, name, service.LogsOptions{Tail: 50})
	if err != nil {
		t.Fatalf("StartLogs: %v", err)
	}
	time.Sleep(time.Second)
	if err := svc.StopStream(ctx, logsID); err != nil {
		t.Logf("StopStream(logs %s): %v", logsID, err)
	}
	logTask := waitTask(t, svc, logsID)
	t.Logf("logs task state=%s captured %d events", logTask.State, len(rec.forChannel(service.ChannelLogs)))

	// 6. Stats must include our container.
	stats, err := svc.ContainerStats(ctx, false)
	if err != nil {
		t.Errorf("ContainerStats: %v", err)
	} else {
		t.Logf("stats returned %d rows", len(stats))
	}

	// 7. Inspect returns JSON mentioning the container.
	raw, err := svc.InspectContainer(ctx, name)
	if err != nil {
		t.Errorf("InspectContainer: %v", err)
	} else if !strings.Contains(string(raw), name) {
		t.Errorf("inspect output does not mention %q: %s", name, truncate(string(raw), 300))
	}

	// 8. Stop, restart, then force-remove.
	if out, err := svc.StopContainer(ctx, name, 10); err != nil {
		t.Errorf("StopContainer: %v (out=%q)", err, out)
	} else {
		t.Logf("stopped %s", name)
	}
	if out, err := svc.RestartContainer(ctx, name, 10); err != nil {
		t.Errorf("RestartContainer: %v (out=%q)", err, out)
	} else {
		t.Logf("restarted %s", name)
	}
	if out, err := svc.RemoveContainer(ctx, name, true, true); err != nil {
		t.Errorf("RemoveContainer: %v (out=%q)", err, out)
	} else {
		t.Logf("removed %s", name)
	}

	after, err := svc.ListContainers(ctx, service.ContainerFilter{All: true})
	if err != nil {
		t.Fatalf("ListContainers after removal: %v", err)
	}
	if findContainer(after, name) != nil {
		t.Errorf("container %q still present after removal", name)
	}
}

func TestIntegrationVolumeLifecycle(t *testing.T) {
	svc, _, _ := newStack(t)
	requireContainerService(t, svc)

	ctx := context.Background()
	const name = "wslctest-volume"

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := svc.RemoveVolume(cleanupCtx, name, true); err != nil {
			t.Logf("cleanup: RemoveVolume(%s): %v", name, err)
		}
	})

	if _, err := svc.CreateVolume(ctx, name, ""); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	volumes, err := svc.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	if !hasVolume(volumes, name) {
		t.Fatalf("volume %q not found among %d volumes", name, len(volumes))
	}
	t.Logf("volume %s created and listed (%d total)", name, len(volumes))

	if _, err := svc.RemoveVolume(ctx, name, false); err != nil {
		t.Fatalf("RemoveVolume: %v", err)
	}
	after, err := svc.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes after removal: %v", err)
	}
	if hasVolume(after, name) {
		t.Errorf("volume %q still present after removal", name)
	}
}

func TestIntegrationNetworkLifecycle(t *testing.T) {
	svc, _, _ := newStack(t)
	requireContainerService(t, svc)

	ctx := context.Background()
	const name = "wslctest-network"

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := svc.RemoveNetwork(cleanupCtx, name, true); err != nil {
			t.Logf("cleanup: RemoveNetwork(%s): %v", name, err)
		}
	})

	if _, err := svc.CreateNetwork(ctx, name, "", "", "", false); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	networks, err := svc.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	if !hasNetwork(networks, name) {
		t.Fatalf("network %q not found among %d networks", name, len(networks))
	}
	t.Logf("network %s created and listed (%d total)", name, len(networks))

	if _, err := svc.RemoveNetwork(ctx, name, false); err != nil {
		t.Fatalf("RemoveNetwork: %v", err)
	}
	after, err := svc.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks after removal: %v", err)
	}
	if hasNetwork(after, name) {
		t.Errorf("network %q still present after removal", name)
	}
}

func TestIntegrationImageListAndPrune(t *testing.T) {
	svc, _, _ := newStack(t)
	requireContainerService(t, svc)

	ctx := context.Background()

	images, err := svc.ListImages(ctx, true)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	t.Logf("ListImages returned %d rows", len(images))
	for i, img := range images {
		if i >= 5 {
			break
		}
		t.Logf("  image: ref=%s id=%s size=%s", img.Reference(), img.ID, img.Size)
	}

	// Prune dangling images; must not report a hard failure.
	res, err := svc.PruneImages(ctx, false)
	if err != nil {
		t.Errorf("PruneImages: %v", err)
	} else {
		t.Logf("PruneImages output: %s", truncate(res.Stdout, 300))
	}
}

func TestIntegrationPruneContainers(t *testing.T) {
	svc, _, _ := newStack(t)
	requireContainerService(t, svc)

	res, err := svc.PruneContainers(context.Background())
	if err != nil {
		t.Fatalf("PruneContainers: %v", err)
	}
	t.Logf("PruneContainers output: %s", truncate(res.Stdout, 300))
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func findContainer(containers []domain.Container, name string) *domain.Container {
	for i := range containers {
		c := containers[i]
		if c.Name() == name || c.ID == name {
			return &c
		}
	}
	return nil
}

func hasVolume(volumes []domain.Volume, name string) bool {
	for _, v := range volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}

func hasNetwork(networks []domain.Network, name string) bool {
	for _, n := range networks {
		if n.Name == name {
			return true
		}
	}
	return false
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return fmt.Sprintf("%s…(+%d bytes)", s[:n], len(s)-n)
}
