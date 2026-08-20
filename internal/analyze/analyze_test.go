package analyze

import (
	"strings"
	"testing"
	"time"

	"github.com/Allan-Nava/edgemix/internal/finding"
	"github.com/Allan-Nava/edgemix/internal/logfmt"
)

// stub is a parser identity for Report(): the analysis never parses anything
// itself, it only needs the dialect's name and what its timing field means.
type stub struct{}

func (stub) Name() string                       { return "haproxy" }
func (stub) LatencyField() string               { return "Tr" }
func (stub) Parse(string) (logfmt.Event, error) { return logfmt.Event{}, logfmt.ErrSkip }

var base = time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)

func ev(offset time.Duration, path string, status int, wait time.Duration) logfmt.Event {
	e := logfmt.Event{Time: base.Add(offset), Method: "GET", Path: path, Status: status, ClientIdentity: true}
	if wait >= 0 {
		e.Response, e.HaveResponse = wait, true
	}
	return e
}

// named stands in for a dialect other than the default stub, where what the
// report says depends on which one it was.
type named struct{ n string }

func (p named) Name() string                     { return p.n }
func (named) LatencyField() string               { return "time-to-first-byte" }
func (named) Parse(string) (logfmt.Event, error) { return logfmt.Event{}, logfmt.ErrSkip }

func report(t *testing.T, o Options, events ...logfmt.Event) Report {
	t.Helper()
	return reportAs(t, stub{}, o, events...)
}

func reportAs(t *testing.T, p logfmt.Parser, o Options, events ...logfmt.Event) Report {
	t.Helper()
	a := New(o)
	for _, e := range events {
		a.Add(e)
	}
	return a.Report("test.log", p, logfmt.Counts{Lines: len(events), Parsed: len(events)})
}

// find returns the first finding of a check, and whether there was one.
func find(r Report, check, target string) (finding.Finding, bool) {
	for _, f := range r.Findings {
		if f.Check == check && (target == "" || f.Target == target) {
			return f, true
		}
	}
	return finding.Finding{}, false
}

func TestRateIsMeasuredAtTheSecond(t *testing.T) {
	// Three requests in one second, one two seconds later. An hourly figure
	// divided by its seconds would report 1.3 req/s and hide the 3.
	var evs []logfmt.Event
	for i := 0; i < 3; i++ {
		evs = append(evs, ev(0, "/", 200, 10*time.Millisecond))
	}
	evs = append(evs, ev(2*time.Second, "/", 200, 10*time.Millisecond))

	r := report(t, Options{}, evs...)
	if r.Rate.Peak != 3 {
		t.Errorf("Peak = %d, want 3", r.Rate.Peak)
	}
	if !r.Rate.PeakAt.Equal(base) {
		t.Errorf("PeakAt = %v, want %v", r.Rate.PeakAt, base)
	}
	if r.WindowSeconds != 3 {
		t.Errorf("WindowSeconds = %d, want 3", r.WindowSeconds)
	}
	if r.SilentSeconds != 1 {
		t.Errorf("SilentSeconds = %d, want 1 — the second with no request is part of the window", r.SilentSeconds)
	}
	if got := r.Rate.Mean; got < 1.3 || got > 1.4 {
		t.Errorf("Mean = %v, want ~1.33", got)
	}
	if r.Rate.P99 != 3 {
		t.Errorf("P99 = %v, want 3", r.Rate.P99)
	}
}

func TestSilentSecondsAreCountedNotIgnored(t *testing.T) {
	// One request at each end of a ten-second window: eight seconds carry
	// nothing, and the p50 second must be zero rather than one.
	r := report(t, Options{},
		ev(0, "/", 200, 5*time.Millisecond),
		ev(9*time.Second, "/", 200, 5*time.Millisecond),
	)
	if r.Rate.P50 != 0 {
		t.Errorf("P50 = %v, want 0", r.Rate.P50)
	}
	if r.SilentSeconds != 8 {
		t.Errorf("SilentSeconds = %d, want 8", r.SilentSeconds)
	}
}

