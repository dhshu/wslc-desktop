package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// waitForCall polls the fake runner until at least n calls have been recorded
// or the deadline passes. PullImage starts its stream in a goroutine, so the
// call is not visible synchronously.
func waitForCall(t *testing.T, fake *wslc.FakeRunner, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(fake.Calls()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("等待 %d 次调用超时，实际 %d 次", n, len(fake.Calls()))
}

// TestApplyMirrorRewrite covers every documented rewrite rule, including the
// cases that must NOT be rewritten (own registries, digests, unknown shapes).
func TestApplyMirrorRewrite(t *testing.T) {
	tests := []struct {
		name   string
		ref    string
		mirror string
		want   string
	}{
		{"official short tag", "alpine:3.20", "docker.m.daocloud.io", "docker.m.daocloud.io/library/alpine:3.20"},
		{"official short no tag", "nginx", "docker.m.daocloud.io", "docker.m.daocloud.io/library/nginx"},
		{"official with library prefix", "library/alpine:3.20", "docker.m.daocloud.io", "docker.m.daocloud.io/library/alpine:3.20"},
		{"docker hub namespace", "bitnami/redis:7", "docker.m.daocloud.io", "docker.m.daocloud.io/bitnami/redis:7"},
		{"docker.io explicit", "docker.io/bitnami/redis:7", "docker.m.daocloud.io", "docker.m.daocloud.io/bitnami/redis:7"},
		{"own registry quay", "quay.io/organization/img:v1", "docker.m.daocloud.io", "quay.io/organization/img:v1"},
		{"own registry ghcr", "ghcr.io/org/img:1", "docker.m.daocloud.io", "ghcr.io/org/img:1"},
		{"own registry gcr", "gcr.io/distroless/base:latest", "docker.m.daocloud.io", "gcr.io/distroless/base:latest"},
		{"own registry ecr", "public.ecr.aws/docker/library/alpine:3", "docker.m.daocloud.io", "public.ecr.aws/docker/library/alpine:3"},
		{"own registry mcr", "mcr.microsoft.com/azure-storage/cli:v1", "docker.m.daocloud.io", "mcr.microsoft.com/azure-storage/cli:v1"},
		{"registry alias rewritten", "registry-1.docker.io/library/a:1", "docker.m.daocloud.io", "docker.m.daocloud.io/library/a:1"},
		{"already rewritten", "docker.m.daocloud.io/library/alpine:3.20", "docker.m.daocloud.io", "docker.m.daocloud.io/library/alpine:3.20"},
		{"digest never rewritten", "alpine@sha256:" + strings.Repeat("a", 64), "docker.m.daocloud.io", "alpine@sha256:" + strings.Repeat("a", 64)},
		{"empty ref", "", "docker.m.daocloud.io", ""},
		{"empty mirror", "alpine:3.20", "", "alpine:3.20"},
		{"scheme stripped from mirror", "alpine:3.20", "https://docker.xuanyuan.me/", "docker.xuanyuan.me/library/alpine:3.20"},
		{"already at a different mirror kept", "docker.1panel.live/library/a:1", "docker.m.daocloud.io", "docker.1panel.live/library/a:1"},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := applyMirrorRewrite(tc.ref, tc.mirror); got != tc.want {
				t.Errorf("applyMirrorRewrite(%q, %q) = %q, want %q", tc.ref, tc.mirror, got, tc.want)
			}
		})
	}
}

// TestValidateSettingsNormalizes confirms UI input is clamped safely.
func TestValidateSettingsNormalizes(t *testing.T) {
	in := AppSettings{
		MirrorEndpoint: " https://docker.xuanyuan.me/",
		MirrorEnabled:  true,
		CustomMirrors:  []string{"a.example", "a.example", "  ", "b.example"},
	}
	out, err := validateSettings(in)
	if err != nil {
		t.Fatalf("validateSettings 返回错误：%v", err)
	}
	if out.MirrorEndpoint != "docker.xuanyuan.me" {
		t.Errorf("MirrorEndpoint = %q, want docker.xuanyuan.me", out.MirrorEndpoint)
	}
	if len(out.CustomMirrors) != 2 || out.CustomMirrors[0] != "a.example" || out.CustomMirrors[1] != "b.example" {
		t.Errorf("CustomMirrors 去重失败：%v", out.CustomMirrors)
	}
	if out.SchemaVersion != settingsSchemaVersion {
		t.Errorf("SchemaVersion = %d", out.SchemaVersion)
	}
}

