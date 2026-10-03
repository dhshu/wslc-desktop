package wslc

import (
	"strings"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
)

// realInfoJSON is the verbatim output of `wslc info --format json` on this
// machine (wslc 3.0.1, WSL 3.0.1.0).
const realInfoJSON = `{"Client":{"Direct3DVersion":"1.611.1-81528511","DxCoreVersion":"10.0.26100.1-240331-1435.ge-release","KernelVersion":"6.18.40.1-1","SettingsFile":"C:\\Users\\test\\AppData\\Local\\wslc\\settings.yaml","Version":"3.0.1.0","WindowsVersion":"10.0.26300.9550"},"Server":{"SessionManagerVersion":"3.0.1","Sessions":[{"CreatorPid":23428,"ID":1,"Name":"wslc-cli-test"}]}}`

// minimalInfoJSON mirrors the shape wslc emits for a client-only answer.
const minimalInfoJSON = `{"Client":{"Version":"3.0.1.0"}}`

// realInfoTable is the verbatim output of `wslc info` (table format, zh-CN UI).
const realInfoTable = `客户端:
WSL 版本: 3.0.1.0
内核版本: 6.18.40.1-1
Direct3D 版本: 1.611.1-81528511
DXCore 版本: 10.0.26100.1-240331-1435.ge-release
Windows 版本: 10.0.26300.9550
设置文件: C:\Users\test\AppData\Local\wslc\settings.yaml

服务器:
会话管理器版本: 3.0.1
会话: 1
ID   创建者 PID   显示名称
1    23428     wslc-cli-test
`

// realSessionList is the verbatim output of
// `wslc system session list --verbose` (zh-CN UI). Note the diagnostic
// "[wslc] ..." line wslc prints before the table.
const realSessionList = `[wslc] Found 1 session
ID   创建者 PID   显示名称
1    23428     wslc-cli-test
`

// copyrightHeader is the two noise lines wslc prepends to --help and some
// table outputs.
const copyrightHeader = "版权所有 (c) Microsoft Corporation。保留所有权利。\n有关此产品的隐私信息，请访问 https://aka.ms/privacy。"

// renderTable mimics wslc's table formatter: every column is padded to the
// widest cell (counted in runes) plus a three space separator, which is what
// the real `wslc system session list` output does.
func renderTable(header []string, rows [][]string) string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = len([]rune(h))
	}
	for _, r := range rows {
		for i, c := range r {
			if i < len(widths) && len([]rune(c)) > widths[i] {
				widths[i] = len([]rune(c))
			}
		}
	}
	var b strings.Builder
	writeRow := func(cells []string) {
		for i := range header {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			b.WriteString(c)
			if i == len(header)-1 {
				break
			}
			b.WriteString(strings.Repeat(" ", widths[i]-len([]rune(c))+3))
		}
		b.WriteString("\n")
	}
	writeRow(header)
	for _, r := range rows {
		writeRow(r)
	}
	return b.String()
}

func containerTableEN() string {
	return copyrightHeader + "\n\n" + renderTable(
		[]string{"CONTAINER ID", "IMAGE", "COMMAND", "CREATED", "STATUS", "PORTS", "NAMES"},
		[][]string{
			{"a1b2c3d4e5f6", "nginx", "run", "2 hours ago", "Up 2 hours", "80/tcp", "web"},
			{"deadbeef0000", "redis", "run", "3 days ago", "Exited (0)", "", "cache"},
		},
	)
}

