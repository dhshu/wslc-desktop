package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// Settings persistence path.
//
// WSLC itself has NO proxy and NO registry-mirror configuration (verified:
// writing `proxy:` or `registryMirrors:` to settings.yaml makes wslc warn
// "unknown setting", the wslc.exe binary contains no proxy/mirror strings, and
// `wslc image pull` exposes only -a/-q. See microsoft/WSL#40951, still open).
//
// So this app keeps its own settings JSON and applies them the only two ways
// that actually work on wslc 3.x:
//
//   1. registry mirrors -> rewrite the image reference before pulling
//      (pull docker.m.daocloud.io/library/alpine:3.20 instead of alpine:3.20);
//   2. proxy -> inject HTTP_PROXY/HTTPS_PROXY into containers at run time via
//      `-e`, because wslc passes container env vars through but does not accept
//      a proxy setting of its own.
//
// The file lives next to the user's wslc settings so the two are easy to find
// together, and it never modifies wslc's own file.
const settingsFileBaseName = "wslc-desktop-settings.json"

// settingsSchemaVersion guards forward-compatible reads.
const settingsSchemaVersion = 1

// settingsFileName is the on-disk file name. Exported so the tests can build the
// exact path without repeating the constant.
func settingsFileName() string { return settingsFileBaseName }

// AppSettings is the user-editable configuration owned by this app.
type AppSettings struct {
	SchemaVersion int `json:"SchemaVersion"`

	// MirrorEnabled turns automatic image-name rewriting on or off.
	MirrorEnabled bool `json:"MirrorEnabled"`
	// MirrorEndpoint is the active mirror host, e.g. "docker.m.daocloud.io".
	MirrorEndpoint string `json:"MirrorEndpoint"`

	// CustomMirrors is the user's editable list of registry endpoints.
	CustomMirrors []string `json:"CustomMirrors"`

	// PresetImages is the user's editable list of "one-click pull" shortcuts.
	// Each entry is a friendly label plus the full image reference. The images
	// view renders them as a dropdown and the settings view as editable chips.
	PresetImages []PresetImage `json:"PresetImages"`
}

// PresetImage is one entry of AppSettings.PresetImages. PascalCase json tags
// match the rest of AppSettings so Wails generates a named TypeScript model.
type PresetImage struct {
	Label string `json:"Label"`
	Ref   string `json:"Ref"`
}

// defaults returns a copy of the built-in settings.
func (s *Settings) defaults() AppSettings {
	return defaultSettings()
}

// defaultSettings returns a copy of the built-in settings without touching any
// store.
func defaultSettings() AppSettings {
	return AppSettings{
		SchemaVersion:  settingsSchemaVersion,
		MirrorEnabled:  true,
		// The fastest verified endpoint from a mainland-China connection.
		// docker.io is listed first in DefaultMirrors for clarity but is not the
		// default because it times out without a proxy.
		MirrorEndpoint: "docker.m.daocloud.io",
		CustomMirrors:  append([]string{}, BuiltInMirrors()...),
		PresetImages:   append([]PresetImage{}, DefaultPresetImages...),
	}
}

// settingsPath resolves the JSON location, honouring WSLC_DESKTOP_SETTINGS for
// tests.
func settingsPath(env map[string]string) (string, error) {
	if v := strings.TrimSpace(env["WSLC_DESKTOP_SETTINGS"]); v != "" {
		return v, nil
	}
	if base := strings.TrimSpace(env["APPDATA"]); base != "" {
		return filepath.Join(base, settingsFileName()), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("service: 无法定位设置文件目录: %w", err)
	}
	return filepath.Join(home, ".wslc-desktop", settingsFileName()), nil
}

// builtInMirror is one row of the built-in registry list. Fields are exported
// so the built-in table can be marshalled straight into the settings view.
type builtInMirror struct {
	Endpoint string `json:"Endpoint"`
	Label    string `json:"Label"`
	Note     string `json:"Note"`
}