// TestValidateSettingsRejectsEmptyEndpoint guards the one input that must fail.
func TestValidateSettingsRejectsEmptyEndpoint(t *testing.T) {
	if _, err := validateSettings(AppSettings{MirrorEndpoint: "   "}); err == nil {
		t.Fatal("空镜像地址应返回错误")
	}
	if _, err := validateSettings(AppSettings{MirrorEndpoint: "x"}); err != nil {
		t.Fatalf("正常地址不应返回错误：%v", err)
	}
}

// TestSettingsLoadSaveRoundTrip writes to a temp file and reads it back, which
// is the only part of this module that actually touches disk.
func TestSettingsLoadSaveRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	s := NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})

	first, err := s.Load()
	if err != nil {
		t.Fatalf("首次 Load 应返回默认值，得到错误：%v", err)
	}
	if first.MirrorEndpoint != "docker.m.daocloud.io" {
		t.Errorf("默认镜像 = %q, want docker.m.daocloud.io", first.MirrorEndpoint)
	}
	if !first.MirrorEnabled {
		t.Error("默认应启用镜像改写")
	}

	in := AppSettings{
		MirrorEnabled:  false,
		MirrorEndpoint: "docker.1panel.live",
		CustomMirrors:  []string{"docker.io", "docker.m.daocloud.io"},
	}
	saved, err := s.Save(in)
	if err != nil {
		t.Fatalf("Save 失败：%v", err)
	}
	if saved.MirrorEndpoint != "docker.1panel.live" {
		t.Errorf("Save 返回值不符：%+v", saved)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("设置文件未写入：%v", err)
	}

	fresh := NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})
	loaded, err := fresh.Load()
	if err != nil {
		t.Fatalf("重新 Load 失败：%v", err)
	}
	if len(loaded.CustomMirrors) != 2 {
		t.Errorf("CustomMirrors 未持久化：%v", loaded.CustomMirrors)
	}
}

// TestSettingsLoadCorruptFileFallsBack guarantees a damaged JSON cannot break
// app startup: the UI must still open and report defaults.
func TestSettingsLoadCorruptFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})
	data, err := s.Load()
	if err != nil {
		t.Fatalf("损坏文件应回退默认值，得到错误：%v", err)
	}
	if data.MirrorEndpoint != "docker.m.daocloud.io" {
		t.Errorf("回退后镜像 = %q", data.MirrorEndpoint)
	}
}

// TestServiceLoadSaveSettings binds the service methods to the Settings store.
func TestServiceLoadSaveSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	svc, _, _ := newTestService(t)
	svc.settings = NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})

	got, err := svc.LoadSettings(context.Background())
	if err != nil {
		t.Fatalf("LoadSettings 失败：%v", err)
	}
	_ = got

	saved, err := svc.SaveSettings(context.Background(), AppSettings{
		MirrorEnabled:  true,
		MirrorEndpoint: "docker.1panel.live",
	})
	if err != nil {
		t.Fatalf("SaveSettings 失败：%v", err)
	}
	if saved.MirrorEndpoint != "docker.1panel.live" {
		t.Errorf("MirrorEndpoint = %q", saved.MirrorEndpoint)
	}

	again, err := svc.LoadSettings(context.Background())
	if err != nil {
		t.Fatalf("再次 LoadSettings 失败：%v", err)
	}
	if again.MirrorEndpoint != "docker.1panel.live" {
		t.Errorf("设置未持久化：%+v", again)
	}

	if _, err := svc.SaveSettings(context.Background(), AppSettings{MirrorEndpoint: "  "}); err == nil {
		t.Error("空镜像地址应返回错误")
	}
}

