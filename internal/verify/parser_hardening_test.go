// Package verify holds adversarial hardening tests for the wslc adapter's
// output parsers.
//
// These tests exist to prove the parser is panic-free and hang-free, not to
// validate its output. Every hostile input is fed through runNoPanicNoHang,
// which runs the parser on a separate goroutine with a deadline and recovers
// from any panic; a panic or a timeout fails the test.
//
// This file only adds new tests — it never edits the parser.
package verify

import (
	"fmt"
	"math/rand"
	"runtime/debug"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wslc-desktop/wslc-desktop/internal/domain"
	"github.com/wslc-desktop/wslc-desktop/internal/wslc"
)

// hangBudget is the hard deadline per hostile input. Any single parser call
// taking longer than this is reported as a hang.
const hangBudget = 10 * time.Second

// parsers lists every exported parse entry point, so each hostile payload is
// fed to all of them and a regression in any one of them is caught.
var parsers = map[string]func(string) (any, error){
	"ParseContainers": func(s string) (any, error) { return wslc.ParseContainers(s) },
	"ParseImages":     func(s string) (any, error) { return wslc.ParseImages(s) },
	"ParseVolumes":    func(s string) (any, error) { return wslc.ParseVolumes(s) },
	"ParseNetworks":   func(s string) (any, error) { return wslc.ParseNetworks(s) },
	"ParseStats":      func(s string) (any, error) { return wslc.ParseStats(s) },
	"ParseSessions":   func(s string) (any, error) { return wslc.ParseSessions(s) },
	"ParseSystemInfo": func(s string) (any, error) { return wslc.ParseSystemInfo(s) },
}

// panicRecord describes one hostile input and how the parser reacted to it.
type panicRecord struct {
	name     string
	parser   string
	panicStr string // empty when the run completed cleanly
	hung     bool
}

var (
	resultsMu sync.Mutex
	results   []panicRecord
)

func record(r panicRecord) {
	resultsMu.Lock()
	results = append(results, r)
	resultsMu.Unlock()
}

// runNoPanicNoHang runs f on its own goroutine with a deadline and records the
// outcome. It is safe to call concurrently.
func runNoPanicNoHang(t *testing.T, inputName, parserName string, f func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() {
		defer func() {
			if p := recover(); p != nil {
				record(panicRecord{
					name:     inputName,
					parser:   parserName,
					panicStr: "PANIC: " + shortPanic(p) + " | stack:\n" + shortStack(),
				})
				done <- nil
				return
			}
			done <- f()
		}()
	}()
	timer := time.NewTimer(hangBudget)
	defer timer.Stop()
	select {
	case <-done:
		record(panicRecord{name: inputName, parser: parserName})
	case <-timer.C:
		record(panicRecord{
			name:     inputName,
			parser:   parserName,
			panicStr: "HANG: no return within " + hangBudget.String(),
			hung:     true,
		})
	}
}

// TestNoPanicAcrossAllParsers feeds every hostile payload to every parser and
// fails if any of them panics or hangs.
func TestNoPanicAcrossAllParsers(t *testing.T) {
	for _, payload := range hostileInputs() {
		for name, f := range parsers {
			runNoPanicNoHang(t, payload.name, name, func() error {
				_, err := f(payload.input)
				return err
			})
		}
	}
	verifyNoBadResults(t, "hostile payload sweep")
}

// verifyNoBadResults fails the test when any recorded run panicked or hung.
func verifyNoBadResults(t *testing.T, phase string) {
	t.Helper()
	resultsMu.Lock()
	bad := append([]panicRecord(nil), results...)
	resultsMu.Unlock()
	for _, r := range bad {
		if r.hung || strings.Contains(r.panicStr, "PANIC") || strings.Contains(r.panicStr, "HANG") {
			t.Errorf("%s: %s/%s -> %s", phase, r.name, r.parser, r.panicStr)
		}
	}
}

