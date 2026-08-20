package analyze

import (
	"sort"
	"time"

	"github.com/Allan-Nava/edgemix/internal/classify"
	"github.com/Allan-Nava/edgemix/internal/logfmt"
)

// Options configure a run.
type Options struct {
	Classes classify.Set
	// Tails are the wait thresholds reported as "share of traffic slower than".
	Tails []time.Duration
	// ReadTimeout is the reverse proxy's own read timeout — the point past
	// which a slow response becomes a 504 for a real visitor. The share of
	// traffic beyond it is the margin, and it is the one threshold that
	// produces a BAD rather than a number.
	ReadTimeout time.Duration
	// TopPaths is how many paths to list per class.
	TopPaths int
	// MaxPaths caps distinct paths kept per class. A path with a unique id in it
	// (or a scanner walking the site) can otherwise turn a log into unbounded
	// memory. Whatever is dropped is counted and reported.
	MaxPaths int
	// Since and Until restrict the window. Zero means unbounded.
	Since, Until time.Time
	// PoolSize is how many paths per class to keep for an emitted load-test
	// pool. Zero means none: the pools are the only part of a report that
	// carries raw paths, so they are opt-in.
	PoolSize int
	// PoolMinOKShare is the share of a path's responses that must have been 2xx
	// for it to enter a pool. A path that 404s is cheap to serve — often
	// cheaper than the render it stands in for — so a pool containing them
	// measures the error handler and reports a capacity that does not exist.
	PoolMinOKShare float64
	// Zone is the label for the timezone the timestamps were read in, stated in
	// the report because HAProxy's accept date carries no offset to read.
	Zone string
	// Version labels the report with the tool version.
	Version string
}

// DefaultTails are 1s, 3s and 7s: a second is where a visitor notices, three is
// where they leave, and seven is the read timeout a reverse proxy is commonly
// configured with — past which the request is a 504 whatever the app does next.
var DefaultTails = []time.Duration{time.Second, 3 * time.Second, 7 * time.Second}

func (o Options) withDefaults() Options {
	if len(o.Classes.Rules) == 0 && o.Classes.Fallback == "" {
		o.Classes = classify.Default()
	}
	if len(o.Tails) == 0 {
		o.Tails = DefaultTails
	}
	if o.ReadTimeout <= 0 {
		o.ReadTimeout = 7 * time.Second
	}
	if o.TopPaths <= 0 {
		o.TopPaths = 10
	}
	if o.MaxPaths <= 0 {
		o.MaxPaths = 200000
	}
	if o.PoolMinOKShare == 0 {
		o.PoolMinOKShare = 0.95
	}
	if o.Zone == "" {
		o.Zone = "UTC"
	}
	return o
}

// Analyzer folds events into a report. Add is called once per event, in file
// order; Report closes the fold. It is not safe for concurrent use — the parse
// is a stream and making this concurrent would buy a lock, not throughput.
type Analyzer struct {
	o Options

	total   *acc
	classes map[string]*acc
	order   []string

	statuses map[string]int
	term     map[string]int
	hosts    map[string]int

	identity     int
	filtered     int
	pathsDropped int

	first, last time.Time
}

// acc accumulates one class (or the whole stream).
type acc struct {
	count  int
	bytes  int64
	perSec map[int64]int

	// lat holds each measured wait in milliseconds. int32 keeps a hundred
	// million requests inside a few hundred megabytes, and a wait beyond 24
	// days is not a number worth a wider type.
	lat    []int32
	latSum time.Duration

	statuses map[string]int
	cache    map[string]int
	cacheN   int

	paths map[string]*pathAcc

	peak   int
	peakAt time.Time
}

type pathAcc struct{ count, ok, fivexx int }

func newAcc() *acc {
	return &acc{
		perSec:   map[int64]int{},
		statuses: map[string]int{},
		cache:    map[string]int{},
		paths:    map[string]*pathAcc{},
	}
}

