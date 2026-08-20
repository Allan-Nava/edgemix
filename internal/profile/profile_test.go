package profile

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Allan-Nava/edgemix/internal/analyze"
	"github.com/Allan-Nava/edgemix/internal/finding"
	"github.com/Allan-Nava/edgemix/internal/logfmt"
)

func sample() analyze.Report {
	start := time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)
	return analyze.Report{
		Source:        "edge.log",
		Dialect:       "haproxy",
		LatencyField:  "Tr",
		Zone:          "UTC",
		Counts:        logfmt.Counts{Lines: 1000, Parsed: 1000},
		Window:        finding.Window{Start: start, End: start.Add(time.Minute)},
		WindowSeconds: 61,
		Rate:          analyze.RateStat{Peak: 120, PeakAt: start, Mean: 16.4, P95: 40},
		Hosts:         []string{"www.example.test"},
		Classes: []analyze.ClassStat{
			{Name: "rsc_nav", Label: "framework navigation", Kind: "rsc", Count: 600, Share: 0.6, PeakPerSec: 90,
				DistinctPaths: 12, Latency: &analyze.LatencyStat{P95: 120}},
			{Name: "doc", Label: "document", Kind: "plain", Count: 400, Share: 0.4, PeakPerSec: 40,
				DistinctPaths: 8, Latency: &analyze.LatencyStat{P95: 1800}},
		},
		Pools: map[string][]analyze.PathStat{
			"rsc_nav": {{Path: "/news", Count: 300, OKShare: 1}, {Path: "/", Count: 300, OKShare: 1}},
			"doc":     {{Path: "/", Count: 400, OKShare: 1}},
		},
		Latency: &analyze.LatencyStat{P50: 40, P95: 900, P99: 3000},
	}
}

func TestBuildNeedsABaseURL(t *testing.T) {
	if _, _, err := Build(sample(), Options{}); err == nil {
		t.Fatal("Build succeeded with no base URL: it would have to guess where to send load")
	}
}

func TestBuildTranscribesTheMeasuredMix(t *testing.T) {
	p, warns, err := Build(sample(), Options{BaseURL: "https://www.example.test", Name: "example"})
	if err != nil {
		t.Fatalf("Build: %v (%v)", err, warns)
	}
	if len(p.Classes) != 2 {
		t.Fatalf("classes = %+v", p.Classes)
	}
	byName := map[string]Class{}
	for _, c := range p.Classes {
		byName[c.Name] = c
	}
	if got := byName["rsc_nav"].Weight; got != 60 {
		t.Errorf("weight = %v, want the measured 60%%", got)
	}
	if byName["rsc_nav"].Kind != "rsc" {
		t.Error("the navigation class must keep kind rsc or the generator sends the wrong headers")
	}
	if byName["doc"].Kind != "" {
		t.Error("plain is crowdsim's default and writing it is noise")
	}
	if len(p.Pools["rsc_nav"]) != 2 {
		t.Errorf("pool = %v", p.Pools["rsc_nav"])
	}
	// The brake watches the class that waits longest, not the biggest one.
	if p.SLO.BrakeClass != "doc" {
		t.Errorf("BrakeClass = %q, want doc (p95 1800ms against 120ms)", p.SLO.BrakeClass)
	}
	// The safety ceiling is a level production has already survived.
	if p.Safety.SafePeakRPS != 120 {
		t.Errorf("SafePeakRPS = %d, want the measured peak", p.Safety.SafePeakRPS)
	}
	if len(p.Safety.AllowHosts) != 1 || p.Safety.AllowHosts[0] != "www.example.test" {
		t.Errorf("AllowHosts = %v", p.Safety.AllowHosts)
	}
	if p.Measured.Source != "edge.log" || p.Measured.PeakPerSec != 120 {
		t.Errorf("provenance lost: %+v", p.Measured)
	}
	if p.Measured.AudienceNote == "" {
		t.Error("a profile must say that its weights are requests, not visitors")
	}
}

