package service

import (
	"context"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// TestCommandConstruction is the regression line for "which wslc command does
// this UI action produce". Every case asserts the complete argv vector, in
// order, plus the CommandKind, so a refactor cannot silently drop a flag.
func TestCommandConstruction(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*wslc.FakeRunner)
		call  func(context.Context, *Service) (any, error)
		kind  wslc.CommandKind
		want  []string
	}{
		// ---- containers ----
		{
			name: "container list 默认",
			call: func(ctx context.Context, s *Service) (any, error) { return s.ListContainers(ctx, ContainerFilter{}) },
			kind: wslc.CmdContainerList,
			want: []string{"container", "list", "--format", "json"},
		},
		{
			name: "container list --all",
			call: func(ctx context.Context, s *Service) (any, error) {
				return s.ListContainers(ctx, ContainerFilter{All: true})
			},
			kind: wslc.CmdContainerList,
			want: []string{"container", "list", "--format", "json", "--all"},
		},
		{
			name: "container start",
			call: func(ctx context.Context, s *Service) (any, error) { return s.StartContainer(ctx, "web") },
			kind: wslc.CmdContainerStart,
			want: []string{"container", "start", "web"},
		},
		{
			name: "container stop 带超时",
			call: func(ctx context.Context, s *Service) (any, error) { return s.StopContainer(ctx, "web", 10) },
			kind: wslc.CmdContainerStop,
			want: []string{"container", "stop", "--time", "10", "web"},
		},
		{
			name: "container stop 使用默认超时",
			call: func(ctx context.Context, s *Service) (any, error) { return s.StopContainer(ctx, "web", 0) },
			kind: wslc.CmdContainerStop,
			want: []string{"container", "stop", "web"},
		},
		{
			name: "container restart 带超时",
			call: func(ctx context.Context, s *Service) (any, error) { return s.RestartContainer(ctx, "web", 5) },
			kind: wslc.CmdContainerRst,
			// wslc 对 restart 用的是 --timeout（不是 --time，那是 stop 的 flag）；
			// 用错会让 wslc 报「当前命令的选项名称未被识别」。
			want: []string{"container", "restart", "--timeout", "5", "web"},
		},
		{
			name: "container kill 带信号",
			call: func(ctx context.Context, s *Service) (any, error) { return s.KillContainer(ctx, "web", "SIGKILL") },
			kind: wslc.CmdContainerKill,
			want: []string{"container", "kill", "--signal", "SIGKILL", "web"},
		},
		{
			name: "container kill 默认信号",
			call: func(ctx context.Context, s *Service) (any, error) { return s.KillContainer(ctx, "web", "") },
			kind: wslc.CmdContainerKill,
			want: []string{"container", "kill", "web"},
		},
		{
			name: "container remove 强制并删卷",
			call: func(ctx context.Context, s *Service) (any, error) {
				return s.RemoveContainer(ctx, "web", true, true)
			},
			kind: wslc.CmdContainerRm,
			want: []string{"container", "remove", "--force", "--volumes", "web"},
		},
		{
			name: "container remove 普通",
			call: func(ctx context.Context, s *Service) (any, error) {
				return s.RemoveContainer(ctx, "web", false, false)
			},
			kind: wslc.CmdContainerRm,
			want: []string{"container", "remove", "web"},
		},
		{
			name: "container run 全量选项",
			call: func(ctx context.Context, s *Service) (any, error) {
				return s.RunContainer(ctx, RunContainerOptions{
					Image:      "nginx:latest",
					Name:       "web",
					Command:    []string{"sh", "-c", "echo hi"},
					Detach:     true,
					Remove:     true,
					TTY:        true,
					Env:        []string{"A=1", "B=2"},
					Ports:      []string{"8080:80"},
					Volumes:    []string{"data:/var/lib/data", `C:\share:/mnt/share`},
					Network:    "app-net",
					WorkDir:    "/app",
					User:       "1000",
					Hostname:   "webhost",
					Memory:     "512M",
					CPUs:       "1.5",
					Entrypoint: "/bin/sh",
					Labels:     []string{"env=dev"},
					Pull:       "always",
				})
			},
			kind: wslc.CmdContainerRun,
			want: []string{
				"container", "run",
				"-d", "--rm", "-t",
				"--name", "web",
				"-e", "A=1", "-e", "B=2",
				"-p", "8080:80",
				"-v", "data:/var/lib/data", "-v", `C:\share:/mnt/share`,
				"--network", "app-net",
				"-w", "/app",
				"-u", "1000",
				"-h", "webhost",
				"-m", "512M",
				"--cpus", "1.5",
				"--entrypoint", "/bin/sh",
				"-l", "env=dev",
				"--pull", "always",
				"nginx:latest",
				"sh", "-c", "echo hi",
			},
		},
		{
			name: "container run 仅镜像",
			call: func(ctx context.Context, s *Service) (any, error) {
				return s.RunContainer(ctx, RunContainerOptions{Image: "alpine"})
			},
			kind: wslc.CmdContainerRun,
			want: []string{"container", "run", "alpine"},
		},
		{
			name:  "container inspect",
			setup: func(f *wslc.FakeRunner) { f.When(inspectArgs(), wslc.Result{Stdout: `{"Id":"web"}`}, nil) },
			call:  func(ctx context.Context, s *Service) (any, error) { return s.InspectContainer(ctx, "web") },
			kind:  wslc.CmdContainerIns,
			want:  []string{"container", "inspect", "web"},
		},
		{
			name: "container stats",
			call: func(ctx context.Context, s *Service) (any, error) { return s.ContainerStats(ctx, false) },
			kind: wslc.CmdContainerStats,
			want: []string{"container", "stats", "--format", "json"},
		},
		{
			name: "container stats --all",
			call: func(ctx context.Context, s *Service) (any, error) { return s.ContainerStats(ctx, true) },
			kind: wslc.CmdContainerStats,
			want: []string{"container", "stats", "--format", "json", "--all"},
		},

		// ---- images ----
		{
			name: "image list",
			call: func(ctx context.Context, s *Service) (any, error) { return s.ListImages(ctx, false) },
			kind: wslc.CmdImageList,
			want: []string{"image", "list", "--format", "json"},
		},
		{
			name: "image list --all",
			call: func(ctx context.Context, s *Service) (any, error) { return s.ListImages(ctx, true) },
			kind: wslc.CmdImageList,
			want: []string{"image", "list", "--format", "json", "--all"},
		},
		{
			name: "image remove --force",
			call: func(ctx context.Context, s *Service) (any, error) { return s.RemoveImage(ctx, "old:1", true) },
			kind: wslc.CmdImageRm,
			want: []string{"image", "remove", "--force", "old:1"},
		},
		{
			name: "image tag",
			call: func(ctx context.Context, s *Service) (any, error) { return s.TagImage(ctx, "src:1", "dst:2") },
			kind: wslc.CmdImageTag,
			want: []string{"image", "tag", "src:1", "dst:2"},
		},
		{
			name:  "image inspect",
			setup: func(f *wslc.FakeRunner) { f.When(imgInsArgs(), wslc.Result{Stdout: `[{"Id":"nginx:1"}]`}, nil) },
			call:  func(ctx context.Context, s *Service) (any, error) { return s.InspectImage(ctx, "nginx:1") },
			kind:  wslc.CmdImageIns,
			want:  []string{"image", "inspect", "nginx:1"},
		},

		// ---- volumes ----
		{
			name: "volume list",
			call: func(ctx context.Context, s *Service) (any, error) { return s.ListVolumes(ctx) },
			kind: wslc.CmdVolumeList,
			want: []string{"volume", "list", "--format", "json"},
		},
		{
			name: "volume create 带驱动",
			call: func(ctx context.Context, s *Service) (any, error) { return s.CreateVolume(ctx, "data", "guest") },
			kind: wslc.CmdVolumeCreate,
			want: []string{"volume", "create", "--driver", "guest", "data"},
		},
		{
			name: "volume create 默认驱动",
			call: func(ctx context.Context, s *Service) (any, error) { return s.CreateVolume(ctx, "data", "") },
			kind: wslc.CmdVolumeCreate,
			want: []string{"volume", "create", "data"},
		},
		{
			name: "volume remove --force",
			call: func(ctx context.Context, s *Service) (any, error) { return s.RemoveVolume(ctx, "data", true) },
			kind: wslc.CmdVolumeRm,
			want: []string{"volume", "remove", "--force", "data"},
		},
		{
			name: "volume remove 普通",
			call: func(ctx context.Context, s *Service) (any, error) { return s.RemoveVolume(ctx, "data", false) },
			kind: wslc.CmdVolumeRm,
			want: []string{"volume", "remove", "data"},
		},

		// ---- networks ----
		{
			name: "network list",
			call: func(ctx context.Context, s *Service) (any, error) { return s.ListNetworks(ctx) },
			kind: wslc.CmdNetworkList,
			want: []string{"network", "list", "--format", "json"},
		},
		{
			name: "network create 全量选项",
			call: func(ctx context.Context, s *Service) (any, error) {
				return s.CreateNetwork(ctx, "app-net", "bridge", "172.20.0.0/16", "172.20.0.1", true)
			},
			kind: wslc.CmdNetworkCreate,
			want: []string{"network", "create", "--driver", "bridge", "--subnet", "172.20.0.0/16", "--gateway", "172.20.0.1", "--internal", "app-net"},
		},
		{
			name: "network create 仅名称",
			call: func(ctx context.Context, s *Service) (any, error) {
				return s.CreateNetwork(ctx, "app-net", "", "", "", false)
			},
			kind: wslc.CmdNetworkCreate,
			want: []string{"network", "create", "app-net"},
		},
		{
			name: "network remove 普通",
			call: func(ctx context.Context, s *Service) (any, error) { return s.RemoveNetwork(ctx, "app-net", false) },
			kind: wslc.CmdNetworkRm,
			want: []string{"network", "remove", "app-net"},
		},

		// ---- system ----
		{
			name: "container prune 使用 container prune（本版本没有 system prune）",
			call: func(ctx context.Context, s *Service) (any, error) { return s.PruneContainers(ctx) },
			kind: wslc.CmdContainerPrune,
			want: []string{"container", "prune", "-f"},
		},
		{
			name: "image prune",
			call: func(ctx context.Context, s *Service) (any, error) { return s.PruneImages(ctx, false) },
			kind: wslc.CmdImagePrune,
			want: []string{"image", "prune", "-f"},
		},
		{
			name: "image prune --all",
			call: func(ctx context.Context, s *Service) (any, error) { return s.PruneImages(ctx, true) },
			kind: wslc.CmdImagePrune,
			want: []string{"image", "prune", "--all", "-f"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake, _ := newTestService(t)
			if tc.setup != nil {
				tc.setup(fake)
			}
			if _, err := tc.call(context.Background(), svc); err != nil {
				t.Fatalf("调用失败: %v", err)
			}
			calls := fake.Calls()
			assertOnlyCall(t, calls, tc.want)
			if calls[0].Kind != tc.kind {
				t.Errorf("CommandKind 不匹配：实际 %q，期望 %q", calls[0].Kind, tc.kind)
			}
		})
	}
}