// DefaultMirrors is the built-in registry list, ordered so the fastest verified
// endpoint comes first. Notes are shown verbatim in the UI, so they must stay
// accurate and actionable.
var DefaultMirrors = []builtInMirror{
	{Endpoint: "docker.io", Label: "Docker Hub 官方", Note: "国内通常直连超时（实测 15s 超时），需配合代理"},
	{Endpoint: "docker.m.daocloud.io", Label: "DaoCloud（默认）", Note: "实测最快，3.2s 拉取 hello-world"},
	{Endpoint: "docker.xuanyuan.me", Label: "幻象网络", Note: "实测可用，5.3s"},
	{Endpoint: "docker.1panel.live", Label: "1Panel", Note: "实测可用，9.1s"},
	{Endpoint: "dockerhub.timeweb.cloud", Label: "Timeweb", Note: "2026-10 实测超时，可用性不稳定"},
	{Endpoint: "dockerproxy.com", Label: "dockerproxy", Note: "2026-10 实测超时，可用性不稳定"},
	{Endpoint: "docker.mirrors.ustc.edu.cn", Label: "中科大", Note: "高校镜像站，2024 起多次下线"},
	{Endpoint: "hub-mirror.c.163.com", Label: "网易", Note: "已停止服务"},
	{Endpoint: "mirror.bjtu.edu.cn", Label: "北交大", Note: "已停止服务"},
	{Endpoint: "docker.nju.edu.cn", Label: "南京大学", Note: "已停止服务"},
	{Endpoint: "mirror.qiniu.com", Label: "七牛", Note: "已停止服务"},
	{Endpoint: "<your-id>.mirror.aliyuncs.com", Label: "阿里云加速器", Note: "需替换为自己的专属地址（控制台获取）"},
}

// DefaultPresetImages is the built-in "one-click pull" list: the images a
// developer actually pulls day-to-day. Order matters — the first five show at
// the top of the images view dropdown. Users can add, remove, or reorder
// freely; changes persist to the settings JSON.
var DefaultPresetImages = []PresetImage{
	{"MySQL 8.4", "mysql:8.4"},
	{"Oracle XE 23ai", "gvenzl/oracle-xe:23"},
	{"PostgreSQL 17", "postgres:17"},
	{"Redis 7", "redis:7"},
	{"Nacos v2.5.1", "nacos/nacos-server:v2.5.1"},
	{"MariaDB 11", "mariadb:11"},
	{"MongoDB 8", "mongo:8"},
	{"Kafka 7.8", "confluentinc/cp-kafka:7.8.0"},
	{"Zookeeper 3.9", "zookeeper:3.9"},
	{"Schema Registry", "confluentinc/cp-schema-registry:7.8.0"},
	{"Elasticsearch 8.17", "elasticsearch:8.17.0"},
	{"Kibana 8.17", "kibana:8.17.0"},
	{"OpenSearch 2.18", "opensearchproject/opensearch:2.18"},
	{"MinIO", "minio/minio:latest"},
	{"Jenkins LTS", "jenkins/jenkins:lts"},
	{"Harbor", "goharbor/harbor:latest"},
	{"Nginx 1.27", "nginx:1.27"},
	{"Node.js 22", "node:22"},
	{"Python 3.12", "python:3.12"},
	{"Golang 1.23", "golang:1.23"},
	{"Temurin 21 JDK", "eclipse-temurin:21-jdk"},
	{"Consul 1.19", "hashicorp/consul:1.19"},
	{"etcd 3.5", "bitnami/etcd:3.5"},
	{"RabbitMQ 3.13", "rabbitmq:3.13"},
	{"Airflow 2.10", "apache/airflow:2.10"},
	{"Grafana 11", "grafana/grafana:11"},
	{"Prometheus", "prom/prometheus:v2.53.0"},
	{"Apache Doris 2.1", "apache/doris:2.1.0"},
	{"ClickHouse", "clickhouse/clickhouse-server:24"},
	{"TiDB 8.1", "pingcap/tidb:8.1"},
}

// BuiltInMirrors returns the endpoint list only, used to seed CustomMirrors.
func BuiltInMirrors() []string {
	out := make([]string, 0, len(DefaultMirrors))
	for _, m := range DefaultMirrors {
		out = append(out, m.Endpoint)
	}
	return out
}

// mirrorLabel looks up the friendly name for an endpoint.
func mirrorLabel(endpoint string) string {
	for _, m := range DefaultMirrors {
		if m.Endpoint == endpoint {
			return m.Label
		}
	}
	return ""
}

// mirrorNote looks up the hint text for an endpoint.
func mirrorNote(endpoint string) string {
	for _, m := range DefaultMirrors {
		if m.Endpoint == endpoint {
			return m.Note
		}
	}
	return ""
}

// Settings owns the AppSettings JSON: read-once lazy load, write-through, and
// the pure image-reference rewriting function the service layer calls.
type Settings struct {
	mu     sync.Mutex
	path   string
	env    map[string]string
	data   AppSettings
	loaded bool
}

