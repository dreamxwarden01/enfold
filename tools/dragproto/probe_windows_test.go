//go:build windows

package main

import (
	"strings"
	"testing"
	"time"
)

// The probe is an accounting machine with one Win32 call at each end -- a post
// and a window procedure. Neither end can be unit-tested: posting needs a window
// on a thread with a queue, and the window procedure is called by user32. The
// accounting between them is the part that decides what the experiment says, and
// all of it is here.

// withProbes gives one test the probe globals to itself and puts them back
// afterwards, so a test that turns the flag on cannot change what a later one
// measures.
func withProbes(t *testing.T) *probeTally {
	t.Helper()
	prevOn := probeOn.Load()
	prevBase := phaseBase.Load()
	prevDepth := extractDepth.Load()
	probeOn.Store(true)
	probes.reset()
	t.Cleanup(func() {
		probeOn.Store(prevOn)
		phaseBase.Store(prevBase)
		extractDepth.Store(prevDepth)
		probes.reset()
	})
	return &probes
}

func TestProbeEncodingRoundTrip(t *testing.T) {
	cases := []struct {
		seq uint32
		ph  dragPhase
		src probeSource
		dst probeDest
	}{
		{1, phaseHover, srcTicker, destMain},
		{2, phaseDrop, srcExtract, destOle},
		{3, phaseExtract, srcExtract, destMain},
		{4, phaseReturned, srcTicker, destOle},
		{0, phaseIdle, srcTicker, destMain},
		// A drag of ten minutes posts a few thousand; this is far past anything
		// a run reaches, and the point is that the sequence number is not
		// truncated by the fields packed under it.
		{1 << 20, phaseDrop, srcTicker, destOle},
	}
	for _, c := range cases {
		w := encodeProbe(c.seq, c.ph, c.src, c.dst)
		seq, ph, src, dst := decodeProbe(w)
		if seq != c.seq || ph != c.ph || src != c.src || dst != c.dst {
			t.Errorf("encodeProbe(%d, %v, %v, %v) -> 0x%X decoded as (%d, %v, %v, %v)",
				c.seq, c.ph, c.src, c.dst, w, seq, ph, src, dst)
		}
	}
}

// TestProbePhasePrecedence pins the one rule that is not obvious: an extraction
// in progress is the phase, whatever the drag was doing around it. A wrong
// answer here would file the extraction's probes under "drop" and the experiment
// would not be able to tell the two apart at all.
func TestProbePhasePrecedence(t *testing.T) {
	withProbes(t)

	setDragPhase(phaseHover)
	if got := currentDragPhase(); got != phaseHover {
		t.Fatalf("phase is %v, want hover", got)
	}
	enterExtraction()
	if got := currentDragPhase(); got != phaseExtract {
		t.Errorf("phase during an extraction is %v, want extraction", got)
	}
	// The release arrives while the extraction is still running: the base phase
	// moves, the effective one does not.
	setDragPhase(phaseDrop)
	if got := currentDragPhase(); got != phaseExtract {
		t.Errorf("phase during an extraction that saw the release is %v, want extraction", got)
	}
	leaveExtraction()
	if got := currentDragPhase(); got != phaseDrop {
		t.Errorf("phase after the extraction is %v, want drop", got)
	}
	setDragPhase(phaseReturned)
	if got := currentDragPhase(); got != phaseReturned {
		t.Errorf("phase after the return is %v, want returned", got)
	}
}

// TestProbeSpansAreASequence checks that the spans the gap is computed over are
// a closed, non-overlapping sequence, and that a phase set twice is not two
// spans -- every caller says "the phase may have changed" without checking.
func TestProbeSpansAreASequence(t *testing.T) {
	tally := &probeTally{}
	tally.notePhase(phaseHover, 0)
	tally.notePhase(phaseHover, 100*time.Millisecond) // not a transition
	tally.notePhase(phaseDrop, time.Second)
	tally.notePhase(phaseReturned, 3*time.Second)

	if len(tally.spans) != 3 {
		t.Fatalf("%d spans, want 3: %+v", len(tally.spans), tally.spans)
	}
	want := []struct {
		phase      dragPhase
		start, end time.Duration
	}{
		{phaseHover, 0, time.Second},
		{phaseDrop, time.Second, 3 * time.Second},
		{phaseReturned, 3 * time.Second, spanOpen},
	}
	for i, w := range want {
		got := tally.spans[i]
		if got.phase != w.phase || got.start != w.start || got.end != w.end {
			t.Errorf("span %d is %v %v..%v, want %v %v..%v",
				i, got.phase, got.start, got.end, w.phase, w.start, w.end)
		}
	}
}

