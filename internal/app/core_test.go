package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/keystore"
)

func TestPasswordUnlockAndLock(t *testing.T) {
	h := newHarness(t, nil, nil)
	st := h.status()
	if st.State != StateLocked || !st.HasPasswordSlot || st.HasHardwareSlot {
		t.Fatalf("after open: %+v", st)
	}
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatalf("begin: %v", e)
	}
	if e := h.c.BeginUnlock(MethodPassword); !isCode(e, CodeCeremonyRunning) {
		t.Fatalf("second begin: %v", e)
	}
	cs := h.rec.waitCeremony(t, StepPassword, true)
	// A wrong id, a wrong kind, then the answer; the answer consumes the
	// prompt so a repeat is stale.
	if e := h.c.SubmitSecret("password", "nope", testPassword); !isCode(e, CodeStalePrompt) {
		t.Fatalf("wrong id: %v", e)
	}
	if e := h.c.SubmitSecret("pin", cs.PromptID, testPassword); !isCode(e, CodeStalePrompt) {
		t.Fatalf("wrong kind: %v", e)
	}
	if e := h.c.SubmitSecret("password", cs.PromptID, testPassword); e != nil {
		t.Fatalf("submit: %v", e)
	}
	if e := h.c.SubmitSecret("password", cs.PromptID, testPassword); e == nil {
		t.Fatal("second submit accepted")
	}
	h.rec.waitState(t, StateUnlocked)
	st = h.status()
	if st.Ceremony != nil && st.Ceremony.Step != StepDone {
		t.Fatalf("ceremony still shown: %+v", st.Ceremony)
	}
	if st.LocksAt == 0 || st.AbsoluteAt == 0 || st.LastUnlockedAt == 0 {
		t.Fatalf("deadlines missing: %+v", st)
	}
	if want := h.clk.Now().Add(defaultIdle).Unix(); st.LocksAt != want {
		t.Fatalf("locksAt %d, want %d", st.LocksAt, want)
	}
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatalf("begin while unlocked should be a no-op: %v", e)
	}
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.c.mu.Lock()
	sess, ks := h.c.vault.sess, h.c.vault.ks
	h.c.mu.Unlock()
	if sess != nil || ks != nil {
		t.Fatal("session survived the lock")
	}
	if st := h.status(); st.LocksAt != 0 || st.Ceremony != nil {
		t.Fatalf("locked status: %+v", st)
	}
	// And again: the facts survive, the vault reopens.
	h.unlockWithPassword()
}

func TestWrongPasswordIsAskedAgainInPlace(t *testing.T) {
	h := newHarness(t, nil, nil)
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatal(e)
	}
	cs := h.rec.waitCeremony(t, StepPassword, true)
	if e := h.c.SubmitSecret("password", cs.PromptID, "wrong"); e != nil {
		t.Fatal(e)
	}
	// Asked again, with the reason, under a fresh prompt id.
	again := h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != cs.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		t.Fatalf("second prompt without the reason: %+v", again)
	}
	if st := h.status(); st.State != StateUnlocking {
		t.Fatalf("the ceremony should hold Unlocking, got %s", st.State)
	}
	h.c.SubmitSecret("password", again.PromptID, testPassword)
	// The derivation shows again, and the note does not outlive the prompt.
	h.rec.waitCeremony(t, StepDeriving, false)
	if done := h.rec.waitCeremony(t, StepDone, false); done.Error != "" {
		t.Fatalf("the note outlived the accepted answer: %+v", done)
	}
	h.rec.waitState(t, StateUnlocked)
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	// The same when the recovery key is mistyped: in place, never a failure.
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodRecovery); e != nil {
		t.Fatal(e)
	}
	r := h.rec.waitCeremony(t, StepRecovery, true)
	h.c.SubmitSecret("recovery", r.PromptID, "000000000000000000000000000000000000000000000000")
	r2 := h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepRecovery && s.PromptID != "" && s.PromptID != r.PromptID
	}).(CeremonyState)
	if r2.Error != CodeAuth {
		t.Fatalf("recovery asked again without the reason: %+v", r2)
	}
	h.c.SubmitSecret("recovery", r2.PromptID, "not digits at all")
	r3 := h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepRecovery && s.PromptID != "" && s.PromptID != r2.PromptID
	}).(CeremonyState)
	if r3.Error != CodeAuth {
		t.Fatalf("unparsable digits asked again without the reason: %+v", r3)
	}
	h.c.SubmitSecret("recovery", r3.PromptID, h.recovery)
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
	if e := h.c.BeginUnlock(MethodPassword); !isCode(e, CodeVaultUnlocked) && !isCode(e, CodeCeremonyRunning) && e != nil {
		t.Fatalf("begin while unlocked: %v", e)
	}
	// What the rest of this test needs: the ceremony parked by a cancel.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatal(e)
	}
	cs = h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", cs.PromptID, "wrong")
	h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != cs.PromptID
	})
	if st := h.status(); st.State != StateUnlocking {
		t.Fatalf("the ceremony should hold Unlocking, got %s", st.State)
	}
	if e := h.c.CancelUnlock(); e != nil {
		t.Fatal(e)
	}
	h.rec.waitState(t, StateLocked)
	if e := h.c.CancelUnlock(); !isCode(e, CodeNoCeremony) {
		t.Fatalf("cancel after end: %v", e)
	}
	h.unlockWithPassword()
}