// NewSettings builds a Settings store from the current process environment.
func NewSettings() *Settings {
	return NewSettingsFromEnv(os.Environ())
}

// NewSettingsFromEnv is the injectable variant tests use.
func NewSettingsFromEnv(env []string) *Settings {
	m := make(map[string]string, len(env))
	for _, kv := range env {
		if i := strings.IndexByte(kv, '='); i >= 0 {
			m[kv[:i]] = kv[i+1:]
		}
	}
	s := &Settings{env: m, data: AppSettings{SchemaVersion: settingsSchemaVersion}}
	return s
}

// LoadSettings reads the current user settings (or built-in defaults on first
// run) and reports them.
func (s *Service) LoadSettings(ctx context.Context) (AppSettings, error) {
	if s == nil || s.settings == nil {
		return defaultSettings(), nil
	}
	return s.settings.Load()
}

// SaveSettings validates and persists user settings.
func (s *Service) SaveSettings(ctx context.Context, in AppSettings) (AppSettings, error) {
	out, err := validateSettings(in)
	if err != nil {
		return AppSettings{}, err
	}
	if s == nil || s.settings == nil {
		return out, nil
	}
	return s.settings.Save(out)
}

// activeMirror returns the effective mirror host for PullImage rewriting:
// "" when mirroring is disabled.
func (s *Service) activeMirror() string {
	if s == nil || s.settings == nil {
		return ""
	}
	data := s.settings.Get()
	if !data.MirrorEnabled {
		return ""
	}
	return normalizeEndpoint(data.MirrorEndpoint)
}

// settingsPath returns the JSON path for display, tolerating a missing store.
func (s *Service) settingsPath() string {
	if s == nil || s.settings == nil {
		return ""
	}
	return s.settings.SettingsFile()
}

// currentSettings returns the live settings, never nil-panic and never erroring.
func (s *Service) currentSettings() AppSettings {
	if s == nil || s.settings == nil {
		return defaultSettings()
	}
	return s.settings.Get()
}

// rewriteImageRef applies the configured registry mirror to one image reference.
//
// It is deliberately idempotent: an already-rewritten reference, a reference
// that names its own registry, or a digest reference all come back unchanged.
func (s *Service) rewriteImageRef(ref string) string {
	mirror := s.activeMirror()
	if mirror == "" {
		return ref
	}
	return applyMirrorRewrite(ref, mirror)
}

// TestMirror probes one endpoint with a tiny public image and reports timing.
// It is deliberately tolerant: any wslc error becomes a failed probe, not a
// service error, so the UI can show a red row instead of a toast.
func (s *Service) TestMirror(ctx context.Context, endpoint string) (MirrorProbe, error) {
	host := strings.TrimSpace(endpoint)
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	host = strings.TrimRight(host, "/")
	if host == "" {
		return MirrorProbe{Endpoint: endpoint, OK: false, DurationMS: 0, Message: "镜像地址为空"}, nil
	}
	// Ports are legal: some mirror endpoints (for example Aliyun's dedicated
	// accelerators) are addressed as host:port. Whitespace and control
	// characters remain invalid.
	if strings.ContainsAny(host, " \t\r\n\x00") {
		return MirrorProbe{Endpoint: endpoint, OK: false, Message: "镜像地址含非法字符"}, nil
	}
	probeRef := host + "/library/hello-world:latest"
	spec := wslc.Spec{
		Kind:    wslc.CmdImagePull,
		Args:    []string{"image", "pull", probeRef},
		Timeout: 30 * time.Second,
	}
	start := time.Now()
	res, err := s.run(ctx, spec)
	probe := MirrorProbe{Endpoint: host, TargetRef: probeRef, DurationMS: time.Since(start).Milliseconds()}
	if err != nil {
		probe.OK = false
		probe.Message = oneLine(firstErrorText(err, res))
		return probe, nil
	}
	if strings.Contains(res.Stdout, "Pull complete") || strings.Contains(res.Stdout, "Downloaded newer") || strings.Contains(res.Stdout, "Image is up to date") {
		probe.OK = true
		probe.Message = "拉取成功（" + shortDigest(res.Stdout) + "）"
	} else {
		probe.OK = false
		probe.Message = "wslc 退出码为 0 但未见拉取完成标志：" + oneLine(firstErrorText(nil, res))
	}
	return probe, nil
}

// MirrorProbe is the result of TestMirror.
type MirrorProbe struct {
	Endpoint   string `json:"Endpoint"`
	TargetRef  string `json:"TargetRef"`
	OK         bool   `json:"OK"`
	DurationMS int64  `json:"DurationMS"`
	Message    string `json:"Message"`
}

