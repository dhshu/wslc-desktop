package service

import (
	"context"
	"strings"
	"testing"
)

// TestValidationRejectsBadInputWithoutCallingWslc is the input-validation line:
// every rejected case must fail before a process is started, and the message
// must name the offending value so the UI can show something useful.
func TestValidationRejectsBadInputWithoutCallingWslc(t *testing.T) {
	cases := []struct {
		name string
		call func(context.Context, *Service) error
		want string
	}{
		{
			name: "空容器引用",
			call: func(ctx context.Context, s *Service) error { _, err := s.StartContainer(ctx, ""); return err },
			want: "不能为空",
		},
		{
			name: "以 - 开头的容器引用（避免被当成选项）",
			call: func(ctx context.Context, s *Service) error { _, err := s.StartContainer(ctx, "--force"); return err },
			want: "'-'",
		},
		{
			name: "含空白的容器引用",
			call: func(ctx context.Context, s *Service) error { _, err := s.StartContainer(ctx, "web 1"); return err },
			want: "空白",
		},
		{
			name: "负的停止超时",
			call: func(ctx context.Context, s *Service) error { _, err := s.StopContainer(ctx, "web", -1); return err },
			want: "不能为负数",
		},
		{
			name: "非法信号",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.KillContainer(ctx, "web", "SIG;KILL")
				return err
			},
			want: "信号非法",
		},
		{
			name: "run 缺少镜像",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{})
				return err
			},
			want: "镜像引用 不能为空",
		},
		{
			name: "run 内存格式非法",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", Memory: "12X"})
				return err
			},
			want: "内存限制格式非法",
		},
		{
			name: "run CPU 格式非法",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", CPUs: "many"})
				return err
			},
			want: "CPU 数量格式非法",
		},
		{
			name: "run pull 策略非法",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", Pull: "sometimes"})
				return err
			},
			want: "--pull",
		},
		{
			name: "run 端口缺少冒号",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", Ports: []string{"8080"}})
				return err
			},
			want: "host:container",
		},
		{
			name: "run 卷映射缺少目标",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", Volumes: []string{"data:"}})
				return err
			},
			want: "卷映射",
		},
		{
			name: "run 环境变量缺少 =",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", Env: []string{"JUSTAKEY"}})
				return err
			},
			want: "KEY=VALUE",
		},
		{
			name: "run 标签缺少 =",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", Labels: []string{"novalue"}})
				return err
			},
			want: "KEY=VALUE",
		},
		{
			name: "run 容器名非法",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.RunContainer(ctx, RunContainerOptions{Image: "alpine", Name: "bad name"})
				return err
			},
			want: "容器名称",
		},
		{
			name: "logs 空引用",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.StartLogs(ctx, "", LogsOptions{})
				return err
			},
			want: "不能为空",
		},
		{
			name: "logs since 含空格",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.StartLogs(ctx, "web", LogsOptions{Since: "2024-01-15 10:30"})
				return err
			},
			want: "--since",
		},
		{
			name: "terminal 空引用",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.StartTerminal(ctx, "", ExecOptions{}, 80, 24)
				return err
			},
			want: "不能为空",
		},
		{
			name: "pull 空引用",
			call: func(ctx context.Context, s *Service) error { _, err := s.PullImage(ctx, ""); return err },
			want: "不能为空",
		},
		{
			name: "build progress 非法",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.BuildImage(ctx, BuildOptions{Context: ".", Progress: "fancy"})
				return err
			},
			want: "--progress",
		},
		{
			name: "remove image 空引用",
			call: func(ctx context.Context, s *Service) error { _, err := s.RemoveImage(ctx, "", false); return err },
			want: "不能为空",
		},
		{
			name: "tag 缺目标",
			call: func(ctx context.Context, s *Service) error { _, err := s.TagImage(ctx, "src:1", ""); return err },
			want: "不能为空",
		},
		{
			name: "volume 名称带空格",
			call: func(ctx context.Context, s *Service) error { _, err := s.CreateVolume(ctx, "bad name", ""); return err },
			want: "卷名称",
		},
		{
			name: "network 子网非 CIDR",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.CreateNetwork(ctx, "net1", "", "172.20.0.0", "", false)
				return err
			},
			want: "CIDR",
		},
		{
			name: "network 网关非法",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.CreateNetwork(ctx, "net1", "", "", "not-an-ip", false)
				return err
			},
			want: "网关",
		},
		{
			name: "network 驱动非法",
			call: func(ctx context.Context, s *Service) error {
				_, err := s.CreateNetwork(ctx, "net1", "br idge", "", "", false)
				return err
			},
			want: "驱动名非法",
		},
		{
			name: "remove volume 空名称",
			call: func(ctx context.Context, s *Service) error { _, err := s.RemoveVolume(ctx, "", false); return err },
			want: "不能为空",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, fake, _ := newTestService(t)
			err := tc.call(context.Background(), svc)
			if err == nil {
				t.Fatal("非法输入必须被拒绝")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("错误信息应包含 %q，实际：%v", tc.want, err)
			}
			if len(fake.Calls()) != 0 {
				t.Fatalf("参数校验失败时不得调用 wslc，实际：%q", callArgs(fake.Calls()))
			}
		})
	}
}

// TestValidationAcceptsZeroValues proves the frontend's blank form fields are
// mapped to "no flag" rather than rejected or turned into empty arguments.
func TestValidationAcceptsZeroValues(t *testing.T) {
	svc, fake, _ := newTestService(t)

	if _, err := svc.RunContainer(context.Background(), RunContainerOptions{Image: "alpine"}); err != nil {
		t.Fatalf("零值选项应被接受：%v", err)
	}
	assertOnlyCall(t, fake.Calls(), []string{"container", "run", "alpine"})

	if _, err := svc.CreateNetwork(context.Background(), "net1", "", "", "", false); err != nil {
		t.Fatalf("零值网络参数应被接受：%v", err)
	}
	for _, call := range fake.Calls() {
		for _, arg := range call.Args {
			if arg == "" {
				t.Fatalf("不得生成空参数：%q", call.Args)
			}
		}
	}
}

// TestLogTailSemantics documents the --tail contract (negative means the whole
// log, expressed by omitting the flag because wslc rejects `--tail all`).
func TestLogTailSemantics(t *testing.T) {
	cases := []struct {
		tail int
		want []string
	}{
		{-1, []string{}},
		{0, []string{}},
		{50, []string{"--tail", "50"}},
	}
	for _, tc := range cases {
		got := (LogsOptions{Tail: tc.tail}).Args()
		assertArgsEqual(t, got, tc.want)

		svc, fake, _ := newTestService(t)
		id, err := svc.StartLogs(context.Background(), "web", LogsOptions{Tail: tc.tail})
		assertNoFailure(t, err)
		waitForTask(t, svc, id)
		call := waitForStreamCall(t, fake)
		want := append([]string{"container", "logs"}, tc.want...)
		want = append(want, "web")
		assertArgsEqual(t, call.Args, want)
	}
}
