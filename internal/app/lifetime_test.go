package app

import (
	"net/http"
	"sync"
	"testing"
	"time"
)

// The open archive's lifetime (APP.md §2.3, DESIGN.md §10, ruled
// 2026-09-10): it has no timeout of its own, it stays open while its page is
// shown, leaving the page closes it at once unless a reader still holds it,
// *Close archive* is the kill switch that drops the readers too, and the
// Archives page's own operations never ask for the archive to be opened
// first.

// holdReader stands in for a body the preview transport is streaming: the
// count preview.go keeps for as long as its handler runs (APP.md §4). The
// release below is the transport's own, so what is proved here is the rule
// and not the fake.
func (h *harness) holdReader(t *testing.T, id string) *openArchive {
	t.Helper()
	aid, ok := parseID(id)
	if !ok {
		t.Fatalf("bad id %q", id)
	}
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	oa := h.c.archives[aid]
	if oa == nil {
		t.Fatal("the archive is not open")
	}
	oa.readers++
	return oa
}

// getStatus fetches a preview URL and reports the status code.
func getStatus(t *testing.T, url string) int {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// Leaving the page closes the archive at once and destroys its keys: nothing
// waits for a clock, and the preview token goes with the handle.
func TestLeavingThePageClosesTheArchiveAtOnce(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Left")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	url, e := h.c.PreviewURL(id, h.row(t, id, rootID, "a.txt").ID)
	if e != nil {
		t.Fatal(e)
	}
	if code := getStatus(t, url); code != 200 {
		t.Fatalf("preview before the leave: %d", code)
	}

	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatalf("leave: %v", e)
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("the archive was left and stayed open: %v", e)
	}
	if code := getStatus(t, url); code != 404 {
		t.Fatalf("the token outlived the archive: %d", code)
	}
	// A page that leaves an archive it has already closed is not an error.
	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatalf("leaving a closed archive: %v", e)
	}
	// And the list says so.
	list, _ := h.c.ListArchives(false)
	if len(list) != 1 || list[0].Open {
		t.Fatalf("list after the leave: %+v", list)
	}
}

// isHeld reports whether the core still holds a handle on the archive —
// open, draining or closing — which the page's own Stat can no longer tell,
// since a handle the page does not hold answers archive.not_open.
func (h *harness) isHeld(id string) bool {
	aid, _ := parseID(id)
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	return h.c.archives[aid] != nil
}

// An archive left with a reader still on it is draining (APP.md §2.3, §4):
// the handle stays for the body in flight and nothing else — the token
// admits no new request, every page-facing method answers archive.not_open,
// and no clock takes it away, which the core says in the log rather than
// inventing one — and it closes the moment the last reader ends. An Open
// meanwhile mounts it again, and the page has it back.
func TestALeftArchiveDrainsForItsReaders(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Reading")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	row := h.row(t, id, rootID, "a.txt")
	url, e := h.c.PreviewURL(id, row.ID)
	if e != nil {
		t.Fatal(e)
	}
	oa := h.holdReader(t, id)

	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatalf("leave: %v", e)
	}
	if !h.isHeld(id) {
		t.Fatal("the archive was closed under a live reader")
	}
	if !h.logged("no timeout of its own") {
		t.Fatal("the core did not say that the draining archive has no timeout")
	}
	// Draining admits no new request: not a page, not a stat, not a preview
	// over the token that was minted before the leave.
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("stat of a draining archive: %v", e)
	}
	if _, e := h.c.Page(id, rootID, "name", 0, 10); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("page of a draining archive: %v", e)
	}
	if _, e := h.c.PreviewURL(id, row.ID); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("a new preview URL of a draining archive: %v", e)
	}
	if _, _, e := h.c.PreviewText(id, row.ID, 16); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("a preview text of a draining archive: %v", e)
	}
	if code := getStatus(t, url); code != 404 {
		t.Fatalf("the token admitted a new request while draining: %d", code)
	}
	if _, e := h.c.CreateFolder(id, rootID, "F"); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("an operation on a draining archive: %v", e)
	}
	// No clock takes it away either, however long the reader reads: well
	// past what used to be the archive's idle deadline, the session kept
	// alive so that the remount below has what an Open needs.
	for i := 0; i < 6; i++ {
		h.clk.Advance(5 * time.Minute)
		h.c.Activity()
	}
	if !h.isHeld(id) {
		t.Fatal("a clock closed the archive kept open for its reader")
	}
	// The page comes back: an Open mounts the draining handle again, and
	// the archive is the page's — the reader's end no longer closes it.
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatalf("reopening a draining archive: %v", e)
	}
	if st, e := h.c.Stat(id); e != nil || st.Files != 1 {
		t.Fatalf("stat after the remount: %+v %v", st, e)
	}
	if code := getStatus(t, url); code != 200 {
		t.Fatalf("the token after the remount: %d", code)
	}
	h.c.releaseReader(oa)
	if _, e := h.c.Stat(id); e != nil {
		t.Fatalf("the reader's end closed the page's archive: %v", e)
	}
	// And left again with nothing in flight, it closes at once.
	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatal(e)
	}
	if h.isHeld(id) {
		t.Fatal("the archive stayed open after the second leave")
	}
	if code := getStatus(t, url); code != 404 {
		t.Fatalf("the token outlived the archive: %d", code)
	}
}

