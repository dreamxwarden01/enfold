package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
)

// Opening an archive from Explorer (APP.md §14): the file's plaintext
// envelope names the archive, the record is found by that id and never by
// the name, and the record's last_path follows the file that has just
// proved where it is.

// recordPath is a record's last_path, as the Archives list reports it.
func (h *harness) recordPath(id string) string {
	h.t.Helper()
	list, e := h.c.ListArchives(true)
	if e != nil {
		h.t.Fatalf("list: %v", e)
	}
	for _, a := range list {
		if a.ID == id {
			return a.Path
		}
	}
	h.t.Fatalf("no record %s in the list", id)
	return ""
}

// strayEnvelope writes a file that is a well-formed Enfold archive head
// for an archive_id no vault here knows: enough for the envelope reader,
// which is all OpenPath reads before it looks the record up.
func strayEnvelope(t *testing.T, path string, id [16]byte) string {
	t.Helper()
	env := format.Envelope{ArchiveID: id, KID: [16]byte{0xAA}}
	if err := os.WriteFile(path, env.Encode(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// A file that moved still opens — the id in the envelope is what the
// record is found by — and the record follows it, which is the write
// Locate… makes.
func TestOpenPathOpensTheRecordAndAdoptsTheFilesPlace(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	was := filepath.Join(h.dir, "holiday.efd")
	id, e := h.c.CreateArchive(was, "Holiday", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	now := filepath.Join(h.dir, "moved", "renamed.efd")
	if err := os.MkdirAll(filepath.Dir(now), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(was, now); err != nil {
		t.Fatal(err)
	}
	r, e := h.c.OpenPath(now)
	if e != nil {
		t.Fatalf("open path: %v", e)
	}
	if r.ArchiveID != id {
		t.Fatalf("opened %s, want the record %s the envelope names", r.ArchiveID, id)
	}
	if !r.Relocated {
		t.Fatal("the record was not moved to the file's new place")
	}
	if got := h.recordPath(id); !samePlace(got, now) {
		t.Fatalf("last_path is %s, want %s", got, now)
	}
	// And it is open, as a double-click of the row leaves it.
	if st := h.stat(t, id); st.ID != id {
		t.Fatalf("stat after the open: %+v", st)
	}
}

// The file where the record already says it is: nothing is written, and
// the archive opens all the same.
func TestOpenPathAtTheRecordsOwnPathWritesNothing(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	p := filepath.Join(h.dir, "here.efd")
	id, e := h.c.CreateArchive(p, "Here", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	before := h.recordPath(id)
	r, e := h.c.OpenPath(p)
	if e != nil {
		t.Fatalf("open path: %v", e)
	}
	if r.ArchiveID != id || r.Relocated {
		t.Fatalf("answered %+v, want %s and no move", r, id)
	}
	if got := h.recordPath(id); got != before {
		t.Fatalf("last_path became %s, want it left at %s", got, before)
	}
	if st := h.stat(t, id); st.ID != id {
		t.Fatalf("stat after the open: %+v", st)
	}
}

// A locked vault answers the code every other call of one answers: the
// page draws the lock scene, keeps the path and asks again after the
// unlock. The file is not opened and nothing is written.
func TestOpenPathWhileLockedAsksForTheUnlock(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	p := filepath.Join(h.dir, "locked.efd")
	if _, e := h.c.CreateArchive(p, "Locked", compressionNormal); e != nil {
		t.Fatal(e)
	}
	h.c.Lock()
	_, e := h.c.OpenPath(p)
	if e == nil || e.Code != CodeNeedsUnlock {
		t.Fatalf("locked answered %v, want %s", e, CodeNeedsUnlock)
	}
}

// An archive whose keys are somewhere else: the envelope decoded, no
// record holds that id, and the page offers Import records… The file is
// not touched.
func TestOpenPathWithoutARecordSaysTheKeyIsNotHere(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	p := strayEnvelope(t, filepath.Join(h.dir, "someone-elses.efd"), [16]byte{9, 8, 7, 6})
	_, e := h.c.OpenPath(p)
	if e == nil || e.Code != CodeKeyNotInVault {
		t.Fatalf("a stray archive answered %v, want %s", e, CodeKeyNotInVault)
	}
}

// The magic wrong, or nothing to read: a toast and nothing more. A file
// that is not there answers the same — there is no archive either way,
// and the double-click of a file that went is not worth its own scene.
func TestOpenPathOfSomethingElseIsNotAnArchive(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	junk := filepath.Join(h.dir, "notes.txt")
	if err := os.WriteFile(junk, []byte("this is not an archive at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{junk, filepath.Join(h.dir, "gone.efd")} {
		if _, e := h.c.OpenPath(p); e == nil || e.Code != CodeNotAnArchive {
			t.Fatalf("%s answered %v, want %s", filepath.Base(p), e, CodeNotAnArchive)
		}
	}
}

// A forgotten record keeps its keys, so it is restored and not found
// again: the refusal is archive.forgotten, as every other operation on
// one answers (APP.md §13).
func TestOpenPathOfAForgottenRecordSaysSo(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	p := filepath.Join(h.dir, "forgotten.efd")
	id, e := h.c.CreateArchive(p, "Forgotten", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	if e := h.c.ForgetArchive(id); e != nil {
		t.Fatalf("forget: %v", e)
	}
	if _, e := h.c.OpenPath(p); e == nil || e.Code != CodeArchiveForgotten {
		t.Fatalf("a forgotten record answered %v, want %s", e, CodeArchiveForgotten)
	}
}

// Enfold's own place is refused here as everywhere else (APP.md §3): a
// command line is as much the user's as a dialog is, and the refusal
// comes before the file is read at all.
func TestOpenPathInsideTheDataFolderIsRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	inside := filepath.Join(h.dir, "data", "planted.efd")
	strayEnvelope(t, inside, [16]byte{1})
	if _, e := h.c.OpenPath(inside); e == nil || e.Code != CodeSourceIsVault {
		t.Fatalf("a path in the data folder answered %v, want %s", e, CodeSourceIsVault)
	}
	// The vault file itself is not an archive to open either.
	if _, e := h.c.OpenPath(h.vault); e == nil || e.Code != CodeSourceIsVault {
		t.Fatalf("the vault file answered %v, want %s", e, CodeSourceIsVault)
	}
}

// Two copies of one archive and one handle (APP.md §2.3): the archive is
// open from the first file, so the second cannot be opened and must not be
// recorded either — the registry would then name a copy the page is not
// reading. The refusal names where it is open from, and nothing is
// written.
func TestOpenPathRefusesACopyWhileTheArchiveIsOpenElsewhere(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	here := filepath.Join(h.dir, "two-copies.efd")
	id, e := h.c.CreateArchive(here, "Two copies", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	// The copy is taken before the handle exists: it is byte for byte the
	// same archive, with the same archive_id in its envelope.
	copyPath := filepath.Join(h.dir, "two-copies (1).efd")
	body, err := os.ReadFile(here)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	_, e = h.c.OpenPath(copyPath)
	if !isCode(e, CodeOpenElsewhere) {
		t.Fatalf("the copy answered %v, want %s", e, CodeOpenElsewhere)
	}
	if !samePlace(e.Path, here) {
		t.Fatalf("the refusal names %s, want the file the handle is on, %s", e.Path, here)
	}
	if got := h.recordPath(id); !samePlace(got, here) {
		t.Fatalf("last_path moved to %s: a refused open writes nothing", got)
	}
}

// The very file that is open, opened again from Explorer: there is
// nothing to write and nothing to open, and the page simply goes to it.
func TestOpenPathOfTheFileAlreadyOpenAnswersTheId(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	p := filepath.Join(h.dir, "already.efd")
	id, e := h.c.CreateArchive(p, "Already", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	r, e := h.c.OpenPath(p)
	if e != nil {
		t.Fatalf("the open file answered %v", e)
	}
	if r.ArchiveID != id || r.Relocated {
		t.Fatalf("answered %+v, want %s and no move", r, id)
	}
}

// An open that has not become a handle yet is still an open, and it is on
// one file: an operation of the Archives page — a Verify, a Compact —
// between reading the record and installing its handle. A copy opened
// from Explorer in that window must not move last_path to itself and then
// join the open of the other file, which would answer success while the
// page reads the copy nobody asked for (the second review's finding 1).
//
// The reservation is installed here as openArchiveFor installs it, which
// is the window itself rather than a stand-in for it.
func TestOpenPathRefusesWhileAnotherFileIsBeingOpened(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	was := filepath.Join(h.dir, "being-opened.efd")
	id, e := h.c.CreateArchive(was, "Being opened", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	aid, _ := parseID(id)
	copyPath := filepath.Join(h.dir, "being-opened (1).efd")
	body, err := os.ReadFile(was)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(copyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	// The window: someone is opening the recorded file, and no handle
	// exists yet.
	h.c.mu.Lock()
	res := h.c.reserveOpenLocked(aid, was)
	h.c.mu.Unlock()

	_, e = h.c.OpenPath(copyPath)
	if !isCode(e, CodeOpenElsewhere) {
		t.Fatalf("the copy answered %v while %s was being opened, want %s", e, filepath.Base(was), CodeOpenElsewhere)
	}
	if !samePlace(e.Path, was) {
		t.Fatalf("the refusal names %s, want the file being opened, %s", e.Path, was)
	}
	if got := h.recordPath(id); !samePlace(got, was) {
		t.Fatalf("last_path moved to %s while another file was being opened", got)
	}
	h.c.releaseOpen(aid, res)
}

// The window the reservation exists for, stood in on purpose (the third
// review's finding 1): OpenPath(A) has moved last_path to A and has not
// opened it yet. A second OpenPath, for another copy of the same archive,
// must find that reservation and be refused by it — it must not reserve,
// must not move last_path to its own file, and must not install a handle
// on it, which the first call would then join and report success for.
//
// When the first call finishes, the two things that must agree do: the
// handle is on A, and the record says A.
func TestOpenPathHoldsTheReservationAcrossTheMoveAndTheOpen(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	made := filepath.Join(h.dir, "one-of-two.efd")
	id, e := h.c.CreateArchive(made, "One of two", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	aid, _ := parseID(id)
	body, err := os.ReadFile(made)
	if err != nil {
		t.Fatal(err)
	}
	// Two copies, at neither of which the record stands: both are a move
	// as far as OpenPath is concerned.
	first := filepath.Join(h.dir, "copies", "a.efd")
	second := filepath.Join(h.dir, "copies", "b.efd")
	if err := os.MkdirAll(filepath.Dir(first), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{first, second} {
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(made); err != nil {
		t.Fatal(err)
	}

	type answer struct {
		r OpenPathResult
		e *Error
	}
	inWindow, letGo, other := make(chan struct{}), make(chan struct{}), make(chan answer, 1)
	// The pause is the first call's alone, and never blocks a second
	// caller that reaches it.
	firstOnly := make(chan struct{}, 1)
	firstOnly <- struct{}{}
	h.c.seams.relocated = func() {
		select {
		case <-firstOnly:
			close(inWindow)
			<-letGo
		default:
		}
	}
	done := make(chan answer, 1)
	go func() {
		r, e := h.c.OpenPath(first)
		done <- answer{r, e}
	}()
	<-inWindow // the move of last_path is made; the open is not

	go func() {
		r, e := h.c.OpenPath(second)
		other <- answer{r, e}
	}()
	// Either permitted answer, and neither of them a move: refused by the
	// reservation, or still waiting for it.
	select {
	case a := <-other:
		other <- a // put it back for the check at the end
		if !isCode(a.e, CodeOpenElsewhere) {
			t.Fatalf("the second copy answered %+v %v inside the window, want %s", a.r, a.e, CodeOpenElsewhere)
		}
		if !samePlace(a.e.Path, first) {
			t.Fatalf("the refusal names %s, want the file being opened, %s", a.e.Path, first)
		}
	case <-time.After(300 * time.Millisecond):
		// Waiting for the reservation is the other permitted answer.
	}
	if got := h.recordPath(id); !samePlace(got, first) {
		t.Fatalf("last_path is %s inside the window, want the first call's file %s", got, first)
	}

	close(letGo)
	a := <-done
	if a.e != nil || a.r.ArchiveID != id || !a.r.Relocated {
		t.Fatalf("the first call answered %+v %v", a.r, a.e)
	}
	// The handle and the record agree, and on the file that was asked for.
	h.c.mu.Lock()
	oa := h.c.archives[aid]
	openPath := ""
	if oa != nil {
		openPath = oa.path
	}
	h.c.mu.Unlock()
	if oa == nil || !samePlace(openPath, first) {
		t.Fatalf("the handle is on %q, want %s", openPath, first)
	}
	if got := h.recordPath(id); !samePlace(got, first) {
		t.Fatalf("last_path is %s, want %s", got, first)
	}
	// And whatever the second call was doing, it never moved the record.
	select {
	case a := <-other:
		if a.e == nil {
			t.Fatalf("the second copy opened after all: %+v", a.r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the second call never answered")
	}
	if got := h.recordPath(id); !samePlace(got, first) {
		t.Fatalf("last_path ended at %s, want %s", got, first)
	}
}

// The same file being opened is joined, not refused: the call waits for
// the open in flight and then asks the whole question again, so it
// answers from whatever that open left behind.
func TestOpenPathWaitsForAnOpenOfTheSameFile(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	p := filepath.Join(h.dir, "same-file.efd")
	id, e := h.c.CreateArchive(p, "Same file", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	aid, _ := parseID(id)
	h.c.mu.Lock()
	res := h.c.reserveOpenLocked(aid, p)
	h.c.mu.Unlock()

	type answer struct {
		r OpenPathResult
		e *Error
	}
	done := make(chan answer, 1)
	go func() {
		r, e := h.c.OpenPath(p)
		done <- answer{r, e}
	}()
	select {
	case a := <-done:
		t.Fatalf("OpenPath answered %+v %v without waiting for the open in flight", a.r, a.e)
	case <-time.After(60 * time.Millisecond):
	}
	// The open in flight ends having installed nothing — a failure, or a
	// Verify that closed again — so the waiting call opens the file
	// itself, and it is the one the record names.
	h.c.releaseOpen(aid, res)
	select {
	case a := <-done:
		if a.e != nil || a.r.ArchiveID != id {
			t.Fatalf("after the wait: %+v %v", a.r, a.e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OpenPath never woke from the open it joined")
	}
}

// The archive is open at the very file Explorer handed over, so the
// answer is that handle — and it is mounted in the same breath as the
// comparison that chose it (the fourth review's finding). Comparing the
// path, letting the mutex go and then opening by id leaves a window in
// which a Leave or an operation's end closes the handle and another
// copy's OpenPath installs a handle on *its* file, which this call would
// join and report success for.
//
// Nothing here is raced (the fifth review's finding): the mutex is proved
// held at the moment of the mount, the close is held inside the core
// where it cannot proceed, and each of the three calls is asserted on by
// itself. What the archive is left open on is read as a **path** and a
// mounted flag — an id says nothing about which copy was joined.
func TestOpenPathMountsTheOpenHandleInOneCriticalSection(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	open := filepath.Join(h.dir, "held.efd")
	id, e := h.c.CreateArchive(open, "Held", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	aid, _ := parseID(id)
	body, err := os.ReadFile(open)
	if err != nil {
		t.Fatal(err)
	}
	copyPath := filepath.Join(h.dir, "held (1).efd")
	if err := os.WriteFile(copyPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	// The handle exists and no page holds it — one the core opened for an
	// operation of the Archives page — so the mount is something OpenPath
	// has to do, and the flag says whether it did.
	oa, e := h.c.acquireForOperation(id)
	if e != nil {
		t.Fatal(e)
	}
	if oa.mounted {
		t.Fatal("the operation's handle is mounted; this test needs one that is not")
	}

	// The close is started from inside the mount's own critical section
	// and held at the seam just before it takes the state mutex: it is in
	// the core and cannot go on, so what follows is ordered, not timed.
	closerIn, letClose := make(chan struct{}), make(chan struct{})
	closed := make(chan *Error, 1)
	closeOnce := make(chan struct{}, 1)
	closeOnce <- struct{}{}
	h.c.seams.closing = func() {
		select {
		case <-closeOnce:
			close(closerIn)
			<-letClose
		default:
		}
	}
	mounting := make(chan struct{})
	mountOnce := make(chan struct{}, 1)
	mountOnce <- struct{}{}
	h.c.seams.mounting = func() {
		select {
		case <-mountOnce:
		default:
			return
		}
		// The mutex is held right here: there is no window between the
		// comparison that chose this handle and the mount of it. Under
		// the shape this replaced, the seam ran with the mutex free.
		if h.c.mu.TryLock() {
			h.c.mu.Unlock()
			t.Error("the state mutex is free at the mount: the comparison and the act are not one section")
		}
		go func() { closed <- h.c.CloseArchive(id) }()
		close(mounting)
	}

	r, e := h.c.OpenPath(open)
	if e != nil || r.ArchiveID != id || r.Relocated {
		t.Fatalf("the open file answered %+v %v, want %s and no move", r, e, id)
	}
	select {
	case <-mounting:
	case <-time.After(5 * time.Second):
		t.Fatal("the same-path branch was never taken")
	}
	// The close got no further than the seam, so the handle stands as
	// OpenPath left it: on the file it was given, and mounted.
	select {
	case <-closerIn:
	case <-time.After(5 * time.Second):
		t.Fatal("the close never reached the core")
	}
	h.c.mu.Lock()
	live := h.c.archives[aid]
	livePath, liveMounted := "", false
	if live != nil {
		livePath, liveMounted = live.path, live.mounted
	}
	h.c.mu.Unlock()
	if live == nil {
		t.Fatal("the archive is not open after OpenPath answered success")
	}
	if !samePlace(livePath, open) {
		t.Fatalf("the handle is on %s, want the file OpenPath was given, %s", livePath, open)
	}
	if !liveMounted {
		t.Fatal("the handle was not mounted: OpenPath answered from a handle no page holds")
	}
	// And the copy, asked for while that handle is live and the close is
	// held: refused, naming the file the handle is on. Never success.
	_, ce := h.c.OpenPath(copyPath)
	if !isCode(ce, CodeOpenElsewhere) {
		t.Fatalf("the copy answered %v while the archive was open on %s, want %s", ce, open, CodeOpenElsewhere)
	}
	if !samePlace(ce.Path, open) {
		t.Fatalf("the refusal names %s, want %s", ce.Path, open)
	}
	if got := h.recordPath(id); !samePlace(got, open) {
		t.Fatalf("last_path moved to %s: a refused open writes nothing", got)
	}
	// The close is let go, and it is asserted on like the rest.
	close(letClose)
	select {
	case e := <-closed:
		if e != nil {
			t.Fatalf("the close answered %v", e)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the close never finished")
	}
	h.c.releaseClaim(oa)
}

// The decision is taken again after every wait, and the second turn is
// taken against the file this call was given — never against whatever
// handle happens to be there by then (the fourth review's finding, and
// the fifth review's (b)).
//
// The wait is a real one: a reservation for the same file, which is a
// boundary OpenPath joins rather than refusing. While the call is held
// between that wait and its next turn, the copy takes the archive — so
// the second turn reads a handle on another file, and must refuse it by
// name and leave the record where the copy put it.
func TestOpenPathNeverAnswersForACopyInstalledWhileItWaited(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	mine := filepath.Join(h.dir, "mine.efd")
	id, e := h.c.CreateArchive(mine, "Mine", compressionNormal)
	if e != nil {
		t.Fatal(e)
	}
	aid, _ := parseID(id)
	body, err := os.ReadFile(mine)
	if err != nil {
		t.Fatal(err)
	}
	theirs := filepath.Join(h.dir, "theirs.efd")
	if err := os.WriteFile(theirs, body, 0o600); err != nil {
		t.Fatal(err)
	}

	// The wait boundary: an open of this very file on its way, which
	// OpenPath(mine) joins.
	h.c.mu.Lock()
	res := h.c.reserveOpenLocked(aid, mine)
	h.c.mu.Unlock()

	betweenTurns, letOn := make(chan struct{}), make(chan struct{})
	turnOnce := make(chan struct{}, 1)
	turnOnce <- struct{}{}
	h.c.seams.redeciding = func() {
		select {
		case <-turnOnce:
			close(betweenTurns)
			<-letOn
		default:
		}
	}

	type answer struct {
		r OpenPathResult
		e *Error
	}
	done := make(chan answer, 1)
	go func() {
		r, e := h.c.OpenPath(mine)
		done <- answer{r, e}
	}()
	// It is waiting on the reservation, not deciding.
	select {
	case a := <-done:
		t.Fatalf("OpenPath answered %+v %v without joining the open in flight", a.r, a.e)
	case <-time.After(100 * time.Millisecond):
	}
	h.c.releaseOpen(aid, res)

	// Now it is between the wait and its second turn, and held there.
	select {
	case <-betweenTurns:
	case <-time.After(5 * time.Second):
		t.Fatal("the wait never led to a second turn")
	}
	// The copy takes the archive: the record moves to it, and the handle
	// is installed on it.
	if r, e := h.c.OpenPath(theirs); e != nil || !r.Relocated {
		t.Fatalf("the copy's own open answered %+v %v", r, e)
	}
	close(letOn)

	a := <-done
	if a.e == nil {
		t.Fatalf("the second turn answered success %+v for a file it was not given", a.r)
	}
	if !isCode(a.e, CodeOpenElsewhere) {
		t.Fatalf("the second turn answered %v, want %s", a.e, CodeOpenElsewhere)
	}
	if !samePlace(a.e.Path, theirs) {
		t.Fatalf("the refusal names %s, want the file the handle is on, %s", a.e.Path, theirs)
	}
	// And it moved nothing back: the record is where the copy's open put
	// it, and the handle is on that file.
	if got := h.recordPath(id); !samePlace(got, theirs) {
		t.Fatalf("last_path is %s, want %s — a refused open writes nothing", got, theirs)
	}
	h.c.mu.Lock()
	livePath := ""
	if live := h.c.archives[aid]; live != nil {
		livePath = live.path
	}
	h.c.mu.Unlock()
	if !samePlace(livePath, theirs) {
		t.Fatalf("the handle is on %s, want %s", livePath, theirs)
	}
}

// The case no path check can see (vaultsource_test.go's own technique): a
// second directory entry for vault.eks, in a folder of the caller's own
// and under a name of its own. The refusal has to come from the handle,
// before a byte of the vault is read and answered about.
func TestOpenPathRefusesAHardLinkToTheVault(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	hard := filepath.Join(h.dir, "innocent.efd")
	if err := os.Link(h.vault, hard); err != nil {
		t.Skipf("this volume does not take a hard link: %v", err)
	}
	if e := h.c.refuseVaultPlaces(hard); e != nil {
		t.Fatalf("the path check saw a hard link, so this test proves nothing about the handle: %v", e)
	}
	if _, e := h.c.OpenPath(hard); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("a hard link to the vault answered %v, want %s", e, CodeSourceIsVault)
	}
}

// A relative path is nobody's to resolve here: the shell resolves a
// launch's argument against that launch's working directory (§14).
func TestOpenPathRefusesARelativePath(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	if _, e := h.c.OpenPath("holiday.efd"); e == nil || e.Code != CodeParams {
		t.Fatalf("a relative path answered %v, want %s", e, CodeParams)
	}
	if _, e := h.c.OpenPath(""); e == nil || !errors.Is(e, &Error{Code: CodeParams}) {
		t.Fatalf("an empty path answered %v, want %s", e, CodeParams)
	}
}
