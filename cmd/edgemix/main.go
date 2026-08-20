// Command edgemix reads a production access log and says what the traffic was
// made of, when it peaked, and where it waited — then writes a load-test profile
// from the same measurement.
//
// Exit codes: 0 when the analysis ran (findings are output, not failure), 1 when
// --exit-on is set and a finding reached that level, 2 when edgemix could not
// run at all.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Allan-Nava/edgemix/internal/analyze"
	"github.com/Allan-Nava/edgemix/internal/classify"
	"github.com/Allan-Nava/edgemix/internal/finding"
	"github.com/Allan-Nava/edgemix/internal/logfmt"
	"github.com/Allan-Nava/edgemix/internal/output"
	"github.com/Allan-Nava/edgemix/internal/profile"
)

// version is stamped at build time (-ldflags "-X main.version=…").
var version = "dev"

const (
	exitOK    = 0
	exitFound = 1
	exitUsage = 2
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return exitUsage
	}
	switch args[0] {
	case "analyze", "check":
		return cmdAnalyze(args[1:], stdout, stderr)
	case "profile":
		return cmdProfile(args[1:], stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "edgemix %s\n", version)
		return exitOK
	case "help", "-h", "--help":
		usage(stdout)
		return exitOK
	}
	fmt.Fprintf(stderr, "edgemix: unknown command %q\n\n", args[0])
	usage(stderr)
	return exitUsage
}

func usage(w io.Writer) {
	fmt.Fprint(w, `edgemix — what your edge log says about capacity, and a load-test profile from it

  edgemix analyze <log...>    read the log: the mix, the peak second, the waiting
  edgemix profile <log...>    write a crowdsim profile from the same measurement
  edgemix version

A log may be a file, a gzipped file, or "-" for standard input. Several files are
read as one window, in the order given.

  edgemix analyze /var/log/haproxy-edge.log
  edgemix analyze --since '2026-08-19 18:00:00' --until '2026-08-19 19:00:00' edge.log.gz
  edgemix analyze --format md edge.log > docs/report.md
  edgemix profile --base-url https://www.example.test --name example edge.log -o profile.json

Run a command with --help for its flags.
`)
}

// common flags shared by both commands: how to read the log.
type commonFlags struct {
	dialect     string
	tz          string
	xffCapture  int
	classesFile string
	since       string
	until       string
	tails       string
	readTimeout time.Duration
	top         int
	maxPaths    int
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.dialect, "dialect", "auto", "log dialect: auto, "+strings.Join(logfmt.Dialects(), ", "))
	fs.StringVar(&c.tz, "tz", "UTC", "timezone of a log whose dates carry no offset (HAProxy): an IANA name, or Local")
	fs.IntVar(&c.xffCapture, "xff-capture", 0, "1-based slot of X-Forwarded-For among HAProxy's captured request headers")
	fs.StringVar(&c.classesFile, "classes", "", "JSON file with the request-class rules (default: built-in set)")
	fs.StringVar(&c.since, "since", "", "ignore requests before this time (RFC3339, or 'YYYY-MM-DD HH:MM:SS' in --tz)")
	fs.StringVar(&c.until, "until", "", "ignore requests at or after this time")
	fs.StringVar(&c.tails, "tails", "1s,3s,7s", "wait thresholds to report the share of traffic beyond")
	fs.DurationVar(&c.readTimeout, "read-timeout", 7*time.Second, "the reverse proxy's read timeout: traffic slower than this became a 504")
	fs.IntVar(&c.top, "top", 10, "how many paths to list per class")
	fs.IntVar(&c.maxPaths, "max-paths", 200000, "cap on distinct paths kept per class (what is dropped is reported)")
}

// scanned is the outcome of reading every named log into one analyzer.
type scanned struct {
	report analyze.Report
}

