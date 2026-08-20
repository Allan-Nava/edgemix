package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLog builds an HAProxy log with a known shape: 40 documents in one
// second, a slower tail, one 504 with a server-timeout termination, and a
// handful of navigation and asset requests.
func writeLog(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	line := func(sec int, ms int, path string, status int, term string) {
		b.WriteString(fmt.Sprintf(
			`10.0.0.9:1 [19/Aug/2026:18:%02d:%02d.000] fe_https~ be_app/app1 0/0/1/%d/%d %d 1200 - - %s 900/900/8/2/0 0/0 {www.example.test} "GET %s HTTP/1.1"`+"\n",
			sec/60, sec%60, ms, ms+1, status, term, path))
	}
	for i := 0; i < 40; i++ {
		line(0, 30, "/", 200, "----")
	}
	for i := 0; i < 10; i++ {
		line(5, 1500, fmt.Sprintf("/news/%d", i), 200, "----")
	}
	for i := 0; i < 5; i++ {
		line(6, 20, "/news?_rsc=1dxlt", 200, "----")
	}
	line(7, 9000, "/page/slow", 504, "sD--")
	line(8, 15, "/_next/static/chunks/main.js", 200, "----")
	b.WriteString("Aug 19 18:00:00 lb1 haproxy[1]: Proxy fe_https started.\n")

	path := filepath.Join(t.TempDir(), "edge.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnalyzeText(t *testing.T) {
	log := writeLog(t)
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, errs.String())
	}
	s := out.String()
	for _, want := range []string{"peak 40 req/s", "haproxy", "rsc_nav", "static", "read timeout"} {
		if !strings.Contains(s, want) {
			t.Errorf("output does not mention %q:\n%s", want, s)
		}
	}
	// Findings are output, not failure: a log full of problems still exits 0.
	if !strings.Contains(errs.String(), "reading") {
		t.Errorf("the detected dialect must be stated on stderr: %s", errs.String())
	}
}

func TestAnalyzeJSONIsMachineReadable(t *testing.T) {
	log := writeLog(t)
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--format", "json", log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var m map[string]any
	if err := json.Unmarshal(out.Bytes(), &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	rate := m["rate"].(map[string]any)
	if rate["peak_per_sec"].(float64) != 40 {
		t.Errorf("peak = %v, want 40", rate["peak_per_sec"])
	}
	if len(m["findings"].([]any)) == 0 {
		t.Error("no findings in the JSON")
	}
}

func TestAnalyzeMarkdownToFile(t *testing.T) {
	log := writeLog(t)
	dest := filepath.Join(t.TempDir(), "report.md")
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--format", "md", "--out", dest, log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "# edgemix") {
		t.Errorf("the file is not the report:\n%s", data)
	}
	if out.Len() != 0 {
		t.Error("--out must not also print to stdout")
	}
}