// TestStreamingCommandConstruction covers the methods that go through
// Runner.Stream: the argv, the Stream flag, the unlimited timeout and the kind.
func TestStreamingCommandConstruction(t *testing.T) {
	cases := []struct {
		name string
		call func(context.Context, *Service) (string, error)
		kind wslc.CommandKind
		want []string
	}{
		{
			name: "container logs 全量选项",
			call: func(ctx context.Context, s *Service) (string, error) {
				return s.StartLogs(ctx, "web", LogsOptions{
					Follow:     true,
					Tail:       200,
					Timestamps: true,
					Since:      "2024-01-15T10:30:00Z",
					Until:      "2024-01-16T10:30:00Z",
				})
			},
			kind: wslc.CmdContainerLogs,
			want: []string{"container", "logs", "--follow", "--tail", "200", "--timestamps",
				"--since", "2024-01-15T10:30:00Z", "--until", "2024-01-16T10:30:00Z", "web"},
		},
		{
			name: "container logs Tail<0 不发 --tail（--tail all 会被 wslc 拒绝）",
			call: func(ctx context.Context, s *Service) (string, error) {
				return s.StartLogs(ctx, "web", LogsOptions{Tail: -1})
			},
			kind: wslc.CmdContainerLogs,
			want: []string{"container", "logs", "web"},
		},
		{
			name: "image pull",
			call: func(ctx context.Context, s *Service) (string, error) { return s.PullImage(ctx, "nginx:latest") },
			kind: wslc.CmdImagePull,
			want: []string{"image", "pull", "nginx:latest"},
		},
		{
			name: "image build 全量选项",
			call: func(ctx context.Context, s *Service) (string, error) {
				return s.BuildImage(ctx, BuildOptions{
					Context:    "/src/app",
					Dockerfile: "Dockerfile.prod",
					Tags:       []string{"demo:1", "demo:2"},
					BuildArgs:  []string{"VERSION=1.2.3"},
					Target:     "prod",
					NoCache:    true,
					Pull:       true,
					Labels:     []string{"env=dev"},
					Progress:   "plain",
				})
			},
			kind: wslc.CmdImageBuild,
			want: []string{"image", "build",
				"--file", "Dockerfile.prod",
				"--tag", "demo:1", "--tag", "demo:2",
				"--build-arg", "VERSION=1.2.3",
				"--target", "prod",
				"--no-cache", "--pull",
				"--label", "env=dev",
				"--progress", "plain",
				"/src/app"},
		},
		{
			name: "image build 空选项（前端会传空值）",
			call: func(ctx context.Context, s *Service) (string, error) {
				return s.BuildImage(ctx, BuildOptions{})
			},
			kind: wslc.CmdImageBuild,
			want: []string{"image", "build", "."},
		},
		{
			name: "events",
			call: func(ctx context.Context, s *Service) (string, error) { return s.StreamEvents(ctx) },
			kind: wslc.CmdEvents,
			want: []string{"events"},
		},
		{
			name: "container exec TTY",
			call: func(ctx context.Context, s *Service) (string, error) {
				return s.StartTerminal(ctx, "web", ExecOptions{TTY: true, User: "1000", WorkDir: "/app", Env: []string{"A=1"}}, 120, 40)
			},
			kind: wslc.CmdContainerExec,
			want: []string{"container", "exec", "--interactive", "--tty", "--user", "1000", "--workdir", "/app", "--env", "A=1", "web", "sh"},
		},
		{
			name: "container exec 零值选项",
			call: func(ctx context.Context, s *Service) (string, error) {
				return s.StartTerminal(ctx, "web", ExecOptions{}, 0, 0)
			},
			kind: wslc.CmdContainerExec,
			want: []string{"container", "exec", "--interactive", "web", "sh"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake, _ := newTestService(t)
			if tc.kind == wslc.CmdContainerExec {
				driver := &fakeTerminalDriver{}
				svc.terminal = driver
				id, err := tc.call(context.Background(), svc)
				assertNoFailure(t, err)
				driver.awaitProcess(t).finish(nil)
				waitForTask(t, svc, id)
				assertArgsEqual(t, driver.lastSpec(), tc.want)
				return
			}
			id, err := tc.call(context.Background(), svc)
			assertNoFailure(t, err)
			waitForTask(t, svc, id)

			call := waitForStreamCall(t, fake)
			assertArgsEqual(t, call.Args, tc.want)
			if call.Kind != tc.kind {
				t.Errorf("CommandKind 不匹配：实际 %q，期望 %q", call.Kind, tc.kind)
			}
			if !call.Stream {
				t.Error("流式调用必须设置 Spec.Stream")
			}
			if call.Timeout >= 0 {
				t.Errorf("流式调用应为不限时（Timeout<0），实际 %v", call.Timeout)
			}
		})
	}
}

// TestExecZeroValueOptionsProducesNoEmptyFlags guards the frontend complaint
// that blank form fields must not become `--flag ""`.
func TestExecZeroValueOptionsProducesNoEmptyFlags(t *testing.T) {
	for _, arg := range (ExecOptions{}).Args() {
		if arg == "" {
			t.Fatal("ExecOptions.Args 产生了空参数")
		}
	}
	for _, arg := range (LogsOptions{}).Args() {
		if arg == "" {
			t.Fatal("LogsOptions.Args 产生了空参数")
		}
	}
	assertArgsEqual(t, ExecOptions{}.Args(), []string{"--interactive"})
	assertArgsEqual(t, (LogsOptions{}).Args(), []string{})
}
