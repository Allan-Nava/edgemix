// Package analyze folds a stream of events into a report: what the traffic was
// made of, how fast it arrived at its worst second, how long the tier behind
// made it wait, and what the log cannot answer.
//
// Three rules shape everything here.
//
// A rate is measured at the second. An hourly figure divided by 3600 is the
// number that made every "we were nowhere near capacity" post-mortem wrong: the
// second that produced the timeouts is routinely three to ten times the mean,
// and it is the only second that matters for sizing.
//
// A missing field is not a zero. A dialect that carries no timing produces no
// latency section, not a fast one; a log with no cache header reports "this log
// cannot tell hit from miss", not a 0% hit ratio.
//
// A cap is announced. Where the analysis bounds what it keeps — distinct paths,
// mostly — the report says how much it dropped, because a silently truncated
// list reads as a complete one.
package analyze

import (
	"time"

	"github.com/Allan-Nava/edgemix/internal/finding"
	"github.com/Allan-Nava/edgemix/internal/logfmt"
)

// Report is the whole result of a run.
type Report struct {
	Tool         string         `json:"tool"`
	Source       string         `json:"source"`
	Dialect      string         `json:"dialect"`
	LatencyField string         `json:"latency_field"`
	Zone         string         `json:"zone"`
	Counts       logfmt.Counts  `json:"counts"`
	Window       finding.Window `json:"window"`

	// WindowSeconds is the span the rates are computed over; SilentSeconds is
	// how many of those seconds carry no request at all. A log the proxy was
	// told not to write every line of (HAProxy's `dontlog-normal`, a sampled
	// nginx log) shows up here and nowhere else, and it invalidates every share
	// below — which is why it is a finding, not a footnote.
	WindowSeconds int `json:"window_seconds"`
	SilentSeconds int `json:"silent_seconds"`

	Rate        RateStat     `json:"rate"`
	Statuses    []Count      `json:"statuses"`
	Classes     []ClassStat  `json:"classes"`
	Latency     *LatencyStat `json:"latency,omitempty"`
	Cache       *CacheStat   `json:"cache,omitempty"`
	Termination []Count      `json:"termination,omitempty"`
	TopPaths    []PathStat   `json:"top_paths"`
	Top5xx      []PathStat   `json:"top_5xx,omitempty"`
	Hosts       []string     `json:"hosts,omitempty"`
	Audience    Audience     `json:"audience"`

	// PathsSeen is the distinct-path cardinality, PathsDropped how many
	// distinct paths were not kept once the cap was reached.
	PathsSeen    int `json:"paths_seen"`
	PathsDropped int `json:"paths_dropped"`

	// Pools are the replayable paths per class, present only when the run asked
	// for them (`edgemix profile`). They are the one part of a report that
	// carries raw paths from the log.
	Pools map[string][]PathStat `json:"pools,omitempty"`

	Findings []finding.Finding `json:"findings"`
}

// RateStat is arrival rate in requests per second, over the report's window.
//
// The percentiles are over *seconds*, not over requests: P95 is the rate the
// busiest 5% of seconds exceeded. Peak divided by P95 is how bursty the traffic
// is, and it is the ratio that decides whether a mean-sized system survives.
type RateStat struct {
	Peak   int       `json:"peak_per_sec"`
	PeakAt time.Time `json:"peak_at"`
	Mean   float64   `json:"mean_per_sec"`
	P50    float64   `json:"p50_per_sec"`
	P95    float64   `json:"p95_per_sec"`
	P99    float64   `json:"p99_per_sec"`
}

// Count is a labelled tally with its share of the whole.
type Count struct {
	Label string  `json:"label"`
	Count int     `json:"count"`
	Share float64 `json:"share"`
}

// ClassStat is one request class: its share of the mix, its own peak second and
// its own latency. A class's peak is not the total's peak divided by its share —
// classes do not peak together, and a navigation burst lands on a different
// second from the document that triggered it.
type ClassStat struct {
	Name          string       `json:"name"`
	Label         string       `json:"label"`
	Kind          string       `json:"kind"`
	Count         int          `json:"count"`
	Share         float64      `json:"share"`
	Bytes         int64        `json:"bytes"`
	PeakPerSec    int          `json:"peak_per_sec"`
	DistinctPaths int          `json:"distinct_paths"`
	Statuses      []Count      `json:"statuses"`
	Latency       *LatencyStat `json:"latency,omitempty"`
	Cache         *CacheStat   `json:"cache,omitempty"`
	TopPaths      []PathStat   `json:"top_paths,omitempty"`
}

// LatencyStat is the wait distribution, in milliseconds.
//
// Field names which measurement it is, and it travels with the numbers because
// HAProxy's Tr, Traefik's OriginDuration and nginx's $request_time are three
// different things: the first two are the wait on the tier behind, the third
// includes the client reading the body. Comparing them across dialects is the
// mistake this field exists to prevent.
type LatencyStat struct {
	Field    string        `json:"field"`
	Measured int           `json:"measured"`
	P50      float64       `json:"p50_ms"`
	P95      float64       `json:"p95_ms"`
	P99      float64       `json:"p99_ms"`
	Max      float64       `json:"max_ms"`
	Tails    []TailStat    `json:"tails,omitempty"`
	Sum      time.Duration `json:"sum_ns"`
}

// TailStat is how much of the traffic waited longer than a threshold. This is
// the number that converts into margin: the share above the reverse proxy's read
// timeout is the share of real visitors who got a 504.
type TailStat struct {
	OverMs float64 `json:"over_ms"`
	Count  int     `json:"count"`
	Share  float64 `json:"share"`
}

// CacheStat is the layer's own verdict on its cache. Verdicts are kept
// individually because MISS and BYPASS are different failures: one is a cache
// that was asked and had nothing, the other a rule that never asked.
type CacheStat struct {
	Field    string  `json:"field"`
	Measured int     `json:"measured"`
	Hits     int     `json:"hits"`
	HitRatio float64 `json:"hit_ratio"`
	Verdicts []Count `json:"verdicts"`
}

// Audience records whether this log can answer "how many people", which is a
// different question from "how many requests" and usually cannot be answered
// from an access log at all.
type Audience struct {
	// WithIdentity is how many parsed lines carried any client identifier.
	WithIdentity int     `json:"with_identity"`
	Share        float64 `json:"identity_share"`
	// Countable is true only when essentially every line carries one. It is
	// never a partial claim: a log where 4% of lines have a source address
	// cannot be counted, it can only mislead.
	Countable bool `json:"countable"`
}

// PathStat is one path's traffic. OKShare is the fraction that answered 2xx,
// which is what decides whether a path belongs in a load-test pool: a pool of
// paths that 404 measures the error handler, not the renderer.
type PathStat struct {
	Path     string  `json:"path"`
	Count    int     `json:"count"`
	Share    float64 `json:"share"`
	OKShare  float64 `json:"ok_share"`
	Worst5xx int     `json:"fivexx,omitempty"`
}
