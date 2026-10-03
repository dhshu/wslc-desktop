//go:build integration

package wslc

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// These tests drive the real wslc.exe on this machine. They are excluded from
// the default `go test` run; use:
//
//	go test -tags integration ./internal/wslc/ -v -run Integration
//
// Container-class commands currently fail with HCS_E_SERVICE_NOT_AVAILABLE
// because the Hyper-V Host Compute Service (vmcompute) is stopped, so those
// cases skip instead of failing.

func integrationRunner(t *testing.T) *ExecRunner {
	t.Helper()
	exe, err := NewResolver().Resolve()
	if err != nil {
		t.Skipf("wslc not installed: %v", err)
	}
	return NewExecRunner(exe, WithTimeout(30*time.Second))
}

func TestIntegrationAvailable(t *testing.T) {
	r := integrationRunner(t)
	if err := r.Available(context.Background()); err != nil {
		t.Fatalf("Available: %v", err)
	}
}

func TestIntegrationVersion(t *testing.T) {
	r := integrationRunner(t)
	res, err := r.Run(context.Background(), Spec{Kind: CmdVersion, Args: []string{"version"}})
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if res.ExitCode != 0 || !strings.Contains(res.Stdout, "wslc") {
		t.Errorf("version output = %q (exit %d)", res.Stdout, res.ExitCode)
	}
	t.Logf("wslc version: %s", strings.TrimSpace(res.Stdout))
}

func TestIntegrationInfoJSON(t *testing.T) {
	r := integrationRunner(t)
	res, err := r.Run(context.Background(), Spec{Kind: CmdInfo, Args: []string{"info", "--format", "json"}})
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	info, err := ParseSystemInfo(res.Stdout)
	if err != nil {
		t.Fatalf("ParseSystemInfo: %v", err)
	}
	if info.Client.Version == "" {
		t.Errorf("Client.Version empty in %+v", info)
	}
	if info.Client.SettingsFile == "" {
		t.Errorf("Client.SettingsFile empty in %+v", info)
	}
	t.Logf("client=%+v server=%+v", info.Client, info.Server)
}

func TestIntegrationInfoTable(t *testing.T) {
	r := integrationRunner(t)
	res, err := r.Run(context.Background(), Spec{Kind: CmdInfo, Args: []string{"info"}})
	if err != nil {
		t.Fatalf("info: %v", err)
	}
	info, err := ParseSystemInfo(res.Stdout)
	if err != nil {
		t.Fatalf("ParseSystemInfo: %v", err)
	}
	if info.Client.Version == "" {
		t.Errorf("Client.Version empty in %+v", info)
	}
	t.Logf("client=%+v sessions=%+v", info.Client, info.Server.Sessions)
}

func TestIntegrationSessionList(t *testing.T) {
	r := integrationRunner(t)
	res, err := r.Run(context.Background(), Spec{Kind: CmdSessionList, Args: []string{"system", "session", "list", "--verbose"}})
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			t.Skipf("vmcompute unavailable: %v", err)
		}
		t.Fatalf("session list: %v", err)
	}
	sessions, err := ParseSessions(res.Stdout)
	if err != nil {
		t.Fatalf("ParseSessions: %v", err)
	}
	if len(sessions) == 0 {
		t.Errorf("no sessions parsed from %q", res.Stdout)
	}
	for _, s := range sessions {
		if s.Name == "" && s.ID == 0 {
			t.Errorf("empty session parsed: %+v (raw %q)", s, res.Stdout)
		}
	}
	t.Logf("sessions=%+v", sessions)
}

func TestIntegrationContainerListSkipsWhenServiceDown(t *testing.T) {
	r := integrationRunner(t)
	res, err := r.Run(context.Background(), Spec{Kind: CmdContainerList, Args: []string{"container", "list", "--all"}})
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			t.Skipf("vmcompute unavailable (expected on this machine): %v", err)
		}
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			t.Skipf("container list rejected by wslc: %v", err)
		}
		t.Fatalf("container list: %v", err)
	}
	if _, err := ParseContainers(res.Stdout); err != nil {
		t.Fatalf("ParseContainers: %v", err)
	}
}

// runList executes a list command, skipping when the container service is down.
func runList(t *testing.T, kind CommandKind, args ...string) Result {
	t.Helper()
	res, err := integrationRunner(t).Run(context.Background(), Spec{Kind: kind, Args: args})
	if err != nil {
		if errors.Is(err, ErrServiceUnavailable) {
			t.Skipf("vmcompute unavailable: %v", err)
		}
		var exitErr *ExitError
		if errors.As(err, &exitErr) {
			t.Skipf("%v rejected: %v", args, err)
		}
		t.Fatalf("%v: %v", args, err)
	}
	return res
}

func TestIntegrationVolumeListNDJSON(t *testing.T) {
	res := runList(t, CmdVolumeList, "volume", "list", "--format", "json")
	vols, err := ParseVolumes(res.Stdout)
	if err != nil {
		t.Fatalf("ParseVolumes(%q): %v", res.Stdout, err)
	}
	for _, v := range vols {
		if v.Name == "" {
			t.Errorf("volume without a name: %+v (raw %q)", v, res.Stdout)
		}
	}
	t.Logf("raw=%q parsed=%d %+v", strings.TrimSpace(res.Stdout), len(vols), vols)
}

func TestIntegrationNetworkListNDJSON(t *testing.T) {
	res := runList(t, CmdNetworkList, "network", "list", "--format", "json")
	nets, err := ParseNetworks(res.Stdout)
	if err != nil {
		t.Fatalf("ParseNetworks(%q): %v", res.Stdout, err)
	}
	if len(nets) == 0 {
		t.Errorf("no networks parsed from %q (wslc always reports bridge/host/none)", res.Stdout)
	}
	for _, n := range nets {
		if n.ID == "" || n.Name == "" || n.Driver == "" {
			t.Errorf("incomplete network: %+v (raw %q)", n, res.Stdout)
		}
	}
	t.Logf("raw=%q parsed=%d %+v", strings.TrimSpace(res.Stdout), len(nets), nets)
}

func TestIntegrationEmptyListCommandsParseToEmptyLists(t *testing.T) {
	// On this machine there are no containers/images (Docker Hub is
	// unreachable), so these commands exit 0 with empty stdout. Empty output
	// must become an empty list, never an error.
	cases := []struct {
		name  string
		kind  CommandKind
		args  []string
		parse func(string) (int, error)
	}{
		{"container list", CmdContainerList, []string{"container", "list", "--all", "--format", "json"}, func(s string) (int, error) {
			v, err := ParseContainers(s)
			return len(v), err
		}},
		{"image list", CmdImageList, []string{"image", "list", "--format", "json"}, func(s string) (int, error) {
			v, err := ParseImages(s)
			return len(v), err
		}},
		{"stats", CmdContainerStats, []string{"stats", "--all", "--format", "json"}, func(s string) (int, error) {
			v, err := ParseStats(s)
			return len(v), err
		}},
	}
	for _, tc := range cases {
		res := runList(t, tc.kind, tc.args...)
		n, err := tc.parse(res.Stdout)
		if err != nil {
			t.Errorf("%s: parse(%q) = %v", tc.name, res.Stdout, err)
			continue
		}
		if strings.TrimSpace(res.Stdout) == "" && n != 0 {
			t.Errorf("%s: empty output parsed as %d items", tc.name, n)
		}
		t.Logf("%s: stdout=%q items=%d exit=%d", tc.name, strings.TrimSpace(res.Stdout), n, res.ExitCode)
	}
}