func TestParseSystemInfoRealJSON(t *testing.T) {
	got, err := ParseSystemInfo(realInfoJSON)
	if err != nil {
		t.Fatalf("ParseSystemInfo: unexpected error: %v", err)
	}
	if got.Client.Version != "3.0.1.0" {
		t.Errorf("Client.Version = %q, want 3.0.1.0", got.Client.Version)
	}
	if got.Client.KernelVersion != "6.18.40.1-1" {
		t.Errorf("Client.KernelVersion = %q", got.Client.KernelVersion)
	}
	if got.Client.Direct3DVersion != "1.611.1-81528511" {
		t.Errorf("Client.Direct3DVersion = %q", got.Client.Direct3DVersion)
	}
	if got.Client.DxCoreVersion != "10.0.26100.1-240331-1435.ge-release" {
		t.Errorf("Client.DxCoreVersion = %q", got.Client.DxCoreVersion)
	}
	if got.Client.WindowsVersion != "10.0.26300.9550" {
		t.Errorf("Client.WindowsVersion = %q", got.Client.WindowsVersion)
	}
	if got.Client.SettingsFile != `C:\Users\test\AppData\Local\wslc\settings.yaml` {
		t.Errorf("Client.SettingsFile = %q", got.Client.SettingsFile)
	}
	if got.Server.SessionManagerVersion != "3.0.1" {
		t.Errorf("Server.SessionManagerVersion = %q", got.Server.SessionManagerVersion)
	}
	if len(got.Server.Sessions) != 1 {
		t.Fatalf("Sessions = %+v, want 1 entry", got.Server.Sessions)
	}
	if s := got.Server.Sessions[0]; s.ID != 1 || s.CreatorPid != 23428 || s.Name != "wslc-cli-test" {
		t.Errorf("Session = %+v", s)
	}
}

