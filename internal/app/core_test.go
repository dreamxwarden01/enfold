package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	touch := h.rec.waitCeremony(t, StepTouch, false)
	if touch.N != 2 || !touch.PINAsked {
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
	if _, e := h.c.CreateArchive("relative.enf", "Photos", false); !isCode(e, CodeParams) {
		t.Fatalf("relative path: %v", e)
	}
	id, e := h.c.CreateArchive(ap, "Photos", false)
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
	if st.Files != 0 || st.State != "open" || !st.SessionAlive {
		t.Fatalf("stat: %+v", st)
	}

	// Stage two files and a folder, then look at the projection.
	src := filepath.Join(h.dir, "src")
	os.MkdirAll(filepath.Join(src, "sub"), 0o700)
	os.WriteFile(filepath.Join(src, "a.txt"), []byte("hello archive\n"), 0o600)
	os.WriteFile(filepath.Join(src, "b.txt"), []byte(strings.Repeat("b", 5000)), 0o600)
	os.WriteFile(filepath.Join(src, "sub", "c.txt"), []byte("nested"), 0o600)
	opID, e := h.c.AddFiles(id, "", []string{filepath.Join(src, "a.txt"), filepath.Join(src, "b.txt")}, PolicySkip)
	if e != nil {
		t.Fatalf("add: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" || len(o.Results) != 2 {
		t.Fatalf("add op: %+v", o)
	}
	opID, e = h.c.AddFolder(id, "docs", filepath.Join(src, "sub"), PolicySkip)
	if e != nil {
		t.Fatalf("add folder: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("add folder op: %+v", o)
	}
	page, e := h.c.Page(id, "", "name", 0, 100)
	if e != nil {
		t.Fatalf("page: %v", e)
	}
	names := map[string]FileRow{}
	for _, r := range page.Rows {
		names[r.Name] = r
	}
	if len(page.Rows) != 3 || names["a.txt"].Pending != "added" || !names["docs"].IsFolder {
		t.Fatalf("staged page: %+v", page.Rows)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 3 || st.State != "dirty" || st.CapAt == 0 {
		t.Fatalf("dirty stat: %+v", st)
	}
	// A second add of the same name is a collision under skip.
	col, e := h.c.CheckNames(id, "", []string{"a.txt", "new.txt"})
	if e != nil || len(col) != 1 || col[0].Name != "a.txt" || !col[0].Pending {
		t.Fatalf("collisions: %+v %v", col, e)
	}
	// Preview of a staged file is refused (not committed yet).
	if _, e := h.c.PreviewURL(id, names["a.txt"].FileID); e == nil {
		t.Fatal("preview of a staged file")
	}

	opID, e = h.c.Save(id)
	if e != nil {
		t.Fatalf("save: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("save op: %+v", o)
	}
	st, _ = h.c.Stat(id)
	if st.Dirty != 0 || st.State != "open" || st.Files != 3 || st.CapAt != 0 {
		t.Fatalf("after save: %+v", st)
	}
	list, _ = h.c.ListArchives(false)
	if list[0].Files != 3 || list[0].ReceiptOwed || list[0].LastWrittenAt == 0 {
		t.Fatalf("list after save: %+v", list[0])
	}
	// A dropped folder keeps its own name under the target folder.
	page, _ = h.c.Page(id, "docs", "name", 0, 100)
	if len(page.Rows) != 1 || !page.Rows[0].IsFolder || page.Rows[0].Name != "sub" || page.Rows[0].Files != 1 {
		t.Fatalf("docs page: %+v", page.Rows)
	}
	page, _ = h.c.Page(id, "docs/sub", "name", 0, 100)
	if len(page.Rows) != 1 || page.Rows[0].Path != "docs/sub/c.txt" || page.Rows[0].Pending != "" {
		t.Fatalf("docs/sub page: %+v", page.Rows)
	}
	cID := page.Rows[0].FileID
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

	// Rename a leaf, delete one, save, then extract everything.
	page, _ = h.c.Page(id, "", "name", 0, 100)
	for _, r := range page.Rows {
		names[r.Name] = r
	}
	if e := h.c.RenameFile(id, names["a.txt"].FileID, "x/a.txt"); !isCode(e, CodeFileName) {
		t.Fatalf("rename with a slash: %v", e)
	}
	if e := h.c.RenameFile(id, names["a.txt"].FileID, "a2.txt"); e != nil {
		t.Fatalf("rename: %v", e)
	}
	if e := h.c.DeleteFiles(id, []string{names["b.txt"].FileID}); e != nil {
		t.Fatalf("delete: %v", e)
	}
	opID, _ = h.c.Save(id)
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("save 2: %+v", o)
	}
	page, _ = h.c.Page(id, "", "name", 0, 100)
	var all []string
	for _, r := range page.Rows {
		if !r.IsFolder {
			all = append(all, r.FileID)
		}
	}
	page2, _ := h.c.Page(id, "docs/sub", "name", 0, 100)
	all = append(all, page2.Rows[0].FileID)
	// Extracted files go under GOTMPDIR when it is set: on the dev machine
	// that directory is excluded from the antivirus, whose scan of a fresh
	// file otherwise holds it open while the temp dir is being removed.
	out, err := os.MkdirTemp(os.Getenv("GOTMPDIR"), "enfold-extract")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(out)
	opID, e = h.c.Extract(id, all, out, ExtractSkip)
	if e != nil {
		t.Fatalf("extract: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" || len(o.Results) != 2 {
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

	// Lock: the archive stays open but Save needs the session.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	if st, e := h.c.Stat(id); e != nil || st.SessionAlive {
		t.Fatalf("stat while locked: %+v %v", st, e)
	}
	if _, e := h.c.Save(id); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("save while locked: %v", e)
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

func TestArchiveIdleClockAndDirtyCap(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	// Clean and idle: the archive closes itself, silently, after the
	// session's idle span (the timers fire inside Advance).
	h.clk.Advance(defaultIdle + time.Second)
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("clean idle archive still open: %v", e)
	}
	h.rec.waitState(t, StateLocked) // the session's own idle clock fired too

	// Dirty: never closed silently. Two expiring notices, then it holds
	// until the cap discards the changes.
	h.unlockWithPassword()
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("x"), 0o600)
	opID, _ := h.c.AddFiles(id, "", []string{f}, PolicySkip)
	h.rec.waitOp(t, opID)
	st, _ := h.c.Stat(id)
	if st.Dirty != 1 || st.CapAt == 0 || st.ExpiresAt == 0 {
		t.Fatalf("dirty: %+v", st)
	}
	dirtyAt := h.clk.Now()
	h.clk.Advance(defaultIdle + time.Second)
	ev := h.rec.waitFor(t, EventArchiveExpiring, func(p any) bool { return true }).(ArchiveExpiring)
	if ev.ID != id || ev.Dirty != 1 || ev.ClosesAt != h.clk.Now().Add(archiveExtension).Unix() {
		t.Fatalf("expiring: %+v", ev)
	}
	h.clk.Advance(archiveExtension + time.Second)
	h.rec.waitFor(t, EventArchiveExpiring, func(p any) bool { return true })
	h.clk.Advance(archiveExtension + time.Second)
	if st, e := h.c.Stat(id); e != nil || st.Dirty != 1 || st.ExpiresAt != 0 {
		t.Fatalf("after the extensions: %+v %v", st, e)
	}
	if e := h.c.KeepOpen(id); e != nil {
		t.Fatalf("keep open: %v", e)
	}
	if st, _ := h.c.Stat(id); st.ExpiresAt == 0 {
		t.Fatal("KeepOpen did not rearm the idle clock")
	}
	// The cap: dirtySince + the absolute span.
	h.clk.Advance(dirtyAt.Add(defaultAbsolute).Sub(h.clk.Now()) + time.Second)
	h.rec.waitFor(t, EventVaultWarning, func(p any) bool {
		w, ok := p.(Warning)
		return ok && w.Code == "archive.changes_discarded"
	})
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("dirty cap did not close the archive: %v", e)
	}
	// The abort left the file at its last commit: empty.
	h.unlockWithPassword()
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	if st, _ := h.c.Stat(id); st.Files != 0 {
		t.Fatalf("aborted change was committed: %+v", st)
	}
}

func TestShutdownCommitsDirtyArchives(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	h.c.OpenArchive(id)
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("saved at exit"), 0o600)
	opID, _ := h.c.AddFiles(id, "", []string{f}, PolicySkip)
	h.rec.waitOp(t, opID)
	h.c.ResolveForShutdown(5 * time.Second)
	if st := h.status(); st.State != StateLocked || st.OpenArchives != 0 {
		t.Fatalf("after shutdown: %+v", st)
	}
	h.unlockWithPassword()
	list, _ := h.c.ListArchives(false)
	if len(list) != 1 || list[0].ReceiptOwed || list[0].Files != 0 {
		t.Fatalf("list: %+v", list)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	if st, _ := h.c.Stat(id); st.Files != 1 {
		t.Fatalf("shutdown did not commit: %+v", st)
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
	rec.waitState(t, StateLocked) // every create ends locked: the build was installed
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
	// Fresh vault, fresh secret, fetched properly this time.
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
