package main

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

// The frontend has no build step, so a missing or misnamed asset is not caught
// by any compiler: it shows up as a blank window at runtime. These tests assert
// the embedded tree the Wails asset server is handed actually contains what
// index.html asks for.
//
// They also pin the two files whose regeneration the frontend depends on:
// wailsjs/go/main/App.js (the generated bindings ui calls) and
// wailsjs/runtime/runtime.js (the event channel behind EventsOn).

// assetFS mirrors exactly what main.go passes to assetserver.Options.Assets.
func assetFS(t *testing.T) fs.FS {
	t.Helper()
	sub, err := fs.Sub(frontendAssets, "frontend")
	if err != nil {
		t.Fatalf("fs.Sub(frontendAssets, \"frontend\") failed: %v", err)
	}
	return sub
}

func TestEmbeddedFrontendHasEntryPoints(t *testing.T) {
	assets := assetFS(t)

	for _, name := range []string{
		"index.html",
		"styles.css",
		"app.js",
		"wailsjs/runtime/runtime.js",
		"wailsjs/go/main/App.js",
	} {
		info, err := fs.Stat(assets, name)
		if err != nil {
			t.Errorf("embedded frontend is missing %s: %v", name, err)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("embedded frontend file %s is empty", name)
		}
	}
}

// refPattern matches the src/href attributes a browser will actually fetch.
var refPattern = regexp.MustCompile(`(?:src|href)\s*=\s*"([^"]+)"`)

// TestIndexHTMLReferencesResolve guards the classic blank-window failure: a
// renamed or moved asset that index.html still points at.
func TestIndexHTMLReferencesResolve(t *testing.T) {
	assets := assetFS(t)

	raw, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	checked := 0
	for _, match := range refPattern.FindAllStringSubmatch(string(raw), -1) {
		ref := strings.TrimSpace(match[1])
		switch {
		case ref == "", strings.HasPrefix(ref, "#"),
			strings.HasPrefix(ref, "http://"), strings.HasPrefix(ref, "https://"),
			strings.HasPrefix(ref, "//"), strings.HasPrefix(ref, "data:"),
			strings.HasPrefix(ref, "mailto:"), strings.Contains(ref, "{{"):
			continue
		}
		// Strip any query/fragment before resolving inside the embedded FS.
		target := strings.TrimPrefix(ref, "./")
		target = strings.TrimPrefix(target, "/")
		if i := strings.IndexAny(target, "?#"); i >= 0 {
			target = target[:i]
		}
		if target == "" {
			continue
		}
		checked++
		if _, err := fs.Stat(assets, target); err != nil {
			t.Errorf("index.html references %q but it is not in the embedded frontend: %v", ref, err)
		}
	}

	if checked == 0 {
		t.Fatal("index.html declares no local src/href assets; did the markup change?")
	}
}

// TestEmbeddedWailsRuntimeIsReal checks we ship Wails' own runtime rather than a
// stub: EventsOn/EventsOff must exist for the event channels to work.
func TestEmbeddedWailsRuntimeIsReal(t *testing.T) {
	raw, err := fs.ReadFile(assetFS(t), "wailsjs/runtime/runtime.js")
	if err != nil {
		t.Fatalf("read runtime.js: %v", err)
	}
	runtime := string(raw)
	for _, symbol := range []string{"EventsOn", "EventsOff", "EventsEmit"} {
		if !strings.Contains(runtime, symbol) {
			t.Errorf("wailsjs/runtime/runtime.js does not define %s; events would be unavailable", symbol)
		}
	}
}
