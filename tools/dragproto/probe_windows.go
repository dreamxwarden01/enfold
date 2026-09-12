//go:build windows

package main

import (
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// -postprobe: does a posted message reach the loop while DoDragDrop runs?
//
// This exists because of something the real application does that the prototype
// does not. Enfold's shell is a Wails v3 WebView2 window whose main thread runs
// DoDragDrop, and its frontend is told what the drag is doing by callbacks that
// are POSTED to the main thread's hidden window -- PostMessage of a registered
// message, picked up by that thread's message loop and dispatched to its window
// procedure. During a real drag the frontend shows no progress at all, and the
// hypothesis was that DoDragDrop's modal loop never dispatches those messages,
// or that the synchronous IDropTarget::Drop -- an outgoing COM call into
// Explorer, on which our thread blocks -- does not.
//
// That is a measurable claim, and nothing in Microsoft's documentation settles
// it. DoDragDrop's page says only that it "enters a loop in which it calls
// various methods in the IDropSource and IDropTarget interfaces"; it never says
// what that loop does with the message queue. So the loop is asked directly: a
// goroutine posts a registered message to the window every 250 ms for the whole
// of the drag and for a moment after it, each one carrying a sequence number and
// the millisecond at which it was posted, and the window procedure writes down
// when it actually arrived and what the drag was doing at each end. The
// extraction posts one of its own every 64 MiB, from whatever thread Explorer
// happens to be extracting on.
//
// The summary at the end is the answer: per phase, how many were posted, how
// many arrived, how many arrived while that phase was still going on, the worst
// latency, and the longest stretch of the phase in which nothing arrived at all.
// A phase that posted eight and delivered none until after DoDragDrop returned
// is the hypothesis confirmed; one that delivered all eight on time sends the
// investigation somewhere else.

const (
	// probeMessageName is registered with RegisterWindowMessageW, which
	// "Defines a new window message that is guaranteed to be unique throughout
	// the system" and returns "a message identifier in the range 0xC000 through
	// 0xFFFF". That range is above every system message, so the window
	// procedure can compare against it without any risk of shadowing WM_NULL or
	// one of the messages it already switches on.
	probeMessageName = "EnfoldProto.Probe"

	// probeInterval is the 250 ms of the experiment.
	probeInterval = 250 * time.Millisecond

	// probeGrace is how long the poster keeps going after DoDragDrop returns.
	// It is the measurement that matters most: if the loop was not dispatching
	// during the drop, the backlog arrives in a burst in this window, and the
	// burst is the proof that the messages were posted and queued rather than
	// lost.
	probeGrace = 2 * time.Second

	// probeLate is the latency above which a receipt is always logged. A probe
	// that arrives within a few milliseconds is the uninteresting case and is
	// logged at probeQuiet intervals instead, so that a minute-long drag does
	// not bury the COM traffic the log is really for.
	probeLate  = 50 * time.Millisecond
	probeQuiet = time.Second

	// probeMax bounds the record. A drag of ten minutes posts 2400 probes per
	// window, so this is not a limit anything ordinary reaches; it is there so
	// that a run left going overnight cannot grow without bound.
	probeMax = 50000
)

// probeOn is -postprobe. Everything in this file is inert without it, including
// the phase bookkeeping, so a run without the flag behaves exactly as it did
// before the flag existed.
var probeOn atomic.Bool

// probeMsg is the registered message value, or zero if registration failed. The
// window procedures compare against it only when it is non-zero.
var probeMsg uint32

// sinceStart is the log's own clock, so a probe's stamps and the log lines
// around them are on one axis.
func sinceStart() time.Duration { return time.Since(logStart) }

// ---------------------------------------------------------------------------
// Phases.

// dragPhase is what the drag was doing when a probe was posted or delivered.
// The four that matter are the four places the messages could be getting stuck.
type dragPhase uint8

const (
	phaseIdle dragPhase = iota
	phaseHover
	phaseDrop
	phaseExtract
	phaseReturned
	phaseCount
)

var phaseNames = [phaseCount]string{
	phaseIdle:     "idle",
	phaseHover:    "hover",
	phaseDrop:     "drop",
	phaseExtract:  "extraction",
	phaseReturned: "returned",
}

func (p dragPhase) String() string {
	if int(p) < len(phaseNames) {
		return phaseNames[p]
	}
	return fmt.Sprintf("phase %d", uint8(p))
}

// phaseLegend is printed once per summary, because the names are short on
// purpose and the distinctions they draw are the whole experiment.
const phaseLegend = "hover = DoDragDrop running with the button still down; " +
	"drop = the button released and DoDragDrop not yet returned, which is where the synchronous IDropTarget::Drop is; " +
	"extraction = inside GetData writing the staged files; " +
	"returned = after DoDragDrop returned"

var (
	// phaseMu serialises "read the effective phase, then record it", so that
	// the span list is a consistent sequence even though the base phase and the
	// extraction depth are moved by different threads. It is taken before the
	// tally's own mutex and never the other way round.
	phaseMu   sync.Mutex
	phaseBase atomic.Uint32
	// extractDepth is a counter rather than a flag because the extraction can
	// in principle be re-entered by a second GetData; it is serialised by
	// extractMu in practice, and a counter costs nothing to be sure.
	extractDepth atomic.Int32
)

// currentDragPhase is the effective phase: an extraction in progress wins,
// because "inside GetData" is the phase the experiment asks about even when it
// happens during the hover (-stage-early) or after the release.
func currentDragPhase() dragPhase {
	if extractDepth.Load() > 0 {
		return phaseExtract
	}
	return dragPhase(phaseBase.Load())
}

func setDragPhase(p dragPhase) {
	phaseBase.Store(uint32(p))
	syncDragPhase()
}

func enterExtraction() {
	extractDepth.Add(1)
	syncDragPhase()
}

func leaveExtraction() {
	extractDepth.Add(-1)
	syncDragPhase()
}

// syncDragPhase closes the span that was open and opens one for the phase now
// in force. Without -postprobe there is nothing to record and it returns at
// once.
func syncDragPhase() {
	if !probeOn.Load() {
		return
	}
	phaseMu.Lock()
	probes.notePhase(currentDragPhase(), sinceStart())
	phaseMu.Unlock()
}

// ---------------------------------------------------------------------------
// What a probe carries.

// probeSource is who posted it.
type probeSource uint8

const (
	srcTicker  probeSource = iota // the 250 ms goroutine
	srcExtract                    // the extraction, once per 64 MiB written
	srcCount
)

var sourceNames = [srcCount]string{
	srcTicker:  "the 250 ms poster",
	srcExtract: "the extraction (a thread of the target's, under -agile)",
}

func (s probeSource) String() string {
	if int(s) < len(sourceNames) {
		return sourceNames[s]
	}
	return fmt.Sprintf("source %d", uint8(s))
}

// probeDest is which window it was posted to. Under -thread there are two, and
// the difference between them is the experiment: the main window's queue is the
// window thread's, the message-only window's queue belongs to the thread that
// is inside DoDragDrop.
type probeDest uint8

const (
	destMain probeDest = iota
	destOle
	destCount
)

var destNames = [destCount]string{
	destMain: "the main window (the window thread's queue)",
	destOle:  "the message-only window (the OLE thread's queue, -thread)",
}

func (d probeDest) String() string {
	if int(d) < len(destNames) {
		return destNames[d]
	}
	return fmt.Sprintf("window %d", uint8(d))
}

// encodeProbe packs everything but the timestamp into wParam: the phase in the
// low four bits, the source in the next two, the destination in the next two,
// and the sequence number above them. lParam carries the millisecond it was
// posted at, which is what the receipt's latency is measured from.
func encodeProbe(seq uint32, ph dragPhase, src probeSource, dst probeDest) uintptr {
	return uintptr(seq)<<8 | uintptr(dst&0x3)<<6 | uintptr(src&0x3)<<4 | uintptr(ph&0xF)
}

func decodeProbe(w uintptr) (seq uint32, ph dragPhase, src probeSource, dst probeDest) {
	return uint32(w >> 8), dragPhase(w & 0xF), probeSource(w >> 4 & 0x3), probeDest(w >> 6 & 0x3)
}

// ---------------------------------------------------------------------------
// The record.

type probeRecord struct {
	seq       uint32
	source    probeSource
	dest      probeDest
	sendPhase dragPhase
	sent      time.Duration
	posted    bool // PostMessageW accepted it
	got       bool
	recv      time.Duration
	recvPhase dragPhase
}

// spanOpen marks a span that has not ended yet.
const spanOpen = time.Duration(-1)

type phaseSpan struct {
	phase      dragPhase
	start, end time.Duration
}

type probeTally struct {
	mu    sync.Mutex
	recs  []probeRecord // indexed by seq-1
	spans []phaseSpan

	over      int // probes not recorded because probeMax was reached
	failed    int // PostMessageW refused
	unknown   int // a receipt whose sequence number is not ours
	duplicate int // the same sequence number delivered twice

	lastLine [destCount]time.Duration
	seen     [phaseCount][destCount]bool
}

var probes probeTally

// reset makes the tally the record of one drag rather than of the process: the
// summary is printed per drag, and a second drag's numbers would be unreadable
// added to the first's.
// The fields are cleared one by one rather than by assigning a zero struct:
// t.mu is one of them, and zeroing a mutex that is held is how a reset ends in
// "unlock of unlocked mutex".
func (t *probeTally) reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.recs = nil
	t.spans = nil
	t.over, t.failed, t.unknown, t.duplicate = 0, 0, 0, 0
	t.lastLine = [destCount]time.Duration{}
	t.seen = [phaseCount][destCount]bool{}
}