// New returns an Analyzer with defaults filled in.
func New(o Options) *Analyzer {
	o = o.withDefaults()
	return &Analyzer{
		o:        o,
		total:    newAcc(),
		classes:  map[string]*acc{},
		statuses: map[string]int{},
		term:     map[string]int{},
		hosts:    map[string]int{},
	}
}

// Filtered is how many events fell outside --since/--until. It is reported so a
// window that quietly excluded most of the log is visible rather than implied.
func (a *Analyzer) Filtered() int { return a.filtered }

// Add folds one event in.
func (a *Analyzer) Add(e logfmt.Event) {
	if !a.o.Since.IsZero() && e.Time.Before(a.o.Since) {
		a.filtered++
		return
	}
	if !a.o.Until.IsZero() && !e.Time.Before(a.o.Until) {
		a.filtered++
		return
	}

	if a.first.IsZero() || e.Time.Before(a.first) {
		a.first = e.Time
	}
	if e.Time.After(a.last) {
		a.last = e.Time
	}

	class := a.o.Classes.Classify(e)
	c, ok := a.classes[class]
	if !ok {
		c = newAcc()
		a.classes[class] = c
		a.order = append(a.order, class)
	}

	a.addTo(a.total, e)
	a.addTo(c, e)

	a.statuses[e.StatusClass()]++
	if e.TermState != "" {
		a.term[e.TermState]++
	}
	if e.Host != "" {
		a.hosts[e.Host]++
	}
	if e.ClientIdentity {
		a.identity++
	}
}

func (a *Analyzer) addTo(c *acc, e logfmt.Event) {
	c.count++
	c.bytes += e.Bytes

	sec := e.Time.Unix()
	c.perSec[sec]++
	if n := c.perSec[sec]; n > c.peak {
		c.peak, c.peakAt = n, time.Unix(sec, 0).UTC()
	}

	if d, ok := e.Latency(); ok {
		ms := d.Milliseconds()
		if ms > 1<<31-1 {
			ms = 1<<31 - 1
		}
		c.lat = append(c.lat, int32(ms))
		c.latSum += d
	}

	c.statuses[e.StatusClass()]++
	if e.Cache != "" {
		c.cacheN++
		c.cache[e.Cache]++
	}

	p, ok := c.paths[e.Path]
	if !ok {
		if len(c.paths) >= a.o.MaxPaths {
			a.pathsDropped++
			return
		}
		p = &pathAcc{}
		c.paths[e.Path] = p
	}
	p.count++
	if e.Status >= 200 && e.Status < 300 {
		p.ok++
	}
	if e.Status >= 500 {
		p.fivexx++
	}
}

// Report closes the fold. source names the log, p the dialect it was read with,
// and counts what the scan saw.
func (a *Analyzer) Report(source string, p logfmt.Parser, counts logfmt.Counts) Report {
	r := Report{
		Tool:         a.o.Version,
		Source:       source,
		Dialect:      p.Name(),
		LatencyField: p.LatencyField(),
		Zone:         a.o.Zone,
		Counts:       counts,
		PathsDropped: a.pathsDropped,
	}
	r.Window.Start, r.Window.End = a.first, a.last
	r.WindowSeconds = r.Window.Seconds()

	r.Rate = rate(a.total, r.WindowSeconds)
	r.SilentSeconds = r.WindowSeconds - len(a.total.perSec)
	if r.SilentSeconds < 0 {
		r.SilentSeconds = 0
	}

	r.Statuses = statusCounts(a.total.statuses, a.total.count)
	r.Termination = topCounts(a.term, a.total.count, 5)
	r.Latency = a.latency(a.total, p.LatencyField())
	r.Cache = cacheStat(a.total, p)
	r.TopPaths = topPaths(a.total, a.o.TopPaths)
	r.Top5xx = top5xx(a.total, a.o.TopPaths)
	r.PathsSeen = len(a.total.paths)
	r.Hosts = topHosts(a.hosts)

	r.Audience.WithIdentity = a.identity
	if a.total.count > 0 {
		r.Audience.Share = float64(a.identity) / float64(a.total.count)
	}
	// "Nearly every line" rather than "some": a log where a tenth of the lines
	// carry an address cannot be counted, only misread.
	r.Audience.Countable = a.total.count > 0 && r.Audience.Share >= 0.99

	// Classes in the order the rule set declares them, so two reports over
	// different logs of the same site line up column by column.
	for _, name := range a.o.Classes.Names() {
		c, ok := a.classes[name]
		if !ok {
			continue
		}
		cs := ClassStat{
			Name:          name,
			Label:         a.o.Classes.Label(name),
			Kind:          a.o.Classes.Kind(name),
			Count:         c.count,
			Bytes:         c.bytes,
			PeakPerSec:    c.peak,
			DistinctPaths: len(c.paths),
			Statuses:      statusCounts(c.statuses, c.count),
			Latency:       a.latency(c, p.LatencyField()),
			Cache:         cacheStat(c, p),
			TopPaths:      topPaths(c, a.o.TopPaths),
		}
		if a.total.count > 0 {
			cs.Share = float64(c.count) / float64(a.total.count)
		}
		r.Classes = append(r.Classes, cs)
	}

	if a.o.PoolSize > 0 {
		r.Pools = a.pools()
	}

	r.Findings = a.findings(&r)
	return r
}

