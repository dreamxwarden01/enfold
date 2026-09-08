package app

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dreamxwarden01/enfold/internal/kdf"
	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// Opening a file that is not a vault leaves the configured vault as it
// was: state, path and facts.
func TestOpenVaultFileFailureKeepsVault(t *testing.T) {
	h := newHarness(t, nil, nil)
	bad := filepath.Join(h.dir, "bad.eks")
	os.WriteFile(bad, []byte("not a keystore at all"), 0o600)
	if e := h.c.OpenVaultFile(bad, "Bad"); !isCode(e, CodeVaultInvalid) {
		t.Fatalf("garbage file: %v", e)
	}
	st := h.status()
	if st.State != StateLocked || st.Path != h.vault || st.DisplayName != "Test vault" || !st.HasPasswordSlot {
		t.Fatalf("vault replaced by a failed open: %+v", st)
	}
	if e := h.c.OpenVaultFile(filepath.Join(h.dir, "missing.eks"), "x"); !isCode(e, CodeVaultNotFound) {
		t.Fatalf("missing file: %v", e)
	}
	if st := h.status(); st.State != StateLocked || st.Path != h.vault {
		t.Fatalf("vault replaced by a missing file: %+v", st)
	}
	h.unlockWithPassword()
}

// A create that fails from a fresh core returns to "no vault", not to a
// Locked state over an empty path.
func TestCreateVaultFailureFromNone(t *testing.T) {
	dir := t.TempDir()
	rec := &recorder{}
	c, _ := New(Deps{Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	c.Start()
	defer c.Close()
	bad := filepath.Join(dir, "no-such-dir", "v.eks")
	if e := c.CreateVault(bad, "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "whatever")
	rec.waitCeremony(t, StepFailed, false)
	rec.waitState(t, StateNone)
	if st := c.Status(); st.State != StateNone || st.Path != "" {
		t.Fatalf("after a failed create: %+v", st)
	}
}

// An add in which every file is skipped stages nothing and leaves the
// archive clean; un-staging the last staged add does the same.
func TestNothingStagedIsClean(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	h.c.OpenArchive(id)
	f := filepath.Join(h.dir, "f.txt")
	os.WriteFile(f, []byte("once"), 0o600)
	opID, _ := h.c.AddFiles(id, "", []string{f}, PolicySkip)
	h.rec.waitOp(t, opID)
	opID, _ = h.c.Save(id)
	h.rec.waitOp(t, opID)
	// The same name again, skipped: nothing staged.
	opID, _ = h.c.AddFiles(id, "", []string{f}, PolicySkip)
	if o := h.rec.waitOp(t, opID); len(o.Results) != 1 || o.Results[0].Outcome != "skipped" {
		t.Fatalf("second add: %+v", o)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 || st.State != "open" {
		t.Fatalf("skipped add left the archive dirty: %+v", st)
	}
	// Stage one add and un-stage it.
	g := filepath.Join(h.dir, "g.txt")
	os.WriteFile(g, []byte("twice"), 0o600)
	opID, _ = h.c.AddFiles(id, "", []string{g}, PolicySkip)
	h.rec.waitOp(t, opID)
	page, _ := h.c.Page(id, "", "name", 0, 10)
	var staged string
	for _, r := range page.Rows {
		if r.Pending == "added" {
			staged = r.FileID
		}
	}
	if staged == "" {
		t.Fatalf("no staged row: %+v", page.Rows)
	}
	if e := h.c.DeleteFiles(id, []string{staged}); e != nil {
		t.Fatal(e)
	}
	if st, _ := h.c.Stat(id); st.Dirty != 0 || st.State != "open" || st.CapAt != 0 {
		t.Fatalf("un-staging the last add left the archive dirty: %+v", st)
	}
	// Verify is allowed again on a clean archive.
	opID, e := h.c.Verify(id)
	if e != nil {
		t.Fatalf("verify: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("verify op: %+v", o)
	}
}

// Creating a vault with the entangled password on asks for the password
// first, before anything is generated on the key; the switch is the
// vault's, so it is on the status and readable while Locked, not on any
// slot (FORMAT.md §6, §18.1).
func TestCreateVaultTokenEntangled(t *testing.T) {
	dir := t.TempDir()
	card := newFakeCard("123456")
	card.mgmt = []byte("0123456789abcdef0123456789abcdef")
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	rec := &recorder{}
	c, _ := New(Deps{Cards: cards, Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	c.Start()
	defer c.Close()
	c.SetAppOrigin("wails://wails")
	vault := filepath.Join(dir, "v.eks")
	if e := c.CreateVault(vault, "Mine", EnrollOptions{Kind: EnrollToken, Label: "Desk", Entangle: true}, false); e != nil {
		t.Fatal(e)
	}
	pw := rec.waitCeremony(t, StepPassword, true)
	if !pw.Choose {
		t.Fatalf("the password prompt is not marked choose: %+v", pw)
	}
	if n := cards.openCount(); n != 0 {
		t.Fatalf("a card was opened before the password was chosen: %d", n)
	}
	c.SubmitSecret("password", pw.PromptID, "entangled words")
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked) // a create ends in the vault
	if st := c.Status(); !st.Entangled {
		t.Fatalf("the vault's switch is not on the status: %+v", st)
	}
	// The key then unlocks with the password and its PIN.
	c.Lock()
	rec.waitState(t, StateLocked)
	// And the switch is a plaintext fact: the lock screen has it with no
	// handle open, which is what lets it ask for the password first.
	if st := c.Status(); !st.Entangled || st.State != StateLocked {
		t.Fatalf("the switch is not readable while locked: %+v", st)
	}
	rec.reset()
	if e := c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pw = rec.waitCeremony(t, StepPassword, true)
	if pw.Choose {
		t.Fatalf("an existing password marked choose: %+v", pw)
	}
	c.SubmitSecret("password", pw.PromptID, "entangled words")
	pin = rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "123456")
	rec.waitCeremony(t, StepDone, false)
	rec.waitState(t, StateUnlocked)
}

// exportBackup writes a backup (recovery slots only) of the harness vault
// through the keystore directly, as ExportBackup would.
func exportBackup(t *testing.T, vault, to string) {
	t.Helper()
	ks, err := keystore.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	unl, err := ks.Unlock(keystore.PasswordCredential{Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	defer unl.Close()
	if err := unl.Export(to); err != nil {
		t.Fatal(err)
	}
}

// InspectFile tells a backup from a vault, names this vault, and dates it.
func TestInspectFileKinds(t *testing.T) {
	h := newHarness(t, nil, nil)
	info, e := h.c.InspectFile(h.vault)
	if e != nil || info.Kind != FileKindVault || !info.VaultMatches || info.Newer || info.SlotCount != 2 {
		t.Fatalf("the vault itself: %+v %v", info, e)
	}
	backup := filepath.Join(h.dir, "backup.eks")
	exportBackup(t, h.vault, backup)
	info, e = h.c.InspectFile(backup)
	if e != nil || info.Kind != FileKindBackup || !info.VaultMatches || info.SlotCount != 1 {
		t.Fatalf("the backup: %+v %v", info, e)
	}
	if _, e := h.c.InspectFile(filepath.Join(h.dir, "nope.eks")); !isCode(e, CodeVaultNotFound) {
		t.Fatalf("missing: %v", e)
	}
	bad := filepath.Join(h.dir, "bad.eks")
	os.WriteFile(bad, []byte("not a keystore at all"), 0o600)
	if _, e := h.c.InspectFile(bad); !isCode(e, CodeVaultInvalid) {
		t.Fatalf("garbage: %v", e)
	}
	if st := h.status(); st.DefaultPath != filepath.Join(h.dir, "data", "vault.eks") || st.SetupNeeded {
		t.Fatalf("status: %+v", st)
	}
}

// A vault file put in the data folder by hand is adopted at start, with
// no override recorded; a backup put there needs setting up and refuses
// an unlock until then.
func TestStartAdoptsDefaultFile(t *testing.T) {
	h := newHarness(t, nil, nil)
	data := t.TempDir()
	b, _ := os.ReadFile(h.vault)
	os.WriteFile(filepath.Join(data, "vault.eks"), b, 0o600)
	rec := &recorder{}
	c, _ := New(Deps{Events: rec, Clock: newFakeClock(), DataDir: data})
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	st := c.Status()
	if st.State != StateLocked || st.Path != filepath.Join(data, "vault.eks") || st.DisplayName != defaultDisplayName || !st.HasPasswordSlot {
		t.Fatalf("adopted: %+v", st)
	}
	if s := c.GetSettings(); s.VaultPath != "" {
		t.Fatalf("an override was recorded for the default place: %q", s.VaultPath)
	}
	c.Close()

	data2 := t.TempDir()
	exportBackup(t, h.vault, filepath.Join(data2, "vault.eks"))
	rec2 := &recorder{}
	c2, _ := New(Deps{Events: rec2, Clock: newFakeClock(), DataDir: data2})
	if _, err := c2.Start(); err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	if st := c2.Status(); st.State != StateLocked || !st.SetupNeeded || st.HasPasswordSlot || st.HasHardwareSlot {
		t.Fatalf("a backup in place: %+v", st)
	}
	if e := c2.BeginUnlock(MethodPassword); !isCode(e, CodeSetupNeeded) {
		t.Fatalf("unlock of a backup: %v", e)
	}
	// The recovery key still opens it: the archives stay reachable, and
	// adding a key from there — a mutation on a vault with nothing else —
	// asks for the recovery key again, then the new secret.
	if e := c2.BeginUnlock(MethodRecovery); e != nil {
		t.Fatalf("recovery unlock of a backup: %v", e)
	}
	r := rec2.waitCeremony(t, StepRecovery, true)
	c2.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	rec2.waitState(t, StateUnlocked)
	rec2.reset()
	if e := c2.BeginEnroll(EnrollOptions{Kind: EnrollPassword, Label: "pw"}); e != nil {
		t.Fatal(e)
	}
	r = rec2.waitCeremony(t, StepRecovery, true)
	c2.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	p := rec2.waitCeremony(t, StepPassword, true)
	if !p.Choose {
		t.Fatalf("the new password not marked choose: %+v", p)
	}
	c2.SubmitSecret("password", p.PromptID, "a new password")
	rec2.waitCeremony(t, StepDone, false)
	if st := c2.Status(); st.SetupNeeded || !st.HasPasswordSlot {
		t.Fatalf("after adding a key from a recovery-only vault: %+v", st)
	}
}

// makeVault writes a vault with a password slot and a recovery slot.
func makeVault(t *testing.T, path, password string) {
	t.Helper()
	rk, _ := kdf.NewRecoveryKey()
	unl, err := keystore.Create(path, keystore.CreateOptions{Slots: []keystore.SlotSpec{
		keystore.RecoverySlot{Key: rk, Label: "Recovery key"},
		keystore.PasswordSlot{Password: password, Argon2: fast, Label: "Password"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()
}

// makeEntangledVault writes a vault with a hardware slot for pub and a
// recovery slot, its entangled password on (FORMAT.md §6): the switch is
// the file's, so a reader of it asks for that password whatever the vault
// kept here does. The recovery key's digits come back for the tests that
// need another way in.
func makeEntangledVault(t *testing.T, path string, pub []byte, password string) string {
	t.Helper()
	rk, err := kdf.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	unl, err := keystore.Create(path, keystore.CreateOptions{
		Slots: []keystore.SlotSpec{
			keystore.RecoverySlot{Key: rk, Label: "Recovery key"},
			keystore.HardwareSlot{PublicKey: pub, Label: "Their key"},
		},
		Entangle: &keystore.Entangle{Password: password, Argon2: fast},
	})
	if err != nil {
		t.Fatal(err)
	}
	ks := unl.Keystore()
	unl.Close()
	ks.Close()
	return rk.Digits()
}

// exportBackupWithRecovery writes a backup of a vault that has no password
// slot, opening it with its own recovery key.
func exportBackupWithRecovery(t *testing.T, vault, to, recovery string) {
	t.Helper()
	rk, err := kdf.ParseRecoveryDigits(recovery)
	if err != nil {
		t.Fatal(err)
	}
	ks, err := keystore.Open(vault)
	if err != nil {
		t.Fatal(err)
	}
	defer ks.Close()
	unl, err := ks.Unlock(keystore.RecoveryCredential{Key: rk})
	if err != nil {
		t.Fatal(err)
	}
	defer unl.Close()
	if err := unl.Export(to); err != nil {
		t.Fatal(err)
	}
}

func freshCore(t *testing.T, data string) (*Core, *recorder) {
	t.Helper()
	return freshCoreWithCards(t, data, nil)
}

func freshCoreWithCards(t *testing.T, data string, cards *fakeCards) (*Core, *recorder) {
	t.Helper()
	rec := &recorder{}
	var cs Cards
	if cards != nil {
		cs = cards
	}
	c, err := New(Deps{Cards: cs, Events: rec, Clock: newFakeClock(), DataDir: data, Log: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c, rec
}

func digits(s string) string { return strings.ReplaceAll(s, " ", "") }

// staged reports whether a staged (incoming or inspect) file sits in dir.
func staged(dir string) bool {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if reservedName(e.Name()) {
			return true
		}
	}
	return false
}

// Importing a vault file installs a copy at the one place only after the
// file unlocks with its own way in; the ceremony ends locked, the source
// is untouched, and a failed proof installs nothing.
func TestImportVaultProvesThenInstalls(t *testing.T) {
	h := newHarness(t, nil, nil)
	data := t.TempDir()
	c, rec := freshCore(t, data)
	if e := c.ImportFile(h.vault, "Mine", MethodPassword, EnrollOptions{}, false); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, testPassword)
	rec.waitCeremony(t, StepDone, false)
	rec.waitState(t, StateLocked)
	st := c.Status()
	if st.Path != filepath.Join(data, "vault.eks") || st.DisplayName != "Mine" || !st.HasPasswordSlot || st.SetupNeeded || st.RetiredCopies != 0 {
		t.Fatalf("after the import: %+v", st)
	}
	if staged(data) {
		t.Fatal("the staged file was left behind")
	}
	if _, err := os.Stat(h.vault); err != nil {
		t.Fatal("the source was touched")
	}
	if s := c.GetSettings(); s.VaultPath != "" {
		t.Fatalf("override recorded: %q", s.VaultPath)
	}
	// The copy unlocks on its own.
	c.BeginUnlock(MethodPassword)
	p = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, testPassword)
	rec.waitState(t, StateUnlocked)

	// A wrong password proves nothing: nothing is installed.
	data2 := t.TempDir()
	c2, rec2 := freshCore(t, data2)
	if e := c2.ImportFile(h.vault, "Mine", MethodPassword, EnrollOptions{}, false); e != nil {
		t.Fatal(e)
	}
	p = rec2.waitCeremony(t, StepPassword, true)
	c2.SubmitSecret("password", p.PromptID, "wrong")
	again := rec2.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != p.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		t.Fatalf("asked again without the reason: %+v", again)
	}
	c2.CancelUnlock() // the user gives up instead
	rec2.waitState(t, StateNone)
	if _, err := os.Stat(filepath.Join(data2, "vault.eks")); err == nil {
		t.Fatal("an unproven file was installed")
	}
	if staged(data2) {
		t.Fatal("the staged file was left behind")
	}

	// The proof takes the INCOMING file's switch, not the kept vault's: a
	// machine whose own vault is not entangled still has to type the
	// incoming vault's password (FORMAT.md §6, APP.md §13).
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	src := filepath.Join(t.TempDir(), "entangled.eks")
	makeEntangledVault(t, src, pub, "the incoming vault password")
	data3 := t.TempDir()
	c3, rec3 := freshCoreWithCards(t, data3, cards)
	if st := c3.Status(); st.Entangled {
		t.Fatalf("a machine with no vault claims a switch: %+v", st)
	}
	if e := c3.ImportFile(src, "Theirs", MethodToken, EnrollOptions{}, false); e != nil {
		t.Fatal(e)
	}
	pw := rec3.waitCeremony(t, StepPassword, true)
	if pw.Choose {
		t.Fatalf("the incoming vault's own password marked choose: %+v", pw)
	}
	c3.SubmitSecret("password", pw.PromptID, "the incoming vault password")
	pin := rec3.waitCeremony(t, StepPIN, true)
	c3.SubmitSecret("pin", pin.PromptID, "123456")
	rec3.waitCeremony(t, StepDone, false)
	rec3.waitState(t, StateLocked)
	if st := c3.Status(); !st.Entangled {
		t.Fatalf("the imported vault's switch was not carried: %+v", st)
	}
}

// Importing a backup asks for its recovery key, then the first way in,
// and installs the result as a vault; the recovery key stays valid.
func TestImportBackupAdopts(t *testing.T) {
	h := newHarness(t, nil, nil)
	backup := filepath.Join(h.dir, "backup.eks")
	exportBackup(t, h.vault, backup)
	data := t.TempDir()
	c, rec := freshCore(t, data)
	// A backup needs a first way in.
	if e := c.ImportFile(backup, "Restored", MethodPassword, EnrollOptions{}, false); !isCode(e, CodeParams) {
		t.Fatalf("no first way in: %v", e)
	}
	if e := c.ImportFile(backup, "Restored", MethodPassword, EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	r := rec.waitCeremony(t, StepRecovery, true)
	if r.Choose {
		t.Fatalf("the recovery prompt marked choose: %+v", r)
	}
	c.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	p := rec.waitCeremony(t, StepPassword, true)
	if !p.Choose {
		t.Fatalf("the new password not marked choose: %+v", p)
	}
	c.SubmitSecret("password", p.PromptID, "a new password")
	rec.waitCeremony(t, StepDone, false)
	rec.waitState(t, StateLocked)
	st := c.Status()
	if st.Path != filepath.Join(data, "vault.eks") || st.SetupNeeded || !st.HasPasswordSlot || st.DisplayName != "Restored" {
		t.Fatalf("after adopting: %+v", st)
	}
	if n := len(c.Slots()); n != 2 {
		t.Fatalf("slots: %+v", c.Slots())
	}
	// A backup's header carries entangle 0 (R28), and the first way in
	// chose no password, so the adopted vault's switch is off whatever the
	// source had.
	if st.Entangled {
		t.Fatalf("an adopted backup inherited a switch: %+v", st)
	}
	c.BeginUnlock(MethodPassword)
	p = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a new password")
	rec.waitState(t, StateUnlocked)
	c.Lock()
	rec.waitState(t, StateLocked)
	rec.reset()
	c.BeginUnlock(MethodRecovery)
	r = rec.waitCeremony(t, StepRecovery, true)
	c.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	rec.waitState(t, StateUnlocked)
}

// An adopted backup's first way in is the moment its entanglement is
// chosen, and the slot and the header land in one commit (FORMAT.md §15):
// the source vault's own password does not open the adopted one, and the
// new one does.
func TestImportBackupChoosesTheEntanglementAfresh(t *testing.T) {
	card := newFakeCard("123456")
	pub := card.addKey(0x9d, true)
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	h := newHarnessEntangled(t, cards, pub, "the source vault password")
	backup := filepath.Join(h.dir, "backup.eks")
	exportBackupWithRecovery(t, h.vault, backup, h.recovery)

	second := newFakeCard("654321")
	second.mgmt = []byte("0123456789abcdef0123456789abcdef")
	cards2 := &fakeCards{card: second}
	cards2.setReaders("Yubico B")
	data := t.TempDir()
	c, rec := freshCoreWithCards(t, data, cards2)
	if e := c.ImportFile(backup, "Restored", MethodRecovery, EnrollOptions{Kind: EnrollToken, Label: "New key", Entangle: true}, false); e != nil {
		t.Fatal(e)
	}
	r := rec.waitCeremony(t, StepRecovery, true)
	c.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	p := rec.waitCeremony(t, StepPassword, true)
	if !p.Choose {
		t.Fatalf("the adopted vault's new password is not marked choose: %+v", p)
	}
	c.SubmitSecret("password", p.PromptID, "the adopted vault password")
	pin := rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "654321")
	rec.waitCeremony(t, StepDone, false)
	rec.waitState(t, StateLocked)
	if st := c.Status(); !st.Entangled || !st.HasHardwareSlot {
		t.Fatalf("after adopting: %+v", st)
	}
	// The source vault's password does not open it: it is said and asked
	// again in place, and the password chosen at adoption opens it.
	rec.reset()
	c.BeginUnlock(MethodToken)
	pw := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", pw.PromptID, "the source vault password")
	pin = rec.waitCeremony(t, StepPIN, true)
	c.SubmitSecret("pin", pin.PromptID, "654321")
	again := rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPassword && s.PromptID != "" && s.PromptID != pw.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		t.Fatalf("the source vault's password opened the adopted one: %+v", again)
	}
	c.SubmitSecret("password", again.PromptID, "the adopted vault password")
	rec.waitState(t, StateUnlocked)
}

// A vault kept here is replaced only with replace, whatever the file
// claims; open archives refuse the import; the vault replaced stays as a
// dated copy, and a vault kept elsewhere is left where it is.
func TestImportReplaceRules(t *testing.T) {
	h := newHarness(t, nil, nil) // the vault is kept elsewhere (h.vault)
	other := filepath.Join(h.dir, "other.eks")
	makeVault(t, other, "other password")
	if e := h.c.ImportFile(other, "Other", MethodPassword, EnrollOptions{}, false); !isCode(e, CodeVaultExists) {
		t.Fatalf("without replace: %v", e)
	}
	info, e := h.c.InspectFile(other)
	if e != nil || info.Kind != FileKindVault || info.VaultMatches || info.Password != 1 || info.Recovery != 1 {
		t.Fatalf("inspect: %+v %v", info, e)
	}
	// An open archive refuses a replacement.
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	h.c.OpenArchive(id)
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	if e := h.c.ImportFile(other, "Other", MethodPassword, EnrollOptions{}, true); !isCode(e, CodeArchivesOpen) {
		t.Fatalf("with an archive open: %v", e)
	}
	h.c.CloseArchive(id)
	h.rec.reset()
	if e := h.c.ImportFile(other, "Other", MethodPassword, EnrollOptions{}, true); e != nil {
		t.Fatal(e)
	}
	p := h.rec.waitCeremony(t, StepPassword, true)
	h.c.SubmitSecret("password", p.PromptID, "other password")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateLocked)
	def := filepath.Join(h.dir, "data", "vault.eks")
	st := h.status()
	if st.Path != def || st.DisplayName != "Other" || st.RetiredCopies != 0 {
		t.Fatalf("after replacing a vault kept elsewhere: %+v", st)
	}
	if _, err := os.Stat(h.vault); err != nil {
		t.Fatal("the vault kept elsewhere was touched")
	}
	if s := h.c.GetSettings(); s.VaultPath != "" {
		t.Fatalf("override kept: %q", s.VaultPath)
	}
	// The same file again: the vault now kept here is retired, never
	// overwritten; two retirements in one second get two names.
	for i := 0; i < 2; i++ {
		h.rec.reset()
		if e := h.c.ImportFile(other, "Other", MethodPassword, EnrollOptions{}, true); e != nil {
			t.Fatal(e)
		}
		p = h.rec.waitCeremony(t, StepPassword, true)
		h.c.SubmitSecret("password", p.PromptID, "other password")
		h.rec.waitCeremony(t, StepDone, false)
		h.rec.waitState(t, StateLocked)
	}
	st = h.status()
	if st.RetiredCopies != 2 || !strings.HasPrefix(filepath.Base(st.RetiredPath), "vault-replaced-") {
		t.Fatalf("retired copies: %+v", st)
	}
	if _, err := os.Stat(def); err != nil {
		t.Fatal("the vault's place is empty")
	}
}

// A backup placed by hand is set up in place: the recovery key, then the
// first way in, ending unlocked.
func TestFinishSetup(t *testing.T) {
	h := newHarness(t, nil, nil)
	data := t.TempDir()
	exportBackup(t, h.vault, filepath.Join(data, "vault.eks"))
	c, rec := freshCore(t, data)
	if st := c.Status(); !st.SetupNeeded {
		t.Fatalf("not setup-needed: %+v", st)
	}
	if e := c.FinishSetup(EnrollOptions{Kind: EnrollPassword, Label: "pw"}); e != nil {
		t.Fatal(e)
	}
	r := rec.waitCeremony(t, StepRecovery, true)
	c.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a new password")
	rec.waitCeremony(t, StepDone, false)
	rec.waitState(t, StateUnlocked)
	if st := c.Status(); st.SetupNeeded || !st.HasPasswordSlot {
		t.Fatalf("after setup: %+v", st)
	}
	if e := c.FinishSetup(EnrollOptions{Kind: EnrollPassword, Label: "pw"}); !isCode(e, CodeVaultUnlocked) {
		t.Fatalf("setup again while unlocked: %v", e)
	}
	// A standalone password slot is never entangled, and the setup chose
	// no vault password: the switch stays off.
	if st := c.Status(); st.Entangled {
		t.Fatalf("a switch appeared from a password setup: %+v", st)
	}
}

// VerifyBackup opens a copy with the recovery key and counts the archives
// it names; the file and the vault are untouched, and a wrong key fails.
func TestVerifyBackup(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	if _, e := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false); e != nil {
		t.Fatal(e)
	}
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	backup := filepath.Join(h.dir, "backup.eks")
	exportBackup(t, h.vault, backup)
	h.rec.reset()
	if e := h.c.VerifyBackup(backup); e != nil {
		t.Fatal(e)
	}
	r := h.rec.waitCeremony(t, StepRecovery, true)
	h.c.SubmitSecret("recovery", r.PromptID, digits(h.recovery))
	done := h.rec.waitCeremony(t, StepDone, false)
	if done.Archives != 1 || done.Kind != "verify" {
		t.Fatalf("verified: %+v", done)
	}
	h.rec.waitState(t, StateLocked)
	if _, err := os.Stat(filepath.Join(h.dir, "data", "vault.inspect.eks")); err == nil {
		t.Fatal("the inspected copy was left behind")
	}
	h.rec.reset()
	if e := h.c.VerifyBackup(backup); e != nil {
		t.Fatal(e)
	}
	r = h.rec.waitCeremony(t, StepRecovery, true)
	h.c.SubmitSecret("recovery", r.PromptID, "000000000000000000000000000000000000000000000000")
	again := h.rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepRecovery && s.PromptID != "" && s.PromptID != r.PromptID
	}).(CeremonyState)
	if again.Error != CodeAuth {
		t.Fatalf("wrong key not asked again in place: %+v", again)
	}
	h.c.SubmitSecret("recovery", again.PromptID, digits(h.recovery))
	if done := h.rec.waitCeremony(t, StepDone, false); done.Archives != 1 {
		t.Fatalf("after correcting the key: %+v", done)
	}
}

// A key already holding a usable key in 9d is not enrolled by merely being
// in the reader: it proves itself — PIN and touch, the agreement checked —
// first; a wrong PIN says so; a label left empty is the serial number.
func TestEnrolledKeyProvesItself(t *testing.T) {
	dir := t.TempDir()
	card := newFakeCard("123456")
	card.addKey(0x9d, true) // a key from before, as a YubiKey used elsewhere holds
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	rec := &recorder{}
	c, _ := New(Deps{Cards: cards, Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	c.Start()
	defer c.Close()
	c.SetAppOrigin("wails://wails")
	if e := c.CreateVault("", "Mine", EnrollOptions{Kind: EnrollToken}, false); e != nil {
		t.Fatal(e)
	}
	pin := rec.waitCeremony(t, StepPIN, true)
	if pin.Error != "" {
		t.Fatalf("first PIN prompt with a note: %+v", pin)
	}
	c.SubmitSecret("pin", pin.PromptID, "wrong!")
	pin2 := rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPIN && s.PromptID != "" && s.PromptID != pin.PromptID
	}).(CeremonyState)
	if pin2.Error != CodeTokenPIN || pin2.Retries != 2 {
		t.Fatalf("a wrong PIN is not said: %+v", pin2)
	}
	c.SubmitSecret("pin", pin2.PromptID, "123456")
	if touch := rec.waitCeremony(t, StepTouch, false); touch.Error != "" {
		t.Fatalf("the note outlived the accepted PIN: %+v", touch)
	}
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked)
	card.mu.Lock()
	ops := card.ops
	card.mu.Unlock()
	if ops != 1 {
		t.Fatalf("the key did not prove itself: %d agreements", ops)
	}
	var label string
	for _, s := range c.Slots() {
		if s.Type == "hardware" {
			label = s.Label
		}
	}
	if label != "YubiKey 1234567" {
		t.Fatalf("the empty label is not the serial: %q", label)
	}

	// A key whose agreement is not its own is refused, and nothing is made.
	dir2 := t.TempDir()
	liar := newFakeCard("123456")
	liar.addKey(0x9d, true)
	liar.proofLies = true
	cards2 := &fakeCards{card: liar}
	cards2.setReaders("Yubico A")
	rec2 := &recorder{}
	c2, _ := New(Deps{Cards: cards2, Events: rec2, Clock: newFakeClock(), DataDir: filepath.Join(dir2, "data")})
	c2.Start()
	defer c2.Close()
	if e := c2.CreateVault("", "Mine", EnrollOptions{Kind: EnrollToken}, false); e != nil {
		t.Fatal(e)
	}
	pin = rec2.waitCeremony(t, StepPIN, true)
	c2.SubmitSecret("pin", pin.PromptID, "123456")
	if f := rec2.waitCeremony(t, StepFailed, false); f.Error != CodeTokenProof {
		t.Fatalf("a lying key: %+v", f)
	}
	c2.CancelUnlock()
	rec2.waitState(t, StateNone)
	if _, err := os.Stat(filepath.Join(dir2, "data", "vault.eks")); err == nil {
		t.Fatal("a vault was made with a key that did not prove itself")
	}
}

// On the generate path — an empty key — a refused PIN at the management
// key is said and asked again; the prompt names the key being enrolled.
func TestGeneratePathWrongPINIsAskedAgain(t *testing.T) {
	dir := t.TempDir()
	card := newFakeCard("123456")
	card.mgmt = []byte("0123456789abcdef0123456789abcdef")
	cards := &fakeCards{card: card}
	cards.setReaders("Yubico A")
	rec := &recorder{}
	c, _ := New(Deps{Cards: cards, Events: rec, Clock: newFakeClock(), DataDir: filepath.Join(dir, "data")})
	c.Start()
	defer c.Close()
	c.SetAppOrigin("wails://wails")
	if e := c.CreateVault("", "Mine", EnrollOptions{Kind: EnrollToken}, false); e != nil {
		t.Fatal(e)
	}
	pin := rec.waitCeremony(t, StepPIN, true)
	if pin.SlotLabel != "YubiKey 1234567" {
		t.Fatalf("the PIN prompt does not name the key being enrolled: %+v", pin)
	}
	c.SubmitSecret("pin", pin.PromptID, "nope")
	pin2 := rec.waitFor(t, EventVaultCeremony, func(x any) bool {
		s, ok := x.(CeremonyState)
		return ok && s.Step == StepPIN && s.PromptID != "" && s.PromptID != pin.PromptID
	}).(CeremonyState)
	if pin2.Error != CodeTokenPIN || pin2.Retries != 2 {
		t.Fatalf("the refused PIN is not said: %+v", pin2)
	}
	c.SubmitSecret("pin", pin2.PromptID, "123456")
	rec.waitCeremony(t, StepTouch, false) // the proof, with the PIN still verified
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked)
	if st := c.Status(); !st.HasHardwareSlot {
		t.Fatalf("after the enrolment: %+v", st)
	}
}

// CreateVault over the vault kept here needs replace, builds the new
// vault as the incoming file, retires the old one and ends locked.
// With a vault kept, a second is never made from inside (APP.md §2.1): a
// create over it, elsewhere, with or without replace, is refused by the
// core; what replaces a vault is an import.
func TestCreateWithVaultKeptIsRefused(t *testing.T) {
	data := t.TempDir()
	c, rec := freshCore(t, data)
	c.SetAppOrigin("wails://wails")
	if e := c.CreateVault("", "First", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "first password")
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked) // a create ends in the vault, the key shown over it
	if st := c.Status(); st.Path != filepath.Join(data, "vault.eks") || st.KeptElsewhere {
		t.Fatalf("created at %s: %+v", st.Path, st)
	}
	c.Lock()
	rec.waitState(t, StateLocked)
	for _, replace := range []bool{false, true} {
		if e := c.CreateVault("", "Second", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, replace); !isCode(e, CodeVaultKept) {
			t.Fatalf("over the vault kept (replace=%v): %v", replace, e)
		}
		if e := c.CreateVault(filepath.Join(data, "..", "elsewhere.eks"), "Second", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, replace); !isCode(e, CodeVaultKept) {
			t.Fatalf("elsewhere with a vault kept (replace=%v): %v", replace, e)
		}
	}
	if st := c.Status(); st.DisplayName != "First" || st.RetiredCopies != 0 || st.State != StateLocked {
		t.Fatalf("after the refusals: %+v", st)
	}
	rec.reset()
	c.BeginUnlock(MethodPassword)
	p = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "first password")
	rec.waitState(t, StateUnlocked)
	if e := c.CreateVault("", "Second", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); !isCode(e, CodeVaultUnlocked) {
		t.Fatalf("while unlocked: %v", e)
	}
}