func TestLockTriggerDuringCeremony(t *testing.T) {
	h := newHarness(t, nil, nil)
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatal(e)
	}
	cs := h.rec.waitCeremony(t, StepPassword, true)
	h.c.LockNow(ReasonDisplayOff)
	final := h.rec.waitCeremony(t, StepFailed, false)
	if final.Error != CodeCancelled {
		t.Fatalf("final error %q", final.Error)
	}
	h.rec.waitState(t, StateLocked)
	if e := h.c.SubmitSecret("password", cs.PromptID, testPassword); !isCode(e, CodeNoCeremony) {
		t.Fatalf("submit after lock: %v", e)
	}
	if st := h.status(); st.State != StateLocked {
		t.Fatalf("state %s", st.State)
	}
}

func TestIdleAndAbsoluteTimers(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.clk.Advance(defaultIdle - time.Minute)
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("locked early: %s", st.State)
	}
	// Activity extends the idle deadline (no InputSource: the heartbeat is
	// trusted).
	h.c.Activity()
	if st := h.status(); st.LocksAt != h.clk.Now().Add(defaultIdle).Unix() {
		t.Fatalf("activity did not extend: %+v", st)
	}
	h.clk.Advance(defaultIdle - time.Minute)
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("locked despite activity: %s", st.State)
	}
	h.clk.Advance(2 * time.Minute)
	h.rec.waitState(t, StateLocked)

	// The absolute cap holds against continuous activity.
	h.unlockWithPassword()
	start := h.clk.Now()
	for h.clk.Now().Sub(start) < defaultAbsolute-10*time.Minute {
		h.clk.Advance(5 * time.Minute)
		h.c.Activity()
		if st := h.status(); st.State != StateUnlocked {
			t.Fatalf("locked before the cap at +%v", h.clk.Now().Sub(start))
		}
	}
	h.clk.Advance(15 * time.Minute)
	h.rec.waitState(t, StateLocked)
}

func TestActivityNeedsRealInput(t *testing.T) {
	h := newHarness(t, nil, nil)
	src := &fakeInput{}
	h.c.deps.Input = src
	h.unlockWithPassword()
	before := h.status().LocksAt
	h.clk.Advance(3 * time.Second)
	h.c.Activity() // no input seen: refused
	if h.status().LocksAt != before {
		t.Fatal("heartbeat extended without input")
	}
	src.set(h.clk.Now())
	h.c.Activity()
	if h.status().LocksAt == before {
		t.Fatal("heartbeat with input did not extend")
	}
}

type fakeInput struct {
	t  time.Time
	ok bool
}

func (f *fakeInput) set(t time.Time)              { f.t, f.ok = t, true }
func (f *fakeInput) LastInput() (time.Time, bool) { return f.t, f.ok }

// An expired idle callback can be waiting for the state mutex while an
// accepted Activity renews the deadline behind it. It must not lock the
// session the grant just extended, and the timer the grant armed must still
// lock at its own deadline (APP.md §2).
func TestStaleIdleCallbackAfterActivity(t *testing.T) {
	h := newHarness(t, nil, nil)
	src := &fakeInput{}
	h.c.deps.Input = src
	h.unlockWithPassword()
	stale := h.idleCallback()
	h.clk.Advance(3 * time.Second)
	src.set(h.clk.Now())
	h.c.Activity()
	granted := h.status()
	if granted.LocksAt != h.clk.Now().Add(defaultIdle).Unix() {
		t.Fatalf("the grant did not extend: %+v", granted)
	}
	stale() // the old callback, held at the mutex while the grant renewed
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("the stale callback locked: %s", st.State)
	}
	if st := h.status(); st.LocksAt != granted.LocksAt {
		t.Fatalf("the deadline moved: %+v", st)
	}
	h.clk.Advance(defaultIdle)
	h.rec.waitState(t, StateLocked)
}

// A callback from a session that has ended does not lock the next one.
func TestStaleIdleCallbackFromAnEndedSession(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	stale := h.idleCallback()
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.unlockWithPassword()
	stale()
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("a previous session's callback locked: %s", st.State)
	}
}

// The absolute cap applies regardless: an Activity grant re-arms the idle
// timer and does not touch it.
func TestAbsoluteLocksAfterAnActivityGrant(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	start := h.clk.Now()
	for h.clk.Now().Sub(start) < defaultAbsolute-5*time.Minute {
		h.clk.Advance(5 * time.Minute)
		h.c.Activity()
		if st := h.status(); st.State != StateUnlocked {
			t.Fatalf("locked before the cap at +%v", h.clk.Now().Sub(start))
		}
	}
	h.clk.Advance(5 * time.Minute)
	h.rec.waitState(t, StateLocked)
}

// The ordinary idle expiry still locks.
func TestIdleExpiryStillLocks(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.clk.Advance(defaultIdle - time.Second)
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("locked early: %s", st.State)
	}
	h.clk.Advance(time.Second)
	h.rec.waitState(t, StateLocked)
}

func TestTokenUnlock(t *testing.T) {
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	h := newHarness(t, cards, pub)
	if st := h.status(); !st.HasHardwareSlot {
		t.Fatal("no hardware slot")
	}
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepWaitingForKey, false)
	cards.setReaders("Yubico A", "Yubico B")
	h.rec.waitCeremony(t, StepTwoKeys, false)
	cards.setReaders("Yubico A")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if pin.Retries != 3 || !pin.RetriesKnown || pin.SlotLabel != "Test key" {
		t.Fatalf("pin prompt: %+v", pin)
	}
	if e := h.c.SubmitSecret("pin", pin.PromptID, "000000"); e != nil {
		t.Fatal(e)
	}
	again := h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepPIN && s.PromptID != "" && s.PromptID != pin.PromptID
	}).(CeremonyState)
	if again.Retries != 2 {
		t.Fatalf("retries after a wrong PIN: %+v", again)
	}
	if e := h.c.SubmitSecret("pin", again.PromptID, "123456"); e != nil {
		t.Fatal(e)
	}
	// One agreement: the wrong PIN was refused by the card before the
	// token was made, so the key was asked for exactly one operation
	// (APP.md §2.2 Probing).
	touch := h.rec.waitCeremony(t, StepTouch, false)
	if touch.N != 1 || !touch.PINAsked {
		t.Fatalf("touch: %+v", touch)
	}
	h.rec.waitCeremony(t, StepReleasing, false)
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
	card.mu.Lock()
	closes, ops := card.closes, card.ops
	card.mu.Unlock()
	if closes != 1 || ops != 1 {
		t.Fatalf("card closes=%d ops=%d, want 1 and 1", closes, ops)
	}
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatalf("begin while unlocked: %v", e)
	}
}

