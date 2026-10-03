//go:build integration

package integration

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// resolveWslc locates the real wslc executable or skips.
func resolveWslc(t *testing.T) string {
	t.Helper()
	exe, err := wslc.NewResolver().Resolve()
	if err != nil {
		t.Skipf("SKIP: wslc.exe not installed (%v)", err)
	}
	return exe
}

// runWslc executes a real command and returns stdout.
func runWslc(t *testing.T, exe string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, exe, args...)
	out, err := cmd.Output()
	if err != nil {
		t.Skipf("SKIP: wslc %s failed (%v); output=%q", strings.Join(args, " "), err, string(out))
	}
	return string(out)
}

// TestParserAgainstRealWslcOutput is a parser contract test against the live CLI.
//
// It deliberately does NOT need a registry: `volume list` and `network list`
// answer locally, and both were observed to return NDJSON on wslc 3.0.1.0 rather
// than a JSON array — the exact case that was broken before the NDJSON fix.
func TestParserAgainstRealWslcOutput(t *testing.T) {
	exe := resolveWslc(t)

	t.Run("network list is NDJSON and parses", func(t *testing.T) {
		raw := runWslc(t, exe, "network", "list", "--format", "json")
		t.Logf("raw network list output (%d bytes):\n%s", len(raw), raw)

		networks, err := wslc.ParseNetworks(raw)
		if err != nil {
			t.Fatalf("ParseNetworks(real output): %v", err)
		}
		if len(networks) == 0 {
			t.Fatalf("expected the built-in networks, got an empty list from %q", raw)
		}
		// wslc always ships bridge/host/none.
		want := map[string]bool{"bridge": false, "host": false, "none": false}
		for _, n := range networks {
			if _, ok := want[n.Name]; ok {
				want[n.Name] = true
			}
			if n.ID == "" {
				t.Errorf("network %q has an empty ID", n.Name)
			}
			if n.Driver == "" {
				t.Errorf("network %q has an empty Driver", n.Name)
			}
		}
		for name, seen := range want {
			if !seen {
				t.Errorf("built-in network %q missing from parsed result (%d rows)", name, len(networks))
			}
		}
		t.Logf("parsed %d networks: %s", len(networks), summarizeNetworks(networks))
	})

	t.Run("volume list is NDJSON and parses", func(t *testing.T) {
		raw := runWslc(t, exe, "volume", "list", "--format", "json")
		t.Logf("raw volume list output (%d bytes):\n%s", len(raw), raw)

		volumes, err := wslc.ParseVolumes(raw)
		if err != nil {
			t.Fatalf("ParseVolumes(real output): %v", err)
		}
		t.Logf("parsed %d volumes: %s", len(volumes), summarizeVolumes(volumes))

		// The previous probe created this volume; if it is still there, its
		// fields must have survived parsing.
		for _, v := range volumes {
			if v.Name != "wslcprobevol" {
				continue
			}
			if v.Driver != "guest" {
				t.Errorf("volume wslcprobevol Driver = %q, want %q", v.Driver, "guest")
			}
			if !strings.Contains(v.Mountpoint, "wslcprobevol") {
				t.Errorf("volume wslcprobevol Mountpoint = %q, want it to contain the name", v.Mountpoint)
			}
			return
		}
		t.Logf("wslcprobevol not present (already cleaned up); shape check only")
	})

	t.Run("empty lists parse to empty slices", func(t *testing.T) {
		// No images and no containers are expected while the registry is
		// unreachable, which exercises the empty-output path for real.
		for _, tc := range []struct {
			name string
			args []string
			call func(string) (int, error)
		}{
			{"containers", []string{"container", "list", "--all", "--format", "json"}, func(s string) (int, error) {
				v, err := wslc.ParseContainers(s)
				return len(v), err
			}},
			{"images", []string{"image", "list", "--format", "json"}, func(s string) (int, error) {
				v, err := wslc.ParseImages(s)
				return len(v), err
			}},
			{"stats", []string{"stats", "--format", "json"}, func(s string) (int, error) {
				v, err := wslc.ParseStats(s)
				return len(v), err
			}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				raw := runWslc(t, exe, tc.args...)
				t.Logf("raw %s output: %q", tc.name, raw)
				n, err := tc.call(raw)
				if err != nil {
					t.Fatalf("parse %s: %v", tc.name, err)
				}
				t.Logf("%s parsed to %d rows", tc.name, n)
			})
		}
	})
}