// A create cut short at any prompt leaves nothing: no vault, no incoming
// file. The create's own recovery key opens the vault it made.
func TestCreateCutShortLeavesNothing(t *testing.T) {
	data := t.TempDir()
	c, rec := freshCore(t, data)
	c.SetAppOrigin("wails://wails")
	if e := c.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	rec.waitCeremony(t, StepPassword, true)
	c.CancelUnlock()
	rec.waitCeremony(t, StepFailed, false)
	rec.waitState(t, StateNone)
	if _, err := os.Stat(filepath.Join(data, "vault.eks")); err == nil || staged(data) {
		t.Fatal("a cancelled create left a file behind")
	}
	rec.reset()
	if e := c.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a password")
	final := rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked)
	req, _ := http.NewRequest("GET", final.SlotLabel, nil)
	req.Header.Set("Origin", "wails://wails")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("digits: %d", resp.StatusCode)
	}
	// And the digits shown open the vault again.
	c.Lock()
	rec.waitState(t, StateLocked)
	rec.reset()
	c.BeginUnlock(MethodRecovery)
	r := rec.waitCeremony(t, StepRecovery, true)
	c.SubmitSecret("recovery", r.PromptID, string(body))
	rec.waitState(t, StateUnlocked)
}

// A file at the vault's place that cannot be opened is named at start,
// and creating a vault there retires it instead of failing; "use it
// where it is" respects the open-archives rule and keeps the name.
func TestCreateOverUnreadableFile(t *testing.T) {
	data := t.TempDir()
	os.WriteFile(filepath.Join(data, "vault.eks"), []byte("not a keystore at all"), 0o600)
	c, rec := freshCore(t, data)
	if st := c.Status(); st.State != StateNone || st.MissingPath != filepath.Join(data, "vault.eks") || !st.Damaged {
		t.Fatalf("an unreadable file at the place: %+v", st)
	}
	c.SetAppOrigin("wails://wails")
	// The file there, unreadable or not, is replaced only knowingly, and
	// the rebuild happens at its place and nowhere else.
	if e := c.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); !isCode(e, CodeVaultExists) {
		t.Fatalf("over an unreadable file without replace: %v", e)
	}
	if e := c.CreateVault(filepath.Join(data, "..", "aside.eks"), "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); !isCode(e, CodeVaultKept) {
		t.Fatalf("a rebuild elsewhere: %v", e)
	}
	if e := c.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a password")
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked)
	if st := c.Status(); st.MissingPath != "" || st.Damaged || !st.HasPasswordSlot {
		t.Fatalf("after creating over it: %+v", st)
	}

	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id, _ := h.c.CreateArchive(filepath.Join(h.dir, "a.enf"), "A", false)
	h.c.OpenArchive(id)
	h.c.Lock()
	h.rec.waitState(t, StateLocked)
	other := filepath.Join(h.dir, "other.eks")
	makeVault(t, other, "other password")
	if e := h.c.OpenVaultFile(other, "Other"); !isCode(e, CodeArchivesOpen) {
		t.Fatalf("switching vaults with an archive open: %v", e)
	}
	if e := h.c.OpenVaultFile(h.vault, ""); e != nil {
		t.Fatal(e)
	}
	if st := h.status(); st.DisplayName != "Test vault" || !st.KeptElsewhere {
		t.Fatalf("reopening the vault kept elsewhere with no name: %+v", st)
	}
	// A staging name can never be the vault.
	if e := h.c.OpenVaultFile(filepath.Join(h.dir, "data", "vault.incoming-abc.eks"), "x"); !isCode(e, CodeParams) {
		t.Fatalf("a reserved name as the vault: %v", e)
	}
	// The vault's own file that would not open is kept as a damaged copy
	// for salvage — named on the status, not counted as a vault.
	entries, _ := os.ReadDir(data)
	var damaged, others int
	for _, e := range entries {
		switch {
		case strings.HasPrefix(e.Name(), "vault-damaged-") && strings.HasSuffix(e.Name(), ".eks"):
			damaged++
		case strings.HasPrefix(e.Name(), "file-replaced-"):
			others++
		}
	}
	if st := c.Status(); damaged != 1 || others != 0 || st.RetiredCopies != 0 || !strings.HasPrefix(filepath.Base(st.DamagedCopyPath), "vault-damaged-") {
		t.Fatalf("the damaged vault was not kept as one: damaged=%d others=%d %+v", damaged, others, st)
	}
	// A file that is not at the vault's place is something else: a first
	// create elsewhere, over junk, keeps it under a name that says so.
	data2 := t.TempDir()
	c2, rec2 := freshCore(t, data2)
	c2.SetAppOrigin("wails://wails")
	elsewhere := filepath.Join(data2, "..", "elsewhere.eks")
	os.WriteFile(elsewhere, []byte("junk"), 0o600)
	if e := c2.CreateVault(elsewhere, "Else", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); e != nil {
		t.Fatal(e)
	}
	p = rec2.waitCeremony(t, StepPassword, true)
	c2.SubmitSecret("password", p.PromptID, "a password")
	rec2.waitCeremony(t, StepRecovery, false)
	rec2.waitState(t, StateUnlocked)
	entries, _ = os.ReadDir(data2)
	others = 0
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "file-replaced-") {
			others++
		}
	}
	if st := c2.Status(); others != 1 || st.DamagedCopyPath != "" {
		t.Fatalf("junk elsewhere retired as: others=%d %+v", others, st)
	}
}