// notePhase closes the open span and opens a new one. A repeat of the phase
// already in force is not a transition and is ignored, which is what lets every
// caller say "the phase may have changed" without checking first.
func (t *probeTally) notePhase(p dragPhase, at time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n := len(t.spans); n > 0 {
		if t.spans[n-1].phase == p {
			return
		}
		t.spans[n-1].end = at
	}
	t.spans = append(t.spans, phaseSpan{phase: p, start: at, end: spanOpen})
}

// noteSend allocates the sequence number that goes into the message. It is
// called before the post, because the number has to be in the message, and the
// post's own failure is recorded separately by notePostFailed.
func (t *probeTally) noteSend(src probeSource, dst probeDest, ph dragPhase, at time.Duration) (uint32, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.recs) >= probeMax {
		t.over++
		return 0, false
	}
	t.recs = append(t.recs, probeRecord{
		seq:       uint32(len(t.recs) + 1),
		source:    src,
		dest:      dst,
		sendPhase: ph,
		sent:      at,
		posted:    true,
	})
	return uint32(len(t.recs)), true
}

func (t *probeTally) notePostFailed(seq uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if int(seq) >= 1 && int(seq) <= len(t.recs) {
		t.recs[seq-1].posted = false
	}
	t.failed++
}

// noteReceive writes down an arrival and decides whether it deserves a line.
// The returned record is a copy: the caller logs outside the mutex, because
// logf takes a mutex of its own and nothing here may be held across it.
func (t *probeTally) noteReceive(seq uint32, at time.Duration, ph dragPhase) (probeRecord, bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if int(seq) < 1 || int(seq) > len(t.recs) {
		t.unknown++
		return probeRecord{}, false, false
	}
	r := &t.recs[seq-1]
	if r.got {
		t.duplicate++
		return *r, false, false
	}
	r.got = true
	r.recv = at
	r.recvPhase = ph
	line := false
	if !t.seen[r.sendPhase][r.dest] {
		t.seen[r.sendPhase][r.dest] = true
		line = true
	}
	if at-r.sent >= probeLate {
		line = true
	}
	if at-t.lastLine[r.dest] >= probeQuiet {
		line = true
	}
	if line {
		t.lastLine[r.dest] = at
	}
	return *r, line, true
}

