package logfmt

import (
	"strconv"
	"strings"
	"time"
)

// Nginx reads the `combined` log format, plus the two extras that make an nginx
// log worth reading for capacity work when they are appended to it:
//
//	10.0.0.9 - - [19/Aug/2026:18:06:38 +0000] "GET /p HTTP/1.1" 200 1234 "-" "UA" HIT 0.042
//	                                                                                └ $upstream_cache_status, $request_time
//
// Both are optional and read by shape, not position: a token that is a cache
// verdict is one, a bare number is a request time. Anything else in the tail is
// left alone rather than guessed at — a log_format edgemix does not understand
// must not produce a number that looks measured.
type Nginx struct{}

// Name implements Parser.
func (Nginx) Name() string { return "nginx" }

// LatencyField implements Parser.
func (Nginx) LatencyField() string { return "$request_time (whole exchange, client read included)" }

const nginxDateLayout = "02/Jan/2006:15:04:05 -0700"

// cacheVerdicts are the values $upstream_cache_status can take. MISS and BYPASS
// are verdicts too: the distinction between "the cache was asked and said no"
// and "this layer has no cache" is the whole point of keeping the field.
var cacheVerdicts = map[string]bool{
	"HIT": true, "MISS": true, "BYPASS": true, "EXPIRED": true,
	"STALE": true, "UPDATING": true, "REVALIDATED": true, "SCARCE": true,
}

// Parse implements Parser.
func (n Nginx) Parse(line string) (Event, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return Event{}, ErrSkip
	}
	fields := splitFields(line)
	// remote_addr, -, remote_user, [time], "request", status, bytes → 7 minimum.
	if len(fields) < 7 {
		return Event{}, ErrSkip
	}

	// The date is the first bracketed field; the request the first quoted one
	// after it. Anchoring this way survives a remote_user with a space in it,
	// which shifts every later column.
	dateAt := -1
	for i, f := range fields {
		if len(f) > 1 && f[0] == '[' {
			dateAt = i
			break
		}
	}
	if dateAt < 0 {
		return Event{}, ErrSkip
	}
	ts, err := time.Parse(nginxDateLayout, unquote(fields[dateAt]))
	if err != nil {
		return Event{}, ErrSkip
	}
	reqAt := -1
	for i := dateAt + 1; i < len(fields); i++ {
		if len(fields[i]) > 1 && fields[i][0] == '"' {
			reqAt = i
			break
		}
	}
	if reqAt < 0 || reqAt+2 >= len(fields) {
		return Event{}, ErrMalformed
	}

	e := Event{Time: ts, ClientIdentity: fields[0] != "-"}
	e.Method, e.Path, e.Query = splitTarget(unquote(fields[reqAt]))
	if e.Path == "" {
		return Event{}, ErrMalformed
	}
	if v, err := strconv.Atoi(fields[reqAt+1]); err == nil {
		e.Status = v
	} else {
		return Event{}, ErrMalformed
	}
	if v, err := strconv.ParseInt(fields[reqAt+2], 10, 64); err == nil {
		e.Bytes = v
	}

	// The tail: referer and user agent are quoted and skipped, a cache verdict
	// and a request time are recognised by shape.
	for _, f := range fields[reqAt+3:] {
		if len(f) > 0 && (f[0] == '"' || f[0] == '[') {
			if v := strings.ToUpper(strings.TrimSpace(unquote(f))); cacheVerdicts[v] {
				e.Cache = v
			}
			continue
		}
		if v := strings.ToUpper(f); cacheVerdicts[v] {
			e.Cache = v
			continue
		}
		if secs, err := strconv.ParseFloat(f, 64); err == nil && secs >= 0 && !e.HaveTotal {
			e.Total = time.Duration(secs * float64(time.Second))
			e.HaveTotal = true
		}
	}
	return e, nil
}
