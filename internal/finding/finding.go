// Package finding is the result model: one Finding is one statement about one
// target, and the severity order plus the "worst first" sort are the two rules
// every renderer obeys.
//
// edgemix findings are statements about a *measurement* — a queue tail, a 5xx
// share, a log that cannot answer a question — so a finding always carries the
// number it is made of. A machine consumer must never parse Message.
package finding

import (
	"sort"
	"time"
)

// Status of a single finding. Severity order: OK < WARN < BAD < ERROR.
type Status string

const (
	OK   Status = "OK"
	WARN Status = "WARN"
	BAD  Status = "BAD"
	// ERROR means the analysis could not run, or ran over an incomplete
	// record. It sorts above BAD because a hole in the coverage invalidates
	// every number below it, which an operator has to know first.
	ERROR Status = "ERROR"
)

var severity = map[Status]int{OK: 0, WARN: 1, BAD: 2, ERROR: 3}

// Severity is the numeric rank of s in the order OK < WARN < BAD < ERROR.
func Severity(s Status) int { return severity[s] }

// AtLeast reports whether s is at or above threshold. An empty threshold is
// satisfied by anything, since severity[""] is the zero value — a caller that
// means "no threshold at all" must test for "" itself.
func AtLeast(s, threshold Status) bool { return severity[s] >= severity[threshold] }

// Finding is one statement about one target.
//
// Check is the analysis that produced it (rate, queue, errors, coverage, …) and
// Target names what was looked at — a class, a path, the log itself.
// Value/Unit carry the measurement, Hint says what it means operationally.
type Finding struct {
	Check   string   `json:"check"`
	Target  string   `json:"target"`
	Status  Status   `json:"status"`
	Message string   `json:"message"`
	Value   *float64 `json:"value,omitempty"`
	Unit    string   `json:"unit,omitempty"`
	Hint    string   `json:"hint,omitempty"`
}

// Num returns a pointer to v, for setting Finding.Value inline.
func Num(v float64) *float64 { return &v }

// Summarize counts findings per status.
func Summarize(fs []Finding) map[Status]int {
	out := map[Status]int{OK: 0, WARN: 0, BAD: 0, ERROR: 0}
	for _, f := range fs {
		out[f.Status]++
	}
	return out
}

// Worst returns the highest severity present, or OK for no findings.
func Worst(fs []Finding) Status {
	worst := OK
	for _, f := range fs {
		if AtLeast(f.Status, worst) {
			worst = f.Status
		}
	}
	return worst
}

// SortWorstFirst orders findings by descending severity, then by check and
// target, so two runs over the same log render byte-identically.
func SortWorstFirst(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if a, b := severity[fs[i].Status], severity[fs[j].Status]; a != b {
			return a > b
		}
		if fs[i].Check != fs[j].Check {
			return fs[i].Check < fs[j].Check
		}
		return fs[i].Target < fs[j].Target
	})
}

// Window is the time span an analysis covers.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Seconds is the length of the window, rounded up to whole seconds. A window
// with a single event is one second long, not zero — a rate needs a divisor.
func (w Window) Seconds() int {
	if w.Start.IsZero() || w.End.IsZero() {
		return 0
	}
	d := w.End.Sub(w.Start)
	if d < 0 {
		return 0
	}
	return int(d/time.Second) + 1
}