// readLogs builds the analyzer options from the flags, reads every file and
// returns the report. poolSize > 0 also collects replayable pools.
func readLogs(c *commonFlags, paths []string, poolSize int, minOK float64, stderr io.Writer) (*scanned, error) {
	loc := time.UTC
	if c.tz != "" && c.tz != "UTC" {
		l, err := time.LoadLocation(c.tz)
		if err != nil {
			return nil, fmt.Errorf("--tz %q: %w", c.tz, err)
		}
		loc = l
	}
	opts := logfmt.Options{Location: loc, XFFCapture: c.xffCapture}

	classes := classify.Default()
	if c.classesFile != "" {
		data, err := os.ReadFile(c.classesFile)
		if err != nil {
			return nil, err
		}
		classes, err = classify.Load(data)
		if err != nil {
			return nil, fmt.Errorf("--classes %s: %w", c.classesFile, err)
		}
	}

	var tails []time.Duration
	for _, s := range strings.Split(c.tails, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, fmt.Errorf("--tails %q: %w", s, err)
		}
		tails = append(tails, d)
	}
	// The read timeout is always among the tails: it is the threshold the
	// report's only BAD is made of, and leaving it out because it was absent
	// from --tails would silently remove that finding.
	have := false
	for _, t := range tails {
		if t == c.readTimeout {
			have = true
		}
	}
	if !have {
		tails = append(tails, c.readTimeout)
	}

	since, err := parseTime(c.since, loc)
	if err != nil {
		return nil, fmt.Errorf("--since: %w", err)
	}
	until, err := parseTime(c.until, loc)
	if err != nil {
		return nil, fmt.Errorf("--until: %w", err)
	}
	if !since.IsZero() && !until.IsZero() && !until.After(since) {
		return nil, errors.New("--until must be after --since")
	}

	a := analyze.New(analyze.Options{
		Classes:        classes,
		Tails:          tails,
		ReadTimeout:    c.readTimeout,
		TopPaths:       c.top,
		MaxPaths:       c.maxPaths,
		Since:          since,
		Until:          until,
		Zone:           loc.String(),
		Version:        "edgemix " + version,
		PoolSize:       poolSize,
		PoolMinOKShare: minOK,
	})

	var parser logfmt.Parser
	if c.dialect != "auto" {
		parser, err = logfmt.ByName(c.dialect, opts)
		if err != nil {
			return nil, err
		}
	}

	var counts logfmt.Counts
	for _, path := range paths {
		closer, br, err := logfmt.Open(path)
		if err != nil {
			return nil, err
		}
		if parser == nil {
			parser, err = logfmt.DetectReader(br, opts)
			if err != nil {
				closer.Close()
				return nil, fmt.Errorf("%s: %w", path, err)
			}
			fmt.Fprintf(stderr, "edgemix: reading %s as %s\n", path, parser.Name())
		}
		c, err := logfmt.Scan(br, parser, a.Add)
		closer.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		counts = counts.Add(c)
	}
	if parser == nil {
		return nil, errors.New("no log to read")
	}
	if n := a.Filtered(); n > 0 {
		fmt.Fprintf(stderr, "edgemix: %d requests fell outside --since/--until\n", n)
	}

	return &scanned{report: a.Report(sourceLabel(paths), parser, counts)}, nil
}

// sourceLabel names the log in the report. Several files are named by the first
// and a count rather than by a joined list, which would put a whole directory
// listing into a report header.
func sourceLabel(paths []string) string {
	switch len(paths) {
	case 0:
		return "(none)"
	case 1:
		if paths[0] == "-" {
			return "(standard input)"
		}
		return paths[0]
	}
	return fmt.Sprintf("%s and %d more", filepath.Base(paths[0]), len(paths)-1)
}

// parseTime reads a window bound. Both an RFC3339 instant and a bare local
// timestamp are accepted, because a log's own dates look like the second and an
// operator copying from a dashboard has the first.
func parseTime(s string, loc *time.Location) (time.Time, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot read %q as a time (try 2006-01-02 15:04:05)", s)
}

// permute moves flags in front of the file arguments before parsing.
//
// Go's flag package stops looking for flags at the first operand, so
// `edgemix profile edge.log -o out.json` would silently treat `-o` and
// `out.json` as two more log files — and "no such file: -o" is a confusing way
// to learn that. Reordering is what every other command-line tool does, and the
// alternative is a footgun in the first example anyone copies.
//
// A `--` ends flag processing, as usual, and `-` stays an operand: it is the
// name for standard input, not a flag.
func permute(fs *flag.FlagSet, args []string) []string {
	var flags, operands []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			operands = append(operands, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			operands = append(operands, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if strings.ContainsRune(name, '=') {
			continue // --flag=value carries its own value
		}
		f := fs.Lookup(name)
		if f == nil {
			continue // unknown flag: let the flag package say so
		}
		if b, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && b.IsBoolFlag() {
			continue // a boolean flag takes no separate value
		}
		if i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, operands...)
}

func cmdAnalyze(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("edgemix analyze", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c commonFlags
	c.register(fs)
	format := fs.String("format", "text", "output format: "+strings.Join(output.Formats(), ", "))
	out := fs.String("out", "", "write to this file instead of standard output")
	fs.StringVar(out, "o", "", "shorthand for --out")
	exitOn := fs.String("exit-on", "", "exit 1 when a finding reaches this level: warn, bad, error")
	if err := fs.Parse(permute(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "edgemix analyze: name at least one log file, or - for standard input")
		return exitUsage
	}

	sc, err := readLogs(&c, fs.Args(), 0, 0, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "edgemix: %v\n", err)
		return exitUsage
	}

	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "edgemix: %v\n", err)
			return exitUsage
		}
		defer f.Close()
		w = f
	}
	bw := bufio.NewWriter(w)
	if err := output.Render(bw, sc.report, output.Format(*format), output.ColourFor(w)); err != nil {
		bw.Flush()
		fmt.Fprintf(stderr, "edgemix: %v\n", err)
		return exitUsage
	}
	if err := bw.Flush(); err != nil {
		fmt.Fprintf(stderr, "edgemix: %v\n", err)
		return exitUsage
	}

	if *exitOn != "" {
		threshold, err := statusOf(*exitOn)
		if err != nil {
			fmt.Fprintf(stderr, "edgemix: %v\n", err)
			return exitUsage
		}
		if finding.AtLeast(finding.Worst(sc.report.Findings), threshold) {
			return exitFound
		}
	}
	return exitOK
}