// ---------------------------------------------------------------------------
// The summary.

type probeRow struct {
	phase   dragPhase
	dest    probeDest
	sent    int
	tick    int
	extract int
	got     int
	inPhase int
	maxLat  time.Duration
	maxSeq  uint32
	span    time.Duration
	gap     time.Duration
}

// line is the one sentence the experiment is read from.
func (r probeRow) line() string {
	s := fmt.Sprintf("%-10s -> %s: %d posted (%d poster, %d extraction), %d delivered, %d of them while the drag was still in that phase",
		r.phase, r.dest, r.sent, r.tick, r.extract, r.got, r.inPhase)
	if r.got > 0 {
		s += fmt.Sprintf("; worst latency %s (probe #%d)", r.maxLat.Round(time.Millisecond), r.maxSeq)
	}
	s += fmt.Sprintf("; the phase lasted %s and its longest stretch with nothing delivered was %s",
		r.span.Round(time.Millisecond), r.gap.Round(time.Millisecond))
	if r.sent > 0 && r.inPhase == 0 {
		s += " -- NOTHING posted in this phase was delivered before the phase ended"
	}
	return s
}

// summary turns the record into one row per phase and destination that saw any
// traffic. now closes the span that is still open, so the caller passes the
// moment the summary is being taken.
func (t *probeTally) summary(now time.Duration) []probeRow {
	t.mu.Lock()
	defer t.mu.Unlock()

	var spanTotal [phaseCount]time.Duration
	var gap [phaseCount][destCount]time.Duration

	// Receipts per destination, in arrival order, for the gap computation. A
	// gap is about the destination and not about who posted into it: the
	// question is whether that queue was being drained at all.
	var recv [destCount][]time.Duration
	for _, r := range t.recs {
		if r.got && int(r.dest) < int(destCount) {
			recv[r.dest] = append(recv[r.dest], r.recv)
		}
	}
	for d := range recv {
		sort.Slice(recv[d], func(i, j int) bool { return recv[d][i] < recv[d][j] })
	}

	for _, s := range t.spans {
		end := s.end
		if end == spanOpen {
			end = now
		}
		if int(s.phase) >= int(phaseCount) {
			continue
		}
		spanTotal[s.phase] += end - s.start
		for d := 0; d < int(destCount); d++ {
			if g := longestGap(s.start, end, recv[d]); g > gap[s.phase][d] {
				gap[s.phase][d] = g
			}
		}
	}

	var rows [phaseCount][destCount]*probeRow
	var order []*probeRow
	for _, r := range t.recs {
		if !r.posted || int(r.sendPhase) >= int(phaseCount) || int(r.dest) >= int(destCount) {
			continue
		}
		row := rows[r.sendPhase][r.dest]
		if row == nil {
			row = &probeRow{
				phase: r.sendPhase,
				dest:  r.dest,
				span:  spanTotal[r.sendPhase],
				gap:   gap[r.sendPhase][r.dest],
			}
			rows[r.sendPhase][r.dest] = row
			order = append(order, row)
		}
		row.sent++
		switch r.source {
		case srcExtract:
			row.extract++
		default:
			row.tick++
		}
		if r.got {
			row.got++
			if r.recvPhase == r.sendPhase {
				row.inPhase++
			}
			if lat := r.recv - r.sent; lat > row.maxLat {
				row.maxLat = lat
				row.maxSeq = r.seq
			}
		}
	}

	out := make([]probeRow, 0, len(order))
	for _, r := range order {
		out = append(out, *r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].phase != out[j].phase {
			return out[i].phase < out[j].phase
		}
		return out[i].dest < out[j].dest
	})
	return out
}

