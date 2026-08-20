package logfmt

import (
	"errors"
	"testing"
	"time"
)

// A CloudFront standard access log names its own columns in a `#Fields:` line,
// and the order of that line is not fixed: it has grown over the years, a
// distribution can be configured with fewer fields, and two files from
// different years concatenated into one window carry two different headers.
// Every test here is about reading by name.

const cfHeader = "#Fields: date time x-edge-location sc-bytes c-ip cs-method cs(Host) cs-uri-stem sc-status cs(Referer) cs(User-Agent) cs-uri-query cs(Cookie) x-edge-result-type x-edge-request-id x-host-header cs-protocol cs-bytes time-taken x-forwarded-for ssl-protocol ssl-cipher x-edge-response-result-type cs-protocol-version fle-status fle-encrypted-fields c-port time-to-first-byte x-edge-detailed-result-type sc-content-type sc-content-len sc-range-start sc-range-end"

// tabs joins fields the way CloudFront writes them.
func tabs(fields ...string) string {
	out := ""
	for i, f := range fields {
		if i > 0 {
			out += "\t"
		}
		out += f
	}
	return out
}

func cfLine() string {
	return tabs(
		"2026-08-19", "18:06:38", "MXP64-C1", "15321", "203.0.113.7", "GET",
		"d111111abcdef8.cloudfront.net", "/news/latest", "200", "-", "Mozilla/5.0",
		"page=2", "-", "Hit", "abc123==", "www.example.test", "https", "310",
		"0.045", "-", "TLSv1.3", "TLS_AES_128_GCM_SHA256", "Hit", "HTTP/2.0",
		"-", "-", "52413", "0.019", "Hit", "text/html", "15321", "-", "-",
	)
}

func TestCloudFront_StandardLine(t *testing.T) {
	p := &CloudFront{}
	if _, err := p.Parse(cfHeader); !errors.Is(err, ErrSkip) {
		t.Fatalf("the #Fields header must be skipped, not counted: %v", err)
	}
	e, err := p.Parse(cfLine())
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got, want := e.Time.Format(time.RFC3339), "2026-08-19T18:06:38Z"; got != want {
		t.Errorf("Time = %s, want %s", got, want)
	}
	// A CloudFront log is UTC by definition: there is nothing to interpret, so
	// --tz must not touch it. A local reading would move every second of the
	// window and quietly mis-place the peak.
	if e.Time.Location() != time.UTC {
		t.Errorf("Time zone = %v, want UTC", e.Time.Location())
	}
	if e.Method != "GET" || e.Path != "/news/latest" || e.Query != "page=2" {
		t.Errorf("request = %q %q %q", e.Method, e.Path, e.Query)
	}
	// x-host-header is the host the viewer asked for. cs(Host) is the
	// distribution domain, and reading that one would report every request as
	// going to d111111abcdef8.cloudfront.net — which then lands in an emitted
	// profile's allow_hosts and aims a load test at the CDN.
	if e.Host != "www.example.test" {
		t.Errorf("Host = %q, want the x-host-header value", e.Host)
	}
	if e.Status != 200 || e.Bytes != 15321 {
		t.Errorf("status/bytes = %d %d", e.Status, e.Bytes)
	}
	if e.Cache != "HIT" {
		t.Errorf("Cache = %q, want HIT from x-edge-result-type", e.Cache)
	}
	if !e.HaveTotal || e.Total != 45*time.Millisecond {
		t.Errorf("Total = %v (have=%v), want time-taken 45ms", e.Total, e.HaveTotal)
	}
	if !e.HaveResponse || e.Response != 19*time.Millisecond {
		t.Errorf("Response = %v (have=%v), want time-to-first-byte 19ms", e.Response, e.HaveResponse)
	}
	// time-taken includes the viewer reading the body; time-to-first-byte is
	// the wait. They are not the same measurement, and the wait is the one a
	// queue question reads.
	if d, ok := e.Latency(); !ok || d != 19*time.Millisecond {
		t.Errorf("Latency() = %v %v, want time-to-first-byte", d, ok)
	}
	if !e.ClientIdentity {
		t.Error("c-ip is present, so this log can be asked audience questions")
	}
	if e.Frontend != "MXP64-C1" {
		t.Errorf("Frontend = %q, want the edge location", e.Frontend)
	}
}

