package logfmt

import (
	"errors"
	"testing"
	"time"
)

func TestHAProxy_SyslogPrefixAndCaptures(t *testing.T) {
	// The line every positional awk one-liner gets wrong: a syslog prefix in
	// front and a captured header block in the middle, both of which shift the
	// columns.
	line := `Aug 19 18:06:38 lb1 haproxy[2411]: 203.0.113.7:53412 [19/Aug/2026:18:06:38.123] fe_https~ be_app/app3 12/0/1/845/860 200 15321 - - ---- 1200/1200/8/2/0 0/0 {www.example.test|Mozilla/5.0|198.51.100.4} "GET /news/latest?page=2 HTTP/1.1"`
	p := HAProxy{XFFCapture: 3}
	e, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := e.Time.UTC().Format(time.RFC3339Nano), "2026-08-19T18:06:38.123Z"; got != want {
		t.Errorf("Time = %s, want %s", got, want)
	}
	if e.Method != "GET" || e.Path != "/news/latest" || e.Query != "page=2" {
		t.Errorf("request = %q %q %q", e.Method, e.Path, e.Query)
	}
	if e.Status != 200 || e.Bytes != 15321 {
		t.Errorf("status/bytes = %d/%d", e.Status, e.Bytes)
	}
	if e.Frontend != "fe_https~" || e.Backend != "be_app" || e.Server != "app3" {
		t.Errorf("tiers = %q %q %q", e.Frontend, e.Backend, e.Server)
	}
	if !e.HaveResponse || e.Response != 845*time.Millisecond {
		t.Errorf("Tr = %v (have %v), want 845ms", e.Response, e.HaveResponse)
	}
	if !e.HaveTotal || e.Total != 860*time.Millisecond {
		t.Errorf("Tt = %v", e.Total)
	}
	if !e.HaveWait || e.Wait != 0 {
		t.Errorf("Tw = %v (have %v)", e.Wait, e.HaveWait)
	}
	if e.Host != "www.example.test" {
		t.Errorf("Host = %q, want the captured one", e.Host)
	}
	if !e.ClientIdentity {
		t.Error("ClientIdentity = false, want true: the line has both a source address and a captured XFF")
	}
	if d, ok := e.Latency(); !ok || d != 845*time.Millisecond {
		t.Errorf("Latency() = %v, %v — must prefer Tr over Tt", d, ok)
	}
}

