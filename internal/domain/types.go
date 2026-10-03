// Package domain holds the read models the desktop UI renders.
//
// Field names are deliberately mapped to wslc's PascalCase JSON keys via
// case-insensitive encoding/json matching, and every field that has been
// observed to vary in shape uses a tolerant type so a wslc output change
// degrades to a missing value rather than a hard parse failure.
package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// StringList accepts either a JSON string or a JSON array of strings.
//
// wslc reports container names on some commands as a single string and on
// others as an array, so both are normalized to a slice.
type StringList []string

func (s *StringList) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*s = nil
		return nil
	}
	switch b[0] {
	case '"':
		var one string
		if err := json.Unmarshal(b, &one); err != nil {
			return err
		}
		*s = StringList{one}
		return nil
	case '[':
		var many []string
		if err := json.Unmarshal(b, &many); err != nil {
			return err
		}
		*s = StringList(many)
		return nil
	default:
		// Numbers and booleans are coerced to their literal text.
		*s = StringList{strings.Trim(string(b), `"`)}
		return nil
	}
}

// First returns the first entry, or "" when empty.
func (s StringList) First() string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// String renders the list as a comma-separated string.
func (s StringList) String() string { return strings.Join(s, ", ") }

// Duration accepts either a Go duration string ("1.5s"), a bare number of
// nanoseconds, or a numeric string, as wslc is inconsistent across fields.
type Duration time.Duration

func (d *Duration) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*d = 0
		return nil
	}
	if b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*d = 0
			return nil
		}
		if parsed, err := time.ParseDuration(s); err == nil {
			*d = Duration(parsed)
			return nil
		}
		// Fall back to a numeric string.
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			*d = Duration(time.Duration(n))
			return nil
		}
		return fmt.Errorf("wslc: unrecognized duration %q", s)
	}
	n, err := strconv.ParseInt(string(b), 10, 64)
	if err != nil {
		return fmt.Errorf("wslc: unrecognized duration %s", b)
	}
	*d = Duration(time.Duration(n))
	return nil
}

// Std converts back to a time.Duration.
func (d Duration) Std() time.Duration { return time.Duration(d) }

// Int64 accepts either a JSON number or a numeric string.
//
// It is deliberately tolerant of values that cannot be represented as int64:
// a caller feeding `99999999999999999999999999999` gets 0, not an error and
// not math.MinInt64. The reason for the tolerance is that jsonList decodes an
// object *into* T first and only then inspects the field values — if this
// decoder returned an error for an out-of-range field, the whole object would
// be rejected by decodeElements and one bad row would wipe out every other
// row in the list (the exact failure the contract forbids). 0 is the "unknown"
// value the contract already uses for a missing or unrepresentable field.
type Int64 int64

func (i *Int64) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		*i = 0
		return nil
	}
	s := strings.Trim(string(b), `"`)
	if s == "" {
		*i = 0
		return nil
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		*i = Int64(n)
		return nil
	}
	// wslc sometimes reports large byte counts as decimals.
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		// Overflow and NaN/Inf are unrepresentable; report 0 rather than
		// math.MinInt64, which the UI would render as a bogus "-9.2e18".
		if f <= math.MinInt64 || f >= math.MaxInt64 || math.IsNaN(f) || math.IsInf(f, 0) {
			*i = 0
			return nil
		}
		*i = Int64(int64(f))
		return nil
	}
	// Any other string (including a stray token that is not a number at all)
	// degrades to 0: the caller can still render the rest of the row.
	*i = 0
	return nil
}

// Container is one row of `wslc container list`.
type Container struct {
	ID         string     `json:"ID"`
	Names      StringList `json:"Names"`
	Image      string     `json:"Image"`
	ImageID    string     `json:"ImageID"`
	Command    string     `json:"Command"`
	CreatedAt  string     `json:"CreatedAt"`
	RunningFor string     `json:"RunningFor"`
	Status     string     `json:"Status"`
	State      string     `json:"State"`
	Ports      StringList `json:"Ports"`
	Size       string     `json:"Size"`
	Labels     string     `json:"Labels"`
	Networks   StringList `json:"Networks"`
	Mounts     string     `json:"Mounts"`
}

