package app

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The app half of VMK rotation at a removal and a password change, and of
// replacing the recovery keys (design docs/research/vmk-rotation.md 2.3-2.7,
// app tests 1-40 of 2.6). A test that fails a keystore commit or watches its
// writes does so through keystore.SetCommitFault and SetWriteSeen, puts the
// previous value back when it ends, and does not run in parallel.

// errInjected is what a seam returns where only the failure matters, not
// its kind.
var errInjected = errors.New("injected by the test")

// newRecoverySpec is a recovery slot to create a vault with, and its digits.
func newRecoverySpec(t *testing.T, label string) (keystore.RecoverySlot, string) {
	t.Helper()
	rk, err := kdf.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	return keystore.RecoverySlot{Key: rk, Label: label}, rk.Digits()
}

// writeVault creates a vault at path from specs, its password on when
// password is not empty.
func writeVault(t *testing.T, path, password string, specs ...keystore.SlotSpec) {
	t.Helper()
	opts := keystore.CreateOptions{Slots: specs}
	if password != "" {
		opts.Entangle = &keystore.Entangle{Password: password, Argon2: fast}
	}
	unl, err := keystore.Create(path, opts)
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()
}

// harnessOver is a core over the vault file at path, locked: newHarness for a
// file the test made. recovery is the digits of a recovery key that opens it
// and password the vault's own, empty while its switch is off.
func harnessOver(t *testing.T, cards *fakeCards, dir, path, recovery, password string) *harness {
	t.Helper()
	h := &harness{t: t, dir: dir, vault: path, recovery: recovery, entangled: password, clk: newFakeClock(), rec: &recorder{}, cards: cards}
	var cs Cards
	if cards != nil {
		cs = cards
	}
	c, err := New(Deps{Cards: cs, Events: h.rec, Clock: h.clk, DataDir: filepath.Join(dir, "data"), Log: h.log})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	h.c = c
	t.Cleanup(c.Close)
	if e := c.OpenVaultFile(path, "Test vault"); e != nil {
		t.Fatalf("open vault: %v", e)
	}
	return h
}

// rekeyHarness is a vault of the given unlock methods and a core over it,
// locked. recovery is the digits of one of its recovery keys.
func rekeyHarness(t *testing.T, cards *fakeCards, password, recovery string, specs ...keystore.SlotSpec) *harness {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "vault.eks")
	writeVault(t, path, password, specs...)
	return harnessOver(t, cards, dir, path, recovery, password)
}

// passwordsHarness is a vault of the test password, n-1 more passwords
// labelled "Second", "Third", and one recovery key: the extra passwords are
// the methods a test removes (two passwords share their secret set, so the
// invariant keeps the recovery key; FORMAT §6.4).
func passwordsHarness(t *testing.T, n int) *harness {
	t.Helper()
	r, digits := newRecoverySpec(t, "Recovery key")
	specs := []keystore.SlotSpec{r, keystore.PasswordSlot{Password: testPassword, Argon2: fast, Label: "Password"}}
	for i, label := range []string{"Second", "Third"}[:n-1] {
		specs = append(specs, keystore.PasswordSlot{Password: fmt.Sprintf("password number %d", i+2), Argon2: fast, Label: label})
	}
	return rekeyHarness(t, nil, "", digits, specs...)
}

// cardWithKey is a fake YubiKey holding one usable key in 9d.
func cardWithKey(pin string) (*fakeCard, []byte) {
	card := newFakeCard(pin)
	return card, card.addKey(0x9d, true)
}

// readerWith is a reader set with card in it.
func readerWith(card *fakeCard) *fakeCards {
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	return cards
}

// slot is the unlock method labelled label on the Keys view.
func (h *harness) slot(label string) SlotView {
	h.t.Helper()
	for _, s := range h.c.Slots() {
		if s.Label == label {
			return s
		}
	}
	h.t.Fatalf("no slot %q among %+v", label, h.c.Slots())
	return SlotView{}
}

// recoveryViews are the recovery keys the Keys view lists, in its order.
func (h *harness) recoveryViews() []SlotView {
	var out []SlotView
	for _, s := range h.c.Slots() {
		if s.Type == "recovery" {
			out = append(out, s)
		}
	}
	return out
}

func ridOf(t *testing.T, hexID string) [16]byte {
	t.Helper()
	rid, ok := parseID(hexID)
	if !ok {
		t.Fatalf("bad id %q", hexID)
	}
	return rid
}

// generation and owes read the open handle under the state mutex, only
// while no ceremony holds it; while Locked, the cached facts.
func (h *harness) generation() uint64 {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	if h.c.vault.ks != nil {
		return h.c.vault.ks.Generation()
	}
	return h.c.vault.generation
}

func (h *harness) owes() bool {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	if h.c.vault.ks != nil {
		return h.c.vault.ks.RetireOwed()
	}
	return h.c.vault.retireOwed
}

func (h *harness) warned(code Code) bool {
	return slices.Contains(h.status().Warnings, code)
}

// proveWithPassword answers an act's proof on a password vault.
func (h *harness) proveWithPassword() {
	h.t.Helper()
	p := h.rec.waitCeremony(h.t, StepPassword, true)
	if p.Choose {
		h.t.Fatalf("the proof asks for a new password: %+v", p)
	}
	if e := h.c.SubmitSecret("password", p.PromptID, testPassword); e != nil {
		h.t.Fatalf("submit: %v", e)
	}
}

// answerRecovery answers the next recovery-key prompt with digits.
func (h *harness) answerRecovery(digits string) CeremonyState {
	h.t.Helper()
	p := h.rec.waitCeremony(h.t, StepRecovery, true)
	if e := h.c.SubmitSecret("recovery", p.PromptID, digits); e != nil {
		h.t.Fatalf("submit the recovery key: %v", e)
	}
	return p
}

// chooseNew answers the prompt for a password being chosen.
func (h *harness) chooseNew(pw string) {
	h.t.Helper()
	p := h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.Choose
	}).(CeremonyState)
	if e := h.c.SubmitSecret("password", p.PromptID, pw); e != nil {
		h.t.Fatalf("submit the new password: %v", e)
	}
}

// end waits for the act's final event, Done or Failed.
func (h *harness) end() CeremonyState {
	h.t.Helper()
	return h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && (s.Step == StepDone || s.Step == StepFailed)
	}).(CeremonyState)
}

// done is end, failing the test unless the act ended Done.
func (h *harness) done() CeremonyState {
	h.t.Helper()
	st := h.end()
	if st.Step != StepDone {
		h.t.Fatalf("the act did not end Done: %+v", st)
	}
	return st
}

// ceremoniesSince are the ceremony states recorded from mark on.
func (h *harness) ceremoniesSince(mark int) []CeremonyState {
	var out []CeremonyState
	for _, e := range h.rec.snapshot()[mark:] {
		if s, ok := e.payload.(CeremonyState); ok {
			out = append(out, s)
		}
	}
	return out
}

func (h *harness) mark() int { return len(h.rec.snapshot()) }

// noCeremonySince fails the test if a ceremony state was recorded from mark
// on: a refusal before the proof asks for nothing.
func (h *harness) noCeremonySince(mark int, what string) {
	h.t.Helper()
	if s := h.ceremoniesSince(mark); len(s) != 0 {
		h.t.Fatalf("%s began a ceremony: %+v", what, s[0])
	}
}

// unlockWithRecovery runs the lock screen's recovery-key unlock.
func (h *harness) unlockWithRecovery(digits string) {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodRecovery); e != nil {
		h.t.Fatalf("begin: %v", e)
	}
	h.answerRecovery(digits)
	h.rec.waitState(h.t, StateUnlocked)
}

// lockAndSettle locks and waits for the lock's unbounded half.
func (h *harness) lockAndSettle() {
	h.t.Helper()
	h.c.Lock()
	h.settleLock()
}

// recoveryRefused shows that digits no longer open the vault: the lock
// screen asks again with vault.auth, and the ceremony is cancelled.
func (h *harness) recoveryRefused(digits string) {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodRecovery); e != nil {
		h.t.Fatalf("begin: %v", e)
	}
	first := h.answerRecovery(digits)
	again := h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepRecovery && s.PromptID != "" && s.PromptID != first.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		h.t.Fatalf("a withdrawn recovery key was not refused: %+v", again)
	}
	h.c.CancelUnlock()
	h.end()
}

// keepAll answers every keep step of a replacement and returns each new
// key's digits, read through the preview's secretValue of the URL's token,
// with the keep states seen. It stops after the last key, leaving the
// confirmation to the caller.
func (h *harness) keepAll() ([]string, []CeremonyState) {
	h.t.Helper()
	var digits []string
	var steps []CeremonyState
	for {
		st := h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
			s, ok := x.(CeremonyState)
			return ok && (s.Step == StepKeepKey && s.PromptID != "" || s.Step == StepFailed)
		}).(CeremonyState)
		if st.Step != StepKeepKey {
			h.t.Fatalf("the replacement failed before every key was kept: %+v", st)
		}
		v, _, id, ok := h.c.preview.secretValue(secretToken(st.SlotLabel))
		if !ok || id != st.RecoveryID {
			h.t.Fatalf("the key shown is not behind its URL: %+v", st)
		}
		digits = append(digits, v)
		steps = append(steps, st)
		if e := h.c.SubmitSecret("kept", st.PromptID, ""); e != nil {
			h.t.Fatalf("kept: %v", e)
		}
		if st.KeepIndex == st.KeepCount {
			return digits, steps
		}
	}
}

// confirmReplacement answers the replacement's one confirmation.
func (h *harness) confirmReplacement() {
	h.t.Helper()
	st := h.rec.waitCeremony(h.t, StepConfirm, true)
	if e := h.c.SubmitSecret("confirm", st.PromptID, ""); e != nil {
		h.t.Fatalf("confirm: %v", e)
	}
}

// replaceOnPassword runs a replacement on a password vault to its end and
// returns the new digits and the final state.
func (h *harness) replaceOnPassword(backupPath string) ([]string, CeremonyState) {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.ReplaceRecoveryKeys(backupPath); e != nil {
		h.t.Fatalf("replace: %v", e)
	}
	h.proveWithPassword()
	digits, _ := h.keepAll()
	h.confirmReplacement()
	return digits, h.end()
}

// removeOnPassword removes the method with id from a password vault and
// returns the final state.
func (h *harness) removeOnPassword(id string, rekey bool, backupPath string) CeremonyState {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.RemoveSlot(id, rekey, backupPath); e != nil {
		h.t.Fatalf("remove: %v", e)
	}
	h.proveWithPassword()
	return h.end()
}

// faultOn makes keystore commits fail through the exported seam for the rest
// of the test, unless a later call replaces it.
func faultOn(t *testing.T, f func(step string) error) {
	t.Helper()
	restore := keystore.SetCommitFault(f)
	t.Cleanup(restore)
}

// faultSwitch is a commit fault a test turns on and off, from any goroutine.
type faultSwitch struct {
	mu   sync.Mutex
	fail func(step string) error
}

func (f *faultSwitch) set(fail func(step string) error) {
	f.mu.Lock()
	f.fail = fail
	f.mu.Unlock()
}

func (f *faultSwitch) step(step string) error {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail == nil {
		return nil
	}
	return fail(step)
}

// installSwitch puts a faultSwitch behind keystore.SetCommitFault.
func installSwitch(t *testing.T) *faultSwitch {
	t.Helper()
	f := &faultSwitch{}
	faultOn(t, f.step)
	return f
}

// everyRegion fails every slot region write: a retiring commit cannot land.
func everyRegion(step string) error {
	if step == "region" {
		return errInjected
	}
	return nil
}

// writeLog records what keystore writes, in order.
type writeLog struct {
	mu     sync.Mutex
	writes []string
	// after, when set, is told every write before it is made: the state
	// of the file as the previous writes left it.
	after func(what string)
}

func watchWrites(t *testing.T) *writeLog {
	t.Helper()
	w := &writeLog{}
	restore := keystore.SetWriteSeen(func(what string, off, n int64) {
		w.mu.Lock()
		w.writes = append(w.writes, what)
		after := w.after
		w.mu.Unlock()
		if after != nil {
			after(what)
		}
	})
	t.Cleanup(restore)
	return w
}

func (w *writeLog) take() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := w.writes
	w.writes = nil
	return out
}

func count(ws []string, what string) int {
	n := 0
	for _, w := range ws {
		if w == what {
			n++
		}
	}
	return n
}

// fileBytes is the vault file as it is on disk.
func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// holdsID reports whether the raw bytes hold a recipient ID, which appears
// in plaintext only in a slot region copy (the registry is ciphertext).
func holdsID(t *testing.T, path string, rid [16]byte) bool {
	t.Helper()
	return bytes.Contains(fileBytes(t, path), rid[:])
}

// holdsRecord reports whether the raw bytes hold a record's recipient ID,
// its epk or its wrapped VMK: a torn write can keep one without another.
func holdsRecord(t *testing.T, path string, rec format.SlotRecord) bool {
	t.Helper()
	b := fileBytes(t, path)
	return bytes.Contains(b, rec.RecipientID[:]) || len(rec.EPK) > 0 && bytes.Contains(b, rec.EPK) || bytes.Contains(b, rec.WrappedVMK[:])
}

// superblocks decodes both superblock copies of an image; nil for one that
// does not decode.
func superblocks(img []byte) (a, b *format.KeystoreSuperblock) {
	dec := func(c format.Copy) *format.KeystoreSuperblock {
		off := c.KeystoreSuperblockOff()
		sb, err := format.DecodeKeystoreSuperblock(img[off : off+format.SuperblockSize])
		if err != nil {
			return nil
		}
		return sb
	}
	return dec(format.CopyA), dec(format.CopyB)
}

// liveSuperblock is the copy a reader opens.
func liveSuperblock(t *testing.T, img []byte) *format.KeystoreSuperblock {
	t.Helper()
	a := img[format.KeystoreSuperblockAOff : format.KeystoreSuperblockAOff+format.SuperblockSize]
	b := img[format.KeystoreSuperblockBOff : format.KeystoreSuperblockBOff+format.SuperblockSize]
	sb, _, _, err := format.PickKeystoreSuperblock(a, b)
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

// regionAt decodes the slot region a copy holds, its length read from its
// own record lengths, since a copy is zero past its region; ok false when
// the copy holds no region.
func regionAt(img []byte, off uint64) (*format.SlotRegion, bool) {
	b := img[off : off+format.SlotRegionSize]
	n := binary.LittleEndian.Uint32(b[0:4])
	if n == 0 || n > format.MaxSlots {
		return nil, false
	}
	end := uint64(format.MinSlotRegionLen)
	for range n {
		if end+4 > uint64(len(b)) {
			return nil, false
		}
		end += 4 + uint64(binary.LittleEndian.Uint32(b[end:end+4]))
		if end > uint64(len(b)) {
			return nil, false
		}
	}
	r, err := format.DecodeSlotRegion(b[:end])
	if err != nil {
		return nil, false
	}
	return r, true
}

// strayRecords are the records in the slot region copy the live superblock
// does not name that the live state does not hold: what a commit that
// failed before its flip wrote there. Called from inside a commit, on the
// ceremony's goroutine, so it fails nothing itself: nil when it cannot
// tell, which the caller's count then refuses.
func strayRecords(path string) []format.SlotRecord {
	img, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	a := img[format.KeystoreSuperblockAOff : format.KeystoreSuperblockAOff+format.SuperblockSize]
	b := img[format.KeystoreSuperblockBOff : format.KeystoreSuperblockBOff+format.SuperblockSize]
	sb, _, _, err := format.PickKeystoreSuperblock(a, b)
	if err != nil {
		return nil
	}
	live, ok := regionAt(img, sb.SlotRegionOff)
	if !ok {
		return nil
	}
	other := sb.LiveSlotRegion().Other().SlotRegionOff()
	r, ok := regionAt(img, other)
	if !ok {
		return nil
	}
	var out []format.SlotRecord
	for _, s := range r.Slots {
		if !slices.ContainsFunc(live.Slots, func(l format.SlotRecord) bool { return l.RecipientID == s.RecipientID }) {
			out = append(out, s)
		}
	}
	return out
}

// dirtyUnit is the first 4 KiB unit of the registry area, up to the file's
// length, holding a nonzero byte outside both registry extents the
// superblock copies name; -1 when the registry area is clean. Read from the
// raw bytes, never from a handle.
func dirtyUnit(t *testing.T, path string) int64 {
	t.Helper()
	img := fileBytes(t, path)
	var named [][2]uint64
	a, b := superblocks(img)
	for _, sb := range []*format.KeystoreSuperblock{a, b} {
		if sb == nil {
			continue
		}
		x := [2]uint64{sb.RegistryOff, sb.RegistryOff + sb.RegistryLen + format.TagSize}
		if x[1] <= uint64(len(img)) {
			named = append(named, x)
		}
	}
	for i := format.RegistryMinOff; i < len(img); i++ {
		if img[i] == 0 {
			continue
		}
		in := false
		for _, x := range named {
			if x[0] <= uint64(i) && uint64(i) < x[1] {
				in = true
			}
		}
		if !in {
			return int64(i) &^ 4095
		}
	}
	return -1
}

func requireCleanArea(t *testing.T, path, when string) {
	t.Helper()
	if u := dirtyUnit(t, path); u >= 0 {
		t.Fatalf("%s: the registry area holds a byte no superblock names, in the unit at 0x%x", when, u)
	}
}

// requireFileFacts checks that the date and the size the Vault page shows
// are the file's after a keystore write the core made, landed or not: the
// live superblock's modified_at and the length by os.Stat (FORMAT R41, rule
// 8; the integration review's F1).
func requireFileFacts(t *testing.T, h *harness, when string) {
	t.Helper()
	st := h.status()
	sb := liveSuperblock(t, fileBytes(t, h.vault))
	fi, err := os.Stat(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	if st.ModifiedAt != sb.ModifiedAt || st.VaultFileSize != uint64(fi.Size()) {
		t.Fatalf("%s: the cached modified_at %d and size %d; the file's %d and %d",
			when, st.ModifiedAt, st.VaultFileSize, sb.ModifiedAt, fi.Size())
	}
}

// recordsNamed are the slot records the live superblock's region holds.
func recordsNamed(t *testing.T, img []byte, sb *format.KeystoreSuperblock) []format.SlotRecord {
	t.Helper()
	r, err := format.DecodeSlotRegion(img[sb.SlotRegionOff : sb.SlotRegionOff+sb.SlotRegionLen])
	if err != nil {
		t.Fatalf("the region a superblock names does not decode: %v", err)
	}
	return r.Slots
}

// ---- 1-7: the removal, its proof, its pre-checks and its backup.

// 1. A removal and its re-key are one ceremony, one proof and one commit,
// retired before the act ends: both superblock copies name the new
// generation, and the vault stays open for writes.
func TestRemoveSlotAndRekeyIsOneCeremony(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	second := h.slot("Second").RecipientID
	mark := h.mark()
	st := h.removeOnPassword(second, true, "")
	if st.Step != StepDone {
		t.Fatalf("end: %+v", st)
	}
	prompts := map[string]bool{}
	commit := 0
	states := h.ceremoniesSince(mark)
	for i, s := range states {
		if s.Kind != "remove" || !s.Rekey {
			t.Fatalf("an event of the act says otherwise: %+v", s)
		}
		if s.Step == StepPassword && s.PromptID != "" {
			prompts[s.PromptID] = true
		}
		if s.Phase == "commit" && (i == 0 || states[i-1].Phase != "commit") {
			commit++
		}
	}
	if len(prompts) != 1 || commit != 1 {
		t.Fatalf("one proof and one commit phase: %d prompts, %d commit phases", len(prompts), commit)
	}
	for _, s := range h.c.Slots() {
		if s.RecipientID == second {
			t.Fatal("the slot is still listed")
		}
	}
	if g := h.generation(); g != 2 {
		t.Fatalf("generation %d", g)
	}
	a, b := superblocks(fileBytes(t, h.vault))
	if a == nil || b == nil || a.VMKGeneration != 2 || b.VMKGeneration != 2 {
		t.Fatalf("the superblock copies: %+v / %+v", a, b)
	}
	if h.owes() {
		t.Fatal("the removal left the file owed")
	}
	if st := h.status(); st.State != StateUnlocked {
		t.Fatalf("state %s", st.State)
	}
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "after.enf"), "After", compressionNormal); e != nil {
		t.Fatalf("a registry write after the act: %v", e)
	}
}