// The last body's end closes a draining archive: the handle and its keys go
// with the reader (APP.md §2.3, §4).
func TestTheLastReaderClosesADrainingArchive(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Drained")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	first, second := h.holdReader(t, id), h.holdReader(t, id)
	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatalf("leave: %v", e)
	}
	h.c.releaseReader(first)
	if !h.isHeld(id) {
		t.Fatal("the archive closed under the second reader")
	}
	h.c.releaseReader(second)
	if h.isHeld(id) {
		t.Fatal("the last reader ended and the archive stayed open")
	}
	list, _ := h.c.ListArchives(false)
	if len(list) != 1 || list[0].Open {
		t.Fatalf("the list after the drain: %+v", list)
	}
}

// Destroying the window leaves every page (APP.md §2.3, §2.4): the shell
// calls LeaveAllArchives as the window goes, and each archive closes, or
// drains while a body is in flight, exactly as if its own page had been
// left.
func TestLeaveAllArchivesClosesOrDrainsEachOne(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	idle := h.openArchive(t, "Idle")
	busy := h.openArchive(t, "Busy")
	h.add(t, busy, rootID, PolicySkip, h.src(t, "b.txt", "b"))
	oa := h.holdReader(t, busy)

	h.c.LeaveAllArchives()
	if h.isHeld(idle) {
		t.Fatal("the idle archive stayed open after the window went")
	}
	if !h.isHeld(busy) {
		t.Fatal("the archive with a body in flight was closed under it")
	}
	if _, e := h.c.Stat(busy); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("the draining archive still answered its page: %v", e)
	}
	// A second LeaveAll finds nothing mounted and changes nothing.
	h.c.LeaveAllArchives()
	if !h.isHeld(busy) {
		t.Fatal("a second LeaveAll closed the draining archive under its reader")
	}
	h.c.releaseReader(oa)
	if h.isHeld(busy) {
		t.Fatal("the last body ended and the drained archive stayed open")
	}
	if st := h.status(); st.OpenArchives != 0 {
		t.Fatalf("open archives after the window went: %d", st.OpenArchives)
	}
}

