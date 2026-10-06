//go:build integration

package service

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// These tests drive the real wslc.exe on this machine. They are excluded from
// the default `go test` run; use:
//
//	go test -tags integration ./internal/service/ -v
//
// On the current machine the container service (vmcompute) is stopped, so
// container-class calls are expected to fail with ErrServiceUnavailable; that
// state must be *reported*, never treated as a test failure.

func integrationService(t *testing.T) *Service {
	t.Helper()
	exe, err := wslc.NewResolver().Resolve()
	if err != nil {
		t.Skipf("wslc not installed: %v", err)
	}
	runner := wslc.NewExecRunner(exe, wslc.WithTimeout(30*time.Second))
	return NewService(runner, EmitterFunc(func(OutputEvent) {}))
}

// TestIntegrationEnvCheckRealMachine asserts the three-state EnvCheck against
// the machine as it really is.
func TestIntegrationEnvCheckRealMachine(t *testing.T) {
	svc := integrationService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	status, err := svc.EnvCheck(ctx)
	if err != nil {
		t.Fatalf("EnvCheck: %v", err)
	}
	t.Logf("EnvStatus: %+v", status)

	if !status.Available {
		t.Fatalf("wslc 已安装，Available 应为 true")
	}
	if status.WslcPath == "" {
		t.Error("WslcPath 应指向实际执行的 wslc.exe")
	}
	if status.WslcVersion == "" {
		t.Error("wslc version --format json 应给出客户端版本")
	}
	if status.WSLVersion == "" || status.KernelVersion == "" {
		t.Errorf("wslc info 应给出 WSL/内核版本：%+v", status)
	}
	if status.SettingsFile == "" {
		t.Error("wslc info 应给出设置文件路径")
	}
	// system session list has no --format, so this proves the table parser.
	if len(status.Sessions) == 0 {
		t.Error("至少应有一个 wslc 会话（表格解析）")
	}

	_, listErr := svc.ListContainers(ctx, ContainerFilter{})
	switch {
	case listErr == nil:
		if !status.ServiceReady {
			t.Error("container list 成功时 ServiceReady 应为 true")
		}
		t.Log("容器服务可用：container list 成功")
	case errors.Is(listErr, wslc.ErrServiceUnavailable):
		if status.ServiceReady {
			t.Error("container list 报 ErrServiceUnavailable 时 ServiceReady 必须为 false")
		}
		if !problemsContain(status.Problems, "HCS_E_SERVICE_NOT_AVAILABLE", "vmcompute", "Set-Service -Name vmcompute") {
			t.Errorf("Problems 未给出可执行的修复建议：%v", status.Problems)
		}
		t.Logf("容器服务不可用（本机预期，vmcompute 未运行）: %v", listErr)
	default:
		t.Fatalf("container list 出现预期外的错误: %v", listErr)
	}
}

// TestIntegrationEnvCheckExecutableMissing drives state 1 with a real
// ExecRunner pointed at a path that does not exist.
func TestIntegrationEnvCheckExecutableMissing(t *testing.T) {
	runner := wslc.NewExecRunner(filepath.Join(t.TempDir(), "wslc-missing.exe"))
	svc := NewService(runner, nil)

	status, err := svc.EnvCheck(context.Background())
	if err != nil {
		t.Fatalf("EnvCheck 不应因环境缺失而报错（前端需要 Problems）: %v", err)
	}
	t.Logf("EnvStatus: %+v", status)
	if status.Available || status.ServiceReady {
		t.Fatalf("缺失的 wslc 不应判为可用：%+v", status)
	}
	if !problemsContain(status.Problems, "WSLC_PATH", "wsl --update") {
		t.Errorf("Problems 应指出 WSLC_PATH / 升级 WSL：%v", status.Problems)
	}
}

