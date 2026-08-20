package finding

import (
	"testing"
	"time"
)

func TestSeverityOrder(t *testing.T) {
	// ERROR above BAD: a hole in the coverage invalidates every measurement
	// below it, so an operator has to see it first.
	if !(Severity(OK) < Severity(WARN) && Severity(WARN) < Severity(BAD) && Severity(BAD) < Severity(ERROR)) {
		t.Fatal("severity order is wrong")
	}
}

func TestAtLeast(t *testing.T) {
	if !AtLeast(BAD, WARN) || AtLeast(WARN, BAD) {
		t.Error("AtLeast is not the threshold test it claims to be")
	}
	if !AtLeast(OK, "") {
		t.Error("an empty threshold is satisfied by anything")
	}
}

func TestWorstAndSummarize(t *testing.T) {
	fs := []Finding{{Status: OK}, {Status: BAD}, {Status: WARN}}
	if Worst(fs) != BAD {
		t.Errorf("Worst = %s", Worst(fs))
	}
	if Worst(nil) != OK {
		t.Error("no findings is OK, not empty")
	}
	sum := Summarize(fs)
	if sum[OK] != 1 || sum[WARN] != 1 || sum[BAD] != 1 || sum[ERROR] != 0 {
		t.Errorf("Summarize = %v", sum)
	}
}

func TestSortWorstFirstIsStableAndTotal(t *testing.T) {
	fs := []Finding{
		{Check: "wait", Target: "b", Status: OK},
		{Check: "rate", Target: "a", Status: BAD},
		{Check: "cache", Target: "a", Status: OK},
		{Check: "coverage", Target: "log", Status: ERROR},
	}
	SortWorstFirst(fs)
	if fs[0].Status != ERROR || fs[1].Status != BAD {
		t.Fatalf("order = %+v", fs)
	}
	// Two runs over the same log must render identically, so ties break on
	// check and target rather than on map order.
	if fs[2].Check != "cache" || fs[3].Check != "wait" {
		t.Errorf("ties not broken deterministically: %+v", fs[2:])
	}
}

func TestWindowSecondsCountsTheSecondItStartsIn(t *testing.T) {
	start := time.Date(2026, 8, 19, 18, 0, 0, 0, time.UTC)
	if got := (Window{Start: start, End: start}).Seconds(); got != 1 {
		t.Errorf("a single-event window = %d seconds, want 1: a rate needs a divisor", got)
	}
	if got := (Window{Start: start, End: start.Add(59 * time.Second)}).Seconds(); got != 60 {
		t.Errorf("Seconds = %d, want 60", got)
	}
	if got := (Window{}).Seconds(); got != 0 {
		t.Errorf("an empty window = %d, want 0", got)
	}
}

func TestNum(t *testing.T) {
	f := Finding{Value: Num(1.5)}
	if f.Value == nil || *f.Value != 1.5 {
		t.Error("Num did not carry the measurement")
	}
}