// 2. Without the re-key the generation stays, and the retirement still runs:
// no copy the superblocks name, and no byte of the file, holds the slot.
func TestRemoveSlotWithoutRekeyKeepsTheGeneration(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	second := h.slot("Second").RecipientID
	mark := h.mark()
	if st := h.removeOnPassword(second, false, ""); st.Step != StepDone {
		t.Fatalf("end: %+v", st)
	}
	for _, s := range h.ceremoniesSince(mark) {
		if s.Rekey {
			t.Fatalf("a plain removal says it re-keys: %+v", s)
		}
	}
	if g := h.generation(); g != 1 {
		t.Fatalf("generation %d", g)
	}
	img := fileBytes(t, h.vault)
	a, b := superblocks(img)
	rid := ridOf(t, second)
	for _, sb := range []*format.KeystoreSuperblock{a, b} {
		for _, r := range recordsNamed(t, img, sb) {
			if r.RecipientID == rid {
				t.Fatal("a superblock copy still names a region with the slot")
			}
		}
	}
	if holdsID(t, h.vault, rid) {
		t.Fatal("the raw bytes still hold the removed slot")
	}
}

// 3. What the invariant, the slot list and the freeze refuse is refused
// before any proof: no ceremony begins.
func TestRemoveSlotIsRefusedBeforeTheProof(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	pw := h.slot("Password")
	if pw.Removable {
		t.Fatal("the fixture: the password is removable")
	}
	mark := h.mark()
	if e := h.c.RemoveSlot(pw.RecipientID, true, ""); !isCode(e, CodeInvariant) {
		t.Fatalf("a removal the invariant forbids: %v", e)
	}
	if e := h.c.RemoveSlot(strings.Repeat("ab", 16), true, ""); !isCode(e, CodeSlotNotFound) {
		t.Fatalf("an unknown slot: %v", e)
	}
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = keystore.ErrTampered, CodeTamperedHash
	h.c.mu.Unlock()
	if e := h.c.RemoveSlot(strings.Repeat("ab", 16), true, ""); !isCode(e, CodeVaultTampered) {
		t.Fatalf("the freeze answers first: %v", e)
	}
	h.noCeremonySince(mark, "a refused removal")
}

// 4. A re-keying removal of the vault's only YubiKey, with two recovery keys
// left, proves with a recovery key and never waits for the YubiKey removed;
// the plain removal still waits for a YubiKey, as before.
func TestRemovingTheOnlyYubiKeyProvesWithARecoveryKey(t *testing.T) {
	setup := func() (*harness, *fakeCards, string) {
		card, pub := cardWithKey("123456")
		cards := readerWith(card)
		r1, d1 := newRecoverySpec(t, "Paper")
		r2, _ := newRecoverySpec(t, "Spare")
		h := rekeyHarness(t, cards, "", d1, keystore.HardwareSlot{PublicKey: pub, Label: "A"}, r1, r2)
		h.unlockWithToken()
		return h, cards, d1
	}
	h, _, digits := setup()
	a := h.slot("A").RecipientID
	h.rec.reset()
	if e := h.c.RemoveSlot(a, true, ""); e != nil {
		t.Fatal(e)
	}
	first := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.PromptID != ""
	}).(CeremonyState)
	if first.Step != StepRecovery || first.Method != string(MethodRecovery) || first.Without != a {
		t.Fatalf("the first prompt: %+v", first)
	}
	if e := h.c.SubmitSecret("recovery", first.PromptID, digits); e != nil {
		t.Fatal(e)
	}
	h.done()
	if g := h.generation(); g != 2 {
		t.Fatalf("generation %d", g)
	}
	for _, s := range h.c.Slots() {
		if s.RecipientID == a {
			t.Fatal("the YubiKey is still listed")
		}
	}

	// The plain removal of the same YubiKey waits for a YubiKey.
	h2, cards2, _ := setup()
	cards2.setReaders()
	h2.rec.reset()
	if e := h2.c.RemoveSlot(h2.slot("A").RecipientID, false, ""); e != nil {
		t.Fatal(e)
	}
	wait := h2.rec.waitCeremony(t, StepWaitingForKey, false)
	h2.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Method == string(MethodToken)
	})
	if wait.Without != "" {
		t.Fatalf("a plain removal leaves the YubiKey out of its proof: %+v", wait)
	}
	h2.c.CancelUnlock()
	h2.end()
	for _, s := range h2.ceremoniesSince(0) {
		if s.Step == StepRecovery {
			t.Fatalf("a plain removal asked for a recovery key: %+v", s)
		}
	}
}

// 5. The backup is written after the change and its retirement, at the new
// generation, holding the recovery keys alone, and the stamp records both.
func TestRemoveSlotBacksUpAfterTheChange(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	path := filepath.Join(h.dir, "backup.eks")
	mark := h.mark()
	st := h.removeOnPassword(h.slot("Second").RecipientID, true, path)
	if st.Step != StepDone || !st.BackedUp || st.BackupError != "" {
		t.Fatalf("end: %+v", st)
	}
	prompts := 0
	for _, s := range h.ceremoniesSince(mark) {
		if s.PromptID != "" {
			prompts++
		}
	}
	if prompts != 1 {
		t.Fatalf("%d prompts for one act with a backup", prompts)
	}
	ks, err := keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	if ks.Generation() != 2 {
		t.Fatalf("the backup is at generation %d", ks.Generation())
	}
	for _, s := range ks.Slots() {
		if s.Type != format.SlotRecovery {
			t.Fatalf("the backup holds a method other than a recovery key: %+v", s)
		}
	}
	rk, err := kdf.ParseRecoveryDigits(h.recovery)
	if err != nil {
		t.Fatal(err)
	}
	unl, err := ks.Unlock(keystore.RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatalf("the backup does not open with the recovery key: %v", err)
	}
	unl.Close()
	if h.c.LastExportAt() == 0 {
		t.Fatal("no stamp")
	}
	h.c.mu.Lock()
	le := *h.c.settings.LastExport
	h.c.mu.Unlock()
	if le.Gen != 2 || !slices.Equal(le.Recovery, []string{h.recoveryViews()[0].RecipientID}) {
		t.Fatalf("the stamp: %+v", le)
	}
	if h.c.BackupStatus().VaultRekeyed {
		t.Fatal("a backup made after the re-key reads as behind it")
	}
}

// 6. A backup that fails leaves the act Done: the change stands, the stamp
// does not move, and the card says the last backup was before the re-key.
func TestABackupThatFailsLeavesTheActDone(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	if e := h.exportBackup(filepath.Join(h.dir, "first.eks")); e != nil {
		t.Fatal(e)
	}
	at := h.c.LastExportAt()
	h.clk.Advance(time.Minute)
	path := filepath.Join(h.dir, "taken.eks")
	if err := os.WriteFile(path, []byte("already here"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := h.removeOnPassword(h.slot("Second").RecipientID, true, path)
	if st.Step != StepDone || st.BackedUp || st.BackupError != CodeArchiveExists {
		t.Fatalf("end: %+v", st)
	}
	if g := h.generation(); g != 2 {
		t.Fatalf("generation %d", g)
	}
	if got := h.c.LastExportAt(); got != at {
		t.Fatalf("the stamp moved: %d, was %d", got, at)
	}
	if b, _ := os.ReadFile(path); string(b) != "already here" {
		t.Fatal("the file in the way was written")
	}
	if !h.c.BackupStatus().VaultRekeyed {
		t.Fatal("the backup made at generation 1 does not read as behind")
	}
}

// 7. Refusals of a backup's place and of turning the password off with a
// re-key or a backup come at once, before any ceremony.
func TestBackupRefusals(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	second := h.slot("Second").RecipientID
	mark := h.mark()
	reserved := filepath.Join(h.dir, stagingName(incomingPrefix))
	inData := filepath.Join(h.c.deps.DataDir, "backup.eks")
	for _, p := range []string{reserved, inData} {
		if e := h.c.RemoveSlot(second, true, p); !isCode(e, CodeParams) {
			t.Fatalf("a backup at %s: %v", p, e)
		}
		if e := h.c.RotateNow(p); !isCode(e, CodeParams) {
			t.Fatalf("a re-key's backup at %s: %v", p, e)
		}
		if e := h.c.ReplaceRecoveryKeys(p); !isCode(e, CodeParams) {
			t.Fatalf("a replacement's backup at %s: %v", p, e)
		}
	}
	if e := h.c.SetEntangled(false, true, ""); !isCode(e, CodeParams) {
		t.Fatalf("turning the password off with a re-key: %v", e)
	}
	if e := h.c.SetEntangled(false, false, filepath.Join(h.dir, "b.eks")); !isCode(e, CodeParams) {
		t.Fatalf("turning the password off with a backup: %v", e)
	}
	h.noCeremonySince(mark, "a refused backup")

	// A backup after the act would hold no recovery key: refused too.
	cardA, pubA := cardWithKey("123456")
	_, pubB := cardWithKey("654321")
	r, digits := newRecoverySpec(t, "Paper")
	hw := rekeyHarness(t, readerWith(cardA), "", digits,
		keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, keystore.HardwareSlot{PublicKey: pubB, Label: "B"}, r)
	hw.unlockWithToken()
	mark = hw.mark()
	if e := hw.c.RemoveSlot(hw.slot("Paper").RecipientID, false, filepath.Join(hw.dir, "b.eks")); !isCode(e, CodeNoRecoverySlot) {
		t.Fatalf("a backup with no recovery key left: %v", e)
	}
	hw.noCeremonySince(mark, "a backup with no recovery key")
}

// ---- 8-12: locks, failed and unknown retirements.

// 8. A lock that lands after the commit leaves the act Done, not Failed:
// the re-key is on disk, the new keys are not installed behind a Locked
// state, and the backup is not written behind it either.
func TestRekeyEndsDoneWhenALockLandsAfterTheCommit(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	second := h.slot("Second").RecipientID
	h.c.seams.rekeyed = func() { h.c.LockNow(ReasonManual) }
	path := filepath.Join(h.dir, "backup.eks")
	st := h.removeOnPassword(second, true, path)
	if st.Step != StepDone || st.Error != "" {
		t.Fatalf("end: %+v", st)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a backup was written behind a Locked state: %v", err)
	}
	h.settleLock()
	h.c.seams.rekeyed = nil
	h.unlockWithPassword()
	if g := h.generation(); g != 2 {
		t.Fatalf("generation %d", g)
	}
	for _, s := range h.c.Slots() {
		if s.RecipientID == second {
			t.Fatal("the slot is still listed")
		}
	}
}

// 9. The retirement runs whatever lands between the two commits: the handle
// is still the ceremony's, and the lock's close waits for it.
func TestTheRetirementRunsWhenALockLandsBetweenTheCommits(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	rid := ridOf(t, h.slot("Second").RecipientID)
	h.c.seams.committed = func() { h.c.LockNow(ReasonManual) }
	if st := h.removeOnPassword(hexID(rid), true, ""); st.Step != StepDone {
		t.Fatalf("end: %+v", st)
	}
	h.settleLock()
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	if h.status().RetireOwed {
		t.Fatal("the file owes a retirement after the act")
	}
	if holdsID(t, h.vault, rid) {
		t.Fatal("the raw bytes still hold the removed slot")
	}
}

// owedAfterRemoval removes "Second" from a three-password vault with every
// retiring write failing from the moment the change has landed: the act
// ends Done with RetireError, the file owed and the warning standing. The
// switch is left failing every region write.
func owedAfterRemoval(t *testing.T, h *harness, rekey bool) *faultSwitch {
	t.Helper()
	f := installSwitch(t)
	h.c.seams.committed = func() { f.set(everyRegion) }
	st := h.removeOnPassword(h.slot("Second").RecipientID, rekey, "")
	h.c.seams.committed = nil
	if st.Step != StepDone || st.RetireError == "" || st.RetireRemnant {
		t.Fatalf("end: %+v", st)
	}
	if !h.warned(CodeRetireOwed) || !h.owes() {
		t.Fatalf("the act left no owed retirement: %+v", h.status())
	}
	return f
}

// 10. A retirement that fails leaves the act Done and the file owed: content
// goes on, a new record waits, and the first held mutation finishes the
// retirement once it can. A receipt's carried pass finishes it as well.
func TestARetirementThatFailsLeavesTheActDoneAndOwed(t *testing.T) {
	setup := func() (*harness, string, string) {
		h := passwordsHarness(t, 3)
		h.unlockWithPassword()
		id := h.openArchive(t, "A")
		h.add(t, id, rootID, PolicySkip, h.src(t, "one.txt", "one"))
		return h, id, h.slot("Second").RecipientID
	}

	t.Run("a held mutation finishes it", func(t *testing.T) {
		h, id, second := setup()
		f := owedAfterRemoval(t, h, true)
		if !holdsID(t, h.vault, ridOf(t, second)) {
			t.Fatal("the control: the raw bytes do not hold the removed slot")
		}
		p := filepath.Join(h.dir, "new.enf")
		if _, e := h.c.CreateArchive(p, "New", compressionNormal); !isCode(e, CodeRetireOwed) {
			t.Fatalf("a create while owed: %v", e)
		}
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("the refused create left its file")
		}
		rows := h.page(t, id, rootID).Rows
		if len(rows) != 1 {
			t.Fatalf("the listing: %+v", rows)
		}
		h.extractsAll(t, id, rows)
		seq := h.record(id).LastSeq
		if o := h.add(t, id, rootID, PolicySkip, h.src(t, "two.txt", "two")); o.Error != "" {
			t.Fatalf("an add while owed: %+v", o)
		}
		if h.record(id).LastSeq <= seq {
			t.Fatal("the add's receipt did not land")
		}
		if !h.owes() || !h.warned(CodeRetireOwed) {
			t.Fatal("the receipt claimed the retirement")
		}
		f.set(nil)
		w := watchWrites(t)
		if _, e := h.c.CreateArchive(p, "New", compressionNormal); e != nil {
			t.Fatalf("a create once the fault is gone: %v", e)
		}
		ws := w.take()
		// The retiring commit (a region, a registry, a superblock) lands
		// before the create's own (a registry and a superblock).
		if len(ws) == 0 || ws[0] != "region" || count(ws, "region") != 1 || count(ws, "superblock") != 2 {
			t.Fatalf("the writes: %v", ws)
		}
		if h.owes() || h.warned(CodeRetireOwed) {
			t.Fatal("still owed after the create")
		}
		if holdsID(t, h.vault, ridOf(t, second)) {
			t.Fatal("the raw bytes still hold the removed slot")
		}
		requireCleanArea(t, h.vault, "after the admitted create")
	})

	t.Run("a receipt finishes it", func(t *testing.T) {
		h, id, second := setup()
		f := owedAfterRemoval(t, h, true)
		f.set(nil)
		if o := h.add(t, id, rootID, PolicySkip, h.src(t, "two.txt", "two")); o.Error != "" {
			t.Fatalf("an add: %+v", o)
		}
		if h.owes() || h.warned(CodeRetireOwed) {
			t.Fatal("the receipt's carried pass did not finish the retirement")
		}
		if holdsID(t, h.vault, ridOf(t, second)) {
			t.Fatal("the raw bytes still hold the removed slot")
		}
	})
}

// 11. The next unlock finishes an owed retirement before the vault is used:
// before the Session, the receipts and the purge. While it cannot, the vault
// opens readable, the warning standing, and what is held waits.
func TestUnlockFinishesAnOwedRetirement(t *testing.T) {
	setup := func() (*harness, *faultSwitch, string, string) {
		h := passwordsHarness(t, 3)
		h.unlockWithPassword()
		id := h.openArchive(t, "A")
		h.newArchive("Other")
		gone, _ := h.newArchive("Gone")
		if e := h.c.ForgetArchive(gone); e != nil {
			t.Fatal(e)
		}
		h.editRecord(gone, func(a *format.ArchiveRecord, at int64) {
			a.ForgottenAt = at - format.ForgottenRetentionSeconds - 60
		})
		f := owedAfterRemoval(t, h, true)
		h.lockAndSettle()
		if st := h.status(); !slices.Contains(st.Warnings, CodeRetireOwed) || !st.RetireOwed {
			t.Fatalf("the lock screen: %+v", st)
		}
		// An add to the archive left open across the lock owes its receipt.
		opID, e := h.c.AddFiles(id, rootID, []string{h.src(t, "late.txt", "late")}, PolicySkip)
		if e != nil {
			t.Fatal(e)
		}
		if o := h.rec.waitOp(t, opID); o.Error != "" {
			t.Fatalf("the add while locked: %+v", o)
		}
		return h, f, id, gone
	}

	t.Run("fault cleared", func(t *testing.T) {
		h, f, _, gone := setup()
		f.set(nil)
		w := watchWrites(t)
		h.rec.reset()
		if e := h.c.BeginUnlock(MethodPassword); e != nil {
			t.Fatal(e)
		}
		h.proveWithPassword()
		first := h.rec.waitFor(t, EventVaultState, func(x any) bool {
			s, ok := x.(VaultStatus)
			return ok && s.State == StateUnlocked
		}).(VaultStatus)
		if first.RetireOwed || slices.Contains(first.Warnings, CodeRetireOwed) {
			t.Fatalf("the first Unlocked state: %+v", first)
		}
		if g := h.generation(); g != 2 {
			t.Fatalf("generation %d", g)
		}
		// The retiring commit's writes come before the receipt's and the
		// purge's: a region write before every registry-only commit.
		ws := w.take()
		if len(ws) == 0 || ws[0] != "region" || count(ws, "region") != 1 || count(ws, "superblock") < 3 {
			t.Fatalf("the unlock's writes: %v", ws)
		}
		if h.hasRecord(gone) {
			t.Fatal("the purge did not run after the retirement")
		}
		list, _ := h.c.ListArchives(true)
		for _, a := range list {
			if a.ReceiptOwed {
				t.Fatalf("a receipt still owed: %+v", a)
			}
		}
	})

	t.Run("fault still set", func(t *testing.T) {
		h, f, id, gone := setup()
		h.unlockWithPassword()
		if !h.warned(CodeRetireOwed) || !h.owes() {
			t.Fatalf("the unlock: %+v", h.status())
		}
		if rows := h.page(t, id, rootID).Rows; len(rows) != 1 {
			t.Fatalf("the listing: %+v", rows)
		}
		seq := h.record(id).LastSeq
		if o := h.add(t, id, rootID, PolicySkip, h.src(t, "more.txt", "more")); o.Error != "" {
			t.Fatalf("an add: %+v", o)
		}
		if h.record(id).LastSeq <= seq {
			t.Fatal("the receipt did not land")
		}
		var other string
		list, _ := h.c.ListArchives(true)
		for _, a := range list {
			if a.Name == "Other" {
				other = a.ID
			}
		}
		if e := h.c.ForgetArchive(other); !isCode(e, CodeRetireOwed) {
			t.Fatalf("a forget while owed: %v", e)
		}
		if !h.hasRecord(gone) {
			t.Fatal("the purge ran while a retirement was owed")
		}
		h.lockAndSettle()
		f.set(nil)
		h.unlockWithPassword()
		if h.hasRecord(gone) {
			t.Fatal("the purge did not run at the unlock after the fault cleared")
		}
	})
}

// 12. A retirement whose outcome is unknown enters Broken, at the act and at
// the unlock; the reopen settles it, and the change stands.
func TestARetirementThatCannotBeConfirmedIsBroken(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	rid := ridOf(t, h.slot("Second").RecipientID)
	h.c.seams.failAt = func(p string) error {
		if p == "retire" {
			return keystore.ErrIndeterminate
		}
		return nil
	}
	st := h.removeOnPassword(hexID(rid), true, "")
	if st.Step != StepFailed || st.Error != CodeIndeterminate {
		t.Fatalf("end: %+v", st)
	}
	h.rec.waitState(t, StateBroken)

	// The unlock of the owed file meets the same answer: Failed, Broken.
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodPassword); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	if st := h.end(); st.Step != StepFailed || st.Error != CodeIndeterminate {
		t.Fatalf("the unlock: %+v", st)
	}
	if s := h.status().State; s != StateBroken {
		t.Fatalf("state %s", s)
	}

	h.c.seams.failAt = nil
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	h.unlockWithPassword()
	if g := h.generation(); g != 2 {
		t.Fatalf("generation %d", g)
	}
	if h.owes() {
		t.Fatal("still owed after the unlock")
	}
	for _, s := range h.c.Slots() {
		if s.RecipientID == hexID(rid) {
			t.Fatal("the slot is still listed")
		}
	}
}