func TestLatencyTailsAndPercentiles(t *testing.T) {
	waits := []time.Duration{
		10 * time.Millisecond, 20 * time.Millisecond, 30 * time.Millisecond,
		1500 * time.Millisecond, 9000 * time.Millisecond,
	}
	var evs []logfmt.Event
	for i, w := range waits {
		evs = append(evs, ev(time.Duration(i)*time.Second, "/", 200, w))
	}
	r := report(t, Options{}, evs...)
	if r.Latency == nil {
		t.Fatal("Latency = nil")
	}
	if r.Latency.Max != 9000 {
		t.Errorf("Max = %v", r.Latency.Max)
	}
	want := map[float64]int{1000: 2, 3000: 1, 7000: 1}
	for _, tl := range r.Latency.Tails {
		if w, ok := want[tl.OverMs]; ok && tl.Count != w {
			t.Errorf("tail over %.0fms = %d, want %d", tl.OverMs, tl.Count, w)
		}
	}
	// The read timeout is where a slow response becomes a 504, so it must
	// produce a BAD rather than a number in a table.
	if got := worstOf(r.Findings, "wait", "read timeout"); got != finding.BAD {
		t.Errorf("read-timeout finding = %s, want BAD (one in five past 7s)", got)
	}
}

func TestNoTimingMeansNoLatencySection(t *testing.T) {
	r := report(t, Options{},
		logfmt.Event{Time: base, Path: "/", Status: 200},
		logfmt.Event{Time: base, Path: "/a.js", Status: 200},
	)
	if r.Latency != nil {
		t.Fatal("a log with no timing field must not report a latency distribution")
	}
	for _, f := range r.Findings {
		if f.Check == "wait" && f.Status != finding.OK {
			t.Errorf("finding %+v: silence about waiting is not a warning", f)
		}
	}
}

func TestClassesGetTheirOwnPeak(t *testing.T) {
	// The document arrives once; the navigation class bursts three times in a
	// later second. Classes do not peak together, and a share of the total peak
	// would put the burst on the wrong second.
	evs := []logfmt.Event{ev(0, "/", 200, time.Millisecond)}
	for i := 0; i < 3; i++ {
		e := ev(5*time.Second, "/news", 200, time.Millisecond)
		e.Query = "_rsc=abc"
		evs = append(evs, e)
	}
	r := report(t, Options{}, evs...)
	var nav, doc *ClassStat
	for i := range r.Classes {
		switch r.Classes[i].Name {
		case "rsc_nav":
			nav = &r.Classes[i]
		case "doc":
			doc = &r.Classes[i]
		}
	}
	if nav == nil || doc == nil {
		t.Fatalf("classes = %+v", r.Classes)
	}
	if nav.PeakPerSec != 3 || doc.PeakPerSec != 1 {
		t.Errorf("peaks: nav %d, doc %d", nav.PeakPerSec, doc.PeakPerSec)
	}
	if nav.Share <= doc.Share {
		t.Error("the navigation class is the larger share here")
	}
}

func TestFivexxFindingRises(t *testing.T) {
	var evs []logfmt.Event
	for i := 0; i < 100; i++ {
		status := 200
		if i < 5 {
			status = 500
		}
		evs = append(evs, ev(time.Duration(i)*time.Second, "/api/auth/login", status, 5*time.Millisecond))
	}
	r := report(t, Options{}, evs...)
	if got := worstOf(r.Findings, "errors", "5xx"); got != finding.BAD {
		t.Errorf("5xx finding = %s, want BAD at 5%%", got)
	}
	if len(r.Top5xx) == 0 || r.Top5xx[0].Path != "/api/auth/login" {
		t.Errorf("Top5xx = %+v, want the path named", r.Top5xx)
	}
}

