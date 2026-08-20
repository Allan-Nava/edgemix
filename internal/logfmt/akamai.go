package logfmt

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
)

// Akamai reads a DataStream 2 log line: one JSON object per request.
//
//	{"cliIP":"203.0.113.7","reqTimeSec":"1787162798.123","reqMethod":"GET",
//	 "reqHost":"www.example.test","reqPath":"/news/latest","statusCode":"200",
//	 "cacheStatus":"1","turnAroundTimeMSec":"45","transferTimeMSec":"12"}
//
// Two properties of the format decide how it is read:
//
//   - **Every value is a string**, numbers and the timestamp included. A parser
//     that decodes `statusCode` into an int fails on the real thing; one that
//     only accepts strings fails on a pipeline that re-typed the record on its
//     way to S3. Both are accepted, and a value that is present but not a
//     number is an unreadable line rather than a zero.
//   - **`turnAroundTimeMSec` is the wait on the origin** — the edge having the
//     whole request until the origin's first byte — while `transferTimeMSec` is
//     the viewer reading the body. There is no field for the whole exchange,
//     and adding the two would produce a number that looks like nginx's
//     `$request_time` without being one, so this dialect reports no total at
//     all.
//
// Like CloudFront, this log sits above the cache: its numbers are what the
// audience asked for, and the origin saw only the misses. `cacheStatus` is 1
// for a hit and 0 for a miss; a stream configured without the field reports no
// verdict, which is not a miss.
//
// DataStream 2 is the format read here. The older Log Delivery Service files
// are a different shape and are not claimed — `--dialect akamai` on one of
// those reads nothing, which is the answer edgemix prefers to a wrong number.
type Akamai struct{}

// Name implements Parser.
func (Akamai) Name() string { return "akamai" }

// LatencyField implements Parser.
func (Akamai) LatencyField() string {
	return "turnAroundTimeMSec (the origin wait, measured at the edge; a cache hit never waits)"
}

// ds2Value is one DataStream 2 field, kept as the literal text it arrived as.
//
// Keeping the raw text rather than a float64 is not fussiness: an epoch like
// 1787162798.123 does not survive a float64 round trip to the millisecond, and
// the timestamp of every request in the window is the one number in this format
// that must be exact.
type ds2Value struct {
	raw     string
	present bool
}

// UnmarshalJSON accepts a string, a number or null, and treats "" and "-" as
// absent. It never fails: a value that is present but unreadable is decided by
// Parse, which knows whether that field mattered.
func (v *ds2Value) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" {
		return nil
	}
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	s = strings.TrimSpace(s)
	if s == "" || s == "-" {
		return nil
	}
	v.raw, v.present = s, true
	return nil
}

// number reads the value as an integer. The second bool separates "not there"
// from "there and not a number": the first is a field the stream does not
// carry, the second is a line edgemix could not read.
func (v ds2Value) number() (n int64, present, ok bool) {
	if !v.present {
		return 0, false, true
	}
	i, err := strconv.ParseInt(v.raw, 10, 64)
	if err != nil {
		// A float where an integer belongs (a byte count written 512.0) is
		// still readable.
		f, ferr := strconv.ParseFloat(v.raw, 64)
		if ferr != nil {
			return 0, true, false
		}
		return int64(f), true, true
	}
	return i, true, true
}

// millis reads the value as a duration written in milliseconds.
func (v ds2Value) millis() (d time.Duration, present, ok bool) {
	if !v.present {
		return 0, false, true
	}
	f, err := strconv.ParseFloat(v.raw, 64)
	if err != nil || f < 0 {
		return 0, true, false
	}
	return time.Duration(f * float64(time.Millisecond)), true, true
}

// epoch reads the value as Unix seconds with an optional fraction, textually.
// A fraction is padded or truncated to nanoseconds rather than multiplied
// through a float, so 1787162798.123 is 18:06:38.123 and not 18:06:38.12300014.
func (v ds2Value) epoch() (time.Time, bool) {
	if !v.present {
		return time.Time{}, false
	}
	secs, frac := v.raw, ""
	if i := strings.IndexByte(v.raw, '.'); i >= 0 {
		secs, frac = v.raw[:i], v.raw[i+1:]
	}
	s, err := strconv.ParseInt(secs, 10, 64)
	if err != nil || s <= 0 {
		return time.Time{}, false
	}
	if len(frac) > 9 {
		frac = frac[:9]
	}
	for len(frac) < 9 {
		frac += "0"
	}
	ns, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(s, ns).UTC(), true
}

// akamaiLine is the subset edgemix reads. DataStream 2 can be configured with
// dozens of fields; decoding only these keeps a stream that adds one from
// breaking the parser.
type akamaiLine struct {
	ReqTimeSec   ds2Value `json:"reqTimeSec"`
	ReqMethod    string   `json:"reqMethod"`
	ReqPath      string   `json:"reqPath"`
	ReqHost      string   `json:"reqHost"`
	QueryStr     string   `json:"queryStr"`
	StatusCode   ds2Value `json:"statusCode"`
	CacheStatus  ds2Value `json:"cacheStatus"`
	TurnAround   ds2Value `json:"turnAroundTimeMSec"`
	TotalBytes   ds2Value `json:"totalBytes"`
	CliIP        string   `json:"cliIP"`
	ForwardedFor string   `json:"xForwardedFor"`
}

// Parse implements Parser.
func (Akamai) Parse(line string) (Event, error) {
	line = strings.TrimSpace(line)
	if line == "" || line[0] != '{' {
		return Event{}, ErrSkip
	}
	var l akamaiLine
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return Event{}, ErrMalformed
	}
	// Traefik's access log is JSON too, and a parser that accepted any object
	// carrying a timestamp would turn the detection vote between the two into a
	// coin toss. A record with neither a path nor a method is not a DataStream 2
	// request line: a stream health record, or somebody else's log.
	if l.ReqPath == "" && l.ReqMethod == "" {
		return Event{}, ErrSkip
	}

	ts, ok := l.ReqTimeSec.epoch()
	if !ok {
		return Event{}, ErrMalformed
	}
	if l.ReqPath == "" {
		return Event{}, ErrMalformed
	}

	e := Event{
		Time:   ts,
		Method: l.ReqMethod,
		Path:   l.ReqPath,
		Query:  l.QueryStr,
		Host:   strings.ToLower(l.ReqHost),
	}
	if i := strings.IndexByte(e.Path, '?'); i >= 0 {
		// Some stream configurations put the query in reqPath. Left in place it
		// becomes a distinct path per query, which inflates the cardinality and
		// fills a profile pool with one-off URLs.
		q := e.Path[i+1:]
		e.Path = e.Path[:i]
		if e.Query == "" {
			e.Query = q
		}
	}

	code, present, ok := l.StatusCode.number()
	if !ok {
		return Event{}, ErrMalformed
	}
	if present {
		e.Status = int(code)
	}
	if b, _, ok := l.TotalBytes.number(); ok {
		e.Bytes = b
	}
	if d, present, ok := l.TurnAround.millis(); ok && present {
		e.Response, e.HaveResponse = d, true
	}
	if v, present, ok := l.CacheStatus.number(); ok && present {
		switch v {
		case 1:
			e.Cache = "HIT"
		case 0:
			e.Cache = "MISS"
		}
	}
	e.ClientIdentity = (l.CliIP != "" && l.CliIP != "-") ||
		(l.ForwardedFor != "" && l.ForwardedFor != "-")
	return e, nil
}