// A configured vault whose file is absent is missing, not damaged: no
// rebuild is offered, since there is nothing to keep.
func TestAbsentVaultIsNotDamaged(t *testing.T) {
	data := t.TempDir()
	gone := filepath.Join(data, "..", "gone", "vault.eks")
	if err := saveSettings(data, settingsFile{VaultPath: gone, DisplayName: "Gone"}); err != nil {
		t.Fatal(err)
	}
	c, _ := freshCore(t, data)
	st := c.Status()
	if st.State != StateNone || st.MissingPath == "" || st.Damaged {
		t.Fatalf("absent file: %+v", st)
	}
	// A file that cannot be read for another reason — here a folder at the
	// vault's place — is not damage either: nothing is offered a rebuild
	// that a copy could not keep.
	data2 := t.TempDir()
	os.Mkdir(filepath.Join(data2, "vault.eks"), 0o700)
	c2, rec2 := freshCore(t, data2)
	c2.SetAppOrigin("wails://wails")
	if st := c2.Status(); st.State != StateNone || st.MissingPath == "" || st.Damaged {
		t.Fatalf("a folder at the place: %+v", st)
	}
	// A create over it is not refused outright — the file is not a vault —
	// but the install cannot keep a folder as a copy, and fails.
	if e := c2.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); e != nil {
		t.Fatal(e)
	}
	p := rec2.waitCeremony(t, StepPassword, true)
	c2.SubmitSecret("password", p.PromptID, "a password")
	if f := rec2.waitCeremony(t, StepFailed, false); f.Error != CodeVaultInvalid {
		t.Fatalf("over a folder: %+v", f)
	}
	if st := c2.Status(); st.State != StateNone || st.MissingPath == "" {
		t.Fatalf("after the failed create: %+v", st)
	}
}