func statusOf(s string) (finding.Status, error) {
	switch strings.ToLower(s) {
	case "warn":
		return finding.WARN, nil
	case "bad":
		return finding.BAD, nil
	case "error":
		return finding.ERROR, nil
	}
	return "", fmt.Errorf("--exit-on %q: use warn, bad or error", s)
}

func cmdProfile(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("edgemix profile", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c commonFlags
	c.register(fs)
	name := fs.String("name", "", "profile name (default: the log's basename)")
	desc := fs.String("description", "", "profile description (default: what was measured)")
	baseURL := fs.String("base-url", "", "required: where a run would send the load, e.g. https://www.example.test")
	target := fs.String("target", "edge", "name of the target the base URL becomes")
	hostHeader := fs.String("host-header", "", "Host header to send, for addressing a tier by IP")
	bypass := fs.String("bypass", "", "host=address, to skip a CDN while keeping SNI and Host correct")
	poolSize := fs.Int("pool-size", 40, "how many paths per class to put in the pool")
	minOK := fs.Float64("min-ok", 0.95, "minimum share of 2xx for a path to enter a pool")
	maxP95 := fs.Duration("max-p95", 5*time.Second, "abort a run when the brake class's p95 crosses this")
	maxFailed := fs.Float64("max-failed", 0.05, "abort a run when the failed rate crosses this")
	safePeak := fs.Int("safe-peak", 0, "safety ceiling in req/s (default: the peak second measured)")
	allowHosts := fs.String("allow-hosts", "", "comma-separated hostname globs a run may target (default: the hosts the log names)")
	out := fs.String("out", "", "write the profile here instead of standard output")
	fs.StringVar(out, "o", "", "shorthand for --out")
	if err := fs.Parse(permute(fs, args)); err != nil {
		return exitUsage
	}
	if fs.NArg() == 0 {
		fmt.Fprintln(stderr, "edgemix profile: name at least one log file, or - for standard input")
		return exitUsage
	}

	sc, err := readLogs(&c, fs.Args(), *poolSize, *minOK, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "edgemix: %v\n", err)
		return exitUsage
	}

	pname := *name
	if pname == "" && fs.Arg(0) != "-" {
		pname = strings.TrimSuffix(strings.TrimSuffix(filepath.Base(fs.Arg(0)), ".gz"), ".log")
	}
	var hosts []string
	for _, h := range strings.Split(*allowHosts, ",") {
		if h = strings.TrimSpace(h); h != "" {
			hosts = append(hosts, h)
		}
	}

	p, warns, err := profile.Build(sc.report, profile.Options{
		Name:          pname,
		Description:   *desc,
		BaseURL:       *baseURL,
		TargetName:    *target,
		HostHeader:    *hostHeader,
		Bypass:        *bypass,
		ReadTimeout:   c.readTimeout,
		MaxP95:        *maxP95,
		MaxFailedRate: *maxFailed,
		AllowHosts:    hosts,
		SafePeak:      *safePeak,
		Tool:          "edgemix " + version,
	})
	// Warnings are printed whether or not the build succeeded: the reason it
	// failed is usually the last one in the list.
	for _, w := range warns {
		fmt.Fprintf(stderr, "edgemix: warning: %s\n", w)
	}
	if err != nil {
		fmt.Fprintf(stderr, "edgemix: %v\n", err)
		return exitUsage
	}

	w := stdout
	if *out != "" {
		f, err := os.Create(*out)
		if err != nil {
			fmt.Fprintf(stderr, "edgemix: %v\n", err)
			return exitUsage
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(p); err != nil {
		fmt.Fprintf(stderr, "edgemix: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(stderr, "edgemix: %d classes, %d pools, safe peak %d req/s — validate it with `crowdsim validate`\n",
		len(p.Classes), len(p.Pools), p.Safety.SafePeakRPS)
	return exitOK
}
