package app

import (
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/format"
	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// foreign is a second keystore file with records of its own, so that a merge
// has something real to offer: its version keys are wrapped under its own
// vault's KWK, which is what the keystore's conversion re-wraps.
type foreign struct {
	t      *testing.T
	path   string
	digits string // its recovery key, empty when it has none
	unl    *keystore.Unlocked
	sess   *keystore.Session
	recs   []format.ArchiveRecord
}

func newForeign(t *testing.T, path string, recovery bool) *foreign {
	t.Helper()
	f := &foreign{t: t, path: path}
	var specs []keystore.SlotSpec
	if recovery {
		rk, err := kdf.NewRecoveryKey()
		if err != nil {
			t.Fatal(err)
		}
		f.digits = rk.Digits()
		specs = []keystore.SlotSpec{
			keystore.PasswordSlot{Password: "the other vault", Argon2: fast, Label: "Password"},
			keystore.RecoverySlot{Key: rk, Label: "Recovery key"},
		}
	} else {
		// Two keys and no recovery slot: two independent ways in, and none of
		// them a key this vault could be asked for. No card is touched — the
		// public keys are all a slot record holds.
		specs = []keystore.SlotSpec{
			keystore.HardwareSlot{PublicKey: newFakeCard("123456").addKey(0x9d, true), Label: "Key A"},
			keystore.HardwareSlot{PublicKey: newFakeCard("654321").addKey(0x9d, true), Label: "Key B"},
		}
	}
	unl, err := keystore.Create(path, keystore.CreateOptions{Slots: specs})
	if err != nil {
		t.Fatal(err)
	}
	sess, err := unl.Session()
	if err != nil {
		t.Fatal(err)
	}
	f.unl, f.sess = unl, sess
	return f
}

// version wraps a fresh archive key under the foreign vault, so the record it
// goes into converts cleanly for the vault kept here.
func (f *foreign) version(archiveID, kid [16]byte, createdAt int64) format.VersionRecord {
	f.t.Helper()
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		f.t.Fatal(err)
	}
	w, n, err := f.sess.WrapArchiveKey(archiveID, kid, key)
	if err != nil {
		f.t.Fatal(err)
	}
	return format.VersionRecord{KID: kid, WrappedArchiveKey: w, WrapNonce: n, CreatedAt: createdAt, State: format.VersionCurrent}
}

// retired is version with the state a key that is no longer current has.
func (f *foreign) retired(archiveID, kid [16]byte, createdAt int64) format.VersionRecord {
	v := f.version(archiveID, kid, createdAt)
	v.State, v.RetiredAt = format.VersionRetired, createdAt+1
	return v
}

// add records one archive in the foreign registry, current version last.
func (f *foreign) add(rec format.ArchiveRecord) { f.recs = append(f.recs, rec) }

// commit writes the records and closes the file.
func (f *foreign) commit() {
	f.t.Helper()
	if err := f.sess.UpdateRegistry(func(g *format.Registry) error {
		g.Archives = append(g.Archives, f.recs...)
		return nil
	}); err != nil {
		f.t.Fatal(err)
	}
	ks := f.unl.Keystore()
	f.sess.Lock()
	f.unl.Close()
	ks.Close()
}

// kid is a readable key id for a test's records.
func kid(b byte) [16]byte {
	var k [16]byte
	for i := range k {
		k[i] = b
	}
	return k
}

// settle waits for a lock's unbounded half to close the vault's handle, so a
// test may replace the file underneath.
func (h *harness) settleLock() {
	h.t.Helper()
	h.rec.waitState(h.t, StateLocked)
	h.c.lockWG.Wait()
}

// mergeFrom runs the merge ceremony over a foreign file, answering this
// vault's password and then that file's own recovery key.
func (h *harness) inspectForeign(f *foreign) string {
	h.t.Helper()
	h.rec.reset()
	if e := h.c.InspectRecords(f.path); e != nil {
		h.t.Fatalf("inspect records: %v", e)
	}
	p := h.rec.waitCeremony(h.t, StepPassword, true)
	if e := h.c.SubmitSecret("password", p.PromptID, testPassword); e != nil {
		h.t.Fatal(e)
	}
	r := h.rec.waitCeremony(h.t, StepRecovery, true)
	if e := h.c.SubmitSecret("recovery", r.PromptID, f.digits); e != nil {
		h.t.Fatal(e)
	}
	st := h.rec.waitCeremony(h.t, StepRecords, false)
	if st.SlotLabel == "" {
		h.t.Fatalf("no handle: %+v", st)
	}
	return st.SlotLabel
}

