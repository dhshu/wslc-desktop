package verify

// Real-machine error classification probes.
//
// These run against C:\Program Files\WSL\wslc.exe (wslc 3.0.1.0). They are not
// build-tagged because they are the whole point of this file: the assertion is
// that the sentinel mapping is based on *genuine* wslc stderr text, so if a
// future refactor changes the real binary's wording, these tests fail loudly
// instead of the mapping silently drifting away from reality.
//
// Every probe prints the raw stderr it captured, so the report a reader sees is
// the actual localized text, not a string this test invented.
//
// Environment notes (deliberately not treated as defects):
//   - the session VM's DNS often bypasses the host proxy, so
//     registry pulls may time out;
//   - container creation therefore fails on image resolution, not on our argv.
// Both are reported, never asserted on.

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/service"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

const wslcExePath = `C:\Program Files\WSL\wslc.exe`

// realRunner builds a real ExecRunner, skipping when wslc is absent.
func realRunner(t *testing.T) *wslc.ExecRunner {
	t.Helper()
	runner := wslc.NewExecRunner(wslcExePath, wslc.WithTimeout(60*time.Second))
	if err := runner.Available(context.Background()); err != nil {
		t.Skipf("无法执行 %s：%v", wslcExePath, err)
	}
	return runner
}

func realService(t *testing.T) *service.Service {
	t.Helper()
	return service.NewService(realRunner(t), service.EmitterFunc(func(service.OutputEvent) {}))
}

// probeSpec runs one raw wslc invocation and returns the runner's Result plus
// the error, so a test can print the genuine stderr verbatim.
func probeSpec(t *testing.T, runner *wslc.ExecRunner, args ...string) (wslc.Result, error) {
	t.Helper()
	return runner.Run(context.Background(), wslc.Spec{Args: args, Timeout: 45 * time.Second})
}

// printProbe emits one labelled block of real output.
func printProbe(t *testing.T, title string, res wslc.Result, err error) {
	t.Helper()
	t.Logf("--- %s ---", title)
	t.Logf("argv: %q", res.Args)
	t.Logf("exit: %d  duration: %v", res.ExitCode, res.Duration)
	t.Logf("STDOUT: %q", res.Stdout)
	t.Logf("STDERR: %q", res.Stderr)
	t.Logf("error: %v", err)
	t.Logf("errors.Is(ErrNotFound)=%v  errors.Is(ErrServiceUnavailable)=%v  errors.Is(ErrUnsupportedCommand)=%v",
		errors.Is(err, wslc.ErrNotFound),
		errors.Is(err, wslc.ErrServiceUnavailable),
		errors.Is(err, wslc.ErrUnsupportedCommand))
}

// TestRealMachineNotFoundOnMissingContainer proves the ErrNotFound mapping on
// the two container commands the task asks for: remove and start.
func TestRealMachineNotFoundOnMissingContainer(t *testing.T) {
	runner := realRunner(t)
	ref := "verify-not-exist-1"

	for _, sub := range []struct {
		name string
		kind wslc.CommandKind
		args []string
	}{
		{"container start", wslc.CmdContainerStart, []string{"container", "start", ref}},
		{"container remove", wslc.CmdContainerRm, []string{"container", "remove", ref}},
		{"container remove --force", wslc.CmdContainerRm, []string{"container", "remove", "--force", ref}},
		{"container stop", wslc.CmdContainerStop, []string{"container", "stop", ref}},
		{"container kill", wslc.CmdContainerKill, []string{"container", "kill", ref}},
		{"container inspect", wslc.CmdContainerIns, []string{"container", "inspect", ref}},
	} {
		t.Run(sub.name, func(t *testing.T) {
			res, err := probeSpec(t, runner, sub.args...)
			printProbe(t, sub.name, res, err)

			if err == nil {
				t.Fatal("不存在的容器必须报错")
			}
			if !errors.Is(err, wslc.ErrNotFound) {
				t.Fatalf("未映射为 ErrNotFound；真实 stderr = %q", res.Stderr)
			}
			// 反向断言：这条错误不能被误判成服务不可用。
			if errors.Is(err, wslc.ErrServiceUnavailable) {
				t.Fatalf("不应误判为 ErrServiceUnavailable：%q", res.Stderr)
			}
			// 真实文案必须真的含 wslc 的“找不到”字样，而不是我们造出来的串。
			if !strings.Contains(res.Stderr, "找不到") && !strings.Contains(strings.ToUpper(res.Stderr), "NOT FOUND") {
				t.Errorf("分类依据的真机文案不含 找不到/NOT FOUND：%q", res.Stderr)
			}
		})
	}
}