// longestGap is the longest stretch inside [start, end] during which nothing in
// recv arrived. recv must be sorted ascending; entries outside the span are
// ignored, which is what lets one destination's whole receipt list be handed to
// every span.
func longestGap(start, end time.Duration, recv []time.Duration) time.Duration {
	if end <= start {
		return 0
	}
	best := time.Duration(0)
	prev := start
	for _, r := range recv {
		if r < start {
			continue
		}
		if r > end {
			break
		}
		if d := r - prev; d > best {
			best = d
		}
		prev = r
	}
	if d := end - prev; d > best {
		best = d
	}
	return best
}

// trouble names the accounting failures, which are findings of their own: a
// refused post is a queue that is full (the documented limit is 10000 messages
// per queue), and an unknown sequence number would mean a message of somebody
// else's arriving on our registered value.
func (t *probeTally) trouble() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var parts []string
	if t.failed > 0 {
		parts = append(parts, fmt.Sprintf("%d post(s) REFUSED by PostMessageW", t.failed))
	}
	if t.over > 0 {
		parts = append(parts, fmt.Sprintf("%d probe(s) not posted because the %d-record limit was reached", t.over, probeMax))
	}
	if t.unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d receipt(s) with a sequence number that is not ours", t.unknown))
	}
	if t.duplicate > 0 {
		parts = append(parts, fmt.Sprintf("%d probe(s) delivered twice", t.duplicate))
	}
	if len(parts) == 0 {
		return ""
	}
	s := parts[0]
	for _, p := range parts[1:] {
		s += ", " + p
	}
	return s
}