func TestTokenBusyParks(t *testing.T) {
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card, openErr: ErrTokenBusy}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	busy := h.rec.waitCeremony(t, StepBusy, false)
	if busy.Error != CodeTokenBusy {
		t.Fatalf("busy: %+v", busy)
	}
	// Parked: the reader is not retried in a loop.
	time.Sleep(3 * readerPoll)
	cards.mu.Lock()
	opens := cards.opens
	cards.mu.Unlock()
	if opens != 0 {
		t.Fatalf("opened %d times while parked", opens)
	}
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
}

func TestTokenNoMatchAndNotUsable(t *testing.T) {
	card := newFakeCard("123456")
	other := newFakeCard("123456")
	pub := other.addKey(0x9d, true) // enrolled key lives on another token
	card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	nm := h.rec.waitCeremony(t, StepNoMatch, false)
	if nm.Error != CodeTokenNoKey {
		t.Fatalf("no match: %+v", nm)
	}
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
	card.mu.Lock()
	closes := card.closes
	card.mu.Unlock()
	if closes != 1 {
		t.Fatalf("card not released after no match: closes=%d", closes)
	}

	// The enrolled key present but with a policy the design refuses.
	card2 := newFakeCard("123456")
	pub2 := card2.addKey(0x82, false)
	cards2 := &fakeCards{card: card2}
	cards2.setReaders("Yubico A")
	h2 := newHarness(t, cards2, pub2)
	if e := h2.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	f := h2.rec.waitCeremony(t, StepFailed, false)
	if f.Error != CodeTokenNotUsable {
		t.Fatalf("not usable: %+v", f)
	}
	h2.c.CancelUnlock()
	h2.rec.waitState(t, StateLocked)
}

func TestTokenPINBlocked(t *testing.T) {
	card := newFakeCard("123456")
	card.retries = 1
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if e := h.c.SubmitSecret("pin", pin.PromptID, "000000"); e != nil {
		t.Fatal(e)
	}
	b := h.rec.waitCeremony(t, StepBlocked, false)
	if b.Error != CodeTokenPINBlocked {
		t.Fatalf("blocked: %+v", b)
	}
	h.c.CancelUnlock()
	h.rec.waitState(t, StateLocked)
}

func TestTokenResetFailedWarns(t *testing.T) {
	card := newFakeCard("123456")
	card.closeErr = ErrTokenResetFailed
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitFor(t, EventVaultWarning, func(p any) bool {
		w, ok := p.(Warning)
		return ok && w.Code == CodeTokenReset
	})
	h.rec.waitState(t, StateUnlocked)
	found := false
	for _, w := range h.status().Warnings {
		if w == CodeTokenReset {
			found = true
		}
	}
	if !found {
		t.Fatal("warning not in status")
	}
}

func TestPromptDeadline(t *testing.T) {
	h := newHarness(t, nil, nil)
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepPassword, true)
	h.clk.Advance(promptWait + time.Second)
	f := h.rec.waitCeremony(t, StepFailed, false)
	if f.Error != CodeCancelled {
		t.Fatalf("deadline error %q", f.Error)
	}
	h.rec.waitState(t, StateLocked)
}