// ---- 13-18: the password, the re-key, vaultErr, the inspection.

// 13. A password change re-keys in the same commit: kind "password", one
// chosen password, generation 2, and the old password no longer opens.
func TestChangeEntangledPasswordAndRekey(t *testing.T) {
	h, _, _ := entangledHarness(t, "the first password")
	h.unlockWithToken()
	mark := h.mark()
	h.rec.reset()
	if e := h.c.ChangeEntangledPassword(true, ""); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	h.chooseNew("the second password")
	h.done()
	chosen := 0
	for _, s := range h.ceremoniesSince(mark) {
		if s.Kind != "password" || !s.Rekey {
			t.Fatalf("an event of the act: %+v", s)
		}
		if s.Step == StepPassword && s.PromptID != "" && s.Choose {
			chosen++
		}
	}
	if chosen != 1 {
		t.Fatalf("%d chosen-password prompts", chosen)
	}
	if g := h.generation(); g != 2 {
		t.Fatalf("generation %d", g)
	}
	h.lockAndSettle()
	h.tokenPasswordRefused("the first password", "the second password")
}

// tokenPasswordRefused unlocks with the card: old is asked again with
// vault.auth, and good opens.
func (h *harness) tokenPasswordRefused(old, good string) {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		h.t.Fatal(e)
	}
	pin := h.rec.waitCeremony(h.t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	first := h.rec.waitCeremony(h.t, StepPassword, true)
	h.c.SubmitSecret("password", first.PromptID, old)
	again := h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != first.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		h.t.Fatalf("the old password was not refused: %+v", again)
	}
	h.c.SubmitSecret("password", again.PromptID, good)
	h.rec.waitState(h.t, StateUnlocked)
	h.entangled = good
}

// 14. Without the re-key the change keeps the generation, kind "password".
func TestChangeEntangledPasswordWithoutRekey(t *testing.T) {
	h, _, _ := entangledHarness(t, "the first password")
	h.unlockWithToken()
	mark := h.mark()
	h.rec.reset()
	if e := h.c.ChangeEntangledPassword(false, ""); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	h.chooseNew("the second password")
	h.done()
	for _, s := range h.ceremoniesSince(mark) {
		if s.Kind != "password" || s.Rekey {
			t.Fatalf("an event of the act: %+v", s)
		}
	}
	if g := h.generation(); g != 1 {
		t.Fatalf("generation %d", g)
	}
}

// 15. Turning the password on re-keys in the same commit when asked to.
func TestTurnOnAndRekey(t *testing.T) {
	h, _, _ := entangledHarness(t, "")
	h.unlockWithToken()
	mark := h.mark()
	h.rec.reset()
	if e := h.c.SetEntangled(true, true, ""); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	h.chooseNew("a new vault password")
	h.done()
	chosen := 0
	for _, s := range h.ceremoniesSince(mark) {
		if s.Kind != "entangle" || !s.Rekey {
			t.Fatalf("an event of the act: %+v", s)
		}
		if s.Choose && s.PromptID != "" {
			chosen++
		}
	}
	if chosen != 1 {
		t.Fatalf("%d chosen-password prompts", chosen)
	}
	if g := h.generation(); g != 2 || !h.status().Entangled {
		t.Fatalf("generation %d, %+v", g, h.status())
	}
	h.lockAndSettle()
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	pw := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", pw.PromptID, "a new vault password")
	h.rec.waitState(t, StateUnlocked)

	plain, _, _ := entangledHarness(t, "")
	plain.unlockWithToken()
	plain.rec.reset()
	if e := plain.c.SetEntangled(true, false, ""); e != nil {
		t.Fatal(e)
	}
	plain.answerTokenUnlock("123456")
	plain.chooseNew("a new vault password")
	plain.done()
	if g := plain.generation(); g != 1 {
		t.Fatalf("turning it on without a re-key: generation %d", g)
	}
}

// 16. A re-key's backup is written after it, at the new key; one that fails
// leaves the re-key standing.
func TestRotateNowBacksUpAfter(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	path := filepath.Join(h.dir, "backup.eks")
	rotate := func() CeremonyState {
		h.rec.reset()
		if e := h.c.RotateNow(path); e != nil {
			t.Fatal(e)
		}
		h.proveWithPassword()
		return h.done()
	}
	st := rotate()
	if !st.BackedUp || st.Rekey || st.Kind != "rotate" {
		t.Fatalf("end: %+v", st)
	}
	if g := h.generation(); g != 2 {
		t.Fatalf("generation %d", g)
	}
	ks, err := keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if ks.Generation() != 2 {
		t.Fatalf("the backup is at generation %d", ks.Generation())
	}
	ks.Close()
	before := fileBytes(t, path)
	st = rotate()
	if st.BackedUp || st.BackupError == "" {
		t.Fatalf("the second re-key's end: %+v", st)
	}
	if g := h.generation(); g != 3 {
		t.Fatalf("generation %d", g)
	}
	if !bytes.Equal(fileBytes(t, path), before) {
		t.Fatal("the backup in the way was written over")
	}
}