// TestActiveMirrorHonoursToggle verifies EnvCheck reports the effective mirror.
func TestActiveMirrorHonoursToggle(t *testing.T) {
	svc, _, _ := newTestService(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	svc.settings = NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})

	if got := svc.activeMirror(); got != "docker.m.daocloud.io" {
		t.Errorf("默认 activeMirror = %q", got)
	}
	if _, err := svc.SaveSettings(context.Background(), AppSettings{MirrorEnabled: false, MirrorEndpoint: "docker.xuanyuan.me"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.activeMirror(); got != "" {
		t.Errorf("禁用后 activeMirror 应为空，得到 %q", got)
	}
	if _, err := svc.SaveSettings(context.Background(), AppSettings{MirrorEnabled: true, MirrorEndpoint: "https://docker.xuanyuan.me/"}); err != nil {
		t.Fatal(err)
	}
	if got := svc.activeMirror(); got != "docker.xuanyuan.me" {
		t.Errorf("启用后 activeMirror = %q, want docker.xuanyuan.me", got)
	}
}

// TestServiceNilSettingsStore confirms the service layer degrades gracefully
// when no Settings store is wired.
func TestServiceNilSettingsStore(t *testing.T) {
	svc, _, _ := newTestService(t)
	svc.settings = nil

	if _, err := svc.LoadSettings(context.Background()); err != nil {
		t.Fatalf("nil store 的 LoadSettings 应返回默认值：%v", err)
	}
	if _, err := svc.SaveSettings(context.Background(), AppSettings{MirrorEndpoint: "x"}); err != nil {
		t.Fatalf("nil store 的 SaveSettings 应直接返回：%v", err)
	}
	if got := svc.activeMirror(); got != "" {
		t.Errorf("nil store 的 activeMirror 应为空：%q", got)
	}
	if got := svc.settingsPath(); got != "" {
		t.Errorf("nil store 的 settingsPath 应为空：%q", got)
	}
}

// TestPullImageUsesConfiguredMirror verifies PullImage rewrites the reference
// through the active mirror and records the rewritten value in the task.
func TestPullImageUsesConfiguredMirror(t *testing.T) {
	svc, fake, _ := newTestService(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	svc.settings = NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})

	if _, err := svc.SaveSettings(context.Background(), AppSettings{
		MirrorEnabled:  true,
		MirrorEndpoint: "docker.xuanyuan.me",
	}); err != nil {
		t.Fatal(err)
	}

	fake.When([]string{"image", "pull", "docker.xuanyuan.me/library/alpine:3.20"}, wslc.Result{
		ExitCode: 0,
		Stdout:   "alpine: Pulling from library/alpine\nsha256:abc: Pull complete\nStatus: Downloaded newer image",
	}, nil)
	id, err := svc.PullImage(context.Background(), "alpine:3.20")
	if err != nil {
		t.Fatalf("PullImage 失败：%v", err)
	}
	if id == "" {
		t.Error("PullImage 应返回任务 ID")
	}
	waitForCall(t, fake, 1)
	assertOnlyCall(t, fake.Calls(), []string{"image", "pull", "docker.xuanyuan.me/library/alpine:3.20"})
}

// TestPullImageSkipsRewriteWhenDisabled confirms the user can turn mirroring
// off and pull from Docker Hub directly.
func TestPullImageSkipsRewriteWhenDisabled(t *testing.T) {
	svc, fake, _ := newTestService(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	svc.settings = NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})

	if _, err := svc.SaveSettings(context.Background(), AppSettings{MirrorEnabled: false, MirrorEndpoint: "docker.xuanyuan.me"}); err != nil {
		t.Fatal(err)
	}

	fake.When([]string{"image", "pull", "alpine:3.20"}, wslc.Result{ExitCode: 1, Stderr: "timeout"}, nil)
	_, _ = svc.PullImage(context.Background(), "alpine:3.20")
	waitForCall(t, fake, 1)
	assertOnlyCall(t, fake.Calls(), []string{"image", "pull", "alpine:3.20"})
}

// TestPullImageKeepsOwnRegistry ensures an image already pointing at its own
// registry is never double-rewritten.
func TestPullImageKeepsOwnRegistry(t *testing.T) {
	svc, fake, _ := newTestService(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	svc.settings = NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})
	if _, err := svc.SaveSettings(context.Background(), AppSettings{MirrorEnabled: true, MirrorEndpoint: "docker.xuanyuan.me"}); err != nil {
		t.Fatal(err)
	}

	fake.When([]string{"image", "pull", "ghcr.io/org/img:1"}, wslc.Result{ExitCode: 0, Stdout: "ok"}, nil)
	_, _ = svc.PullImage(context.Background(), "ghcr.io/org/img:1")
	waitForCall(t, fake, 1)
	assertOnlyCall(t, fake.Calls(), []string{"image", "pull", "ghcr.io/org/img:1"})
}

// TestTestMirrorReportsSuccess exercises the fake-runner success path.
func TestTestMirrorReportsSuccess(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When([]string{"image", "pull", "docker.xuanyuan.me/library/hello-world:latest"}, wslc.Result{
		ExitCode: 0,
		Stdout:   "hello-world: Pulling from library/hello-world\nsha256:" + strings.Repeat("b", 64) + "\nPull complete\nStatus: Downloaded newer image",
	}, nil)
	probe, err := svc.TestMirror(context.Background(), "https://docker.xuanyuan.me/")
	if err != nil {
		t.Fatalf("TestMirror 返回错误：%v", err)
	}
	if !probe.OK {
		t.Errorf("应报告成功：%+v", probe)
	}
	if probe.Endpoint != "docker.xuanyuan.me" {
		t.Errorf("Endpoint = %q, 方案/尾斜杠应被剥离", probe.Endpoint)
	}
	if probe.TargetRef != "docker.xuanyuan.me/library/hello-world:latest" {
		t.Errorf("TargetRef = %q", probe.TargetRef)
	}
}