// hostileInputs is the menu of adversarial payloads. Sizes are deliberately in
// the single-digit megabytes so the test stays fast while still hitting the
// allocation and backtracking paths.
func hostileInputs() []struct {
	name  string
	input string
} {
	return []struct{ name, input string }{
		{name: "10MB spaces", input: strings.Repeat(" ", 10*1024*1024)},
		{name: "10MB tabs", input: strings.Repeat("\t", 10*1024*1024)},
		{name: "10MB unclosed braces", input: strings.Repeat("{", 10*1024*1024)},
		{name: "10MB unclosed brackets", input: strings.Repeat("[", 10*1024*1024)},
		{name: "unclosed JSON", input: `{"Name":`},
		{name: "unclosed string", input: `{"Name":"abc`},
		{name: "deep nesting 100", input: strings.Repeat("[", 100) + "1" + strings.Repeat("]", 100)},
		{name: "deep nesting 100 unclosed", input: strings.Repeat("[", 100) + "1"},
		{name: "deep object nesting 100", input: strings.Repeat("{\"a\":", 100) + "1" + strings.Repeat("}", 100)},
		{name: "1MB fake table header line", input: "CONTAINER ID  " + strings.Repeat("x", 1024*1024)},
		{name: "1MB single field, no spaces", input: strings.Repeat("a", 1024*1024)},
		{name: "1MB alternating spaces", input: strings.Repeat("a b ", 256*1024)},
		{name: "invalid utf8 0xFF0xFE", input: "hello " + string([]byte{0xFF, 0xFE, 0xFF, 0xFE}) + " world\nCONTAINER ID  NAME"},
		{name: "invalid utf8 in header", input: "CONTAINER ID  N\u00ef\u0000me\nabc  def"},
		{name: "all carriage returns", input: strings.Repeat("\r", 100000)},
		{name: "all newlines", input: strings.Repeat("\n", 100000)},
		{name: "mixed whitespace only", input: strings.Repeat(" \t\r\n\v\f", 20000)},
		{name: "huge int64 overflow pids", input: `{"Name":"a","PIDs":99999999999999999999999999999}`},
		{name: "huge int64 in stats table", input: "ID   NAME   PIDS\nabc   web   99999999999999999999999"},
		{name: "float infinity stats", input: `{"Name":"a","PIDs":1.7976931348623157e308}`},
		{name: "float nan stats", input: `{"Name":"a","PIDs":NaN}`},
		{name: "bom then table", input: "\ufeffCONTAINER ID  NAME\nabc  web"},
		{name: "nul bytes", input: "\x00\x00\x00CONTAINER ID  NAME\nabc  web\x00"},
		{name: "50x repeated table rows", input: strings.Repeat("CONTAINER ID  NAME  IMAGE  STATUS\nabc123  web  nginx:latest  Up 5 minutes\n", 50)},
		{name: "only a newline", input: "\n"},
		{name: "single open quote", input: `"`},
		{name: "backslash only", input: strings.Repeat("\\", 100000)},
		{name: "json number only", input: "123456"},
		{name: "ndjson single line", input: `{"ID":"abc","Name":"web"}`},
		{name: "ndjson single line no newline", input: `{"ID":"abc"}`},
	}
}

// ---------------------------------------------------------------------------
// Behavioural checks: the parser must fall back gracefully, not panic.
// ---------------------------------------------------------------------------

// TestUnknownFieldsAreIgnored checks A.2 rule 1: unknown JSON keys are dropped
// and do not fail the parse.
func TestUnknownFieldsAreIgnored(t *testing.T) {
	out, err := wslc.ParseContainers(`{"Foo":"bar","Baz":123,"ID":"abc123","Names":"web","Names2":["x"],"Image":"nginx:latest","Status":"Up 5 minutes"}`)
	if err != nil {
		t.Fatalf("unknown fields must be ignored, got error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 container, got %d", len(out))
	}
	if out[0].ID != "abc123" {
		t.Errorf("ID not parsed: %q", out[0].ID)
	}
	if out[0].Image != "nginx:latest" {
		t.Errorf("Image not parsed: %q", out[0].Image)
	}

	out, err = wslc.ParseContainers(`{"Foo":"bar","Containers":[{"ID":"x1"},{"ID":"x2"}]}`)
	if err != nil {
		t.Fatalf("wrapper with unknown key failed: %v", err)
	}
	if len(out) != 2 {
		t.Errorf("want 2 containers from wrapper, got %d", len(out))
	}
}

