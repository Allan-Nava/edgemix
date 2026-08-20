package logfmt

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const haproxySample = `10.0.0.9:1 [19/Aug/2026:18:06:38.001] fe be/s1 0/0/0/12/13 200 100 - - ---- 1/1/0/0/0 0/0 "GET / HTTP/1.1"
10.0.0.9:2 [19/Aug/2026:18:06:38.002] fe be/s1 0/0/0/15/16 200 100 - - ---- 1/1/0/0/0 0/0 "GET /a.js HTTP/1.1"`

const nginxSample = `10.0.0.9 - - [19/Aug/2026:18:06:38 +0000] "GET / HTTP/1.1" 200 100 "-" "UA" HIT 0.010
10.0.0.9 - - [19/Aug/2026:18:06:39 +0000] "GET /a.js HTTP/1.1" 200 100 "-" "UA" MISS 0.200`

const traefikSample = `{"RequestMethod":"GET","RequestPath":"/","DownstreamStatus":200,"Duration":10000000,"StartUTC":"2026-08-19T18:06:38Z"}
{"RequestMethod":"GET","RequestPath":"/a.js","DownstreamStatus":200,"Duration":11000000,"StartUTC":"2026-08-19T18:06:39Z"}`

// A CloudFront file always opens with its two header lines, which is also
// what makes it recognisable: nothing else in the vote reads a #Fields line.
const cloudfrontSample = `#Version: 1.0
#Fields: date time cs-method cs-uri-stem sc-status x-edge-result-type time-taken
2026-08-19	18:06:38	GET	/	200	Hit	0.010
2026-08-19	18:06:39	GET	/a.js	200	Miss	0.200`

// Akamai DataStream 2 is JSON, like Traefik. The two must not tie: a tie
// refuses, and a refusal on a log that only one parser can actually read would
// be a worse answer than either.
const akamaiSample = `{"reqTimeSec":"1787162798","reqMethod":"GET","reqPath":"/","statusCode":"200","cacheStatus":"1","turnAroundTimeMSec":"10"}
{"reqTimeSec":"1787162799","reqMethod":"GET","reqPath":"/a.js","statusCode":"200","cacheStatus":"0","turnAroundTimeMSec":"200"}`

func TestDetect(t *testing.T) {
	for _, tc := range []struct{ name, sample, want string }{
		{"haproxy", haproxySample, "haproxy"},
		{"nginx", nginxSample, "nginx"},
		{"traefik", traefikSample, "traefik"},
		{"cloudfront", cloudfrontSample, "cloudfront"},
		{"akamai", akamaiSample, "akamai"},
	} {
		p, err := Detect(strings.Split(tc.sample, "\n"), Options{})
		if err != nil {
			t.Errorf("%s: Detect: %v", tc.name, err)
			continue
		}
		if p.Name() != tc.want {
			t.Errorf("%s: detected %s", tc.name, p.Name())
		}
	}
}

func TestDetect_RefusesRatherThanGuesses(t *testing.T) {
	// Detection is a vote over what parsed. Nothing parsed means no answer,
	// which is better than printing numbers from the wrong reader.
	if _, err := Detect([]string{"hello", "world", "{}"}, Options{}); err == nil {
		t.Error("Detect succeeded on a file that is not an access log")
	}
}

func TestDetectReaderDoesNotConsume(t *testing.T) {
	br := bufio.NewReader(strings.NewReader(haproxySample))
	p, err := DetectReader(br, Options{})
	if err != nil {
		t.Fatalf("DetectReader: %v", err)
	}
	c, err := Scan(br, p, func(Event) {})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.Parsed != 2 {
		t.Errorf("Parsed = %d, want 2 — detection must peek, not consume", c.Parsed)
	}
}

func TestScanCountsSkippedApartFromMalformed(t *testing.T) {
	in := haproxySample + "\n" +
		"Aug 19 18:00:00 lb1 haproxy[1]: Proxy fe started.\n" +
		`10.0.0.9:3 [19/Aug/2026:18:06:40.003] fe be/s1 200 "GET / HTTP/1.1"` + "\n"
	c, err := Scan(bufio.NewReader(strings.NewReader(in)), HAProxy{}, func(Event) {})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.Parsed != 2 || c.Skipped != 1 || c.Malformed != 1 {
		t.Fatalf("counts = %+v, want 2 parsed, 1 skipped, 1 malformed", c)
	}
	if got, want := c.MalformedShare(), 1.0/3.0; got != want {
		t.Errorf("MalformedShare = %v, want %v — the divisor is request lines, not every line", got, want)
	}
}

func TestOpen_GzipByMagicNotByName(t *testing.T) {
	dir := t.TempDir()
	// A gzipped file whose name says otherwise: rotated logs get renamed, and
	// trusting the extension means reading compressed bytes as text.
	path := filepath.Join(dir, "edge.log")
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(haproxySample))
	zw.Close()
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	closer, br, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer closer.Close()
	c, err := Scan(br, HAProxy{}, func(Event) {})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if c.Parsed != 2 {
		t.Errorf("Parsed = %d, want 2", c.Parsed)
	}
}

func TestByNameUnknown(t *testing.T) {
	if _, err := ByName("apache", Options{}); err == nil {
		t.Error("ByName accepted a dialect that does not exist")
	}
}

// Two JSON dialects in the same vote is the case where a resemblance could win.
// Each parser has to refuse the other's lines outright — as a skip, not as a
// malformed line, since neither log is unreadable, it is simply not theirs.
func TestDetect_TwoJSONDialectsDoNotTie(t *testing.T) {
	for _, tc := range []struct{ name, sample, want string }{
		{"traefik is not akamai", traefikSample, "traefik"},
		{"akamai is not traefik", akamaiSample, "akamai"},
	} {
		p, err := Detect(strings.Split(tc.sample, "\n"), Options{})
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if p.Name() != tc.want {
			t.Errorf("%s: detected %s", tc.name, p.Name())
		}
	}
}

// Detection votes over a sample and the winner then reads the file from the
// start. A CloudFront parser that kept the mapping it built during the vote
// would be indistinguishable from one that rebuilt it — until the day the vote
// sample stops at the header. Reading twice has to be safe.
func TestDetect_CloudFrontParserIsReusable(t *testing.T) {
	lines := strings.Split(cloudfrontSample, "\n")
	p, err := Detect(lines, Options{})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	var parsed int
	for _, l := range lines {
		if _, err := p.Parse(l); err == nil {
			parsed++
		}
	}
	if parsed != 2 {
		t.Errorf("the detected parser read %d of 2 request lines on a second pass", parsed)
	}
}

func TestDialects_AreAllReachableByName(t *testing.T) {
	// --dialect takes this list verbatim, and every id in it has to resolve.
	for _, name := range Dialects() {
		p, err := ByName(name, Options{})
		if err != nil {
			t.Errorf("ByName(%q): %v", name, err)
			continue
		}
		if p.Name() != name {
			t.Errorf("ByName(%q).Name() = %q", name, p.Name())
		}
		if p.LatencyField() == "" {
			t.Errorf("%s: LatencyField is empty — every number needs the name of what it measured", name)
		}
	}
}
