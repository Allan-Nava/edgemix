// Package profile turns a measured report into a crowdsim profile.
//
// This is the point of edgemix. A load test built from a guess measures a load
// that does not exist, and the mix it needs — which classes, in what
// proportion, against which paths, braking on which one — is already written in
// the edge log. What this package does is transcribe it, and refuse to invent
// the parts the log cannot supply.
//
// Three refusals are deliberate:
//
//   - A path that did not render does not enter a pool. A 404 is cheap, and a
//     pool of them yields a flattering number for a load that never reached the
//     renderer.
//   - A path that looks like it carries a secret or a per-user id is dropped and
//     counted. A profile is a file people paste into tickets.
//   - The allowlist is never invented. If the log does not name the host, the
//     emitted profile has an empty allowlist and says so: a load test aimed at
//     the wrong hostname is indistinguishable from an attack.
package profile

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/Allan-Nava/edgemix/internal/analyze"
	"github.com/Allan-Nava/edgemix/internal/logfmt"
)

// Options are the parts a log cannot supply.
type Options struct {
	Name        string
	Description string
	// BaseURL is where the load would go. Required: a profile without one is
	// not runnable, and defaulting it to the measured hostname would aim a load
	// test at production by accident.
	BaseURL    string
	TargetName string
	HostHeader string
	Bypass     string

	// ReadTimeout is the reverse proxy's read timeout, emitted as
	// `slo.guillotine_ms` — the line past which a slow response is a 504.
	ReadTimeout time.Duration
	// MaxP95 is the brake, emitted as `slo.max_p95_ms`.
	MaxP95        time.Duration
	MaxFailedRate float64

	// AllowHosts overrides the hostnames read from the log.
	AllowHosts []string
	// SafePeak overrides the measured peak as `safety.safe_peak_rps`.
	SafePeak int

	Tool string
}

// Profile is the emitted document. Keys beginning with `_` are documentation:
// crowdsim strips them before the generator sees them, which makes them the
// right place for provenance — a profile that cannot say which log and which
// window it came from gets replayed against a different system a month later.
type Profile struct {
	Note         string              `json:"_note"`
	Name         string              `json:"name"`
	Description  string              `json:"description"`
	Measured     Measured            `json:"_measured"`
	Targets      Targets             `json:"targets"`
	Classes      []Class             `json:"classes"`
	Pools        map[string][]string `json:"pools"`
	CacheHeaders []CacheHeader       `json:"cache_headers,omitempty"`
	SLO          SLO                 `json:"slo"`
	Safety       Safety              `json:"safety"`
}

// Measured is the provenance of every number in the file.
type Measured struct {
	By           string  `json:"by"`
	Source       string  `json:"source"`
	Dialect      string  `json:"dialect"`
	WindowStart  string  `json:"window_start"`
	WindowEnd    string  `json:"window_end"`
	Zone         string  `json:"zone"`
	Requests     int     `json:"requests"`
	PeakPerSec   int     `json:"peak_per_sec"`
	PeakAt       string  `json:"peak_at"`
	MeanPerSec   float64 `json:"mean_per_sec"`
	LatencyField string  `json:"latency_field"`
	LatencyP95Ms float64 `json:"latency_p95_ms,omitempty"`
	AudienceNote string  `json:"audience_note"`
	CoverageNote string  `json:"coverage_note,omitempty"`
	// EdgeNote is set when the mix was measured above a cache. A CDN log
	// records what the audience asked for, so replaying it against an origin
	// replays the requests the CDN was absorbing — at the measured peak, which
	// the origin never saw. The number stays as measured and the file says what
	// it is: a profile that silently meant something else is worse than one
	// that has to be read.
	EdgeNote     string `json:"edge_note,omitempty"`
	DroppedPaths int    `json:"dropped_paths,omitempty"`
}

// Targets, Class, CacheHeader, SLO and Safety mirror the crowdsim profile
// schema. They are written out here rather than imported because crowdsim is a
// separate tool in another language: the coupling is the file format, and it is
// better stated once, visibly, than hidden behind a shared package.
type Targets struct {
	Default string            `json:"default"`
	List    map[string]Target `json:"list"`
}