// Close archive never waits behind an add (APP.md §2.3): while an add is
// still running, the kill switch drops the token, answers every page method
// archive.not_open and cancels the operation; the handle closes as soon as
// the cancelled operation has let go, and the add ends cancelled with
// nothing published.
func TestCloseNeverWaitsBehindARunningAdd(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Killed")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	url, e := h.c.PreviewURL(id, h.row(t, id, rootID, "a.txt").ID)
	if e != nil {
		t.Fatal(e)
	}
	big := h.src(t, "big.bin", string(incompressible(t, 8<<20)))

	// The add is stopped inside a progress event of its own — on its own
	// goroutine, holding opMu and its transaction, some way into the file —
	// so that Close is proved to land while it runs.
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.rec.onEvent(func(name string, payload any) {
		o, ok := payload.(OpView)
		if !ok || name != EventOpProgress || o.Kind != "add" || o.Done == 0 {
			return
		}
		once.Do(func() {
			close(held)
			<-release
		})
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.AddFiles(id, rootID, []string{big}, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	<-held

	closed := make(chan *Error, 1)
	go func() { closed <- h.c.CloseArchive(id) }()
	// With the add still held, the kill has already landed: the token is
	// dead, the page is answered archive.not_open, the operation is
	// cancelled — and Close has not returned, since the add still holds the
	// handle.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, e := h.c.Stat(id); isCode(e, CodeArchiveNotOpen) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the kill switch waited behind the running add")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if code := getStatus(t, url); code != 404 {
		t.Fatalf("the token survived the kill switch: %d", code)
	}
	select {
	case e := <-closed:
		t.Fatalf("Close returned while the add still held the handle: %v", e)
	case <-time.After(50 * time.Millisecond):
	}
	if e := h.c.CancelOp(opID); e != nil {
		t.Fatalf("cancel of an operation on a closing archive: %v", e)
	}
	// The add resumes, finds itself cancelled at its next chunk, aborts and
	// lets go; Close then returns at once.
	close(release)
	select {
	case e := <-closed:
		if e != nil {
			t.Fatalf("close: %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return once the cancelled add had let go")
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeOpCancelled {
		t.Fatalf("the add under the kill switch: %+v", o)
	}
	if h.isHeld(id) {
		t.Fatal("the archive is still held after the kill switch")
	}
	if code := getStatus(t, url); code != 404 {
		t.Fatalf("the token after the close: %d", code)
	}
	// Nothing was published: the archive reopens with the one file it had.
	st, e := h.c.OpenArchive(id)
	if e != nil || st.Files != 1 {
		t.Fatalf("reopen after the kill switch: %+v %v", st, e)
	}
}

// Two openers of a closed archive — an Archives-page operation and the page
// — end with one handle (APP.md §2.3): the first installs an opening
// reservation, the second waits for it and joins, and the archive layer's
// one-handle-per-path rule is never tripped.
func TestConcurrentFirstOpensShareOneHandle(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Raced")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 40; i++ {
		var wg sync.WaitGroup
		var pageErr, opErr *Error
		var oa *openArchive
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, pageErr = h.c.OpenArchive(id)
		}()
		go func() {
			defer wg.Done()
			oa, opErr = h.c.acquireForOperation(id)
		}()
		wg.Wait()
		if pageErr != nil || opErr != nil {
			t.Fatalf("round %d: the page %v, the operation %v", i, pageErr, opErr)
		}
		if st, e := h.c.Stat(id); e != nil || st.Files != 1 {
			t.Fatalf("round %d: stat after the joined open: %+v %v", i, st, e)
		}
		// The operation's claim goes; the page still holds it, so it stays.
		h.c.releaseClaim(oa)
		if !h.isHeld(id) {
			t.Fatalf("round %d: the claim's release closed the page's archive", i)
		}
		if e := h.c.LeaveArchive(id); e != nil {
			t.Fatal(e)
		}
		if h.isHeld(id) {
			t.Fatalf("round %d: the archive stayed open after the leave", i)
		}
	}
}

// A lock never hands a page an archive it did not have (APP.md §2.3, "What
// stays usable after a lock"): a handle the core opened for an Archives-page
// operation is not mounted by an Open while the vault is locked —
// vault.needs_unlock, the handle left unmounted and closed by the operation's
// end — while an archive whose page was open before the lock stays usable.
func TestALockNeverMountsAHandleThePageDidNotHave(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	mine := h.openArchive(t, "Mine")
	h.add(t, mine, rootID, PolicySkip, h.src(t, "m.txt", "m"))
	other := h.openArchive(t, "Other")
	h.add(t, other, rootID, PolicySkip, h.src(t, "o.txt", "o"))
	if e := h.c.CloseArchive(other); e != nil {
		t.Fatal(e)
	}

	// A verify of the closed archive, held inside its first progress event
	// so that its handle — the core's own, unmounted — stands while the
	// vault locks.
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.rec.onEvent(func(name string, payload any) {
		o, ok := payload.(OpView)
		if !ok || name != EventOpProgress || o.Kind != "verify" {
			return
		}
		once.Do(func() {
			close(held)
			<-release
		})
	})
	defer h.rec.onEvent(nil)
	opID, e := h.c.Verify(other)
	if e != nil {
		t.Fatal(e)
	}
	<-held
	h.c.Lock()
	h.rec.waitState(t, StateLocked)

	if _, e := h.c.OpenArchive(other); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("a locked Open mounted the operation's handle: %v", e)
	}
	if _, e := h.c.Stat(other); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("the operation's handle answered the page under the lock: %v", e)
	}
	// The page that was open before the lock is untouched.
	if st, e := h.c.Stat(mine); e != nil || st.Files != 1 {
		t.Fatalf("the open archive after the lock: %+v %v", st, e)
	}
	if _, e := h.c.Page(mine, rootID, "name", 0, 10); e != nil {
		t.Fatalf("the open archive's page after the lock: %v", e)
	}
	close(release)
	// The verify ends as it ends — its registry write needs the session and
	// answers as it always did — and what matters here is what it leaves.
	o := h.rec.waitOp(t, opID)
	t.Logf("the verify under the lock ended with %q", o.Error)
	// The refused Open left nothing behind: the operation's end closed the
	// handle it had opened for itself.
	if h.isHeld(other) {
		t.Fatal("the operation's handle outlived the operation")
	}
	if _, e := h.c.OpenArchive(other); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("a locked Open of a closed archive: %v", e)
	}
	// Unlocked, the page opens it as ever.
	h.unlockWithPassword()
	if st, e := h.c.OpenArchive(other); e != nil || st.Files != 1 {
		t.Fatalf("open after the unlock: %+v %v", st, e)
	}
}