func TestArchiveRoundTrip(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	ap := filepath.Join(h.dir, "photos.enf")
	if _, e := h.c.CreateArchive("relative.enf", "Photos", compressionNormal); !isCode(e, CodeParams) {
		t.Fatalf("relative path: %v", e)
	}
	id, e := h.c.CreateArchive(ap, "Photos", compressionNormal)
	if e != nil {
		t.Fatalf("create: %v", e)
	}
	list, e := h.c.ListArchives(false)
	if e != nil || len(list) != 1 || list[0].ID != id || list[0].Open {
		t.Fatalf("list: %+v %v", list, e)
	}
	st, e := h.c.OpenArchive(id)
	if e != nil {
		t.Fatalf("open: %v", e)
	}
	if st.Files != 0 || st.Records != 0 || st.State != "open" {
		t.Fatalf("stat: %+v", st)
	}

	// Add two files, a folder made in the app and a source folder walked
	// into it: a folder is a record, never a prefix of a name.
	src := filepath.Join(h.dir, "src")
	os.MkdirAll(filepath.Join(src, "sub"), 0o700)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello archive\n"), 0o600)
	os.WriteFile(filepath.Join(src, "b.txt"), []byte(strings.Repeat("b", 5000)), 0o600)
	os.WriteFile(filepath.Join(src, "sub", "c.txt"), []byte("nested"), 0o600)
	opID, e := h.c.AddFiles(id, rootID, []string{filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt")}, PolicySkip)
	if e != nil {
		t.Fatalf("add: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" || len(o.Results) != 2 {
		t.Fatalf("add op: %+v", o)
	}
	docs, e := h.c.CreateFolder(id, rootID, "docs")
	if e != nil {
		t.Fatalf("create folder: %v", e)
	}
	opID, e = h.c.AddFolder(id, docs, filepath.Join(src, "sub"), PolicySkip)
	if e != nil {
		t.Fatalf("add folder: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("add folder op: %+v", o)
	}
	page, e := h.c.Page(id, rootID, "name", 0, 100)
	if e != nil {
		t.Fatalf("page: %v", e)
	}
	names := rowsByName(page)
	if len(page.Rows) != 3 || !names["docs"].IsDir {
		t.Fatalf("root page: %+v", page.Rows)
	}
	// Two files, the folder made, the folder the walk created and its file:
	// five records, three of them files.
	if st, _ := h.c.Stat(id); st.Records != 5 || st.Files != 3 || st.State != "open" {
		t.Fatalf("stat after the adds: %+v", st)
	}
	// A second add of the same name is a collision under skip; the kind on
	// both sides is what the dialog greys Replace from.
	col, e := h.c.CheckNames(id, rootID, []string{"a.txt", "new.txt", "docs/"})
	if e != nil || len(col) != 2 {
		t.Fatalf("collisions: %+v %v", col, e)
	}
	if col[0].Name != "a.txt" || col[0].IsDir || col[0].ExistingIsDir {
		t.Fatalf("file collision: %+v", col[0])
	}
	if col[1].Name != "docs/" || !col[1].IsDir || !col[1].ExistingIsDir {
		t.Fatalf("folder collision: %+v", col[1])
	}
	// An added file is committed, so it previews at once.
	if _, e := h.c.PreviewURL(id, names["a.txt"].ID); e != nil {
		t.Fatalf("preview of an added file: %v", e)
	}

	list, _ = h.c.ListArchives(false)
	if list[0].Files != 3 || list[0].ReceiptOwed || list[0].LastWrittenAt == 0 {
		t.Fatalf("list after the adds: %+v", list[0])
	}
	// The walked folder keeps its own name under the folder the app made,
	// and its file hangs off it by id.
	page, e = h.c.Page(id, docs, "name", 0, 100)
	if e != nil {
		t.Fatalf("docs page: %v", e)
	}
	if len(page.Rows) != 1 || !page.Rows[0].IsDir || page.Rows[0].Name != "sub" || page.Rows[0].Path != "docs/sub" {
		t.Fatalf("docs page: %+v", page.Rows)
	}
	sub := page.Rows[0].ID
	page, _ = h.c.Page(id, sub, "name", 0, 100)
	if len(page.Rows) != 1 || page.Rows[0].Path != "docs/sub/c.txt" {
		t.Fatalf("sub page: %+v", page.Rows)
	}
	// The breadcrumb is the whole chain, root-inclusive, the archive's name
	// on the root and the folder shown last.
	if cr := page.Crumbs; len(cr) != 3 || cr[0].ID != rootID || cr[0].Name != "Photos" || cr[1].Name != "docs" || cr[2].ID != sub {
		t.Fatalf("crumbs: %+v", page.Crumbs)
	}
	cID := page.Rows[0].ID
	text, trunc, e := h.c.PreviewText(id, cID, 3)
	if e != nil || text != "nes" || !trunc {
		t.Fatalf("preview text: %q %v %v", text, trunc, e)
	}
	u, e := h.c.PreviewURL(id, cID)
	if e != nil {
		t.Fatalf("preview url: %v", e)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "nested" || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("preview GET: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Range", "bytes=0-1,3-4")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("multi-range: %d", resp.StatusCode)
	}

	// Rename one record and delete another — each its own commit — then
	// extract the whole archive: the all-zero id is the root and takes
	// everything.
	page, _ = h.c.Page(id, rootID, "name", 0, 100)
	names = rowsByName(page)
	if e := h.c.RenameRecord(id, names["a.txt"].ID, "x/a.txt"); !isCode(e, CodeFileName) {
		t.Fatalf("rename with a slash: %v", e)
	}
	if e := h.c.RenameRecord(id, names["a.txt"].ID, "a2.txt"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if e := h.c.DeleteRecords(id, []string{names["b.txt"].ID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	// Extracted files go under GOTMPDIR when it is set: on the dev machine
	// that directory is excluded from the antivirus, whose scan of a fresh
	// file otherwise holds it open while the temp dir is being removed.
	out, err := os.MkdirTemp(os.Getenv("GOTMPDIR"), "enfold-extract")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(out)
	opID, e = h.c.Extract(id, []string{rootID}, out, ExtractSkip, nil)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	// Two folders and two files: a folder is in the plan because its record
	// is live, never because a file needed a parent.
	if o := h.rec.waitOp(t, opID); o.Error != "" || len(o.Results) != 4 {
		t.Fatalf("extract op: %+v", o)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "a2.txt")); string(b) != "hello archive\n" {
		t.Fatalf("extracted a2: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "docs", "sub", "c.txt")); string(b) != "nested" {
		t.Fatalf("extracted c: %q", b)
	}
	if _, err := os.Stat(filepath.Join(out, "b.txt")); !os.IsNotExist(err) {
		t.Fatal("deleted file was extracted")
	}

	// Verify refreshes the hash; the list shows nothing behind.
	opID, e = h.c.Verify(id)
	if e != nil {
		t.Fatalf("verify: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("verify op: %+v", o)
	}
	list, _ = h.c.ListArchives(false)
	if list[0].HashBehind != 0 {
		t.Fatalf("hash behind after verify: %+v", list[0])
	}

	// Lock: the archive stays open and its operations still commit, each
	// owing its receipt until the next unlock (APP.md §2.3).
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	after, e := h.c.CreateFolder(id, rootID, "after")
	if e != nil {
		t.Fatalf("create folder while locked: %v", e)
	}
	if e := h.c.MoveRecords(id, []string{h.row(t, id, rootID, "a2.txt").ID}, after); e != nil {
		t.Fatalf("move while locked: %v", e)
	}
	if st, e := h.c.Stat(id); e != nil || !st.ReceiptOwed {
		t.Fatalf("a commit under a lock owes its receipt: %+v %v", st, e)
	}
	if _, e := h.c.Compact(id); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("compact while locked: %v", e)
	}
	list, _ = h.c.ListArchives(false)
	if len(list) != 1 || !list[0].Open {
		t.Fatalf("list while locked: %+v", list)
	}
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close: %v", e)
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("stat after close: %v", e)
	}
}

// An open archive has no timeout of its own (APP.md §2.3, DESIGN.md §10,
// ruled 2026-09-10): it stays open while its page is shown, vault locked or
// not, and a lock locks the keystore and not an archive already open. What
// stays usable after the lock is §2.3's list — Page, PreviewText, and the
// operations, each committing and owing its receipt — and no amount of time
// takes it away.
func TestAnOpenArchiveHasNoTimeoutOfItsOwn(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("kept"), 0o600)
	opID, _ := h.c.AddFiles(id, rootID, []string{f}, PolicySkip)
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("add: %+v", o)
	}

	// Well past the session's own idle span, which the archive used to
	// borrow: the session locks and the archive does not close.
	h.clk.Advance(defaultIdle + time.Second)
	h.rec.waitState(t, StateLocked)
	for i := 0; i < 6; i++ {
		h.clk.Advance(defaultIdle + time.Second)
	}
	st, e := h.c.Stat(id)
	if e != nil {
		t.Fatalf("the archive was closed by a clock of its own: %v", e)
	}
	if st.Files != 1 {
		t.Fatalf("stat after the lock: %+v", st)
	}
	// And usable: the page, a preview, and an operation that commits and
	// owes its receipt until the next unlock.
	row := h.row(t, id, rootID, "f.txt")
	if text, _, e := h.c.PreviewText(id, row.ID, 64); e != nil || text != "kept" {
		t.Fatalf("preview under the lock: %q %v", text, e)
	}
	if _, e := h.c.CreateFolder(id, rootID, "after"); e != nil {
		t.Fatalf("create folder under the lock: %v", e)
	}
	if st := h.stat(t, id); !st.ReceiptOwed || st.Records != 2 {
		t.Fatalf("the commit under the lock: %+v", st)
	}
	// Leaving the page is what closes it, and what was committed is there.
	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("the page was left and the archive stayed open: %v", e)
	}
	h.unlockWithPassword()
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	if st, _ := h.c.Stat(id); st.Files != 1 || st.Records != 2 {
		t.Fatalf("the committed work did not survive the close: %+v", st)
	}
}

// The shutdown cancels a running operation, aborts its transaction, closes
// the archives and locks (APP.md §5): nothing half-written is published, and
// what earlier operations committed is there.
func TestShutdownCancelsARunningOperation(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", compressionNormal)
	h.c.OpenArchive(id)
	done := filepath.Join(h.dir, "done.txt")
	os.WriteFile(done, []byte("committed before the exit"), 0o600)
	opID, _ := h.c.AddFiles(id, rootID, []string{done}, PolicySkip)
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("the first add: %+v", o)
	}

	// A second add, big enough to still be running: the operation is
	// registered before AddFiles returns, so the cancel below always
	// reaches it, whatever it has written by then.
	big := filepath.Join(h.dir, "big.bin")
	os.WriteFile(big, incompressible(t, 4<<20), 0o600)
	opID, e := h.c.AddFiles(id, rootID, []string{big}, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	h.c.ResolveForShutdown(5 * time.Second)
	if o := h.rec.waitOp(t, opID); o.Error != CodeOpCancelled {
		t.Fatalf("the running operation was not cancelled: %+v", o)
	}
	if st := h.status(); st.State != StateLocked || st.OpenArchives != 0 {
		t.Fatalf("after shutdown: %+v", st)
	}
	h.unlockWithPassword()
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	st, _ := h.c.Stat(id)
	if st.Files != 1 {
		t.Fatalf("the cancelled add was published: %+v", st)
	}
	if r := h.row(t, id, rootID, "done.txt"); r.Size != uint64(len("committed before the exit")) {
		t.Fatalf("the committed add did not survive: %+v", r)
	}
}

// Close does not return until the lock it makes has closed the vault's file
// (APP.md §5). The close happens on afterLock's goroutine, so a caller free
// to remove the vault's folder the moment Close returns — every test's
// t.TempDir — used to race the handle, which on Windows is a cleanup that
// fails with "the directory is not empty".
func TestCloseWaitsForTheLocksHandle(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.lockWait = time.Minute // only the held handle holds Close here
	// The close is held where the lock does it, on its own goroutine.
	closing, release := make(chan struct{}), make(chan struct{})
	var announced, freed sync.Once
	var closedKS atomic.Bool
	h.c.closeKS = func(ks *keystore.Keystore) {
		announced.Do(func() { close(closing) })
		<-release
		ks.Close()
		closedKS.Store(true)
	}
	returned := make(chan struct{})
	// However this test ends, the held close is let go and Close is waited
	// for: a Fatal that left the seam blocked would keep the vault's file
	// open past the test and fail its folder's own cleanup — the very
	// failure this is about.
	t.Cleanup(func() {
		freed.Do(func() { close(release) })
		<-returned
	})
	go func() { h.c.Close(); close(returned) }()

	<-closing
	// The window catches a regression rather than proving the wait: a Close
	// that does not wait returns in microseconds, long inside it.
	select {
	case <-returned:
		t.Fatal("Close returned while the vault's file was still open")
	case <-time.After(50 * time.Millisecond):
	}
	freed.Do(func() { close(release) })
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return once the handle had been closed")
	}
	// And the proof the seam gives: the close ran to its end first.
	if !closedKS.Load() {
		t.Fatal("Close returned before the keystore's own Close had finished")
	}
}

// The wait is bounded: a handle that does not close — afterLock waits for a
// cancelled ceremony and for a pending touch first, which is the card's own
// time — never keeps the process standing (APP.md §5).
func TestCloseDoesNotWaitForeverForTheLocksHandle(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.lockWait = 20 * time.Millisecond // the real budget, in miniature
	release := make(chan struct{})
	var freed sync.Once
	h.c.closeKS = func(ks *keystore.Keystore) {
		<-release
		ks.Close()
	}
	returned := make(chan struct{})
	// However this test ends, the held close is let go and both goroutines
	// are waited for: a seam left blocked would hold the vault's file open
	// past the test.
	t.Cleanup(func() {
		freed.Do(func() { close(release) })
		<-returned
		h.c.lockWG.Wait()
	})
	go func() { h.c.Close(); close(returned) }()

	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Close waited past its bound for a handle that would not close")
	}
	if !h.logged("did not close the vault's file within") {
		t.Fatal("the core did not say that the lock's handle outlived the budget")
	}
}

// The ordinary path: the lock's handle is closed long before Close is
// called, and Close does not linger for the budget it never needs.
func TestCloseAfterAnOrdinaryLockReturnsPromptly(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.c.lockWait = time.Minute // a wait of the budget would be seen

	returned := make(chan struct{})
	go func() { h.c.Close(); close(returned) }()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Close lingered after an ordinary lock")
	}
}

func TestSettingsAndTimeouts(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.c.GetSettings()
	if s.TimeoutsAdjustable || s.RecoveryRecordPct != 3 || s.Look != "native" {
		t.Fatalf("locked settings: %+v", s)
	}
	s.IdleMinutes = 45
	if e := h.c.SetSettings(s); !isCode(e, CodeParams) {
		t.Fatalf("idle above the clamp: %v", e)
	}
	h.unlockWithPassword()
	s = h.c.GetSettings()
	if !s.TimeoutsAdjustable || s.IdleMinutes != 0 {
		t.Fatalf("unlocked settings: %+v", s)
	}
	s.IdleMinutes, s.AbsoluteMinutes, s.Theme = 5, 120, "dark"
	if e := h.c.SetSettings(s); e != nil {
		t.Fatalf("set: %v", e)
	}
	if st := h.status(); st.LocksAt != h.clk.Now().Add(5*time.Minute).Unix() {
		t.Fatalf("idle not rearmed: %+v", st)
	}
	if got := h.c.GetSettings(); got.IdleMinutes != 5 || got.AbsoluteMinutes != 120 || got.Theme != "dark" {
		t.Fatalf("get after set: %+v", got)
	}
	// The registry carries the timeouts: a fresh core reads them back.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	h.unlockWithPassword()
	if got := h.c.GetSettings(); got.IdleMinutes != 5 || got.AbsoluteMinutes != 120 {
		t.Fatalf("timeouts did not persist: %+v", got)
	}
	b, _ := os.ReadFile(filepath.Join(h.dir, "data", "settings.json"))
	if strings.Contains(string(b), "idle") || !strings.Contains(string(b), `"theme": "dark"`) {
		t.Fatalf("settings file: %s", b)
	}
	// lastExportAt is the settings file's too (APP.md §13): absent until an
	// export, written as {vaultId, at}, and read back with the rest.
	if strings.Contains(string(b), "lastExportAt") {
		t.Fatalf("a stamp before any export: %s", b)
	}
	data := filepath.Join(h.dir, "data")
	file := loadSettings(data)
	file.LastExport = &lastExport{VaultID: "00112233445566778899aabbccddeeff", At: 1700000000}
	if err := saveSettings(data, file); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(data, "settings.json"))
	if !strings.Contains(string(b), `"lastExportAt"`) || !strings.Contains(string(b), `"vaultId"`) {
		t.Fatalf("the stamp was not written: %s", b)
	}
	back := loadSettings(data)
	if back.LastExport == nil || back.LastExport.At != 1700000000 || back.LastExport.VaultID != "00112233445566778899aabbccddeeff" {
		t.Fatalf("the stamp did not read back: %+v", back.LastExport)
	}
	if back.Theme != "dark" || back.RecoveryRecordPct != file.RecoveryRecordPct {
		t.Fatalf("the rest of the file changed: %+v", back)
	}
}