// A chosen password under the minimum is refused at submit and the prompt
// stands; an existing password is never measured.
func TestChosenPasswordMinimum(t *testing.T) {
	data := t.TempDir()
	c, rec := freshCore(t, data)
	c.SetAppOrigin("wails://wails")
	if e := c.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	if e := c.SubmitSecret("password", p.PromptID, "short"); !isCode(e, CodePasswordShort) {
		t.Fatalf("a short password: %v", e)
	}
	if e := c.SubmitSecret("password", p.PromptID, "long enough now"); e != nil {
		t.Fatalf("the prompt did not stand: %v", e)
	}
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateUnlocked)
	// The existing password, however short the rule would call it, is
	// what it is: an unlock measures nothing.
	h := newHarness(t, nil, nil)
	h.rec.reset()
	h.c.BeginUnlock(MethodPassword)
	p = h.rec.waitCeremony(t, StepPassword, true)
	if e := h.c.SubmitSecret("password", p.PromptID, "x"); e != nil {
		t.Fatalf("an existing password measured: %v", e)
	}
}

// The Smart Card service stops when the last reader leaves and starts
// when one arrives: while waiting for a key, "no service" is no reader,
// not a failure.
func TestNoServiceWhileWaitingIsNoReader(t *testing.T) {
	first := newFakeCard("123456")
	pub := first.addKey(0x9d, true)
	cards := &fakeCards{card: first}
	cards.setReadersErr(ErrTokenNoService)
	h := newHarness(t, cards, pub)
	if r, e := h.c.Readers(); e != nil || len(r) != 0 {
		t.Fatalf("readers with the service down: %v %v", r, e)
	}
	old := noServiceNoteAfter
	noServiceNoteAfter = 2
	defer func() { noServiceNoteAfter = old }()
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	h.rec.waitCeremony(t, StepWaitingForKey, false)
	time.Sleep(3 * readerPoll)
	if st := h.status(); st.Ceremony == nil || st.Ceremony.Step != StepWaitingForKey || st.Ceremony.Error != CodeTokenNoService {
		t.Fatalf("waiting did not survive the stopped service, or did not say so: %+v", st.Ceremony)
	}
	cards.setReadersErr(nil)
	cards.setReaders("Yubico A")
	pin := h.rec.waitCeremony(t, StepPIN, true)
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitState(t, StateUnlocked)
}