// pools picks, per class, the paths a load test could replay: the most
// requested ones that actually rendered. Hot-path concentration is kept rather
// than flattened — real traffic piles onto a handful of keys, and a pool of
// evenly spread cold URLs is a harsher test than the event it claims to replay.
func (a *Analyzer) pools() map[string][]PathStat {
	out := map[string][]PathStat{}
	for name, c := range a.classes {
		all := topPaths(c, len(c.paths))
		kept := make([]PathStat, 0, a.o.PoolSize)
		for _, p := range all {
			if len(kept) >= a.o.PoolSize {
				break
			}
			if p.OKShare < a.o.PoolMinOKShare {
				continue
			}
			kept = append(kept, p)
		}
		if len(kept) > 0 {
			out[name] = kept
		}
	}
	return out
}

// rate computes the per-second distribution over the whole window.
//
// The zero seconds are counted without being stored: percentiles are taken over
// windowSeconds values, of which (windowSeconds - observed) are zero, by
// offsetting the index into the sorted observed counts. A week-long log
// therefore costs nothing beyond the seconds that carry traffic.
func rate(c *acc, windowSeconds int) RateStat {
	var st RateStat
	st.Peak, st.PeakAt = c.peak, c.peakAt
	if windowSeconds <= 0 || c.count == 0 {
		return st
	}
	st.Mean = float64(c.count) / float64(windowSeconds)

	counts := make([]int, 0, len(c.perSec))
	for _, n := range c.perSec {
		counts = append(counts, n)
	}
	sort.Ints(counts)
	silent := windowSeconds - len(counts)
	if silent < 0 {
		silent = 0
	}
	at := func(p float64) float64 {
		idx := int(p*float64(windowSeconds)+0.5) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= windowSeconds {
			idx = windowSeconds - 1
		}
		if idx < silent {
			return 0
		}
		i := idx - silent
		if i >= len(counts) {
			i = len(counts) - 1
		}
		return float64(counts[i])
	}
	st.P50, st.P95, st.P99 = at(0.50), at(0.95), at(0.99)
	return st
}

func (a *Analyzer) latency(c *acc, field string) *LatencyStat {
	if len(c.lat) == 0 {
		return nil
	}
	lat := make([]int32, len(c.lat))
	copy(lat, c.lat)
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	pick := func(p float64) float64 {
		idx := int(p*float64(len(lat))+0.5) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(lat) {
			idx = len(lat) - 1
		}
		return float64(lat[idx])
	}
	st := &LatencyStat{
		Field:    field,
		Measured: len(lat),
		P50:      pick(0.50),
		P95:      pick(0.95),
		P99:      pick(0.99),
		Max:      float64(lat[len(lat)-1]),
		Sum:      c.latSum,
	}
	for _, t := range a.o.Tails {
		ms := int32(t.Milliseconds())
		// The count above the threshold, found by binary search on the sorted
		// waits rather than by a second pass.
		i := sort.Search(len(lat), func(i int) bool { return lat[i] > ms })
		over := len(lat) - i
		st.Tails = append(st.Tails, TailStat{
			OverMs: float64(ms),
			Count:  over,
			Share:  float64(over) / float64(len(lat)),
		})
	}
	return st
}