// 17. A record of the vault file that does not open is the vault's
// problem: vault.invalid, never archive.invalid. Anything else passes.
func TestVaultErrMapsAFileThatDoesNotOpen(t *testing.T) {
	bad := fmt.Errorf("keystore: the entangled_key record: %w", format.ErrInvalid)
	if c := classify(bad).Code; c != CodeArchiveInvalid {
		t.Fatalf("the control: %s", c)
	}
	if c := classify(vaultErr(bad)).Code; c != CodeVaultInvalid {
		t.Fatalf("vaultErr: %s", c)
	}
	other := fmt.Errorf("io: %w", fs.ErrPermission)
	if got := vaultErr(other); got != other {
		t.Fatalf("another error changed: %v", got)
	}
	if vaultErr(nil) != nil {
		t.Fatal("nil changed")
	}

	// Through an act: a re-key over a recovery key's escrow record that does
	// not open fails closed (keystore's TestRemoveSlotAndRotateFailsClosed),
	// and the act's end says the vault's file, not an archive's.
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	rid := ridOf(t, h.recoveryViews()[0].RecipientID)
	if e := h.c.updateRegistry(func(g *registry) error {
		rec := g.Secret(format.SecretRecoveryEscrow, rid)
		if rec == nil {
			return errors.New("the fixture: no escrow record for the recovery key")
		}
		damaged := *rec
		damaged.Ciphertext[0] ^= 1
		g.SetSecret(damaged)
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	before := fileBytes(t, h.vault)
	h.rec.reset()
	if e := h.c.RotateNow(""); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	if st := h.end(); st.Step != StepFailed || st.Error != CodeVaultInvalid {
		t.Fatalf("a re-key over a record that does not open: %+v", st)
	}
	if !bytes.Equal(fileBytes(t, h.vault), before) || h.generation() != 1 {
		t.Fatal("the failed re-key changed the file")
	}
}

// 18. A backup made before a re-key reads as behind it, in the file's
// inspection and on the backups card; a new one does not.
func TestInspectFileSaysReKeyedSince(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	old := filepath.Join(h.dir, "old.eks")
	if e := h.exportBackup(old); e != nil {
		t.Fatal(e)
	}
	h.rec.reset()
	if e := h.c.RotateNow(""); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.done()
	info, e := h.c.InspectFile(old)
	if e != nil || !info.VaultMatches || !info.Rekeyed {
		t.Fatalf("the old backup: %+v %v", info, e)
	}
	if !h.c.BackupStatus().VaultRekeyed {
		t.Fatal("the card does not say the backup is behind")
	}
	// A stamp written before the stamp carried the generation and the
	// recovery keys (a settings file of an earlier version) knows nothing of
	// either, and claims neither.
	h.c.mu.Lock()
	stamp := *h.c.settings.LastExport
	h.c.settings.LastExport.Gen, h.c.settings.LastExport.Recovery = 0, nil
	h.c.mu.Unlock()
	if bs := h.c.BackupStatus(); bs.VaultRekeyed || bs.KeysReplaced || bs.At != stamp.At {
		t.Fatalf("a stamp without its generation: %+v", bs)
	}
	h.c.mu.Lock()
	*h.c.settings.LastExport = stamp
	h.c.mu.Unlock()
	fresh := filepath.Join(h.dir, "fresh.eks")
	if e := h.exportBackup(fresh); e != nil {
		t.Fatal(e)
	}
	info, e = h.c.InspectFile(fresh)
	if e != nil || info.Rekeyed {
		t.Fatalf("the new backup: %+v %v", info, e)
	}
	if h.c.BackupStatus().VaultRekeyed {
		t.Fatal("the card calls the new backup behind")
	}
}

// ---- 19-24: Replace recovery keys.

// 19. One ceremony: one proof, the new key shown and kept, one
// confirmation, nothing written before it, then one commit that replaces the
// key and re-keys the vault.
func TestReplaceRecoveryKeysIsOneCeremony(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	old := h.recoveryViews()[0]
	before := fileBytes(t, h.vault)
	mark := h.mark()
	h.rec.reset()
	if e := h.c.ReplaceRecoveryKeys(""); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	keep := h.rec.waitCeremony(t, StepKeepKey, true)
	if keep.KeepIndex != 1 || keep.KeepCount != 1 || !slices.Equal(keep.OldIDs, []string{old.RecoveryID}) ||
		keep.RecoveryID == "" || keep.RecoveryID == old.RecoveryID || !strings.HasPrefix(keep.SlotLabel, "http://127.0.0.1:") || keep.Kind != "replace" || !keep.Rekey {
		t.Fatalf("the keep step: %+v", keep)
	}
	digits, _, _, ok := h.c.preview.secretValue(secretToken(keep.SlotLabel))
	if !ok {
		t.Fatal("the new key is not behind its URL")
	}
	if e := h.c.SubmitSecret("kept", keep.PromptID, ""); e != nil {
		t.Fatal(e)
	}
	confirm := h.rec.waitCeremony(t, StepConfirm, true)
	if !bytes.Equal(fileBytes(t, h.vault), before) {
		t.Fatal("something was written before the confirmation")
	}
	if e := h.c.SubmitSecret("confirm", confirm.PromptID, ""); e != nil {
		t.Fatal(e)
	}
	end := h.done()
	if !end.Rekey || !slices.Equal(end.NewIDs, []string{keep.RecoveryID}) || !slices.Equal(end.OldIDs, []string{old.RecoveryID}) {
		t.Fatalf("end: %+v", end)
	}
	states := h.ceremoniesSince(mark)
	commit := 0
	for i, s := range states {
		if s.Phase == "commit" && (i == 0 || states[i-1].Phase != "commit") {
			commit++
		}
	}
	if commit != 1 {
		t.Fatalf("%d commit phases", commit)
	}
	if _, _, _, ok := h.c.preview.secretValue(secretToken(keep.SlotLabel)); ok {
		t.Fatal("a handle the act minted outlived its Done")
	}
	if g := h.generation(); g != 2 || h.owes() {
		t.Fatalf("generation %d, owed %v", g, h.owes())
	}
	slots := h.c.Slots()
	rec := h.recoveryViews()
	if len(slots) != 2 || len(rec) != 1 || rec[0].RecoveryID != keep.RecoveryID || rec[0].Label != old.Label {
		t.Fatalf("the slots: %+v", slots)
	}
	h.lockAndSettle()
	h.recoveryRefused(h.recovery)
	h.unlockWithRecovery(digits)
	if holdsID(t, h.vault, ridOf(t, old.RecipientID)) {
		t.Fatal("the raw bytes still hold the old recovery key's ID")
	}
}

// replaceHarness is a password, "safe" and "office": a replacement makes two
// keys.
func replaceHarness(t *testing.T) (*harness, []string) {
	t.Helper()
	safe, d1 := newRecoverySpec(t, "safe")
	office, d2 := newRecoverySpec(t, "office")
	h := rekeyHarness(t, nil, "", d1, keystore.PasswordSlot{Password: testPassword, Argon2: fast, Label: "Password"}, safe, office)
	h.unlockWithPassword()
	return h, []string{d1, d2}
}

// 20. As many keys as there were, one at a time, in the order the slots are
// listed, each taking the label of the one it replaces.
func TestReplaceRecoveryKeysKeepsTheCountAndTheLabels(t *testing.T) {
	h, _ := replaceHarness(t)
	olds := h.recoveryViews()
	h.rec.reset()
	if e := h.c.ReplaceRecoveryKeys(""); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	first := h.rec.waitCeremony(t, StepKeepKey, true)
	if first.KeepIndex != 1 || first.KeepCount != 2 || len(first.NewIDs) != 1 {
		t.Fatalf("the first keep step: %+v", first)
	}
	for _, s := range h.ceremoniesSince(0) {
		if s.KeepIndex == 2 {
			t.Fatalf("the second key was shown before the first was kept: %+v", s)
		}
	}
	h.c.SubmitSecret("kept", first.PromptID, "")
	second := h.rec.waitCeremony(t, StepKeepKey, true)
	if second.KeepIndex != 2 || second.KeepCount != 2 || !slices.Equal(second.NewIDs, []string{first.RecoveryID, second.RecoveryID}) {
		t.Fatalf("the second keep step: %+v", second)
	}
	want := []string{olds[0].RecoveryID, olds[1].RecoveryID}
	if !slices.Equal(second.OldIDs, want) {
		t.Fatalf("the old IDs %v, the slots list %v", second.OldIDs, want)
	}
	h.c.SubmitSecret("kept", second.PromptID, "")
	h.confirmReplacement()
	end := h.done()
	labels := map[string]string{}
	for _, s := range h.recoveryViews() {
		labels[s.RecoveryID] = s.Label
	}
	for i, id := range end.NewIDs {
		if labels[id] != olds[i].Label {
			t.Fatalf("the new key %s is labelled %q, the one it replaces %q", id, labels[id], olds[i].Label)
		}
	}
	if len(labels) != 2 {
		t.Fatalf("the recovery keys: %v", labels)
	}
}

// 21. A cancel, at either keep step or at the confirmation, or a lock at a
// keep step, changes nothing, and the act drops every handle it minted.
func TestReplaceRecoveryKeysCancelledChangesNothing(t *testing.T) {
	cases := []struct {
		name string
		at   func(h *harness) // reaches the step and stops the act there
	}{
		{"first keep", func(h *harness) {
			h.rec.waitCeremony(h.t, StepKeepKey, true)
			h.c.CancelUnlock()
		}},
		{"second keep", func(h *harness) {
			k := h.rec.waitCeremony(h.t, StepKeepKey, true)
			h.c.SubmitSecret("kept", k.PromptID, "")
			h.rec.waitFor(h.t, EventVaultCeremony, func(x any) bool {
				s, ok := x.(CeremonyState)
				return ok && s.Step == StepKeepKey && s.KeepIndex == 2 && s.PromptID != ""
			})
			h.c.CancelUnlock()
		}},
		{"confirm", func(h *harness) {
			for range 2 {
				k := h.rec.waitCeremony(h.t, StepKeepKey, true)
				h.c.SubmitSecret("kept", k.PromptID, "")
			}
			h.rec.waitCeremony(h.t, StepConfirm, true)
			h.c.CancelUnlock()
		}},
		{"a lock at a keep step", func(h *harness) {
			h.rec.waitCeremony(h.t, StepKeepKey, true)
			h.c.LockNow(ReasonIdle)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, digits := replaceHarness(t)
			before := fileBytes(t, h.vault)
			mark := h.mark()
			h.rec.reset()
			if e := h.c.ReplaceRecoveryKeys(""); e != nil {
				t.Fatal(e)
			}
			h.proveWithPassword()
			tc.at(h)
			end := h.end()
			if end.Step != StepFailed || end.Error != CodeCancelled {
				t.Fatalf("end: %+v", end)
			}
			for _, s := range h.ceremoniesSince(mark) {
				if s.Step == StepKeepKey && s.SlotLabel != "" {
					if _, _, _, ok := h.c.preview.secretValue(secretToken(s.SlotLabel)); ok {
						t.Fatalf("a minted handle outlived the act: %s", s.SlotLabel)
					}
				}
			}
			if !bytes.Equal(fileBytes(t, h.vault), before) {
				t.Fatal("the file changed")
			}
			if g := h.generation(); g != 1 {
				t.Fatalf("generation %d", g)
			}
			if h.status().State == StateUnlocked {
				h.lockAndSettle()
			} else {
				h.settleLock()
			}
			h.unlockWithRecovery(digits[1])
			h.lockAndSettle()
			h.unlockWithRecovery(digits[0])
		})
	}
}

// 22. A failed commit after the confirmation changes nothing, and what a
// commit that failed after writing left in the file is retired at once.
func TestReplaceRecoveryKeysFailedCommitChangesNothing(t *testing.T) {
	ioErr := &fs.PathError{Op: "write", Path: "vault.eks", Err: fs.ErrPermission}
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	before := fileBytes(t, h.vault)
	h.c.seams.failAt = func(p string) error {
		if p == "commit" {
			return ioErr
		}
		return nil
	}
	_, end := h.replaceOnPassword("")
	if end.Step != StepFailed || end.Error != CodeIO || len(end.NewIDs) != 1 {
		t.Fatalf("end: %+v", end)
	}
	if !bytes.Equal(fileBytes(t, h.vault), before) {
		t.Fatal("the file changed")
	}
	if g := h.generation(); g != 1 {
		t.Fatalf("generation %d", g)
	}

	// A real write that fails after the region and the registry landed.
	h2 := newHarness(t, nil, nil)
	h2.unlockWithPassword()
	var mu sync.Mutex
	var stray []format.SlotRecord
	failed := false
	faultOn(t, func(step string) error {
		mu.Lock()
		defer mu.Unlock()
		if step != "sync" || failed {
			return nil
		}
		failed = true
		stray = strayRecords(h2.vault)
		return ioErr
	})
	_, end = h2.replaceOnPassword("")
	if end.Step != StepFailed || end.Error != CodeIO || len(end.NewIDs) != 1 {
		t.Fatalf("end: %+v", end)
	}
	mu.Lock()
	got := stray
	mu.Unlock()
	if len(got) != 1 || recoveryID(got[0].RecipientID) != end.NewIDs[0] {
		t.Fatalf("the control: the failed commit's region holds %d new records", len(got))
	}
	if h2.owes() || h2.warned(CodeRetireOwed) {
		t.Fatal("the act's own retirement did not run at once")
	}
	if holdsRecord(t, h2.vault, got[0]) {
		t.Fatal("the raw bytes still hold the key never added")
	}
	requireCleanArea(t, h2.vault, "after the failed replacement")
	requireFileFacts(t, h2, "after the failed replacement's retirement")
	if g := h2.generation(); g != 1 {
		t.Fatalf("generation %d", g)
	}
	h2.lockAndSettle()
	h2.unlockWithRecovery(h2.recovery)
}

// 23. A replacement the vault cannot take is refused before the proof.
func TestReplaceRecoveryKeysRefusedBeforeTheProof(t *testing.T) {
	r1, d1 := newRecoverySpec(t, "one")
	r2, _ := newRecoverySpec(t, "two")
	only := rekeyHarness(t, nil, "", d1, r1, r2)
	only.unlockWithRecovery(d1)
	mark := only.mark()
	if e := only.c.ReplaceRecoveryKeys(""); !isCode(e, CodeSetupNeeded) {
		t.Fatalf("recovery keys alone: %v", e)
	}
	only.noCeremonySince(mark, "a refused replacement")

	c2, rec2 := hardwareOnlyCore(t)
	n := len(rec2.snapshot())
	if e := c2.ReplaceRecoveryKeys(""); !isCode(e, CodeNoRecoverySlot) {
		t.Fatalf("no recovery key: %v", e)
	}
	for _, e := range rec2.snapshot()[n:] {
		if _, ok := e.payload.(CeremonyState); ok {
			t.Fatal("a replacement with nothing to replace began a ceremony")
		}
	}

	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	mark = h.mark()
	if e := h.c.ReplaceRecoveryKeys(filepath.Join(h.c.deps.DataDir, "b.eks")); !isCode(e, CodeParams) {
		t.Fatalf("a backup in the data folder: %v", e)
	}
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = keystore.ErrTampered, CodeTamperedHash
	h.c.mu.Unlock()
	if e := h.c.ReplaceRecoveryKeys(""); !isCode(e, CodeVaultTampered) {
		t.Fatalf("tampered: %v", e)
	}
	h.noCeremonySince(mark, "a refused replacement")
}

// 24. The replacement's backup holds the new keys only, at the new key; a
// backup made before it reads as replaced and behind.
func TestReplaceRecoveryKeysBacksUpAfter(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	path := filepath.Join(h.dir, "backup.eks")
	digits, end := h.replaceOnPassword(path)
	if end.Step != StepDone || !end.BackedUp {
		t.Fatalf("end: %+v", end)
	}
	ks, err := keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	if ks.Generation() != 2 {
		t.Fatalf("the backup is at generation %d", ks.Generation())
	}
	for i, d := range []string{h.recovery, digits[0]} {
		rk, err := kdf.ParseRecoveryDigits(d)
		if err != nil {
			t.Fatal(err)
		}
		unl, err := ks.Unlock(keystore.RecoveryCredential{Key: rk})
		if (err == nil) != (i == 1) {
			t.Fatalf("key %d against the backup: %v", i, err)
		}
		if unl != nil {
			unl.Close()
		}
	}
	h.c.mu.Lock()
	le := *h.c.settings.LastExport
	h.c.mu.Unlock()
	if !slices.Equal(le.Recovery, []string{h.recoveryViews()[0].RecipientID}) {
		t.Fatalf("the stamp: %+v", le)
	}
	if h.c.BackupStatus().KeysReplaced {
		t.Fatal("the backup made after the replacement reads as replaced")
	}

	h2 := newHarness(t, nil, nil)
	h2.unlockWithPassword()
	if e := h2.exportBackup(filepath.Join(h2.dir, "before.eks")); e != nil {
		t.Fatal(e)
	}
	if _, end := h2.replaceOnPassword(""); end.Step != StepDone {
		t.Fatalf("end: %+v", end)
	}
	if bs := h2.c.BackupStatus(); !bs.KeysReplaced || !bs.VaultRekeyed {
		t.Fatalf("the card after a replacement: %+v", bs)
	}
}

// ---- 25-28: the recovery-key proof and the lost YubiKey.

// 25. With the user's word that no YubiKey is at hand, an enrolment proves
// with a recovery key: a YubiKey is then asked for at once, with nothing to
// swap out; a recovery key is shown after its commit.
func TestEnrollWithARecoveryKeyProof(t *testing.T) {
	setup := func() (*harness, *fakeCards) {
		cardA, pubA := cardWithKey("123456")
		cards := readerWith(cardA)
		r, digits := newRecoverySpec(t, "Paper")
		h := rekeyHarness(t, cards, "", digits, keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, r)
		h.unlockWithRecovery(digits)
		return h, cards
	}
	h, cards := setup()
	cardB, _ := cardWithKey("654321")
	cards.setCard(cardB)
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "new", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	p := h.answerRecovery(h.recovery)
	if p.Method != string(MethodRecovery) {
		t.Fatalf("the proof: %+v", p)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	for _, s := range h.ceremoniesSince(0) {
		if s.Step == StepSwapKey && s.RemoveLabel != "" {
			t.Fatalf("a swap was asked for: %+v", s)
		}
	}
	if n := len(h.c.Slots()); n != 3 {
		t.Fatalf("%d slots", n)
	}

	h2, _ := setup()
	h2.rec.reset()
	if e := h2.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "spare", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h2.answerRecovery(h2.recovery)
	shown := h2.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepRecovery && s.PromptID == "" && strings.HasPrefix(s.SlotLabel, "http://")
	}).(CeremonyState)
	if shown.RecoveryID == "" {
		t.Fatalf("the new key: %+v", shown)
	}
	waitIdle(t, h2)
	if n := len(h2.c.Slots()); n != 3 {
		t.Fatalf("%d slots", n)
	}

	if e := h2.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "p", WithRecoveryKey: true}); !isCode(e, CodeParams) {
		t.Fatalf("a password with the switch: %v", e)
	}
	plain := newHarness(t, nil, nil)
	plain.unlockWithPassword()
	if e := plain.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "x", WithRecoveryKey: true}); !isCode(e, CodeParams) {
		t.Fatalf("the switch on a vault with no YubiKey: %v", e)
	}
}

// waitIdle waits for the ceremony to end, when its final step is one that
// stands (StepRecovery) rather than Done.
func waitIdle(t *testing.T, h *harness) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.c.mu.Lock()
		idle := h.c.cer == nil
		h.c.mu.Unlock()
		if idle {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the ceremony did not end")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 26. The acceptance scenario of 3.4, in the core: the only YubiKey lost,
// the vault's password on, one recovery key. A spare YubiKey is enrolled on
// the recovery key's proof, the lost one removed with a re-key, the
// password changed and the recovery key replaced, each re-keying: nothing
// the lost YubiKey or the old recovery key reached opens the vault after.
func TestTheLostYubiKeyScenario(t *testing.T) {
	h, cardA, cards := entangledHarness(t, "the first password")
	pubA := cardA.keys[0x9d].PublicKey().Bytes()
	ridA := ridOf(t, h.slot("Test key").RecipientID)
	ridR := ridOf(t, h.recoveryViews()[0].RecipientID)
	// Every version the file held of the lost YubiKey's record and of the
	// old recovery key's, each re-key wrapping the latter anew: the end
	// searches the raw bytes for each one's ID, epk and wrapped VMK.
	var gone []format.SlotRecord
	keep := func(rids ...[16]byte) {
		t.Helper()
		img := fileBytes(t, h.vault)
		for _, s := range recordsNamed(t, img, liveSuperblock(t, img)) {
			if slices.Contains(rids, s.RecipientID) {
				gone = append(gone, s)
			}
		}
	}
	keep(ridA, ridR)
	oldDigits := h.recovery
	h.unlockWithRecovery(oldDigits)

	cardB, _ := cardWithKey("654321")
	cards.setCard(cardB)
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "B", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(oldDigits)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()

	answerB := func(password string) {
		pin := h.rec.waitCeremony(t, StepPIN, true)
		h.c.SubmitSecret("pin", pin.PromptID, "654321")
		pw := h.rec.waitCeremony(t, StepPassword, true)
		if pw.Choose {
			t.Fatalf("the proof's password marked choose: %+v", pw)
		}
		h.c.SubmitSecret("password", pw.PromptID, password)
	}
	h.rec.reset()
	if e := h.c.RemoveSlot(hexID(ridA), true, ""); e != nil {
		t.Fatal(e)
	}
	wait := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Method == string(MethodToken)
	}).(CeremonyState)
	if wait.Without != hexID(ridA) {
		t.Fatalf("the wait does not leave the lost YubiKey out: %+v", wait)
	}
	answerB("the first password")
	h.done()
	if g := h.generation(); g != 2 {
		t.Fatalf("after the removal: generation %d", g)
	}
	keep(ridA, ridR)

	h.rec.reset()
	if e := h.c.ChangeEntangledPassword(true, ""); e != nil {
		t.Fatal(e)
	}
	answerB("the first password")
	h.chooseNew("the second password")
	h.done()
	if g := h.generation(); g != 3 {
		t.Fatalf("after the change: generation %d", g)
	}
	keep(ridA, ridR)

	h.rec.reset()
	if e := h.c.ReplaceRecoveryKeys(""); e != nil {
		t.Fatal(e)
	}
	answerB("the second password")
	digits, _ := h.keepAll()
	h.confirmReplacement()
	h.done()
	if g := h.generation(); g != 4 {
		t.Fatalf("after the replacement: generation %d", g)
	}
	h.lockAndSettle()

	// The lost YubiKey finds no slot.
	cards.setCard(cardA)
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	if st := h.rec.waitCeremony(t, StepNoMatch, false); st.Error != CodeTokenNoKey {
		t.Fatalf("the lost YubiKey: %+v", st)
	}
	h.c.CancelUnlock()
	h.end()
	h.recoveryRefused(oldDigits)
	cards.setCard(cardB)
	h.rec.reset()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin = h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	first := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", first.PromptID, "the first password")
	again := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != first.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		t.Fatalf("the old password with B: %+v", again)
	}
	h.c.SubmitSecret("password", again.PromptID, "the second password")
	h.rec.waitState(t, StateUnlocked)
	h.lockAndSettle()
	h.unlockWithRecovery(digits[0])
	img := fileBytes(t, h.vault)
	if bytes.Contains(img, ridA[:]) || bytes.Contains(img, pubA) || bytes.Contains(img, ridR[:]) {
		t.Fatal("the raw bytes still hold the lost YubiKey or the old recovery key")
	}
	// A at generation 1, R at generations 1, 2 and 3: the removal took A
	// out, and the replacement R.
	if len(gone) != 4 {
		t.Fatalf("the control: %d records of A and R were read", len(gone))
	}
	for _, rec := range gone {
		if holdsRecord(t, h.vault, rec) {
			t.Fatalf("the raw bytes still hold a record of %s", recoveryID(rec.RecipientID))
		}
	}
	requireCleanArea(t, h.vault, "after the scenario")
	if h.owes() {
		t.Fatal("the file is owed")
	}
}

// 27. A re-keying removal of a password proves with the password being
// removed, never with a recovery key.
func TestRemovingAPasswordProvesWithThePassword(t *testing.T) {
	r1, d1 := newRecoverySpec(t, "one")
	r2, _ := newRecoverySpec(t, "two")
	h := rekeyHarness(t, nil, "", d1, keystore.PasswordSlot{Password: testPassword, Argon2: fast, Label: "Password"}, r1, r2)
	h.unlockWithPassword()
	mark := h.mark()
	h.rec.reset()
	if e := h.c.RemoveSlot(h.slot("Password").RecipientID, true, ""); e != nil {
		t.Fatal(e)
	}
	first := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.PromptID != ""
	}).(CeremonyState)
	if first.Step != StepPassword || first.Without != "" {
		t.Fatalf("the first prompt: %+v", first)
	}
	h.c.SubmitSecret("password", first.PromptID, testPassword)
	h.done()
	if g := h.generation(); g != 2 || len(h.recoveryViews()) != 2 || len(h.c.Slots()) != 2 {
		t.Fatalf("generation %d, slots %+v", g, h.c.Slots())
	}
	for _, s := range h.ceremoniesSince(mark) {
		if s.Step == StepRecovery {
			t.Fatalf("a recovery key was asked for: %+v", s)
		}
	}
}

// lostOnlyHarness is YubiKey A and two recovery keys, the password on or
// off, unlocked with A, with A then removed on a recovery key's proof: a
// vault left with recovery keys alone.
func lostOnlyHarness(t *testing.T, password string) (*harness, *fakeCards) {
	t.Helper()
	cardA, pubA := cardWithKey("123456")
	cards := readerWith(cardA)
	r1, d1 := newRecoverySpec(t, "one")
	r2, _ := newRecoverySpec(t, "two")
	h := rekeyHarness(t, cards, password, d1, keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, r1, r2)
	h.unlockWithToken()
	h.rec.reset()
	if e := h.c.RemoveSlot(h.slot("A").RecipientID, true, ""); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(d1)
	h.done()
	h.lockAndSettle()
	return h, cards
}