// validateSettings clamps untrusted UI input into a safe AppSettings value.
func validateSettings(in AppSettings) (AppSettings, error) {
	out := in
	out.SchemaVersion = settingsSchemaVersion
	out.MirrorEndpoint = normalizeEndpoint(out.MirrorEndpoint)
	if out.MirrorEndpoint == "" {
		return AppSettings{}, errors.New("service: 镜像地址不能为空")
	}

	if out.CustomMirrors == nil {
		out.CustomMirrors = []string{}
	}
	seen := make(map[string]bool, len(out.CustomMirrors))
	filtered := out.CustomMirrors[:0:0]
	for _, m := range out.CustomMirrors {
		m = normalizeEndpoint(m)
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		filtered = append(filtered, m)
	}
	out.CustomMirrors = filtered

	// PresetImages: trim, drop empty refs, drop duplicate refs (keeping the
	// first occurrence), and cap the list so a fat-fingered paste cannot blow
	// up the images view dropdown.
	if out.PresetImages == nil {
		out.PresetImages = []PresetImage{}
	}
	maxPresets := 200
	if len(out.PresetImages) > maxPresets {
		out.PresetImages = out.PresetImages[:maxPresets]
	}
	pseen := make(map[string]bool, len(out.PresetImages))
	pfiltered := out.PresetImages[:0:0]
	for _, p := range out.PresetImages {
		p.Label = strings.TrimSpace(p.Label)
		p.Ref = strings.TrimSpace(p.Ref)
		if p.Ref == "" || pseen[p.Ref] {
			continue
		}
		if p.Label == "" {
			p.Label = p.Ref
		}
		pseen[p.Ref] = true
		pfiltered = append(pfiltered, p)
	}
	out.PresetImages = pfiltered

	return out, nil
}

// normalizeEndpoint strips scheme and trailing slash from a registry host.
func normalizeEndpoint(v string) string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "https://")
	v = strings.TrimPrefix(v, "http://")
	v = strings.TrimRight(v, "/")
	return v
}

// applyMirrorRewrite rewrites an image reference through the configured mirror.
//
// Rules:
//
//	alpine:3.20                    -> <mirror>/library/alpine:3.20
//	nginx                          -> <mirror>/library/nginx
//	library/alpine:3.20            -> <mirror>/library/alpine:3.20
//	bitnami/redis:7                -> <mirror>/bitnami/redis:7
//	quay.io/organization/img:v1    -> unchanged (own registry)
//	ghcr.io/org/img:1              -> unchanged (own registry)
//	public.ecr.aws/docker/lib/a:3  -> unchanged (own registry)
//	<mirror>/library/a:t            -> unchanged (already rewritten)
//
// Digest references (image@sha256:...) are never rewritten: they already name
// an exact digest and pulling them through a mirror would change the target.
//
// Any reference whose host is not a recognised Docker Hub alias is left alone.
// The mirror only stands in for Docker Hub.
func applyMirrorRewrite(ref, mirror string) string {
	if strings.TrimSpace(ref) == "" || strings.TrimSpace(mirror) == "" {
		return ref
	}
	mirror = normalizeEndpoint(mirror)
	if mirror == "" {
		return ref
	}
	// Digest references are pulled by digest; never rewrite them.
	if strings.Contains(ref, "@") {
		return ref
	}

	host, rest := splitHost(ref)
	// Already rewritten through this mirror, or pointing at some other
	// registry (mirror only substitutes for Docker Hub).
	if host == mirror {
		return ref
	}
	if host != "" && !dockerHubHost(host) {
		return ref
	}
	// No explicit host, or a Docker Hub alias: rewrite.
	if host == "" {
		// alpine:3.20 -> <mirror>/library/alpine:3.20
		// library/alpine:3.20 -> <mirror>/library/alpine:3.20
		// bitnami/redis:7 -> <mirror>/bitnami/redis:7
		if strings.HasPrefix(rest, "library/") {
			return mirror + "/" + rest
		}
		if strings.IndexByte(rest, '/') < 0 {
			// Official library image, single component.
			return mirror + "/library/" + rest
		}
		return mirror + "/" + rest
	}
	// host is a Docker Hub alias.
	if strings.HasPrefix(rest, "library/") {
		return mirror + "/" + rest
	}
	if strings.IndexByte(rest, '/') < 0 {
		return mirror + "/library/" + rest
	}
	return mirror + "/" + rest
}

