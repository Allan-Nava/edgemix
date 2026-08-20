package logfmt

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// CloudFront reads an Amazon CloudFront standard access log.
//
//	#Version: 1.0
//	#Fields: date time x-edge-location sc-bytes c-ip cs-method cs(Host) cs-uri-stem sc-status …
//	2026-08-19→18:06:38→MXP64-C1→15321→203.0.113.7→GET→d111.cloudfront.net→/news/latest→200→…
//
// This is the one dialect that names its own columns, and the parser reads them
// by name for the same reason the others anchor on shape: the field list is not
// fixed. It has grown over the years, a distribution can be configured with
// fewer fields, and two files written years apart and concatenated into one
// window carry two different headers. A positional reader survives a test
// corpus and then reports `sc-status` values that are really byte counts.
//
// Three things about a CDN log are different in kind, not in detail, and the
// report has to keep saying so:
//
//   - It sits *above* the cache. Everything the edge answered never reached the
//     origin, so these numbers are what the audience asked for, and the tier
//     behind saw only the misses. Every other dialect here is the opposite way
//     round.
//   - `time-taken` is the whole exchange at the edge, viewer read included, and
//     `time-to-first-byte` is the wait. On a hit that wait is the cache
//     answering, not the origin, so a p95 over all requests blends two
//     different systems. LatencyField says this out loud because a report that
//     does not gets compared with an origin one.
//   - `x-host-header` is the host the viewer asked for; `cs(Host)` is the
//     distribution domain. Reading the wrong one reports every request as going
//     to d111111abcdef8.cloudfront.net, which then lands in an emitted
//     profile's allow_hosts and aims a load test at the CDN.
type CloudFront struct {
	// fields maps a lower-cased column name to its index, from the last
	// `#Fields:` line seen. It is parser state, which no other dialect here
	// needs — hence the pointer receiver — and it is rebuilt every time a
	// header appears, so a second file in the same window is read through its
	// own header rather than the previous one's.
	fields map[string]int
}

// Name implements Parser.
func (*CloudFront) Name() string { return "cloudfront" }

// LatencyField implements Parser.
func (*CloudFront) LatencyField() string {
	return "time-to-first-byte (the wait measured at the edge; on a cache hit the edge answered, not the origin)"
}

// cloudFrontDateLayout is date and time as two columns, always UTC. There is
// nothing to interpret here, so --tz does not apply and the report says UTC.
const cloudFrontDateLayout = "2006-01-02 15:04:05"

// Parse implements Parser.
func (c *CloudFront) Parse(line string) (Event, error) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return Event{}, ErrSkip
	}
	if trimmed[0] == '#' {
		if f, ok := cloudFrontHeader(trimmed); ok {
			c.fields = f
		}
		// A `#Version:` line, a comment, or the header itself: not a request
		// record, and not a coverage hole either.
		return Event{}, ErrSkip
	}
	if c.fields == nil {
		// The columns are named nowhere but in the header. Assuming the current
		// standard order would read any differently-configured distribution as
		// plausible nonsense, so a file whose header was stripped is refused
		// rather than guessed at. Keep the rotation intact, or concatenate the
		// files with their headers.
		return Event{}, ErrMalformed
	}

	// Tabs, split without collapsing: CloudFront writes `-` for an empty value,
	// but a stray empty column must shift nothing.
	cols := strings.Split(line, "\t")
	get := func(name string) string {
		i, ok := c.fields[name]
		if !ok || i >= len(cols) {
			return ""
		}
		v := strings.TrimSpace(cols[i])
		if v == "-" {
			return "" // absent, which is never a zero and never a literal dash
		}
		return v
	}

	date, clock := get("date"), get("time")
	if date == "" || clock == "" {
		return Event{}, ErrMalformed
	}
	ts, err := time.Parse(cloudFrontDateLayout, date+" "+clock)
	if err != nil {
		return Event{}, ErrMalformed
	}

	path := get("cs-uri-stem")
	if path == "" {
		return Event{}, ErrMalformed
	}
	status := get("sc-status")
	if status == "" {
		return Event{}, ErrMalformed
	}
	// `000` is CloudFront saying the viewer closed the connection before an
	// answer. It is a request that happened, so it is counted with status 0
	// rather than dropped: a load that produced no response is still a load.
	code, err := strconv.Atoi(status)
	if err != nil {
		return Event{}, ErrMalformed
	}

	e := Event{
		Time:     ts,
		Method:   get("cs-method"),
		Path:     path,
		Query:    get("cs-uri-query"),
		Status:   code,
		Frontend: get("x-edge-location"),
		Cache:    strings.ToUpper(get("x-edge-result-type")),
	}
	if e.Query == "" {
		// Some configurations write the query into the stem.
		if i := strings.IndexByte(e.Path, '?'); i >= 0 {
			e.Path, e.Query = e.Path[:i], e.Path[i+1:]
		}
	}
	if h := get("x-host-header"); h != "" {
		e.Host = strings.ToLower(h)
	} else {
		e.Host = strings.ToLower(get("cs(host)"))
	}
	if v, err := strconv.ParseInt(get("sc-bytes"), 10, 64); err == nil {
		e.Bytes = v
	}
	if d, ok := secondsField(get("time-taken")); ok {
		e.Total, e.HaveTotal = d, true
	}
	if d, ok := secondsField(get("time-to-first-byte")); ok {
		e.Response, e.HaveResponse = d, true
	}
	// c-ip is the viewer address as the edge saw it; x-forwarded-for is what
	// arrived in the header. Either answers "does this log identify a client at
	// all", which is the only audience question edgemix will touch.
	e.ClientIdentity = get("c-ip") != "" || get("x-forwarded-for") != ""
	return e, nil
}

// cloudFrontHeader reads a `#Fields:` line into a name → index map.
func cloudFrontHeader(line string) (map[string]int, bool) {
	if !strings.HasPrefix(strings.ToLower(line), "#fields:") {
		return nil, false
	}
	names := strings.Fields(line[len("#fields:"):])
	if len(names) == 0 {
		return nil, false
	}
	m := make(map[string]int, len(names))
	for i, n := range names {
		m[strings.ToLower(n)] = i
	}
	return m, true
}

// secondsField reads a duration written in seconds with a fraction, as
// CloudFront writes its timers. It rounds rather than truncates: 0.045 is not
// exact in binary, and a truncating conversion turns it into 44.999999ms —
// which is harmless in a percentile and confusing in a test.
func secondsField(s string) (time.Duration, bool) {
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v < 0 {
		return 0, false
	}
	return time.Duration(math.Round(v * float64(time.Second))), true
}
