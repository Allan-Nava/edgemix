// Package output renders a report.
//
// Three renderers, one rule: worst findings first, and never a number the
// analysis did not measure. Colour is a terminal affordance only — the markdown
// and JSON forms are what get pasted into an incident document and read by a
// script, and neither may drift from the other.
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/Allan-Nava/edgemix/internal/analyze"
	"github.com/Allan-Nava/edgemix/internal/finding"
)

// Format is a rendering.
type Format string

// The supported formats.
const (
	Text     Format = "text"
	Markdown Format = "md"
	JSON     Format = "json"
)

// Formats lists the format ids, for --help.
func Formats() []string { return []string{"text", "md", "json"} }

// Render writes r to w in the given format.
func Render(w io.Writer, r analyze.Report, f Format, colour bool) error {
	switch f {
	case Text:
		return renderText(w, r, colour)
	case Markdown:
		return renderMarkdown(w, r)
	case JSON:
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	return fmt.Errorf("unknown format %q (known: %s)", f, strings.Join(Formats(), ", "))
}

// ColourFor decides whether to colour: only on a terminal, and never when
// NO_COLOR is set.
func ColourFor(w io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	st, err := f.Stat()
	if err != nil {
		return false
	}
	return st.Mode()&os.ModeCharDevice != 0
}

const (
	reset  = "\033[0m"
	dim    = "\033[2m"
	green  = "\033[32m"
	yellow = "\033[33m"
	red    = "\033[31m"
	purple = "\033[35m"
)

func statusStyle(s finding.Status) (string, string) {
	switch s {
	case finding.OK:
		return green, "🟢 OK   "
	case finding.WARN:
		return yellow, "🟡 WARN "
	case finding.BAD:
		return red, "🔴 BAD  "
	case finding.ERROR:
		return purple, "🟣 ERROR"
	}
	return "", string(s)
}

func renderText(w io.Writer, r analyze.Report, colour bool) error {
	c := func(code, s string) string {
		if !colour || code == "" {
			return s
		}
		return code + s + reset
	}
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	p("%s  %s  %s → %s (%s)\n",
		c(dim, "log"), r.Source,
		fmtTime(r.Window.Start), fmtTime(r.Window.End), r.Zone)
	p("%s  %s, %d requests read, %d skipped, %d unreadable\n\n",
		c(dim, "fmt"), r.Dialect, r.Counts.Parsed, r.Counts.Skipped, r.Counts.Malformed)

	for _, f := range r.Findings {
		code, label := statusStyle(f.Status)
		p("%s %-9s %-14s %s\n", c(code, label), f.Check, truncate(f.Target, 14), f.Message)
		if f.Hint != "" {
			p("         %s %s\n", c(dim, "↳"), c(dim, f.Hint))
		}
	}

	p("\n%s\n", c(dim, "── arrival ─────────────────────────────────────────────────────────"))
	p("peak %d req/s at %s · p99 %.0f · p95 %.0f · p50 %.0f · mean %.1f over %ds",
		r.Rate.Peak, fmtTime(r.Rate.PeakAt), r.Rate.P99, r.Rate.P95, r.Rate.P50, r.Rate.Mean, r.WindowSeconds)
	if r.SilentSeconds > 0 {
		p(" (%d silent)", r.SilentSeconds)
	}
	p("\n")

	if len(r.Classes) > 0 {
		p("\n%s\n", c(dim, "── the mix ─────────────────────────────────────────────────────────"))
		p("%-12s %9s %7s %8s %9s %9s %7s\n", "class", "requests", "share", "peak/s", "p95", "p99", "paths")
		for _, cl := range r.Classes {
			p95, p99 := "-", "-"
			if cl.Latency != nil {
				p95 = fmt.Sprintf("%.0fms", cl.Latency.P95)
				p99 = fmt.Sprintf("%.0fms", cl.Latency.P99)
			}
			p("%-12s %9d %6.1f%% %8d %9s %9s %7d\n",
				cl.Name, cl.Count, cl.Share*100, cl.PeakPerSec, p95, p99, cl.DistinctPaths)
		}
	}

	if r.Latency != nil {
		p("\n%s\n", c(dim, "── waiting ─────────────────────────────────────────────────────────"))
		p("%s\n", r.LatencyField)
		for _, t := range r.Latency.Tails {
			p("  over %5.0fms  %8d  %6.2f%%\n", t.OverMs, t.Count, t.Share*100)
		}
	}

	if len(r.Statuses) > 0 {
		p("\n%s\n", c(dim, "── answers ─────────────────────────────────────────────────────────"))
		var parts []string
		for _, s := range r.Statuses {
			parts = append(parts, fmt.Sprintf("%s %d (%.1f%%)", s.Label, s.Count, s.Share*100))
		}
		p("%s\n", strings.Join(parts, " · "))
		for _, t := range r.Top5xx {
			p("  5xx %6d  %s\n", t.Worst5xx, t.Path)
		}
	}

	if r.Cache != nil {
		p("\n%s\n", c(dim, "── cache ───────────────────────────────────────────────────────────"))
		p("%s: %.1f%% served without the origin (%d judged)\n", r.Cache.Field, r.Cache.HitRatio*100, r.Cache.Measured)
		var parts []string
		for _, v := range r.Cache.Verdicts {
			parts = append(parts, fmt.Sprintf("%s %.1f%%", v.Label, v.Share*100))
		}
		p("  %s\n", strings.Join(parts, " · "))
	}

	if len(r.TopPaths) > 0 {
		p("\n%s\n", c(dim, "── busiest paths ───────────────────────────────────────────────────"))
		for _, t := range r.TopPaths {
			p("  %8d  %5.1f%%  %s\n", t.Count, t.Share*100, t.Path)
		}
	}

	sum := finding.Summarize(r.Findings)
	p("\n%d findings: %d OK, %d WARN, %d BAD, %d ERROR\n",
		len(r.Findings), sum[finding.OK], sum[finding.WARN], sum[finding.BAD], sum[finding.ERROR])
	return nil
}

// renderMarkdown writes the report as an operations document: the schema of
// what was measured first, then the findings, then the tables. It is meant to be
// committed next to an incident — which is why the measurement path is drawn
// rather than described. A report whose reader cannot tell which tier the log
// came from is a report that gets compared with the wrong one.
func renderMarkdown(w io.Writer, r analyze.Report) error {
	p := func(format string, a ...any) { fmt.Fprintf(w, format, a...) }

	p("# edgemix — %s\n\n", r.Source)
	p("- **window**: %s → %s (%s), %d seconds", fmtTime(r.Window.Start), fmtTime(r.Window.End), r.Zone, r.WindowSeconds)
	if r.SilentSeconds > 0 {
		p(", %d with no logged request", r.SilentSeconds)
	}
	p("\n")
	p("- **dialect**: `%s` — %d request lines read, %d skipped, %d unreadable\n", r.Dialect, r.Counts.Parsed, r.Counts.Skipped, r.Counts.Malformed)
	p("- **peak**: **%d req/s** at %s (mean %.1f, p95 %.0f)\n", r.Rate.Peak, fmtTime(r.Rate.PeakAt), r.Rate.Mean, r.Rate.P95)
	p("- **waiting**: %s", r.LatencyField)
	if r.Latency != nil {
		p(" — p50 %.0fms, p95 %.0fms, p99 %.0fms\n", r.Latency.P50, r.Latency.P95, r.Latency.P99)
	} else {
		p(" — not recorded in this log\n")
	}
	p("- **audience**: %s\n\n", audienceLine(r))

	p("```\n")
	p("%s\n", schema(r))
	p("```\n\n")

	p("## Findings\n\n")
	p("| | check | target | statement |\n|---|---|---|---|\n")
	for _, f := range r.Findings {
		p("| %s | %s | %s | %s |\n", f.Status, f.Check, f.Target, mdCell(f.Message))
	}
	p("\n")
	for _, f := range r.Findings {
		if f.Hint == "" {
			continue
		}
		p("- **%s / %s** — %s\n  - %s\n", f.Check, f.Target, mdCell(f.Message), mdCell(f.Hint))
	}

	p("\n## The mix\n\n")
	p("| class | requests | share | own peak | p95 | p99 | distinct paths |\n|---|---:|---:|---:|---:|---:|---:|\n")
	for _, cl := range r.Classes {
		p95, p99 := "–", "–"
		if cl.Latency != nil {
			p95 = fmt.Sprintf("%.0fms", cl.Latency.P95)
			p99 = fmt.Sprintf("%.0fms", cl.Latency.P99)
		}
		p("| `%s` | %d | %.1f%% | %d/s | %s | %s | %d |\n", cl.Name, cl.Count, cl.Share*100, cl.PeakPerSec, p95, p99, cl.DistinctPaths)
	}

	if r.Latency != nil {
		p("\n## Waiting, and what it costs\n\n")
		p("| slower than | requests | share |\n|---|---:|---:|\n")
		for _, t := range r.Latency.Tails {
			p("| %.0fms | %d | %.2f%% |\n", t.OverMs, t.Count, t.Share*100)
		}
		p("\nThe share past the reverse proxy's read timeout is the share of visitors who got a 504 rather than a slow page.\n")
	}

	p("\n## Answers\n\n")
	for _, s := range r.Statuses {
		p("- `%s` — %d (%.1f%%)\n", s.Label, s.Count, s.Share*100)
	}
	if len(r.Top5xx) > 0 {
		p("\n| path | 5xx | requests |\n|---|---:|---:|\n")
		for _, t := range r.Top5xx {
			p("| `%s` | %d | %d |\n", t.Path, t.Worst5xx, t.Count)
		}
	}

	if r.Cache != nil {
		p("\n## Cache\n\n`%s`: **%.1f%%** of the %d judged responses were served without the origin.\n\n", r.Cache.Field, r.Cache.HitRatio*100, r.Cache.Measured)
		for _, v := range r.Cache.Verdicts {
			p("- `%s` — %d (%.1f%%)\n", v.Label, v.Count, v.Share*100)
		}
	}

	if len(r.TopPaths) > 0 {
		p("\n## Busiest paths\n\n| path | requests | share | rendered |\n|---|---:|---:|---:|\n")
		for _, t := range r.TopPaths {
			p("| `%s` | %d | %.1f%% | %.0f%% |\n", t.Path, t.Count, t.Share*100, t.OKShare*100)
		}
	}
	p("\n---\n\n_Measured with %s. Every share above is of the lines this log actually recorded._\n", r.Tool)
	return nil
}

// schema draws where the measurement came from and what it can therefore say.
func schema(r analyze.Report) string {
	var b strings.Builder
	b.WriteString("  visitors\n     │\n")
	switch r.Dialect {
	case "haproxy":
		b.WriteString("  [ CDN ]  ─── not in this log ───\n     │\n  ┌──▼──────────────────┐\n  │  HAProxy (edge)     │  ◄── the log read here\n  └──┬──────────────────┘\n")
	case "nginx":
		b.WriteString("  [ CDN ] · [ edge LB ]  ─── not in this log ───\n     │\n  ┌──▼──────────────────┐\n  │  nginx (proxy)      │  ◄── the log read here\n  └──┬──────────────────┘\n")
	case "traefik":
		b.WriteString("  [ CDN ] · [ edge LB ]  ─── not in this log ───\n     │\n  ┌──▼──────────────────┐\n  │  Traefik (ingress)  │  ◄── the log read here\n  └──┬──────────────────┘\n")
	default:
		b.WriteString("  ┌─────────────────────┐\n  │  the logging tier   │  ◄── the log read here\n  └──┬──────────────────┘\n")
	}
	b.WriteString("     │\n  ┌──▼──────────────────┐\n")
	if r.Latency != nil {
		b.WriteString(fmt.Sprintf("  │  the tier behind    │  waited p95 %.0fms, p99 %.0fms\n", r.Latency.P95, r.Latency.P99))
	} else {
		b.WriteString("  │  the tier behind    │  wait not recorded\n")
	}
	b.WriteString("  └─────────────────────┘\n\n")
	b.WriteString(fmt.Sprintf("  arrivals: peak %d req/s · mean %.1f req/s over %ds\n", r.Rate.Peak, r.Rate.Mean, r.WindowSeconds))
	b.WriteString("  everything above the marked tier is invisible here: a CDN hit never\n  reaches this log, so these numbers are origin-side load, not audience.")
	return b.String()
}

func audienceLine(r analyze.Report) string {
	if r.Audience.Countable {
		return fmt.Sprintf("a client identifier is present on %.1f%% of lines — countable, but an address is not a person", r.Audience.Share*100)
	}
	return fmt.Sprintf("only %.1f%% of lines carry a client identifier — this log cannot count people", r.Audience.Share*100)
}

// truncate keeps the target column aligned. A target longer than the column is
// cut with an ellipsis rather than allowed to shift every message on the line,
// since the full value is in the JSON and the markdown.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func mdCell(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "|", "\\|"), "\n", " ")
}

// fmtTime renders a timestamp, or an en dash for a zero one. A zero time is
// what an empty window looks like, and printing its year 1 would read as data.
func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "–"
	}
	return t.Format("2006-01-02 15:04:05")
}