func TestServerTimeoutTerminationIsCalledOut(t *testing.T) {
	var evs []logfmt.Event
	for i := 0; i < 100; i++ {
		e := ev(time.Duration(i)*time.Second, "/page", 200, 5*time.Millisecond)
		if i < 2 {
			e.Status, e.TermState = 504, "sD--"
		}
		evs = append(evs, e)
	}
	r := report(t, Options{}, evs...)
	if got := worstOf(r.Findings, "errors", "server timeout"); got != finding.WARN {
		t.Errorf("sD finding = %s, want WARN", got)
	}
}

func TestIncompleteLogIsFlaggedRatherThanDivided(t *testing.T) {
	// 60 requests in the first second of a 60-second window: the mean is 1/s,
	// so the 59 empty seconds are not a quiet site — they are a log that is not
	// recording everything, and every share below is of the logged subset.
	var evs []logfmt.Event
	for i := 0; i < 60; i++ {
		evs = append(evs, ev(0, "/", 200, time.Millisecond))
	}
	evs = append(evs, ev(59*time.Second, "/", 200, time.Millisecond))
	r := report(t, Options{}, evs...)
	if got := worstOf(r.Findings, "coverage", "window"); got != finding.WARN {
		t.Errorf("coverage/window = %s, want WARN", got)
	}
}

func TestUnreadableLinesInvalidateTheRest(t *testing.T) {
	a := New(Options{})
	a.Add(ev(0, "/", 200, time.Millisecond))
	r := a.Report("test.log", stub{}, logfmt.Counts{Lines: 100, Parsed: 1, Malformed: 99})
	if got := worstOf(r.Findings, "coverage", "log"); got != finding.ERROR {
		t.Errorf("coverage/log = %s, want ERROR", got)
	}
}

func TestNothingReadIsAnErrorAndNothingElse(t *testing.T) {
	a := New(Options{})
	r := a.Report("test.log", stub{}, logfmt.Counts{Lines: 500})
	if len(r.Findings) != 1 || r.Findings[0].Status != finding.ERROR {
		t.Fatalf("findings = %+v, want one ERROR and no measurements", r.Findings)
	}
}

func TestAudienceIsNotClaimedFromAPartialIdentity(t *testing.T) {
	evs := []logfmt.Event{ev(0, "/", 200, time.Millisecond)}
	for i := 1; i < 20; i++ {
		e := ev(time.Duration(i)*time.Second, "/", 200, time.Millisecond)
		e.ClientIdentity = false
		evs = append(evs, e)
	}
	r := report(t, Options{}, evs...)
	if r.Audience.Countable {
		t.Error("Countable = true with 5% of lines carrying an identity")
	}
}

func TestCacheUnknownIsNotZero(t *testing.T) {
	r := report(t, Options{}, ev(0, "/", 200, time.Millisecond))
	if r.Cache != nil {
		t.Fatal("a log with no cache verdict must report no ratio at all")
	}
	found := false
	for _, f := range r.Findings {
		if f.Check == "cache" && f.Target == "log" {
			found = true
		}
	}
	if !found {
		t.Error("the report must say that the hit ratio is unknown, not stay silent")
	}
}

func TestCacheRatioCountsWhatNeverReachedTheOrigin(t *testing.T) {
	var evs []logfmt.Event
	for i, v := range []string{"HIT", "STALE", "MISS", "BYPASS"} {
		e := ev(time.Duration(i)*time.Second, "/a.js", 200, time.Millisecond)
		e.Cache = v
		evs = append(evs, e)
	}
	r := report(t, Options{}, evs...)
	if r.Cache == nil {
		t.Fatal("Cache = nil")
	}
	if r.Cache.HitRatio != 0.5 {
		t.Errorf("HitRatio = %v, want 0.5 — STALE was served without the origin too", r.Cache.HitRatio)
	}
}