// TestRealMachineNotFoundViaServiceLayer checks the same mapping end-to-end
// through the service methods the frontend actually calls.
func TestRealMachineNotFoundViaServiceLayer(t *testing.T) {
	svc := realService(t)
	ctx := context.Background()
	ref := "verify-not-exist-2"

	cases := []struct {
		name string
		call func() error
	}{
		{"StartContainer", func() error { _, e := svc.StartContainer(ctx, ref); return e }},
		{"RemoveContainer", func() error { _, e := svc.RemoveContainer(ctx, ref, false, false); return e }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("不存在的容器必须报错")
			}
			if !errors.Is(err, wslc.ErrNotFound) {
				t.Fatalf("服务层未透传 ErrNotFound：%v", err)
			}
			t.Logf("服务层错误：%v", err)
		})
	}
}

// TestRealMachineNotFoundOnMissingVolumeAndNetwork uses the create-then-delete
// round trip the task suggests, plus the missing-object deletes, because this
// machine's DNS blocks registry pulls and therefore container creation.
//
// Note on --force: `wslc volume remove --force` (and `network remove --force`)
// on a non-existent object exits 0 with no output at all — the same semantics
// Docker's `volume rm -f` has. That is a documented wslc behaviour, not a
// parser bug: without --force the real binary reports 找不到卷 and is
// classified as ErrNotFound, which is what the service layer surfaces.
func TestRealMachineNotFoundOnMissingVolumeAndNetwork(t *testing.T) {
	runner := realRunner(t)

	for _, sub := range []struct {
		name string
		args []string
	}{
		{"volume remove 不存在的卷", []string{"volume", "remove", "verify-no-such-volume"}},
		{"network remove 不存在的网络", []string{"network", "remove", "verify-no-such-network"}},
	} {
		t.Run(sub.name, func(t *testing.T) {
			res, err := probeSpec(t, runner, sub.args...)
			printProbe(t, sub.name, res, err)
			if err == nil {
				t.Fatal("不存在的对象必须报错")
			}
			if !errors.Is(err, wslc.ErrNotFound) {
				t.Fatalf("未映射为 ErrNotFound；真实 stderr = %q", res.Stderr)
			}
		})
	}
}

// TestRealMachineVolumeRoundTrip produces genuine success-then-not-found
// output from the real binary, proving both the happy path and the failure path
// against real stdout/stderr.
func TestRealMachineVolumeRoundTrip(t *testing.T) {
	runner := realRunner(t)
	name := fmt.Sprintf("verify-vol-%d", time.Now().UnixNano()%1_000_000)

	create, createErr := probeSpec(t, runner, "volume", "create", name)
	printProbe(t, "volume create "+name, create, createErr)
	if createErr != nil {
		t.Fatalf("卷创建失败（环境限制或 bug）：%v", createErr)
	}

	remove, removeErr := probeSpec(t, runner, "volume", "remove", name)
	printProbe(t, "volume remove "+name, remove, removeErr)
	if removeErr != nil {
		t.Fatalf("刚创建的卷删除失败：%v", removeErr)
	}

	again, againErr := probeSpec(t, runner, "volume", "remove", name)
	printProbe(t, "volume remove "+name+"（第二次，应 Not Found）", again, againErr)
	if !errors.Is(againErr, wslc.ErrNotFound) {
		t.Fatalf("第二次删除必须映射为 ErrNotFound，实际：%v", againErr)
	}
}

// TestRealMachineIrrelevantErrorsAreNotErrNotFound is the critical negative
// control: unrelated failures must NOT be classified as ErrNotFound. The
// mapping in errors.go keys on substrings including 找不到 / 不存在, so a
// message that merely mentions "不存在" in a non-object context would be a real
// bug.
func TestRealMachineIrrelevantErrorsAreNotErrNotFound(t *testing.T) {
	runner := realRunner(t)

	subtests := []struct {
		name       string
		args       []string
		skipOnCode bool // some failures are benign-by-design on this machine
	}{
		{
			name: "不存在的顶层命令",
			args: []string{"totally-bogus-command-verify"},
		},
		{
			name: "无法识别的 flag",
			args: []string{"container", "list", "--definitely-not-a-flag"},
		},
		{
			name: "--tail all（wslc 3.0.1 明确拒绝）",
			args: []string{"container", "logs", "--tail", "all", "verify-no-such-container"},
		},
		{
			name: "非法信号名",
			args: []string{"container", "kill", "--signal", "SIGBOGUS", "verify-no-such-container"},
		},
		{
			name: "不存在的卷驱动",
			args: []string{"volume", "create", "--driver", "nonexistent-driver-verify", "verify-no-such-driver-vol"},
		},
		{
			name: "非法卷名",
			args: []string{"volume", "create", "not/a/valid/name"},
		},
		{
			name: "镜像拉取失败（本机会被 DNS 拦下）",
			args: []string{"image", "pull", "verify-unreachable.example.invalid:latest"},
		},
	}

	for _, sub := range subtests {
		t.Run(sub.name, func(t *testing.T) {
			res, err := probeSpec(t, runner, sub.args...)
			printProbe(t, sub.name, res, err)
			if err == nil {
				t.Fatalf("预期失败的命令竟然成功了：argv=%q", res.Args)
			}
			if errors.Is(err, wslc.ErrNotFound) {
				t.Fatalf("【误判】无关错误被当成 ErrNotFound（真实 stderr = %q）", res.Stderr)
			}
			if errors.Is(err, wslc.ErrServiceUnavailable) {
				t.Fatalf("【误判】无关错误被当成 ErrServiceUnavailable（真实 stderr = %q）", res.Stderr)
			}
		})
	}
}