// TestTestMirrorReportsFailure turns a wslc failure into a failed probe, not an
// error, so the UI can render a red row.
func TestTestMirrorReportsFailure(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When([]string{"image", "pull", "dead.example/library/hello-world:latest"}, wslc.Result{
		ExitCode: 1, Stderr: "lookup dead.example: no such host",
	}, nil)
	probe, err := svc.TestMirror(context.Background(), "dead.example")
	if err != nil {
		t.Fatalf("失败探针不应返回错误：%v", err)
	}
	if probe.OK {
		t.Errorf("应报告失败：%+v", probe)
	}
	if probe.Message == "" {
		t.Error("失败探针必须给出可读原因")
	}
}

// TestTestMirrorRejectsEmptyInput avoids spawning a wslc call for an empty host.
func TestTestMirrorRejectsEmptyInput(t *testing.T) {
	svc, fake, _ := newTestService(t)
	probe, err := svc.TestMirror(context.Background(), "   ")
	if err != nil {
		t.Fatalf("空输入不应返回错误：%v", err)
	}
	if probe.OK {
		t.Error("空输入应报告失败")
	}
	if len(fake.Calls()) != 0 {
		t.Fatalf("空输入不应执行任何命令，实际 %d", len(fake.Calls()))
	}
}

// TestMirrorProbeDoesNotReturnError guards the contract the UI relies on:
// TestMirror always returns (probe, nil).
func TestMirrorProbeDoesNotReturnError(t *testing.T) {
	svc, fake, _ := newTestService(t)
	fake.When([]string{"image", "pull", "a.example/library/hello-world:latest"}, wslc.Result{ExitCode: 2, Stderr: "x"}, nil)
	for _, input := range []string{"a.example", "", "  ", "http://b.example", "b.example"} {
		probe, err := svc.TestMirror(context.Background(), input)
		if err != nil {
			t.Errorf("TestMirror(%q) 不应返回错误：%v", input, err)
		}
		if strings.TrimSpace(input) == "" {
			if probe.OK {
				t.Errorf("空输入应失败：%+v", probe)
			}
		}
	}
}

// TestShortDigestExtractsTheHash confirms the probe message shows a digest.
func TestShortDigestExtractsTheHash(t *testing.T) {
	got := shortDigest("status\nDigest: sha256:" + strings.Repeat("c", 64))
	want := "sha256:" + strings.Repeat("c", 19)
	if got != want {
		t.Errorf("shortDigest = %q, want %q", got, want)
	}
	if shortDigest("nothing here") != "" {
		t.Error("无 digest 时应返回空")
	}
}

