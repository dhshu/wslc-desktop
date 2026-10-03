package service

import (
	"context"
	"errors"
	"testing"

	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// The fixtures below are verbatim wslc 3.0.1 output captured on this machine.
// They exist because the *shape* of the output is what broke assumptions:
// `volume list --format json` and `network list --format json` emit NDJSON (one
// object per line), not a JSON array, and `container list --format json` prints
// nothing at all when there are no containers.

// Real `wslc network list --format json` output: NDJSON, three built-in
// networks, with Internal/IPv6 as JSON *strings*.
const fixtureNetworkNDJSON = `{"CreatedAt":"2026-10-03 02:09:36.930875148 +0000 UTC","Driver":"bridge","ID":"748d3e61c1b7","IPv4":"true","IPv6":"false","Internal":"false","Labels":"","Name":"bridge","Scope":"local"}
{"CreatedAt":"2026-10-03 02:07:19.75502922 +0000 UTC","Driver":"host","ID":"99c877d5d3de","IPv4":"true","IPv6":"false","Internal":"false","Labels":"","Name":"host","Scope":"local"}
{"CreatedAt":"2026-10-03 02:07:19.742930506 +0000 UTC","Driver":"null","ID":"02ae537d93dd","IPv4":"true","IPv6":"false","Internal":"false","Labels":"","Name":"none","Scope":"local"}`

// Real `wslc volume list --format json` element shape (two entries to exercise
// the NDJSON path).
const fixtureVolumeNDJSON = `{"Availability":"N/A","Driver":"guest","Group":"N/A","Labels":"","Links":"N/A","Mountpoint":"/var/lib/docker/volumes/wslcprobevol/_data","Name":"wslcprobevol","Scope":"local","Size":"N/A","Status":"N/A"}
{"Availability":"N/A","Driver":"guest","Group":"N/A","Labels":"","Links":"N/A","Mountpoint":"/var/lib/docker/volumes/data/_data","Name":"data","Scope":"local","Size":"N/A","Status":"N/A"}`

// TestListNetworksRealNDJSON pins the NDJSON shape of network list.
func TestListNetworksRealNDJSON(t *testing.T) {
	svc, fake, _ := newTestService(t)
	args := []string{"network", "list", "--format", "json"}
	fake.When(args, wslc.Result{Stdout: fixtureNetworkNDJSON}, nil)

	networks, err := svc.ListNetworks(context.Background())
	assertNoFailure(t, err)
	if len(networks) != 3 {
		t.Fatalf("应解析出 3 个网络，实际 %d：%+v", len(networks), networks)
	}
	names := []string{networks[0].Name, networks[1].Name, networks[2].Name}
	assertArgsEqual(t, names, []string{"bridge", "host", "none"})
	if networks[0].Driver != "bridge" || networks[0].Scope != "local" {
		t.Errorf("网络字段不符：%+v", networks[0])
	}
	if networks[0].Internal != "false" || networks[1].IPv6 != "false" {
		t.Errorf("Internal/IPv6 为字符串字段，实际：%+v", networks[0])
	}
}

// TestListVolumesRealNDJSON pins the NDJSON shape of volume list.
func TestListVolumesRealNDJSON(t *testing.T) {
	svc, fake, _ := newTestService(t)
	args := []string{"volume", "list", "--format", "json"}
	fake.When(args, wslc.Result{Stdout: fixtureVolumeNDJSON}, nil)

	volumes, err := svc.ListVolumes(context.Background())
	assertNoFailure(t, err)
	if len(volumes) != 2 {
		t.Fatalf("应解析出 2 个卷，实际 %d：%+v", len(volumes), volumes)
	}
	if volumes[0].Name != "wslcprobevol" || volumes[1].Name != "data" {
		t.Fatalf("卷名不符：%+v", volumes)
	}
	if volumes[0].Driver != "guest" || volumes[0].Scope != "local" {
		t.Errorf("卷字段不符：%+v", volumes[0])
	}
	if volumes[0].Mountpoint == "" {
		t.Error("Mountpoint 应被解析")
	}
}

// TestListEmptyOutputs covers the machine's current state: no containers, no
// images, and `--format json` printing literally nothing.
func TestListEmptyOutputs(t *testing.T) {
	svc, fake, _ := newTestService(t)
	for _, args := range [][]string{
		{"container", "list", "--format", "json"},
		{"container", "list", "--format", "json", "--all"},
		{"image", "list", "--format", "json"},
		{"container", "stats", "--format", "json", "--all"},
		{"volume", "list", "--format", "json"},
		{"network", "list", "--format", "json"},
	} {
		fake.When(args, wslc.Result{Stdout: ""}, nil)
	}

	containers, err := svc.ListContainers(context.Background(), ContainerFilter{All: true})
	assertNoFailure(t, err)
	if containers == nil || len(containers) != 0 {
		t.Errorf("空容器列表应为非 nil 空切片：%v", containers)
	}
	images, err := svc.ListImages(context.Background(), true)
	assertNoFailure(t, err)
	if images == nil || len(images) != 0 {
		t.Errorf("空镜像列表应为非 nil 空切片：%v", images)
	}
	stats, err := svc.ContainerStats(context.Background(), true)
	assertNoFailure(t, err)
	if stats == nil || len(stats) != 0 {
		t.Errorf("空 stats 应为非 nil 空切片：%v", stats)
	}
	volumes, err := svc.ListVolumes(context.Background())
	assertNoFailure(t, err)
	if volumes == nil || len(volumes) != 0 {
		t.Errorf("空卷列表应为非 nil 空切片：%v", volumes)
	}
	networks, err := svc.ListNetworks(context.Background())
	assertNoFailure(t, err)
	if networks == nil || len(networks) != 0 {
		t.Errorf("空网络列表应为非 nil 空切片：%v", networks)
	}
}

// TestInspectRealIndentedJSON covers the real inspect shape: indented JSON
// printed with -f left alone (on these commands -f is a format *template*).
func TestInspectRealIndentedJSON(t *testing.T) {
	svc, fake, _ := newTestService(t)
	indented := "[\n  {\n    \"Id\": \"748d3e61c1b7\",\n    \"Name\": \"/web\"\n  }\n]\n"
	fake.When(inspectArgs(), wslc.Result{Stdout: indented}, nil)
	raw, err := svc.InspectContainer(context.Background(), "web")
	assertNoFailure(t, err)
	if len(raw) == 0 {
		t.Fatal("inspect 应返回原始 JSON")
	}
}

// TestNotFoundErrorMapping checks that wslc's localized not-found text (the
// real message on a zh-CN Windows is `找不到容器 'x'。`) still maps onto the
// adapter's sentinel, which is what the service layer relies on.
func TestNotFoundErrorMapping(t *testing.T) {
	svc, fake, _ := newTestService(t)
	localized := exitError([]string{"container", "start", "ghost"}, 1,
		"找不到容器 'ghost'。\n错误代码： WSLC_E_CONTAINER_NOT_FOUND")
	fake.When(startArgs(), wslc.Result{ExitCode: 1}, localized)

	_, err := svc.StartContainer(context.Background(), "ghost")
	if err == nil {
		t.Fatal("不存在的容器应报错")
	}
	if !errors.Is(err, wslc.ErrNotFound) {
		t.Fatalf("应映射为 ErrNotFound，实际：%v", err)
	}
}