// Name returns the best display name for the container.
func (c Container) Name() string {
	if n := c.Names.First(); n != "" {
		return strings.TrimPrefix(n, "/")
	}
	return ShortID(c.ID)
}

// IsRunning reports whether the container is currently running.
func (c Container) IsRunning() bool {
	state := strings.ToLower(strings.TrimSpace(c.State))
	if state != "" {
		return state == "running"
	}
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Status)), "up")
}

// ShortID truncates an ID the way wslc's own table output does.
func ShortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

// Image is one row of `wslc image list`.
type Image struct {
	ID          string     `json:"ID"`
	Repository  string     `json:"Repository"`
	Tag         string     `json:"Tag"`
	Digest      string     `json:"Digest"`
	CreatedAt   string     `json:"CreatedAt"`
	CreatedSince string    `json:"CreatedSince"`
	Size        string     `json:"Size"`
	Labels      string     `json:"Labels"`
}

// Reference renders the image the way a user would type it.
func (i Image) Reference() string {
	repo := strings.TrimSpace(i.Repository)
	tag := strings.TrimSpace(i.Tag)
	switch {
	case repo == "":
		return ShortID(i.ID)
	case tag == "" || tag == "<none>":
		return repo
	default:
		return repo + ":" + tag
	}
}

// Volume is one row of `wslc volume list`.
type Volume struct {
	Name       string `json:"Name"`
	Driver     string `json:"Driver"`
	Mountpoint string `json:"Mountpoint"`
	Scope      string `json:"Scope"`
	CreatedAt  string `json:"CreatedAt"`
	Labels     string `json:"Labels"`
	Size       string `json:"Size"`
}

// Network is one row of `wslc network list`.
type Network struct {
	ID         string     `json:"ID"`
	Name       string     `json:"Name"`
	Driver     string     `json:"Driver"`
	Scope      string     `json:"Scope"`
	IPv6       string     `json:"IPv6"`
	Internal   string     `json:"Internal"`
	CreatedAt  string     `json:"CreatedAt"`
	Labels     string     `json:"Labels"`
	Containers StringList `json:"Containers"`
}

// ContainerStats is one row of `wslc stats`.
type ContainerStats struct {
	ID       string `json:"ID"`
	Name     string `json:"Name"`
	CPUPerc  string `json:"CPUPerc"`
	MemUsage string `json:"MemUsage"`
	MemPerc  string `json:"MemPerc"`
	NetIO    string `json:"NetIO"`
	BlockIO  string `json:"BlockIO"`
	PIDs     Int64  `json:"PIDs"`
}

// ClientInfo mirrors the Client section of `wslc info`.
type ClientInfo struct {
	Version        string `json:"Version"`
	KernelVersion  string `json:"KernelVersion"`
	Direct3DVersion string `json:"Direct3DVersion"`
	DxCoreVersion  string `json:"DxCoreVersion"`
	WindowsVersion string `json:"WindowsVersion"`
	SettingsFile   string `json:"SettingsFile"`
}

// Session is one entry of the Server.Sessions array in `wslc info`.
type Session struct {
	ID         int    `json:"ID"`
	Name       string `json:"Name"`
	CreatorPid int    `json:"CreatorPid"`
}

// ServerInfo mirrors the Server section of `wslc info`.
type ServerInfo struct {
	SessionManagerVersion string    `json:"SessionManagerVersion"`
	Sessions              []Session `json:"Sessions"`
}

// SystemInfo is the parsed form of `wslc info --format json`.
type SystemInfo struct {
	Client ClientInfo `json:"Client"`
	Server ServerInfo `json:"Server"`
}