// requireService skips a test when the container service is unavailable, which
// is the documented strategy for this machine (ENVIRONMENT.md §6).
func requireService(t *testing.T, svc *Service) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if _, err := svc.ListContainers(ctx, ContainerFilter{}); err != nil {
		if errors.Is(err, wslc.ErrServiceUnavailable) {
			t.Skipf("容器服务不可用，跳过真实容器测试：%v", err)
		}
		t.Fatalf("container list: %v", err)
	}
}

// TestIntegrationReadOnlyCommands exercises the list/prune paths against real
// wslc output, including the NDJSON shapes of volume/network list.
func TestIntegrationReadOnlyCommands(t *testing.T) {
	svc := integrationService(t)
	requireService(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	containers, err := svc.ListContainers(ctx, ContainerFilter{All: true})
	if err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	t.Logf("containers: %d", len(containers))

	images, err := svc.ListImages(ctx, true)
	if err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	t.Logf("images: %d", len(images))

	volumes, err := svc.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes（真实 NDJSON）: %v", err)
	}
	t.Logf("volumes: %+v", volumes)

	networks, err := svc.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks（真实 NDJSON）: %v", err)
	}
	if len(networks) == 0 {
		t.Error("真实环境至少有 bridge/host/none 三个内置网络")
	}
	t.Logf("networks: %d", len(networks))

	stats, err := svc.ContainerStats(ctx, true)
	if err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}
	t.Logf("stats rows: %d", len(stats))

	pruned, err := svc.PruneContainers(ctx)
	if err != nil {
		t.Fatalf("PruneContainers: %v", err)
	}
	t.Logf("container prune: %q", pruned.Stdout)

	pruned, err = svc.PruneImages(ctx, false)
	if err != nil {
		t.Fatalf("PruneImages: %v", err)
	}
	t.Logf("image prune: %q", pruned.Stdout)
}

// TestIntegrationRunArgvAccepted proves wslc's own parser accepts the full flag
// vector RunContainer builds: the call must fail on the missing image, not on
// an unknown option. It also checks the not-found sentinel mapping for the
// container commands.
func TestIntegrationRunArgvAccepted(t *testing.T) {
	svc := integrationService(t)
	requireService(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	_, err := svc.RunContainer(ctx, RunContainerOptions{
		Image:      "wslc-desktop-nonexistent:latest",
		Name:       "wslc-svc-probe",
		Command:    []string{"sh", "-c", "echo hi"},
		Detach:     true,
		Remove:     true,
		TTY:        true,
		Env:        []string{"A=1"},
		Ports:      []string{"8080:80"},
		Volumes:    []string{"wslc-svc-probe:/data"},
		Network:    "bridge",
		WorkDir:    "/app",
		User:       "1000",
		Hostname:   "probe",
		Memory:     "512M",
		CPUs:       "1.5",
		Entrypoint: "/bin/sh",
		Labels:     []string{"env=dev"},
		Pull:       "never",
	})
	if err == nil {
		t.Fatal("不存在的镜像必须报错（否则容器可能残留）")
	}
	if errors.Is(err, wslc.ErrUnsupportedCommand) {
		t.Fatalf("wslc 不接受生成的参数向量：%v", err)
	}
	if !errors.Is(err, wslc.ErrNotFound) {
		t.Logf("注意：镜像不存在未映射为 ErrNotFound，而是 %v", err)
	}
	t.Logf("container run（完整 flag 集）被 wslc 接受，仅镜像缺失：%v", err)

	if _, err := svc.StartContainer(ctx, "wslc-desktop-no-such-container"); !errors.Is(err, wslc.ErrNotFound) {
		t.Fatalf("start 不存在的容器应映射为 ErrNotFound，实际：%v", err)
	}
	if _, err := svc.InspectContainer(ctx, "wslc-desktop-no-such-container"); !errors.Is(err, wslc.ErrNotFound) && err == nil {
		t.Fatalf("inspect 不存在的容器应报错，实际：%v", err)
	}
}

// TestIntegrationNetworkRoundTrip creates and removes a real network, which
// exercises CreateNetwork's argument order and the NDJSON list parser.
func TestIntegrationNetworkRoundTrip(t *testing.T) {
	svc := integrationService(t)
	requireService(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	name := fmt.Sprintf("wslc-svc-%d", time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := svc.RemoveNetwork(cleanupCtx, name, true); err != nil {
			t.Logf("清理网络 %s 失败：%v", name, err)
		}
	})

	if _, err := svc.CreateNetwork(ctx, name, "bridge", "172.31.99.0/24", "172.31.99.1", true); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	networks, err := svc.ListNetworks(ctx)
	if err != nil {
		t.Fatalf("ListNetworks: %v", err)
	}
	found := false
	for _, network := range networks {
		if network.Name == name {
			found = true
			if network.Driver != "bridge" {
				t.Errorf("驱动不符：%+v", network)
			}
			if network.Internal != "true" {
				t.Errorf("--internal 应生效：%+v", network)
			}
		}
	}
	if !found {
		t.Fatalf("新建的网络 %s 未出现在列表中：%+v", name, networks)
	}
	if _, err := svc.RemoveNetwork(ctx, name, false); err != nil {
		t.Fatalf("RemoveNetwork: %v", err)
	}
}

// TestIntegrationVolumeRoundTrip creates and removes a real volume.
func TestIntegrationVolumeRoundTrip(t *testing.T) {
	svc := integrationService(t)
	requireService(t, svc)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	name := fmt.Sprintf("wslc-svc-vol-%d", time.Now().UnixNano()%1_000_000)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cleanupCancel()
		if _, err := svc.RemoveVolume(cleanupCtx, name, true); err != nil {
			t.Logf("清理卷 %s 失败：%v", name, err)
		}
	})

	if _, err := svc.CreateVolume(ctx, name, "guest"); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	volumes, err := svc.ListVolumes(ctx)
	if err != nil {
		t.Fatalf("ListVolumes: %v", err)
	}
	found := false
	for _, volume := range volumes {
		if volume.Name == name {
			found = true
		}
	}
	if !found {
		t.Fatalf("新建的卷 %s 未出现在列表中：%+v", name, volumes)
	}
	if _, err := svc.RemoveVolume(ctx, name, false); err != nil {
		t.Fatalf("RemoveVolume: %v", err)
	}
}

