package service

import (
	"context"
	"errors"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// TestEnvCheckExecutableMissing is state 1: wslc.exe cannot be located. The
// probe must not even try to run a command, and the advice must name WSLC_PATH
// and the WSL upgrade path.
func TestEnvCheckExecutableMissing(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.SetAvailable(wslc.ErrExecutableNotFound)

	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if status.Available {
		t.Error("Available 应为 false")
	}
	if status.ServiceReady {
		t.Error("ServiceReady 应为 false")
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("wslc 不可用时不应执行任何命令，实际：%q", callArgs(fake.Calls()))
	}
	if !problemsContain(status.Problems, "wsl --update", "WSLC_PATH") {
		t.Fatalf("Problems 必须给出可执行建议，实际：%v", status.Problems)
	}
	if status.CheckedAt.IsZero() {
		t.Error("CheckedAt 必须填写")
	}
	if status.Sessions == nil || len(status.Sessions) != 0 {
		t.Errorf("Sessions 应为空切片，实际：%v", status.Sessions)
	}
}

// TestEnvCheckServiceUnavailable is state 2, and the machine's real state:
// wslc runs, version/info/session list work, container commands fail with
// HCS_E_SERVICE_NOT_AVAILABLE because vmcompute is stopped.
func TestEnvCheckServiceUnavailable(t *testing.T) {
	svc, fake, _ := newEnvFake(t)
	fake.When(listArgs(), wslc.Result{ExitCode: 1}, serviceUnavailableErr(listArgs()))

	status, err := svc.EnvCheck(context.Background())
	// A degraded environment is data, not an error: Wails turns an error into a
	// rejected promise and the environment view would stay empty.
	assertNoFailure(t, err)

	if !status.Available {
		t.Error("Available 应为 true（wslc 本身可用）")
	}
	if status.ServiceReady {
		t.Error("ServiceReady 应为 false")
	}
	if status.WslcVersion != "3.0.1.0" {
		t.Errorf("WslcVersion 不符：%q", status.WslcVersion)
	}
	if status.WSLVersion != "3.0.1.0" {
		t.Errorf("WSLVersion 不符：%q", status.WSLVersion)
	}
	if status.KernelVersion != "6.18.40.1-1" {
		t.Errorf("KernelVersion 不符：%q", status.KernelVersion)
	}
	if status.SettingsFile == "" {
		t.Error("SettingsFile 应来自 wslc info")
	}
	if len(status.Sessions) != 1 || status.Sessions[0].Name != "wslc-cli-dhshu" ||
		status.Sessions[0].ID != 1 || status.Sessions[0].CreatorPid != 23428 {
		t.Errorf("Sessions 解析不符：%+v", status.Sessions)
	}
	if !problemsContain(status.Problems,
		"HCS_E_SERVICE_NOT_AVAILABLE",
		"vmcompute",
		"Set-Service -Name vmcompute -StartupType Manual",
		"Start-Service vmcompute",
		"Get-Service vmcompute") {
		t.Fatalf("Problems 必须给出 vmcompute 修复步骤，实际：%v", status.Problems)
	}
}

// TestEnvCheckHealthy is state 3: everything works and Problems stays empty.
func TestEnvCheckHealthy(t *testing.T) {
	svc, _, _ := newEnvFake(t)

	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if !status.Available || !status.ServiceReady {
		t.Fatalf("健康环境应 Available && ServiceReady，实际：%+v", status)
	}
	if len(status.Problems) != 0 {
		t.Fatalf("健康环境 Problems 应为空，实际：%v", status.Problems)
	}
	if len(status.Sessions) != 1 {
		t.Fatalf("Sessions 应有 1 条，实际：%v", status.Sessions)
	}
}

// TestEnvCheckReadsSessionsWithTableParser pins the "no --format" rule: the
// session probe must be the plain table command.
func TestEnvCheckReadsSessionsWithTableParser(t *testing.T) {
	svc, fake, _ := newEnvFake(t)
	if _, err := svc.EnvCheck(context.Background()); err != nil {
		t.Fatalf("EnvCheck: %v", err)
	}
	found := false
	for _, call := range fake.Calls() {
		if call.Kind == wslc.CmdSessionList {
			found = true
			assertArgsEqual(t, call.Args, []string{"system", "session", "list"})
			for _, arg := range call.Args {
				if arg == "--format" {
					t.Fatalf("system session list 不支持 --format，实际：%q", call.Args)
				}
			}
		}
	}
	if !found {
		t.Fatal("EnvCheck 必须执行 system session list")
	}
}

// TestEnvCheckProbesWithContainerCommand proves the readiness probe is a
// container-class command (the only kind that needs vmcompute).
func TestEnvCheckProbesWithContainerCommand(t *testing.T) {
	svc, fake, _ := newEnvFake(t)
	if _, err := svc.EnvCheck(context.Background()); err != nil {
		t.Fatalf("EnvCheck: %v", err)
	}
	kinds := map[wslc.CommandKind]bool{}
	for _, call := range fake.Calls() {
		kinds[call.Kind] = true
	}
	for _, want := range []wslc.CommandKind{wslc.CmdVersion, wslc.CmdInfo, wslc.CmdSessionList, wslc.CmdContainerList} {
		if !kinds[want] {
			t.Errorf("EnvCheck 缺少探针 %q", want)
		}
	}
}

// TestEnvCheckUnsupportedContainerList covers an older wslc that does not know
// `container list --format json`.
func TestEnvCheckUnsupportedContainerList(t *testing.T) {
	svc, fake, _ := newEnvFake(t)
	fake.When(listArgs(), wslc.Result{ExitCode: 1}, unsupportedErr(listArgs()))

	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if status.ServiceReady {
		t.Error("ServiceReady 应为 false")
	}
	if !problemsContain(status.Problems, "container list --format json", "wsl --update") {
		t.Fatalf("应提示升级 WSL，实际：%v", status.Problems)
	}
}

// TestEnvCheckDegradedVersionInfo keeps a failure in the informational probes
// from claiming the container service is fine or broken.
func TestEnvCheckDegradedVersionInfo(t *testing.T) {
	svc, fake, _ := newContainerFake(t)
	fake.When(versionArgs(), wslc.Result{ExitCode: 1}, exitError(versionArgs(), 1, "boom"))
	fake.When(infoArgs(), wslc.Result{Stdout: fixtureInfoJSON}, nil)
	fake.When(sessionArgs(), wslc.Result{Stdout: fixtureSessionTable}, nil)

	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if status.WslcVersion != "" {
		t.Errorf("版本读取失败时应留空，实际 %q", status.WslcVersion)
	}
	if status.WSLVersion != "3.0.1.0" {
		t.Errorf("info 仍应可用，实际 %q", status.WSLVersion)
	}
	if !status.ServiceReady {
		t.Error("container list 成功时 ServiceReady 应为 true")
	}
	if !problemsContain(status.Problems, "无法读取 wslc 版本") {
		t.Fatalf("应记录版本探测失败，实际：%v", status.Problems)
	}
}

// TestEnvCheckSessionFailureIsNotFatal keeps a broken session probe from
// disabling the container service verdict.
func TestEnvCheckSessionFailureIsNotFatal(t *testing.T) {
	svc, fake, _ := newEnvFake(t)
	fake.When(sessionArgs(), wslc.Result{ExitCode: 1}, unsupportedErr(sessionArgs()))

	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if !status.ServiceReady {
		t.Error("会话读取失败不应影响 ServiceReady")
	}
	if !problemsContain(status.Problems, "无法读取会话列表") {
		t.Fatalf("应记录会话读取失败，实际：%v", status.Problems)
	}
	if status.Sessions == nil || len(status.Sessions) != 0 {
		t.Errorf("失败后 Sessions 应为空切片，实际：%v", status.Sessions)
	}
}

// TestEnvCheckUnknownProbeFailure treats an unexpected probe error as "not
// ready" with the raw reason attached.
func TestEnvCheckUnknownProbeFailure(t *testing.T) {
	svc, fake, _ := newEnvFake(t)
	fake.When(listArgs(), wslc.Result{ExitCode: 1}, errors.New("pipe broken"))

	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if status.ServiceReady {
		t.Error("未知错误不应判定为就绪")
	}
	if !problemsContain(status.Problems, "容器服务探测失败", "pipe broken") {
		t.Fatalf("应保留原始错误，实际：%v", status.Problems)
	}
}

// TestEnvCheckNilRunner must not panic when the app has no runner configured.
func TestEnvCheckNilRunner(t *testing.T) {
	svc := NewService(nil, nil)
	status, err := svc.EnvCheck(context.Background())
	assertNoFailure(t, err)
	if status.Available || status.ServiceReady {
		t.Error("未配置 runner 时不应判定为可用")
	}
	if len(status.Problems) == 0 {
		t.Fatal("未配置 runner 时应给出原因")
	}
}

// TestEnvCheckCancelledContext surfaces cancellation as an error, so a request
// that nobody is waiting for does not look successful.
func TestEnvCheckCancelledContext(t *testing.T) {
	svc, _, _ := newEnvFake(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.EnvCheck(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("已取消的 context 应返回错误，实际：%v", err)
	}
}
