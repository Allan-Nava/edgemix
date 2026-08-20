package logfmt

import (
	"errors"
	"testing"
	"time"
)

func TestTraefik_JSON(t *testing.T) {
	line := `{"ClientHost":"203.0.113.7","RequestHost":"www.example.test","RequestMethod":"GET","RequestPath":"/page/1?x=2","DownstreamStatus":200,"DownstreamContentSize":15321,"Duration":905000000,"OriginDuration":845000000,"RouterName":"web@nomad","ServiceName":"web-svc@nomad","StartUTC":"2026-08-19T18:06:38.123Z"}`
	e, err := Traefik{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Path != "/page/1" || e.Query != "x=2" {
		t.Errorf("path/query = %q %q", e.Path, e.Query)
	}
	if e.Host != "www.example.test" || e.Backend != "web-svc@nomad" || e.Frontend != "web@nomad" {
		t.Errorf("tiers = %q %q %q", e.Host, e.Backend, e.Frontend)
	}
	if !e.HaveResponse || e.Response != 845*time.Millisecond {
		t.Errorf("OriginDuration = %v", e.Response)
	}
	if !e.HaveTotal || e.Total != 905*time.Millisecond {
		t.Errorf("Duration = %v", e.Total)
	}
	if d, ok := e.Latency(); !ok || d != 845*time.Millisecond {
		t.Errorf("Latency() = %v %v — the service wait, not the whole exchange", d, ok)
	}
	if got, want := e.Time.Format(time.RFC3339Nano), "2026-08-19T18:06:38.123Z"; got != want {
		t.Errorf("Time = %s, want %s", got, want)
	}
}

func TestTraefik_ApplicationLinesAreSkipped(t *testing.T) {
	// Traefik's own logs share the file when accessLog is not separated. A JSON
	// object that is not a request must not be counted as an unreadable one.
	for _, line := range []string{
		`{"level":"info","msg":"Configuration loaded from flags.","time":"2026-08-19T18:00:00Z"}`,
		`time="2026-08-19T18:00:00Z" level=info msg="Starting provider"`,
		``,
	} {
		if _, err := (Traefik{}).Parse(line); !errors.Is(err, ErrSkip) {
			t.Errorf("Parse(%q) = %v, want ErrSkip", line, err)
		}
	}
}

func TestTraefik_BrokenJSONIsMalformed(t *testing.T) {
	if _, err := (Traefik{}).Parse(`{"RequestPath":"/a","Downstream`); !errors.Is(err, ErrMalformed) {
		t.Error("a truncated JSON line must count as unreadable")
	}
}

func TestTraefik_OriginStatusFallback(t *testing.T) {
	line := `{"RequestMethod":"GET","RequestPath":"/a","OriginStatus":503,"StartUTC":"2026-08-19T18:06:38Z"}`
	e, err := Traefik{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Status != 503 {
		t.Errorf("Status = %d, want the origin's when the downstream one is absent", e.Status)
	}
}