// TestIntegrationSessionStorageReadsRealSessions drives ListSessionStorage
// against the machine's real %LOCALAPPDATA%\wslc directory. The whole point of
// the view is to see storage that wslc itself never reports, so the assertion
// here is that the filesystem enumeration agrees with wslc's own session list
// for the sessions that are currently running.
func TestIntegrationSessionStorageReadsRealSessions(t *testing.T) {
	svc := integrationService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	got, err := svc.ListSessionStorage(ctx)
	if err != nil {
		t.Fatalf("ListSessionStorage: %v", err)
	}
	t.Logf("会话存储：%+v", got)

	// Every reported session must have a well-formed path and a size that is
	// not negative. Nothing stronger is safe to assert: the machine may have
	// any number of historical sessions and deleting them would be destructive.
	for _, entry := range got {
		if entry.SessionName == "" {
			t.Errorf("会话名不应为空：%+v", entry)
		}
		if !filepath.IsAbs(entry.Path) {
			t.Errorf("存储路径应为绝对路径：%+v", entry)
		}
		if entry.BytesOnDisk < 0 {
			t.Errorf("磁盘占用不应为负：%+v", entry)
		}
		if entry.Exists && entry.BytesOnDisk == 0 {
			t.Errorf("存在的存储文件不应报告 0 字节：%+v", entry)
		}
	}

	// Every running session must have a matching entry, which is the join key
	// the service uses for the Active flag.
	sessions, err := svc.ListSessions(ctx)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	for _, sess := range sessions {
		name := sess.Name
		if name == "" {
			continue
		}
		found := false
		for _, entry := range got {
			if entry.SessionName == name {
				found = entry.Active
				break
			}
		}
		if !found {
			t.Errorf("运行中的会话 %s 未在存储列表中或未被标记为运行中：%+v", name, got)
		}
	}
}
