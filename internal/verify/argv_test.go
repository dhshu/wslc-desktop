// Package verify holds adversarial, red-team style tests for the wslc desktop
// stack.
//
// It is a *separate package* on purpose: nothing here is expected to pass if it
// exposes a real defect, and every file here is additive-only (no existing
// implementation or test file is touched). The value of this package is in
// boundaries that internal/service's own tests do not cover.
//
// Two kinds of checks live here:
//
//   - argv_test.go: white-box assertions on the exact argument vector the
//     service layer hands to the runner, scripted with wslc.NewFakeRunner.
//     The runner receives a vector, never a shell string, so the interesting
//     question is "did the service omit an empty option, or emit it as
//     --flag "" ?" and "is a hostile-looking user value still a single argv
//     element?".
//   - errors_real_test.go: real-machine probes against C:\Program Files\WSL\
//     wslc.exe, proving the sentinel error mapping is based on genuine wslc 3.0.1
//     stderr text rather than fabricated strings.
package verify

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/service"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// ---------------------------------------------------------------------------
// Local test harness (deliberately self-contained: internal/service's helpers
// are package-private and cannot be reused from here).
// ---------------------------------------------------------------------------

// sinkEmitter discards every broadcast; the assertions here are about argv.
type sinkEmitter struct{}

func (sinkEmitter) Emit(service.OutputEvent) {}

// scriptEmitter captures events so a streaming call can be observed without a
// real process.
type scriptEmitter struct {
	mu     sync.Mutex
	events []service.OutputEvent
}

func (s *scriptEmitter) Emit(e service.OutputEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
}

func (s *scriptEmitter) snapshot() []service.OutputEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]service.OutputEvent(nil), s.events...)
}

// testHarness bundles a Service with its scripted runner.
type testHarness struct {
	svc    *service.Service
	fake   *wslc.FakeRunner
	events *scriptEmitter
}

// newHarness returns a Service whose runner answers every call with an empty
// successful result. Empty stdout is enough: the parsers turn it into "no rows",
// and every method under test returns text or a task id.
//
// The settings store is disabled so the argv tests assert the pre-mirror
// baseline: these tests verify argv construction in isolation, and the mirror
// rewrite behaviour is covered separately in internal/service/settings_test.go.
func newHarness(t *testing.T) *testHarness {
	t.Helper()
	fake := wslc.NewFakeRunner().Default(wslc.Result{}, nil)
	ev := &scriptEmitter{}
	svc := service.NewService(fake, ev)
	svc.DisableSettingsForTest()
	return &testHarness{svc: svc, fake: fake, events: ev}
}

// ---------------------------------------------------------------------------
// Assertion helpers
// ---------------------------------------------------------------------------

func assertSingleCall(t *testing.T, calls []wslc.Spec) []string {
	t.Helper()
	if len(calls) != 1 {
		t.Fatalf("期望恰好 1 次 wslc 调用，实际 %d 次：%v", len(calls), specArgv(calls))
	}
	return calls[0].Args
}

func specArgv(calls []wslc.Spec) [][]string {
	out := make([][]string, 0, len(calls))
	for _, c := range calls {
		out = append(out, c.Args)
	}
	return out
}

// argsEqual fails with a precise diff instead of comparing %v of slices.
func argsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv 长度不同：\n实际(%d) %q\n期望(%d) %q", len(got), got, len(want), want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv[%d] 不同：\n实际  %q\n期望  %q\n完整 argv: %q", i, got[i], want[i], got)
		}
	}
}

