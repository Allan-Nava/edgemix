package logfmt

import (
	"errors"
	"testing"
	"time"
)

// Akamai DataStream 2 writes one JSON object per line, and writes every value
// as a string — including the numbers and the timestamp. A parser that decodes
// `statusCode` into an int fails on the real thing, and one that decodes it
// only as a string fails on the pipelines that re-type it. Both are tested.

const ds2Line = `{"version":"1","cliIP":"203.0.113.7","reqTimeSec":"1787162798.123",` +
	`"reqMethod":"GET","reqHost":"www.example.test","reqPath":"/news/latest","queryStr":"page=2",` +
	`"statusCode":"200","cacheStatus":"1","turnAroundTimeMSec":"45","transferTimeMSec":"12",` +
	`"totalBytes":"15321","reqPort":"443","proto":"https","UA":"Mozilla/5.0","edgeIP":"23.0.0.1"}`

func TestAkamai_DataStream2Line(t *testing.T) {
	e, err := Akamai{}.Parse(ds2Line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got, want := e.Time.Format(time.RFC3339Nano), "2026-08-19T18:06:38.123Z"; got != want {
		t.Errorf("Time = %s, want %s", got, want)
	}
	// reqTimeSec is an epoch: an absolute instant, so --tz has nothing to do
	// here and the report must not claim an assumed zone.
	if e.Time.Location() != time.UTC {
		t.Errorf("Time zone = %v, want UTC", e.Time.Location())
	}
	if e.Method != "GET" || e.Path != "/news/latest" || e.Query != "page=2" {
		t.Errorf("request = %q %q %q", e.Method, e.Path, e.Query)
	}
	if e.Host != "www.example.test" || e.Status != 200 || e.Bytes != 15321 {
		t.Errorf("host/status/bytes = %q %d %d", e.Host, e.Status, e.Bytes)
	}
	// turnAroundTimeMSec is the wait on the origin: the time from the edge
	// having the whole request to the origin's first byte. transferTimeMSec is
	// the viewer reading the body, which a slow phone inflates.
	if !e.HaveResponse || e.Response != 45*time.Millisecond {
		t.Errorf("Response = %v (have=%v), want turnAroundTimeMSec", e.Response, e.HaveResponse)
	}
	// DataStream 2 has no field for the whole exchange. Adding two of its
	// timers together would produce a number that looks like nginx's
	// $request_time and is not one, so there is no total at all.
	if e.HaveTotal {
		t.Errorf("Total = %v, want absent: DS2 does not log the whole exchange", e.Total)
	}
	if d, ok := e.Latency(); !ok || d != 45*time.Millisecond {
		t.Errorf("Latency() = %v %v, want the origin wait", d, ok)
	}
	if e.Cache != "HIT" {
		t.Errorf("Cache = %q, want HIT: cacheStatus 1 is a hit", e.Cache)
	}
	if !e.ClientIdentity {
		t.Error("cliIP is present")
	}
}

func TestAkamai_NumbersMayBeNumbers(t *testing.T) {
	// The same record after a pipeline that re-typed it: Firehose to S3 with a
	// transform, or a hand-written fixture. The values mean the same thing.
	line := `{"cliIP":"203.0.113.7","reqTimeSec":1787162798,"reqMethod":"GET","reqPath":"/a",` +
		`"statusCode":404,"cacheStatus":0,"turnAroundTimeMSec":9,"totalBytes":512}`
	e, err := Akamai{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Status != 404 || e.Bytes != 512 {
		t.Errorf("status/bytes = %d %d", e.Status, e.Bytes)
	}
	if !e.HaveResponse || e.Response != 9*time.Millisecond {
		t.Errorf("Response = %v", e.Response)
	}
	if e.Cache != "MISS" {
		t.Errorf("Cache = %q, want MISS: cacheStatus 0 is a miss", e.Cache)
	}
	if got, want := e.Time.Format(time.RFC3339), "2026-08-19T18:06:38Z"; got != want {
		t.Errorf("Time = %s, want %s", got, want)
	}
}

func TestAkamai_NoCacheFieldIsUnknownNotMiss(t *testing.T) {
	line := `{"reqTimeSec":"1787162798","reqMethod":"GET","reqPath":"/a","statusCode":"200"}`
	e, err := Akamai{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Cache != "" {
		t.Errorf("Cache = %q, want empty: a stream configured without cacheStatus knows nothing about caching, and a MISS here would invent a ratio", e.Cache)
	}
}

func TestAkamai_QueryInsideThePath(t *testing.T) {
	// Some stream configurations put the query into reqPath instead of
	// queryStr. A path carrying `?x=1` would otherwise become a distinct path
	// per query, which inflates the path cardinality and fills a profile pool
	// with one-off URLs.
	line := `{"reqTimeSec":"1787162798","reqMethod":"GET","reqPath":"/search?q=lazio","statusCode":"200"}`
	e, err := Akamai{}.Parse(line)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if e.Path != "/search" || e.Query != "q=lazio" {
		t.Errorf("path/query = %q %q", e.Path, e.Query)
	}
}

func TestAkamai_SkipsWhatIsNotARequest(t *testing.T) {
	for _, line := range []string{
		``,
		`   `,
		`not json at all`,
		// A Traefik access log line. The two dialects are both JSON, and a
		// parser that accepted anything with a timestamp in it would make the
		// detection vote a coin toss between them.
		`{"ClientHost":"203.0.113.7","RequestMethod":"GET","RequestPath":"/a","DownstreamStatus":200,"StartUTC":"2026-08-19T18:06:38Z"}`,
		// A DS2 object with no request in it — a stream health record.
		`{"version":"1","type":"heartbeat"}`,
	} {
		if _, err := (Akamai{}).Parse(line); !errors.Is(err, ErrSkip) {
			t.Errorf("Parse(%q) = %v, want ErrSkip", line, err)
		}
	}
}

func TestAkamai_MalformedLines(t *testing.T) {
	for name, line := range map[string]string{
		"truncated json": `{"reqPath":"/a","reqTimeSec":"178716`,
		"no time":        `{"reqMethod":"GET","reqPath":"/a","statusCode":"200"}`,
		"bad time":       `{"reqTimeSec":"yesterday","reqMethod":"GET","reqPath":"/a","statusCode":"200"}`,
		"bad status":     `{"reqTimeSec":"1787162798","reqMethod":"GET","reqPath":"/a","statusCode":"OK"}`,
	} {
		if _, err := (Akamai{}).Parse(line); !errors.Is(err, ErrMalformed) {
			t.Errorf("%s: Parse(%q) = %v, want ErrMalformed", name, line, err)
		}
	}
}

func TestAkamai_NameAndLatencyField(t *testing.T) {
	p := Akamai{}
	if p.Name() != "akamai" {
		t.Errorf("Name = %q", p.Name())
	}
	if f := p.LatencyField(); !contains(f, "turnAroundTimeMSec") {
		t.Errorf("LatencyField = %q, want it to name the field it measured", f)
	}
}