func cacheStat(c *acc, p logfmt.Parser) *CacheStat {
	if c.cacheN == 0 {
		return nil
	}
	field := "cache verdict"
	switch p.Name() {
	case "nginx":
		field = "$upstream_cache_status"
	case "traefik":
		field = "Cache-Status"
	case "cloudfront":
		field = "x-edge-result-type"
	case "akamai":
		field = "cacheStatus"
	}
	st := &CacheStat{Field: field, Measured: c.cacheN}
	// Every verdict here means the request was answered without the origin
	// being asked, which is what "hit" has to mean for a capacity question: the
	// request that did not reach the app tier.
	//
	// The CDN vocabularies are folded in for that reason and not for
	// convenience. CloudFront's REFRESHHIT revalidated with the origin but
	// served from the edge; ORIGINSHIELDHIT was answered by the shield layer,
	// so the origin never saw it either. LIMITEXCEEDED and CAPACITYEXCEEDED are
	// the CDN refusing — the origin was spared, and counting them as hits would
	// read a throttled visitor as a cached one, so they are not here.
	hitish := map[string]bool{
		"HIT": true, "STALE": true, "UPDATING": true, "REVALIDATED": true,
		"REFRESHHIT": true, "ORIGINSHIELDHIT": true,
	}
	for v, n := range c.cache {
		if hitish[v] {
			st.Hits += n
		}
	}
	st.HitRatio = float64(st.Hits) / float64(c.cacheN)
	st.Verdicts = topCounts(c.cache, c.cacheN, 8)
	return st
}

// statusCounts renders the status classes in a fixed order, omitting the empty
// ones — a report that lists "1xx: 0" every time trains the eye to skip the row.
func statusCounts(m map[string]int, total int) []Count {
	var out []Count
	for _, k := range []string{"2xx", "3xx", "4xx", "5xx", "1xx", "other"} {
		if n, ok := m[k]; ok && n > 0 {
			out = append(out, Count{Label: k, Count: n, Share: share(n, total)})
		}
	}
	return out
}

func topCounts(m map[string]int, total, n int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{Label: k, Count: v, Share: share(v, total)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func topPaths(c *acc, n int) []PathStat {
	out := make([]PathStat, 0, len(c.paths))
	for p, s := range c.paths {
		out = append(out, PathStat{
			Path:     p,
			Count:    s.count,
			Share:    share(s.count, c.count),
			OKShare:  share(s.ok, s.count),
			Worst5xx: s.fivexx,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

func top5xx(c *acc, n int) []PathStat {
	out := make([]PathStat, 0)
	for p, s := range c.paths {
		if s.fivexx == 0 {
			continue
		}
		out = append(out, PathStat{
			Path:     p,
			Count:    s.count,
			Share:    share(s.count, c.count),
			OKShare:  share(s.ok, s.count),
			Worst5xx: s.fivexx,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Worst5xx != out[j].Worst5xx {
			return out[i].Worst5xx > out[j].Worst5xx
		}
		return out[i].Path < out[j].Path
	})
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// topHosts lists the hostnames seen, most-seen first. They become the
// allowlist of an emitted profile, so the order is what a human reads first.
func topHosts(m map[string]int) []string {
	type kv struct {
		host string
		n    int
	}
	all := make([]kv, 0, len(m))
	for h, n := range m {
		all = append(all, kv{h, n})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].n != all[j].n {
			return all[i].n > all[j].n
		}
		return all[i].host < all[j].host
	})
	out := make([]string, 0, len(all))
	for _, e := range all {
		out = append(out, e.host)
	}
	if len(out) > 20 {
		out = out[:20]
	}
	return out
}

func share(n, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(n) / float64(total)
}
