package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

func idPrefixes(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if len(id) > 4 {
			id = id[:4]
		}
		out = append(out, id)
	}
	return out
}

// TestListContainersFiltering locks the client-side Query/State/Limit behaviour.
func TestListContainersFiltering(t *testing.T) {
	cases := []struct {
		name   string
		filter ContainerFilter
		want   []string
	}{
		{"默认返回全部三行", ContainerFilter{}, []string{"aaaa", "bbbb", "cccc"}},
		{"Query 匹配名称", ContainerFilter{Query: "web"}, []string{"aaaa", "cccc"}},
		{"Query 大小写不敏感", ContainerFilter{Query: "WEB"}, []string{"aaaa", "cccc"}},
		{"Query 匹配镜像", ContainerFilter{Query: "alpine"}, []string{"cccc"}},
		{"Query 匹配镜像名片段", ContainerFilter{Query: "postgres"}, []string{"bbbb"}},
		{"Query 匹配 ID 片段", ContainerFilter{Query: "bbbb"}, []string{"bbbb"}},
		{"Query 无匹配返回空切片", ContainerFilter{Query: "zzz"}, []string{}},
		{"State running", ContainerFilter{State: "running"}, []string{"aaaa", "cccc"}},
		{"State RUNNING 大小写不敏感", ContainerFilter{State: "RUNNING"}, []string{"aaaa", "cccc"}},
		{"State exited 即非 running", ContainerFilter{State: "exited"}, []string{"bbbb"}},
		{"State stopped 等价于 exited", ContainerFilter{State: "stopped"}, []string{"bbbb"}},
		{"State created 无匹配但不报错", ContainerFilter{State: "created"}, []string{}},
		{"Limit 2", ContainerFilter{Limit: 2}, []string{"aaaa", "bbbb"}},
		{"Limit 大于总数", ContainerFilter{Limit: 10}, []string{"aaaa", "bbbb", "cccc"}},
		{"Limit 0 表示不限", ContainerFilter{Limit: 0}, []string{"aaaa", "bbbb", "cccc"}},
		{"Query 与 State 组合", ContainerFilter{Query: "web", State: "running"}, []string{"aaaa", "cccc"}},
		{"Query 与 Limit 组合", ContainerFilter{Query: "web", Limit: 1}, []string{"aaaa"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, _, _ := newContainerFake(t)
			items, err := svc.ListContainers(context.Background(), tc.filter)
			assertNoFailure(t, err)
			if items == nil {
				t.Fatal("ListContainers 不得返回 nil（前端期望数组）")
			}
			ids := make([]string, 0, len(items))
			for _, item := range items {
				ids = append(ids, item.ID)
			}
			got := idPrefixes(ids)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("筛选结果不符：实际 %v，期望 %v", got, tc.want)
			}
		})
	}
}

// TestListContainersPassesAllFlag proves the server-side --all still travels.
func TestListContainersPassesAllFlag(t *testing.T) {
	svc, fake, _ := newContainerFake(t)
	if _, err := svc.ListContainers(context.Background(), ContainerFilter{All: true}); err != nil {
		t.Fatalf("ListContainers: %v", err)
	}
	assertOnlyCall(t, fake.Calls(), []string{"container", "list", "--format", "json", "--all"})
}

// TestListContainersRejectsNegativeLimit keeps a nonsensical filter out of wslc.
func TestListContainersRejectsNegativeLimit(t *testing.T) {
	svc, fake, _ := newContainerFake(t)
	_, err := svc.ListContainers(context.Background(), ContainerFilter{Limit: -1})
	if err == nil {
		t.Fatal("负数 Limit 应被拒绝")
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("参数校验失败时不应调用 wslc，实际调用：%q", callArgs(fake.Calls()))
	}
}

func TestListContainersRejectsUnknownState(t *testing.T) {
	svc, fake, _ := newContainerFake(t)
	_, err := svc.ListContainers(context.Background(), ContainerFilter{State: "bogus"})
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("未知状态应被拒绝并回显状态名，实际：%v", err)
	}
	if len(fake.Calls()) != 0 {
		t.Fatal("参数校验失败时不应调用 wslc")
	}
}

// TestListContainersEmptyOutput checks the "no rows" path is an empty list, not
// an error (wslc prints nothing when a session has no containers).
func TestListContainersEmptyOutput(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When(listArgs(), wslc.Result{Stdout: ""}, nil)
	items, err := svc.ListContainers(context.Background(), ContainerFilter{})
	assertNoFailure(t, err)
	if items == nil || len(items) != 0 {
		t.Fatalf("空输出应得到非 nil 空切片，实际：%v", items)
	}
}