// TestCloseAction is the close question's answer (APP.md §2.4, ruled
// 2026-09-13): ask by default — the close button never means "to the tray"
// until the user has said so — those three values and nothing else, and a
// settings file an older build wrote, whose closeToTray key this one
// simply no longer reads, loading with everything beside it intact.
func TestCloseAction(t *testing.T) {
	dir := t.TempDir()
	if got := defaultSettings().CloseAction; got != CloseAsk {
		t.Fatalf("the default: %q", got)
	}
	if got := loadSettings(dir).CloseAction; got != CloseAsk {
		t.Fatalf("with no file at all: %q", got)
	}
	for _, v := range []string{CloseAsk, CloseTray, CloseQuit} {
		f := defaultSettings()
		f.CloseAction, f.DisplayName = v, "Personal"
		if err := saveSettings(dir, f); err != nil {
			t.Fatal(err)
		}
		if got := loadSettings(dir); got.CloseAction != v || got.DisplayName != "Personal" {
			t.Fatalf("round trip of %q: %+v", v, got)
		}
	}
	f := defaultSettings()
	f.CloseAction = "destroy"
	if err := saveSettings(dir, f); err != nil {
		t.Fatal(err)
	}
	if got := loadSettings(dir).CloseAction; got != CloseAsk {
		t.Fatalf("a value that is not one of the three: %q", got)
	}
	old := `{"vaultPath": "D:/Vaults/p.eks", "displayName": "Older", "closeToTray": "hide", "theme": "dark", "recoveryRecordPct": 5, "dictionaryBelow": 65536}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	got := loadSettings(dir)
	if got.CloseAction != CloseAsk || got.VaultPath != "D:/Vaults/p.eks" || got.DisplayName != "Older" || got.Theme != "dark" || got.RecoveryRecordPct != 5 || got.DictionaryBelow != 65536 {
		t.Fatalf("a settings file from before the close question: %+v", got)
	}

	// Through the core, the way the settings page saves and the way
	// Shell.CloseDecided remembers.
	h := newHarness(t, nil, nil)
	if v := h.c.GetSettings().CloseAction; v != CloseAsk {
		t.Fatalf("a fresh core: %q", v)
	}
	s := h.c.GetSettings()
	s.CloseAction = "destroy"
	if e := h.c.SetSettings(s); !isCode(e, CodeParams) {
		t.Fatalf("a close action that is not one of the three: %v", e)
	}
	s.CloseAction = CloseQuit
	if e := h.c.SetSettings(s); e != nil {
		t.Fatal(e)
	}
	if v := h.c.GetSettings().CloseAction; v != CloseQuit {
		t.Fatalf("get after set: %q", v)
	}
	if v := loadSettings(h.c.deps.DataDir).CloseAction; v != CloseQuit {
		t.Fatalf("the file after set: %q", v)
	}
}

// TestSetCloseActionMovesOneField is how Shell.CloseDecided remembers
// (APP.md §2.4): the close question's answer alone, written over the
// settings as they are — never a read-change-write of the whole snapshot
// from the shell, which would take the lock twice and could put back
// everything the page had saved in between.
func TestSetCloseActionMovesOneField(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.c.GetSettings()
	s.Theme, s.DisplayName, s.RecoveryRecordPct, s.DictionaryBelow = "dark", "Personal", 7, 1<<20
	if e := h.c.SetSettings(s); e != nil {
		t.Fatal(e)
	}
	if e := h.c.SetCloseAction("destroy"); !isCode(e, CodeParams) {
		t.Fatalf("a close action that is not one of the three: %v", e)
	}
	if e := h.c.SetCloseAction(CloseTray); e != nil {
		t.Fatal(e)
	}
	got := h.c.GetSettings()
	if got.CloseAction != CloseTray {
		t.Fatalf("the answer was not kept: %q", got.CloseAction)
	}
	if got.Theme != "dark" || got.DisplayName != "Personal" || got.RecoveryRecordPct != 7 || got.DictionaryBelow != 1<<20 {
		t.Fatalf("something beside the close action moved: %+v", got)
	}
	// The file, not only the memory: the next launch reads this.
	file := loadSettings(h.c.deps.DataDir)
	if file.CloseAction != CloseTray || file.Theme != "dark" || file.DisplayName != "Personal" || file.RecoveryRecordPct != 7 || file.DictionaryBelow != 1<<20 {
		t.Fatalf("the settings file: %+v", file)
	}
	// A stamp another writer put down between two of these is not put back
	// by them: only the fields a call owns are applied after the write.
	h.c.recordExport()
	if e := h.c.SetCloseAction(CloseQuit); e != nil {
		t.Fatal(e)
	}
	if got := h.c.GetSettings(); got.CloseAction != CloseQuit {
		t.Fatalf("the second answer: %q", got.CloseAction)
	}
	if h.c.LastExportAt() == 0 {
		t.Fatal("the export stamp was dropped by a close-action write")
	}
}

// TestSettingsRefuseLeaveMemoryAlone is the commit-then-apply rule: a
// settings file that cannot be written is refused with the settings in
// memory exactly as they were. It matters most for the close question —
// the shell's gate reads CloseAction the moment the window closes, and a
// remembered answer whose write was refused must not be obeyed while the
// page still shows the old one.
func TestSettingsRefuseLeaveMemoryAlone(t *testing.T) {
	h := newHarness(t, nil, nil)
	s := h.c.GetSettings()
	s.Theme, s.DisplayName, s.RecoveryRecordPct = "dark", "Personal", 7
	if e := h.c.SetSettings(s); e != nil {
		t.Fatal(e)
	}
	before := h.c.GetSettings()
	fileBefore := loadSettings(h.c.deps.DataDir)

	// The seam saveSettings has: it writes settings.json.tmp and renames
	// it. A directory at that path fails the write every time, on every
	// platform, without touching settings.json.
	blocked := filepath.Join(h.c.deps.DataDir, "settings.json.tmp")
	if err := os.MkdirAll(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	bad := before
	bad.Theme, bad.DisplayName, bad.RecoveryRecordPct, bad.CloseAction = "light", "Renamed", 11, CloseQuit
	if e := h.c.SetSettings(bad); e == nil {
		t.Fatal("a settings file that could not be written was not refused")
	}
	if got := h.c.GetSettings(); got.Theme != before.Theme || got.DisplayName != before.DisplayName ||
		got.RecoveryRecordPct != before.RecoveryRecordPct || got.CloseAction != before.CloseAction {
		t.Fatalf("a refused save moved the settings in memory: %+v, was %+v", got, before)
	}
	if e := h.c.SetCloseAction(CloseTray); e == nil {
		t.Fatal("a close action that could not be written was not refused")
	}
	if got := h.c.GetSettings().CloseAction; got != before.CloseAction {
		t.Fatalf("a refused close action moved the settings in memory: %q", got)
	}
	if got := loadSettings(h.c.deps.DataDir); got.Theme != fileBefore.Theme || got.CloseAction != fileBefore.CloseAction || got.DisplayName != fileBefore.DisplayName {
		t.Fatalf("a refused save changed the file: %+v", got)
	}

	// With the way clear again, both write.
	if err := os.Remove(blocked); err != nil {
		t.Fatal(err)
	}
	if e := h.c.SetCloseAction(CloseTray); e != nil {
		t.Fatal(e)
	}
	if got := h.c.GetSettings(); got.CloseAction != CloseTray || got.Theme != "dark" {
		t.Fatalf("after the way was clear: %+v", got)
	}
	if got := loadSettings(h.c.deps.DataDir); got.CloseAction != CloseTray {
		t.Fatalf("the file after the way was clear: %+v", got)
	}
}

func TestEnrollPasswordAndRemove(t *testing.T) {
	h := newHarness(t, nil, nil)
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "Second"}); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("enroll while locked: %v", e)
	}
	h.unlockWithPassword()
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "Second"}); e != nil {
		t.Fatal(e)
	}
	// First the current password (the VMK), then the new one.
	p1 := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p1.PromptID, testPassword)
	p2 := h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != p1.PromptID
	}).(CeremonyState)
	h.c.SubmitSecret("password", p2.PromptID, "second password")
	h.rec.waitCeremony(t, StepDone, false)
	slots := h.c.Slots()
	if len(slots) != 3 {
		t.Fatalf("slots: %+v", slots)
	}
	var second string
	for _, s := range slots {
		if s.Label == "Second" {
			second = s.RecipientID
		}
	}
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("enrollment locked the vault: %s", st.State)
	}
	h.rec.reset()
	if e := h.c.RemoveSlot(second); e != nil {
		t.Fatal(e)
	}
	p3 := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p3.PromptID, testPassword)
	h.rec.waitCeremony(t, StepDone, false)
	if slots := h.c.Slots(); len(slots) != 2 {
		t.Fatalf("after remove: %+v", slots)
	}
}

func TestCreateVaultShowsRecoveryOnce(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	c, err := New(Deps{Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetAppOrigin("wails://wails")
	if st := c.Status(); st.State != StateNone {
		t.Fatalf("fresh core: %s", st.State)
	}
	vault := filepath.Join(dir, "new.eks")
	if e := c.CreateVault(vault, "Mine", EnrollOptions{Kind: EnrollPassword, Label: "Password"}, false); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a new password")
	final := rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked) // a create ends in the vault, the key shown over it
	if !strings.HasPrefix(final.SlotLabel, "http://127.0.0.1:") {
		t.Fatalf("no one-time URL: %+v", final)
	}
	if staged(dir) {
		t.Fatal("the incoming file was left beside the vault")
	}
	if st := c.Status(); st.Ceremony != nil && st.Ceremony.Step != StepRecovery {
		t.Fatalf("ceremony after create: %+v", st.Ceremony)
	}
	// Wrong origin: nothing; right origin: the digits, once.
	resp, _ := http.Get(final.SlotLabel)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("foreign origin got %d", resp.StatusCode)
	}
	req, _ := http.NewRequest("GET", final.SlotLabel, nil)
	req.Header.Set("Origin", "wails://wails")
	resp, _ = http.DefaultClient.Do(req)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("secret survived the foreign fetch: %d %q", resp.StatusCode, body)
	}
	// Fresh vault, fresh secret, fetched properly this time — after a lock
	// and an unlock with the way in that was chosen.
	c.Lock()
	rec.waitState(t, StateLocked)
	rec.reset()
	if e := c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Paper"}); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("enroll while locked: %v", e)
	}
	c.BeginUnlock(MethodPassword)
	p = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a new password")
	rec.waitCeremony(t, StepDone, false)
	rec.waitState(t, StateUnlocked)
	if e := c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "Paper"}); e != nil {
		t.Fatal(e)
	}
	p = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a new password")
	shown := rec.waitCeremony(t, StepRecovery, false)
	req, _ = http.NewRequest("OPTIONS", shown.SlotLabel, nil)
	req.Header.Set("Origin", "wails://wails")
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	req, _ = http.NewRequest("GET", shown.SlotLabel, nil)
	req.Header.Set("Origin", "wails://wails")
	resp, _ = http.DefaultClient.Do(req)
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(body) < 20 || resp.Header.Get("Access-Control-Allow-Origin") != "wails://wails" {
		t.Fatalf("digits: %d %q", resp.StatusCode, body)
	}
	resp, _ = http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("second fetch got %d", resp.StatusCode)
	}
	if e := c.BeginUnlock(MethodRecovery); e != nil {
		t.Fatalf("recovery begin: %v", e)
	}
	// And the digits open the vault.
	c.Lock()
	rec.waitState(t, StateLocked)
	c.BeginUnlock(MethodRecovery)
	p = rec.waitCeremony(t, StepRecovery, true)
	c.SubmitSecret("recovery", p.PromptID, string(body))
	rec.waitCeremony(t, StepDone, false)
	rec.waitState(t, StateUnlocked)
}

func TestOpenVaultErrors(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	c, _ := New(Deps{Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	c.Start()
	defer c.Close()
	if e := c.OpenVaultFile(filepath.Join(dir, "missing.eks"), "x"); !isCode(e, CodeVaultNotFound) {
		t.Fatalf("missing: %v", e)
	}
	bad := filepath.Join(dir, "bad.eks")
	os.WriteFile(bad, []byte("not a keystore at all, just bytes"), 0o600)
	if e := c.OpenVaultFile(bad, "x"); e == nil || e.Code == CodeVaultNotFound {
		t.Fatalf("garbage: %v", e)
	}
	if e := c.BeginUnlock(MethodPassword); !isCode(e, CodeNoVault) {
		t.Fatalf("unlock with no vault: %v", e)
	}
}