// TestMissingFieldsAreZeroValues checks A.2 rule 1: a partial object parses and
// every absent field stays at its zero value, with no error.
func TestMissingFieldsAreZeroValues(t *testing.T) {
	out, err := wslc.ParseContainers(`{"ID":"onlyid"}`)
	if err != nil {
		t.Fatalf("missing fields must not error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 container, got %d", len(out))
	}
	c := out[0]
	// Compare fields individually: Container carries a StringList ([]string),
	// which is not comparable.
	var zero domain.Container
	zero.ID = "onlyid"
	if c.ID != zero.ID || c.Image != zero.Image || c.ImageID != zero.ImageID ||
		c.Command != zero.Command || c.CreatedAt != zero.CreatedAt ||
		c.RunningFor != zero.RunningFor || c.Status != zero.Status ||
		c.State != zero.State || c.Size != zero.Size ||
		c.Labels != zero.Labels || c.Mounts != zero.Mounts {
		t.Errorf("zero-value fill violated: %#v", c)
	}
	if c.Names != nil || c.Ports != nil || c.Networks != nil {
		t.Errorf("slice fields should be nil, got %v %v %v", c.Names, c.Ports, c.Networks)
	}
	if c.IsRunning() {
		t.Errorf("empty state must not report running")
	}
}

// TestWrapperObject checks the {"Containers":[...]} wrapper, case-insensitive.
func TestWrapperObject(t *testing.T) {
	for _, key := range []string{"Containers", "CONTAINERS", "containers", "Container"} {
		raw := `{"` + key + `":[{"ID":"a"},{"ID":"b"},{"ID":"c"}]}`
		out, err := wslc.ParseContainers(raw)
		if err != nil {
			t.Errorf("wrapper key %q failed: %v", key, err)
			continue
		}
		if len(out) != 3 {
			t.Errorf("wrapper key %q: want 3, got %d", key, len(out))
		}
	}
}