func TestWindowFilterIsReportedNotSilent(t *testing.T) {
	a := New(Options{Since: base.Add(2 * time.Second)})
	a.Add(ev(0, "/", 200, time.Millisecond))
	a.Add(ev(3*time.Second, "/", 200, time.Millisecond))
	if a.Filtered() != 1 {
		t.Errorf("Filtered = %d, want 1", a.Filtered())
	}
	r := a.Report("t", stub{}, logfmt.Counts{Parsed: 2})
	if r.Counts.Parsed-a.Filtered() != 1 {
		t.Error("the report has to make the excluded requests visible")
	}
}

func TestPoolsKeepOnlyPathsThatRendered(t *testing.T) {
	var evs []logfmt.Event
	for i := 0; i < 10; i++ {
		evs = append(evs, ev(time.Duration(i)*time.Second, "/renders", 200, time.Millisecond))
	}
	for i := 0; i < 20; i++ {
		// The busiest path in the log is a 404. A pool of these would measure
		// the error handler and call it capacity.
		evs = append(evs, ev(time.Duration(i)*time.Second, "/gone", 404, time.Millisecond))
	}
	r := report(t, Options{PoolSize: 10}, evs...)
	pool := r.Pools["doc"]
	if len(pool) != 1 || pool[0].Path != "/renders" {
		t.Fatalf("pool = %+v, want only the path that rendered", pool)
	}
}

func TestPathCapIsAnnounced(t *testing.T) {
	a := New(Options{MaxPaths: 2})
	for i, p := range []string{"/a", "/b", "/c", "/d"} {
		a.Add(ev(time.Duration(i)*time.Second, p, 200, time.Millisecond))
	}
	r := a.Report("t", stub{}, logfmt.Counts{Parsed: 4})
	if r.PathsDropped == 0 {
		t.Fatal("a cap that drops paths must be counted")
	}
	if got := worstOf(r.Findings, "coverage", "paths"); got != finding.WARN {
		t.Errorf("coverage/paths = %s, want WARN: a silently truncated list reads as a complete one", got)
	}
}

func TestBurstinessIsCalledOut(t *testing.T) {
	// A quiet minute with one spike: the mean says the system is idle.
	var evs []logfmt.Event
	for i := 0; i < 60; i++ {
		evs = append(evs, ev(time.Duration(i)*time.Second, "/", 200, time.Millisecond))
	}
	for i := 0; i < 40; i++ {
		evs = append(evs, ev(30*time.Second, "/", 200, time.Millisecond))
	}
	r := report(t, Options{}, evs...)
	if got := worstOf(r.Findings, "rate", "burstiness"); got != finding.WARN {
		t.Errorf("rate/burstiness = %s, want WARN", got)
	}
}

func TestFindingsAreSortedWorstFirst(t *testing.T) {
	r := report(t, Options{}, ev(0, "/", 200, 20*time.Second))
	for i := 1; i < len(r.Findings); i++ {
		if finding.Severity(r.Findings[i-1].Status) < finding.Severity(r.Findings[i].Status) {
			t.Fatalf("findings out of order at %d: %v", i, r.Findings)
		}
	}
}

func TestEveryFindingCarriesItsNumber(t *testing.T) {
	r := report(t, Options{}, ev(0, "/", 200, 20*time.Second))
	for _, f := range r.Findings {
		if f.Message == "" || f.Check == "" || f.Target == "" {
			t.Errorf("incomplete finding: %+v", f)
		}
	}
}

// worstOf returns the worst status among the findings with this check and
// target, or OK when there are none.
func worstOf(fs []finding.Finding, check, target string) finding.Status {
	worst := finding.OK
	for _, f := range fs {
		if f.Check == check && f.Target == target && finding.AtLeast(f.Status, worst) {
			worst = f.Status
		}
	}
	return worst
}

