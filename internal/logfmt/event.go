// Package logfmt turns one line of an access log into one Event.
//
// The parsers here anchor on the *shape* of a line — the bracketed date, the
// quoted request at the end, the five slash-separated timers — rather than on
// field positions. A positional reader breaks the moment a syslog prefix, a
// captured header or a custom log-format shifts the columns by one, and the
// way it breaks is silent: it reports a plausible wrong number. Every awk
// one-liner this tool replaces was rewritten at least once for exactly that.
//
// The second rule is that an unreadable line is counted, never guessed. A field
// a log does not carry stays absent (the Have* flags), and a check that needs
// it says so instead of substituting a zero.
package logfmt

import (
	"errors"
	"strings"
	"time"
)

// Event is one request, as much of it as the log actually recorded.
type Event struct {
	Time   time.Time
	Method string
	Path   string // without the query string
	Query  string // raw, may carry tokens: never rendered unredacted
	Host   string
	Status int
	Bytes  int64

	// Frontend/Backend/Server name the tiers the line attributes the request
	// to. Traefik calls them router and service; the names are kept generic.
	Frontend string
	Backend  string
	Server   string

	// Wait is time queued before a server was picked (HAProxy Tw), Response is
	// time waiting for the server's response (HAProxy Tr, Traefik
	// OriginDuration), Total is the whole exchange (Tt, Duration,
	// $request_time). Response is the one that says "the tier behind is busy";
	// Total includes the client reading the body, which a slow phone inflates.
	Wait     time.Duration
	Response time.Duration
	Total    time.Duration

	HaveWait     bool
	HaveResponse bool
	HaveTotal    bool

	// Cache is the layer's own verdict on this response — nginx
	// $upstream_cache_status, an X-Cache capture, RFC 9211 Cache-Status.
	// Empty means the log does not carry one, which is not a miss.
	Cache string

	// TermState is HAProxy's four-character termination state. `sD` in the
	// first two positions is a server timeout, `cD` a client one, and the
	// difference decides whether a 5xx is yours.
	TermState string

	// ClientIdentity records that the line carried something that identifies a
	// client (a source address, a captured X-Forwarded-For). It is a boolean on
	// purpose: edgemix never stores or prints the value, but whether one exists
	// at all decides if audience questions can be answered from this log.
	ClientIdentity bool
}

// StatusClass is "2xx", "5xx", … or "other" for a status outside 1xx–5xx.
func (e Event) StatusClass() string {
	switch {
	case e.Status >= 100 && e.Status < 200:
		return "1xx"
	case e.Status >= 200 && e.Status < 300:
		return "2xx"
	case e.Status >= 300 && e.Status < 400:
		return "3xx"
	case e.Status >= 400 && e.Status < 500:
		return "4xx"
	case e.Status >= 500 && e.Status < 600:
		return "5xx"
	}
	return "other"
}

// Latency is the field a queue check should read: the server response wait when
// the log has it, the total exchange otherwise, and (0, false) when it has
// neither. Callers must honour the bool — a zero here would read as a fast
// request rather than as an unmeasured one.
func (e Event) Latency() (time.Duration, bool) {
	if e.HaveResponse {
		return e.Response, true
	}
	if e.HaveTotal {
		return e.Total, true
	}
	return 0, false
}

// Parser reads one dialect of access log.
type Parser interface {
	// Name is the dialect id used in reports and on the --dialect flag.
	Name() string
	// LatencyField names what Latency() means in this dialect, for the report
	// to quote. "Tr (server response wait)" and "$request_time" are not the
	// same measurement and must not be compared across dialects.
	LatencyField() string
	Parse(line string) (Event, error)
}

// ErrSkip means the line is not a request record — a daemon message, a header,
// a blank line. Skipped lines are counted separately from failures, because a
// startup banner is not a coverage hole and a request edgemix could not read is.
var ErrSkip = errors.New("not a request line")

// ErrMalformed means the line looks like a request record of this dialect but
// could not be read. These are counted and reported: above a small share they
// invalidate every rate below them.
var ErrMalformed = errors.New("malformed request line")

// splitFields splits a log line on spaces, keeping "quoted strings",
// [bracketed dates] and {captured|headers} together as single fields.
func splitFields(s string) []string {
	var out []string
	var cur strings.Builder
	var closer byte
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if closer != 0 {
			cur.WriteByte(c)
			if c == closer {
				closer = 0
				flush()
			}
			continue
		}
		switch c {
		case ' ', '\t':
			flush()
		case '"':
			flush()
			cur.WriteByte(c)
			closer = '"'
		case '[':
			flush()
			cur.WriteByte(c)
			closer = ']'
		case '{':
			flush()
			cur.WriteByte(c)
			closer = '}'
		default:
			cur.WriteByte(c)
		}
	}
	flush()
	return out
}

// unquote strips one layer of surrounding "…", […] or {…}.
func unquote(s string) string {
	if len(s) < 2 {
		return s
	}
	switch {
	case s[0] == '"' && s[len(s)-1] == '"',
		s[0] == '[' && s[len(s)-1] == ']',
		s[0] == '{' && s[len(s)-1] == '}':
		return s[1 : len(s)-1]
	}
	return s
}

// splitTarget splits "GET /a/b?x=1 HTTP/1.1" into method, path and query.
// A request line the proxy itself could not read (HAProxy writes "<BADREQ>")
// yields that token as the path, so it can be counted rather than dropped.
func splitTarget(request string) (method, path, query string) {
	f := strings.Fields(request)
	switch len(f) {
	case 0:
		return "", "", ""
	case 1:
		path = f[0]
	default:
		method, path = f[0], f[1]
	}
	if i := strings.IndexByte(path, '?'); i >= 0 {
		path, query = path[:i], path[i+1:]
	}
	return method, path, query
}