// ---------------------------------------------------------------------------
// The windows a probe is posted to.

type probeWindow struct {
	hwnd uintptr
	dest probeDest
}

var probeWnd struct {
	mu   sync.Mutex
	list []probeWindow
}

func registerProbeWindow(hwnd uintptr, dest probeDest) {
	if hwnd == 0 {
		return
	}
	probeWnd.mu.Lock()
	defer probeWnd.mu.Unlock()
	for _, w := range probeWnd.list {
		if w.hwnd == hwnd {
			return
		}
	}
	probeWnd.list = append(probeWnd.list, probeWindow{hwnd: hwnd, dest: dest})
}

// unregisterProbeWindow stops the poster before the window is destroyed. Under
// -thread it is called as soon as DoDragDrop returns, so that the OLE thread's
// queue holds exactly what the drag put there and nothing from the grace period
// it will never pump.
func unregisterProbeWindow(hwnd uintptr) {
	probeWnd.mu.Lock()
	defer probeWnd.mu.Unlock()
	for i, w := range probeWnd.list {
		if w.hwnd == hwnd {
			probeWnd.list = append(probeWnd.list[:i], probeWnd.list[i+1:]...)
			return
		}
	}
}

func probeWindows() []probeWindow {
	probeWnd.mu.Lock()
	defer probeWnd.mu.Unlock()
	return append([]probeWindow(nil), probeWnd.list...)
}

// ---------------------------------------------------------------------------
// Posting and receiving.

// postProbe posts one probe to every registered window. PostMessageW "Places
// (posts) a message in the message queue associated with the thread that
// created the specified window and returns without waiting for the thread to
// process the message" -- which is why it is safe to call from the extraction,
// running on a thread of Explorer's, and why a probe that never arrives says
// something about the recipient's loop rather than about the sender.
func postProbe(src probeSource) {
	if !probeOn.Load() || probeMsg == 0 {
		return
	}
	ph := currentDragPhase()
	now := sinceStart()
	for _, w := range probeWindows() {
		seq, ok := probes.noteSend(src, w.dest, ph, now)
		if !ok {
			return
		}
		r, _, _ := procPostMessageW.Call(w.hwnd, uintptr(probeMsg),
			encodeProbe(seq, ph, src, w.dest), uintptr(now.Milliseconds()))
		if r == 0 {
			probes.notePostFailed(seq)
			logf("probe #%d could not be posted to %s -- PostMessageW refused it", seq, w.dest)
		}
	}
}

// handleProbe is the window procedure's half, and the whole point of the
// exercise: the moment written here is when the loop got round to dispatching a
// message that was posted at the moment carried in lParam.
func handleProbe(dest probeDest, wParam, lParam uintptr) {
	now := sinceStart()
	nowPhase := currentDragPhase()
	seq, sendPhase, src, encDest := decodeProbe(wParam)
	stamp := time.Duration(int64(lParam)) * time.Millisecond
	rec, line, ok := probes.noteReceive(seq, now, nowPhase)
	if !ok {
		// An unrecorded sequence number is either a probe from a previous
		// drag's tally, or a message of somebody else's on our registered
		// value. Either is worth one line.
		logf("probe #%d arrived on %s but is not in this drag's record (posted in phase %s, %s ago)",
			seq, dest, sendPhase, (now - stamp).Round(time.Millisecond))
		return
	}
	if encDest != dest {
		logf("probe #%d was addressed to %s but arrived on %s", seq, encDest, dest)
	}
	if !line {
		return
	}
	logf("probe #%d from %s reached %s %s after it was posted: posted in phase %s, delivered in phase %s",
		seq, src, dest, (now - stamp).Round(time.Millisecond), rec.sendPhase, nowPhase)
}

