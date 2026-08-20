package logfmt

import (
	"encoding/json"
	"strings"
	"time"
)

// Traefik reads the JSON access log (`accessLog.format: json`).
//
// Two of its fields are a gift for capacity work and one is a trap. The gift:
// `OriginDuration` is the wait on the service behind, separately from
// `Duration`, which is the whole exchange — the same distinction as HAProxy's Tr
// against Tt, and the only one that says whether the tier behind is busy. The
// trap: `DownstreamStatus` is what the client got, and on a retry or a circuit
// breaker it differs from what the origin answered.
type Traefik struct{}

// Name implements Parser.
func (Traefik) Name() string { return "traefik" }

// LatencyField implements Parser.
func (Traefik) LatencyField() string { return "OriginDuration (wait for the service)" }

// traefikLine is the subset edgemix reads. Traefik writes many more fields;
// decoding only these keeps a version that adds one from breaking the parser.
type traefikLine struct {
	StartUTC              string  `json:"StartUTC"`
	StartLocal            string  `json:"StartLocal"`
	RequestMethod         string  `json:"RequestMethod"`
	RequestPath           string  `json:"RequestPath"`
	RequestHost           string  `json:"RequestHost"`
	DownstreamStatus      int     `json:"DownstreamStatus"`
	OriginStatus          int     `json:"OriginStatus"`
	DownstreamContentSize int64   `json:"DownstreamContentSize"`
	Duration              float64 `json:"Duration"`       // nanoseconds
	OriginDuration        float64 `json:"OriginDuration"` // nanoseconds
	ServiceName           string  `json:"ServiceName"`
	RouterName            string  `json:"RouterName"`
	ClientHost            string  `json:"ClientHost"`
	CacheStatus           string  `json:"request_Cache-Status"`
}

// Parse implements Parser.
func (Traefik) Parse(line string) (Event, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Event{}, ErrSkip
	}
	if line[0] != '{' {
		// Traefik's own startup and plugin lines are not JSON objects. A JSON
		// line that fails to decode is a different matter and counts as one.
		return Event{}, ErrSkip
	}
	var l traefikLine
	if err := json.Unmarshal([]byte(line), &l); err != nil {
		return Event{}, ErrMalformed
	}
	if l.RequestPath == "" && l.RequestMethod == "" {
		// A JSON line from something else — a Traefik application log, which
		// shares the file when accessLog is not separated.
		return Event{}, ErrSkip
	}

	var ts time.Time
	for _, s := range []string{l.StartUTC, l.StartLocal} {
		if s == "" {
			continue
		}
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
			ts = t.UTC()
			break
		}
	}
	if ts.IsZero() {
		return Event{}, ErrMalformed
	}

	e := Event{
		Time:           ts,
		Method:         l.RequestMethod,
		Host:           strings.ToLower(l.RequestHost),
		Status:         l.DownstreamStatus,
		Bytes:          l.DownstreamContentSize,
		Frontend:       l.RouterName,
		Backend:        l.ServiceName,
		Cache:          strings.ToUpper(l.CacheStatus),
		ClientIdentity: l.ClientHost != "",
	}
	if e.Status == 0 {
		e.Status = l.OriginStatus
	}
	e.Path = l.RequestPath
	if i := strings.IndexByte(e.Path, '?'); i >= 0 {
		e.Path, e.Query = e.Path[:i], e.Path[i+1:]
	}
	if l.Duration > 0 {
		e.Total, e.HaveTotal = time.Duration(l.Duration), true
	}
	if l.OriginDuration > 0 {
		e.Response, e.HaveResponse = time.Duration(l.OriginDuration), true
	}
	if e.Path == "" {
		return Event{}, ErrMalformed
	}
	return e, nil
}
