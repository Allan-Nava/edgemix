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

func TestDetect(t *testing.T) {
	for _, tc := range []struct{ name, sample, want string }{
		{"haproxy", haproxySample, "haproxy"},
		{"nginx", nginxSample, "nginx"},
		{"traefik", traefikSample, "traefik"},
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