// Target is one place the load can be pointed at.
type Target struct {
	BaseURL    string `json:"base_url"`
	HostHeader string `json:"host_header,omitempty"`
	Bypass     string `json:"bypass,omitempty"`
}

// Class is one request class with its measured weight.
type Class struct {
	Name   string  `json:"name"`
	Label  string  `json:"label,omitempty"`
	Weight float64 `json:"weight"`
	Kind   string  `json:"kind,omitempty"`
	Pool   string  `json:"pool"`
	Note   string  `json:"_note,omitempty"`
}

// CacheHeader names the header that reveals one layer's cache decision.
type CacheHeader struct {
	Label  string `json:"label"`
	Header string `json:"header"`
	Hit    string `json:"hit,omitempty"`
}

// SLO is the brake and the read timeout.
type SLO struct {
	MaxFailedRate float64 `json:"max_failed_rate"`
	MaxP95Ms      int     `json:"max_p95_ms"`
	GuillotineMs  int     `json:"guillotine_ms"`
	BrakeClass    string  `json:"brake_class,omitempty"`
	Note          string  `json:"_note,omitempty"`
}

// Safety is the allowlist and the peak above which a run needs an override.
type Safety struct {
	SafePeakRPS int      `json:"safe_peak_rps"`
	AllowHosts  []string `json:"allow_hosts"`
	Note        string   `json:"_note,omitempty"`
}

