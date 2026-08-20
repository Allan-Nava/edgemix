package analyze

import (
	"fmt"

	"github.com/Allan-Nava/edgemix/internal/finding"
)

// Thresholds. They are constants rather than flags because each one is a claim
// about what the number means operationally, and a claim belongs in the source
// where it can be argued with — not in a config file where it becomes whatever
// makes the report green.
const (
	// A log that could not be read at all invalidates the rest.
	malformedError = 0.02
	malformedWarn  = 0.002

	// Traffic this bursty is not sized by its mean. Three is where the
	// difference between "the mean fits" and "the peak does not" starts costing
	// timeouts.
	burstyRatio = 3.0

	// Any traffic past the reverse proxy's read timeout is a 504 that a real
	// visitor saw; a tenth of a percent of a busy hour is thousands of them.
	timeoutShareBad = 0.001

	// A second of waiting is where a visitor notices. A twentieth of the
	// traffic waiting that long is a system with no headroom left.
	slowShareWarn = 0.05

	fivexxWarn = 0.005
	fivexxBad  = 0.02

	// HAProxy's `sD` termination: the server did not answer in time. It is the
	// difference between "the app returned an error" and "the app never
	// returned", and only the second one is a capacity problem.
	serverTimeoutWarn = 0.005

	// A log with holes this large is not a complete record of the traffic.
	silentShareWarn = 0.20

	// Below this rate, silent seconds are just a quiet site.
	silentMinMean = 1.0

	// A cache this cold on the biggest class is usually a rule that never asks,
	// not a cache that keeps missing.
	coldCacheWarn  = 0.10
	coldCacheFloor = 1000
)