func TestExitOnRaisesTheExitCodeWithoutHidingTheReport(t *testing.T) {
	log := writeLog(t)
	var out, errs bytes.Buffer
	code := run([]string{"analyze", "--exit-on", "bad", log}, &out, &errs)
	if code != exitFound {
		t.Fatalf("exit %d, want %d — the log has a request past the read timeout", code, exitFound)
	}
	if out.Len() == 0 {
		t.Error("the report must still be printed")
	}

	out.Reset()
	if code := run([]string{"analyze", log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d without --exit-on, want 0: a check that ran is a success", code)
	}
}

func TestSinceAndUntilNarrowTheWindow(t *testing.T) {
	log := writeLog(t)
	var out, errs bytes.Buffer
	code := run([]string{"analyze", "--format", "json", "--since", "2026-08-19 18:00:05", "--until", "2026-08-19 18:00:07", log}, &out, &errs)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var m map[string]any
	json.Unmarshal(out.Bytes(), &m)
	if got := m["counts"].(map[string]any)["parsed"].(float64); got != 57 {
		t.Errorf("parsed = %v, want the whole file counted", got)
	}
	// The window kept the slow tail and the navigation burst: 15 requests.
	rate := m["rate"].(map[string]any)
	if rate["peak_per_sec"].(float64) != 10 {
		t.Errorf("peak = %v, want 10 (the 40-request second is outside the window)", rate["peak_per_sec"])
	}
	if !strings.Contains(errs.String(), "outside --since/--until") {
		t.Error("the excluded requests must be reported, not silently dropped")
	}
}

func TestProfileWritesARunnableMix(t *testing.T) {
	log := writeLog(t)
	dest := filepath.Join(t.TempDir(), "profile.json")
	var out, errs bytes.Buffer
	code := run([]string{"profile", "--base-url", "https://www.example.test", "--name", "example", "--out", dest, log}, &out, &errs)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	var p struct {
		Name    string `json:"name"`
		Classes []struct {
			Name   string  `json:"name"`
			Weight float64 `json:"weight"`
			Kind   string  `json:"kind"`
			Pool   string  `json:"pool"`
		} `json:"classes"`
		Pools  map[string][]string `json:"pools"`
		Safety struct {
			SafePeakRPS int      `json:"safe_peak_rps"`
			AllowHosts  []string `json:"allow_hosts"`
		} `json:"safety"`
		SLO struct {
			BrakeClass   string `json:"brake_class"`
			GuillotineMs int    `json:"guillotine_ms"`
			MaxP95Ms     int    `json:"max_p95_ms"`
		} `json:"slo"`
		Measured struct {
			PeakPerSec int    `json:"peak_per_sec"`
			Source     string `json:"source"`
		} `json:"_measured"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("emitted profile is not JSON: %v", err)
	}
	if p.Name != "example" {
		t.Errorf("name = %q", p.Name)
	}
	if len(p.Classes) < 3 {
		t.Errorf("classes = %+v, want the document, navigation and asset classes", p.Classes)
	}
	total := 0.0
	for _, c := range p.Classes {
		total += c.Weight
		if len(p.Pools[c.Pool]) == 0 {
			t.Errorf("class %s points at an empty pool", c.Name)
		}
	}
	if total < 90 || total > 100.5 {
		t.Errorf("weights total %v, want about 100 minus any dropped class", total)
	}
	// The hostname was captured in the log, so the allowlist is measured
	// rather than left for the operator.
	if len(p.Safety.AllowHosts) != 1 || p.Safety.AllowHosts[0] != "www.example.test" {
		t.Errorf("allow_hosts = %v", p.Safety.AllowHosts)
	}
	if p.Safety.SafePeakRPS != 40 {
		t.Errorf("safe_peak_rps = %d, want the measured peak of 40", p.Safety.SafePeakRPS)
	}
	if p.SLO.MaxP95Ms >= p.SLO.GuillotineMs {
		t.Error("the brake must sit below the read timeout")
	}
	if p.Measured.PeakPerSec != 40 || p.Measured.Source == "" {
		t.Errorf("provenance = %+v", p.Measured)
	}
	if !strings.Contains(errs.String(), "crowdsim validate") {
		t.Error("the next step belongs on stderr")
	}
}

func TestProfileRefusesWithoutABaseURL(t *testing.T) {
	log := writeLog(t)
	var out, errs bytes.Buffer
	if code := run([]string{"profile", log}, &out, &errs); code != exitUsage {
		t.Fatalf("exit %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errs.String(), "base URL") {
		t.Errorf("stderr = %q", errs.String())
	}
}

func TestStdinAndGzipAndSeveralFiles(t *testing.T) {
	log := writeLog(t)
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	// Two copies of the same log read as one window: the counts double, the
	// peak does not, because both copies land on the same seconds.
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--format", "json", log, log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	var m map[string]any
	json.Unmarshal(out.Bytes(), &m)
	if got := m["rate"].(map[string]any)["peak_per_sec"].(float64); got != 80 {
		t.Errorf("peak = %v, want 80 (both copies on the same second)", got)
	}
	_ = data
}

func TestUnknownDialectAndFormatAreUsageErrors(t *testing.T) {
	log := writeLog(t)
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--dialect", "apache", log}, &out, &errs); code != exitUsage {
		t.Errorf("unknown dialect: exit %d", code)
	}
	out.Reset()
	if code := run([]string{"analyze", "--format", "csv", log}, &out, &errs); code != exitUsage {
		t.Errorf("unknown format: exit %d", code)
	}
	out.Reset()
	if code := run([]string{"analyze", "--exit-on", "maybe", log}, &out, &errs); code != exitUsage {
		t.Errorf("unknown exit-on level: exit %d", code)
	}
	out.Reset()
	if code := run([]string{"analyze", "--since", "yesterday", log}, &out, &errs); code != exitUsage {
		t.Errorf("unreadable --since: exit %d", code)
	}
}

func TestNotALogIsAnErrorNotAnEmptyReport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "random.txt")
	os.WriteFile(path, []byte("hello\nworld\n"), 0o600)
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", path}, &out, &errs); code != exitUsage {
		t.Fatalf("exit %d, want %d — printing zeros for a file that is not a log is worse than refusing", code, exitUsage)
	}
}

func TestVersionAndHelp(t *testing.T) {
	var out, errs bytes.Buffer
	if code := run([]string{"version"}, &out, &errs); code != exitOK || !strings.Contains(out.String(), "edgemix") {
		t.Errorf("version: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run([]string{"help"}, &out, &errs); code != exitOK || !strings.Contains(out.String(), "crowdsim profile") {
		t.Errorf("help: exit %d, %q", code, out.String())
	}
	out.Reset()
	if code := run(nil, &out, &errs); code != exitUsage {
		t.Errorf("no command: exit %d", code)
	}
	if code := run([]string{"frobnicate"}, &out, &errs); code != exitUsage {
		t.Errorf("unknown command: exit %d", code)
	}
}

func TestClassesFileIsUsed(t *testing.T) {
	log := writeLog(t)
	rules := filepath.Join(t.TempDir(), "classes.json")
	os.WriteFile(rules, []byte(`{"fallback":"page","rules":[{"name":"asset","path_suffixes":[".js"]}]}`), 0o600)
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--format", "json", "--classes", rules, log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), `"name": "page"`) {
		t.Errorf("the custom fallback class is missing from the report:\n%s", out.String())
	}
	out.Reset()
	if code := run([]string{"analyze", "--classes", "/nonexistent.json", log}, &out, &errs); code != exitUsage {
		t.Errorf("a missing class file: exit %d", code)
	}
}

func TestFlagsAfterTheFileNameStillWork(t *testing.T) {
	// The form every example is written in: the file first, the output flag
	// after. Go's flag package stops at the first operand, so without
	// reordering this reads "-o" as another log file.
	log := writeLog(t)
	dest := filepath.Join(t.TempDir(), "profile.json")
	var out, errs bytes.Buffer
	code := run([]string{"profile", log, "--base-url", "https://www.example.test", "-o", dest}, &out, &errs)
	if code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("the profile was not written: %v", err)
	}
}

func TestDoubleDashEndsFlagsAndDashIsStdin(t *testing.T) {
	log := writeLog(t)
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--format", "json", "--", log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d: %s", code, errs.String())
	}
	if !strings.Contains(out.String(), `"peak_per_sec": 40`) {
		t.Errorf("output:\n%s", out.String())
	}
}

// writeCDNLog builds a CloudFront file the way the console delivers one: two
// header lines, then tab-separated rows, with the field list in an order that
// is not the current default.
func writeCDNLog(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("#Version: 1.0\n")
	b.WriteString("#Fields: date time cs-method cs-uri-stem cs-uri-query sc-status x-edge-result-type time-taken time-to-first-byte x-host-header c-ip sc-bytes\n")
	row := func(sec int, path, query string, status int, verdict string, ttfb float64) {
		b.WriteString(fmt.Sprintf("2026-08-19\t18:00:%02d\tGET\t%s\t%s\t%d\t%s\t%.3f\t%.3f\twww.example.test\t203.0.113.7\t1200\n",
			sec, path, query, status, verdict, ttfb+0.005, ttfb))
	}
	for i := 0; i < 30; i++ {
		row(0, "/_next/static/chunks/main.js", "-", 200, "Hit", 0.002)
	}
	for i := 0; i < 10; i++ {
		row(5, fmt.Sprintf("/news/%d", i), "-", 200, "Miss", 1.400)
	}
	for i := 0; i < 5; i++ {
		row(6, "/news", "_rsc=1dxlt", 200, "Miss", 0.040)
	}
	row(7, "/page/slow", "-", 504, "Error", 9.000)

	path := filepath.Join(t.TempDir(), "cf.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The whole path over a CDN log: detection, the header, the report, and the one
// sentence that must come out the other way round than it does for a proxy log.
func TestAnalyzeCloudFrontEndToEnd(t *testing.T) {
	log := writeCDNLog(t)
	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--format", "md", log}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, errs.String())
	}
	if !strings.Contains(errs.String(), "cloudfront") {
		t.Errorf("the vote must land on cloudfront: %s", errs.String())
	}
	s := out.String()
	for _, want := range []string{
		"CloudFront (CDN)",
		"the origin behind",
		"what the audience asked",
		"x-edge-result-type",
		"time-to-first-byte",
		"peak 30 req/s",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("the report does not mention %q:\n%s", want, s)
		}
	}
	// The two header lines are skipped, not counted as unreadable: a coverage
	// hole is a claim about the traffic and these are not traffic.
	if !strings.Contains(s, "2 skipped") && !strings.Contains(s, "skipped 2") {
		t.Errorf("the header lines must be reported as skipped:\n%s", s)
	}
}

func TestAnalyzeAkamaiEndToEnd(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 20; i++ {
		b.WriteString(fmt.Sprintf(`{"cliIP":"203.0.113.7","reqTimeSec":"178716240%d","reqMethod":"GET","reqHost":"www.example.test","reqPath":"/a.js","statusCode":"200","cacheStatus":"1","turnAroundTimeMSec":"5","totalBytes":"1200"}`+"\n", i%10))
	}
	for i := 0; i < 5; i++ {
		b.WriteString(fmt.Sprintf(`{"cliIP":"203.0.113.7","reqTimeSec":"17871624%02d","reqMethod":"GET","reqHost":"www.example.test","reqPath":"/news/%d","statusCode":"200","cacheStatus":"0","turnAroundTimeMSec":"1800","totalBytes":"14000"}`+"\n", 10+i, i))
	}
	path := filepath.Join(t.TempDir(), "ds2.log")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	var out, errs bytes.Buffer
	if code := run([]string{"analyze", "--format", "json", path}, &out, &errs); code != exitOK {
		t.Fatalf("exit %d, stderr:\n%s", code, errs.String())
	}
	var r struct {
		Dialect      string `json:"dialect"`
		LatencyField string `json:"latency_field"`
		Cache        *struct {
			Field    string  `json:"field"`
			HitRatio float64 `json:"hit_ratio"`
		} `json:"cache"`
		Findings []struct {
			Check  string `json:"check"`
			Target string `json:"target"`
		} `json:"findings"`
	}
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("the JSON report must stay machine-readable: %v", err)
	}
	if r.Dialect != "akamai" {
		t.Errorf("dialect = %q", r.Dialect)
	}
	if !strings.Contains(r.LatencyField, "turnAroundTimeMSec") {
		t.Errorf("latency_field = %q — a consumer has to be able to tell what was measured", r.LatencyField)
	}
	if r.Cache == nil || r.Cache.Field != "cacheStatus" || r.Cache.HitRatio != 0.8 {
		t.Errorf("cache = %+v, want cacheStatus at 0.8", r.Cache)
	}
	var edge bool
	for _, f := range r.Findings {
		if f.Check == "edge" {
			edge = true
		}
	}
	if !edge {
		t.Error("a CDN report must carry the edge finding, so a machine consumer can tell audience-side numbers from origin-side ones")
	}
}