// rowFor finds one incoming row by archive id.
func rowFor(rows []IncomingRecord, id string) IncomingRecord {
	for _, r := range rows {
		if r.ArchiveID == id {
			return r
		}
	}
	return IncomingRecord{}
}

// A backup of this vault at the current generation opens with the current
// VMK: no key from the user at all.
func TestInspectRecordsOpensWithTheCurrentVMK(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	backup := filepath.Join(h.dir, "backup.eks")
	if e := h.exportBackup(backup); e != nil {
		t.Fatal(e)
	}
	mark := len(h.rec.snapshot())
	handle := h.inspectRecords(backup)
	for _, ev := range h.rec.snapshot()[mark:] {
		if s, ok := ev.payload.(CeremonyState); ok && s.Kind == "records" && s.Step == StepRecovery {
			t.Fatalf("a recovery key was asked for a backup of this vault: %+v", s)
		}
	}
	rows, e := h.c.IncomingRecords(handle)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0].ArchiveID != id {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Action != "skip" || rows[0].Ticked {
		t.Fatalf("a record the vault already has whole: %+v", rows[0])
	}
}

// A backup from an earlier generation opens with a vmk_history VMK, still
// with no key from the user (FORMAT.md §18.2).
func TestInspectRecordsOpensWithAHistoryVMK(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	backup := filepath.Join(h.dir, "backup.eks")
	if e := h.exportBackup(backup); e != nil {
		t.Fatal(e)
	}
	h.rec.reset()
	if e := h.c.RotateNow(); e != nil {
		t.Fatalf("rotate: %v", e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	h.rec.waitCeremony(t, StepDone, false)
	mark := len(h.rec.snapshot())
	handle := h.inspectRecords(backup)
	for _, ev := range h.rec.snapshot()[mark:] {
		if s, ok := ev.payload.(CeremonyState); ok && s.Kind == "records" && s.Step == StepRecovery {
			t.Fatalf("a recovery key was asked for a backup from a generation this vault keeps: %+v", s)
		}
	}
	rows, _ := h.c.IncomingRecords(handle)
	if len(rows) != 1 || rows[0].ArchiveID != id {
		t.Fatalf("rows: %+v", rows)
	}
}

// A backup from a LATER generation — this vault was rolled back to an older
// copy — is not readable by any VMK here and asks for that file's own
// recovery key.
func TestInspectRecordsAsksTheRecoveryKeyForALaterGeneration(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	older, err := os.ReadFile(h.vault)
	if err != nil {
		t.Fatal(err)
	}
	h.rec.reset()
	if e := h.c.RotateNow(); e != nil {
		t.Fatalf("rotate: %v", e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	h.rec.waitCeremony(t, StepDone, false)
	later := filepath.Join(h.dir, "later.eks")
	if e := h.exportBackup(later); e != nil {
		t.Fatal(e)
	}
	// Roll the vault back to the copy from before the rotation.
	h.c.LockNow(ReasonManual)
	h.settleLock()
	if err := os.WriteFile(h.vault, older, 0o600); err != nil {
		t.Fatal(err)
	}
	if e := h.c.Reopen(); e != nil {
		t.Fatalf("reopen: %v", e)
	}
	h.rec.reset()
	h.unlockWithPassword()
	h.rec.reset()
	if e := h.c.InspectRecords(later); e != nil {
		t.Fatal(e)
	}
	pw := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", pw.PromptID, testPassword)
	r := h.rec.waitCeremony(t, StepRecovery, true)
	if e := h.c.SubmitSecret("recovery", r.PromptID, h.recovery); e != nil {
		t.Fatal(e)
	}
	st := h.rec.waitCeremony(t, StepRecords, false)
	rows, e := h.c.IncomingRecords(st.SlotLabel)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0].ArchiveID != id {
		t.Fatalf("rows: %+v", rows)
	}
}

// A file no VMK and no key opens is not this vault — never corruption.
func TestInspectRecordsReportsNotThisVaultRatherThanCorruption(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	other := filepath.Join(h.dir, "other.eks")
	f := newForeign(t, other, false) // no recovery slot: nothing to ask for
	f.add(format.ArchiveRecord{
		ArchiveID: kid(0x21), Name: "Theirs", CreatedAt: 100, CurrentKID: kid(0x22),
		Versions: []format.VersionRecord{f.version(kid(0x21), kid(0x22), 100)},
	})
	f.commit()
	h.rec.reset()
	if e := h.c.InspectRecords(other); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, testPassword)
	st := h.rec.waitCeremony(t, StepFailed, false)
	if st.Error != CodeNotThisVault {
		t.Fatalf("the verdict: %+v", st)
	}
}

// The file's own plaintext vmk_generation orders the attempts and decides
// nothing: doctored, the same VMK still opens it (FORMAT.md §5, §18.2).
func TestInspectRecordsIgnoresTheFilesGenerationForTheVerdict(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	backup := filepath.Join(h.dir, "backup.eks")
	if e := h.exportBackup(backup); e != nil {
		t.Fatal(e)
	}
	doctorGeneration(t, backup, 9999)
	mark := len(h.rec.snapshot())
	handle := h.inspectRecords(backup)
	for _, ev := range h.rec.snapshot()[mark:] {
		if s, ok := ev.payload.(CeremonyState); ok && s.Kind == "records" && s.Step == StepRecovery {
			t.Fatalf("the doctored generation changed which candidate was tried: %+v", s)
		}
	}
	rows, _ := h.c.IncomingRecords(handle)
	if len(rows) != 1 || rows[0].ArchiveID != id {
		t.Fatalf("rows: %+v", rows)
	}
}

// doctorGeneration rewrites both superblock copies' vmk_generation, checksum
// recomputed, leaving every authenticated byte alone.
func doctorGeneration(t *testing.T, path string, gen uint64) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, off := range []int{format.KeystoreSuperblockAOff, format.KeystoreSuperblockBOff} {
		sb, err := format.DecodeKeystoreSuperblock(b[off : off+format.SuperblockSize])
		if err != nil {
			continue // an unused copy
		}
		sb.VMKGeneration = gen
		enc, err := sb.Encode()
		if err != nil {
			t.Fatal(err)
		}
		copy(b[off:off+format.SuperblockSize], enc)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// A field the two vaults hold differently keeps the local value and the row
// says what the other had.
func TestMergeKeepsLocalValuesAndSaysWhatDiffered(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	h.c.SetArchiveDescription(id, "mine")
	aid, _ := parseID(id)
	local := h.record(id)
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	f.add(format.ArchiveRecord{
		ArchiveID: aid, Name: "Photos 2023", Description: "theirs", Policy: format.PolicyHidden,
		CreatedAt: local.CreatedAt, CurrentKID: local.CurrentKID,
		Versions: []format.VersionRecord{f.version(aid, local.CurrentKID, local.CreatedAt)},
	})
	f.commit()
	handle := h.inspectForeign(f)
	rows, _ := h.c.IncomingRecords(handle)
	row := rowFor(rows, id)
	fields := map[string]string{}
	for _, d := range row.Differs {
		fields[d.Field] = d.Theirs
	}
	if fields["name"] != "Photos 2023" || fields["description"] != "theirs" || fields["policy"] != "hidden" {
		t.Fatalf("differs: %+v", row.Differs)
	}
	if e := h.c.MergeRecords(handle, []string{id}); e != nil {
		t.Fatalf("merge: %v", e)
	}
	got := h.record(id)
	if got.Name != "A" || got.Description != "mine" || got.Policy&format.PolicyHidden != 0 {
		t.Fatalf("the local values did not stand: %+v", got)
	}
}

// always_require_full_auth is set if either side has it and cleared by
// neither.
func TestMergeUnionsAlwaysRequireFullAuth(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	on, _ := h.newArchive("On")
	off, _ := h.newArchive("Off")
	onID, _ := parseID(on)
	offID, _ := parseID(off)
	h.editRecord(on, func(a *format.ArchiveRecord, _ int64) { a.Policy |= format.PolicyAlwaysRequireFullAuth })
	lOn, lOff := h.record(on), h.record(off)
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	// Theirs has it where ours does not, and lacks it where ours has it.
	f.add(format.ArchiveRecord{
		ArchiveID: offID, Name: "Off", Policy: format.PolicyAlwaysRequireFullAuth,
		CreatedAt: lOff.CreatedAt, CurrentKID: lOff.CurrentKID,
		Versions: []format.VersionRecord{f.version(offID, lOff.CurrentKID, lOff.CreatedAt)},
	})
	f.add(format.ArchiveRecord{
		ArchiveID: onID, Name: "On", CreatedAt: lOn.CreatedAt, CurrentKID: lOn.CurrentKID,
		Versions: []format.VersionRecord{f.version(onID, lOn.CurrentKID, lOn.CreatedAt)},
	})
	f.commit()
	handle := h.inspectForeign(f)
	rows, _ := h.c.IncomingRecords(handle)
	if r := rowFor(rows, off); r.Action != "version" || !r.Ticked {
		t.Fatalf("a record that gains the flag: %+v", r)
	}
	if r := rowFor(rows, on); r.Action != "skip" {
		t.Fatalf("a record that would lose nothing: %+v", r)
	}
	if e := h.c.MergeRecords(handle, []string{off, on}); e != nil {
		t.Fatal(e)
	}
	if h.record(off).Policy&format.PolicyAlwaysRequireFullAuth == 0 {
		t.Fatal("the flag was not taken from the other side")
	}
	if h.record(on).Policy&format.PolicyAlwaysRequireFullAuth == 0 {
		t.Fatal("the flag was cleared by a side that did not have it")
	}
}

// The fields that describe a copy are the other vault's observation of its
// own file and are never taken.
func TestMergeNeverTakesTheCopyFields(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	aid, _ := parseID(id)
	local := h.record(id)
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	f.add(format.ArchiveRecord{
		ArchiveID: aid, Name: "A", CreatedAt: local.CreatedAt, CurrentKID: kid(0x31),
		LastPath: `D:\theirs\A.enf`, LastSeq: 99, HashAtSeq: 99, LastStoredSize: 123456,
		LastWrittenAt: 4_000_000_000, LastCiphertextHash: [32]byte{0xAA},
		Versions: []format.VersionRecord{f.version(aid, kid(0x31), local.CreatedAt)},
	})
	f.commit()
	handle := h.inspectForeign(f)
	if e := h.c.MergeRecords(handle, []string{id}); e != nil {
		t.Fatal(e)
	}
	got := h.record(id)
	switch {
	case got.LastPath != local.LastPath:
		t.Fatalf("last_path was taken: %q", got.LastPath)
	case got.LastSeq != local.LastSeq || got.HashAtSeq != local.HashAtSeq:
		t.Fatalf("last_seq/hash_at_seq were taken: %d/%d", got.LastSeq, got.HashAtSeq)
	case got.LastStoredSize != local.LastStoredSize:
		t.Fatalf("last_stored_size was taken: %d", got.LastStoredSize)
	case got.LastWrittenAt != local.LastWrittenAt:
		t.Fatalf("last_written_at was taken: %d", got.LastWrittenAt)
	case got.LastCiphertextHash != local.LastCiphertextHash:
		t.Fatal("last_ciphertext_hash was taken")
	case len(got.Versions) != 2:
		t.Fatalf("the version was not taken: %d", len(got.Versions))
	}
}

// Version lists union — the incoming key becomes a retired version here,
// since the file's envelope decides which is current — and a field the local
// record leaves empty is filled.
func TestMergeUnionsVersionListsAndFillsEmptyFields(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	aid, _ := parseID(id)
	local := h.record(id)
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	f.add(format.ArchiveRecord{
		ArchiveID: aid, Name: "A", Description: "the description we never wrote",
		CreatedAt: local.CreatedAt, CurrentKID: kid(0x41),
		Versions: []format.VersionRecord{
			f.retired(aid, local.CurrentKID, local.CreatedAt),
			f.version(aid, kid(0x41), local.CreatedAt),
		},
	})
	f.commit()
	handle := h.inspectForeign(f)
	rows, _ := h.c.IncomingRecords(handle)
	if r := rowFor(rows, id); r.Action != "version" || !r.Ticked || r.Versions != 2 {
		t.Fatalf("row: %+v", r)
	}
	if e := h.c.MergeRecords(handle, []string{id}); e != nil {
		t.Fatal(e)
	}
	got := h.record(id)
	if got.Description != "the description we never wrote" {
		t.Fatalf("the empty field was not filled: %q", got.Description)
	}
	if got.CurrentKID != local.CurrentKID {
		t.Fatal("current_kid was taken from the other side")
	}
	if len(got.Versions) != 2 {
		t.Fatalf("versions: %d", len(got.Versions))
	}
	current := 0
	for _, v := range got.Versions {
		if v.State == format.VersionCurrent {
			current++
		}
		if v.KID == kid(0x41) && (v.State != format.VersionRetired || v.RetiredAt == 0) {
			t.Fatalf("the incoming key was not retired here: %+v", v)
		}
	}
	if current != 1 {
		t.Fatalf("%d current versions", current)
	}
	// The key that came in opens with this vault's own session: it was
	// re-wrapped inside the keystore, never as a bare key here.
	h.c.mu.Lock()
	sess, _ := h.c.sessionLocked()
	rec := findRecord(sess.Registry(), aid)
	var keys int
	for i := range rec.Versions {
		if _, err := sess.UnwrapArchiveKey(aid, &rec.Versions[i]); err == nil {
			keys++
		}
	}
	h.c.mu.Unlock()
	if keys != 2 {
		t.Fatalf("%d of 2 versions unwrap under this vault", keys)
	}
}

// Every record the merge changes carries revision = max(local, incoming) + 1
// and this vault's device_id (SYNC.md §5).
func TestMergeBumpsRevisionAndLastWriter(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	aid, _ := parseID(id)
	h.editRecord(id, func(a *format.ArchiveRecord, _ int64) {
		a.Revision = 7
		a.LastWriter = kid(0xEE)
	})
	local := h.record(id)
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	f.add(format.ArchiveRecord{
		ArchiveID: aid, Name: "A", CreatedAt: local.CreatedAt, CurrentKID: kid(0x51),
		Revision: 41, LastWriter: kid(0xDD),
		Versions: []format.VersionRecord{f.version(aid, kid(0x51), local.CreatedAt)},
	})
	f.add(format.ArchiveRecord{
		ArchiveID: kid(0x61), Name: "New there", CreatedAt: 100, CurrentKID: kid(0x62), Revision: 3,
		Versions: []format.VersionRecord{f.version(kid(0x61), kid(0x62), 100)},
	})
	f.commit()
	handle := h.inspectForeign(f)
	rows, _ := h.c.IncomingRecords(handle)
	if r := rowFor(rows, hexID(kid(0x61))); r.Action != "add" || !r.Ticked {
		t.Fatalf("a new archive_id: %+v", r)
	}
	if e := h.c.MergeRecords(handle, []string{id, hexID(kid(0x61))}); e != nil {
		t.Fatal(e)
	}
	h.c.mu.Lock()
	sess, _ := h.c.sessionLocked()
	device := sess.Registry().DeviceID
	h.c.mu.Unlock()
	got := h.record(id)
	if got.Revision != 42 {
		t.Fatalf("revision: %d, want max(7, 41) + 1", got.Revision)
	}
	if got.LastWriter != device {
		t.Fatalf("last_writer: %x", got.LastWriter)
	}
	added := h.record(hexID(kid(0x61)))
	if added.Revision != 4 || added.LastWriter != device {
		t.Fatalf("the added record: revision %d writer %x", added.Revision, added.LastWriter)
	}
	if added.Name != "New there" || len(added.Versions) != 1 {
		t.Fatalf("the added record: %+v", added)
	}
}

// A record held here as forgotten is listed unticked, and ticking it is an
// explicit Restore.
func TestMergeOfAForgottenRecordIsAnExplicitRestore(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	aid, _ := parseID(id)
	local := h.record(id)
	if e := h.c.ForgetArchive(id); e != nil {
		t.Fatal(e)
	}
	forgottenAt := h.record(id).ForgottenAt
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	f.add(format.ArchiveRecord{
		ArchiveID: aid, Name: "A", CreatedAt: local.CreatedAt, CurrentKID: local.CurrentKID,
		Versions: []format.VersionRecord{f.version(aid, local.CurrentKID, local.CreatedAt)},
	})
	f.commit()
	handle := h.inspectForeign(f)
	rows, _ := h.c.IncomingRecords(handle)
	r := rowFor(rows, id)
	if r.Action != "forgotten" || r.Ticked || r.ForgottenAt != forgottenAt {
		t.Fatalf("row: %+v", r)
	}
	// Left unticked, the merge leaves it forgotten.
	if e := h.c.MergeRecords(handle, nil); e != nil {
		t.Fatal(e)
	}
	if !h.record(id).Forgotten() {
		t.Fatal("an unticked forgotten record was restored")
	}
	handle = h.inspectForeign(f)
	if e := h.c.MergeRecords(handle, []string{id}); e != nil {
		t.Fatal(e)
	}
	if h.record(id).Forgotten() {
		t.Fatal("ticking the row did not restore the record")
	}
}

// The merge is an ordinary registry write: refused while an archive is open,
// while a ceremony runs and while an operation runs.
func TestMergeIsRefusedWhileAnArchiveIsOpen(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	aid, _ := parseID(id)
	local := h.record(id)
	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	f.add(format.ArchiveRecord{
		ArchiveID: aid, Name: "A", CreatedAt: local.CreatedAt, CurrentKID: kid(0x71),
		Versions: []format.VersionRecord{f.version(aid, kid(0x71), local.CreatedAt)},
	})
	f.commit()
	handle := h.inspectForeign(f)
	if _, e := h.c.OpenArchive(id); e != nil {
		t.Fatal(e)
	}
	if e := h.c.MergeRecords(handle, []string{id}); !isCode(e, CodeArchivesOpen) {
		t.Fatalf("merge with an archive open: %v", e)
	}
	if e := h.c.CloseArchive(id); e != nil {
		t.Fatal(e)
	}
	if e := h.c.MergeRecords(handle, []string{id}); e != nil {
		t.Fatalf("merge once closed: %v", e)
	}
	// The handle is consumed.
	if _, e := h.c.IncomingRecords(handle); !isCode(e, CodeStalePrompt) {
		t.Fatalf("the handle after a merge: %v", e)
	}
}

// The handle lives until the dialog closes or a lock trigger drops it with
// every other held key (APP.md §13).
func TestDiscardRecordsAndLockTriggerDropTheHandle(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	h.newArchive("A")
	backup := filepath.Join(h.dir, "backup.eks")
	if e := h.exportBackup(backup); e != nil {
		t.Fatal(e)
	}
	handle := h.inspectRecords(backup)
	if _, e := h.c.IncomingRecords(handle); e != nil {
		t.Fatal(e)
	}
	if e := h.c.DiscardRecords(handle); e != nil {
		t.Fatal(e)
	}
	if _, e := h.c.IncomingRecords(handle); !isCode(e, CodeStalePrompt) {
		t.Fatalf("after discard: %v", e)
	}
	if e := h.c.DiscardRecords(handle); !isCode(e, CodeStalePrompt) {
		t.Fatalf("discarding twice: %v", e)
	}
	// A lock trigger drops what is still held.
	handle = h.inspectRecords(backup)
	h.c.LockNow(ReasonIdle)
	h.settleLock()
	if _, e := h.c.IncomingRecords(handle); !isCode(e, CodeStalePrompt) {
		t.Fatalf("after a lock trigger: %v", e)
	}
	h.rec.reset()
	h.unlockWithPassword()
	if e := h.c.MergeRecords(handle, nil); !isCode(e, CodeStalePrompt) {
		t.Fatalf("a merge on a dropped handle: %v", e)
	}
}

// A kid is unique across the whole registry (FORMAT.md §7.1). An incoming
// record that names one this vault already holds cannot be taken whole, and a
// crafted file must not make the merge write fail validation and take every
// other row down with it: the colliding version is dropped, a record whose
// current version is the colliding one is offered unticked and left out, and
// the rest of the write lands.
func TestMergeOfARecordWhoseKIDIsHeldHereTakesTheRest(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	idA, _ := h.newArchive("A")
	idB, _ := h.newArchive("B")
	localA, localB := h.record(idA), h.record(idB)

	f := newForeign(t, filepath.Join(h.dir, "other.eks"), true)
	clash := kid(0xC1)   // its current version claims this vault's kid
	partial := kid(0xC2) // only a retired version of its does
	clean := kid(0xC3)
	f.add(format.ArchiveRecord{
		ArchiveID: clash, Name: "Clash", CreatedAt: 100, CurrentKID: localA.CurrentKID,
		Versions: []format.VersionRecord{f.version(clash, localA.CurrentKID, 100)},
	})
	f.add(format.ArchiveRecord{
		ArchiveID: partial, Name: "Partial", CreatedAt: 100, CurrentKID: kid(0x51),
		Versions: []format.VersionRecord{
			f.retired(partial, localB.CurrentKID, 100),
			f.version(partial, kid(0x51), 100),
		},
	})
	f.add(format.ArchiveRecord{
		ArchiveID: clean, Name: "Clean", CreatedAt: 100, CurrentKID: kid(0x52),
		Versions: []format.VersionRecord{f.version(clean, kid(0x52), 100)},
	})
	f.commit()

	handle := h.inspectForeign(f)
	rows, e := h.c.IncomingRecords(handle)
	if e != nil {
		t.Fatal(e)
	}
	if r := rowFor(rows, hexID(clash)); r.Action != "skip" || r.Ticked {
		t.Fatalf("a record whose current kid is held here: %+v", r)
	}
	if r := rowFor(rows, hexID(partial)); r.Action != "add" || !r.Ticked {
		t.Fatalf("a record whose retired kid alone is held here: %+v", r)
	}
	if r := rowFor(rows, hexID(clean)); r.Action != "add" || !r.Ticked {
		t.Fatalf("an ordinary new record: %+v", r)
	}

	// Even ticked — the page cannot be trusted to have left it alone — the
	// untakeable row is left out and the write still goes through.
	if e := h.c.MergeRecords(handle, []string{hexID(clash), hexID(partial), hexID(clean)}); e != nil {
		t.Fatalf("merge: %v", e)
	}
	if h.hasRecord(hexID(clash)) {
		t.Fatal("the untakeable record was written")
	}
	got := h.record(hexID(partial))
	if len(got.Versions) != 1 || got.Versions[0].KID != kid(0x51) {
		t.Fatalf("the colliding version was taken: %+v", got.Versions)
	}
	if !h.hasRecord(hexID(clean)) {
		t.Fatal("the clean record was not taken")
	}
	// The registry is still valid and this vault's kids are still unique.
	h.c.mu.Lock()
	sess, _ := h.c.sessionLocked()
	err := sess.Registry().Validate()
	h.c.mu.Unlock()
	if err != nil {
		t.Fatalf("the merged registry: %v", err)
	}
}

// R25's freeze is over the mutating calls and the export (amendment A.5,
// APP.md §13): the merge's inspection writes to neither file, so it runs on a
// tampered vault exactly as the reveal does — the way out of a tampered vault
// may be the records another copy holds.
func TestInspectRecordsRunsOnATamperedVault(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.newArchive("A")
	backup := filepath.Join(h.dir, "backup.eks")
	if e := h.exportBackup(backup); e != nil {
		t.Fatal(e)
	}
	h.c.mu.Lock()
	h.c.vault.tampered, h.c.vault.tamperedReason = keystore.ErrTampered, CodeTamperedHash
	h.c.vault.warnings[CodeVaultTampered] = true
	h.c.mu.Unlock()
	if st := h.status(); !st.Tampered {
		t.Fatalf("status: %+v", st)
	}
	handle := h.inspectRecords(backup)
	rows, e := h.c.IncomingRecords(handle)
	if e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0].ArchiveID != id {
		t.Fatalf("rows: %+v", rows)
	}
}
