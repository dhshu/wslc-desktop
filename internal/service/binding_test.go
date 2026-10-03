package service

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
)

// wantSignatures is docs/CONTRACT.md B.2, transcribed by hand (including the
// corrected RemoveVolume signature). Any drift — a renamed method, an extra
// return value, a changed parameter — fails here instead of silently
// disappearing from the Wails binding.
var wantSignatures = map[string]string{
	"EnvCheck":         "(ctx)->(EnvStatus,error)",
	"ListContainers":   "(ctx,ContainerFilter)->([]domain.Container,error)",
	"StartContainer":   "(ctx,string)->(string,error)",
	"StopContainer":    "(ctx,string,int)->(string,error)",
	"RestartContainer": "(ctx,string,int)->(string,error)",
	"KillContainer":    "(ctx,string,string)->(string,error)",
	"RemoveContainer":  "(ctx,string,bool,bool)->(string,error)",
	"RunContainer":     "(ctx,RunContainerOptions)->(string,error)",
	"InspectContainer": "(ctx,string)->(json.RawMessage,error)",
	"ContainerStats":   "(ctx,bool)->([]domain.ContainerStats,error)",
	"StartLogs":        "(ctx,string,LogsOptions)->(string,error)",
	"StopStream":       "(ctx,string)->(error)",
	"StartTerminal":    "(ctx,string,ExecOptions,int,int)->(string,error)",
	"TerminalWrite":    "(ctx,string,string)->(error)",
	"TerminalResize":   "(ctx,string,int,int)->(error)",
	"ListImages":       "(ctx,bool)->([]domain.Image,error)",
	"PullImage":        "(ctx,string)->(string,error)",
	"BuildImage":       "(ctx,BuildOptions)->(string,error)",
	"RemoveImage":      "(ctx,string,bool)->(string,error)",
	"TagImage":         "(ctx,string,string)->(string,error)",
	"InspectImage":     "(ctx,string)->(json.RawMessage,error)",
	"ListVolumes":      "(ctx)->([]domain.Volume,error)",
	"CreateVolume":     "(ctx,string,string)->(string,error)",
	"RemoveVolume":     "(ctx,string,bool)->(string,error)",
	"ListNetworks":     "(ctx)->([]domain.Network,error)",
	"CreateNetwork":    "(ctx,string,string,string,string,bool)->(string,error)",
	"RemoveNetwork":    "(ctx,string,bool)->(string,error)",
	"PruneContainers":  "(ctx)->(PruneResult,error)",
	"PruneImages":      "(ctx,bool)->(PruneResult,error)",
	"ListTasks":        "(ctx)->([]Task,error)",
	"CancelTask":       "(ctx,string)->(error)",
	"StreamEvents":     "(ctx)->(string,error)",
	"LoadSettings":     "(ctx)->(AppSettings,error)",
	"SaveSettings":     "(ctx,AppSettings)->(AppSettings,error)",
	"TestMirror":       "(ctx,string)->(MirrorProbe,error)",
}

// TestServiceSignaturesMatchContract enforces the frozen method set, and with
// it the Wails v2 rule that a bound method may only return (T, error) or
// (error): a method with 0 or 3+ results is silently dropped by the binding
// generator, so this test is the only thing standing between the frontend and a
// missing backend method.
func TestServiceSignaturesMatchContract(t *testing.T) {
	serviceType := reflect.TypeOf(&Service{})
	ctxType := reflect.TypeOf((*context.Context)(nil)).Elem()

	seen := map[string]bool{}
	for i := 0; i < serviceType.NumMethod(); i++ {
		method := serviceType.Method(i)
		if !method.IsExported() {
			continue
		}
		seen[method.Name] = true

		// Test-only helpers are not part of the frozen contract but must not
		// leak through the Wails binding either; they never appear on App.
		if strings.HasSuffix(method.Name, "ForTest") {
			continue
		}

		want, ok := wantSignatures[method.Name]
		if !ok {
			t.Errorf("发现契约外的导出方法 %s%s", method.Name, method.Type.String())
			continue
		}
		got := describeMethod(method.Type, ctxType)
		if got != want {
			t.Errorf("%s 签名不符：\n实际 %s\n契约 %s", method.Name, got, want)
		}

		// Re-state the Wails rule explicitly, independent of the table above.
		returns := method.Type.NumOut()
		if returns == 0 || returns > 2 {
			t.Errorf("%s 有 %d 个返回值；Wails 只支持 (T, error) 或 (error)", method.Name, returns)
		}
		if returns == 2 && method.Type.Out(1) != reflect.TypeOf((*error)(nil)).Elem() {
			t.Errorf("%s 的第二个返回值必须是 error", method.Name)
		}
	}

	for name := range wantSignatures {
		if !seen[name] {
			t.Errorf("契约方法 %s 未实现（前端会调用到一个不存在的方法）", name)
		}
	}
}