// A CDN log is written above the cache, which reverses the meaning of every
// number in the report: these are the requests the audience made, and the tier
// behind was asked only for the ones that missed. A report that does not say so
// gets read as origin load, and an origin sized on it is sized for traffic a
// CDN was absorbing.
func TestCDNLogSaysTheOriginSawOnlyTheMisses(t *testing.T) {
	var evs []logfmt.Event
	for i, v := range []string{"HIT", "REFRESHHIT", "MISS", "MISS"} {
		e := ev(time.Duration(i)*time.Second, "/a.js", 200, time.Millisecond)
		e.Cache = v
		evs = append(evs, e)
	}
	r := reportAs(t, named{"cloudfront"}, Options{}, evs...)

	if r.Cache == nil {
		t.Fatal("Cache = nil")
	}
	if r.Cache.Field != "x-edge-result-type" {
		t.Errorf("Cache.Field = %q, want the CloudFront field name: a report that does not name the field it read cannot be compared with anything", r.Cache.Field)
	}
	// REFRESHHIT revalidated with the origin but answered from the edge, so the
	// application tier was not asked. For a capacity question that is a hit.
	if r.Cache.HitRatio != 0.5 {
		t.Errorf("HitRatio = %v, want 0.5 — REFRESHHIT never reached the application", r.Cache.HitRatio)
	}

	f, ok := find(r, "edge", "origin share")
	if !ok {
		t.Fatal("a CDN log must state what share of it reached the origin")
	}
	if f.Status != finding.OK {
		t.Errorf("status = %v, want OK: it is a statement about what the log means, not a problem", f.Status)
	}
	if f.Value == nil || *f.Value != 50 {
		t.Errorf("Value = %v, want 50 (the share that missed), so a machine consumer never parses the prose", f.Value)
	}
	if f.Unit != "%" {
		t.Errorf("Unit = %q", f.Unit)
	}
}

func TestOriginSideLogMakesNoEdgeClaim(t *testing.T) {
	// The same events under an origin-side dialect: there is nothing to say
	// about what the edge absorbed, because this log never saw it.
	e := ev(0, "/a.js", 200, time.Millisecond)
	e.Cache = "HIT"
	r := report(t, Options{}, e)
	if _, ok := find(r, "edge", ""); ok {
		t.Error("an nginx or HAProxy log must not make a claim about the edge above it")
	}
}

func TestCDNLogWithoutACacheFieldCannotSpeakForTheOrigin(t *testing.T) {
	// A CloudFront distribution logging without x-edge-result-type, or a
	// DataStream 2 field set without cacheStatus. The traffic is measurable and
	// the origin's share of it is not — which is a warning, not a silence.
	r := reportAs(t, named{"akamai"}, Options{}, ev(0, "/a.js", 200, time.Millisecond))
	f, ok := find(r, "edge", "origin share")
	if !ok {
		t.Fatal("a CDN log with no cache verdict must say that the origin share is unknown")
	}
	if f.Status != finding.WARN {
		t.Errorf("status = %v, want WARN: every capacity number in this report is audience-side only", f.Status)
	}
}

// The wait percentiles of a CDN log are the visitor's experience, not the
// origin's queue: a request the edge answered never waited on the origin at
// all. The number is the same shape as an origin-side one, which is exactly why
// the hint has to differ.
func TestCDNWaitHintDoesNotClaimTheOrigin(t *testing.T) {
	e := ev(0, "/a.js", 200, 40*time.Millisecond)
	e.Cache = "HIT"

	cdn, ok := find(reportAs(t, named{"cloudfront"}, Options{}, e), "wait", "all classes")
	if !ok {
		t.Fatal("no wait finding")
	}
	origin, ok := find(report(t, Options{}, e), "wait", "all classes")
	if !ok {
		t.Fatal("no wait finding")
	}
	if cdn.Hint == origin.Hint {
		t.Error("a CDN log and a proxy log cannot carry the same explanation of what the wait measured")
	}
	if !strings.Contains(cdn.Hint, "edge") {
		t.Errorf("the CDN hint must say where the wait was measured: %q", cdn.Hint)
	}
}