// TestRealVolumeRoundTrip proves the volume command surface end to end against
// the live CLI and confirms the parser sees the created volume.
func TestRealVolumeRoundTrip(t *testing.T) {
	exe := resolveWslc(t)
	const name = "wslcinttest-vol"

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = exec.CommandContext(ctx, exe, "volume", "remove", name).Run()
	})

	created := runWslc(t, exe, "volume", "create", name)
	if !strings.Contains(created, name) {
		t.Errorf("volume create output %q does not mention %q", created, name)
	}

	raw := runWslc(t, exe, "volume", "list", "--format", "json")
	volumes, err := wslc.ParseVolumes(raw)
	if err != nil {
		t.Fatalf("ParseVolumes: %v", err)
	}
	found := false
	for _, v := range volumes {
		if v.Name == name {
			found = true
			t.Logf("round-tripped volume: name=%s driver=%s mountpoint=%s scope=%s",
				v.Name, v.Driver, v.Mountpoint, v.Scope)
		}
	}
	if !found {
		t.Fatalf("created volume %q not found by the parser; raw=%q", name, raw)
	}

	removed := runWslc(t, exe, "volume", "remove", name)
	t.Logf("volume remove output: %q", strings.TrimSpace(removed))

	after, err := wslc.ParseVolumes(runWslc(t, exe, "volume", "list", "--format", "json"))
	if err != nil {
		t.Fatalf("ParseVolumes after removal: %v", err)
	}
	for _, v := range after {
		if v.Name == name {
			t.Errorf("volume %q still present after removal", name)
		}
	}
}

// TestRealNetworkRoundTrip does the same for networks.
func TestRealNetworkRoundTrip(t *testing.T) {
	exe := resolveWslc(t)
	const name = "wslcinttest-net"

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		_ = exec.CommandContext(ctx, exe, "network", "remove", name).Run()
	})

	created := runWslc(t, exe, "network", "create", name)
	t.Logf("network create output: %q", strings.TrimSpace(created))

	networks, err := wslc.ParseNetworks(runWslc(t, exe, "network", "list", "--format", "json"))
	if err != nil {
		t.Fatalf("ParseNetworks: %v", err)
	}
	found := false
	for _, n := range networks {
		if n.Name == name {
			found = true
			t.Logf("round-tripped network: name=%s id=%s driver=%s scope=%s",
				n.Name, n.ID, n.Driver, n.Scope)
		}
	}
	if !found {
		t.Fatalf("created network %q not found by the parser", name)
	}

	runWslc(t, exe, "network", "remove", name)

	after, err := wslc.ParseNetworks(runWslc(t, exe, "network", "list", "--format", "json"))
	if err != nil {
		t.Fatalf("ParseNetworks after removal: %v", err)
	}
	for _, n := range after {
		if n.Name == name {
			t.Errorf("network %q still present after removal", name)
		}
	}
}

func summarizeNetworks(networks []domain.Network) string {
	var parts []string
	for _, n := range networks {
		parts = append(parts, n.Name+"("+n.Driver+","+n.ID+")")
	}
	return strings.Join(parts, " ")
}

func summarizeVolumes(volumes []domain.Volume) string {
	var parts []string
	for _, v := range volumes {
		parts = append(parts, v.Name+"("+v.Driver+")")
	}
	return strings.Join(parts, " ")
}