// 28. Finish setting up is refused before its ceremony while the vault's
// password is on; the way back is a recovery key's unlock and an enrolment.
func TestFinishSetupIsRefusedWhileThePasswordIsOn(t *testing.T) {
	h, cards := lostOnlyHarness(t, "the vault password")
	if st := h.status(); !st.SetupNeeded || !st.Entangled {
		t.Fatalf("the lock screen: %+v", st)
	}
	before := fileBytes(t, h.vault)
	mark := h.mark()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken}); !isCode(e, CodeSetupPasswordOn) {
		t.Fatalf("finish setting up: %v", e)
	}
	h.noCeremonySince(mark, "a refused setup")
	if !bytes.Equal(fileBytes(t, h.vault), before) {
		t.Fatal("the refused setup wrote the file")
	}
	h.unlockWithRecovery(h.recovery)
	cardB, _ := cardWithKey("654321")
	cards.setCard(cardB)
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollToken, Label: "B"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	if h.status().SetupNeeded {
		t.Fatal("still setup needed")
	}

	off, _ := lostOnlyHarness(t, "")
	if e := off.c.FinishSetup(EnrollOptions{Kind: EnrollToken}); e != nil {
		t.Fatalf("finish setting up with the password off: %v", e)
	}
	off.rec.waitCeremony(t, StepRecovery, true)
	off.c.CancelUnlock()
	off.end()
}

// ---- 29-35: the barrier.

// barrierVault is YubiKeys A and B and recovery keys R1 and R2, the password
// on, unlocked with A; archive "A" open with one file, archive "Kept" and a
// forgotten one closed, a backup exported and inspected. Then R2 is removed
// with every retiring write failing: the file owes its retirement.
type barrierVault struct {
	h                *harness
	f                *faultSwitch
	open, kept, gone string
	backup, handle   string
	ids              []string
}

func newBarrierVault(t *testing.T) *barrierVault {
	t.Helper()
	cardA, pubA := cardWithKey("123456")
	_, pubB := cardWithKey("654321")
	r1, d1 := newRecoverySpec(t, "R1")
	r2, _ := newRecoverySpec(t, "R2")
	h := rekeyHarness(t, readerWith(cardA), "pw", d1,
		keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, keystore.HardwareSlot{PublicKey: pubB, Label: "B"}, r1, r2)
	h.unlockWithToken()
	v := &barrierVault{h: h}
	v.open = h.openArchive(t, "A")
	h.add(t, v.open, rootID, PolicySkip, h.src(t, "one.txt", "one"))
	v.kept, _ = h.newArchive("Kept")
	v.gone, _ = h.newArchive("Gone")
	if e := h.c.ForgetArchive(v.gone); e != nil {
		t.Fatal(e)
	}
	v.backup = filepath.Join(h.dir, "backup.eks")
	h.rec.reset()
	if e := h.c.ExportBackup(v.backup); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	h.done()
	h.rec.reset()
	if e := h.c.InspectRecords(v.backup); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	v.handle = h.rec.waitCeremony(t, StepRecords, false).SlotLabel
	waitIdle(t, h)
	inc, e := h.c.IncomingRecords(v.handle)
	if e != nil || len(inc.Records) == 0 {
		t.Fatalf("the inspected backup: %+v %v", inc, e)
	}
	for _, r := range inc.Records {
		v.ids = append(v.ids, r.ArchiveID)
	}

	v.f = installSwitch(t)
	h.c.seams.committed = func() { v.f.set(everyRegion) }
	h.rec.reset()
	if e := h.c.RemoveSlot(h.slot("R2").RecipientID, false, ""); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	if st := h.done(); st.RetireError == "" {
		t.Fatalf("the removal's end: %+v", st)
	}
	h.c.seams.committed = nil
	if !h.owes() || !h.warned(CodeRetireOwed) {
		t.Fatal("the fixture owes nothing")
	}
	return v
}

// 29. While a retirement is owed and cannot land, every mutation that writes
// a record or a secret, and every backup, is refused before anything is
// written; content and fields of existing records go on. Once it can land,
// the first held mutation lands it and the rest find nothing owed.
func TestARegistryMutationWhileOwedFinishesFirst(t *testing.T) {
	v := newBarrierVault(t)
	h := v.h
	refused := func(what string, call func() *Error, code Code) {
		t.Helper()
		before := fileBytes(t, h.vault)
		mark := h.mark()
		if e := call(); !isCode(e, code) {
			t.Fatalf("%s: %v", what, e)
		}
		if !bytes.Equal(fileBytes(t, h.vault), before) {
			t.Fatalf("%s wrote the file", what)
		}
		h.noCeremonySince(mark, what)
	}
	newPath := filepath.Join(h.dir, "new.enf")
	refused("CreateArchive", func() *Error { _, e := h.c.CreateArchive(newPath, "New", compressionNormal); return e }, CodeRetireOwed)
	if _, err := os.Stat(newPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the refused create left its file")
	}
	// The barrier answers before the archive file is made: a path the
	// archive layer would refuse is never reached.
	refused("CreateArchive before its file", func() *Error {
		_, e := h.c.CreateArchive(filepath.Join(h.dir, "no such folder", "x.enf"), "X", compressionNormal)
		return e
	}, CodeRetireOwed)
	opCount := func() int {
		h.c.mu.Lock()
		defer h.c.mu.Unlock()
		return len(h.c.ops)
	}
	ops := opCount()
	refused("RotateKey", func() *Error { _, e := h.c.RotateKey(v.kept); return e }, CodeRetireOwed)
	if opCount() != ops {
		t.Fatal("the refused re-key started an operation")
	}

	// Forget, while an add runs in the open archive: neither closed nor
	// cancelled.
	held, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h.rec.onEvent(func(name string, payload any) {
		if o, ok := payload.(OpView); ok && name == EventOpProgress && o.Kind == "add" {
			once.Do(func() {
				close(held)
				// Bounded, so that a Forget that waits for the add fails
				// the test rather than hanging it.
				select {
				case <-release:
				case <-time.After(5 * time.Second):
				}
			})
		}
	})
	opID, e := h.c.AddFiles(v.open, rootID, []string{h.src(t, "running.txt", "running")}, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	<-held
	refused("ForgetArchive", func() *Error { return h.c.ForgetArchive(v.open) }, CodeRetireOwed)
	h.c.mu.Lock()
	stillOpen := h.c.archives[ridOf(t, v.open)] != nil
	h.c.mu.Unlock()
	close(release)
	if o := h.rec.waitOp(t, opID); o.Error != "" || !stillOpen {
		t.Fatalf("the add beside a refused forget: %+v, open %v", o, stillOpen)
	}
	h.rec.onEvent(nil)

	refused("RestoreArchive", func() *Error { return h.c.RestoreArchive(v.gone) }, CodeRetireOwed)
	refused("DeleteArchive", func() *Error { return h.c.DeleteArchive(v.kept, true) }, CodeRetireOwed)
	if _, err := os.Stat(h.record(v.kept).LastPath); err != nil {
		t.Fatalf("the refused delete removed the file: %v", err)
	}
	exportPath := filepath.Join(h.dir, "second.eks")
	refused("ExportBackup", func() *Error { return h.c.ExportBackup(exportPath) }, CodeRetireOwed)
	if _, err := os.Stat(exportPath); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("the refused export left a file")
	}
	refused("InspectRecords", func() *Error { return h.c.InspectRecords(v.backup) }, CodeRetireOwed)
	refused("RemoveSlot", func() *Error { return h.c.RemoveSlot(h.slot("B").RecipientID, false, "") }, CodeRetireOwed)
	refused("RotateNow", func() *Error { return h.c.RotateNow("") }, CodeRetireOwed)
	refused("ChangeEntangledPassword", func() *Error { return h.c.ChangeEntangledPassword(false, "") }, CodeRetireOwed)
	refused("SetEntangled", func() *Error { return h.c.SetEntangled(false, false, "") }, CodeRetireOwed)
	refused("BeginEnroll", func() *Error { return h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "x"}) }, CodeRetireOwed)
	refused("ReplaceRecoveryKeys", func() *Error { return h.c.ReplaceRecoveryKeys("") }, CodeRetireOwed)
	closeAll(t, h)
	refused("MergeRecords", func() *Error { return h.c.MergeRecords(v.handle, v.ids) }, CodeRetireOwed)

	// Admitted and landing: content, fields of existing records, settings.
	if _, e := h.c.OpenArchive(v.open); e != nil {
		t.Fatal(e)
	}
	h.add(t, v.open, rootID, PolicySkip, h.src(t, "two.txt", "two"))
	one := h.row(t, v.open, rootID, "one.txt")
	rid, e := h.c.ReplaceFile(v.open, one.ID, h.src(t, "one-new.txt", "one, replaced"))
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, rid); o.Error != "" {
		t.Fatalf("replace: %+v", o)
	}
	dir, e := h.c.CreateFolder(v.open, rootID, "Folder")
	if e != nil {
		t.Fatalf("create folder: %v", e)
	}
	two := h.row(t, v.open, rootID, "two.txt")
	if e := h.c.RenameRecord(v.open, two.ID, "renamed.txt"); e != nil {
		t.Fatalf("rename record: %v", e)
	}
	if e := h.c.MoveRecords(v.open, []string{two.ID}, dir); e != nil {
		t.Fatalf("move records: %v", e)
	}
	if e := h.c.DeleteRecords(v.open, []string{h.row(t, v.open, rootID, "one.txt").ID}); e != nil {
		t.Fatalf("delete records: %v", e)
	}
	if e := h.c.RenameArchive(v.kept, "Kept, renamed"); e != nil {
		t.Fatalf("rename archive: %v", e)
	}
	if e := h.c.SetArchiveDescription(v.kept, "a description"); e != nil {
		t.Fatalf("description: %v", e)
	}
	if e := h.c.HideArchive(v.kept, true); e != nil {
		t.Fatalf("hide: %v", e)
	}
	if e := h.c.Locate(v.kept, h.record(v.kept).LastPath); e != nil {
		t.Fatalf("locate: %v", e)
	}
	s := h.c.GetSettings()
	s.IdleMinutes, s.TimeoutsFromVault = 7, true
	if e := h.c.SetSettings(s); e != nil {
		t.Fatalf("the lock times: %v", e)
	}
	if r := h.record(v.kept); r.Name != "Kept, renamed" || r.Description != "a description" || r.Policy&format.PolicyHidden == 0 {
		t.Fatalf("the record's fields: %+v", r)
	}
	if !h.owes() || !h.warned(CodeRetireOwed) {
		t.Fatal("a write the barrier does not hold claimed the retirement")
	}

	// The fault cleared: each refused act is admitted, the first after the
	// retirement lands.
	v.f.set(nil)
	if _, e := h.c.CreateArchive(newPath, "New", compressionNormal); e != nil {
		t.Fatalf("create: %v", e)
	}
	if h.owes() || h.warned(CodeRetireOwed) || !h.logged("finished a retirement left owed") {
		t.Fatal("the create did not land the retirement first")
	}
	rotID, e := h.c.RotateKey(v.kept)
	if e != nil {
		t.Fatalf("rotate key: %v", e)
	}
	if o := h.rec.waitOp(t, rotID); o.Error != "" {
		t.Fatalf("rotate key: %+v", o)
	}
	closeAll(t, h)
	if e := h.c.MergeRecords(v.handle, v.ids); e != nil {
		t.Fatalf("merge: %v", e)
	}
	if e := h.c.RestoreArchive(v.gone); e != nil {
		t.Fatalf("restore: %v", e)
	}
	if e := h.c.ForgetArchive(v.gone); e != nil {
		t.Fatalf("forget: %v", e)
	}
	if e := h.c.DeleteArchive(v.kept, true); e != nil {
		t.Fatalf("delete: %v", e)
	}
	begins := []struct {
		what string
		call func() *Error
	}{
		{"ExportBackup", func() *Error { return h.c.ExportBackup(exportPath) }},
		{"InspectRecords", func() *Error { return h.c.InspectRecords(v.backup) }},
		{"RemoveSlot", func() *Error { return h.c.RemoveSlot(h.slot("B").RecipientID, false, "") }},
		{"RotateNow", func() *Error { return h.c.RotateNow("") }},
		{"ChangeEntangledPassword", func() *Error { return h.c.ChangeEntangledPassword(false, "") }},
		{"SetEntangled", func() *Error { return h.c.SetEntangled(false, false, "") }},
		{"BeginEnroll", func() *Error { return h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "x"}) }},
		{"ReplaceRecoveryKeys", func() *Error { return h.c.ReplaceRecoveryKeys("") }},
	}
	for _, b := range begins {
		h.rec.reset()
		if e := b.call(); e != nil {
			t.Fatalf("%s once nothing is owed: %v", b.what, e)
		}
		h.rec.waitCeremony(t, StepPIN, true)
		h.c.CancelUnlock()
		h.end()
		waitIdle(t, h)
	}
}

// closeAll closes every archive the core holds open: a merge is refused
// while one is.
func closeAll(t *testing.T, h *harness) {
	t.Helper()
	h.c.mu.Lock()
	var ids []string
	for id := range h.c.archives {
		ids = append(ids, hexID(id))
	}
	h.c.mu.Unlock()
	for _, id := range ids {
		if e := h.c.CloseArchive(id); e != nil {
			t.Fatalf("close %s: %v", id, e)
		}
	}
}

// legacyVault writes the file a writer before R41 could leave: YubiKey X
// removed without a re-key, its whole record still in the live slot region
// copy past slot_region_len (it wraps the current VMK), and the live
// registry's last 4 KiB unit holding an older registry's bytes past its tag.
// The vault keeps YubiKey A and a recovery key. It returns X's record.
func legacyVault(t *testing.T, path string, pubA []byte, r keystore.RecoverySlot) format.SlotRecord {
	t.Helper()
	_, pubX := cardWithKey("000000")
	writeVault(t, path, "", keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, keystore.HardwareSlot{PublicKey: pubX, Label: "X"}, r)
	orig := fileBytes(t, path)
	sb := liveSuperblock(t, orig)
	var x format.SlotRecord
	for _, s := range recordsNamed(t, orig, sb) {
		if bytes.Equal(s.SlotPubkey, pubX) {
			x = s
		}
	}
	oldRegistry := bytes.Clone(orig[sb.RegistryOff : sb.RegistryOff+sb.RegistryLen])

	ks, err := keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	unl, err := ks.Unlock(keystore.RecoveryCredential{Key: r.Key})
	if err != nil {
		t.Fatal(err)
	}
	if err := unl.RemoveSlot(x.RecipientID); err != nil {
		t.Fatal(err)
	}
	if err := unl.Retire(); err != nil {
		t.Fatal(err)
	}
	unl.Close()
	ks.Close()

	img := fileBytes(t, path)
	sb = liveSuperblock(t, img)
	enc, err := x.Encode()
	if err != nil {
		t.Fatal(err)
	}
	copy(img[sb.SlotRegionOff+sb.SlotRegionLen:], enc)
	tagEnd := sb.RegistryOff + sb.RegistryLen + format.TagSize
	unitEnd := (tagEnd + 4095) &^ 4095
	if unitEnd == tagEnd {
		t.Fatal("the fixture: the live registry ends on a unit boundary")
	}
	if uint64(len(img)) < unitEnd {
		img = append(img, make([]byte, unitEnd-uint64(len(img)))...)
	}
	copy(img[tagEnd:unitEnd], oldRegistry)
	if err := os.WriteFile(path, img, 0o600); err != nil {
		t.Fatal(err)
	}
	ks, err = keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	owed := ks.RetireOwed()
	ks.Close()
	if !owed || !bytes.Contains(img, x.RecipientID[:]) {
		t.Fatal("the fixture: the legacy file owes nothing")
	}
	return x
}

// 30. A legacy file is retired in full, both passes, before a held mutation
// publishes: no retiring pass counts as the barrier's success while anything
// is owed.
func TestALegacyTailIsRetiredInFullBeforeAPublish(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	cards := readerWith(cardA)
	r, digits := newRecoverySpec(t, "Paper")
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.eks")
	x := legacyVault(t, path, pubA, r)
	h := harnessOver(t, cards, dir, path, digits, "")
	h.c.seams.failAt = func(p string) error {
		if p == "retire" {
			return errInjected
		}
		return nil
	}
	h.unlockWithToken()
	if !h.owes() || !h.warned(CodeRetireOwed) {
		t.Fatal("the unlock claimed the legacy retirement")
	}
	before := fileBytes(t, path)
	p := filepath.Join(dir, "new.enf")
	if _, e := h.c.CreateArchive(p, "New", compressionNormal); !isCode(e, CodeRetireOwed) {
		t.Fatalf("a create while owed: %v", e)
	}
	if !bytes.Equal(fileBytes(t, path), before) || !holdsID(t, path, x.RecipientID) {
		t.Fatal("the refused create wrote the file")
	}
	h.c.mu.Lock()
	n := len(h.c.vault.sess.Registry().Archives)
	h.c.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d archive records after a refused create", n)
	}

	h.c.seams.failAt = nil
	w := watchWrites(t)
	var mu sync.Mutex
	var heldAfter []bool // after each flip: whether the raw bytes hold X's record
	flipped := false
	w.after = func(what string) {
		mu.Lock()
		defer mu.Unlock()
		if what == "superblock" {
			flipped = true
			return
		}
		if flipped {
			flipped = false
			heldAfter = append(heldAfter, bytes.Contains(fileBytes(t, path), x.RecipientID[:]))
		}
	}
	seq := liveSuperblock(t, fileBytes(t, path)).Seq
	id, e := h.c.CreateArchive(p, "New", compressionNormal)
	if e != nil {
		t.Fatalf("a create once the seam is clear: %v", e)
	}
	ws := w.take()
	mu.Lock()
	held := append([]bool(nil), heldAfter...)
	mu.Unlock()
	if got := liveSuperblock(t, fileBytes(t, path)).Seq; got != seq+3 {
		t.Fatalf("seq %d, was %d: two retiring commits and the create's", got, seq)
	}
	// Two retiring commits, each a region then a superblock, and the
	// create's registry-only commit after both.
	sbs := []int{}
	for i, w := range ws {
		if w == "superblock" {
			sbs = append(sbs, i)
		}
	}
	if len(sbs) != 3 || count(ws[:sbs[0]], "region") != 1 || count(ws[sbs[0]:sbs[1]], "region") != 1 || count(ws[sbs[1]:], "region") != 0 {
		t.Fatalf("the writes: %v", ws)
	}
	if len(held) < 2 || !held[0] || held[1] {
		t.Fatalf("X's record after each flip: %v", held)
	}
	if holdsID(t, path, x.RecipientID) || h.owes() {
		t.Fatal("still owed after the create")
	}
	if !h.hasRecord(id) {
		t.Fatal("the create's record is not there")
	}
	requireCleanArea(t, path, "after the create")
}