func TestAllowlistIsNeverInvented(t *testing.T) {
	r := sample()
	r.Hosts = nil
	p, warns, err := Build(r, Options{BaseURL: "https://www.example.test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Safety.AllowHosts) != 0 {
		t.Errorf("AllowHosts = %v, want empty: a load test aimed at the wrong host is indistinguishable from an attack", p.Safety.AllowHosts)
	}
	if !containsWord(warns, "allowlist") {
		t.Errorf("warnings = %v, want one about the empty allowlist", warns)
	}
}

func TestBrakeBelowTheReadTimeout(t *testing.T) {
	p, warns, err := Build(sample(), Options{
		BaseURL: "https://www.example.test", ReadTimeout: 5 * time.Second, MaxP95: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.SLO.MaxP95Ms >= p.SLO.GuillotineMs {
		t.Errorf("brake %dms is not below the read timeout %dms: it would fire after visitors already got 504s", p.SLO.MaxP95Ms, p.SLO.GuillotineMs)
	}
	if !containsWord(warns, "504") {
		t.Errorf("warnings = %v, want the brake adjustment explained", warns)
	}
}

func TestPathsThatLookLikeSecretsAreDropped(t *testing.T) {
	r := sample()
	r.Pools["doc"] = []analyze.PathStat{
		{Path: "/", OKShare: 1},
		{Path: "/reset/9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c", OKShare: 1},
		{Path: "/news/48211", OKShare: 1},
		{Path: "/news/champions-league-final", OKShare: 1},
	}
	p, warns, err := Build(r, Options{BaseURL: "https://www.example.test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, path := range p.Pools["doc"] {
		if strings.Contains(path, "9f8a7b6c") {
			t.Error("a token-shaped path segment reached the emitted profile")
		}
	}
	if len(p.Pools["doc"]) != 3 {
		t.Errorf("pool = %v, want the numeric id and the slug kept", p.Pools["doc"])
	}
	if !containsWord(warns, "token") {
		t.Errorf("warnings = %v, want the drop reported", warns)
	}
}

func TestLooksSecret(t *testing.T) {
	yes := []string{"/x/9f8a7b6c5d4e3f2a1b0c9d8e7f6a5b4c", "/s/1234567890123456789012"}
	no := []string{"/", "/news/48211", "/news/champions-league-final-2026", "/en/very-long-slug-with-words-here"}
	for _, p := range yes {
		if !looksSecret(p) {
			t.Errorf("looksSecret(%q) = false", p)
		}
	}
	for _, p := range no {
		if looksSecret(p) {
			t.Errorf("looksSecret(%q) = true — a real page was dropped from the pool", p)
		}
	}
}

func TestEmptyClassIsReportedNotSilentlyDropped(t *testing.T) {
	r := sample()
	delete(r.Pools, "doc")
	p, warns, err := Build(r, Options{BaseURL: "https://www.example.test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(p.Classes) != 1 {
		t.Errorf("classes = %+v", p.Classes)
	}
	if !containsWord(warns, "missing that share") {
		t.Errorf("warnings = %v, want the missing share stated", warns)
	}
}

func TestIncompleteLogIsCarriedIntoTheProfile(t *testing.T) {
	r := sample()
	r.SilentSeconds = 50 // of 61
	p, warns, err := Build(r, Options{BaseURL: "https://www.example.test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Measured.CoverageNote == "" {
		t.Error("a profile measured from an incomplete log has to say so in the file itself")
	}
	if len(warns) == 0 {
		t.Error("and on the terminal")
	}
}

func TestEmittedProfileIsValidJSONWithTheExpectedKeys(t *testing.T) {
	p, _, err := Build(sample(), Options{BaseURL: "https://www.example.test", Tool: "edgemix test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	// The keys crowdsim's validator reads. A profile missing one of these is
	// refused by the tool it exists for.
	for _, k := range []string{"name", "targets", "classes", "pools", "slo", "safety", "cache_headers", "_measured"} {
		if _, ok := m[k]; !ok {
			t.Errorf("emitted profile has no %q", k)
		}
	}
	targets := m["targets"].(map[string]any)
	if targets["default"] != "edge" {
		t.Errorf("default target = %v", targets["default"])
	}
	list := targets["list"].(map[string]any)
	if _, ok := list["edge"]; !ok {
		t.Error("the default target must exist in the list")
	}
}

func containsWord(warns []string, word string) bool {
	for _, w := range warns {
		if strings.Contains(w, word) {
			return true
		}
	}
	return false
}

// A mix measured above a cache is the audience's mix, and pointing a run at the
// origin with it replays every request the CDN was absorbing — at a peak the
// origin never saw. The emitter cannot fix that (lowering the ceiling would be
// inventing a number), so it has to say it, in the file and on stderr.
func TestProfileFromACDNLogSaysItIsAudienceSide(t *testing.T) {
	r := sample()
	r.Dialect = "cloudfront"
	r.LatencyField = "time-to-first-byte"
	r.Cache = &analyze.CacheStat{Field: "x-edge-result-type", Measured: 1000, Hits: 700, HitRatio: 0.7}

	p, warns, err := Build(r, Options{BaseURL: "https://www.example.test", Name: "cdn"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Measured.EdgeNote == "" {
		t.Fatal("a profile measured on a CDN log must carry that in _measured: nobody re-reads the command they ran a week later")
	}
	for _, want := range []string{"above the cache", "30.0%", "safe_peak_rps"} {
		if !strings.Contains(p.Measured.EdgeNote, want) {
			t.Errorf("edge_note does not mention %q: %s", want, p.Measured.EdgeNote)
		}
	}
	found := false
	for _, w := range warns {
		if strings.Contains(w, "CDN log") {
			found = true
		}
	}
	if !found {
		t.Errorf("the emitter must warn on stderr too, not only inside the file: %v", warns)
	}
	// The measured ceiling stays measured. Scaling it here would be an
	// invented number wearing the word "safe".
	if p.Safety.SafePeakRPS != r.Rate.Peak {
		t.Errorf("safe_peak_rps = %d, want the measured peak %d", p.Safety.SafePeakRPS, r.Rate.Peak)
	}
}

func TestProfileFromAnOriginLogMakesNoEdgeClaim(t *testing.T) {
	p, _, err := Build(sample(), Options{BaseURL: "https://www.example.test", Name: "example"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if p.Measured.EdgeNote != "" {
		t.Errorf("edge_note = %q, want empty for an origin-side log", p.Measured.EdgeNote)
	}
}