// TestListContainersPropagatesServiceUnavailable is the error-propagation line
// for the machine's current blocking condition.
func TestListContainersPropagatesServiceUnavailable(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When(listArgs(), wslc.Result{ExitCode: 1}, serviceUnavailableErr(listArgs()))
	_, err := svc.ListContainers(context.Background(), ContainerFilter{})
	if !errors.Is(err, wslc.ErrServiceUnavailable) {
		t.Fatalf("应保留 ErrServiceUnavailable 语义，实际：%v", err)
	}
}

func TestStartContainerPropagatesNotFound(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When(startArgs(), wslc.Result{ExitCode: 1}, notFoundErr(startArgs()))
	_, err := svc.StartContainer(context.Background(), "ghost")
	if !errors.Is(err, wslc.ErrNotFound) {
		t.Fatalf("应保留 ErrNotFound 语义，实际：%v", err)
	}
}

func TestListContainersMalformedOutput(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When(listArgs(), wslc.Result{Stdout: "这不是表格\n也不是 JSON\n"}, nil)
	_, err := svc.ListContainers(context.Background(), ContainerFilter{})
	if err == nil || !strings.Contains(err.Error(), "unrecognized") {
		t.Fatalf("无法识别的输出应报错并带上下文，实际：%v", err)
	}
}

// TestContainerActionOutput checks the string each mutating method returns.
func TestContainerActionOutput(t *testing.T) {
	svc, fake, _ := newTestService(t)
	startArgs := []string{"container", "start", "web"}
	fake.When(startArgs, wslc.Result{Stdout: "web\n"}, nil)
	out, err := svc.StartContainer(context.Background(), "web")
	assertNoFailure(t, err)
	if out != "web" {
		t.Fatalf("应返回 wslc 的 stdout，实际 %q", out)
	}

	// Empty stdout falls back to a human-readable message.
	stopArgs := []string{"container", "stop", "web"}
	fake.When(stopArgs, wslc.Result{}, nil)
	out, err = svc.StopContainer(context.Background(), "web", 0)
	assertNoFailure(t, err)
	if !strings.Contains(out, "web") {
		t.Fatalf("空 stdout 应给出兜底文案，实际 %q", out)
	}
}

func TestInspectContainerRequiresJSON(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When(inspectArgs(), wslc.Result{Stdout: "not json at all"}, nil)
	_, err := svc.InspectContainer(context.Background(), "web")
	if err == nil || !strings.Contains(err.Error(), "JSON") {
		t.Fatalf("非 JSON 的 inspect 输出应报错，实际：%v", err)
	}
}

func TestInspectContainerEmptyOutput(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When(inspectArgs(), wslc.Result{}, nil)
	if _, err := svc.InspectContainer(context.Background(), "web"); err == nil {
		t.Fatal("空 inspect 输出应报错")
	}
}

func TestContainerStatsParsesJSON(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When(statsArgs(), wslc.Result{Stdout: fixtureStatsJSON}, nil)
	stats, err := svc.ContainerStats(context.Background(), false)
	assertNoFailure(t, err)
	if len(stats) != 1 {
		t.Fatalf("应解析出 1 行 stats，实际 %d", len(stats))
	}
	if stats[0].CPUPerc != "0.50%" || stats[0].PIDs != 12 {
		t.Fatalf("stats 字段解析不符：%+v", stats[0])
	}
}

func TestContainerStatsPropagatesServiceUnavailable(t *testing.T) {
	svc, fake, _ := newTestService(t)
	args := []string{"container", "stats", "--format", "json"}
	fake.When(args, wslc.Result{ExitCode: 1}, serviceUnavailableErr(args))
	_, err := svc.ContainerStats(context.Background(), false)
	if !errors.Is(err, wslc.ErrServiceUnavailable) {
		t.Fatalf("应保留 ErrServiceUnavailable 语义，实际：%v", err)
	}
}

func TestPruneResultUsesStdout(t *testing.T) {
	svc, fake, _ := newTestService(t)
	args := []string{"container", "prune", "-f"}
	fake.When(args, wslc.Result{Stdout: "已删除容器 abc\n"}, nil)
	result, err := svc.PruneContainers(context.Background())
	assertNoFailure(t, err)
	if result.Stdout != "已删除容器 abc" {
		t.Fatalf("PruneResult.Stdout 不符：%q", result.Stdout)
	}
}