// tamperLiveRegion swaps a recovery slot's public key in the live slot
// region copy of a closed vault file for another valid one, so that the
// region decodes and fails R25 against its registry.
func tamperLiveRegion(t *testing.T, path string) {
	t.Helper()
	img := fileBytes(t, path)
	sb := liveSuperblock(t, img)
	r, err := format.DecodeSlotRegion(img[sb.SlotRegionOff : sb.SlotRegionOff+sb.SlotRegionLen])
	if err != nil {
		t.Fatal(err)
	}
	for i := range r.Slots {
		if r.Slots[i].Type == format.SlotRecovery {
			fake := bytes.Clone(r.Slots[i].SlotPubkey)
			fake[0] ^= 0x55
			r.Slots[i].SlotPubkey = fake
			break
		}
	}
	enc, err := r.Encode()
	if err != nil || uint64(len(enc)) != sb.SlotRegionLen {
		t.Fatalf("the tampered region: %v", err)
	}
	copy(img[sb.SlotRegionOff:], enc)
	if err := os.WriteFile(path, img, 0o600); err != nil {
		t.Fatal(err)
	}
}

// liveCopy is the whole live slot region copy.
func liveCopy(t *testing.T, path string) []byte {
	t.Helper()
	img := fileBytes(t, path)
	sb := liveSuperblock(t, img)
	return bytes.Clone(img[sb.SlotRegionOff : sb.SlotRegionOff+format.SlotRegionSize])
}

// 31. A Tampered vault that owes stays readable: its retirement is finished
// as far as R25 lets it be, nothing copied forward, and what only a region
// write could clear leaves every held mutation refused with vault.tampered
// until a good copy replaces it.
func TestATamperedVaultThatOwesStaysReadable(t *testing.T) {
	t.Run("owed by its regions", func(t *testing.T) {
		h := passwordsHarness(t, 2)
		h.unlockWithPassword()
		h.c.seams.failAt = func(p string) error {
			if p == "retire" {
				return errInjected
			}
			return nil
		}
		if st := h.removeOnPassword(h.slot("Second").RecipientID, false, ""); st.RetireError == "" {
			t.Fatalf("end: %+v", st)
		}
		h.lockAndSettle()
		tamperLiveRegion(t, h.vault)
		h.c.seams.failAt = nil
		w := watchWrites(t)
		h.unlockWithPassword()
		ws := w.take()
		st := h.status()
		if !st.Tampered || st.State != StateUnlocked || slices.Contains(st.Warnings, CodeRetireOwed) || h.owes() {
			t.Fatalf("after the unlock: %+v, owed %v", st, h.owes())
		}
		if count(ws, "region") != 0 || count(ws, "zero") != 1 {
			t.Fatalf("the unlock's writes: %v", ws)
		}
		img := fileBytes(t, h.vault)
		other := liveSuperblock(t, img).LiveSlotRegion().Other().SlotRegionOff()
		if !bytes.Equal(img[other:other+format.SlotRegionSize], make([]byte, format.SlotRegionSize)) {
			t.Fatal("the slot region copy no superblock names is not zero")
		}
		if _, e := h.c.CreateArchive(filepath.Join(h.dir, "c.enf"), "C", compressionNormal); e != nil {
			t.Fatalf("a create on a Tampered vault that owes nothing: %v", e)
		}
		if e := h.c.RemoveSlot(strings.Repeat("ab", 16), false, ""); !isCode(e, CodeVaultTampered) {
			t.Fatalf("a removal on a Tampered vault: %v", e)
		}
	})

	t.Run("a live tail", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		h.unlockWithPassword()
		id := h.openArchive(t, "A")
		h.add(t, id, rootID, PolicySkip, h.src(t, "one.txt", "one"))
		backup := filepath.Join(h.dir, "backup.eks")
		if e := h.exportBackup(backup); e != nil {
			t.Fatal(e)
		}
		if e := h.c.CloseArchive(id); e != nil {
			t.Fatal(e)
		}
		h.lockAndSettle()
		good := filepath.Join(h.dir, "good.eks")
		if err := os.WriteFile(good, fileBytes(t, h.vault), 0o600); err != nil {
			t.Fatal(err)
		}
		// A legacy tail past slot_region_len, then the tampering.
		img := fileBytes(t, h.vault)
		sb := liveSuperblock(t, img)
		copy(img[sb.SlotRegionOff+sb.SlotRegionLen:], img[sb.SlotRegionOff:sb.SlotRegionOff+sb.SlotRegionLen])
		if err := os.WriteFile(h.vault, img, 0o600); err != nil {
			t.Fatal(err)
		}
		tamperLiveRegion(t, h.vault)
		// An earlier attempt failed: retireFailed stands.
		h.c.seams.failAt = func(p string) error {
			if p == "retire" {
				return errInjected
			}
			return nil
		}
		h.unlockWithPassword()
		if !h.warned(CodeRetireOwed) {
			t.Fatal("the failed attempt left no warning")
		}
		h.lockAndSettle()
		h.c.seams.failAt = nil
		h.unlockWithPassword()
		st := h.status()
		if !st.Tampered || st.State != StateUnlocked || slices.Contains(st.Warnings, CodeRetireOwed) || !h.owes() || !st.RetireOwed {
			t.Fatalf("after the unlock: %+v", st)
		}
		if _, e := h.c.OpenArchive(id); e != nil {
			t.Fatal(e)
		}
		rows := h.page(t, id, rootID).Rows
		h.extractsAll(t, id, rows)
		tail := liveCopy(t, h.vault)
		seq := h.record(id).LastSeq
		if o := h.add(t, id, rootID, PolicySkip, h.src(t, "two.txt", "two")); o.Error != "" {
			t.Fatalf("an add: %+v", o)
		}
		if h.record(id).LastSeq <= seq || !bytes.Equal(liveCopy(t, h.vault), tail) {
			t.Fatal("the receipt did not land, or wrote the live copy")
		}
		refused := func(what string, call func() *Error) {
			t.Helper()
			before := fileBytes(t, h.vault)
			if e := call(); !isCode(e, CodeVaultTampered) {
				t.Fatalf("%s: %v", what, e)
			}
			if !bytes.Equal(fileBytes(t, h.vault), before) {
				t.Fatalf("%s wrote the file", what)
			}
		}
		create := func() *Error {
			_, e := h.c.CreateArchive(filepath.Join(h.dir, "c.enf"), "C", compressionNormal)
			return e
		}
		refused("CreateArchive", create)
		refused("ForgetArchive", func() *Error { return h.c.ForgetArchive(id) })
		refused("RotateKey", func() *Error { _, e := h.c.RotateKey(id); return e })
		refused("InspectRecords", func() *Error { return h.c.InspectRecords(backup) })
		refused("ExportBackup", func() *Error { return h.c.ExportBackup(filepath.Join(h.dir, "b2.eks")) })
		if o := h.add(t, id, rootID, PolicySkip, h.src(t, "three.txt", "three")); o.Error != "" {
			t.Fatalf("an add: %+v", o)
		}
		refused("CreateArchive after a receipt", create)

		// A good copy of the vault opens untampered, and the barrier admits.
		if e := h.c.CloseArchive(id); e != nil {
			t.Fatal(e)
		}
		h.lockAndSettle()
		if e := h.c.OpenVaultFile(good, "Good copy"); e != nil {
			t.Fatal(e)
		}
		h.vault = good
		h.unlockWithPassword()
		if st := h.status(); st.Tampered || h.owes() {
			t.Fatalf("the good copy: %+v", st)
		}
		if e := create(); e != nil {
			t.Fatalf("a create on the good copy: %v", e)
		}
	})

	t.Run("owing nothing", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		tamperLiveRegion(t, h.vault)
		h.unlockWithPassword()
		if st := h.status(); !st.Tampered || h.owes() {
			t.Fatalf("the fixture: %+v", st)
		}
		if _, e := h.c.CreateArchive(filepath.Join(h.dir, "c.enf"), "C", compressionNormal); e != nil {
			t.Fatalf("a create on a Tampered vault that owes nothing: %v", e)
		}
	})
}

// 32. The owed retirement and its warning survive a lock as nonsecret facts;
// nothing held runs while Locked, and the next unlock tries first.
func TestLockAfterAFailedRetirement(t *testing.T) {
	h := passwordsHarness(t, 3)
	h.unlockWithPassword()
	id := h.openArchive(t, "A")
	f := owedAfterRemoval(t, h, true)
	h.lockAndSettle()
	st := h.status()
	if !slices.Contains(st.Warnings, CodeRetireOwed) || !st.RetireOwed {
		t.Fatalf("the lock screen: %+v", st)
	}
	h.c.mu.Lock()
	cached, handle := h.c.vault.retireOwed, h.c.vault.ks
	h.c.mu.Unlock()
	if !cached || handle != nil {
		t.Fatalf("cached %v, handle %v", cached, handle)
	}
	opID, e := h.c.AddFiles(id, rootID, []string{h.src(t, "late.txt", "late")}, PolicySkip)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("an add across the lock: %+v", o)
	}
	h.c.mu.Lock()
	owedReceipts := len(h.c.owed)
	h.c.mu.Unlock()
	if owedReceipts != 1 {
		t.Fatalf("%d receipts owed", owedReceipts)
	}
	p := filepath.Join(h.dir, "new.enf")
	if _, e := h.c.CreateArchive(p, "New", compressionNormal); !isCode(e, CodeNeedsUnlock) {
		t.Fatalf("a create while Locked: %v", e)
	}
	h.unlockWithPassword()
	if _, e := h.c.CreateArchive(p, "New", compressionNormal); !isCode(e, CodeRetireOwed) {
		t.Fatalf("a create with the fault still set: %v", e)
	}
	f.set(nil)
	if _, e := h.c.CreateArchive(p, "New", compressionNormal); e != nil {
		t.Fatalf("a create once the fault is gone: %v", e)
	}
	if h.owes() {
		t.Fatal("still owed")
	}
}

// failedEnrolment fails a password enrolment's commit after its region copy
// is written, and then, when thenRegions is set, every region write after:
// the act's at-once retirement fails too. It returns the record never added.
func failedEnrolment(t *testing.T, h *harness, thenRegions bool) (format.SlotRecord, *faultSwitch) {
	t.Helper()
	f := installSwitch(t)
	var mu sync.Mutex
	var stray []format.SlotRecord
	f.set(func(step string) error {
		if step != "registry" {
			return nil
		}
		mu.Lock()
		stray = strayRecords(h.vault)
		mu.Unlock()
		if thenRegions {
			f.set(everyRegion)
		} else {
			f.set(nil)
		}
		return errInjected
	})
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "Second"}); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.chooseNew("second password")
	if st := h.end(); st.Step != StepFailed {
		t.Fatalf("end: %+v", st)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(stray) != 1 {
		t.Fatalf("the control: the failed commit's region holds %d new records", len(stray))
	}
	return stray[0], f
}

// The cached fact follows the file: a registry write that fails after
// writing leaves the file owed, and the cache says so at once, failed write
// or not, so that the barrier and the lock screen see it; merely owed, it
// warns nothing. An open that finds the file busy knows nothing of what it
// owes, and the next open reads it from the bytes again (design 2.4, the
// cached retireOwed; 2.6 names no test for either, so this one is added).
func TestTheCachedOwedFollowsTheFile(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "A")
	if h.status().RetireOwed {
		t.Fatal("the control: a fresh vault owes")
	}
	f := installSwitch(t)
	f.set(func(step string) error {
		if step == "sync" {
			return errInjected
		}
		return nil
	})
	if e := h.c.SetArchiveDescription(id, "written, never synced"); e == nil {
		t.Fatal("the control: the faulted write landed")
	}
	f.set(nil)
	if !h.owes() {
		t.Fatal("the control: the failed write left the handle owing nothing")
	}
	if st := h.status(); !st.RetireOwed || slices.Contains(st.Warnings, CodeRetireOwed) {
		t.Fatalf("after the failed write: %+v", st)
	}

	h.lockAndSettle()
	if !h.status().RetireOwed {
		t.Fatal("the lock dropped the cached fact")
	}
	held, err := keystore.Open(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	if e := h.c.Reopen(); !isCode(e, CodeVaultBusy) {
		held.Close()
		t.Fatalf("a reopen of a file held elsewhere: %v", e)
	}
	st := h.status()
	held.Close()
	if st.State != StateBusy || st.RetireOwed {
		t.Fatalf("a busy open: %+v", st)
	}
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	if st := h.status(); st.State != StateLocked || !st.RetireOwed {
		t.Fatalf("the open after: %+v", st)
	}
	h.unlockWithPassword()
	if h.owes() || h.status().RetireOwed {
		t.Fatal("the unlock did not finish the retirement")
	}
}

// A failed retirement outlives a Busy open as it outlives a lock: the Busy
// open knows nothing of the file, so the warning leaves with the rest of its
// facts, and the next open of the same vault reads that the file still owes
// and brings the warning back to the lock screen. Only another vault's open
// clears the failed attempt (design 2.4, retireFailed; 2.6 names no test for
// it, so this one is added for the integration review's F3).
func TestABusyOpenKeepsAFailedRetirement(t *testing.T) {
	h := passwordsHarness(t, 3)
	h.unlockWithPassword()
	f := owedAfterRemoval(t, h, true)
	h.lockAndSettle()
	if st := h.status(); !st.RetireOwed || !slices.Contains(st.Warnings, CodeRetireOwed) {
		t.Fatalf("the lock screen: %+v", st)
	}
	held, err := keystore.Open(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	if e := h.c.Reopen(); !isCode(e, CodeVaultBusy) {
		held.Close()
		t.Fatalf("a reopen of a file held elsewhere: %v", e)
	}
	st := h.status()
	held.Close()
	if st.State != StateBusy || st.RetireOwed || slices.Contains(st.Warnings, CodeRetireOwed) {
		t.Fatalf("a busy open: %+v", st)
	}
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	if st := h.status(); st.State != StateLocked || !st.RetireOwed || !slices.Contains(st.Warnings, CodeRetireOwed) {
		t.Fatalf("the same vault opened after: %+v", st)
	}

	// The control: another vault's open clears it, and the first vault's
	// own bytes then say only that it owes.
	other := filepath.Join(h.dir, "other.eks")
	r, _ := newRecoverySpec(t, "Recovery key")
	writeVault(t, other, "", r, keystore.PasswordSlot{Password: testPassword, Argon2: fast, Label: "Password"})
	if e := h.c.OpenVaultFile(other, ""); e != nil {
		t.Fatal(e)
	}
	if e := h.c.OpenVaultFile(h.vault, ""); e != nil {
		t.Fatal(e)
	}
	if st := h.status(); !st.RetireOwed || slices.Contains(st.Warnings, CodeRetireOwed) {
		t.Fatalf("the vault opened after another one: %+v", st)
	}
	f.set(nil)
	h.unlockWithPassword()
	if h.owes() || h.warned(CodeRetireOwed) {
		t.Fatal("the unlock did not finish the retirement")
	}
}

// A retirement that left nothing owed is no failure, whatever error came
// with it: the keystore returns a scrub's error even when the reread after
// its retiring commit finds nothing left (design 2.6, keystore test 16).
// The act then ends Done with no bar and no warning and takes its backup,
// and the barrier admits the write it holds (2.6 names no test for it, so
// this one is added for the integration review's F2).
func TestAnErrorBesideALandedRetirementIsNoFailure(t *testing.T) {
	// scrubOnce fails the first scrub after it is set, and only that one,
	// and says that it did.
	scrubOnce := func(f *faultSwitch, fired *atomic.Bool) func(step string) error {
		return func(step string) error {
			if step != "scrub" {
				return nil
			}
			f.set(nil)
			fired.Store(true)
			return errInjected
		}
	}

	t.Run("the act", func(t *testing.T) {
		h := passwordsHarness(t, 3)
		h.unlockWithPassword()
		f := installSwitch(t)
		var fired atomic.Bool
		h.c.seams.committed = func() { f.set(scrubOnce(f, &fired)) }
		path := filepath.Join(h.dir, "backup.eks")
		st := h.removeOnPassword(h.slot("Second").RecipientID, true, path)
		h.c.seams.committed = nil
		if !fired.Load() {
			t.Fatal("the control: no scrub failed")
		}
		if st.Step != StepDone || st.RetireError != "" || st.RetireRemnant || !st.BackedUp || st.BackupError != "" {
			t.Fatalf("end: %+v", st)
		}
		if h.owes() || h.warned(CodeRetireOwed) {
			t.Fatal("owed after a retirement that landed")
		}
		requireCleanArea(t, h.vault, "after the act")
	})

	t.Run("the barrier", func(t *testing.T) {
		h := passwordsHarness(t, 3)
		h.unlockWithPassword()
		f := owedAfterRemoval(t, h, true)
		var fired atomic.Bool
		f.set(scrubOnce(f, &fired))
		p := filepath.Join(h.dir, "new.enf")
		if _, e := h.c.CreateArchive(p, "New", compressionNormal); e != nil {
			t.Fatalf("a create once the retirement lands: %v", e)
		}
		if !fired.Load() {
			t.Fatal("the control: no scrub failed")
		}
		if h.owes() || h.warned(CodeRetireOwed) {
			t.Fatal("owed after the barrier's retirement landed")
		}
		requireCleanArea(t, h.vault, "after the barrier")
	})
}

// 33. A commit that fails after writing its region copy is retired at once,
// inside the act; when that fails too, the file is owed at once and the
// barrier, or the next unlock, finishes it.
func TestAFailedRegionWriteIsRetiredAtOnce(t *testing.T) {
	t.Run("retired at once", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		h.unlockWithPassword()
		w := watchWrites(t)
		stray, _ := failedEnrolment(t, h, false)
		ws := w.take()
		if count(ws, "superblock") != 1 || h.owes() || h.warned(CodeRetireOwed) {
			t.Fatalf("one retiring commit: %v, owed %v", ws, h.owes())
		}
		if holdsRecord(t, h.vault, stray) {
			t.Fatal("the raw bytes still hold the slot never added")
		}
		if n := len(h.c.Slots()); n != 2 {
			t.Fatalf("%d slots", n)
		}
		requireFileFacts(t, h, "after the at-once retirement")
	})

	t.Run("the barrier finishes it", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		h.unlockWithPassword()
		stray, f := failedEnrolment(t, h, true)
		if !h.owes() || !h.warned(CodeRetireOwed) {
			t.Fatal("the failed at-once retirement left nothing owed")
		}
		requireFileFacts(t, h, "after the failed at-once retirement")
		p := filepath.Join(h.dir, "new.enf")
		if _, e := h.c.CreateArchive(p, "New", compressionNormal); !isCode(e, CodeRetireOwed) {
			t.Fatalf("a create while owed: %v", e)
		}
		f.set(nil)
		w := watchWrites(t)
		seq := liveSuperblock(t, fileBytes(t, h.vault)).Seq
		if _, e := h.c.CreateArchive(p, "New", compressionNormal); e != nil {
			t.Fatal(e)
		}
		ws := w.take()
		if got := liveSuperblock(t, fileBytes(t, h.vault)).Seq; got != seq+2 || len(ws) == 0 || ws[0] != "region" {
			t.Fatalf("seq %d, was %d; writes %v", got, seq, ws)
		}
		if holdsRecord(t, h.vault, stray) || h.owes() {
			t.Fatal("the slot never added is still in the file")
		}
		requireCleanArea(t, h.vault, "after the barrier's retirement")
		requireFileFacts(t, h, "after the barrier's retirement and the create")
	})

	t.Run("the next unlock finishes it", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		h.unlockWithPassword()
		stray, f := failedEnrolment(t, h, true)
		h.lockAndSettle()
		f.set(nil)
		h.unlockWithPassword()
		if holdsRecord(t, h.vault, stray) || h.owes() || h.warned(CodeRetireOwed) {
			t.Fatal("the unlock did not retire the slot never added")
		}
	})
}