// A key pulled while its PIN prompt stands is noticed by the probe that
// keeps the connection alive; the prompt ends, the strip goes back to
// waiting for the key with a note, and the ceremony goes on when the key
// is back. A card busy for a moment is retried before the ceremony parks.
func TestKeyPulledDuringPINGoesBackToWaiting(t *testing.T) {
	old := keepAliveEvery
	keepAliveEvery = 30 * time.Millisecond
	defer func() { keepAliveEvery = old }()
	first := newFakeCard("123456")
	pub := first.addKey(0x9d, true)
	cards := &fakeCards{card: first, busyOpens: 2}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	pin := h.rec.waitCeremony(t, StepPIN, true)
	// Pulled while the PIN is being typed.
	first.setRemoved(true)
	cards.setReaders()
	w := h.rec.waitCeremony(t, StepWaitingForKey, false)
	if w.Error != CodeTokenNoCard {
		t.Fatalf("waiting again without the note: %+v", w)
	}
	if e := h.c.SubmitSecret("pin", pin.PromptID, "123456"); !isCode(e, CodeStalePrompt) {
		t.Fatalf("the pulled key's prompt still stands: %v", e)
	}
	// Back in: the PIN is asked again, and the note is gone.
	first.setRemoved(false)
	first.mu.Lock()
	first.closed = false
	first.mu.Unlock()
	cards.setReaders("Yubico A")
	pin2 := h.rec.waitCeremony(t, StepPIN, true)
	if pin2.PromptID == pin.PromptID || pin2.Error != "" {
		t.Fatalf("second prompt: %+v", pin2)
	}
	h.c.SubmitSecret("pin", pin2.PromptID, "123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
	if n := cards.openCount(); n != 2 {
		t.Fatalf("opens: %d", n)
	}
}

// A reader listed without its key — the moment of a removal, or a card
// that answers nothing — is not opened in a spin: the attempts pause, and
// the flow goes on when the key answers.
func TestKeyGoneWithReaderListedBacksOff(t *testing.T) {
	first := newFakeCard("123456")
	pub := first.addKey(0x9d, true)
	cards := &fakeCards{card: first, openErr: ErrTokenNoCard}
	cards.setReaders("Yubico A")
	h := newHarness(t, cards, pub)
	if e := h.c.BeginUnlock(MethodToken); e != nil {
		t.Fatal(e)
	}
	h.rec.waitFor(t, EventVaultCeremony, func(p any) bool {
		s, ok := p.(CeremonyState)
		return ok && s.Step == StepWaitingForKey && s.Error == CodeTokenNoCard
	})
	time.Sleep(1200 * time.Millisecond)
	if n := cards.openAttempts(); n > 4 {
		t.Fatalf("%d opens in 1.2 s: a spin", n)
	}
	cards.setOpenErr(nil)
	pin := h.rec.waitCeremony(t, StepPIN, true)
	if pin.Error != "" {
		t.Fatalf("the note stands after the key answered: %+v", pin)
	}
	h.c.SubmitSecret("pin", pin.PromptID, "123456")
	h.rec.waitCeremony(t, StepDone, false)
	h.rec.waitState(t, StateUnlocked)
}

// A reset the flow could not heal reads as the key gone, never as an
// internal error.
func TestResetClassifiesAsKeyGone(t *testing.T) {
	if e := classify(fmt.Errorf("probe: %w", ErrTokenReset)); e.Code != CodeTokenNoCard {
		t.Fatalf("%+v", e)
	}
}
