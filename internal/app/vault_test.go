package app

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// Creating a vault with a token and an entangled password asks for the
// password first, before anything is generated on the key.
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
	rec.waitState(t, StateLocked)
	var entangled int
	for _, s := range c.Slots() {
		if s.Type == "hardware" && s.Entangled {
			entangled++
		}
	}
	if entangled != 1 {
		t.Fatalf("slots after create: %+v", c.Slots())
	}
	// The key then unlocks with the password and its PIN.
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

func freshCore(t *testing.T, data string) (*Core, *recorder) {
	t.Helper()
	rec := &recorder{}
	c, err := New(Deps{Events: rec, Clock: newFakeClock(), DataDir: data})
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
	f := rec2.waitCeremony(t, StepFailed, false)
	if f.Error != CodeAuth {
		t.Fatalf("failed with %s", f.Error)
	}
	c2.CancelUnlock() // a park stands until the user leaves it
	rec2.waitState(t, StateNone)
	if _, err := os.Stat(filepath.Join(data2, "vault.eks")); err == nil {
		t.Fatal("an unproven file was installed")
	}
	if staged(data2) {
		t.Fatal("the staged file was left behind")
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
	if f := h.rec.waitCeremony(t, StepFailed, false); f.Error != CodeAuth {
		t.Fatalf("wrong key: %+v", f)
	}
}

// CreateVault over the vault kept here needs replace, builds the new
// vault as the incoming file, retires the old one and ends locked.
func TestCreateVaultReplaces(t *testing.T) {
	data := t.TempDir()
	c, rec := freshCore(t, data)
	c.SetAppOrigin("wails://wails")
	if e := c.CreateVault("", "First", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "first password")
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateLocked)
	if st := c.Status(); st.Path != filepath.Join(data, "vault.eks") || st.KeptElsewhere {
		t.Fatalf("created at %s: %+v", st.Path, st)
	}
	if e := c.CreateVault("", "Second", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); !isCode(e, CodeVaultExists) {
		t.Fatalf("without replace: %v", e)
	}
	rec.reset()
	if e := c.CreateVault("", "Second", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); e != nil {
		t.Fatal(e)
	}
	p = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "second password")
	final := rec.waitCeremony(t, StepRecovery, false)
	if !strings.HasPrefix(final.SlotLabel, "http://127.0.0.1:") {
		t.Fatalf("no one-time URL: %+v", final)
	}
	rec.waitState(t, StateLocked)
	st := c.Status()
	if st.DisplayName != "Second" || st.RetiredCopies != 1 || st.State != StateLocked {
		t.Fatalf("after replacing: %+v", st)
	}
	if staged(data) {
		t.Fatal("the incoming file was left behind")
	}
	// The retired copy is the first vault, and still opens with its password.
	ks, err := keystore.Open(st.RetiredPath)
	if err != nil {
		t.Fatal(err)
	}
	unl, err := ks.Unlock(keystore.PasswordCredential{Password: "first password"})
	if err != nil {
		t.Fatalf("the retired copy is not the first vault: %v", err)
	}
	unl.Close()
	ks.Close()
	rec.reset()
	c.BeginUnlock(MethodPassword)
	p = rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "second password")
	rec.waitState(t, StateUnlocked)
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
	rec.waitState(t, StateLocked)
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
	if st := c.Status(); st.State != StateNone || st.MissingPath != filepath.Join(data, "vault.eks") {
		t.Fatalf("an unreadable file at the place: %+v", st)
	}
	c.SetAppOrigin("wails://wails")
	// The file there, unreadable or not, is replaced only knowingly.
	if e := c.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, false); !isCode(e, CodeVaultExists) {
		t.Fatalf("over an unreadable file without replace: %v", e)
	}
	if e := c.CreateVault("", "New", EnrollOptions{Kind: EnrollPassword, Label: "pw"}, true); e != nil {
		t.Fatal(e)
	}
	p := rec.waitCeremony(t, StepPassword, true)
	c.SubmitSecret("password", p.PromptID, "a password")
	rec.waitCeremony(t, StepRecovery, false)
	rec.waitState(t, StateLocked)
	if st := c.Status(); st.MissingPath != "" || !st.HasPasswordSlot {
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
	// A garbage file that was at the vault's place is retired under a
	// name that says so, and not counted as a vault.
	entries, _ := os.ReadDir(data)
	var others int
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "file-replaced-") {
			others++
		}
	}
	if st := c.Status(); others != 1 || st.RetiredCopies != 0 {
		t.Fatalf("garbage retired as a vault: others=%d %+v", others, st)
	}
}