func TestParseSystemInfoMinimalJSON(t *testing.T) {
	got, err := ParseSystemInfo(minimalInfoJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.Client.Version != "3.0.1.0" {
		t.Errorf("Client.Version = %q", got.Client.Version)
	}
	if got.Client.KernelVersion != "" || got.Server.SessionManagerVersion != "" {
		t.Errorf("missing fields must stay zero: %+v", got)
	}
}

func TestParseSystemInfoJSONTolerant(t *testing.T) {
	// Unknown fields are ignored, missing sections stay zero, wrong shapes in
	// an unknown section must not fail the parse.
	for _, in := range []string{
		`{"Client":{"Future":"x"},"Server":{}}`,
		`{"Something":42}`,
		`{}`,
		`{"Client":{"Version":3010},"Server":{"Sessions":"none"}}`,
	} {
		if _, err := ParseSystemInfo(in); err != nil {
			t.Errorf("ParseSystemInfo(%s) returned error %v, want tolerant parse", in, err)
		}
	}
}

func TestParseSystemInfoRealTable(t *testing.T) {
	got, err := ParseSystemInfo(realInfoTable)
	if err != nil {
		t.Fatalf("ParseSystemInfo: unexpected error: %v", err)
	}
	if got.Client.Version != "3.0.1.0" {
		t.Errorf("Client.Version = %q", got.Client.Version)
	}
	if got.Client.KernelVersion != "6.18.40.1-1" {
		t.Errorf("Client.KernelVersion = %q", got.Client.KernelVersion)
	}
	if got.Client.Direct3DVersion != "1.611.1-81528511" {
		t.Errorf("Client.Direct3DVersion = %q", got.Client.Direct3DVersion)
	}
	if got.Client.SettingsFile != `C:\Users\test\AppData\Local\wslc\settings.yaml` {
		t.Errorf("Client.SettingsFile = %q", got.Client.SettingsFile)
	}
	if got.Server.SessionManagerVersion != "3.0.1" {
		t.Errorf("Server.SessionManagerVersion = %q", got.Server.SessionManagerVersion)
	}
	if len(got.Server.Sessions) != 1 {
		t.Fatalf("Sessions = %+v, want 1", got.Server.Sessions)
	}
	if s := got.Server.Sessions[0]; s.ID != 1 || s.CreatorPid != 23428 || s.Name != "wslc-cli-test" {
		t.Errorf("Session = %+v", s)
	}
}

func TestParseSystemInfoEmpty(t *testing.T) {
	for _, in := range []string{"", "   \n\n", "\ufeff"} {
		got, err := ParseSystemInfo(in)
		if err != nil {
			t.Errorf("ParseSystemInfo(%q) error = %v, want nil", in, err)
		}
		if got.Client.Version != "" {
			t.Errorf("ParseSystemInfo(%q) = %+v, want zero value", in, got)
		}
	}
}

func TestParseSystemInfoUnknownFormat(t *testing.T) {
	_, err := ParseSystemInfo("totally unknown\nsecond raw line\nthird line")
	if err == nil {
		t.Fatal("expected error for unknown format")
	}
	if !strings.Contains(err.Error(), "totally unknown") || !strings.Contains(err.Error(), "second raw line") {
		t.Errorf("error must quote the first two raw lines, got: %v", err)
	}
	if strings.Contains(err.Error(), "third line") {
		t.Errorf("error must only quote the first two lines, got: %v", err)
	}
}

func TestParseContainersJSON(t *testing.T) {
	in := `[
	  {"ID":"a1b2c3d4e5f6aa","Names":["/web","/web2"],"Image":"nginx:latest","ImageID":"sha256:abc",
	   "Command":"nginx -g daemon off;","CreatedAt":"2024-01-01T00:00:00Z","RunningFor":"2 hours ago",
	   "Status":"Up 2 hours","State":"running","Ports":["80/tcp"],"Size":"1.2MB","Labels":"app=web",
	   "Networks":["bridge"],"Mounts":"vol","UnknownField":"ignored"},
	  {"ID":"bb","Names":"/solo","Image":"redis:7"}
	]`
	got, err := ParseContainers(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	c := got[0]
	if c.ID != "a1b2c3d4e5f6aa" || c.Image != "nginx:latest" || c.ImageID != "sha256:abc" {
		t.Errorf("container = %+v", c)
	}
	if c.Names.String() != "/web, /web2" {
		t.Errorf("Names = %v", c.Names)
	}
	if c.Name() != "web" {
		t.Errorf("Name() = %q, want web", c.Name())
	}
	if !c.IsRunning() {
		t.Error("IsRunning() = false, want true")
	}
	if c.Ports.String() != "80/tcp" || c.Networks.String() != "bridge" {
		t.Errorf("Ports/Networks = %v/%v", c.Ports, c.Networks)
	}
	if c.CreatedAt != "2024-01-01T00:00:00Z" || c.RunningFor != "2 hours ago" || c.Size != "1.2MB" {
		t.Errorf("created/size = %+v", c)
	}
	if got[1].Names.First() != "/solo" || got[1].Image != "redis:7" {
		t.Errorf("single-string Names not tolerated: %+v", got[1])
	}
}

func TestParseContainersJSONWrapperAndBrokenRows(t *testing.T) {
	in := `{"Containers":[{"ID":"ok"},{"ID":{"nested":"bad"}}]}`
	got, err := ParseContainers(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "ok" {
		t.Errorf("got %+v, want the single parsable row", got)
	}
}

func TestParseContainersJSONEmptyArray(t *testing.T) {
	got, err := ParseContainers("[]")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
}

func TestParseContainersInfoJSONIsNotAContainerList(t *testing.T) {
	// A single JSON object that decodes to the zero Container must not be
	// silently accepted as one empty container.
	if got, err := ParseContainers(realInfoJSON); err == nil {
		t.Errorf("want error for info JSON, got %+v", got)
	}
}

func TestParseContainersTable(t *testing.T) {
	got, err := ParseContainers(containerTableEN())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (copyright header and blank lines must be skipped): %+v", len(got), got)
	}
	first := got[0]
	if first.ID != "a1b2c3d4e5f6" || first.Image != "nginx" || first.Command != "run" {
		t.Errorf("row 0 = %+v", first)
	}
	if first.Status != "Up 2 hours" {
		t.Errorf("Status = %q, want %q", first.Status, "Up 2 hours")
	}
	if first.Ports.String() != "80/tcp" {
		t.Errorf("Ports = %v", first.Ports)
	}
	if first.Names.String() != "web" {
		t.Errorf("Names = %v", first.Names)
	}
	if first.RunningFor != "2 hours ago" || first.CreatedAt != "" {
		t.Errorf("relative CREATED must land in RunningFor: %+v", first)
	}
	second := got[1]
	if second.ID != "deadbeef0000" || second.Image != "redis" {
		t.Errorf("row 1 = %+v", second)
	}
	if second.Status != "Exited (0)" {
		t.Errorf("Status = %q", second.Status)
	}
	if len(second.Ports) != 0 {
		t.Errorf("empty PORTS column must stay empty, got %v (its vertical position is empty in the source table)", second.Ports)
	}
	if second.Names.String() != "cache" {
		t.Errorf("Names = %v, want cache (the empty middle column must not shift it into PORTS)", second.Names)
	}
}

func TestParseContainersTableChineseHeaders(t *testing.T) {
	in := renderTable(
		[]string{"容器 ID", "映像", "命令", "已创建", "状态", "端口", "名称"},
		[][]string{{"a1b2c3d4e5f6", "nginx", "run", "2 hours ago", "Up 2 hours", "80/tcp", "web"}},
	)
	got, err := ParseContainers(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	c := got[0]
	if c.ID != "a1b2c3d4e5f6" || c.Image != "nginx" || c.Status != "Up 2 hours" || c.Names.String() != "web" {
		t.Errorf("container = %+v", c)
	}
}

func TestParseContainersTableGreedyLastColumn(t *testing.T) {
	// The status text contains a two-space run, so the row splits into more
	// cells than the header has columns. Everything from the start of the last
	// matched cell must be kept verbatim in the last column instead of being
	// truncated to the first token.
	in := renderTable([]string{"ID", "NAME", "STATUS"}, nil) + "abc   web   Up  2 hours\n"
	got, err := ParseContainers(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	if got[0].ID != "abc" || got[0].Names.String() != "web" {
		t.Errorf("leading columns = %+v", got[0])
	}
	if got[0].Status != "Up  2 hours" {
		t.Errorf("Status = %q, want %q (greedy fill of the remaining text)", got[0].Status, "Up  2 hours")
	}
}

func TestParseContainersUnknownExtraColumnIsIgnored(t *testing.T) {
	in := renderTable([]string{"ID", "NAME", "MYSTERY COLUMN"}, [][]string{{"abc", "web", "whatever"}})
	got, err := ParseContainers(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != "abc" || got[0].Names.String() != "web" {
		t.Errorf("got %+v", got)
	}
}

func TestParseContainersEmptyAndHeaderOnly(t *testing.T) {
	cases := map[string]string{
		"empty":       "",
		"whitespace":  "   \n\t\n",
		"header only": renderTable([]string{"CONTAINER ID", "IMAGE", "NAMES"}, nil),
		"noise only":  copyrightHeader + "\n\n",
		"crlf + bom":  "\ufeff" + strings.ReplaceAll(containerTableEN(), "\n", "\r\n"),
	}
	for name, in := range cases {
		got, err := ParseContainers(in)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}
		if name == "crlf + bom" {
			if len(got) != 2 {
				t.Errorf("%s: len = %d, want 2", name, len(got))
			}
			continue
		}
		if len(got) != 0 {
			t.Errorf("%s: len = %d, want 0 (%+v)", name, len(got), got)
		}
	}
}

func TestParseContainersUnknownFormat(t *testing.T) {
	_, err := ParseContainers("completely unexpected output\nsecond line here\nthird")
	if err == nil {
		t.Fatal("expected error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "completely unexpected output") || !strings.Contains(msg, "second line here") {
		t.Errorf("error must contain the first 2 raw lines, got: %v", err)
	}
	if strings.Contains(msg, "third") {
		t.Errorf("error must stop at the first 2 lines, got: %v", err)
	}
}

func TestParseImagesTableAndJSON(t *testing.T) {
	table := renderTable(
		[]string{"REPOSITORY", "TAG", "DIGEST", "IMAGE ID", "CREATED", "SIZE"},
		[][]string{{"nginx", "latest", "sha256:abc", "a1b2c3d4e5f6", "2 weeks ago", "187MB"}},
	)
	got, err := ParseImages(copyrightHeader + "\n" + table)
	if err != nil {
		t.Fatalf("table: unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("table: len = %d, want 1", len(got))
	}
	img := got[0]
	if img.Repository != "nginx" || img.Tag != "latest" || img.Reference() != "nginx:latest" {
		t.Errorf("image = %+v (reference %q)", img, img.Reference())
	}
	if img.Digest != "sha256:abc" || img.ID != "a1b2c3d4e5f6" || img.Size != "187MB" {
		t.Errorf("image = %+v", img)
	}
	if img.CreatedSince != "2 weeks ago" || img.CreatedAt != "" {
		t.Errorf("relative CREATED must land in CreatedSince: %+v", img)
	}

	js, err := ParseImages(`[{"ID":"aa","Repository":"redis","Tag":"<none>","CreatedAt":"2024-02-02T10:00:00Z","Size":"10MB"}]`)
	if err != nil {
		t.Fatalf("json: unexpected error: %v", err)
	}
	if len(js) != 1 || js[0].Reference() != "redis" || js[0].CreatedAt != "2024-02-02T10:00:00Z" {
		t.Errorf("json image = %+v", js)
	}
}

func TestParseVolumesTable(t *testing.T) {
	in := renderTable(
		[]string{"DRIVER", "VOLUME NAME", "SCOPE", "SIZE"},
		[][]string{{"guest", "myvol", "local", "1.5GB"}},
	)
	got, err := ParseVolumes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	v := got[0]
	if v.Name != "myvol" || v.Driver != "guest" || v.Scope != "local" || v.Size != "1.5GB" {
		t.Errorf("volume = %+v", v)
	}

	js, err := ParseVolumes(`{"Volumes":[{"Name":"vol2","Driver":"vhd","Mountpoint":"C:\\x","Scope":"local"}]}`)
	if err != nil || len(js) != 1 || js[0].Name != "vol2" || js[0].Mountpoint != `C:\x` {
		t.Errorf("json volume = %+v (err %v)", js, err)
	}
}

func TestParseNetworksTable(t *testing.T) {
	in := renderTable(
		[]string{"NETWORK ID", "NAME", "DRIVER", "SCOPE"},
		[][]string{{"n1b2c3", "bridge", "bridge", "local"}},
	)
	got, err := ParseNetworks(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	n := got[0]
	if n.ID != "n1b2c3" || n.Name != "bridge" || n.Driver != "bridge" || n.Scope != "local" {
		t.Errorf("network = %+v", n)
	}
}

func TestParseStatsTable(t *testing.T) {
	in := renderTable(
		[]string{"CONTAINER ID", "NAME", "CPU %", "MEM USAGE / LIMIT", "MEM %", "NET I/O", "BLOCK I/O", "PIDS"},
		[][]string{{"a1b2", "web", "0.50%", "1.5MiB / 1GiB", "0.15%", "1.2kB / 0B", "0B / 0B", "12"}},
	)
	got, err := ParseStats(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	s := got[0]
	if s.ID != "a1b2" || s.Name != "web" || s.CPUPerc != "0.50%" {
		t.Errorf("stats = %+v", s)
	}
	if s.MemUsage != "1.5MiB / 1GiB" || s.MemPerc != "0.15%" {
		t.Errorf("mem = %+v", s)
	}
	if s.NetIO != "1.2kB / 0B" || s.BlockIO != "0B / 0B" {
		t.Errorf("io = %+v", s)
	}
	if s.PIDs != 12 {
		t.Errorf("PIDs = %d, want 12", s.PIDs)
	}
}

func TestParseStatsChineseHeadersAndNonNumericPIDs(t *testing.T) {
	in := renderTable(
		[]string{"容器 ID", "名称", "CPU 百分比", "最大用量/限制", "内存百分比", "网络 I/O", "块 I/O", "PIDS"},
		[][]string{{"a1b2", "web", "1.00%", "2MiB / 1GiB", "0.20%", "0B / 0B", "0B / 0B", "-"}},
	)
	got, err := ParseStats(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].CPUPerc != "1.00%" || got[0].MemUsage != "2MiB / 1GiB" || got[0].NetIO != "0B / 0B" {
		t.Errorf("stats = %+v", got[0])
	}
	if got[0].PIDs != 0 {
		t.Errorf("unparsable PIDs must be 0, got %d", got[0].PIDs)
	}
}

func TestParseSessionsRealOutput(t *testing.T) {
	got, err := ParseSessions(realSessionList)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	s := got[0]
	if s.ID != 1 || s.CreatorPid != 23428 || s.Name != "wslc-cli-test" {
		t.Errorf("session = %+v", s)
	}
}

func TestParseSessionsEnglishAndJSON(t *testing.T) {
	en := renderTable(
		[]string{"ID", "CREATOR PID", "DISPLAY NAME"},
		[][]string{{"2", "4242", "wslc-cli-other"}},
	)
	got, err := ParseSessions(en)
	if err != nil {
		t.Fatalf("table: unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].ID != 2 || got[0].CreatorPid != 4242 || got[0].Name != "wslc-cli-other" {
		t.Errorf("table sessions = %+v", got)
	}

	js, err := ParseSessions(`{"Sessions":[{"ID":3,"Name":"x","CreatorPid":7}]}`)
	if err != nil {
		t.Fatalf("json: unexpected error: %v", err)
	}
	if len(js) != 1 || js[0].ID != 3 || js[0].Name != "x" || js[0].CreatorPid != 7 {
		t.Errorf("json sessions = %+v", js)
	}
}

func TestParseSessionsEmptyAndHeaderOnly(t *testing.T) {
	for _, in := range []string{"", renderTable([]string{"ID", "CREATOR PID", "DISPLAY NAME"}, nil), "[wslc] Found 0 sessions\n"} {
		got, err := ParseSessions(in)
		if err != nil {
			t.Errorf("ParseSessions(%q) error = %v", in, err)
			continue
		}
		if len(got) != 0 {
			t.Errorf("ParseSessions(%q) = %+v, want empty", in, got)
		}
	}
}

func TestParseSessionsUnknownFormat(t *testing.T) {
	_, err := ParseSessions("nonsense here\nmore nonsense")
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "nonsense here") {
		t.Errorf("error must quote raw output: %v", err)
	}
}

func TestParseContainersTolerantOfPortsAndNamesLists(t *testing.T) {
	in := renderTable(
		[]string{"CONTAINER ID", "NAMES", "PORTS"},
		[][]string{{"abc123", "web,web-second", "0.0.0.0:8080->80/tcp, :::8080->80/tcp"}},
	)
	got, err := ParseContainers(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].Names.String() != "web, web-second" {
		t.Errorf("Names = %v", got[0].Names)
	}
	if got[0].Ports.String() != "0.0.0.0:8080->80/tcp, :::8080->80/tcp" {
		t.Errorf("Ports = %v", got[0].Ports)
	}
}

func TestParseContainersReturnsNonNilEmptySlice(t *testing.T) {
	for _, in := range []string{"", renderTable([]string{"CONTAINER ID", "NAMES"}, nil)} {
		got, err := ParseContainers(in)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got == nil {
			t.Errorf("ParseContainers(%q) returned a nil slice, want empty slice", in)
		}
	}
}

// --- NDJSON ---------------------------------------------------------------
//
// wslc 3.0.1's `--format json` list commands print one JSON object per line,
// NOT a JSON array. The constants below are byte-for-byte copies of real
// output captured on this machine.

const realVolumeListNDJSON = `{"Availability":"N/A","Driver":"guest","Group":"N/A","Labels":"","Links":"N/A","Mountpoint":"/var/lib/docker/volumes/wslcprobevol/_data","Name":"wslcprobevol","Scope":"local","Size":"N/A","Status":"N/A"}`

const realNetworkListNDJSON = `{"CreatedAt":"2026-10-03 02:07:19.786917485 +0000 UTC","Driver":"bridge","ID":"be47264221f2","IPv4":"true","IPv6":"false","Internal":"false","Labels":"","Name":"bridge","Scope":"local"}
{"CreatedAt":"2026-10-03 02:07:19.75502922 +0000 UTC","Driver":"host","ID":"99c877d5d3de","IPv4":"true","IPv6":"false","Internal":"false","Labels":"","Name":"host","Scope":"local"}
{"CreatedAt":"2026-10-03 02:07:19.742930506 +0000 UTC","Driver":"null","ID":"02ae537d93dd","IPv4":"true","IPv6":"false","Internal":"false","Labels":"","Name":"none","Scope":"local"}`

func TestParseVolumesNDJSON(t *testing.T) {
	for name, in := range map[string]string{
		"raw":           realVolumeListNDJSON,
		"trailing LF":   realVolumeListNDJSON + "\n",
		"CRLF":          strings.ReplaceAll(realVolumeListNDJSON, "\n", "\r\n") + "\r\n",
		"trailing ws":   realVolumeListNDJSON + "  \n\t\n",
		"leading blank": "\n" + realVolumeListNDJSON,
	} {
		got, err := ParseVolumes(in)
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}
		if len(got) != 1 {
			t.Errorf("%s: len = %d, want 1 (%+v)", name, len(got), got)
			continue
		}
		v := got[0]
		if v.Name != "wslcprobevol" || v.Driver != "guest" || v.Scope != "local" {
			t.Errorf("%s: volume = %+v", name, v)
		}
		if v.Mountpoint != "/var/lib/docker/volumes/wslcprobevol/_data" {
			t.Errorf("%s: Mountpoint = %q", name, v.Mountpoint)
		}
		if v.Size != "N/A" || v.Labels != "" {
			t.Errorf("%s: size/labels = %+v", name, v)
		}
	}
}

func TestParseNetworksNDJSON(t *testing.T) {
	got, err := ParseNetworks(realNetworkListNDJSON)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len = %d, want 3 (NDJSON must be decoded per line): %+v", len(got), got)
	}
	want := []struct {
		id, driver, name string
	}{
		{"be47264221f2", "bridge", "bridge"},
		{"99c877d5d3de", "host", "host"},
		{"02ae537d93dd", "null", "none"},
	}
	for i, w := range want {
		n := got[i]
		if n.ID != w.id || n.Driver != w.driver || n.Name != w.name {
			t.Errorf("network %d = {ID:%q Driver:%q Name:%q}, want %+v", i, n.ID, n.Driver, n.Name, w)
		}
		if n.Scope != "local" || n.CreatedAt == "" {
			t.Errorf("network %d = %+v, want scope/createdAt filled", i, n)
		}
	}
	// The string-typed IPv4/IPv6/Internal fields must survive too.
	if got[0].IPv6 != "false" || got[0].Internal != "false" {
		t.Errorf("network 0 = %+v", got[0])
	}
}

func TestParseNDJSONToleratesBlankAndBrokenLines(t *testing.T) {
	broken := realVolumeListNDJSON[:40] + " not json at all"
	in := strings.Join([]string{
		"",
		realVolumeListNDJSON,
		"   ",
		broken,
		strings.ReplaceAll(realVolumeListNDJSON, "wslcprobevol", "secondvol"),
		"\t",
	}, "\n")
	got, err := ParseVolumes(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2 (a broken line must be skipped, not the whole list): %+v", len(got), got)
	}
	if got[0].Name != "wslcprobevol" || got[1].Name != "secondvol" {
		t.Errorf("volumes = %+v", got)
	}
}

func TestParseNDJSONMultiLineObjects(t *testing.T) {
	// A streaming decoder must win over naive \n splitting: pretty-printed
	// objects are still one JSON value each.
	in := `{
  "ID": 1,
  "Name": "pretty"
}
{
  "ID": 2,
  "Name": "printed"
}`
	got, err := ParseSessions(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "pretty" || got[1].Name != "printed" {
		t.Errorf("sessions = %+v", got)
	}
	if got[0].ID != 1 || got[1].ID != 2 {
		t.Errorf("sessions = %+v, want IDs 1 and 2", got)
	}
}

func TestParseContainersNDJSON(t *testing.T) {
	in := `{"ID":"a1b2c3d4e5f6","Names":["/web"],"Image":"nginx:latest","State":"running","Status":"Up 2 hours"}
{"ID":"deadbeef0000","Names":"/cache","Image":"redis:7","State":"exited","Status":"Exited (0) 3 days ago"}`
	got, err := ParseContainers(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].ID != "a1b2c3d4e5f6" || got[0].Image != "nginx:latest" || !got[0].IsRunning() {
		t.Errorf("container 0 = %+v", got[0])
	}
	if got[1].ID != "deadbeef0000" || got[1].Names.First() != "/cache" || got[1].IsRunning() {
		t.Errorf("container 1 = %+v", got[1])
	}
}

func TestParseImagesNDJSON(t *testing.T) {
	in := `{"ID":"sha256:aa","Repository":"nginx","Tag":"latest","Size":"187MB"}
{"ID":"sha256:bb","Repository":"redis","Tag":"7","Size":"130MB"}`
	got, err := ParseImages(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Reference() != "nginx:latest" || got[1].Reference() != "redis:7" {
		t.Errorf("images = %+v", got)
	}
}

func TestParseStatsNDJSON(t *testing.T) {
	in := `{"ID":"a1b2","Name":"web","CPUPerc":"0.50%","MemUsage":"1.5MiB / 1GiB","MemPerc":"0.15%","NetIO":"1.2kB / 0B","BlockIO":"0B / 0B","PIDs":12}
{"ID":"c3d4","Name":"db","CPUPerc":"0.00%","MemUsage":"1MiB / 1GiB","MemPerc":"0.10%","NetIO":"0B / 0B","BlockIO":"0B / 0B","PIDs":"7"}`
	got, err := ParseStats(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].PIDs != 12 || got[0].CPUPerc != "0.50%" {
		t.Errorf("stats 0 = %+v", got[0])
	}
	if got[1].PIDs != 7 || got[1].Name != "db" {
		t.Errorf("stats 1 = %+v (PIDs is a tolerant Int64)", got[1])
	}
}

func TestParseNDJSONNotJSONFallsBackToTable(t *testing.T) {
	// None of these is JSON, so the parser must NOT invent an empty list: it
	// must fall through to the table parser and report an unrecognized format.
	for _, in := range []string{
		"{ this is not json\nsecond raw line\nthird",
		"{wslc} container list\nsecond raw line",
		"{",
		"{ \"Name\": \"x\" \n not json either \n more",
	} {
		got, err := ParseVolumes(in)
		if err == nil {
			t.Errorf("ParseVolumes(%q) = %+v, want an unrecognized-format error", in, got)
			continue
		}
		if !strings.Contains(err.Error(), "unrecognized") {
			t.Errorf("ParseVolumes(%q) error = %v, want an unrecognized-format error", in, err)
		}
	}
}

func TestParseNDJSONRecoversAfterBrokenLineWithMultiLineObjects(t *testing.T) {
	// A pretty-printed object followed by a broken line and then another
	// object: both objects must survive (the decoder resyncs at the next
	// newline instead of giving up on the stream).
	in := `{
  "ID": 1,
  "Name": "pretty"
}
{ this is broken
{ "ID": 2, "Name": "after" }`
	got, err := ParseSessions(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "pretty" || got[1].Name != "after" {
		t.Errorf("sessions = %+v, want pretty and after", got)
	}
}

func TestParseNDJSONMultipleValuesOnOneLine(t *testing.T) {
	in := `{"ID":1,"Name":"one"} {"ID":2,"Name":"two"}`
	got, err := ParseSessions(in)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 || got[0].Name != "one" || got[1].Name != "two" {
		t.Errorf("sessions = %+v", got)
	}
}

func TestParseVolumesEmptyOutputIsEmptyList(t *testing.T) {
	// Real behavior when no volume exists: the command exits 0 with no output.
	for _, in := range []string{"", "\n", "  \n\n", "\ufeff"} {
		got, err := ParseVolumes(in)
		if err != nil {
			t.Errorf("ParseVolumes(%q) error = %v, want nil", in, err)
			continue
		}
		if len(got) != 0 {
			t.Errorf("ParseVolumes(%q) = %+v, want empty", in, got)
		}
		if got == nil {
			t.Errorf("ParseVolumes(%q) returned nil, want an empty slice", in)
		}
	}
}

var _ = domain.Container{}