// describeMethod renders a Go method type as "(params)->(results)" with stable,
// compact type names. The receiver (In(0)) is skipped.
func describeMethod(methodType reflect.Type, ctxType reflect.Type) string {
	parts := make([]string, 0, methodType.NumIn())
	for i := 1; i < methodType.NumIn(); i++ {
		parts = append(parts, compactType(methodType.In(i), ctxType))
	}
	results := make([]string, 0, methodType.NumOut())
	for i := 0; i < methodType.NumOut(); i++ {
		results = append(results, compactType(methodType.Out(i), ctxType))
	}
	return "(" + strings.Join(parts, ",") + ")->(" + strings.Join(results, ",") + ")"
}

func compactType(t reflect.Type, ctxType reflect.Type) string {
	if t == ctxType {
		return "ctx"
	}
	name := strings.ReplaceAll(t.String(), "service.", "")
	// Go 1.27 declares json.RawMessage as an alias of jsontext.Value; the
	// contract spells it json.RawMessage, so normalise back.
	name = strings.ReplaceAll(name, "jsontext.Value", "json.RawMessage")
	return name
}

// TestBoundStructsAreSerializable covers the other half of the binding rules:
// only exported fields, every field with a *named* json tag (that is what the
// generated TypeScript, and the already-written frontend, read), no anonymous
// embedding, and nothing a JSON encoder cannot handle.
func TestBoundStructsAreSerializable(t *testing.T) {
	check := func(name string, value any) {
		t.Helper()
		typ := reflect.TypeOf(value)
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			label := name + "." + field.Name
			if field.Anonymous {
				t.Errorf("%s 是匿名嵌入字段；Wails 会跳过它", label)
			}
			if !field.IsExported() {
				t.Errorf("%s 未导出；跨边界不可见", label)
			}
			tag := field.Tag.Get("json")
			tagName := strings.Split(tag, ",")[0]
			if tagName == "" {
				t.Errorf("%s 缺少命名的 json tag（TS 模型不会生成该字段）", label)
				continue
			}
			if tagName != field.Name {
				t.Errorf("%s 的 json tag 是 %q；前端按 Go 字段名读取（PascalCase）", label, tagName)
			}
			switch field.Type.Kind() {
			case reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.Interface:
				t.Errorf("%s 的类型 %s 不能跨边界序列化", label, field.Type)
			}
		}
	}

	check("EnvStatus", EnvStatus{})
	check("ContainerFilter", ContainerFilter{})
	check("RunContainerOptions", RunContainerOptions{})
	check("LogsOptions", LogsOptions{})
	check("ExecOptions", ExecOptions{})
	check("BuildOptions", BuildOptions{})
	check("PruneResult", PruneResult{})
	check("OutputEvent", OutputEvent{})
	check("Task", Task{})
	check("AppSettings", AppSettings{})
	check("PresetImage", PresetImage{})
	check("MirrorProbe", MirrorProbe{})

	// The read models travel across the same boundary.
	check("domain.Container", domain.Container{})
	check("domain.Image", domain.Image{})
	check("domain.Volume", domain.Volume{})
	check("domain.Network", domain.Network{})
	check("domain.ContainerStats", domain.ContainerStats{})
	check("domain.Session", domain.Session{})
	check("domain.SystemInfo", domain.SystemInfo{})
}

// TestFrozenChannelAndEventNames pins the strings the frontend hard-codes.
func TestFrozenChannelAndEventNames(t *testing.T) {
	channels := map[string]string{
		"ChannelLogs":     ChannelLogs,
		"ChannelTerminal": ChannelTerminal,
		"ChannelBuild":    ChannelBuild,
		"ChannelPull":     ChannelPull,
		"ChannelEvents":   ChannelEvents,
		"ChannelTask":     ChannelTask,
	}
	want := map[string]string{
		"ChannelLogs":     "container-logs",
		"ChannelTerminal": "terminal",
		"ChannelBuild":    "build",
		"ChannelPull":     "pull",
		"ChannelEvents":   "events",
		"ChannelTask":     "task",
	}
	for name, got := range channels {
		if got != want[name] {
			t.Errorf("%s = %q，前端常量是 %q", name, got, want[name])
		}
	}
	if EventName != "wslc:output" {
		t.Errorf("EventName = %q，前端订阅的是 %q", EventName, "wslc:output")
	}
	if EnvEventName != "env:status" {
		t.Errorf("EnvEventName = %q，前端订阅的是 %q", EnvEventName, "env:status")
	}
	// Task states are compared lowercase by the frontend.
	for _, state := range []string{TaskRunning, TaskSucceeded, TaskFailed, TaskCanceled} {
		if state != strings.ToLower(state) {
			t.Errorf("任务状态 %q 必须是小写", state)
		}
	}
}