// splitHost separates the registry host from the repository path.
//
// "alpine:3.20" has no registry host (Docker Hub implicit).
// "docker.io/library/alpine:1" has host docker.io.
// "bitnami/redis:7" has NO host — "bitnami" is a Docker Hub namespace.
// "ghcr.io/org/img:1" has host ghcr.io.
//
// The rule: the first path component is a registry host only if it contains a
// dot, a colon (port), or is literally "localhost". Otherwise it is a namespace.
func splitHost(ref string) (host, rest string) {
	slash := strings.IndexByte(ref, '/')
	if slash < 0 {
		return "", ref
	}
	first := ref[:slash]
	if strings.ContainsAny(first, ".:") || first == "localhost" {
		return first, ref[slash+1:]
	}
	return "", ref
}

// dockerHubHost reports whether host is Docker Hub or one of its aliases.
func dockerHubHost(host string) bool {
	switch host {
	case "docker.io", "index.docker.io", "registry-1.docker.io", "localhost":
		return true
	}
	return false
}

// shortDigest pulls the first digest-looking token out of wslc output for the
// probe message, or "" when none is present.
func shortDigest(out string) string {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "sha256:") {
			start := strings.Index(line, "sha256:")
			d := line[start:]
			// sha256: + 20 hex chars is long enough to identify a digest.
			if len(d) > 26 {
				d = d[:26]
			}
			return d
		}
	}
	return ""
}

// firstErrorText prefers the error, else the runner's stderr.
func firstErrorText(err error, res wslc.Result) error {
	if err != nil {
		return err
	}
	if strings.TrimSpace(res.Stderr) != "" {
		return errors.New(res.Stderr)
	}
	return errors.New(strings.TrimSpace(res.Stdout))
}

// SettingsFile returns the JSON path for the environment tab.
func (s *Settings) SettingsFile() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path != "" {
		return s.path
	}
	p, err := settingsPath(s.env)
	if err != nil {
		return ""
	}
	s.path = p
	return p
}

// Load returns the current settings, loading from disk on first use.
func (s *Settings) Load() (AppSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.data, nil
	}
	s.data = AppSettings{SchemaVersion: settingsSchemaVersion}
	path, err := settingsPath(s.env)
	if err != nil {
		s.data = s.defaults()
		s.loaded = true
		return s.data, nil
	}
	s.path = path
	b, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			// A corrupt file must not block the app; fall back to defaults.
			s.data = s.defaults()
			s.loaded = true
			return s.data, nil
		}
		s.data = s.defaults()
		s.loaded = true
		return s.data, nil
	}
	var decoded AppSettings
	if err := json.Unmarshal(b, &decoded); err != nil {
		s.data = s.defaults()
		s.loaded = true
		return s.data, nil
	}
	decoded.SchemaVersion = settingsSchemaVersion
	s.data = decoded
	s.loaded = true
	return s.data, nil
}

// Save validates, persists, and returns the stored value.
func (s *Settings) Save(in AppSettings) (AppSettings, error) {
	out, err := validateSettings(in)
	if err != nil {
		return AppSettings{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = out
	s.loaded = true
	path := s.path
	if path == "" {
		p, err := settingsPath(s.env)
		if err != nil {
			return AppSettings{}, err
		}
		s.path = p
		path = p
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return AppSettings{}, fmt.Errorf("service: 无法创建设置目录 %s: %w", dir, err)
		}
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return AppSettings{}, fmt.Errorf("service: 序列化设置失败: %w", err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		return AppSettings{}, fmt.Errorf("service: 写入设置失败 %s: %w", path, err)
	}
	return out, nil
}

// Get returns a copy of the current settings without writing to disk.
//
// It lazily loads on first use but must not recurse into Load (which takes the
// same mutex) — the load path is therefore inlined here.
func (s *Settings) Get() AppSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loaded {
		return s.data
	}
	s.data = AppSettings{SchemaVersion: settingsSchemaVersion}
	path, err := settingsPath(s.env)
	if err != nil {
		s.data = defaultSettings()
		s.loaded = true
		return s.data
	}
	if s.path == "" {
		s.path = path
	}
	b, err := os.ReadFile(path)
	if err != nil {
		s.data = defaultSettings()
		s.loaded = true
		return s.data
	}
	var decoded AppSettings
	if err := json.Unmarshal(b, &decoded); err != nil {
		s.data = defaultSettings()
		s.loaded = true
		return s.data
	}
	decoded.SchemaVersion = settingsSchemaVersion
	s.data = decoded
	s.loaded = true
	return s.data
}