func TestHAProxy_NoSyslogPrefix(t *testing.T) {
	line := `10.0.0.9:44100 [19/Aug/2026:18:06:39.001] fe be/srv 0/0/0/12/12 304 0 - - ---- 3/3/0/0/0 0/0 "GET /favicon.ico HTTP/1.1"`
	e, err := HAProxy{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Path != "/favicon.ico" || e.Status != 304 {
		t.Fatalf("got %q %d", e.Path, e.Status)
	}
}

func TestHAProxy_IncompletePhasesAreAbsentNotZero(t *testing.T) {
	// A client that went away leaves -1 timers. Reading them as zero would
	// report the fastest requests in the log.
	line := `10.0.0.9:44100 [19/Aug/2026:18:06:39.001] fe be/<NOSRV> -1/-1/-1/-1/30001 503 0 - - CC-- 3/3/0/0/0 0/0 "GET /slow HTTP/1.1"`
	e, err := HAProxy{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.HaveResponse || e.HaveWait {
		t.Error("a -1 timer must be absent, not zero")
	}
	if !e.HaveTotal || e.Total != 30001*time.Millisecond {
		t.Errorf("Tt = %v, want 30.001s", e.Total)
	}
	if _, ok := e.Latency(); !ok {
		t.Error("Latency() must fall back to Tt when Tr is absent")
	}
	if e.TermState != "CC--" {
		t.Errorf("TermState = %q", e.TermState)
	}
}

func TestHAProxy_ServerTimeoutTermination(t *testing.T) {
	line := `10.0.0.9:44100 [19/Aug/2026:18:06:39.001] fe be/app1 0/0/0/-1/7001 504 0 - - sD-- 900/900/40/6/0 0/38 "GET /page/1 HTTP/1.1"`
	e, err := HAProxy{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.TermState != "sD--" {
		t.Fatalf("TermState = %q, want sD--: a server timeout is not the same failure as a client one", e.TermState)
	}
}

func TestHAProxy_BadRequestIsCountedNotDropped(t *testing.T) {
	line := `10.0.0.9:44100 [19/Aug/2026:18:06:39.001] fe fe/<NOSRV> -1/-1/-1/-1/0 400 187 - - PR-- 1/1/0/0/0 0/0 "<BADREQ>"`
	e, err := HAProxy{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Path != "<BADREQ>" || e.Status != 400 {
		t.Fatalf("got %q %d — a request the proxy could not read is still traffic", e.Path, e.Status)
	}
}

func TestHAProxy_DaemonLinesAreSkippedNotFailed(t *testing.T) {
	for _, line := range []string{
		`Aug 19 18:00:00 lb1 haproxy[2411]: Proxy fe_https started.`,
		`[ALERT] 231/180000 (2411) : Starting frontend fe_https: cannot bind socket`,
		``,
		`   `,
	} {
		if _, err := (HAProxy{}).Parse(line); !errors.Is(err, ErrSkip) {
			t.Errorf("Parse(%q) = %v, want ErrSkip: a daemon message is not a coverage hole", line, err)
		}
	}
}

func TestHAProxy_RequestLookingLineThatCannotBeReadIsMalformed(t *testing.T) {
	// A dated line with no timers: a tcplog record, or a truncated line. It is
	// a request edgemix could not read, and must be counted as one.
	line := `10.0.0.9:44100 [19/Aug/2026:18:06:39.001] fe be/srv 200 "GET / HTTP/1.1"`
	if _, err := (HAProxy{}).Parse(line); !errors.Is(err, ErrMalformed) {
		t.Errorf("err = %v, want ErrMalformed", err)
	}
}

func TestHAProxy_LocationIsStatedNotGuessed(t *testing.T) {
	// HAProxy writes local time with no offset. Whatever zone the caller names
	// is the zone the timestamp means, and the report says which.
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skipf("no tzdata: %v", err)
	}
	line := `10.0.0.9:1 [19/Aug/2026:18:06:39.001] fe be/s 0/0/0/1/1 200 1 - - ---- 1/1/0/0/0 0/0 "GET / HTTP/1.1"`
	e, err := HAProxy{Location: rome}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := e.Time.UTC().Format("15:04:05"), "16:06:39"; got != want {
		t.Errorf("UTC time = %s, want %s (18:06 Rome in August is 16:06 UTC)", got, want)
	}
}

func TestHAProxy_XFFCaptureOnlyCountsWhenPresent(t *testing.T) {
	// The case this flag exists for: a frontend that captures X-Forwarded-For
	// but writes a dash for it. Counting that as an identity would make an
	// unanswerable audience question look answerable.
	line := `- [19/Aug/2026:18:06:39.001] fe be/s 0/0/0/1/1 200 1 - - ---- 1/1/0/0/0 0/0 {www.example.test|-} "GET / HTTP/1.1"`
	e, err := HAProxy{XFFCapture: 2}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.ClientIdentity {
		t.Error("ClientIdentity = true for a captured dash")
	}
}

func TestLooksLikeHost(t *testing.T) {
	yes := []string{"www.example.test", "api.example.test:443", "10.0.0.1"}
	no := []string{"-", "", "Mozilla/5.0 (Macintosh)", "sessionid", "a@b.test", "GET /"}
	for _, s := range yes {
		if !looksLikeHost(s) {
			t.Errorf("looksLikeHost(%q) = false", s)
		}
	}
	for _, s := range no {
		if looksLikeHost(s) {
			t.Errorf("looksLikeHost(%q) = true — a wrong hostname ends up in a load test's allowlist", s)
		}
	}
}

func TestSplitFieldsKeepsQuotedAndBracketedTogether(t *testing.T) {
	got := splitFields(`a [19/Aug/2026:18:06:39] {h|u a} "GET /a b HTTP/1.1" z`)
	want := []string{"a", "[19/Aug/2026:18:06:39]", "{h|u a}", `"GET /a b HTTP/1.1"`, "z"}
	if len(got) != len(want) {
		t.Fatalf("got %d fields %q, want %d", len(got), got, len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}
}
