package output

import (
	"bytes"
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
		Tool:          "edgemix test",
		Source:        "edge.log",
		Dialect:       "haproxy",
		LatencyField:  "Tr (wait for the server's response)",
		Zone:          "UTC",
		Counts:        logfmt.Counts{Lines: 120, Parsed: 100, Skipped: 18, Malformed: 2},
		Window:        finding.Window{Start: start, End: start.Add(time.Minute)},
		WindowSeconds: 61,
		SilentSeconds: 3,
		Rate:          analyze.RateStat{Peak: 120, PeakAt: start, Mean: 1.6, P50: 1, P95: 40, P99: 90},
		Statuses:      []analyze.Count{{Label: "2xx", Count: 95, Share: 0.95}, {Label: "5xx", Count: 5, Share: 0.05}},
		Classes: []analyze.ClassStat{
			{Name: "doc", Label: "document", Count: 100, Share: 1, PeakPerSec: 120, DistinctPaths: 4,
				Latency: &analyze.LatencyStat{P50: 40, P95: 900, P99: 3000}},
		},
		Latency: &analyze.LatencyStat{Field: "Tr", Measured: 100, P50: 40, P95: 900, P99: 3000, Max: 9000,
			Tails: []analyze.TailStat{{OverMs: 1000, Count: 9, Share: 0.09}, {OverMs: 7000, Count: 1, Share: 0.01}}},
		Cache:    &analyze.CacheStat{Field: "$upstream_cache_status", Measured: 50, Hits: 20, HitRatio: 0.4, Verdicts: []analyze.Count{{Label: "MISS", Count: 30, Share: 0.6}}},
		TopPaths: []analyze.PathStat{{Path: "/", Count: 60, Share: 0.6, OKShare: 1}},
		Top5xx:   []analyze.PathStat{{Path: "/api/auth/login", Count: 20, Worst5xx: 5}},
		Audience: analyze.Audience{WithIdentity: 100, Share: 1, Countable: true},
		Findings: []finding.Finding{
			{Check: "wait", Target: "read timeout", Status: finding.BAD, Message: "1.0% of requests waited longer than the 7s read timeout", Hint: "these are visitors who saw a 504", Value: finding.Num(1), Unit: "%"},
			{Check: "rate", Target: "peak second", Status: finding.OK, Message: "busiest second was 120 req/s"},
		},
	}
}

func TestRenderTextPutsTheWorstFirstAndNamesTheNumbers(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sample(), Text, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	bad, ok := strings.Index(out, "BAD"), strings.Index(out, "OK")
	if bad < 0 || ok < 0 || bad > ok {
		t.Errorf("the BAD finding is not first:\n%s", out)
	}
	for _, want := range []string{"120 req/s", "edge.log", "haproxy", "over  1000ms", "$upstream_cache_status", "/api/auth/login"} {
		if !strings.Contains(out, want) {
			t.Errorf("output does not mention %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\033[") {
		t.Error("colour leaked into an uncoloured render")
	}
}

func TestRenderTextColourOnlyWhenAsked(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sample(), Text, true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "\033[") {
		t.Error("no colour in a coloured render")
	}
}

func TestRenderMarkdownIsADocumentWithTheMeasurementPathDrawn(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sample(), Markdown, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"# edgemix — edge.log",
		"| | check | target | statement |",
		"HAProxy (edge)",  // the schema names the tier the log came from
		"the tier behind", // and the one it measures the wait on
		"origin-side load, not audience",
		"## The mix",
		"## Waiting, and what it costs",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown does not contain %q:\n%s", want, out)
		}
	}
}

func TestMarkdownEscapesPipesSoTheTableSurvives(t *testing.T) {
	r := sample()
	r.Findings[0].Message = "a|b"
	var buf bytes.Buffer
	if err := Render(&buf, r, Markdown, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `a\|b`) {
		t.Error("an unescaped pipe breaks the findings table")
	}
}

func TestRenderJSONRoundTrips(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sample(), JSON, false); err != nil {
		t.Fatalf("Render: %v", err)
	}
	var back analyze.Report
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Rate.Peak != 120 || back.Latency == nil || back.Latency.P95 != 900 {
		t.Errorf("round trip lost data: %+v", back)
	}
	if len(back.Findings) != 2 || back.Findings[0].Status != finding.BAD {
		t.Errorf("findings did not survive: %+v", back.Findings)
	}
}

func TestRenderRejectsAnUnknownFormat(t *testing.T) {
	var buf bytes.Buffer
	if err := Render(&buf, sample(), Format("csv"), false); err == nil {
		t.Error("Render accepted a format it does not have")
	}
}

func TestEmptyReportRendersWithoutInventingNumbers(t *testing.T) {
	for _, f := range []Format{Text, Markdown, JSON} {
		var buf bytes.Buffer
		if err := Render(&buf, analyze.Report{}, f, false); err != nil {
			t.Errorf("Render(%s) on an empty report: %v", f, err)
		}
		if f == Markdown && !strings.Contains(buf.String(), "–") {
			t.Error("an absent window must render as a dash, not as year 1")
		}
	}
}