// 34. The act's backup waits for a complete retirement: skipped with
// vault.retire_owed, the stamp unchanged; an export waits too, and lands
// once the retirement has.
func TestTheBackupWaitsForACompleteRetirement(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	h.c.seams.failAt = func(p string) error {
		if p == "retire" {
			return errInjected
		}
		return nil
	}
	path := filepath.Join(h.dir, "backup.eks")
	st := h.removeOnPassword(h.slot("Second").RecipientID, true, path)
	if st.Step != StepDone || st.RetireError == "" || st.BackupError != CodeRetireOwed || st.BackedUp {
		t.Fatalf("end: %+v", st)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a backup was written while the retirement was owed")
	}
	h.c.mu.Lock()
	stamp := h.c.settings.LastExport
	h.c.mu.Unlock()
	if stamp != nil || h.c.LastExportAt() != 0 {
		t.Fatalf("the stamp moved: %+v", stamp)
	}
	if e := h.c.ExportBackup(path); !isCode(e, CodeRetireOwed) {
		t.Fatalf("an export while owed: %v", e)
	}
	h.c.seams.failAt = nil
	if e := h.exportBackup(path); e != nil {
		t.Fatal(e)
	}
	if h.owes() {
		t.Fatal("the export did not land the retirement first")
	}
	if _, err := os.Stat(path); err != nil || h.c.LastExportAt() == 0 {
		t.Fatalf("the export: %v", err)
	}
}

// 35. While a Delete holds its claim between the file's removal and the
// record's forgetting, no slot change begins.
func TestADeleteHoldsSlotChangesWhileItRemovesTheFile(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	id, _ := h.newArchive("Doomed")
	entered, release := make(chan struct{}), make(chan struct{})
	prev := deleteArchiveFile
	deleteArchiveFile = func(path string, want [16]byte) (bool, error) {
		close(entered)
		<-release
		return prev(path, want)
	}
	t.Cleanup(func() { deleteArchiveFile = prev })
	done := make(chan *Error, 1)
	go func() { done <- h.c.DeleteArchive(id, true) }()
	<-entered
	second := h.slot("Second").RecipientID
	if e := h.c.RemoveSlot(second, true, ""); !isCode(e, CodeOpRunning) {
		t.Fatalf("a removal during a delete: %v", e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatalf("the delete: %v", e)
	}
	if r := h.record(id); !r.Forgotten() {
		t.Fatal("the record was not forgotten")
	}
	if st := h.removeOnPassword(second, true, ""); st.Step != StepDone {
		t.Fatalf("the removal after the delete: %+v", st)
	}
}

// ---- 36-40: what the end carries, unknown outcomes, the race, the session.

// 36. The end's next step reads what the act committed, never the slot list
// of before.
func TestTheCommittedFactsRideTheEnd(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	_, pubB := cardWithKey("654321")
	r, digits := newRecoverySpec(t, "Paper")
	h := rekeyHarness(t, readerWith(cardA), "", digits,
		keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, keystore.HardwareSlot{PublicKey: pubB, Label: "B"}, r)
	h.unlockWithToken()
	h.rec.reset()
	if e := h.c.RemoveSlot(h.slot("Paper").RecipientID, true, ""); e != nil {
		t.Fatal(e)
	}
	h.answerTokenUnlock("123456")
	end := h.done()
	if end.After == nil || len(end.After.RecoveryIDs) != 0 || end.After.Entangled || end.After.SetupNeeded {
		t.Fatalf("after the last recovery key's removal: %+v", end.After)
	}

	rh := newHarness(t, nil, nil)
	rh.unlockWithPassword()
	_, end = rh.replaceOnPassword("")
	if end.After == nil || !slices.Equal(end.After.RecoveryIDs, end.NewIDs) {
		t.Fatalf("after a replacement: %+v, new %v", end.After, end.NewIDs)
	}

	eh := newHarness(t, nil, nil)
	eh.unlockWithPassword()
	eh.rec.reset()
	if e := eh.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "spare"}); e != nil {
		t.Fatal(e)
	}
	eh.proveWithPassword()
	shown := eh.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepRecovery && s.PromptID == "" && s.After != nil
	}).(CeremonyState)
	waitIdle(t, eh)
	var want []string
	for _, s := range eh.recoveryViews() {
		want = append(want, s.RecoveryID)
	}
	if len(want) != 2 || !slices.Equal(shown.After.RecoveryIDs, want) {
		t.Fatalf("after an enrolment: %+v, the vault lists %v", shown.After, want)
	}
}

// 37. A replacement whose outcome is unknown is Broken, whether the conflict
// came after its commit or before it; the reopen shows which.
func TestAReplacementWhoseRetirementConflicts(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.c.seams.failAt = func(p string) error {
		if p == "retire" {
			return keystore.ErrConflict
		}
		return nil
	}
	digits, end := h.replaceOnPassword("")
	if end.Step != StepFailed || end.Error != CodeConflict || len(end.NewIDs) != 1 {
		t.Fatalf("end: %+v", end)
	}
	h.rec.waitState(t, StateBroken)
	h.c.seams.failAt = nil
	if e := h.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	h.unlockWithRecovery(digits[0])
	if ids := h.recoveryViews(); len(ids) != 1 || ids[0].RecoveryID != end.NewIDs[0] {
		t.Fatalf("after the reopen: %+v", ids)
	}
	h.lockAndSettle()
	h.recoveryRefused(h.recovery)

	h2 := newHarness(t, nil, nil)
	h2.unlockWithPassword()
	h2.c.seams.failAt = func(p string) error {
		if p == "commit" {
			return keystore.ErrConflict
		}
		return nil
	}
	_, end = h2.replaceOnPassword("")
	if end.Step != StepFailed || end.Error != CodeConflict {
		t.Fatalf("end: %+v", end)
	}
	h2.rec.waitState(t, StateBroken)
	h2.c.seams.failAt = nil
	if e := h2.c.Reopen(); e != nil {
		t.Fatal(e)
	}
	h2.unlockWithRecovery(h2.recovery)
	if ids := h2.recoveryViews(); len(ids) != 1 || ids[0].RecoveryID == end.NewIDs[0] {
		t.Fatalf("after the reopen: %+v", ids)
	}
}

// 38. The barrier never reads a handle a ceremony holds: a create during a
// removal, held between its change and its retirement, is refused with
// ceremony.in_progress at its barrier, whatever the cached fact says, since
// one let through would read the Session, and the handle with it, before its
// registry write met that write's own refusal (the outside review of the
// diff, finding 5). An archive re-key is refused there too, before its
// forgotten check and its open of a closed archive, both of which read the
// Session (the review of the repairs, finding A). A pending touch on the
// vault's own handle is held to the cached fact by the acts that adopt it.
// Part B's gate runs it under -race.
func TestTheBarrierLeavesACeremonysHandleAlone(t *testing.T) {
	for _, owed := range []bool{false, true} {
		t.Run(fmt.Sprintf("cached owed %v", owed), func(t *testing.T) {
			h := passwordsHarness(t, 2)
			h.unlockWithPassword()
			// The re-key's two archives, made before the removal begins: one
			// current and closed, one forgotten.
			kept, _ := h.newArchive("Kept")
			gone, _ := h.newArchive("Gone")
			if e := h.c.ForgetArchive(gone); e != nil {
				t.Fatal(e)
			}
			at, release := make(chan struct{}), make(chan struct{})
			// A failure while the ceremony is held still lets it go, before
			// the harness's own cleanup locks the vault and closes its file.
			let := sync.OnceFunc(func() { close(release) })
			t.Cleanup(let)
			h.c.seams.committed = func() {
				close(at)
				<-release
			}
			h.rec.reset()
			if e := h.c.RemoveSlot(h.slot("Second").RecipientID, false, ""); e != nil {
				t.Fatal(e)
			}
			h.proveWithPassword()
			<-at
			if owed {
				h.c.mu.Lock()
				h.c.vault.retireOwed = true
				h.c.mu.Unlock()
			}
			// A path the archive layer refuses: only the barrier can answer
			// ceremony.in_progress here, since a create that got past it
			// fails at its archive file with another code, and never reaches
			// the registry write's refusal of the same state.
			p := filepath.Join(h.dir, "no such folder", "during.enf")
			before := fileBytes(t, h.vault)
			if _, e := h.c.CreateArchive(p, "During", compressionNormal); !isCode(e, CodeCeremonyRunning) {
				t.Fatalf("a create during the removal: %v", e)
			}
			if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("the refused create left its file")
			}
			// A forgotten record's re-key answers ceremony.in_progress, not
			// archive.forgotten, which only a read of the registry through
			// the ceremony's handle could answer; a closed archive's is not
			// opened, an open unwrapping its key through the Session, and
			// a refused one closing it again with an archives event.
			mark := h.mark()
			if _, e := h.c.RotateKey(gone); !isCode(e, CodeCeremonyRunning) {
				t.Fatalf("a re-key of a forgotten archive during the removal: %v", e)
			}
			if _, e := h.c.RotateKey(kept); !isCode(e, CodeCeremonyRunning) {
				t.Fatalf("a re-key during the removal: %v", e)
			}
			for _, ev := range h.rec.snapshot()[mark:] {
				if ev.name == EventArchivesChanged {
					t.Fatal("the refused re-key opened its archive")
				}
			}
			// Nothing went through the ceremony's handle: no retiring commit
			// of the barrier's, no registry write of the create's or the
			// re-key's.
			if !bytes.Equal(fileBytes(t, h.vault), before) {
				t.Fatal("a refused write wrote the vault file through the ceremony's handle")
			}
			let()
			h.done()
		})
	}

	// A reveal, which the barrier admits since it writes nothing, cancelled
	// at its touch leaves the touch pending on the vault's own handle: the
	// keystore acts that begin while it stands are refused on the cached
	// fact, not by a read of that handle, and no ceremony begins over the
	// owed retirement.
	t.Run("a pending touch holds the handle", func(t *testing.T) {
		card, pub := cardWithKey("123456")
		r1, d1 := newRecoverySpec(t, "one")
		r2, _ := newRecoverySpec(t, "two")
		h := rekeyHarness(t, readerWith(card), "", d1, keystore.HardwareSlot{PublicKey: pub, Label: "A"}, r1, r2)
		h.unlockWithToken()
		f := installSwitch(t)
		h.c.seams.committed = func() { f.set(everyRegion) }
		h.rec.reset()
		if e := h.c.RemoveSlot(h.slot("two").RecipientID, false, ""); e != nil {
			t.Fatal(e)
		}
		h.answerTokenUnlock("123456")
		if st := h.done(); st.RetireError == "" {
			t.Fatalf("the removal's end: %+v", st)
		}
		h.c.seams.committed = nil
		if !h.owes() || !h.status().RetireOwed {
			t.Fatal("the control: the removal left nothing owed")
		}

		card.mu.Lock()
		card.holdTouch = true
		card.mu.Unlock()
		h.rec.reset()
		if e := h.c.RevealRecoveryKey(h.slot("one").RecipientID); e != nil {
			t.Fatalf("a reveal while owed: %v", e)
		}
		pin := h.rec.waitCeremony(t, StepPIN, true)
		h.c.SubmitSecret("pin", pin.PromptID, "123456")
		h.rec.waitCeremony(t, StepTouch, false)
		h.c.CancelUnlock()
		if st := h.end(); st.Step != StepFailed || st.Error != CodeCancelled {
			t.Fatalf("the cancelled reveal: %+v", st)
		}
		h.waitStatus(func(s VaultStatus) bool { return s.PendingTouch && s.Ceremony == nil })
		before := fileBytes(t, h.vault)
		mark := h.mark()
		if e := h.c.RotateNow(""); !isCode(e, CodeCeremonyRunning) {
			t.Fatalf("a re-key under the pending touch: %v", e)
		}
		h.noCeremonySince(mark, "the re-key under the pending touch")
		if _, e := h.c.CreateArchive(filepath.Join(h.dir, "no such folder", "x.enf"), "X", compressionNormal); !isCode(e, CodeCeremonyRunning) {
			t.Fatalf("a create under the pending touch: %v", e)
		}
		if !bytes.Equal(fileBytes(t, h.vault), before) {
			t.Fatal("a refused act wrote the vault file through the pending touch's handle")
		}

		// The key gives up: the handle is the core's again, and the barrier
		// reads it, tries the retirement, and refuses while it cannot land.
		card.giveUp()
		h.waitStatus(func(s VaultStatus) bool { return !s.PendingTouch })
		mark = h.mark()
		if e := h.c.RotateNow(""); !isCode(e, CodeRetireOwed) {
			t.Fatalf("a re-key once the touch is over: %v", e)
		}
		h.noCeremonySince(mark, "the re-key refused by the barrier")
		f.set(nil)
		card.mu.Lock()
		card.holdTouch = false
		card.mu.Unlock()
		h.rec.reset()
		if e := h.c.RotateNow(""); e != nil {
			t.Fatalf("a re-key once the fault is gone: %v", e)
		}
		h.answerTokenUnlock("123456")
		h.done()
		if h.owes() || h.generation() != 2 {
			t.Fatalf("after the re-key: owed %v, generation %d", h.owes(), h.generation())
		}
	})
}

// 39. A failure after the commit landed ends the act Done: a Session that
// cannot be derived locks the vault as a stale one does.
func TestAFailureAfterTheCommitEndsDone(t *testing.T) {
	sessionFails := func(p string) error {
		if p == "session" {
			return errInjected
		}
		return nil
	}
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	oldID := h.recoveryViews()[0].RecoveryID
	h.c.seams.failAt = sessionFails
	digits, end := h.replaceOnPassword("")
	if end.Step != StepDone || len(end.NewIDs) != 1 || !slices.Equal(end.OldIDs, []string{oldID}) || end.After == nil {
		t.Fatalf("end: %+v", end)
	}
	h.settleLock()
	if !h.logged("locked: stale") {
		t.Fatal("the vault was not locked as stale")
	}
	h.c.seams.failAt = nil
	h.unlockWithRecovery(digits[0])
	if ids := h.recoveryViews(); len(ids) != 1 || ids[0].RecoveryID != end.NewIDs[0] {
		t.Fatalf("the slots: %+v", ids)
	}
	h.lockAndSettle()
	h.recoveryRefused(h.recovery)

	h2 := passwordsHarness(t, 2)
	h2.unlockWithPassword()
	second := h2.slot("Second").RecipientID
	h2.c.seams.failAt = sessionFails
	if st := h2.removeOnPassword(second, true, ""); st.Step != StepDone {
		t.Fatalf("end: %+v", st)
	}
	h2.settleLock()
	h2.c.seams.failAt = nil
	h2.unlockWithPassword()
	for _, s := range h2.c.Slots() {
		if s.RecipientID == second {
			t.Fatal("the slot is still listed")
		}
	}
}