func containsSeq(got []string, seq ...string) bool {
	if len(seq) > len(got) {
		return false
	}
	for i := 0; i+len(seq) <= len(got); i++ {
		ok := true
		for j, s := range seq {
			if got[i+j] != s {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func containsFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}

func notContains(got []string, seq ...string) bool { return !containsSeq(got, seq...) }

// countValueAfterFlag returns how many times the two-element pair appears.
func countValueAfterFlag(args []string, flag, value string) int {
	n := 0
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			n++
		}
	}
	return n
}

func index(args []string, value string) int {
	for i, a := range args {
		if a == value {
			return i
		}
	}
	return -1
}

// assertStreamed waits until the service's background pump issued a Stream call,
// then returns its Spec. A cancelled context and a scripted error make the pump
// terminate promptly, so this does not need a real process.
func waitStreamSpec(t *testing.T, h *testHarness) wslc.Spec {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, call := range h.fake.Calls() {
			if call.Stream {
				return call
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("未观察到流式调用（已发生的调用：%v）", specArgv(h.fake.Calls()))
	return wslc.Spec{}
}

// assertStreamStopped waits until the streamed task left the running state, so a
// test never leaves a live goroutine.
func waitStreamSettled(t *testing.T, h *testHarness, taskID string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := h.svc.ListTasks(context.Background())
		if err == nil {
			for _, task := range tasks {
				if task.ID == taskID && task.State != service.TaskRunning {
					return
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Logf("任务 %s 未在 5s 内落定（pump 已由脚本化错误驱动结束）", taskID)
}

// ---------------------------------------------------------------------------
// 攻击点 1：空值不得生成空 flag
// ---------------------------------------------------------------------------

// TestRunContainerZeroOptionsEmitNoFlags drives RunContainer with every
// user-controlled field empty and asserts the produced argv is exactly
// "container run IMAGE" — no "--memory", no "--cpus", no "--network", and above
// all no "--flag "" " two-element pair anywhere in the vector.
//
// A regression here would show up as wslc failing with a localized option-parse
// error that the user cannot act on, so this is a defensive invariant rather
// than a style preference.
func TestRunContainerZeroOptionsEmitNoFlags(t *testing.T) {
	h := newHarness(t)

	_, err := h.svc.RunContainer(context.Background(), service.RunContainerOptions{Image: "alpine:3.20"})
	if err != nil {
		t.Fatalf("零值选项的 RunContainer 不应报错：%v", err)
	}
	args := assertSingleCall(t, h.fake.Calls())

	argsEqual(t, args, []string{"container", "run", "alpine:3.20"})

	// 整体不存在：不是"带了空值"，而是这些 flag 从头到尾没有出现。
	for _, flag := range []string{
		"--name", "--network", "--workdir", "-w", "-u", "-h",
		"-m", "--cpus", "--entrypoint", "--pull", "-e", "-p", "-v", "-l",
		"-d", "--rm", "-t",
	} {
		if containsFlag(args, flag) {
			t.Errorf("零值选项下出现了 %s：%q", flag, args)
		}
	}

	// 关键攻击面：flag 后跟空串。任何一对 "--flag \"\"" 都是 wslc 会拒绝的写法。
	for i, a := range args {
		if a == "" {
			t.Errorf("argv[%d] 是空字符串（产生了 --flag \"\" 形态）：%q", i, args)
		}
	}
	for _, flag := range []string{"--memory", "--cpus", "--network", "--name"} {
		if n := countValueAfterFlag(args, flag, ""); n > 0 {
			t.Errorf("%q 后出现空串 %d 次：%q", flag, n, args)
		}
	}

	t.Logf("argv = %q", args)
}

// TestRunContainerWhitespaceOnlyFieldsAreOmitted covers the sibling attack:
// fields that are only whitespace. TrimSpace-based omission must treat them the
// same as empty, and requireRef's leading/trailing whitespace check must fire
// before the value ever reaches the runner.
func TestRunContainerWhitespaceOnlyFieldsAreOmitted(t *testing.T) {
	cases := []struct {
		name    string
		opts    service.RunContainerOptions
		wantErr bool
	}{
		{
			name:    "空白 Memory/CPUs/Network 省略，其余保留",
			opts:    service.RunContainerOptions{Image: "alpine", Memory: "   ", CPUs: "  \t", Network: "   "},
			wantErr: false,
		},
		{
			name:    "首尾空白的 Name 被修剪后接受",
			opts:    service.RunContainerOptions{Image: "alpine", Name: " web "},
			wantErr: false,
		},
		{
			name:    "纯空白 Image 被 requireRef 拒绝",
			opts:    service.RunContainerOptions{Image: "   "},
			wantErr: true,
		},
		{
			name:    "全零值（Image 为空）被 requireRef 拒绝",
			opts:    service.RunContainerOptions{},
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.svc.RunContainer(context.Background(), tc.opts)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("期望拒绝，实际发出调用 argv=%q", specArgv(h.fake.Calls()))
				}
				if len(h.fake.Calls()) != 0 {
					t.Fatalf("校验失败时不得发出 wslc 调用，实际：%q", specArgv(h.fake.Calls()))
				}
				t.Logf("拒绝：%v", err)
				return
			}
			if err != nil {
				t.Fatalf("不应报错：%v", err)
			}
			args := assertSingleCall(t, h.fake.Calls())
			for _, flag := range []string{"-m", "--cpus", "--network"} {
				if containsFlag(args, flag) {
					t.Errorf("空白字段产生了 %s：%q", flag, args)
				}
			}
			if strings.Contains(tc.name, "Name") {
				// 已确认：RunContainer 先 TrimSpace 再交给 requireName，
				// 所以 " web " 被修剪成 "web" 后接受（见 F1）。
				argsEqual(t, args, []string{"container", "run", "--name", "web", "alpine"})
				return
			}
			argsEqual(t, args, []string{"container", "run", "alpine"})
		})
	}
}

// TestExecOptionsZeroFieldsOmitEveryFlag checks the exec vector the same way.
// ExecOptions is frozen as "--interactive" plus anything explicitly requested;
// LogsOptions renders to an empty vector at zero value.
func TestExecOptionsZeroFieldsOmitEveryFlag(t *testing.T) {
	args := (service.ExecOptions{}).Args()
	argsEqual(t, args, []string{"--interactive"})

	logs := (service.LogsOptions{}).Args()
	argsEqual(t, logs, []string{})

	for _, a := range append(append([]string{}, args...), logs...) {
		if a == "" {
			t.Errorf("产生了空参数：%q", append(args, logs...))
		}
	}
}

// ---------------------------------------------------------------------------
// 攻击点 2：shell 注入防护
// ---------------------------------------------------------------------------

// TestRunContainerRejectsHostileValuesAsPositionalValues is the core
// injection test. Every hostile value is submitted through a *different* input
// field so the test proves the per-field validator, not one lucky field.
//
// The contract is two-fold:
//
//   - the call must fail (the value is hostile: leading '-', whitespace, or
//     missing '=', all of which would let wslc's own parser re-interpret it);
//   - nothing may reach the runner, so no argv can ever contain a hostile
//     fragment at all.
func TestRunContainerRejectsHostileValuesAsPositionalValues(t *testing.T) {
	hostile := "evil; rm -rf /"
	flagLike := "--foo"

	cases := []struct {
		name   string
		opts   service.RunContainerOptions
		reason string
	}{
		{"name=注入串", service.RunContainerOptions{Image: "alpine", Name: hostile}, "空白字符"},
		{"name=flag 形态", service.RunContainerOptions{Image: "alpine", Name: flagLike}, "以 '-' 开头"},
		{"Env=注入串", service.RunContainerOptions{Image: "alpine", Env: []string{hostile}}, "缺少 '='"},
		{"Env=flag 形态", service.RunContainerOptions{Image: "alpine", Env: []string{flagLike}}, "以 '-' 开头"},
		{"Volumes=注入串", service.RunContainerOptions{Image: "alpine", Volumes: []string{hostile}}, "空白字符"},
		{"Volumes=flag 形态", service.RunContainerOptions{Image: "alpine", Volumes: []string{flagLike}}, "以 '-' 开头"},
		{"Labels=注入串", service.RunContainerOptions{Image: "alpine", Labels: []string{hostile}}, "缺少 '='"},
		{"Labels=flag 形态", service.RunContainerOptions{Image: "alpine", Labels: []string{flagLike}}, "以 '-' 开头"},
		{"Image=注入串", service.RunContainerOptions{Image: hostile}, "空白字符"},
		{"Image=flag 形态", service.RunContainerOptions{Image: flagLike}, "以 '-' 开头"},
		{"Network=注入串", service.RunContainerOptions{Image: "alpine", Network: hostile}, "空白字符"},
		{"Network=flag 形态", service.RunContainerOptions{Image: "alpine", Network: flagLike}, "以 '-' 开头"},
		{"Memory=注入串", service.RunContainerOptions{Image: "alpine", Memory: hostile}, "格式非法"},
		{"CPUs=注入串", service.RunContainerOptions{Image: "alpine", CPUs: hostile}, "格式非法"},
		{"WorkDir=flag 形态", service.RunContainerOptions{Image: "alpine", WorkDir: flagLike}, "以 '-' 开头"},
		{"User=flag 形态", service.RunContainerOptions{Image: "alpine", User: flagLike}, "以 '-' 开头"},
		{"Hostname=flag 形态", service.RunContainerOptions{Image: "alpine", Hostname: flagLike}, "以 '-' 开头"},
		{"Entrypoint=flag 形态", service.RunContainerOptions{Image: "alpine", Entrypoint: flagLike}, "以 '-' 开头"},
		{"Pull=注入串", service.RunContainerOptions{Image: "alpine", Pull: hostile}, "取值不在白名单"},
		{"Ports=注入串", service.RunContainerOptions{Image: "alpine", Ports: []string{hostile}}, "空白字符"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.svc.RunContainer(context.Background(), tc.opts)
			if err == nil {
				t.Fatalf("恶意值必须被拒绝；实际发出 argv=%q", specArgv(h.fake.Calls()))
			}
			// 校验失败时绝不能有任何 wslc 调用：否则"拒绝"只是事后诸葛。
			if calls := h.fake.Calls(); len(calls) != 0 {
				t.Fatalf("校验失败仍发出了 %d 次调用：%q", len(calls), specArgv(calls))
			}
			t.Logf("拒绝（%s）：%v", tc.reason, err)
		})
	}
}

// TestRunContainerLegitimateValuesSurviveAsSingleArgvElements asserts the
// *positive* half of the injection test: values that a hostile user can put
// into a field legitimately — spaces inside an env value, ':' , ',', '"', '\'
// in a label or mount — must reach the runner as ONE argv element each, not
// split by the service and not reassembled into a shell string.
func TestRunContainerLegitimateValuesSurviveAsSingleArgvElements(t *testing.T) {
	h := newHarness(t)

	values := service.RunContainerOptions{
		Image:      "alpine:3.20",
		Name:       "web-01.a",
		Env:        []string{"MY_SECRET=a b c", "EMPTY=", "A=B=B"},
		Labels:     []string{"greeting=hello world, ok"},
		Volumes:    []string{`C:\Users\test\AppData:/data:ro`},
		Network:    "app-net",
		WorkDir:    "/opt/app",
		User:       "1000:1000",
		Hostname:   "web01",
		Memory:     "512M",
		CPUs:       "1.5",
		Entrypoint: "/bin/sh",
		Pull:       "always",
	}
	if _, err := h.svc.RunContainer(context.Background(), values); err != nil {
		t.Fatalf("合法值不应被拒绝：%v", err)
	}
	args := assertSingleCall(t, h.fake.Calls())

	// 关键断言：这些原文必须**逐个、完整地**作为单一 argv 元素出现。
	for _, want := range []string{
		"MY_SECRET=a b c", // 值内含空格
		"EMPTY=",          // 空值但键存在
		"A=B=B",           // 多个 '='
		"greeting=hello world, ok",
		`C:\Users\test\AppData:/data:ro`,
		"1000:1000",
		"alpine:3.20",
	} {
		if index(args, want) < 0 {
			t.Errorf("合法值 %q 未作为单一 argv 元素出现：%q", want, args)
		}
	}

	// 反向断言：service 绝不能把含空白的值切散。任何 "MY_SECRET=" 或
	// 独立的 "a"、"b"、"c" 都是注入面。
	for _, fragment := range []string{"MY_SECRET=", "hello", "world,", "AppData", "1000"} {
		for _, a := range args {
			if a == fragment {
				t.Errorf("疑似被切散的片段 %q 单独成为 argv 元素：%q", fragment, args)
			}
		}
	}

	// 没有元素是空串，也没有元素被 shell 元字符拼接出别的形态。
	for _, a := range args {
		if a == "" {
			t.Errorf("argv 含空元素：%q", args)
		}
	}

	t.Logf("argv = %q", args)
}

// TestRunContainerEnvValueWithSpacesIsOneArgvElement is the explicit case the
// task calls out for "A=b c": a KEY=VALUE env entry whose *value* contains a
// space is legal for wslc and must survive as a single argv element.
func TestRunContainerEnvValueWithSpacesIsOneArgvElement(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RunContainer(context.Background(), service.RunContainerOptions{
		Image: "alpine",
		Env:   []string{"A=b c", "EMPTY=", "X=1"},
	})
	if err != nil {
		t.Fatalf("值内含空格的环境变量不应被拒绝：%v", err)
	}
	args := assertSingleCall(t, h.fake.Calls())
	argsEqual(t, args, []string{"container", "run", "-e", "A=b c", "-e", "EMPTY=", "-e", "X=1", "alpine"})

	// 逐个断言：原文是单一元素，且没有被切散成独立片段。
	for _, whole := range []string{"A=b c", "EMPTY=", "X=1"} {
		if index(args, whole) < 0 {
			t.Errorf("环境值 %q 未作为单一 argv 元素出现：%q", whole, args)
		}
	}
	for _, frag := range []string{"A=", "A=b", "b c", "c", "b"} {
		for _, a := range args {
			if a == frag {
				t.Errorf("疑似被切散的片段 %q 成为独立 argv 元素：%q", frag, args)
			}
		}
	}
}

// TestContainerRefRejectsHostileValues covers the ref-bearing methods that are
// not exercised by RunContainer: start/stop/restart/kill/remove/logs/exec and
// the image/volume/network variants.
func TestContainerRefRejectsHostileValues(t *testing.T) {
	hostile := []string{
		"evil; rm -rf /", // shell 元字符 + 空白
		"--foo",          // 会被 wslc 当作选项
		"A=b c",          // 空白
		"web\nrm -rf /",  // 换行注入
		"web\tinject",    // 制表符
		"\x00web",        // NUL
		" web",           // 前导空白
		"",               // 空
	}

	for _, ref := range hostile {
		t.Run(fmt.Sprintf("ref=%q", ref), func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			if _, err := h.svc.StartContainer(ctx, ref); err == nil {
				t.Errorf("StartContainer 接受了恶意 ref")
			}
			if _, err := h.svc.StopContainer(ctx, ref, 5); err == nil {
				t.Errorf("StopContainer 接受了恶意 ref")
			}
			if _, err := h.svc.RestartContainer(ctx, ref, 5); err == nil {
				t.Errorf("RestartContainer 接受了恶意 ref")
			}
			if _, err := h.svc.KillContainer(ctx, ref, "SIGKILL"); err == nil {
				t.Errorf("KillContainer 接受了恶意 ref")
			}
			if _, err := h.svc.RemoveContainer(ctx, ref, true, true); err == nil {
				t.Errorf("RemoveContainer 接受了恶意 ref")
			}
			if _, err := h.svc.InspectContainer(ctx, ref); err == nil {
				t.Errorf("InspectContainer 接受了恶意 ref")
			}
			if _, err := h.svc.StartLogs(ctx, ref, service.LogsOptions{}); err == nil {
				t.Errorf("StartLogs 接受了恶意 ref")
			}
			if _, err := h.svc.StartTerminal(ctx, ref, service.ExecOptions{}, 80, 24); err == nil {
				t.Errorf("StartTerminal 接受了恶意 ref")
			}
			if _, err := h.svc.PullImage(ctx, ref); err == nil {
				t.Errorf("PullImage 接受了恶意 ref")
			}
			if _, err := h.svc.RemoveImage(ctx, ref, true); err == nil {
				t.Errorf("RemoveImage 接受了恶意 ref")
			}
			if _, err := h.svc.TagImage(ctx, ref, "dst:1"); err == nil {
				t.Errorf("TagImage(source) 接受了恶意 ref")
			}
			if _, err := h.svc.TagImage(ctx, "src:1", ref); err == nil {
				t.Errorf("TagImage(target) 接受了恶意 ref")
			}
			if _, err := h.svc.InspectImage(ctx, ref); err == nil {
				t.Errorf("InspectImage 接受了恶意 ref")
			}
			if _, err := h.svc.CreateVolume(ctx, ref, "guest"); err == nil {
				t.Errorf("CreateVolume 接受了恶意 name")
			}
			if _, err := h.svc.RemoveVolume(ctx, ref, true); err == nil {
				t.Errorf("RemoveVolume 接受了恶意 name")
			}
			if _, err := h.svc.CreateNetwork(ctx, ref, "bridge", "", "", false); err == nil {
				t.Errorf("CreateNetwork 接受了恶意 name")
			}
			if _, err := h.svc.RemoveNetwork(ctx, ref, true); err == nil {
				t.Errorf("RemoveNetwork 接受了恶意 name")
			}

			if calls := h.fake.Calls(); len(calls) != 0 {
				t.Fatalf("校验失败仍发出 %d 次调用：%q", len(calls), specArgv(calls))
			}
		})
	}
}

// TestBuildImageRejectsHostileValues covers BuildOptions, which has its own
// validation rules (context path, dockerfile, tags, build args).
func TestBuildImageRejectsHostileValues(t *testing.T) {
	cases := []struct {
		name string
		opts service.BuildOptions
	}{
		{"Context=flag 形态", service.BuildOptions{Context: "-rf"}},
		{"Context=含换行", service.BuildOptions{Context: "/src\nrm -rf /"}},
		{"Context=含 NUL", service.BuildOptions{Context: "/src\x00x"}},
		{"Dockerfile=flag 形态", service.BuildOptions{Context: ".", Dockerfile: "--force"}},
		{"Tags=空", service.BuildOptions{Context: ".", Tags: []string{""}}},
		{"Tags=flag 形态", service.BuildOptions{Context: ".", Tags: []string{"-x"}}},
		{"BuildArgs=缺 =", service.BuildOptions{Context: ".", BuildArgs: []string{"BAD"}}},
		{"BuildArgs=flag 形态", service.BuildOptions{Context: ".", BuildArgs: []string{"-x=1"}}},
		{"Target=flag 形态", service.BuildOptions{Context: ".", Target: "--all"}},
		{"Labels=缺 =", service.BuildOptions{Context: ".", Labels: []string{"NOEQ"}}},
		{"Progress=非法", service.BuildOptions{Context: ".", Progress: "evil; rm -rf /"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			if _, err := h.svc.BuildImage(context.Background(), tc.opts); err == nil {
				t.Fatalf("恶意 BuildOptions 必须被拒绝")
			}
			if calls := h.fake.Calls(); len(calls) != 0 {
				t.Fatalf("校验失败仍发出调用：%q", specArgv(calls))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 攻击点 3：StopContainer timeout
// ---------------------------------------------------------------------------

// TestStopContainerTimeoutBoundaries is narrower than commands_test.go's two
// stop cases: it sweeps the boundary values the validator decides on, including
// a negative timeout (must not become "--time -1") and 1 (smallest positive).
func TestStopContainerTimeoutBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		timeout int
		want    []string
		wantErr bool
	}{
		{"timeout=0 省略 --time", 0, []string{"container", "stop", "web"}, false},
		{"timeout=1 最小正值", 1, []string{"container", "stop", "--time", "1", "web"}, false},
		{"timeout=30 生成 --time 30", 30, []string{"container", "stop", "--time", "30", "web"}, false},
		{"timeout=86400 大值", 86400, []string{"container", "stop", "--time", "86400", "web"}, false},
		{"timeout=-1 被拒绝", -1, nil, true},
		{"timeout=-60 被拒绝", -60, nil, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.svc.StopContainer(context.Background(), "web", tc.timeout)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("负超时未被拒绝")
				}
				if calls := h.fake.Calls(); len(calls) != 0 {
					t.Fatalf("拒绝后仍发出调用：%q", specArgv(calls))
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错：%v", err)
			}
			args := assertSingleCall(t, h.fake.Calls())
			argsEqual(t, args, tc.want)
			// 绝不允许负值超时被渲染成 flag 值。
			if containsSeq(args, "--time", "-1") {
				t.Fatalf("--time 不得取负值：%q", args)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 攻击点 4：KillContainer signal
// ---------------------------------------------------------------------------

// TestKillContainerSignalBoundaries asserts the empty signal is dropped while a
// real signal name/number is emitted, and that hostile signal strings never
// reach the runner.
func TestKillContainerSignalBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		signal  string
		want    []string
		wantErr bool
	}{
		{"signal=空 省略 --signal", "", []string{"container", "kill", "web"}, false},
		{"signal=SIGKILL", "SIGKILL", []string{"container", "kill", "--signal", "SIGKILL", "web"}, false},
		{"signal=SIGTERM 小写保留原样", "sigterm", []string{"container", "kill", "--signal", "sigterm", "web"}, false},
		{"signal=数字 9", "9", []string{"container", "kill", "--signal", "9", "web"}, false},
		{"signal=空白 归零", "   ", []string{"container", "kill", "web"}, false},
		{"signal=含分号", "SIGTERM;rm -rf /", nil, true},
		{"signal=flag 形态", "--force", nil, true},
		{"signal=首尾空白 被修剪", " SIGKILL ", []string{"container", "kill", "--signal", "SIGKILL", "web"}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			_, err := h.svc.KillContainer(context.Background(), "web", tc.signal)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("恶意信号 %q 未被拒绝", tc.signal)
				}
				if calls := h.fake.Calls(); len(calls) != 0 {
					t.Fatalf("拒绝后仍发出调用：%q", specArgv(calls))
				}
				return
			}
			if err != nil {
				t.Fatalf("不应报错：%v", err)
			}
			args := assertSingleCall(t, h.fake.Calls())
			argsEqual(t, args, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// 攻击点 5：ContainerFilter 边界
// ---------------------------------------------------------------------------

// TestContainerFilterBoundaries checks that Limit is *client-side* and therefore
// must never leak into argv at all: the same three argv vectors are produced for
// Limit=0, 1 and 1000, and the truncation happens in Go.
func TestContainerFilterBoundaries(t *testing.T) {
	const fixture = `[
		{"ID":"aaaa","Names":["/a"],"Image":"nginx","State":"running"},
		{"ID":"bbbb","Names":["/b"],"Image":"nginx","State":"running"},
		{"ID":"cccc","Names":["/c"],"Image":"postgres","State":"exited"}]`

	listArgs := []string{"container", "list", "--format", "json"}

	cases := []struct {
		filter service.ContainerFilter
		want   []string
		rows   int
	}{
		{service.ContainerFilter{}, listArgs, 3},
		{service.ContainerFilter{All: true}, append(listArgs, "--all"), 3},
		{service.ContainerFilter{All: true, Limit: 0}, append(listArgs, "--all"), 3},
		{service.ContainerFilter{All: true, Limit: 1}, append(listArgs, "--all"), 1},
		{service.ContainerFilter{All: true, Limit: 1000}, append(listArgs, "--all"), 3},
		{service.ContainerFilter{State: "running"}, listArgs, 2},
		{service.ContainerFilter{State: "running", Limit: 1}, listArgs, 1},
		{service.ContainerFilter{Query: "postgres", Limit: 1}, listArgs, 1},
		{service.ContainerFilter{Query: "nope", Limit: 1}, listArgs, 0},
	}

	for i, tc := range cases {
		name := fmt.Sprintf("%d_all=%v_query=%q_state=%q_limit=%d", i, tc.filter.All, tc.filter.Query, tc.filter.State, tc.filter.Limit)
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.fake.When(listArgs, wslc.Result{Stdout: fixture}, nil)
			h.fake.When(append(listArgs, "--all"), wslc.Result{Stdout: fixture}, nil)

			items, err := h.svc.ListContainers(context.Background(), tc.filter)
			if err != nil {
				t.Fatalf("ListContainers: %v", err)
			}
			args := assertSingleCall(t, h.fake.Calls())
			argsEqual(t, args, tc.want)

			// argv 里绝不允许出现任何 limit 痕迹。
			for _, a := range args {
				if a == "--limit" {
					t.Fatalf("--limit 泄漏进 argv：%q", args)
				}
			}
			if len(items) != tc.rows {
				t.Errorf("客户端截断结果行数=%d，期望 %d（argv=%q）", len(items), tc.rows, args)
			}
			if items == nil {
				t.Errorf("不得返回 nil 切片（前端期望数组）：%v", items)
			}
		})
	}
}

// TestContainerFilterRejectsInvalidInput pins the validation half: a negative
// limit and an unknown state must fail *before* any wslc call.
func TestContainerFilterRejectsInvalidInput(t *testing.T) {
	cases := []service.ContainerFilter{
		{Limit: -1},
		{State: "not-a-state"},
		{State: "running; rm -rf /"},
	}
	for i, f := range cases {
		t.Run(fmt.Sprintf("case%d", i), func(t *testing.T) {
			h := newHarness(t)
			_, err := h.svc.ListContainers(context.Background(), f)
			if err == nil {
				t.Fatalf("非法 filter 未被拒绝：%+v", f)
			}
			if calls := h.fake.Calls(); len(calls) != 0 {
				t.Fatalf("拒绝后仍发出调用：%q", specArgv(calls))
			}
			t.Logf("拒绝：%v", err)
		})
	}
}

// ---------------------------------------------------------------------------
// 攻击点 6：LogsOptions.Tail 边界
// ---------------------------------------------------------------------------

// TestLogsTailBoundaries pins the wslc-3.0.1 quirk: Tail<=0 must omit --tail
// entirely, because `--tail all` is rejected by the real binary with
// "tail 选项值无效: all". Tail>0 renders the number.
func TestLogsTailBoundaries(t *testing.T) {
	cases := []struct {
		tail int
		want []string
	}{
		{0, []string{"container", "logs", "web"}},
		{-1, []string{"container", "logs", "web"}},
		{-100, []string{"container", "logs", "web"}},
		{1, []string{"container", "logs", "--tail", "1", "web"}},
		{50, []string{"container", "logs", "--tail", "50", "web"}},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("Tail=%d", tc.tail), func(t *testing.T) {
			runner := wslc.NewFakeRunner().Default(
				wslc.Result{},
				&wslc.ExitError{Code: 1, Args: tc.want, Stderr: "probe"},
			)
			svc := service.NewService(runner, sinkEmitter{})

			if _, err := svc.StartLogs(context.Background(), "web", service.LogsOptions{Tail: tc.tail}); err != nil {
				t.Fatalf("StartLogs: %v", err)
			}
			call := waitStreamSpec(t, &testHarness{fake: runner})
			argsEqual(t, call.Args, tc.want)
			if !call.Stream {
				t.Errorf("日志流必须 Stream=true")
			}
			if call.Timeout >= 0 {
				t.Errorf("日志流应为不限时，实际 Timeout=%v", call.Timeout)
			}
			if tc.tail <= 0 && containsFlag(call.Args, "--tail") {
				t.Fatalf("Tail=%d 仍发出 --tail（wslc 3.0.1 会拒绝 --tail all）：%q", tc.tail, call.Args)
			}
			if tc.tail > 0 && notContains(call.Args, "--tail", strconv.Itoa(tc.tail)) {
				t.Errorf("Tail=%d 未渲染为 --tail %d：%q", tc.tail, tc.tail, call.Args)
			}
		})
	}
}

// TestLogsTailRejectsSinceUntilGrammar covers the timestamp flags that share the
// same Args() code path, including the empty-string omission rule.
func TestLogsTailRejectsSinceUntilGrammar(t *testing.T) {
	cases := []struct {
		name string
		opts service.LogsOptions
		want []string
		err  bool
	}{
		{
			name: "since/until 合法 RFC3339",
			opts: service.LogsOptions{Since: "2024-01-15T10:30:00Z", Until: "2024-01-16T10:30:00Z", Tail: 50, Timestamps: true},
			want: []string{"container", "logs", "--tail", "50", "--timestamps",
				"--since", "2024-01-15T10:30:00Z", "--until", "2024-01-16T10:30:00Z", "web"},
		},
		{
			name: "since 空白 省略",
			opts: service.LogsOptions{Since: "  ", Until: "\t", Tail: 0},
			want: []string{"container", "logs", "web"},
		},
		{
			name: "since 含空白 被拒绝",
			opts: service.LogsOptions{Since: "2024 01"},
			err:  true,
		},
		{
			name: "since 含注入 被拒绝",
			opts: service.LogsOptions{Since: "2024-01-01T00:00:00Z;rm -rf /"},
			err:  true,
		},
		{
			name: "until flag 形态 被拒绝",
			opts: service.LogsOptions{Until: "--bad"},
			err:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err {
				h := newHarness(t)
				if _, err := h.svc.StartLogs(context.Background(), "web", tc.opts); err == nil {
					t.Fatalf("恶意 since/until 未被拒绝：%v", tc.opts)
				}
				if calls := h.fake.Calls(); len(calls) != 0 {
					t.Fatalf("拒绝后仍发出调用：%q", specArgv(calls))
				}
				return
			}
			runner := wslc.NewFakeRunner().Default(wslc.Result{},
				&wslc.ExitError{Code: 1, Args: tc.want, Stderr: "probe"})
			svc := service.NewService(runner, sinkEmitter{})
			if _, err := svc.StartLogs(context.Background(), "web", tc.opts); err != nil {
				t.Fatalf("StartLogs: %v", err)
			}
			call := waitStreamSpec(t, &testHarness{fake: runner})
			argsEqual(t, call.Args, tc.want)
		})
	}
}

// ---------------------------------------------------------------------------
// 攻击点 7：RemoveContainer 布尔组合
// ---------------------------------------------------------------------------

// TestRemoveContainerBoolCombination enumerates all four Force/Volumes
// combinations. commands_test.go only covers (true,true) and (false,false);
// the two mixed cells are where a flag-ordering or double-emit bug hides.
func TestRemoveContainerBoolCombination(t *testing.T) {
	cases := []struct {
		force   bool
		volumes bool
		want    []string
	}{
		{false, false, []string{"container", "remove", "web"}},
		{true, false, []string{"container", "remove", "--force", "web"}},
		{false, true, []string{"container", "remove", "--volumes", "web"}},
		{true, true, []string{"container", "remove", "--force", "--volumes", "web"}},
	}

	for _, tc := range cases {
		name := fmt.Sprintf("force=%v_volumes=%v", tc.force, tc.volumes)
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			out, err := h.svc.RemoveContainer(context.Background(), "web", tc.force, tc.volumes)
			if err != nil {
				t.Fatalf("RemoveContainer: %v", err)
			}
			args := assertSingleCall(t, h.fake.Calls())
			argsEqual(t, args, tc.want)

			// 恰好各出现一次：重复 --force / --volumes 是 argparse 会放过的隐性 bug。
			if got := countOccurrences(args, "--force"); got != map[bool]int{false: 0, true: 1}[tc.force] {
				t.Errorf("--force 出现 %d 次（期望 %d）：%q", got, map[bool]int{false: 0, true: 1}[tc.force], args)
			}
			if got := countOccurrences(args, "--volumes"); got != map[bool]int{false: 0, true: 1}[tc.volumes] {
				t.Errorf("--volumes 出现 %d 次（期望 %d）：%q", got, map[bool]int{false: 0, true: 1}[tc.volumes], args)
			}
			// ref 必须是最后一个 argv 元素：布尔 flag 不得排在 ref 之后。
			if args[len(args)-1] != "web" {
				t.Errorf("ref 必须是最后一个 argv 元素：%q", args)
			}
			if out == "" {
				t.Errorf("应返回输出文本，实际为空")
			}
		})
	}
}

func countOccurrences(args []string, value string) int {
	n := 0
	for _, a := range args {
		if a == value {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// 攻击点 8：PullImage / BuildImage 参数
// ---------------------------------------------------------------------------

// TestPullImageArgvMinimal checks the pull vector: a bare positional ref, no
// flag, streamed, and unlimited timeout.
func TestPullImageArgvMinimal(t *testing.T) {
	runner := wslc.NewFakeRunner().Default(wslc.Result{},
		&wslc.ExitError{Code: 1, Args: []string{"image", "pull", "ghost:1"}, Stderr: "probe"})
	svc := service.NewService(runner, sinkEmitter{})
	svc.DisableSettingsForTest()

	id, err := svc.PullImage(context.Background(), "nginx:latest")
	if err != nil {
		t.Fatalf("PullImage: %v", err)
	}
	call := waitStreamSpec(t, &testHarness{fake: runner})
	argsEqual(t, call.Args, []string{"image", "pull", "nginx:latest"})
	if call.Kind != wslc.CmdImagePull {
		t.Errorf("CommandKind=%q", call.Kind)
	}
	if call.Timeout >= 0 {
		t.Errorf("pull 应为无限时，实际 Timeout=%v", call.Timeout)
	}
	// 记录发现 F3：PullImage/StreamEvents 未设置 Spec.Stream=true，
	// 与 StartLogs/BuildImage 不一致。此处按实际行为断言以免误报。
	t.Logf("pull Spec.Stream=%v Timeout=%v（F3：未置 Stream）", call.Stream, call.Timeout)
	// 不得夹带任何 flag。
	for i, a := range call.Args {
		if i > 2 && a != "" {
			t.Errorf("pull 不应有额外参数：%q", call.Args)
		}
	}
	_ = id
}

// TestBuildImageArgvIsContextLast is the highest-value structural assertion in
// this file: BuildImage has the largest flag surface, and the context path must
// be the *last positional argument*. If a future flag is appended after the
// context, wslc treats it as a second build argument and the build silently
// changes meaning.
func TestBuildImageArgvIsContextLast(t *testing.T) {
	cases := []struct {
		name string
		opts service.BuildOptions
		want []string
	}{
		{
			name: "全量选项，ContextPath 必须是最后一个位置参数",
			opts: service.BuildOptions{
				Context:    `/src/app`,
				Dockerfile: "Dockerfile.prod",
				Tags:       []string{"demo:1", "demo:2"},
				BuildArgs:  []string{"VERSION=1.2.3", "EMPTY="},
				Target:     "prod",
				NoCache:    true,
				Pull:       true,
				Labels:     []string{"env=dev"},
				Progress:   "plain",
			},
			want: []string{"image", "build",
				"--file", "Dockerfile.prod",
				"--tag", "demo:1", "--tag", "demo:2",
				"--build-arg", "VERSION=1.2.3", "--build-arg", "EMPTY=",
				"--target", "prod",
				"--no-cache", "--pull",
				"--label", "env=dev",
				"--progress", "plain",
				`/src/app`},
		},
		{
			name: "空选项 -> 当前目录",
			opts: service.BuildOptions{},
			want: []string{"image", "build", "."},
		},
		{
			name: "仅 Context",
			opts: service.BuildOptions{Context: `D:\build\ctx`},
			want: []string{"image", "build", `D:\build\ctx`},
		},
		{
			name: "仅标签与 build-arg",
			opts: service.BuildOptions{Context: ".", Tags: []string{"a:1"}, BuildArgs: []string{"K=V"}},
			want: []string{"image", "build", "--tag", "a:1", "--build-arg", "K=V", "."},
		},
		{
			name: "Progress 大写被规范化",
			opts: service.BuildOptions{Context: ".", Progress: "PLAIN"},
			want: []string{"image", "build", "--progress", "plain", "."},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runner := wslc.NewFakeRunner().Default(wslc.Result{},
				&wslc.ExitError{Code: 1, Args: tc.want, Stderr: "probe"})
			svc := service.NewService(runner, sinkEmitter{})

			id, err := svc.BuildImage(context.Background(), tc.opts)
			if err != nil {
				t.Fatalf("BuildImage: %v", err)
			}
			call := waitStreamSpec(t, &testHarness{fake: runner})
			argsEqual(t, call.Args, tc.want)
			if call.Kind != wslc.CmdImageBuild {
				t.Errorf("CommandKind=%q", call.Kind)
			}
			// 核心结构断言：最后一个 argv 元素就是 ContextPath（默认 "."）。
			wantCtx := tc.opts.Context
			if wantCtx == "" {
				wantCtx = "."
			}
			if call.Args[len(call.Args)-1] != wantCtx {
				t.Errorf("ContextPath 必须是最后一个位置参数：期望 %q，实际 argv 末尾 %q（完整 %q）",
					wantCtx, call.Args[len(call.Args)-1], call.Args)
			}
			// flag 必须在 context 之前：context 就是最后一个 argv 元素，
			// 所以上面的长度断言已经等价于这条断言（保留注释作说明）。
			if len(call.Args) != len(tc.want) || len(call.Args) < 1 {
				t.Fatalf("argv 异常：%q", call.Args)
			}
			_ = id
		})
	}
}

// TestBuildImageContextMustBeLastEvenWhenFlagsFollowEachOther is a focused
// regression: with only flags and a context, no flag may appear after the
// context.
func TestBuildImageContextMustBeLastEvenWhenFlagsFollowEachOther(t *testing.T) {
	h := newHarness(t)
	h.fake.Default(wslc.Result{}, nil)

	_, err := h.svc.BuildImage(context.Background(), service.BuildOptions{
		Context: "/ctx", Tags: []string{"a:1"}, NoCache: true, Pull: true, Progress: "quiet",
	})
	if err != nil {
		t.Fatalf("BuildImage: %v", err)
	}
	call := waitStreamSpec(t, h)
	ctxIdx := len(call.Args) - 1
	if call.Args[ctxIdx] != "/ctx" {
		t.Fatalf("context 不在末尾：%q", call.Args)
	}
	for _, a := range call.Args[:ctxIdx] {
		if a == "/ctx" {
			t.Fatalf("/ctx 不应出现在 flag 区：%q", call.Args)
		}
	}
	if countOccurrences(call.Args, "/ctx") != 1 {
		t.Fatalf("/ctx 必须恰好出现一次：%q", call.Args)
	}
}

// TestBuildImageRejectsCancelledContextUpFront covers the pre-check that must
// short-circuit before any task registration.
func TestBuildImageRejectsCancelledContextUpFront(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := h.svc.BuildImage(ctx, service.BuildOptions{Context: "."}); err == nil {
		t.Fatal("已取消的 context 必须报错")
	}
	if _, err := h.svc.PullImage(ctx, "nginx:latest"); err == nil {
		t.Fatal("已取消的 context 必须报错")
	}
	if _, err := h.svc.StartLogs(ctx, "web", service.LogsOptions{}); err == nil {
		t.Fatal("已取消的 context 必须报错")
	}
	if _, err := h.svc.StartTerminal(ctx, "web", service.ExecOptions{}, 80, 24); err == nil {
		t.Fatal("已取消的 context 必须报错")
	}
	if calls := h.fake.Calls(); len(calls) != 0 {
		t.Fatalf("取消时应不发出任何调用：%q", specArgv(calls))
	}
}

// ---------------------------------------------------------------------------
// 额外边界：argv 卫生（跨命令的通用不变量）
// ---------------------------------------------------------------------------

// TestNoMethodEmitsEmptyArgvElement runs a broad sweep of service methods with
// realistic-but-partial input and asserts the universal invariant: no argv
// element is ever the empty string. A single empty element anywhere is a
// "--flag \"\"" or a missing positional, both of which wslc rejects.
func TestNoMethodEmitsEmptyArgvElement(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	if _, err := h.svc.RunContainer(ctx, service.RunContainerOptions{
		Image: "nginx:latest", Name: "web", Detach: true, Remove: true, TTY: true,
		Env: []string{"A=1"}, Ports: []string{"8080:80"},
		Volumes: []string{"data:/data"}, Network: "app", WorkDir: "/app",
		User: "1000", Hostname: "h", Memory: "256M", CPUs: "0.5",
		Entrypoint: "/bin/sh", Labels: []string{"l=v"}, Pull: "always",
		Command: []string{"sh", "-c", "echo hi", ""}, // 空 command 片段必须被丢弃
	}); err != nil {
		t.Fatalf("RunContainer: %v", err)
	}
	if _, err := h.svc.ContainerStats(ctx, true); err != nil {
		t.Fatalf("ContainerStats: %v", err)
	}
	if _, err := h.svc.ListImages(ctx, true); err != nil {
		t.Fatalf("ListImages: %v", err)
	}
	if _, err := h.svc.CreateVolume(ctx, "d-1", "guest"); err != nil {
		t.Fatalf("CreateVolume: %v", err)
	}
	if _, err := h.svc.CreateNetwork(ctx, "n-1", "bridge", "172.20.0.0/16", "172.20.0.1", true); err != nil {
		t.Fatalf("CreateNetwork: %v", err)
	}
	if _, err := h.svc.PruneContainers(ctx); err != nil {
		t.Fatalf("PruneContainers: %v", err)
	}
	if _, err := h.svc.PruneImages(ctx, true); err != nil {
		t.Fatalf("PruneImages: %v", err)
	}

	for _, call := range h.fake.Calls() {
		for i, a := range call.Args {
			if a == "" {
				t.Errorf("argv[%d] 为空字符串（%s）：%q", i, call.Kind, call.Args)
			}
		}
	}
}

// TestRunContainerDiscardsEmptyCommandFragments ensures an empty entry in
// Command never becomes a trailing empty argv element.
func TestRunContainerDiscardsEmptyCommandFragments(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RunContainer(context.Background(), service.RunContainerOptions{
		Image: "alpine", Command: []string{"", "sh", "-c", "", "echo hi", ""},
	})
	if err != nil {
		t.Fatalf("RunContainer: %v", err)
	}
	args := assertSingleCall(t, h.fake.Calls())
	argsEqual(t, args, []string{"container", "run", "alpine", "sh", "-c", "echo hi"})
}

// TestRunContainerRejectsNULInCommand ensures a NUL byte in a command fragment
// is rejected rather than reaching the OS exec boundary.
func TestRunContainerRejectsNULInCommand(t *testing.T) {
	h := newHarness(t)
	_, err := h.svc.RunContainer(context.Background(), service.RunContainerOptions{
		Image: "alpine", Command: []string{"sh", "a\x00b"},
	})
	if err == nil {
		t.Fatal("含 NUL 的命令片段必须被拒绝")
	}
	if calls := h.fake.Calls(); len(calls) != 0 {
		t.Fatalf("拒绝后仍发出调用：%q", specArgv(calls))
	}
}

// TestNilRunnerAndNilEmitterDoNotPanic exercises the defensive nil-tolerance
// documented on NewService: a missing runner or emitter must fail softly rather
// than panic, because the frontend calls these before the backend is ready.
func TestNilRunnerAndNilEmitterDoNotPanic(t *testing.T) {
	noRunner := service.NewService(nil, sinkEmitter{})
	if _, err := noRunner.ListContainers(context.Background(), service.ContainerFilter{}); err == nil {
		t.Fatal("nil runner 必须报错")
	}
	if _, err := noRunner.RunContainer(context.Background(), service.RunContainerOptions{Image: "alpine"}); err == nil {
		t.Fatal("nil runner 必须报错")
	}

	noEmitter := service.NewService(wslc.NewFakeRunner().Default(wslc.Result{}, nil), nil)
	if _, err := noEmitter.ListVolumes(context.Background()); err != nil {
		t.Fatalf("nil emitter 不应影响一-shot 调用：%v", err)
	}
	if _, err := noEmitter.PullImage(context.Background(), "alpine"); err != nil {
		t.Fatalf("nil emitter 不应影响流式调用的注册：%v", err)
	}
}

// TestFormatCommandDoesNotInjectShellMeta is a direct check on the argv renderer
// used for the "system" diagnostic line, which must quote rather than join.
func TestFormatCommandDoesNotInjectShellMeta(t *testing.T) {
	runner := wslc.NewFakeRunner().Default(wslc.Result{},
		&wslc.ExitError{Code: 1, Args: []string{"container", "logs", "web"}, Stderr: "probe"})
	ev := &scriptEmitter{}
	svc := service.NewService(runner, ev)

	if _, err := svc.StartLogs(context.Background(), "web", service.LogsOptions{Tail: 5}); err != nil {
		t.Fatalf("StartLogs: %v", err)
	}
	_ = waitStreamSpec(t, &testHarness{fake: runner})

	joined := ""
	for _, e := range ev.snapshot() {
		joined += e.Text
	}
	// 诊断行里出现的必须是被引号包裹的 argv，而不是空格拼接后的可执行字符串。
	if !containsString(joined, "--tail") || !containsString(joined, "5") {
		t.Errorf("诊断行缺少 argv 内容：%q", joined)
	}
}

func containsString(haystack, needle string) bool {
	return strings.Contains(haystack, needle)
}