// TestRealMachineNotExecutedNeverClaimsNotFound covers the launch-failure path:
// a missing executable must map to ErrExecutableNotFound and never leak into
// ErrNotFound.
func TestRealMachineNotExecutedNeverClaimsNotFound(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "wslc-does-not-exist.exe")
	runner := wslc.NewExecRunner(missing)

	_, err := runner.Run(context.Background(), wslc.Spec{Args: []string{"container", "start", "x"}})
	printProbe(t, "缺失可执行文件", wslc.Result{}, err)
	if err == nil {
		t.Fatal("缺失可执行文件必须报错")
	}
	if errors.Is(err, wslc.ErrNotFound) {
		t.Fatal("【误判】启动失败被当成 ErrNotFound")
	}
	if !errors.Is(err, wslc.ErrExecutableNotFound) {
		t.Fatalf("应映射为 ErrExecutableNotFound，实际：%v", err)
	}
}

// TestRealMachineContainerListReportsActualServiceState is an informational
// probe: it records whether the container service is actually reachable on this
// machine, so the report can distinguish "environment limitation" from "bug".
func TestRealMachineContainerListReportsActualServiceState(t *testing.T) {
	runner := realRunner(t)

	res, err := probeSpec(t, runner, "container", "list", "--format", "json")
	printProbe(t, "container list --format json", res, err)

	switch {
	case err == nil:
		t.Logf("容器服务可用（vmcompute Running），解析行数=%d", strings.Count(res.Stdout, "\"ID\""))
	case errors.Is(err, wslc.ErrServiceUnavailable):
		t.Logf("容器服务不可用（真机确认）：%v", err)
	case errors.Is(err, wslc.ErrNotFound):
		t.Fatalf("container list 不应映射为 ErrNotFound：%v", err)
	default:
		t.Logf("容器服务返回其他错误（如实记录，不当作 bug）：%v", err)
	}
}

// TestRealMachineTailAllReallyIsRejected pins the assumption LogsOptions.Args()
// is built on: wslc 3.0.1 rejects `--tail all`. If the binary ever accepts it,
// the omission-by-default design in the service layer is unnecessary but still
// correct.
func TestRealMachineTailAllReallyIsRejected(t *testing.T) {
	runner := realRunner(t)
	res, err := probeSpec(t, runner, "container", "logs", "--tail", "all", "verify-no-such-container")
	printProbe(t, "container logs --tail all", res, err)
	if err == nil {
		t.Log("注意：wslc 接受了 --tail all；LogsOptions 的省略策略变为保守而非必需")
		return
	}
	if errors.Is(err, wslc.ErrNotFound) {
		t.Fatalf("说明 wslc 先解析到了容器名：真实 stderr = %q", res.Stderr)
	}
	// 期望出现 wslc 自己的 “tail 选项值无效” 文案。
	if !strings.Contains(res.Stderr, "tail") {
		t.Logf("注意：未出现 'tail' 文案，实际 stderr = %q", res.Stderr)
	} else {
		t.Logf("确认：%s", res.Stderr)
	}
}

// TestRealMachineServiceLayerPullFailsOnDNS documents the DNS limitation the
// task warns about, so a later reader does not mistake it for a defect.
func TestRealMachineServiceLayerPullFailsOnDNS(t *testing.T) {
	svc := realService(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	if _, err := svc.PullImage(ctx, "verify-unreachable.example.invalid:latest"); err != nil {
		t.Logf("pull 失败（DNS/网络限制，非 bug）：%v", err)
		if errors.Is(err, wslc.ErrNotFound) {
			t.Fatalf("【误判】网络错误被当成 ErrNotFound：%v", err)
		}
		return
	}
	t.Log("注意：本环境的镜像拉取竟然成功了")
}
