package logfmt

import (
	"errors"
	"testing"
	"time"
)

func TestNginx_Combined(t *testing.T) {
	line := `203.0.113.7 - - [19/Aug/2026:18:06:38 +0200] "GET /page/1?x=2 HTTP/1.1" 200 15321 "https://www.example.test/" "Mozilla/5.0"`
	e, err := Nginx{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := e.Time.UTC().Format(time.RFC3339), "2026-08-19T16:06:38Z"; got != want {
		t.Errorf("Time = %s, want %s (the offset in the line is authoritative)", got, want)
	}
	if e.Method != "GET" || e.Path != "/page/1" || e.Query != "x=2" {
		t.Errorf("request = %q %q %q", e.Method, e.Path, e.Query)
	}
	if e.Status != 200 || e.Bytes != 15321 {
		t.Errorf("status/bytes = %d/%d", e.Status, e.Bytes)
	}
	if e.HaveTotal || e.HaveResponse {
		t.Error("combined carries no timing: it must not report one")
	}
	if !e.ClientIdentity {
		t.Error("ClientIdentity = false, want true")
	}
	if e.Cache != "" {
		t.Errorf("Cache = %q, want empty: a log with no cache field has an unknown ratio, not a zero one", e.Cache)
	}
}

func TestNginx_CacheStatusAndRequestTime(t *testing.T) {
	line := `203.0.113.7 - - [19/Aug/2026:18:06:38 +0000] "GET /a.js HTTP/1.1" 200 100 "-" "UA" HIT 0.042`
	e, err := Nginx{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Cache != "HIT" {
		t.Errorf("Cache = %q", e.Cache)
	}
	if !e.HaveTotal || e.Total != 42*time.Millisecond {
		t.Errorf("Total = %v (have %v), want 42ms", e.Total, e.HaveTotal)
	}
	if d, ok := e.Latency(); !ok || d != 42*time.Millisecond {
		t.Errorf("Latency() = %v %v", d, ok)
	}
}

func TestNginx_QuotedCacheStatus(t *testing.T) {
	line := `203.0.113.7 - - [19/Aug/2026:18:06:38 +0000] "GET /a.js HTTP/1.1" 200 100 "-" "UA" "BYPASS"`
	e, err := Nginx{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Cache != "BYPASS" {
		t.Errorf("Cache = %q, want BYPASS — a cache that is never asked is a different failure from one that misses", e.Cache)
	}
}

func TestNginx_AnonymisedClient(t *testing.T) {
	line := `- - - [19/Aug/2026:18:06:38 +0000] "GET / HTTP/1.1" 200 100 "-" "UA"`
	e, err := Nginx{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.ClientIdentity {
		t.Error("ClientIdentity = true for a dash: a log with the address stripped cannot count people")
	}
}

func TestNginx_NotAnAccessLine(t *testing.T) {
	for _, line := range []string{"", "# comment", "2026/08/19 18:06:38 [error] 1#1: *3 upstream timed out"} {
		if _, err := (Nginx{}).Parse(line); err == nil {
			t.Errorf("Parse(%q) succeeded", line)
		} else if !errors.Is(err, ErrSkip) && !errors.Is(err, ErrMalformed) {
			t.Errorf("Parse(%q) = %v", line, err)
		}
	}
}
