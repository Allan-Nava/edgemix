package classify

import (
	"testing"

	"github.com/Allan-Nava/edgemix/internal/logfmt"
)

func TestDefaultSet(t *testing.T) {
	set := Default()
	for _, tc := range []struct{ path, query, want string }{
		{"/", "", "doc"},
		{"/news/latest", "", "doc"},
		{"/news/latest", "_rsc=1dxlt", "rsc_nav"},
		// The trap the rule order exists for: a static asset requested as a
		// framework navigation is navigation, not an asset.
		{"/logo.svg", "_rsc=1dxlt", "rsc_nav"},
		{"/_next/static/chunks/main.js", "", "static"},
		{"/media/stream.m3u8", "", "media"},
		{"/api/auth/login", "", "api"},
		{"/search", "q=alpha", "search"},
		{"/en/search/results", "q=b", "search"},
		{"/graphql", "", "api"},
		{"/favicon.ico", "", "static"},
	} {
		got := set.Classify(logfmt.Event{Path: tc.path, Query: tc.query})
		if got != tc.want {
			t.Errorf("Classify(%q?%q) = %q, want %q", tc.path, tc.query, got, tc.want)
		}
	}
}

func TestHasParamMatchesTheWholeName(t *testing.T) {
	if hasParam("_rscx=1", "_rsc") {
		t.Error("_rsc must not match _rscx: a prefix match would put ordinary requests in the navigation class")
	}
	if !hasParam("a=1&_rsc", "_rsc") {
		t.Error("a valueless parameter still counts")
	}
	if !hasParam("_rsc=abc", "_rsc") {
		t.Error("plain match failed")
	}
	if hasParam("", "_rsc") {
		t.Error("empty query matched")
	}
}

func TestKindAndLabel(t *testing.T) {
	set := Default()
	if set.Kind("rsc_nav") != "rsc" {
		t.Error("the navigation class must carry kind rsc into an emitted profile")
	}
	if set.Kind("doc") != "plain" {
		t.Error("an unset kind is plain")
	}
	if set.Label("static") == "static" {
		t.Error("a label was expected for the built-in static rule")
	}
	if set.Label("doc") != "doc" {
		t.Error("a class with no label reads as its name")
	}
}

func TestNamesPutsFallbackLast(t *testing.T) {
	names := Default().Names()
	if names[len(names)-1] != "doc" {
		t.Errorf("Names() = %v, want the fallback last", names)
	}
}

func TestLoadRejectsSetsThatWouldSkewShares(t *testing.T) {
	for name, data := range map[string]string{
		"no fallback":     `{"rules":[{"name":"a","path_prefixes":["/a"]}]}`,
		"no condition":    `{"fallback":"doc","rules":[{"name":"a"}]}`,
		"duplicate class": `{"fallback":"doc","rules":[{"name":"a","path_prefixes":["/a"]},{"name":"a","path_prefixes":["/b"]}]}`,
		"bad kind":        `{"fallback":"doc","rules":[{"name":"a","kind":"turbo","path_prefixes":["/a"]}]}`,
		"not json":        `{`,
	} {
		if _, err := Load([]byte(data)); err == nil {
			t.Errorf("Load accepted a set with %s", name)
		}
	}
}

func TestLoadAcceptsAWorkingSet(t *testing.T) {
	set, err := Load([]byte(`{"fallback":"page","rules":[{"name":"nav","kind":"rsc","query_params":["__turbo"]},{"name":"asset","path_suffixes":[".JS"]}]}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := set.Classify(logfmt.Event{Path: "/x", Query: "__turbo=1"}); got != "nav" {
		t.Errorf("got %q", got)
	}
	// Matching is case-insensitive on both sides, so a rule written in capitals
	// and a path served in lower case still meet.
	if got := set.Classify(logfmt.Event{Path: "/app.js"}); got != "asset" {
		t.Errorf("got %q", got)
	}
	if got := set.Classify(logfmt.Event{Path: "/"}); got != "page" {
		t.Errorf("got %q", got)
	}
}

func TestMethodCondition(t *testing.T) {
	set, err := Load([]byte(`{"fallback":"doc","rules":[{"name":"write","methods":["POST","PUT"],"path_prefixes":["/"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := set.Classify(logfmt.Event{Method: "POST", Path: "/api/x"}); got != "write" {
		t.Errorf("POST classified as %q", got)
	}
	if got := set.Classify(logfmt.Event{Method: "GET", Path: "/api/x"}); got != "doc" {
		t.Errorf("GET classified as %q", got)
	}
}
