package app

import (
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
)

// recoverySlotID finds the vault's recovery slot on the Keys view.
func recoverySlotID(t *testing.T, h *harness) (recovery, other string) {
	t.Helper()
	for _, s := range h.c.Slots() {
		if s.Type == "recovery" {
			recovery = s.RecipientID
		} else {
			other = s.RecipientID
		}
	}
	if recovery == "" || other == "" {
		t.Fatalf("slots: %+v", h.c.Slots())
	}
	return recovery, other
}

// fetchSecret is the page's fetch of the one-time URL.
func fetchSecret(t *testing.T, url string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Origin", "wails://wails")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return resp.StatusCode, string(body)
}

// reveal runs the reveal ceremony on a password vault to its final event.
func reveal(t *testing.T, h *harness, rid string) CeremonyState {
	t.Helper()
	h.rec.reset()
	if e := h.c.RevealRecoveryKey(rid); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	if p.Kind != "reveal" || p.Choose {
		t.Fatalf("the reveal's protector prompt: %+v", p)
	}
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	shown := h.rec.waitCeremony(t, StepRecovery, false)
	if !strings.HasPrefix(shown.SlotLabel, "http://127.0.0.1:") || shown.PromptID != "" {
		t.Fatalf("no one-time URL: %+v", shown)
	}
	return shown
}

// The recovery key is shown again after a protector unlock (APP.md §3
// Keys): the digits over the one-time URL, saved by the core behind the
// URL's handle, never over a bound call; the handle ends when dropped, on
// a second fetch, and on a lock trigger.
func TestRevealRecoveryKey(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.c.SetAppOrigin("wails://wails")
	if e := h.c.RevealRecoveryKey("zz"); !isCode(e, CodeParams) {
		t.Fatalf("bad id: %v", e)
	}
	h.unlockWithPassword()
	rid, pwID := recoverySlotID(t, h)
	// What is refused is refused before any prompt.
	if e := h.c.RevealRecoveryKey(pwID); !isCode(e, CodeSlotNotFound) {
		t.Fatalf("a password slot: %v", e)
	}
	// The key's ID is on the view, derived once for the view, the reveal
	// and the saved file (FORMAT.md §18.4).
	var wantID string
	for _, s := range h.c.Slots() {
		if s.Type == "recovery" {
			wantID = s.RecoveryID
		} else if s.RecoveryID != "" {
			t.Fatalf("a slot that is not a recovery slot carries a key ID: %+v", s)
		}
	}
	if len(wantID) != 9 || wantID[4] != '-' {
		t.Fatalf("the recovery slot's ID: %q", wantID)
	}
	shown := reveal(t, h, rid)
	if shown.RecoveryID != wantID {
		t.Fatalf("the reveal shows %q, the view %q", shown.RecoveryID, wantID)
	}
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("a reveal changes the state: %s", st.State)
	}
	code, body := fetchSecret(t, shown.SlotLabel)
	if code != 200 || body != h.recovery {
		t.Fatalf("digits: %d %q (want %q)", code, body, h.recovery)
	}
	// The value stays behind the handle for a save the core writes.
	handle := path.Base(shown.SlotLabel)
	out := filepath.Join(t.TempDir(), "recovery-key.txt") // away from the vault's own folder
	if e := h.c.SaveRecoveryKey(handle, out); e != nil {
		t.Fatalf("save: %v", e)
	}
	text, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{h.recovery, "Test vault", "Recovery key", wantID, "\r\n"} {
		if !strings.Contains(string(text), want) {
			t.Fatalf("saved file lacks %q: %q", want, text)
		}
	}
	// Not into the data folder or beneath it, not beside a vault kept
	// elsewhere, not under a staging name, not relative, not over a folder.
	for _, bad := range []string{
		filepath.Join(h.dir, "data", "key.txt"),
		filepath.Join(h.dir, "data", "WebView2", "key.txt"),
		filepath.Join(h.dir, "vault.incoming-key.txt"),
		filepath.Join(filepath.Dir(h.vault), "key.txt"),
		h.dir,
	} {
		if e := h.c.SaveRecoveryKey(handle, bad); !isCode(e, CodeRecoveryPlace) {
			t.Fatalf("%s: %v", bad, e)
		}
	}
	if e := h.c.SaveRecoveryKey(handle, "key.txt"); !isCode(e, CodeParams) {
		t.Fatalf("relative: %v", e)
	}
	// A save replaces the file the dialog confirmed.
	if e := h.c.SaveRecoveryKey(handle, out); e != nil {
		t.Fatalf("save again: %v", e)
	}
	// A second fetch of the URL gets nothing, and ends the handle too:
	// the page fetches once, so a second fetch is not the page's.
	if code, _ := fetchSecret(t, shown.SlotLabel); code != 404 {
		t.Fatalf("second fetch: %d", code)
	}
	if e := h.c.SaveRecoveryKey(handle, out); !isCode(e, CodeStalePrompt) {
		t.Fatalf("after a second fetch: %v", e)
	}
	if e := h.c.SaveRecoveryKey("nonsense", out); !isCode(e, CodeStalePrompt) {
		t.Fatalf("unknown handle: %v", e)
	}
	// Dropped: nothing to save; the digits are the page's problem now.
	shown = reveal(t, h, rid)
	handle = path.Base(shown.SlotLabel)
	if e := h.c.DropRecoveryKey(handle); e != nil {
		t.Fatal(e)
	}
	if e := h.c.SaveRecoveryKey(handle, out); !isCode(e, CodeStalePrompt) {
		t.Fatalf("after the drop: %v", e)
	}
	if code, _ := fetchSecret(t, shown.SlotLabel); code != 404 {
		t.Fatalf("fetch after the drop: %d", code)
	}
	// A lock trigger drops a held key: nothing decrypted outlives it.
	shown = reveal(t, h, rid)
	handle = path.Base(shown.SlotLabel)
	h.c.LockNow(ReasonWorkstation)
	h.rec.waitState(t, StateLocked)
	if e := h.c.SaveRecoveryKey(handle, out); !isCode(e, CodeStalePrompt) {
		t.Fatalf("after a lock: %v", e)
	}
	if code, _ := fetchSecret(t, shown.SlotLabel); code != 404 {
		t.Fatalf("fetch after a lock: %d", code)
	}
	// The ID is plaintext (FORMAT.md §18.4): the view has it while locked
	// too, which is what lets the unlock prompt list the sheets.
	for _, s := range h.c.Slots() {
		if s.Type == "recovery" && s.RecoveryID != wantID {
			t.Fatalf("the key ID is not readable while locked: %+v", s)
		}
	}
}

// The reveal holds the handle while the VMK is recovered, like a slot
// change: a registry write meanwhile owes its receipt.
func TestRevealHoldsTheHandle(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.c.SetAppOrigin("wails://wails")
	h.unlockWithPassword()
	rid, _ := recoverySlotID(t, h)
	h.rec.reset()
	if e := h.c.RevealRecoveryKey(rid); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepPassword, true)
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false); !isCode(e, CodeCeremonyRunning) {
		t.Fatalf("a registry write during the reveal: %v", e)
	}
	h.c.CancelUnlock()
	h.rec.waitCeremony(t, StepFailed, false)
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false); e != nil {
		t.Fatalf("after the reveal: %v", e)
	}
}