func TestCloudFront_HeaderAndBlankLinesAreSkipped(t *testing.T) {
	p := &CloudFront{}
	for _, line := range []string{
		"#Version: 1.0",
		cfHeader,
		"",
		"   ",
	} {
		if _, err := p.Parse(line); !errors.Is(err, ErrSkip) {
			t.Errorf("Parse(%q) = %v, want ErrSkip", line, err)
		}
	}
}

func TestCloudFront_DataBeforeHeaderIsMalformed(t *testing.T) {
	// A file whose header was stripped cannot be read: the columns are named
	// nowhere else, and assuming the current standard order would produce a
	// plausible wrong report on any distribution configured differently. This
	// is the one place where refusing is the feature.
	if _, err := (&CloudFront{}).Parse(cfLine()); !errors.Is(err, ErrMalformed) {
		t.Error("a data line with no #Fields header seen must count as unreadable, never be guessed at")
	}
}

func TestCloudFront_ColumnsAreReadByName(t *testing.T) {
	// A short, reordered header — a distribution that logs less than the
	// default, which is common. A positional reader passes every other test in
	// this file and fails this one.
	p := &CloudFront{}
	if _, err := p.Parse("#Fields: date time cs-method cs-uri-stem sc-status x-edge-result-type time-taken x-host-header"); !errors.Is(err, ErrSkip) {
		t.Fatalf("header: %v", err)
	}
	e, err := p.Parse(tabs("2026-08-19", "18:06:38", "POST", "/api/live", "503", "Error", "2.500", "api.example.test"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Method != "POST" || e.Path != "/api/live" || e.Status != 503 {
		t.Errorf("request = %q %q %d", e.Method, e.Path, e.Status)
	}
	if e.Host != "api.example.test" || e.Cache != "ERROR" {
		t.Errorf("host/cache = %q %q", e.Host, e.Cache)
	}
	if !e.HaveTotal || e.Total != 2500*time.Millisecond {
		t.Errorf("Total = %v", e.Total)
	}
	// The header did not carry time-to-first-byte, so there is no wait to
	// report. Absent, not zero: a zero here would be the fastest request in
	// the log.
	if e.HaveResponse {
		t.Errorf("Response = %v, want absent when the column is not in the header", e.Response)
	}
	if d, ok := e.Latency(); !ok || d != 2500*time.Millisecond {
		t.Errorf("Latency() = %v %v, want the total when there is no first-byte column", d, ok)
	}
}

func TestCloudFront_SecondHeaderRemaps(t *testing.T) {
	// Several files are read as one window, and each CloudFront file carries
	// its own header. A parser that keeps the first mapping reads the second
	// file through the wrong columns — and reports numbers rather than errors.
	p := &CloudFront{}
	if _, err := p.Parse("#Fields: date time cs-method cs-uri-stem sc-status"); !errors.Is(err, ErrSkip) {
		t.Fatalf("first header: %v", err)
	}
	if _, err := p.Parse(tabs("2026-08-19", "18:06:38", "GET", "/a", "200")); err != nil {
		t.Fatalf("first file: %v", err)
	}
	if _, err := p.Parse("#Fields: date time sc-status cs-uri-stem cs-method"); !errors.Is(err, ErrSkip) {
		t.Fatalf("second header: %v", err)
	}
	e, err := p.Parse(tabs("2026-08-19", "18:07:38", "404", "/b", "HEAD"))
	if err != nil {
		t.Fatalf("second file: %v", err)
	}
	if e.Method != "HEAD" || e.Path != "/b" || e.Status != 404 {
		t.Errorf("the second header was not applied: %q %q %d", e.Method, e.Path, e.Status)
	}
}

func TestCloudFront_DashIsAbsentNotZero(t *testing.T) {
	p := &CloudFront{}
	if _, err := p.Parse(cfHeader); !errors.Is(err, ErrSkip) {
		t.Fatal("header")
	}
	line := tabs(
		"2026-08-19", "18:06:38", "MXP64-C1", "-", "203.0.113.7", "GET",
		"d111111abcdef8.cloudfront.net", "/news/latest", "200", "-", "Mozilla/5.0",
		"-", "-", "Miss", "abc123==", "-", "https", "310",
		"-", "-", "TLSv1.3", "TLS_AES_128_GCM_SHA256", "Miss", "HTTP/2.0",
		"-", "-", "52413", "-", "Miss", "text/html", "-", "-", "-",
	)
	e, err := p.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.HaveTotal || e.HaveResponse {
		t.Errorf("a dash timing must be absent, not zero: total=%v resp=%v", e.HaveTotal, e.HaveResponse)
	}
	if e.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0 for a dash (unknown size, and nothing reads it as a rate)", e.Bytes)
	}
	if e.Query != "" {
		t.Errorf("Query = %q, want empty: a dash means there was no query string, not a query of %q", e.Query, "-")
	}
	// x-host-header was a dash, so the only host the line names is the
	// distribution domain. It is the host the viewer asked for in that case.
	if e.Host != "d111111abcdef8.cloudfront.net" {
		t.Errorf("Host = %q, want the cs(Host) fallback", e.Host)
	}
	if e.Cache != "MISS" {
		t.Errorf("Cache = %q", e.Cache)
	}
}

func TestCloudFront_ViewerDisconnectIsTraffic(t *testing.T) {
	// sc-status 000 is CloudFront saying the viewer closed the connection
	// before an answer. It is a request that happened, so it is counted; a
	// parser that treats it as malformed hides a real load.
	p := &CloudFront{}
	if _, err := p.Parse("#Fields: date time cs-method cs-uri-stem sc-status"); !errors.Is(err, ErrSkip) {
		t.Fatal("header")
	}
	e, err := p.Parse(tabs("2026-08-19", "18:06:38", "GET", "/video/live.m3u8", "000"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Status != 0 || e.StatusClass() != "other" {
		t.Errorf("status = %d (%s), want 0 counted as other", e.Status, e.StatusClass())
	}
}

func TestCloudFront_MalformedLines(t *testing.T) {
	p := &CloudFront{}
	if _, err := p.Parse("#Fields: date time cs-method cs-uri-stem sc-status"); !errors.Is(err, ErrSkip) {
		t.Fatal("header")
	}
	for name, line := range map[string]string{
		"short row":     tabs("2026-08-19", "18:06:38", "GET"),
		"bad status":    tabs("2026-08-19", "18:06:38", "GET", "/a", "OK"),
		"bad date":      tabs("19/Aug/2026", "18:06:38", "GET", "/a", "200"),
		"bad time":      tabs("2026-08-19", "six pm", "GET", "/a", "200"),
		"no uri":        tabs("2026-08-19", "18:06:38", "GET", "-", "200"),
		"not tabbed":    "2026-08-19 18:06:38 GET /a 200",
		"empty columns": tabs("2026-08-19", "18:06:38", "", "", ""),
	} {
		if _, err := p.Parse(line); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: Parse(%q) = %v, want ErrMalformed", name, line, err)
		}
	}
}

func TestCloudFront_NameAndLatencyField(t *testing.T) {
	p := &CloudFront{}
	if p.Name() != "cloudfront" {
		t.Errorf("Name = %q", p.Name())
	}
	// The field name travels with every number this dialect produces, and it
	// has to say that a hit is measured too: on a cache hit the wait is the
	// edge answering, not the origin. Two reports are never comparable across
	// dialects, and this is the sentence that stops it.
	if f := p.LatencyField(); !contains(f, "time-to-first-byte") || !contains(f, "edge") {
		t.Errorf("LatencyField = %q, want it to name the measurement and the tier", f)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