// Build transcribes a report into a profile, returning the warnings that must be
// shown to whoever runs it. A warning is not a failure: the profile is emitted
// anyway, because a half-known profile that says what it does not know is more
// use than a refusal.
func Build(r analyze.Report, o Options) (Profile, []string, error) {
	if strings.TrimSpace(o.BaseURL) == "" {
		return Profile{}, nil, fmt.Errorf("a base URL is required: a profile has to say where the load goes, and guessing it from the log would aim a load test at production")
	}
	if len(r.Pools) == 0 {
		return Profile{}, nil, fmt.Errorf("the report carries no pools: run the analysis with a pool size (edgemix profile does this) over a log that has requests that rendered")
	}
	if o.TargetName == "" {
		o.TargetName = "edge"
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = 7 * time.Second
	}
	if o.MaxP95 <= 0 {
		o.MaxP95 = 5 * time.Second
	}
	if o.MaxFailedRate <= 0 {
		o.MaxFailedRate = 0.05
	}

	var warns []string
	warn := func(f string, a ...any) { warns = append(warns, fmt.Sprintf(f, a...)) }

	if o.MaxP95 >= o.ReadTimeout {
		o.MaxP95 = o.ReadTimeout * 3 / 4
		warn("the p95 brake was at or above the read timeout, which would abort a run only after real visitors were already getting 504s: it has been lowered to %s", o.MaxP95)
	}

	p := Profile{
		Note:        "Measured from a production access log by edgemix. Keys starting with _ are documentation and are stripped before the generator runs. Re-measure after a deploy: static pools carry build hashes.",
		Name:        o.Name,
		Description: o.Description,
		Pools:       map[string][]string{},
		Targets: Targets{
			Default: o.TargetName,
			List: map[string]Target{
				o.TargetName: {BaseURL: o.BaseURL, HostHeader: o.HostHeader, Bypass: o.Bypass},
			},
		},
		CacheHeaders: []CacheHeader{
			{Label: "proxy", Header: "X-Proxy-Cache", Hit: "HIT|STALE|UPDATING|REVALIDATED"},
			{Label: "cdn", Header: "X-Cache", Hit: "Hit"},
			{Label: "rfc9211", Header: "Cache-Status", Hit: "hit"},
		},
	}
	if p.Name == "" {
		p.Name = "measured"
	}
	if p.Description == "" {
		p.Description = fmt.Sprintf("request mix measured on %s (%s), %s → %s",
			r.Source, r.Dialect, stamp(r.Window.Start), stamp(r.Window.End))
	}

	// ── classes and pools ────────────────────────────────────────────────────
	// Classes keep the report's order, and the weight is the measured share of
	// requests. Only relative values matter to crowdsim, so the share in
	// percent is emitted as-is: a reader can check it against the report.
	for _, c := range r.Classes {
		paths, dropped := poolPaths(r.Pools[c.Name])
		if len(paths) == 0 {
			warn("class %s (%s of requests) has no path that rendered often enough to replay, so it is not in the profile: the mix is missing that share", c.Name, pctString(c.Share))
			continue
		}
		if dropped > 0 {
			warn("class %s: %d path(s) were dropped from the pool because they look like they carry a token or a per-user id", c.Name, dropped)
		}
		pool := c.Name
		p.Pools[pool] = paths
		note := fmt.Sprintf("%d requests measured, %s of the mix, own peak %d req/s, %d distinct paths seen",
			c.Count, pctString(c.Share), c.PeakPerSec, c.DistinctPaths)
		if c.Latency != nil {
			note += fmt.Sprintf(", p95 %.0fms on %s", c.Latency.P95, r.LatencyField)
		}
		p.Classes = append(p.Classes, Class{
			Name:   c.Name,
			Label:  c.Label,
			Weight: round1(c.Share * 100),
			Kind:   kind(c.Kind),
			Pool:   pool,
			Note:   note,
		})
	}
	if len(p.Classes) == 0 {
		return Profile{}, warns, fmt.Errorf("no class survived: every pool was empty after dropping the paths that did not render")
	}

	// ── the brake ────────────────────────────────────────────────────────────
	// The class that waits longest is the one that falls over first, which is
	// what a brake has to watch. crowdsim would otherwise default to the first
	// class in the mix, and the first class is usually the cheapest one.
	brake, worst := "", 0.0
	for _, c := range r.Classes {
		if c.Latency != nil && c.Latency.P95 > worst && hasClass(p.Classes, c.Name) {
			brake, worst = c.Name, c.Latency.P95
		}
	}
	if brake == "" {
		brake = p.Classes[0].Name
		warn("no class had a measured wait, so the brake is the first class (%s) rather than the slowest one: a log with a timing field would have decided this", brake)
	}
	p.SLO = SLO{
		MaxFailedRate: o.MaxFailedRate,
		MaxP95Ms:      int(o.MaxP95.Milliseconds()),
		GuillotineMs:  int(o.ReadTimeout.Milliseconds()),
		BrakeClass:    brake,
		Note:          fmt.Sprintf("guillotine_ms is the reverse proxy's read timeout as given to edgemix (%s), not something measured; brake_class is the slowest class at p95 in the measured window", o.ReadTimeout),
	}

	// ── safety ───────────────────────────────────────────────────────────────
	hosts := o.AllowHosts
	if len(hosts) == 0 {
		hosts = r.Hosts
	}
	if len(hosts) == 0 {
		hosts = []string{}
		warn("this log does not record the hostname, so the allowlist is empty and crowdsim will refuse to run until you fill safety.allow_hosts (or set CROWDSIM_ALLOW_TARGETS)")
	}
	peak := o.SafePeak
	if peak <= 0 {
		peak = r.Rate.Peak
	}
	p.Safety = Safety{
		SafePeakRPS: peak,
		AllowHosts:  hosts,
		Note:        "safe_peak_rps is the busiest second actually measured in the window: a level production has already survived, which is the only kind of ceiling worth declaring. Going above it in a run is a deliberate act.",
	}

	// ── provenance ───────────────────────────────────────────────────────────
	p.Measured = Measured{
		By:           o.Tool,
		Source:       r.Source,
		Dialect:      r.Dialect,
		WindowStart:  stamp(r.Window.Start),
		WindowEnd:    stamp(r.Window.End),
		Zone:         r.Zone,
		Requests:     r.Counts.Parsed,
		PeakPerSec:   r.Rate.Peak,
		PeakAt:       stamp(r.Rate.PeakAt),
		MeanPerSec:   round1(r.Rate.Mean),
		LatencyField: r.LatencyField,
		DroppedPaths: r.PathsDropped,
	}
	if r.Latency != nil {
		p.Measured.LatencyP95Ms = r.Latency.P95
	}
	if r.Audience.Countable {
		p.Measured.AudienceNote = "the source log identifies clients, but this profile is a request mix and says nothing about how many people produced it"
	} else {
		p.Measured.AudienceNote = "the source log does not identify clients, so the number of people behind this mix is unknown — the weights are requests, not visitors"
	}
	if r.WindowSeconds > 0 && r.SilentSeconds*5 >= r.WindowSeconds {
		p.Measured.CoverageNote = fmt.Sprintf("%d of %d seconds in the window carry no logged request: the source log is probably not a complete record of the traffic, so these weights are of the logged subset",
			r.SilentSeconds, r.WindowSeconds)
		warn("%s", p.Measured.CoverageNote)
	}
	if r.Counts.MalformedShare() > 0.002 {
		warn("%s of request lines could not be read: the weights are computed over the rest", pctString(r.Counts.MalformedShare()))
	}
	if logfmt.IsCDN(r.Dialect) {
		origin := "an unknown share of it"
		if r.Cache != nil {
			origin = pctString(1-r.Cache.HitRatio) + " of it"
		}
		p.Measured.EdgeNote = fmt.Sprintf("this mix was measured on a %s log, above the cache: it is what the audience asked for, and the origin behind was asked for %s. Pointed at an origin, a run replays requests the CDN was absorbing, and safe_peak_rps is the peak the *edge* survived — aim the run at the CDN, or scale it to the miss share.",
			r.Dialect, origin)
		warn("the mix comes from a CDN log, so it is audience-side: the origin behind saw %s, and safe_peak_rps is a level the edge survived rather than the origin", origin)
	}

	sort.Strings(p.Safety.AllowHosts)
	return p, warns, nil
}