// ---------------------------------------------------------------------------
// The run.

// probeRun is one drag's worth of posting. It outlives the drag by probeGrace,
// on purpose: in the default mode the drag runs on the window thread itself, so
// a backlog left by DoDragDrop can only be dispatched once startDrag has
// returned and the message loop is running again. Printing the summary from the
// goroutine rather than from the drag is what lets it include that.
type probeRun struct {
	drag int
	// end is the drag saying it is over: run out the grace period, then
	// summarise. cut is the next drag saying it cannot wait: summarise now.
	end  chan struct{}
	cut  chan struct{}
	done chan struct{}

	endOnce, cutOnce, doneOnce sync.Once
}

// activeProbe is the run of the last drag, so that a second gesture inside the
// grace period does not reset the tally out from under the first drag's summary.
// The tally is one drag's, and two drags' numbers added together would be
// unreadable.
var activeProbe atomic.Pointer[probeRun]

// startProbeRun resets the tally, opens the hover span and starts the poster.
// It returns nil without -postprobe, and every method below tolerates a nil
// receiver so that the drag does not have to check.
func startProbeRun(drag int) *probeRun {
	if !probeOn.Load() {
		setDragPhase(phaseHover)
		return nil
	}
	// A drag started within probeGrace of the last one arrives while the
	// previous poster is still running. Cut it short and let it write its
	// summary BEFORE the tally is cleared; that wait is a channel receive the
	// other goroutine satisfies at once, not the grace period.
	if prev := activeProbe.Swap(nil); prev != nil {
		prev.cutShort()
	}

	probes.reset()
	setDragPhase(phaseHover)
	r := &probeRun{
		drag: drag,
		end:  make(chan struct{}),
		cut:  make(chan struct{}),
		done: make(chan struct{}),
	}
	activeProbe.Store(r)
	logf("-postprobe: posting %s to %d window(s) every %s for the whole of drag %d, and for %s after it returns",
		probeMessageName, len(probeWindows()), probeInterval, drag, probeGrace)
	go r.loop(r.end)
	return r
}

// loop posts on the tick and, once the drag says it is finished, keeps posting
// for probeGrace before writing the summary.
func (r *probeRun) loop(end <-chan struct{}) {
	t := time.NewTicker(probeInterval)
	defer t.Stop()
	var grace <-chan time.Time
	postProbe(srcTicker)
	for {
		select {
		case <-t.C:
			postProbe(srcTicker)
		case <-end:
			// Nilled so that the closed channel does not spin the select. Only
			// this goroutine touches the local.
			end = nil
			grace = time.After(probeGrace)
		case <-grace:
			r.summarise()
			return
		case <-r.cut:
			logf("-postprobe: drag %d's grace period was cut short by the next drag", r.drag)
			r.summarise()
			return
		}
	}
}

// summarise writes the run's summary exactly once, whichever way it ended.
func (r *probeRun) summarise() {
	r.doneOnce.Do(func() {
		logProbeSummary(r.drag)
		close(r.done)
	})
}

// finish tells the poster the drag is over. It does not wait: in the default
// mode the caller IS the window thread, and waiting here would stop the very
// loop whose backlog is being measured.
func (r *probeRun) finish() {
	if r == nil {
		return
	}
	r.endOnce.Do(func() { close(r.end) })
}

// cutShort ends the run now and waits for its summary. It is only ever called
// from the start of the NEXT drag, on whichever thread runs it, and the wait is
// for a goroutine sitting in a select -- never for the grace period.
func (r *probeRun) cutShort() {
	if r == nil {
		return
	}
	r.cutOnce.Do(func() { close(r.cut) })
	<-r.done
}

func logProbeSummary(drag int) {
	rows := probes.summary(sinceStart())
	logf("---- -postprobe summary for drag %d ----", drag)
	logf("    phases: %s", phaseLegend)
	for _, row := range rows {
		logf("    %s", row.line())
	}
	if len(rows) == 0 {
		logf("    nothing was posted at all")
	}
	if t := probes.trouble(); t != "" {
		logf("    trouble: %s", t)
	}
	logf("---- end of the -postprobe summary for drag %d ----", drag)
}