// findings turns the report's numbers into statements. Every finding carries the
// number it is made of, and the ones that report a healthy measurement are
// emitted too: "no request waited past the read timeout" is the sentence an
// operator needs before a launch, and a report that only speaks when something
// is wrong cannot be used as a baseline.
func (a *Analyzer) findings(r *Report) []finding.Finding {
	var fs []finding.Finding
	add := func(f finding.Finding) { fs = append(fs, f) }

	if r.Counts.Parsed == 0 {
		add(finding.Finding{
			Check: "coverage", Target: "log", Status: finding.ERROR,
			Message: fmt.Sprintf("no request line could be read out of %d lines", r.Counts.Lines),
			Value:   finding.Num(float64(r.Counts.Lines)), Unit: "lines",
			Hint: "wrong dialect, a custom log-format, or not an access log at all — nothing below this line is a measurement",
		})
		return fs
	}

	// ── coverage ─────────────────────────────────────────────────────────────
	if s := r.Counts.MalformedShare(); s > 0 {
		st := finding.OK
		switch {
		case s >= malformedError:
			st = finding.ERROR
		case s >= malformedWarn:
			st = finding.WARN
		}
		add(finding.Finding{
			Check: "coverage", Target: "log", Status: st,
			Message: fmt.Sprintf("%s of request lines could not be read (%d of %d)", pct(s), r.Counts.Malformed, r.Counts.Malformed+r.Counts.Parsed),
			Value:   finding.Num(s * 100), Unit: "%",
			Hint: "a custom log-format the parser does not know: the shares below are computed over the lines that did read, which is not the whole traffic",
		})
	}
	if r.PathsDropped > 0 {
		add(finding.Finding{
			Check: "coverage", Target: "paths", Status: finding.WARN,
			Message: fmt.Sprintf("distinct-path cap reached: %d requests went to paths that were not kept", r.PathsDropped),
			Value:   finding.Num(float64(r.PathsDropped)), Unit: "requests",
			Hint: "top-path lists and any emitted pool are incomplete — raise --max-paths, or narrow the window",
		})
	}
	if r.WindowSeconds > 0 && r.Rate.Mean >= silentMinMean {
		if s := float64(r.SilentSeconds) / float64(r.WindowSeconds); s >= silentShareWarn {
			add(finding.Finding{
				Check: "coverage", Target: "window", Status: finding.WARN,
				Message: fmt.Sprintf("%s of the window has no logged request at all (%d of %d seconds)", pct(s), r.SilentSeconds, r.WindowSeconds),
				Value:   finding.Num(s * 100), Unit: "%",
				Hint: "at this rate the traffic does not stop for whole seconds: the proxy is not logging every request (HAProxy `dontlog-normal`, a sampled nginx log) — treat every share here as being of the logged subset, not of the traffic",
			})
		}
	}

	// ── arrival rate ─────────────────────────────────────────────────────────
	if r.Rate.Peak > 0 {
		add(finding.Finding{
			Check: "rate", Target: "peak second", Status: finding.OK,
			Message: fmt.Sprintf("busiest second was %d req/s at %s (mean %.1f, p95 %.0f)",
				r.Rate.Peak, r.Rate.PeakAt.Format("15:04:05"), r.Rate.Mean, r.Rate.P95),
			Value: finding.Num(float64(r.Rate.Peak)), Unit: "req/s",
			Hint: "size against this second, not the mean: it is the one that produced the timeouts",
		})
		if r.Rate.P95 > 0 {
			if ratio := float64(r.Rate.Peak) / r.Rate.P95; ratio >= burstyRatio {
				add(finding.Finding{
					Check: "rate", Target: "burstiness", Status: finding.WARN,
					Message: fmt.Sprintf("peak is %.1f× the p95 second (%d vs %.0f req/s)", ratio, r.Rate.Peak, r.Rate.P95),
					Value:   finding.Num(ratio), Unit: "×",
					Hint: "the arrival is spiky, so a system sized on the average is undersized for the seconds that matter — replay the peak, do not average it",
				})
			}
		}
	}

	// ── waiting ──────────────────────────────────────────────────────────────
	if r.Latency != nil {
		add(finding.Finding{
			Check: "wait", Target: "all classes", Status: finding.OK,
			Message: fmt.Sprintf("%s: p50 %.0fms, p95 %.0fms, p99 %.0fms, max %.0fms",
				r.LatencyField, r.Latency.P50, r.Latency.P95, r.Latency.P99, r.Latency.Max),
			Value: finding.Num(r.Latency.P95), Unit: "ms",
			Hint: "this is time spent waiting for the tier behind, not time spent computing: it grows with queueing, so it is the earliest sign of a saturated tier",
		})

		timeout := a.o.ReadTimeout
		var over TailStat
		found := false
		for _, t := range r.Latency.Tails {
			if t.OverMs == float64(timeout.Milliseconds()) {
				over, found = t, true
			}
		}
		if found {
			switch {
			case over.Share >= timeoutShareBad:
				add(finding.Finding{
					Check: "wait", Target: "read timeout", Status: finding.BAD,
					Message: fmt.Sprintf("%s of requests waited longer than the %s read timeout (%d requests)", pct(over.Share), timeout, over.Count),
					Value:   finding.Num(over.Share * 100), Unit: "%",
					Hint: "past the read timeout the proxy answers 504 whatever the app does next — these are visitors who saw an error page, and the margin is gone",
				})
			case over.Count > 0:
				add(finding.Finding{
					Check: "wait", Target: "read timeout", Status: finding.WARN,
					Message: fmt.Sprintf("%d requests waited longer than the %s read timeout (%s)", over.Count, timeout, pct(over.Share)),
					Value:   finding.Num(float64(over.Count)), Unit: "requests",
					Hint: "the tail already touches the timeout: the next comparable peak crosses it",
				})
			default:
				add(finding.Finding{
					Check: "wait", Target: "read timeout", Status: finding.OK,
					Message: fmt.Sprintf("nothing waited past the %s read timeout", timeout),
					Value:   finding.Num(0), Unit: "requests",
					Hint: "the margin is intact at this load — which is what makes this log worth keeping as a baseline",
				})
			}
		}
		for _, t := range r.Latency.Tails {
			if t.OverMs == 1000 && t.Share >= slowShareWarn {
				add(finding.Finding{
					Check: "wait", Target: "1s tail", Status: finding.WARN,
					Message: fmt.Sprintf("%s of requests waited longer than a second (%d requests)", pct(t.Share), t.Count),
					Value:   finding.Num(t.Share * 100), Unit: "%",
					Hint: "a queue this deep at a second means the tier behind has no spare worker at peak, not that a query is slow",
				})
			}
		}
	} else {
		add(finding.Finding{
			Check: "wait", Target: "all classes", Status: finding.OK,
			Message: "this log carries no timing field, so nothing is said about waiting",
			Hint:    "add %Tr to the HAProxy log-format, or $request_time to nginx's — without it a log can size arrivals but never find the tier that saturates",
		})
	}

	// The class that waits longest is the tier that will fail first, and it is
	// rarely the biggest class by count.
	worst, worstP95 := "", 0.0
	for _, c := range r.Classes {
		if c.Latency != nil && c.Latency.P95 > worstP95 {
			worst, worstP95 = c.Name, c.Latency.P95
		}
	}
	if worst != "" {
		add(finding.Finding{
			Check: "wait", Target: worst, Status: finding.OK,
			Message: fmt.Sprintf("slowest class at p95 is %s (%.0fms)", worst, worstP95),
			Value:   finding.Num(worstP95), Unit: "ms",
			Hint: "this is the class to brake a load test on: the mix falls over here first",
		})
	}

	// ── errors ───────────────────────────────────────────────────────────────
	for _, s := range r.Statuses {
		if s.Label != "5xx" {
			continue
		}
		st := finding.OK
		switch {
		case s.Share >= fivexxBad:
			st = finding.BAD
		case s.Share >= fivexxWarn:
			st = finding.WARN
		}
		msg := fmt.Sprintf("%s of responses were 5xx (%d)", pct(s.Share), s.Count)
		if len(r.Top5xx) > 0 {
			msg += fmt.Sprintf(", most on %s (%d)", r.Top5xx[0].Path, r.Top5xx[0].Worst5xx)
		}
		add(finding.Finding{
			Check: "errors", Target: "5xx", Status: st,
			Message: msg,
			Value:   finding.Num(s.Share * 100), Unit: "%",
			Hint: "a 5xx concentrated on one path is usually the application answering, not the tier saturating — check the termination state before reading it as capacity",
		})
	}
	for _, t := range r.Termination {
		if len(t.Label) >= 2 && t.Label[0] == 's' && t.Label[1] == 'D' && t.Share >= serverTimeoutWarn {
			add(finding.Finding{
				Check: "errors", Target: "server timeout", Status: finding.WARN,
				Message: fmt.Sprintf("%s of requests ended in an `sD` termination — the server did not answer in time (%d)", pct(t.Share), t.Count),
				Value:   finding.Num(t.Share * 100), Unit: "%",
				Hint: "`sD` is the proxy giving up on the backend, which is a capacity symptom; a `cD` at the same rate would have been visitors leaving",
			})
		}
	}

	// ── the mix ──────────────────────────────────────────────────────────────
	if len(r.Classes) > 0 {
		big := r.Classes[0]
		for _, c := range r.Classes {
			if c.Count > big.Count {
				big = c
			}
		}
		add(finding.Finding{
			Check: "mix", Target: big.Name, Status: finding.OK,
			Message: fmt.Sprintf("largest class is %s at %s of requests (%d), peaking at %d req/s of its own",
				big.Name, pct(big.Share), big.Count, big.PeakPerSec),
			Value: finding.Num(big.Share * 100), Unit: "%",
			Hint: "a load test firing a flat URL list reproduces none of this: the class peaks do not coincide, and the cheap class is usually the numerous one",
		})
	}

	// ── caching ──────────────────────────────────────────────────────────────
	if r.Cache != nil {
		add(finding.Finding{
			Check: "cache", Target: r.Cache.Field, Status: finding.OK,
			Message: fmt.Sprintf("%s of the responses this log judged were served without the origin (%d of %d)",
				pct(r.Cache.HitRatio), r.Cache.Hits, r.Cache.Measured),
			Value: finding.Num(r.Cache.HitRatio * 100), Unit: "%",
			Hint: "hit means the request never reached the application tier — the only cache number a capacity question cares about",
		})
		for _, c := range r.Classes {
			if c.Cache == nil || c.Cache.Measured < coldCacheFloor || c.Cache.HitRatio >= coldCacheWarn {
				continue
			}
			add(finding.Finding{
				Check: "cache", Target: c.Name, Status: finding.WARN,
				Message: fmt.Sprintf("%s is served from cache %s of the time (%d requests judged)", c.Name, pct(c.Cache.HitRatio), c.Cache.Measured),
				Value:   finding.Num(c.Cache.HitRatio * 100), Unit: "%",
				Hint: "a class this cold is usually a cache that is never asked rather than one that keeps missing: a `no-store` from the application, a `Vary` the origin does not send on this response type, or a CDN behavior with a zero minimum TTL that leaves the decision to the origin",
			})
		}
	} else {
		add(finding.Finding{
			Check: "cache", Target: "log", Status: finding.OK,
			Message: "this log carries no cache verdict, so the hit ratio is unknown",
			Hint:    "unknown is not zero: add $upstream_cache_status to nginx's log-format, or capture the layer's cache header in HAProxy, before concluding anything about caching",
		})
	}

	// ── audience ─────────────────────────────────────────────────────────────
	if r.Audience.Countable {
		add(finding.Finding{
			Check: "audience", Target: "log", Status: finding.OK,
			Message: fmt.Sprintf("every line carries a client identifier (%s)", pct(r.Audience.Share)),
			Value:   finding.Num(r.Audience.Share * 100), Unit: "%",
			Hint: "distinct addresses can be counted from this log, but an address is not a person: behind a CDN or a mobile carrier it is neither one visitor nor a stable one",
		})
	} else {
		add(finding.Finding{
			Check: "audience", Target: "log", Status: finding.OK,
			Message: fmt.Sprintf("only %s of lines carry a client identifier: this log cannot count people", pct(r.Audience.Share)),
			Value:   finding.Num(r.Audience.Share * 100), Unit: "%",
			Hint: "the frontend may capture X-Forwarded-For without printing it — check the log-format before trying to derive audience, and use the player's own beacons for the real number",
		})
	}

	finding.SortWorstFirst(fs)
	return fs
}

func pct(f float64) string {
	switch {
	case f == 0:
		return "0%"
	case f < 0.0001:
		return fmt.Sprintf("%.4f%%", f*100)
	case f < 0.01:
		return fmt.Sprintf("%.2f%%", f*100)
	}
	return fmt.Sprintf("%.1f%%", f*100)
}
