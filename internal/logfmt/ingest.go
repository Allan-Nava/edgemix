package logfmt

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// Options configure the parsers that need it.
type Options struct {
	// Location interprets a date with no zone in it (HAProxy's accept date).
	Location *time.Location
	// XFFCapture is the 1-based slot of X-Forwarded-For among HAProxy's
	// captured request headers.
	XFFCapture int
}

// ByName returns the parser for a dialect id.
func ByName(name string, o Options) (Parser, error) {
	switch strings.ToLower(name) {
	case "haproxy":
		return HAProxy{Location: o.Location, XFFCapture: o.XFFCapture}, nil
	case "nginx":
		return Nginx{}, nil
	case "traefik":
		return Traefik{}, nil
	}
	return nil, fmt.Errorf("unknown dialect %q (known: haproxy, nginx, traefik)", name)
}

// Dialects lists the known dialect ids, for --help and error messages.
func Dialects() []string { return []string{"haproxy", "nginx", "traefik"} }

// Detect picks the dialect by trying every parser over a sample of lines and
// keeping the one that read the most.
//
// It is a vote rather than a signature match because the signatures overlap: an
// nginx log and an HAProxy log both carry a bracketed date and a quoted request,
// and a line that "looks like" one parses to nonsense under the other. Counting
// what actually parsed is the only test that cannot be fooled by a resemblance.
// A tie goes to no answer at all — the caller then has to pass --dialect, which
// is better than a coin toss over which numbers to print.
func Detect(sample []string, o Options) (Parser, error) {
	best, bestScore, tied := Parser(nil), 0, false
	for _, name := range Dialects() {
		p, err := ByName(name, o)
		if err != nil {
			continue
		}
		score := 0
		for _, line := range sample {
			if _, err := p.Parse(line); err == nil {
				score++
			}
		}
		switch {
		case score > bestScore:
			best, bestScore, tied = p, score, false
		case score == bestScore && score > 0:
			tied = true
		}
	}
	if bestScore == 0 {
		return nil, errors.New("no known dialect could read this log (try --dialect, or check it is an access log)")
	}
	if tied {
		return nil, errors.New("two dialects read this log equally well: pass --dialect to say which one it is")
	}
	return best, nil
}

// sampleSize is how many lines Detect votes over. Enough to get past a header
// or a burst of daemon messages, small enough to peek rather than buffer a file.
const sampleSize = 200

// DetectReader peeks at the start of br and detects the dialect without
// consuming it, so the same reader can then be scanned.
func DetectReader(br *bufio.Reader, o Options) (Parser, error) {
	buf, err := br.Peek(1 << 20)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return nil, err
	}
	lines := strings.Split(string(buf), "\n")
	// The last line of a peek is almost always cut in half; a truncated JSON
	// object or request line would parse as malformed and skew a close vote.
	if len(lines) > 1 {
		lines = lines[:len(lines)-1]
	}
	if len(lines) > sampleSize {
		lines = lines[:sampleSize]
	}
	return Detect(lines, o)
}

// Counts is what a scan saw. Skipped and Malformed are separate on purpose: a
// daemon message is not a coverage hole, and a request line edgemix could not
// read is one.
type Counts struct {
	Lines     int `json:"lines"`
	Parsed    int `json:"parsed"`
	Skipped   int `json:"skipped"`
	Malformed int `json:"malformed"`
}

// Add sums two counts, for a scan over several files.
func (c Counts) Add(o Counts) Counts {
	return Counts{c.Lines + o.Lines, c.Parsed + o.Parsed, c.Skipped + o.Skipped, c.Malformed + o.Malformed}
}

// MalformedShare is the fraction of request-looking lines that could not be
// read. It divides by lines that were *meant* to parse, not by every line in
// the file: a log with a thousand daemon messages and ten unreadable requests
// has a coverage hole, and dividing by the thousand would hide it.
func (c Counts) MalformedShare() float64 {
	den := c.Parsed + c.Malformed
	if den == 0 {
		return 0
	}
	return float64(c.Malformed) / float64(den)
}

// maxLine caps one log line. HAProxy truncates at its own limit; a longer line
// here means a binary file or a log with no newlines, and buffering it whole
// would be the tool's own out-of-memory.
const maxLine = 1 << 20

// Scan reads every line of br, parses it and calls fn for each event.
//
// fn is called in file order, single-threaded: the analysis is a fold over the
// stream, so parallelising the parse would only add a lock and lose the order
// the per-second series needs.
func Scan(br *bufio.Reader, p Parser, fn func(Event)) (Counts, error) {
	sc := bufio.NewScanner(br)
	sc.Buffer(make([]byte, 0, 64*1024), maxLine)
	var c Counts
	for sc.Scan() {
		c.Lines++
		e, err := p.Parse(sc.Text())
		switch {
		case err == nil:
			c.Parsed++
			fn(e)
		case errors.Is(err, ErrSkip):
			c.Skipped++
		default:
			c.Malformed++
		}
	}
	if err := sc.Err(); err != nil {
		return c, err
	}
	return c, nil
}

// Open returns a reader for path, transparently decompressing gzip and reading
// "-" as standard input.
//
// The gzip test is the magic number, not the file extension: rotated logs are
// renamed by hand often enough (`haproxy.log.1` that is gzipped, `edge.gz` that
// is not) that trusting the name means either a crash or a file read as binary.
func Open(path string) (io.Closer, *bufio.Reader, error) {
	var rc io.ReadCloser = io.NopCloser(os.Stdin)
	if path != "-" {
		f, err := os.Open(path)
		if err != nil {
			return nil, nil, err
		}
		rc = f
	}
	br := bufio.NewReaderSize(rc, 1<<20)
	if magic, err := br.Peek(2); err == nil && magic[0] == 0x1f && magic[1] == 0x8b {
		zr, err := gzip.NewReader(br)
		if err != nil {
			rc.Close()
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
		return multiCloser{zr, rc}, bufio.NewReaderSize(zr, 1<<20), nil
	}
	return rc, br, nil
}

type multiCloser []io.Closer

func (m multiCloser) Close() error {
	var first error
	for _, c := range m {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}