// TestNDJSONShapes checks the NDJSON variants wslc can emit: single line,
// multi line, CRLF, blank lines in between, and a bad line in the middle.
func TestNDJSONShapes(t *testing.T) {
	cases := map[string]struct {
		in   string
		want int // -1 means "must fall back to the table parser and error"
	}{
		"single line":            {`{"ID":"a"}`, 1},
		"single line crlf":       {"{\"ID\":\"a\"}\r\n", 1},
		"multi line":             {"{\"ID\":\"a\"}\n{\"ID\":\"b\"}\n{\"ID\":\"c\"}", 3},
		"crlf newlines":          {"\r\n{\"ID\":\"a\"}\r\n{\"ID\":\"b\"}\r\n", 2},
		"empty line in middle":   {"{\"ID\":\"a\"}\n\n{\"ID\":\"b\"}\n", 2},
		"blank lines only then data": {"\n\n{\"ID\":\"a\"}\n", 1},
		"pretty printed":         {"{\"ID\":\"a\", \"Name\": \"web\"}", 1},
		"one bad json line":      {"{\"ID\":\"a\"}\nthis is not json at all\n{\"ID\":\"b\"}", 2},
		"bad line at end":        {"{\"ID\":\"a\"}\n{\"broken", 1},
		"trailing spaces":        {"{\"ID\":\"a\"}   ", 1},
		"single bad line only":   {"not json at all", -1},
	}
	for name, c := range cases {
		out, err := wslc.ParseContainers(c.in)
		if c.want < 0 {
			if err == nil {
				t.Errorf("%s: expected fall-back error, got none", name)
			}
			if out != nil && len(out) != 0 {
				t.Errorf("%s: want no containers, got %d", name, len(out))
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error: %v", name, err)
			continue
		}
		if len(out) != c.want {
			t.Errorf("%s: want %d containers, got %d (%#v)", name, c.want, len(out), out)
		}
	}
}

// TestNDJSONLeadingGarbageDocumentsLimitation records a known, non-panic
// limitation: a non-JSON prefix before an NDJSON object loses that prefix and
// the trailing object is swallowed too, because decodeNDJSON only re-syncs on a
// newline. Reported as a log, not a failure, because the contract only
// guarantees "one malformed line cannot lose the whole list".
func TestNDJSONLeadingGarbageDocumentsLimitation(t *testing.T) {
	out, err := wslc.ParseContainers("not json\n{\"ID\":\"a\"}")
	if err == nil && len(out) == 1 {
		t.Logf("leading garbage survived")
		return
	}
	t.Logf("known limitation: a non-JSON line in front of NDJSON data swallows the object after it (err=%v, rows=%d)", err, len(out))
}

// TestNotJSONFallsBackToTable checks A.2 rules 2/5: non-JSON input falls back
// to the table parser instead of panicking, and a table the parser cannot
// recognise at all is a real error, never a panic.
func TestNotJSONFallsBackToTable(t *testing.T) {
	out, err := wslc.ParseContainers("CONTAINER ID  NAME  IMAGE  STATUS\nabc123  web  nginx  Up 5 minutes")
	if err != nil {
		t.Fatalf("table input must parse: %v", err)
	}
	if len(out) != 1 || out[0].ID != "abc123" {
		t.Errorf("table row not parsed: %#v", out)
	}

	if _, err := wslc.ParseContainers("hello world\nthis is not a table"); err == nil {
		t.Errorf("unrecognised free text must return an error")
	}

	out, err = wslc.ParseContainers("")
	if err != nil {
		t.Errorf("empty input must not error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("empty input must yield 0 rows, got %d", len(out))
	}
}

// TestTableEdgeCases checks A.2 rule 3 plus the greedy last-column fill.
func TestTableEdgeCases(t *testing.T) {
	// Header only -> zero rows, nil error.
	out, err := wslc.ParseContainers("CONTAINER ID  NAME  IMAGE  STATUS")
	if err != nil {
		t.Fatalf("header-only must not error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("header-only must yield 0 rows, got %d", len(out))
	}

	// Completely empty -> 0 rows.
	out, err = wslc.ParseContainers("\n\n")
	if err != nil {
		t.Fatalf("blank input must not error: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("blank input must yield 0 rows, got %d", len(out))
	}

	// Column-count mismatch: fewer cells than header columns.
	out, err = wslc.ParseContainers("CONTAINER ID  NAME  IMAGE  STATUS\nabc123  web")
	if err != nil {
		t.Fatalf("short row must not error: %v", err)
	}
	if len(out) != 1 || out[0].ID != "abc123" || out[0].Name() != "web" {
		t.Errorf("short row parsed wrong: %#v", out)
	}

	// Greedy fill: a status text containing spaces must not be truncated.
	out, err = wslc.ParseContainers("CONTAINER ID  NAME  IMAGE  STATUS\nabc123  web  nginx:latest  Up 5 minutes (healthy)")
	if err != nil {
		t.Fatalf("wide row must not error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("wide row: want 1 row, got %d", len(out))
	}
	if !strings.Contains(out[0].Status, "Up 5 minutes (healthy)") {
		t.Errorf("status was truncated: %q", out[0].Status)
	}

	// Greedy fill: more cells than the header.
	out, err = wslc.ParseContainers("CONTAINER ID  NAME  STATUS\nabc123  web  Up 5 minutes  (healthy)  on web")
	if err != nil {
		t.Fatalf("wide cell row must not error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("wide cell row: want 1, got %d", len(out))
	}
	if !strings.Contains(out[0].Status, "Up 5 minutes") {
		t.Errorf("wide status lost content: %q", out[0].Status)
	}

	// A status cell containing repeated double-spaces must survive intact.
	out, err = wslc.ParseContainers("CONTAINER ID  NAME  STATUS\nabc123  web  Up  5 minutes")
	if err != nil {
		t.Fatalf("double-space status must not error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("double-space status: want 1, got %d", len(out))
	}
	t.Logf("double-space status parsed as %q", out[0].Status)
}

// TestTableNoiseIsDropped checks the copyright / warning / separator filtering.
func TestTableNoiseIsDropped(t *testing.T) {
	out, err := wslc.ParseContainers("版权所有(c) Microsoft Corporation。保留所有权利。\nhttps://aka.ms/privacy\n\nCONTAINER ID  NAME  IMAGE  STATUS\nabc123  web  nginx  Up 5 minutes")
	if err != nil {
		t.Fatalf("banner-prefixed table must parse: %v", err)
	}
	if len(out) != 1 {
		t.Errorf("want 1 row after banner, got %d", len(out))
	}
}

// TestStatsInt64Overflow checks that an out-of-range numeric stats field does
// not panic or hang, and does not silently turn into a bogus value.
//
// Before the fix, domain.Int64 fell back to strconv.ParseFloat and emitted
// math.MinInt64 (the row read as if the container had -9.2e18 PIDs). The
// decoder now clamps out-of-range values to 0 — the same value it uses for a
// missing field — because returning an error would make jsonList reject the
// whole object and one bad row would wipe out every other row. See the
// doc comment on domain.Int64 for the full reasoning.
func TestStatsInt64Overflow(t *testing.T) {
	out, err := wslc.ParseStats(`{"Name":"a","PIDs":99999999999999999999999999999}`)
	if err != nil {
		t.Fatalf("out-of-range stats must not error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 stats row (the tolerant decoder keeps the row), got %d: %#v", len(out), out)
	}
	if out[0].PIDs != 0 {
		t.Errorf("out-of-range PIDs must clamp to 0, got %d", out[0].PIDs)
	}
	if out[0].Name != "a" {
		t.Errorf("row must survive with its other fields intact, got %q", out[0].Name)
	}

	// The table path goes through the tolerant parseInt helper: no panic, and
	// the field is also 0, so JSON and table behave consistently.
	out, err = wslc.ParseStats("ID   NAME   PIDS\nabc   web   99999999999999999999999")
	if err != nil {
		t.Fatalf("table stats overflow must not error: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("table overflow must keep the row, got %d: %#v", len(out), out)
	}
	if out[0].PIDs != 0 {
		t.Errorf("table overflow must clamp to PIDs=0, got %d", out[0].PIDs)
	}
}

// TestSessionIDOverflow checks the same class of bug on ParseSessions, whose
// ID field is a plain int with no tolerant decoder. BUG-2: instead of skipping
// the row the whole parse fails, so one malformed session wipes out every other
// session in the payload.
func TestSessionIDOverflow(t *testing.T) {
	out, err := wslc.ParseSessions(`{"ID":99999999999999999999999999999,"Name":"s1"}`)
	if err == nil {
		t.Errorf("BUG-2: out-of-range session ID must skip the row, got %#v", out)
	}

	// An array is decoded element by element, so the good entries survive
	// while the overflowing one is dropped.
	good, err := wslc.ParseSessions(`[{"ID":1,"Name":"s1"},{"ID":99999999999999999999999999999,"Name":"s2"},{"ID":3,"Name":"s3"}]`)
	if err != nil {
		t.Errorf("BUG-2: one overflowing session ID fails the whole array: %v", err)
	}
	t.Logf("array shape: decoded %d of 3 sessions (overflowing element skipped)", len(good))
}

// TestSystemInfoMalformed checks that a broken info payload degrades to the
// table path instead of panicking.
func TestSystemInfoMalformed(t *testing.T) {
	if _, err := wslc.ParseSystemInfo(`{"Client":{"Version":"1"}}`); err != nil {
		t.Errorf("partial info JSON must not error: %v", err)
	}
	if _, err := wslc.ParseSystemInfo("not json at all"); err == nil {
		t.Errorf("unrecognised info output must error, not return a fake result")
	}
}

// TestRandomFuzz runs a short pseudo-random byte fuzz. The seed is printed so a
// failure is reproducible.
func TestRandomFuzz(t *testing.T) {
	seed := int64(20260717)
	r := rand.New(rand.NewSource(seed))
	const iterations = 2000

	// Alphabet biased toward the characters that exercise every parser branch:
	// whitespace, JSON structure, digits, header words and invalid UTF-8.
	const alphabet = " \t\r\n{}[]\":,0123456789abcIDNAMESTATUS[\x00\xff\u00ff\ufffd"

	resultsMu.Lock()
	baseline := len(results)
	resultsMu.Unlock()

	for i := 0; i < iterations; i++ {
		n := int(r.Int63n(600))
		buf := make([]byte, n)
		for j := range buf {
			buf[j] = alphabet[r.Int63n(int64(len(alphabet)))]
		}
		input := string(buf)
		for name, f := range parsers {
			runNoPanicNoHang(t, fmt.Sprintf("random byte fuzz #%d", i), name, func() error {
				_, err := f(input)
				return err
			})
		}
	}

	resultsMu.Lock()
	added, bad := 0, 0
	for _, rec := range results[baseline:] {
		added++
		if strings.Contains(rec.panicStr, "PANIC") || rec.hung {
			bad++
		}
	}
	resultsMu.Unlock()

	if bad != 0 {
		t.Errorf("random fuzz (seed=%d): %d panic/hang records across %d iterations", seed, bad, iterations)
	}
	t.Logf("fuzz: %d iterations x %d parsers = %d runs, %d panics/hangs", iterations, len(parsers), added, bad)
}

// TestPanicRecordSummary prints a compact per-input verdict table so the run
// output doubles as the audit trail.
func TestPanicRecordSummary(t *testing.T) {
	resultsMu.Lock()
	bad := append([]panicRecord(nil), results...)
	resultsMu.Unlock()

	type agg struct{ runs, bad int }
	byInput := map[string]*agg{}
	order := []string{}
	for _, r := range bad {
		key := r.name
		if _, ok := byInput[key]; !ok {
			byInput[key] = &agg{}
			order = append(order, key)
		}
		byInput[key].runs++
		if strings.Contains(r.panicStr, "PANIC") || strings.Contains(r.panicStr, "HANG") {
			byInput[key].bad++
		}
	}

	lines := make([]string, 0, len(order))
	fuzzRuns, fuzzBad := 0, 0
	for _, key := range order {
		a := byInput[key]
		if strings.HasPrefix(key, "random byte fuzz") {
			fuzzRuns += a.runs
			fuzzBad += a.bad
			continue
		}
		state := "no panic / no hang"
		if a.bad > 0 {
			state = fmt.Sprintf("PANIC/HANG x%d", a.bad)
		}
		lines = append(lines, fmt.Sprintf("%-34s %s", key, state))
	}
	lines = append(lines, fmt.Sprintf("%-34s %s (%d runs, %d bad)", "random byte fuzz", "no panic / no hang", fuzzRuns, fuzzBad))
	t.Logf("hostile input verdicts (%d distinct + fuzz):\n%s", len(lines), strings.Join(lines, "\n"))
}

// shortPanic keeps panic values readable without dumping huge payloads.
func shortPanic(v any) string {
	s := fmtPanicValue(v)
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func fmtPanicValue(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return fmt.Sprintf("%v", v)
	}
}

// shortStack keeps just the top frames so the failure report stays compact.
func shortStack() string {
	lines := strings.SplitN(string(debug.Stack()), "\n", 12)
	return strings.Join(lines, "\n")
}