func TestLongestGap(t *testing.T) {
	ms := time.Millisecond
	cases := []struct {
		name       string
		start, end time.Duration
		recv       []time.Duration
		want       time.Duration
	}{
		{"nothing arrived at all", 0, time.Second, nil, time.Second},
		{"one in the middle", 0, time.Second, []time.Duration{400 * ms}, 600 * ms},
		{"evenly spaced", 0, time.Second, []time.Duration{250 * ms, 500 * ms, 750 * ms}, 250 * ms},
		{"a stall in the middle", 0, time.Second, []time.Duration{100 * ms, 900 * ms}, 800 * ms},
		// Receipts outside the span belong to another phase and must not close
		// this one's gap -- the whole list of a destination's receipts is handed
		// to every span.
		{"receipts outside the span are ignored", time.Second, 2 * time.Second,
			[]time.Duration{100 * ms, 3 * time.Second}, time.Second},
		{"at the edges", 0, time.Second, []time.Duration{0, time.Second}, time.Second},
		{"an empty span", time.Second, time.Second, nil, 0},
		{"a span that ended before it started", 2 * time.Second, time.Second, nil, 0},
	}
	for _, c := range cases {
		if got := longestGap(c.start, c.end, c.recv); got != c.want {
			t.Errorf("%s: longestGap = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestProbeSummaryPerPhase is the experiment itself, in the shape the real thing
// is expected to produce: probes posted during the drop are delivered only after
// DoDragDrop returns. The row for "drop" must say none of them arrived while the
// drag was still in it, and its gap must be the whole phase.
func TestProbeSummaryPerPhase(t *testing.T) {
	tally := &probeTally{}
	sec := time.Second

	// Hover: two probes, both delivered promptly.
	tally.notePhase(phaseHover, 0)
	h1, _ := tally.noteSend(srcTicker, destMain, phaseHover, 0)
	h2, _ := tally.noteSend(srcTicker, destMain, phaseHover, 250*time.Millisecond)
	tally.noteReceive(h1, 2*time.Millisecond, phaseHover)
	tally.noteReceive(h2, 252*time.Millisecond, phaseHover)

	// Drop: three probes, none delivered until the phase is over.
	tally.notePhase(phaseDrop, sec)
	d1, _ := tally.noteSend(srcTicker, destMain, phaseDrop, sec)
	d2, _ := tally.noteSend(srcTicker, destMain, phaseDrop, sec+250*time.Millisecond)
	d3, _ := tally.noteSend(srcExtract, destMain, phaseDrop, sec+500*time.Millisecond)

	tally.notePhase(phaseReturned, 4*sec)
	tally.noteReceive(d1, 4*sec+time.Millisecond, phaseReturned)
	tally.noteReceive(d2, 4*sec+2*time.Millisecond, phaseReturned)
	tally.noteReceive(d3, 4*sec+3*time.Millisecond, phaseReturned)

	rows := tally.summary(5 * sec)
	byPhase := map[dragPhase]probeRow{}
	for _, r := range rows {
		byPhase[r.phase] = r
	}

	hover, ok := byPhase[phaseHover]
	if !ok {
		t.Fatalf("no row for the hover: %+v", rows)
	}
	if hover.sent != 2 || hover.got != 2 || hover.inPhase != 2 {
		t.Errorf("hover: %d posted, %d delivered, %d in phase; want 2/2/2", hover.sent, hover.got, hover.inPhase)
	}
	if hover.gap != 748*time.Millisecond {
		t.Errorf("hover gap %v, want 748ms (the stretch from the second receipt to the end of the phase)", hover.gap)
	}

	drop, ok := byPhase[phaseDrop]
	if !ok {
		t.Fatalf("no row for the drop: %+v", rows)
	}
	if drop.sent != 3 || drop.tick != 2 || drop.extract != 1 {
		t.Errorf("drop: %d posted (%d poster, %d extraction); want 3 (2, 1)", drop.sent, drop.tick, drop.extract)
	}
	if drop.got != 3 {
		t.Errorf("drop: %d delivered eventually, want 3", drop.got)
	}
	if drop.inPhase != 0 {
		t.Errorf("drop: %d delivered while still in the phase, want 0 -- that is the whole finding", drop.inPhase)
	}
	if drop.span != 3*sec {
		t.Errorf("drop span %v, want 3s", drop.span)
	}
	if drop.gap != 3*sec {
		t.Errorf("drop gap %v, want the whole 3s phase: nothing arrived during it", drop.gap)
	}
	if drop.maxLat != 3*sec+time.Millisecond {
		t.Errorf("drop worst latency %v, want 3.001s", drop.maxLat)
	}
	if line := drop.line(); !strings.Contains(line, "NOTHING posted in this phase was delivered") {
		t.Errorf("the drop row does not say nothing got through: %q", line)
	}
	if line := hover.line(); strings.Contains(line, "NOTHING posted") {
		t.Errorf("the hover row claims nothing got through when everything did: %q", line)
	}
}

// TestProbeSummarySeparatesDestinations is -thread's half: the same phase, two
// queues, and the window thread's draining while the OLE thread's does not.
func TestProbeSummarySeparatesDestinations(t *testing.T) {
	tally := &probeTally{}
	tally.notePhase(phaseHover, 0)
	main, _ := tally.noteSend(srcTicker, destMain, phaseHover, 0)
	ole, _ := tally.noteSend(srcTicker, destOle, phaseHover, 0)
	tally.noteReceive(main, 3*time.Millisecond, phaseHover)
	// The OLE window's probe is never delivered: nothing pumps that queue.
	tally.notePhase(phaseReturned, time.Second)

	rows := tally.summary(time.Second)
	if len(rows) != 2 {
		t.Fatalf("%d rows, want one per destination: %+v", len(rows), rows)
	}
	var seenMain, seenOle bool
	for _, r := range rows {
		switch r.dest {
		case destMain:
			seenMain = true
			if r.got != 1 || r.inPhase != 1 {
				t.Errorf("the main window: %d delivered, %d in phase; want 1/1", r.got, r.inPhase)
			}
		case destOle:
			seenOle = true
			if r.got != 0 {
				t.Errorf("the OLE window: %d delivered, want 0", r.got)
			}
			if r.gap != time.Second {
				t.Errorf("the OLE window's gap is %v, want the whole second", r.gap)
			}
		}
		_ = ole
	}
	if !seenMain || !seenOle {
		t.Errorf("the summary did not keep the two queues apart: %+v", rows)
	}
}

// TestProbeReceiptAccounting covers the three things that can go wrong at the
// receiving end, each of which would otherwise be invisible: a sequence number
// that is not ours, the same probe delivered twice, and a post that was refused.
func TestProbeReceiptAccounting(t *testing.T) {
	tally := &probeTally{}
	tally.notePhase(phaseHover, 0)
	seq, ok := tally.noteSend(srcTicker, destMain, phaseHover, 0)
	if !ok || seq != 1 {
		t.Fatalf("noteSend gave seq %d, ok %v; want 1, true", seq, ok)
	}

	if _, _, ok := tally.noteReceive(99, time.Second, phaseHover); ok {
		t.Error("a sequence number that was never posted was accepted")
	}
	if tally.unknown != 1 {
		t.Errorf("%d unknown receipts counted, want 1", tally.unknown)
	}

	if _, _, ok := tally.noteReceive(seq, 5*time.Millisecond, phaseHover); !ok {
		t.Fatal("the first receipt of a posted probe was rejected")
	}
	if _, _, ok := tally.noteReceive(seq, 6*time.Millisecond, phaseHover); ok {
		t.Error("the same probe was accepted twice")
	}
	if tally.duplicate != 1 {
		t.Errorf("%d duplicates counted, want 1", tally.duplicate)
	}

	refused, _ := tally.noteSend(srcTicker, destMain, phaseHover, time.Second)
	tally.notePostFailed(refused)
	rows := tally.summary(2 * time.Second)
	for _, r := range rows {
		if r.sent != 1 {
			t.Errorf("a refused post was counted as posted: %d in %v", r.sent, r)
		}
	}
	if !strings.Contains(tally.trouble(), "REFUSED") {
		t.Errorf("the trouble line does not mention the refused post: %q", tally.trouble())
	}
}

// TestProbeRecordLimit keeps a run left going overnight from growing without
// bound, and makes the overflow say so rather than silently under-reporting.
func TestProbeRecordLimit(t *testing.T) {
	tally := &probeTally{}
	tally.recs = make([]probeRecord, probeMax)
	if _, ok := tally.noteSend(srcTicker, destMain, phaseHover, 0); ok {
		t.Error("noteSend kept recording past the limit")
	}
	if tally.over != 1 {
		t.Errorf("%d overflows counted, want 1", tally.over)
	}
	if !strings.Contains(tally.trouble(), "limit was reached") {
		t.Errorf("the trouble line does not mention the limit: %q", tally.trouble())
	}
}

// TestProbeLineThrottle pins the rule that keeps a minute-long drag from burying
// the COM traffic: the first receipt of each phase and destination is always
// logged, a late one always is, and the quiet ones are logged about once a
// second.
func TestProbeLineThrottle(t *testing.T) {
	tally := &probeTally{}
	tally.notePhase(phaseHover, 0)

	first, _ := tally.noteSend(srcTicker, destMain, phaseHover, 0)
	if _, line, _ := tally.noteReceive(first, time.Millisecond, phaseHover); !line {
		t.Error("the first receipt of a phase was not logged")
	}

	quiet, _ := tally.noteSend(srcTicker, destMain, phaseHover, 250*time.Millisecond)
	if _, line, _ := tally.noteReceive(quiet, 251*time.Millisecond, phaseHover); line {
		t.Error("a prompt receipt a quarter of a second after the last line was logged")
	}

	late, _ := tally.noteSend(srcTicker, destMain, phaseHover, 500*time.Millisecond)
	if _, line, _ := tally.noteReceive(late, 500*time.Millisecond+probeLate, phaseHover); !line {
		t.Error("a receipt over the latency threshold was not logged")
	}

	after, _ := tally.noteSend(srcTicker, destMain, phaseHover, 2*time.Second)
	if _, line, _ := tally.noteReceive(after, 2*time.Second+time.Millisecond, phaseHover); !line {
		t.Error("a receipt more than a second after the last line was not logged")
	}
}

// TestProbeInertWithoutTheFlag is the promise the rest of the prototype relies
// on: a run without -postprobe behaves exactly as it did before the flag
// existed. The phase is still tracked -- -disable reads it -- but nothing is
// recorded and no poster is started.
func TestProbeInertWithoutTheFlag(t *testing.T) {
	prevOn := probeOn.Load()
	prevBase := phaseBase.Load()
	probeOn.Store(false)
	probes.reset()
	t.Cleanup(func() {
		probeOn.Store(prevOn)
		phaseBase.Store(prevBase)
		probes.reset()
	})

	if r := startProbeRun(1); r != nil {
		t.Error("startProbeRun started a poster without -postprobe")
	}
	if got := currentDragPhase(); got != phaseHover {
		t.Errorf("startProbeRun did not set the phase without -postprobe: %v", got)
	}
	setDragPhase(phaseDrop)
	enterExtraction()
	leaveExtraction()

	probes.mu.Lock()
	spans := len(probes.spans)
	probes.mu.Unlock()
	if spans != 0 {
		t.Errorf("%d spans recorded without -postprobe, want none", spans)
	}
	// finish, cutShort and postProbe on a run that was never started must not
	// panic: the drag calls all three without checking.
	var nilRun *probeRun
	nilRun.finish()
	nilRun.cutShort()
	postProbe(srcTicker)
}

// TestProbeRunCutShort is the two-drags-in-one-process case the README asks for.
// A second gesture inside the grace period must not clear the tally before the
// first drag's summary has been written, and must not wait out the grace period
// to say so.
func TestProbeRunCutShort(t *testing.T) {
	withProbes(t)
	prevActive := activeProbe.Swap(nil)
	t.Cleanup(func() { activeProbe.Store(prevActive) })
	read := captureLog(t)

	first := startProbeRun(1)
	if first == nil {
		t.Fatal("no poster was started with -postprobe on")
	}
	// The drag is over, so the grace period -- two seconds -- has begun.
	first.finish()

	done := make(chan *probeRun, 1)
	go func() { done <- startProbeRun(2) }()

	var second *probeRun
	select {
	case second = <-done:
	case <-time.After(probeGrace):
		t.Fatal("the second drag waited out the first one's grace period")
	}
	t.Cleanup(func() {
		second.cutShort()
		activeProbe.Store(nil)
	})

	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("the first run never wrote its summary")
	}
	s := read()
	if !strings.Contains(s, "summary for drag 1") {
		t.Errorf("drag 1's summary was never written: %q", s)
	}
	if !strings.Contains(s, "cut short by the next drag") {
		t.Errorf("the log does not say the grace period was cut short: %q", s)
	}
	if strings.Index(s, "summary for drag 1") > strings.Index(s, "posting EnfoldProto.Probe to 0 window(s) every 250ms for the whole of drag 2") &&
		strings.Contains(s, "drag 2") {
		t.Error("drag 1's summary was written after drag 2's tally had started")
	}
}
