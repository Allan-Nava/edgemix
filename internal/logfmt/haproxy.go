package logfmt

import (
	"strconv"
	"strings"
	"time"
)

// HAProxy reads `option httplog` lines, with or without a syslog prefix, with
// or without captured headers.
//
// The layout it anchors on:
//
//	… haproxy[123]: 10.0.0.9:53412 [19/Aug/2026:18:06:38.123] fe~ be/srv 0/0/1/25/26 200 1234 - - ---- 12/12/3/1/0 0/0 {host|ua} "GET /p HTTP/1.1"
//	                └ client       └ accept date              └ tiers └ timers    └ st └ bytes         └ term └ conns  └ queues └ captures └ request
//
// Anchors: the first […] that parses as a date, the last quoted field, and the
// five-timer field. Everything else is read at a fixed offset *from the timers*,
// never from the start of the line — which is what makes a syslog prefix, an
// extra capture or a different frontend name harmless.
type HAProxy struct {
	// Location interprets the accept date. HAProxy writes local time with no
	// zone offset, so there is nothing in the line to read it from: the report
	// states which zone was assumed. Nil means UTC.
	Location *time.Location
	// XFFCapture is the 1-based position of X-Forwarded-For inside the captured
	// request headers ({a|b|c}), when the frontend declares one. Its presence
	// is all that is recorded — the value is never kept.
	XFFCapture int
}

// Name implements Parser.
func (HAProxy) Name() string { return "haproxy" }

// LatencyField implements Parser.
func (HAProxy) LatencyField() string { return "Tr (wait for the server's response)" }

var haproxyDateLayouts = []string{"02/Jan/2006:15:04:05.000", "02/Jan/2006:15:04:05"}

// isTimers reports whether f is HAProxy's Tq/Tw/Tc/Tr/Tt field. Any of the five
// may be -1 (the phase never completed), which is why this is not a digits-only
// test.
func isTimers(f string) bool {
	parts := strings.Split(f, "/")
	if len(parts) != 5 {
		return false
	}
	for _, p := range parts {
		if p == "" {
			return false
		}
		if _, err := strconv.Atoi(p); err != nil {
			return false
		}
	}
	return true
}

// msField reads one timer as a duration. A negative timer means the phase did
// not complete, and is reported as absent rather than as zero.
func msField(s string) (time.Duration, bool) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, false
	}
	return time.Duration(n) * time.Millisecond, true
}

// Parse implements Parser.
func (h HAProxy) Parse(line string) (Event, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Event{}, ErrSkip
	}

	// The bracketed accept date, which is also where the request record begins:
	// anything before it is a syslog prefix and a client address.
	//
	// Every bracketed group is tried, not just the first one — with a syslog
	// prefix the first one is the daemon's own pid (`haproxy[2411]`), and
	// stopping there is how a reader ends up skipping every line of a
	// syslog-shipped log.
	loc := h.Location
	if loc == nil {
		loc = time.UTC
	}
	var ts time.Time
	open, end, ok := -1, -1, false
	for from := 0; ; {
		o := strings.IndexByte(line[from:], '[')
		if o < 0 {
			break
		}
		o += from
		e := strings.IndexByte(line[o:], ']')
		if e < 0 {
			break
		}
		e += o
		for _, layout := range haproxyDateLayouts {
			if t, err := time.ParseInLocation(layout, line[o+1:e], loc); err == nil {
				ts, open, end, ok = t, o, e, true
				break
			}
		}
		if ok {
			break
		}
		from = o + 1
	}
	if !ok {
		// No […] that is a date: a daemon message ("[ALERT] …", "Proxy fe
		// started."), not a request record.
		return Event{}, ErrSkip
	}

	fields := splitFields(line[end+1:])
	timers := -1
	for i, f := range fields {
		if isTimers(f) {
			timers = i
			break
		}
	}
	if timers < 0 {
		// Dated, but with no timers: a connection log (`option tcplog` without
		// timers) or a truncated line. It looked like a request record, so it
		// counts as one edgemix could not read.
		return Event{}, ErrMalformed
	}

	e := Event{Time: ts}

	// The two fields before the timers are the tiers. On a line where a capture
	// sits between them this still holds: HAProxy writes captures after the
	// queues, never before the timers.
	if timers >= 2 {
		e.Frontend = fields[timers-2]
		be := fields[timers-1]
		if i := strings.IndexByte(be, '/'); i >= 0 {
			e.Backend, e.Server = be[:i], be[i+1:]
		} else {
			e.Backend = be
		}
	}

	tf := strings.Split(fields[timers], "/")
	e.Wait, e.HaveWait = msField(tf[1])
	e.Response, e.HaveResponse = msField(tf[3])
	e.Total, e.HaveTotal = msField(tf[4])

	at := func(off int) string {
		if i := timers + off; i < len(fields) {
			return fields[i]
		}
		return ""
	}
	if n, err := strconv.Atoi(at(1)); err == nil {
		e.Status = n
	} else {
		return Event{}, ErrMalformed
	}
	if n, err := strconv.ParseInt(at(2), 10, 64); err == nil {
		e.Bytes = n
	}
	if ts := at(5); len(ts) == 4 && ts != "----" {
		e.TermState = ts
	}

	// The request is the last quoted field. Scanning from the end is what keeps
	// a captured header containing a quote from being read as the request.
	if i := strings.LastIndexByte(line, '"'); i > 0 {
		if j := strings.LastIndexByte(line[:i], '"'); j >= 0 {
			e.Method, e.Path, e.Query = splitTarget(line[j+1 : i])
		}
	}
	if e.Path == "" {
		return Event{}, ErrMalformed
	}

	// A client address before the date is the usual identity. A captured
	// X-Forwarded-For is the real one behind a CDN — and a frontend that
	// captures it but does not print it is the case this flag exists for.
	if pre := strings.Fields(line[:open]); len(pre) > 0 {
		last := pre[len(pre)-1]
		if strings.Contains(last, ":") && !strings.HasSuffix(last, ":") {
			e.ClientIdentity = true
		}
	}
	for _, f := range fields {
		if len(f) > 1 && f[0] == '{' {
			caps := strings.Split(unquote(f), "|")
			if h.XFFCapture > 0 && h.XFFCapture <= len(caps) {
				// A dash is HAProxy writing "the header was not there", and
				// counting it as an identity would make an unanswerable
				// audience question look answerable.
				if v := strings.TrimSpace(caps[h.XFFCapture-1]); v != "" && v != "-" {
					e.ClientIdentity = true
				}
			}
			// A captured Host is worth keeping: it is what names the site in an
			// emitted profile, and a multi-tenant frontend has several.
			for _, c := range caps {
				if e.Host == "" && looksLikeHost(c) {
					e.Host = strings.ToLower(c)
				}
			}
		}
	}
	return e, nil
}

// looksLikeHost is deliberately strict: a captured header slot may hold a user
// agent, a cookie or a dash, and mistaking one for a hostname would put it in
// an emitted profile's allowlist.
func looksLikeHost(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" || s == "-" || len(s) > 253 || strings.ContainsAny(s, " /\\@") {
		return false
	}
	if i := strings.LastIndexByte(s, ':'); i >= 0 {
		s = s[:i] // host:port
	}
	if !strings.Contains(s, ".") {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}