// Close archive is the kill switch: it closes now, readers or not, and the
// token goes with the handle (APP.md §2.3, §4).
func TestCloseDropsTheReaders(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Killed")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	url, e := h.c.PreviewURL(id, h.row(t, id, rootID, "a.txt").ID)
	if e != nil {
		t.Fatal(e)
	}
	h.holdReader(t, id)

	if e := h.c.CloseArchive(id); e != nil {
		t.Fatalf("close: %v", e)
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("the kill switch left the archive open: %v", e)
	}
	if code := getStatus(t, url); code != 404 {
		t.Fatalf("the token survived the kill switch: %d", code)
	}
}

// The Archives page's operations never ask for the archive to be opened
// first (APP.md §2.3, ruled 2026-09-10): while the vault is unlocked they
// unwrap the key through the session, open the file for the operation, and
// close it — the key destroyed — when the operation ends. A session that is
// not live answers as it always did.
func TestArchivesPageOperationsOpenTheArchiveThemselves(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Closed")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}

	closedAfter := func(what string) {
		t.Helper()
		if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
			t.Fatalf("%s left the archive open: %v", what, e)
		}
		list, _ := h.c.ListArchives(false)
		if len(list) != 1 || list[0].Open {
			t.Fatalf("the list after %s: %+v", what, list)
		}
	}

	opID, e := h.c.Verify(id)
	if e != nil {
		t.Fatalf("verify on a closed archive: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("verify: %+v", o)
	}
	if rec := h.record(id); rec.HashAtSeq != rec.LastSeq {
		t.Fatalf("the verify did not refresh the hash: %+v", rec)
	}
	closedAfter("a verify")

	opID, e = h.c.Compact(id)
	if e != nil {
		t.Fatalf("compact on a closed archive: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("compact: %+v", o)
	}
	closedAfter("a compaction")

	opID, e = h.c.RotateKey(id)
	if e != nil {
		t.Fatalf("rotate on a closed archive: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("rotate: %+v", o)
	}
	if rec := h.record(id); len(rec.Versions) != 2 {
		t.Fatalf("the rotation did not publish a version: %+v", rec.Versions)
	}
	closedAfter("a rotation")

	// The work is all there when the page opens the archive again.
	st, e := h.c.OpenArchive(id)
	if e != nil || st.Files != 1 {
		t.Fatalf("reopen after the operations: %+v %v", st, e)
	}
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}

	// Locked, the unwrap has nothing to work with and the answer is the
	// session's, as it always was.
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	if _, e := h.c.Verify(id); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("verify while locked: %v", e)
	}
	if _, e := h.c.Compact(id); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("compact while locked: %v", e)
	}
	if _, e := h.c.RotateKey(id); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("rotate while locked: %v", e)
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("a refused operation left a handle behind: %v", e)
	}
}

// A page that opens the archive while such an operation runs joins that
// handle — the archive layer's one handle per path is never tripped — and
// the archive stays open afterwards: the operation closes only what nothing
// else holds (APP.md §2.3).
func TestAPageJoinsAnOperationsHandle(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Joined")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}

	// The page opens the archive from inside the operation's first progress
	// event: the handle is the core's own by then, and the Open joins it.
	var once sync.Once
	joined := make(chan *Error, 1)
	h.rec.onEvent(func(name string, payload any) {
		o, ok := payload.(OpView)
		if !ok || name != EventOpProgress || o.Kind != "verify" {
			return
		}
		once.Do(func() {
			_, e := h.c.OpenArchive(id)
			joined <- e
		})
	})
	opID, e := h.c.Verify(id)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("verify: %+v", o)
	}
	h.rec.onEvent(nil)
	if e := <-joined; e != nil {
		t.Fatalf("the page could not join the operation's handle: %v", e)
	}
	st, e := h.c.Stat(id)
	if e != nil {
		t.Fatalf("the operation closed the archive under the page: %v", e)
	}
	if st.Files != 1 {
		t.Fatalf("stat after the join: %+v", st)
	}
	// And it is the page's now: leaving closes it.
	if e := h.c.LeaveArchive(id); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.Stat(id); !isCode(e, CodeArchiveNotOpen) {
		t.Fatalf("the joined archive stayed open after the leave: %v", e)
	}
}