// 40. A lost YubiKey with no spare is withdrawn: a second recovery key is
// added on the first one's proof, and the YubiKey is then removed on a
// recovery key's proof, the vault left with recovery keys alone.
func TestALostYubiKeyWithNoSpareIsWithdrawn(t *testing.T) {
	h, cardA, _ := entangledHarness(t, "the vault password")
	pubA := cardA.keys[0x9d].PublicKey().Bytes()
	a := h.slot("Test key").RecipientID
	h.unlockWithRecovery(h.recovery)
	h.rec.reset()
	if e := h.c.BeginEnroll(EnrollOptions{Kind: EnrollRecovery, Label: "spare", WithRecoveryKey: true}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(h.recovery)
	shown := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepRecovery && s.PromptID == "" && strings.HasPrefix(s.SlotLabel, "http://")
	}).(CeremonyState)
	spare, _, _, ok := h.c.preview.secretValue(secretToken(shown.SlotLabel))
	if !ok {
		t.Fatal("the spare is not behind its URL")
	}
	waitIdle(t, h)
	h.rec.reset()
	if e := h.c.RemoveSlot(a, true, ""); e != nil {
		t.Fatal(e)
	}
	first := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.PromptID != ""
	}).(CeremonyState)
	if first.Step != StepRecovery || first.Without != a {
		t.Fatalf("the first prompt: %+v", first)
	}
	h.c.SubmitSecret("recovery", first.PromptID, h.recovery)
	end := h.done()
	if g := h.generation(); g != 2 || !h.status().SetupNeeded || end.After == nil || !end.After.SetupNeeded {
		t.Fatalf("generation %d, %+v, after %+v", g, h.status(), end.After)
	}
	// The password stays on with no YubiKey left, so that the end words
	// the setup's refusal (vault.setup_password_on) from what is on disk.
	if !end.After.Entangled || !h.status().Entangled || len(end.After.RecoveryIDs) != 2 {
		t.Fatalf("after %+v, status entangled %v", end.After, h.status().Entangled)
	}
	img := fileBytes(t, h.vault)
	ridA := ridOf(t, a)
	if bytes.Contains(img, pubA) || bytes.Contains(img, ridA[:]) {
		t.Fatal("the YubiKey is still in the file: it would find its slot")
	}
	h.lockAndSettle()
	h.unlockWithRecovery(h.recovery)
	h.lockAndSettle()
	h.unlockWithRecovery(spare)
}

// Finish setting up writes a secret, so it waits behind an owed retirement
// like every such mutation: it runs while Locked, with no Session for the
// barrier, so its ceremony finishes the retirement itself once the recovery
// key has given it an Unlocked, and is refused with vault.retire_owed,
// nothing of it written, while that cannot land (design 2.4; no test is
// named for it there, so this one is added).
func TestFinishSetupRetiresBeforeItsFirstWayIn(t *testing.T) {
	cardA, pubA := cardWithKey("123456")
	cards := readerWith(cardA)
	r1, d1 := newRecoverySpec(t, "one")
	r2, _ := newRecoverySpec(t, "two")
	h := rekeyHarness(t, cards, "", d1, keystore.HardwareSlot{PublicKey: pubA, Label: "A"}, r1, r2)
	h.unlockWithToken()
	ridA := ridOf(t, h.slot("A").RecipientID)
	failRetire := func(p string) error {
		if p == "retire" {
			return errInjected
		}
		return nil
	}
	h.c.seams.failAt = failRetire
	h.rec.reset()
	if e := h.c.RemoveSlot(hexID(ridA), true, ""); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(d1)
	if st := h.done(); st.RetireError == "" {
		t.Fatalf("the removal's end: %+v", st)
	}
	h.lockAndSettle()
	if st := h.status(); !st.SetupNeeded || st.Entangled || !st.RetireOwed {
		t.Fatalf("the lock screen: %+v", st)
	}
	before := fileBytes(t, h.vault)
	cardB, _ := cardWithKey("654321")
	cards.setCard(cardB)
	h.rec.reset()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "B"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(d1)
	if st := h.end(); st.Step != StepFailed || st.Error != CodeRetireOwed {
		t.Fatalf("a setup while the retirement cannot land: %+v", st)
	}
	if !bytes.Equal(fileBytes(t, h.vault), before) {
		t.Fatal("the refused setup wrote the file")
	}

	// With the fault gone the retirement lands at the setup's start, before
	// its first way in: a setup cancelled at the PIN has retired the file
	// and written nothing else, and the lock screen shows the date and the
	// size the retiring commit left.
	h.c.seams.failAt = nil
	h.rec.reset()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "B"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(d1)
	h.rec.waitCeremony(t, StepPIN, true)
	h.c.CancelUnlock()
	if st := h.end(); st.Step != StepFailed || st.Error != CodeCancelled {
		t.Fatalf("the setup cancelled at its PIN: %+v", st)
	}
	if st := h.status(); st.State != StateLocked || st.RetireOwed || slices.Contains(st.Warnings, CodeRetireOwed) || !st.SetupNeeded {
		t.Fatalf("after the cancelled setup: %+v", st)
	}
	if holdsID(t, h.vault, ridA) {
		t.Fatal("the setup's retirement left the removed YubiKey in the file")
	}
	requireFileFacts(t, h, "after the setup's retirement")

	h.rec.reset()
	if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollToken, Label: "B"}); e != nil {
		t.Fatal(e)
	}
	h.answerRecovery(d1)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "654321")
	h.done()
	h.rec.waitState(t, StateUnlocked)
	if h.owes() || h.status().SetupNeeded || holdsID(t, h.vault, ridA) {
		t.Fatalf("after the setup: owed %v, %+v", h.owes(), h.status())
	}
}

// ---- The outside review of the diff (2026-10-28), findings 3 to 7.

// unwrapsHere reports whether the record's current version unwraps under the
// Session the vault has now, the one every later open uses.
func (h *harness) unwrapsHere(aid [16]byte) error {
	h.c.mu.Lock()
	defer h.c.mu.Unlock()
	sess, e := h.c.sessionLocked()
	if e != nil {
		return e
	}
	rec := findRecord(sess.Registry(), aid)
	if rec == nil {
		return errNoRecord
	}
	for i := range rec.Versions {
		if rec.Versions[i].KID == rec.CurrentKID {
			key, err := sess.UnwrapArchiveKey(aid, &rec.Versions[i])
			kdf.Zero(key[:])
			return err
		}
	}
	return errors.New("no current version")
}

// errNoRecord is unwrapsHere's answer for a record the registry does not hold.
var errNoRecord = errors.New("no such record")

// Finding 3. A new archive's key is wrapped under the generation that
// publishes its record: a re-key that lands between the archive file and its
// record leaves a record whose key the vault's Session unwraps, and the
// archive opens. Wrapped before the re-key, it would be published under the
// KWK the re-key replaced.
func TestANewArchiveIsWrappedAtItsPublication(t *testing.T) {
	h := passwordsHarness(t, 1)
	h.unlockWithPassword()
	at, release := make(chan struct{}), make(chan struct{})
	h.c.seams.created = func() {
		close(at)
		<-release
	}
	type created struct {
		id string
		e  *Error
	}
	res := make(chan created, 1)
	go func() {
		id, e := h.c.CreateArchive(filepath.Join(h.dir, "new.enf"), "New", compressionNormal)
		res <- created{id, e}
	}()
	<-at
	h.rec.reset()
	if e := h.c.RotateNow(""); e != nil {
		t.Fatalf("a re-key while the create waits: %v", e)
	}
	h.proveWithPassword()
	h.done()
	if g := h.generation(); g != 2 {
		t.Fatalf("the control: generation %d after the re-key", g)
	}
	close(release)
	r := <-res
	if r.e != nil {
		t.Fatalf("the create: %v", r.e)
	}
	if err := h.unwrapsHere(ridOf(t, r.id)); err != nil {
		t.Fatalf("the published key does not unwrap under the vault's Session: %v", err)
	}
	if _, e := h.c.OpenArchive(r.id); e != nil {
		t.Fatalf("the new archive does not open: %v", e)
	}
}

// Finding 4. A merge handle holds keys wrapped under the KWK of the
// generation it was read at: a re-keying act drops it, and the merge answers
// ceremony.stale_prompt rather than publish keys no Session of the vault
// unwraps. Read again, the records merge at the new generation.
func TestAReKeyDropsTheMergeHandles(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	aid := kid(0x61)
	f.add(format.ArchiveRecord{
		ArchiveID: aid, Name: "New there", CreatedAt: 100, CurrentKID: kid(0x62), Revision: 3,
		Versions: []format.VersionRecord{f.version(aid, kid(0x62), 100)},
	})
	f.commit()
	handle := h.inspectForeign(f)
	waitIdle(t, h)
	h.rec.reset()
	if e := h.c.RotateNow(""); e != nil {
		t.Fatal(e)
	}
	h.proveWithPassword()
	h.done()
	if g := h.generation(); g != 2 {
		t.Fatalf("the control: generation %d after the re-key", g)
	}
	if e := h.c.MergeRecords(handle, []string{hexID(aid)}); !isCode(e, CodeStalePrompt) {
		t.Fatalf("a merge of records read before the re-key: %v", e)
	}
	if _, e := h.c.IncomingRecords(handle); !isCode(e, CodeStalePrompt) {
		t.Fatalf("the handle after the re-key: %v", e)
	}
	if err := h.unwrapsHere(aid); !errors.Is(err, errNoRecord) {
		t.Fatalf("the stale handle's record: %v", err)
	}

	handle = h.inspectForeign(f)
	waitIdle(t, h)
	if e := h.c.MergeRecords(handle, []string{hexID(aid)}); e != nil {
		t.Fatalf("a merge read at the new generation: %v", e)
	}
	if err := h.unwrapsHere(aid); err != nil {
		t.Fatalf("the merged key does not unwrap under the vault's Session: %v", err)
	}
}

// Finding 5. Registry writes, creates and archive re-keys made while a
// ceremony commits through the vault's handle are refused before they touch
// it: the ownership check comes before the Session is read, in the barrier
// and in the registry write alike, and a re-key meets the barrier before its
// forgotten check and its open (the review of the repairs, finding A). The
// calls overlap the ceremony's proof, its commit and the moment after it,
// the ceremony held there. Under -race (Part B's gate) a read of the handle
// meanwhile is a reported race; without it, a create let through fails at
// its archive file with another code.
func TestWritesDuringACommitLeaveTheHandleAlone(t *testing.T) {
	h := passwordsHarness(t, 2)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	at, release := make(chan struct{}), make(chan struct{})
	h.c.seams.committed = func() {
		close(at)
		<-release
	}
	h.rec.reset()
	if e := h.c.RemoveSlot(h.slot("Second").RecipientID, false, ""); e != nil {
		t.Fatal(e)
	}
	var (
		stop  atomic.Bool
		turns atomic.Int64
		wg    sync.WaitGroup
		mu    sync.Mutex
		bad   []string
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		p := filepath.Join(h.dir, "no such folder", "during.enf")
		for i := 0; !stop.Load(); i++ {
			if e := h.c.RenameArchive(id, fmt.Sprintf("A %d", i)); !isCode(e, CodeCeremonyRunning) {
				mu.Lock()
				bad = append(bad, fmt.Sprintf("a rename: %v", e))
				mu.Unlock()
			}
			if _, e := h.c.CreateArchive(p, "During", compressionNormal); !isCode(e, CodeCeremonyRunning) {
				mu.Lock()
				bad = append(bad, fmt.Sprintf("a create: %v", e))
				mu.Unlock()
			}
			if _, e := h.c.RotateKey(id); !isCode(e, CodeCeremonyRunning) {
				mu.Lock()
				bad = append(bad, fmt.Sprintf("a re-key: %v", e))
				mu.Unlock()
			}
			turns.Add(1)
		}
	}()
	h.proveWithPassword()
	<-at
	// The ceremony is held after its commit. This goroutine takes no lock
	// the writer takes until it stops it, so nothing orders the writer's
	// calls after the commit's writes but their own reads, if they read.
	first := turns.Load()
	deadline := time.Now().Add(5 * time.Second)
	for turns.Load() < first+3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()
	if n := turns.Load(); n < first+3 {
		t.Fatalf("the control: %d calls after the commit", n-first)
	}
	close(release)
	h.done()
	mu.Lock()
	defer mu.Unlock()
	if len(bad) > 0 {
		t.Fatalf("%d calls were not refused before the handle, the first: %s", len(bad), bad[0])
	}
}

// interloper, armed, writes a valid superblock copy with the next seq over
// the losing copy at the next scrub's start, as a writer that ignores the
// file's lock would (keystore test 33): the scrub's check of the disk then
// finds it, and the handle is Broken with ErrConflict after a flip that
// landed.
func interloper(t *testing.T, path string) (arm func()) {
	t.Helper()
	var armed atomic.Bool
	faultOn(t, func(step string) error {
		if step != "scrub" || !armed.CompareAndSwap(true, false) {
			return nil
		}
		img, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		a := img[format.KeystoreSuperblockAOff : format.KeystoreSuperblockAOff+format.SuperblockSize]
		b := img[format.KeystoreSuperblockBOff : format.KeystoreSuperblockBOff+format.SuperblockSize]
		live, which, _, err := format.PickKeystoreSuperblock(a, b)
		if err != nil {
			return err
		}
		next := *live
		next.Seq++
		enc, err := next.Encode()
		if err != nil {
			return err
		}
		f, err := os.OpenFile(path, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.WriteAt(enc, int64(which.Other().KeystoreSuperblockOff()))
		return err
	})
	return func() { armed.Store(true) }
}

// Finding 6. A registry write whose flip landed and whose scrub then found
// another writer's commit on disk reports success with the keystore's handle
// Broken: the write stands, the vault enters Broken and the page is told,
// and Reopen settles it. A handle that went Broken by a path that did not say
// so is met the same way at the next use of the Session.
func TestAConflictTheScrubFindsIsBroken(t *testing.T) {
	t.Run("a registry write", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		h.unlockWithPassword()
		id, _ := h.newArchive("A")
		arm := interloper(t, h.vault)
		h.rec.reset()
		arm()
		if e := h.c.RenameArchive(id, "B"); e != nil {
			t.Fatalf("a rename whose flip landed: %v", e)
		}
		if st := h.status(); st.State != StateBroken {
			t.Fatalf("after the conflict: %s", st.State)
		}
		h.rec.waitState(t, StateBroken)
		if e := h.c.Reopen(); e != nil {
			t.Fatalf("the reopen: %v", e)
		}
		if st := h.status(); st.State != StateLocked {
			t.Fatalf("after the reopen: %s", st.State)
		}
	})
	t.Run("the Session's next use", func(t *testing.T) {
		h := newHarness(t, nil, nil)
		h.unlockWithPassword()
		id, _ := h.newArchive("A")
		arm := interloper(t, h.vault)
		arm()
		// Through the Session itself, past the registry write's own check:
		// the handle Broken and nothing told.
		h.c.mu.Lock()
		err := h.c.vault.sess.UpdateRegistry(func(g *format.Registry) error { g.IdleMinutes++; return nil })
		state := h.c.vault.state
		h.c.mu.Unlock()
		if err != nil || state != StateUnlocked {
			t.Fatalf("the control: %v, %s", err, state)
		}
		h.rec.reset()
		if e := h.c.RenameArchive(id, "B"); !isCode(e, CodeConflict) {
			t.Fatalf("a rename over the broken handle: %v", e)
		}
		h.rec.waitState(t, StateBroken)
		if e := h.c.Reopen(); e != nil {
			t.Fatalf("the reopen: %v", e)
		}
	})
}

// freshOwed is what a fresh open of the file says it owes, while the core
// holds no handle on it.
func freshOwed(t *testing.T, path string) bool {
	t.Helper()
	ks, err := keystore.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	return ks.RetireOwed()
}

// Finding 7. Every write Finish setting up makes is retired inside it, and
// the lock screen's facts follow the file: a first way in whose commit failed
// after writing its record is retired at once, as a failed change is; one
// that landed before a cancel ends the act Done over the Locked state,
// retired, rather than Cancelled with the file owed.
func TestFinishSetupRetiresItsOwnWrites(t *testing.T) {
	setupHarness := func(t *testing.T) *harness {
		t.Helper()
		// A backup: its recovery key alone, the one file Finish setting up
		// is for (a vault is created with two independent ways in).
		r, digits := newRecoverySpec(t, "Recovery key")
		dir := t.TempDir()
		src := filepath.Join(dir, "source.eks")
		writeVault(t, src, "", r, keystore.PasswordSlot{Password: testPassword, Argon2: fast, Label: "Password"})
		path := filepath.Join(dir, "vault.eks")
		exportBackup(t, src, path)
		h := harnessOver(t, nil, dir, path, digits, "")
		if st := h.status(); !st.SetupNeeded || st.RetireOwed {
			t.Fatalf("the control: %+v", st)
		}
		return h
	}
	finish := func(h *harness) CeremonyState {
		h.t.Helper()
		h.rec.reset()
		if e := h.c.FinishSetup(EnrollOptions{Kind: EnrollPassword, Label: "Password"}); e != nil {
			h.t.Fatal(e)
		}
		h.answerRecovery(h.recovery)
		h.chooseNew(testPassword)
		return h.end()
	}

	t.Run("a first way in that fails after writing", func(t *testing.T) {
		h := setupHarness(t)
		f := installSwitch(t)
		var mu sync.Mutex
		var stray []format.SlotRecord
		f.set(func(step string) error {
			if step != "registry" {
				return nil
			}
			mu.Lock()
			stray = strayRecords(h.vault)
			mu.Unlock()
			f.set(nil)
			return errInjected
		})
		if st := finish(h); st.Step != StepFailed {
			t.Fatalf("end: %+v", st)
		}
		mu.Lock()
		got := stray
		mu.Unlock()
		if len(got) != 1 {
			t.Fatalf("the control: the failed commit's region holds %d new records", len(got))
		}
		if holdsRecord(t, h.vault, got[0]) {
			t.Fatal("the raw bytes still hold the first way in never added")
		}
		if st := h.status(); st.State != StateLocked || !st.SetupNeeded || st.RetireOwed || freshOwed(t, h.vault) {
			t.Fatalf("after the failed setup: %+v, the file owes %v", st, freshOwed(t, h.vault))
		}
		requireFileFacts(t, h, "after the failed setup")
	})

	t.Run("a cancel after the first way in landed", func(t *testing.T) {
		h := setupHarness(t)
		f := installSwitch(t)
		f.set(func(step string) error {
			if step == "scrub" {
				// The first way in's flip has landed: the cancel comes after
				// its commit.
				f.set(nil)
				h.c.CancelUnlock()
			}
			return nil
		})
		if st := finish(h); st.Step != StepDone {
			t.Fatalf("end: %+v", st)
		}
		if st := h.status(); st.State != StateLocked || st.SetupNeeded || !st.HasPasswordSlot || st.RetireOwed || freshOwed(t, h.vault) {
			t.Fatalf("after the cancelled setup: %+v, the file owes %v", st, freshOwed(t, h.vault))
		}
		requireFileFacts(t, h, "after the cancelled setup")
		h.unlockWithPassword()
	})
}