// TestNormalizeEndpointStripsSchemeAndSlash is the shared cleanup helper.
func TestNormalizeEndpointStripsSchemeAndSlash(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"docker.m.daocloud.io", "docker.m.daocloud.io"},
		{" https://docker.xuanyuan.me/", "docker.xuanyuan.me"},
		{"http://a.example/", "a.example"},
		{"  b.example  ", "b.example"},
		{"", ""},
		{"   ", ""},
	} {
		if got := normalizeEndpoint(tc.in); got != tc.want {
			t.Errorf("normalizeEndpoint(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestBuiltInMirrorsSeedsCustomMirrors guarantees the defaults contain the
// built-in list and that it starts with Docker Hub.
func TestBuiltInMirrorsSeedsCustomMirrors(t *testing.T) {
	got := BuiltInMirrors()
	if len(got) == 0 {
		t.Fatal("BuiltInMirrors 不应为空")
	}
	if got[0] != "docker.io" {
		t.Errorf("首个内置镜像应为 docker.io，实际 %q", got[0])
	}
	found := map[string]bool{}
	for _, e := range got {
		found[e] = true
	}
	for _, want := range []string{"docker.io", "docker.m.daocloud.io", "docker.xuanyuan.me", "docker.1panel.live"} {
		if !found[want] {
			t.Errorf("内置镜像缺少 %q", want)
		}
	}
}

// TestDefaultSettingsAreSane confirms the shipped defaults are usable.
func TestDefaultSettingsAreSane(t *testing.T) {
	s := NewSettingsFromEnv(nil)
	d := s.defaults()
	if d.SchemaVersion != settingsSchemaVersion {
		t.Errorf("SchemaVersion = %d", d.SchemaVersion)
	}
	if !d.MirrorEnabled {
		t.Error("默认应启用镜像")
	}
	if d.MirrorEndpoint != "docker.m.daocloud.io" {
		t.Errorf("默认镜像 = %q", d.MirrorEndpoint)
	}
	if len(d.CustomMirrors) != len(DefaultMirrors) {
		t.Errorf("CustomMirrors = %d, want %d", len(d.CustomMirrors), len(DefaultMirrors))
	}
}

// TestEnvCheckReportsActiveMirror ties the settings into EnvStatus.
func TestEnvCheckReportsActiveMirror(t *testing.T) {
	svc, _, _ := newEnvFake(t)
	dir := t.TempDir()
	path := filepath.Join(dir, "s.json")
	svc.settings = NewSettingsFromEnv([]string{"WSLC_DESKTOP_SETTINGS=" + path, "APPDATA=" + dir})
	if _, err := svc.SaveSettings(context.Background(), AppSettings{MirrorEnabled: true, MirrorEndpoint: "docker.xuanyuan.me"}); err != nil {
		t.Fatal(err)
	}
	status, err := svc.EnvCheck(context.Background())
	if err != nil {
		t.Fatalf("EnvCheck 失败：%v", err)
	}
	if status.ActiveMirror != "docker.xuanyuan.me" {
		t.Errorf("ActiveMirror = %q", status.ActiveMirror)
	}
	if status.SettingsPath != path {
		t.Errorf("SettingsPath = %q, want %q", status.SettingsPath, path)
	}
}

// TestSplitHostSeparatesRegistryFromRepository checks the host detection that
// decides whether "nginx" is a host or a namespace.
func TestSplitHostSeparatesRegistryFromRepository(t *testing.T) {
	for _, tc := range []struct {
		ref      string
		wantHost string
		wantRest string
	}{
		{"alpine:3.20", "", "alpine:3.20"},
		{"docker.io/library/alpine:1", "docker.io", "library/alpine:1"},
		{"docker.io/bitnami/redis:7", "docker.io", "bitnami/redis:7"},
		{"bitnami/redis:7", "", "bitnami/redis:7"},
		{"localhost/a:1", "localhost", "a:1"},
		{"myregistry:5000/a:1", "myregistry:5000", "a:1"},
	} {
		host, rest := splitHost(tc.ref)
		if host != tc.wantHost || rest != tc.wantRest {
			t.Errorf("splitHost(%q) = (%q, %q), want (%q, %q)", tc.ref, host, rest, tc.wantHost, tc.wantRest)
		}
	}
}

// TestDefaultSettingsSeedsPresetImages confirms the built-in preset list is
// available on first launch without the user having to add anything.
func TestDefaultSettingsSeedsPresetImages(t *testing.T) {
	d := defaultSettings()
	if len(d.PresetImages) == 0 {
		t.Fatal("默认设置应自带常用镜像预设清单")
	}
	if d.PresetImages[0].Ref != "mysql:8.4" {
		t.Errorf("第一个预设 = %q, want mysql:8.4", d.PresetImages[0].Ref)
	}
}

// TestValidateSettingsNormalizesPresetImages trims, drops empty refs, and
// de-duplicates so a fat-fingered paste cannot blow up the images dropdown.
func TestValidateSettingsNormalizesPresetImages(t *testing.T) {
	in := AppSettings{
		MirrorEndpoint: "docker.io",
		PresetImages: []PresetImage{
			{Label: "  MySQL  ", Ref: "  mysql:8.4  "},
			{Label: "", Ref: "mysql:8.4"},     // 重复 ref → 丢弃
			{Label: "Redis", Ref: ""},          // 空 ref → 丢弃
			{Label: "Redis", Ref: "redis:7"},
			{Ref: "redis:8"},                   // 无 label → 回填 ref 作为 label
		},
	}
	got, err := validateSettings(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PresetImages) != 3 {
		t.Fatalf("PresetImages = %d 项，want 3：%+v", len(got.PresetImages), got.PresetImages)
	}
	if got.PresetImages[0].Label != "MySQL" || got.PresetImages[0].Ref != "mysql:8.4" {
		t.Errorf("第一项未修剪：%+v", got.PresetImages[0])
	}
	if got.PresetImages[1].Ref != "redis:7" {
		t.Errorf("第二项 = %+v", got.PresetImages[1])
	}
	if got.PresetImages[2].Label != "redis:8" {
		t.Errorf("第三项 label 未回填：%+v", got.PresetImages[2])
	}
}