func hasClass(cs []Class, name string) bool {
	for _, c := range cs {
		if c.Name == name {
			return true
		}
	}
	return false
}

func kind(k string) string {
	if k == "rsc" {
		return "rsc"
	}
	return "" // crowdsim's default is "plain"; writing it would be noise
}

// poolPaths keeps the replayable paths and drops the ones that look like they
// carry something private, returning how many were dropped.
func poolPaths(ps []analyze.PathStat) ([]string, int) {
	out := make([]string, 0, len(ps))
	dropped := 0
	for _, p := range ps {
		if p.Path == "" || p.Path[0] != '/' {
			// "<BADREQ>", a CONNECT authority, an absolute-form URI: not a path
			// a generator can replay.
			dropped++
			continue
		}
		if looksSecret(p.Path) {
			dropped++
			continue
		}
		out = append(out, p.Path)
	}
	return out, dropped
}

// looksSecret is a coarse test for a path segment that is an opaque
// identifier — a session token, a signed URL, a password-reset link.
//
// It is deliberately blunt and errs towards dropping: a legitimate slug lost
// from a pool costs one URL out of forty, while a token copied into a profile
// that gets attached to a ticket costs rather more. Numeric ids are kept, since
// /news/48211 is a real page and a pool of them is what the site serves.
func looksSecret(path string) bool {
	for _, seg := range strings.Split(strings.Trim(path, "/"), "/") {
		if len(seg) < 20 {
			continue
		}
		digits, alnum, other := 0, 0, 0
		for i := 0; i < len(seg); i++ {
			c := seg[i]
			switch {
			case c >= '0' && c <= '9':
				digits++
				alnum++
			case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z':
				alnum++
			case c == '-', c == '_', c == '.':
			default:
				other++
			}
		}
		if other > 0 {
			continue
		}
		// A long segment with no word separator and a healthy share of digits
		// is an id, not a slug: slugs have hyphens and words.
		if !strings.ContainsAny(seg, "-_.") && digits*4 >= len(seg) {
			return true
		}
	}
	return false
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func round1(f float64) float64 {
	return float64(int(f*10+0.5)) / 10
}

func pctString(f float64) string { return fmt.Sprintf("%.1f%%", f*100) }
