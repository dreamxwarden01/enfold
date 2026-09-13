package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The vault is never a source and never a destination (APP.md §3, the
// outside review of 2026-09-13). The harness keeps its vault beside the
// data folder rather than in it — vault.eks and data\ are siblings under
// one temporary directory — so both halves of the rule have something to
// refuse: the folder Enfold owns, and the vault file kept elsewhere.

// TestTheVaultIsNeverASource: nothing of Enfold's own place goes into an
// archive. Named outright, reached through the folder above it, or written
// in a case the file system does not distinguish — all one refusal, and a
// sibling whose name merely starts with the same letters is not the folder
// and is added like anything else.
func TestTheVaultIsNeverASource(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Sources")
	data := h.c.deps.DataDir
	settings := filepath.Join(data, "settings.json")
	if _, err := os.Stat(settings); err != nil {
		t.Fatalf("the data folder has no settings file to try: %v", err)
	}
	plain := h.src(t, "ordinary.txt", "ordinary")

	refused := []struct {
		name string
		path string
	}{
		{"the vault file itself", h.vault},
		{"a file inside the data folder", settings},
		{"the data folder itself", data},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := h.c.AddFiles(id, rootID, []string{tc.path}, PolicySkip); !isCode(e, CodeSourceIsVault) {
				t.Fatalf("AddFiles(%s): %v", tc.name, e)
			}
			// One bad path poisons the call: the batch is refused whole,
			// never nine of ten files written around an outcome.
			if _, e := h.c.AddFiles(id, rootID, []string{plain, tc.path}, PolicySkip); !isCode(e, CodeSourceIsVault) {
				t.Fatalf("AddFiles(ordinary, %s): %v", tc.name, e)
			}
			if _, e := h.c.AddFolder(id, rootID, tc.path, PolicySkip); !isCode(e, CodeSourceIsVault) {
				t.Fatalf("AddFolder(%s): %v", tc.name, e)
			}
		})
	}

	// The folder above the data folder is the whole reason the rule is
	// checked in both directions: it names neither the vault nor the data
	// folder, and a walk of it would carry both in.
	if _, e := h.c.AddFolder(id, rootID, h.dir, PolicySkip); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("AddFolder of the folder holding the vault and the data folder: %v", e)
	}

	if runtime.GOOS == "windows" {
		// The file system does not distinguish the case, so neither does
		// the refusal.
		for _, p := range []string{strings.ToUpper(h.vault), strings.ToUpper(data)} {
			if _, e := h.c.AddFiles(id, rootID, []string{p}, PolicySkip); !isCode(e, CodeSourceIsVault) {
				t.Fatalf("AddFiles of %q in another case: %v", p, e)
			}
		}
	}

	// A sibling with the same prefix is a sibling: "inside" is a whole
	// element, not a string prefix (the Enfold / Enfold2 pair).
	beside := data + "2"
	if err := os.MkdirAll(beside, 0o700); err != nil {
		t.Fatal(err)
	}
	near := filepath.Join(beside, "notes.txt")
	if err := os.WriteFile(near, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	o := h.add(t, id, rootID, PolicySkip, near)
	if o.Error != "" || len(o.Results) != 1 || o.Results[0].Outcome != "added" {
		t.Fatalf("a folder beside the data folder was refused: %+v", o)
	}
	o = h.addFolder(t, id, rootID, beside, PolicySkip)
	if o.Error != "" {
		t.Fatalf("adding the folder beside the data folder: %+v", o)
	}

	// The replace is the same door and carries the same lock.
	fid := h.row(t, id, rootID, "notes.txt").ID
	if _, e := h.c.ReplaceFile(id, fid, h.vault); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("ReplaceFile from the vault: %v", e)
	}
	if _, e := h.c.ReplaceFile(id, fid, settings); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("ReplaceFile from inside the data folder: %v", e)
	}
}

// TestALinkToTheVaultIsTheVault: addFile opens its source with os.Open,
// which follows a junction or a symlink wherever it points, so the check
// follows them first. Windows makes a symlink only for a user who may, so
// the test says what it could not try rather than claiming the rule holds.
func TestALinkToTheVaultIsTheVault(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Links")
	link := filepath.Join(h.dir, "shortcut.eks")
	if err := os.Symlink(h.vault, link); err != nil {
		t.Skipf("this machine does not let the tests make a symlink: %v", err)
	}
	if _, e := h.c.AddFiles(id, rootID, []string{link}, PolicySkip); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("AddFiles through a link to the vault: %v", e)
	}
	dirLink := filepath.Join(h.dir, "shortcut-folder")
	if err := os.Symlink(h.c.deps.DataDir, dirLink); err != nil {
		t.Skipf("this machine does not let the tests link a folder: %v", err)
	}
	if _, e := h.c.AddFolder(id, rootID, dirLink, PolicySkip); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("AddFolder through a link to the data folder: %v", e)
	}
	if _, e := h.c.Extract(id, []string{rootID}, dirLink, ExtractSkip, nil); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("Extract into a link to the data folder: %v", e)
	}
}

// TestAHardLinkToTheVaultIsCaughtOnTheHandle is the case no path check can
// ever see: a second directory entry for vault.eks, in a folder of the
// caller's own, with a name of its own. Nothing about that path is the
// vault, so the refusal has to come from what was opened — os.SameFile on
// the handle, which compares the volume and the file id and not the name.
//
// The refusal lands as the item's outcome rather than the call's, since it
// is found inside the transaction with the batch already running: what
// matters is that no byte of the vault is read into the archive.
func TestAHardLinkToTheVaultIsCaughtOnTheHandle(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Hard links")
	hard := filepath.Join(h.dir, "innocent.bin")
	if err := os.Link(h.vault, hard); err != nil {
		t.Skipf("this volume does not take a hard link: %v", err)
	}
	// The path check cannot see it: it is not in the data folder and it is
	// not the vault's path.
	if e := h.c.refuseVaultPlaces(hard); e != nil {
		t.Fatalf("the path check saw a hard link, so this test proves nothing about the handle: %v", e)
	}
	plain := h.src(t, "ordinary.txt", "ordinary")
	o := h.add(t, id, rootID, PolicySkip, plain, hard)
	by := outcomesByName(o)
	if r := by["innocent.bin"]; r.Outcome != "failed" || r.Code != CodeSourceIsVault {
		t.Fatalf("a hard link to the vault was added as %+v", r)
	}
	// The rest of the batch is nobody's fault and is written.
	if r := by["ordinary.txt"]; r.Outcome != "added" {
		t.Fatalf("the ordinary file of the same batch: %+v", r)
	}
	if _, e := h.c.Page(id, rootID, "name", 0, 100); e != nil {
		t.Fatal(e)
	}
	if rows := rowsByName(h.page(t, id, rootID)); rows["innocent.bin"].ID != "" {
		t.Fatal("the hard link to the vault is in the archive")
	}

	// The replace is the same door. One file, so the refusal is the whole
	// operation's — and it is found once the source is open, which is after
	// the call has handed its id back, so it lands on the operation rather
	// than on the return.
	fid := h.row(t, id, rootID, "ordinary.txt").ID
	opID, e := h.c.ReplaceFile(id, fid, hard)
	if e != nil {
		t.Fatalf("ReplaceFile: %v", e)
	}
	if ro := h.rec.waitOp(t, opID); ro.Error != CodeSourceIsVault {
		t.Fatalf("a replace from a hard link to the vault ended as %+v", ro)
	}
	// Nothing of the vault landed in the record: it still holds the eight
	// bytes it was added with, and the vault file is far larger.
	if got := h.row(t, id, rootID, "ordinary.txt").Size; got != uint64(len("ordinary")) {
		t.Fatalf("the record is %d bytes, so the replace wrote after all", got)
	}
}

// TestAnExtractNeverDescendsIntoALink: the destination is judged once, on
// the way in, and that says nothing about what is inside it. A folder named
// Enfold under an ordinary destination may be a junction onto the data
// folder, and an archive holding Enfold\vault.eks would then be written
// straight over the vault. The link is not followed, the item fails, and
// its subtree goes with it — reported once, for its top.
func TestAnExtractNeverDescendsIntoALink(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Trees")
	h.src(t, "Enfold/vault.eks", "the tree's own copy")
	h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "Enfold"), PolicySkip)

	out := outDir(t)
	standing := filepath.Join(out, "Enfold")
	if err := os.MkdirAll(standing, 0o700); err != nil {
		t.Fatal(err)
	}
	// The seam says this folder is a link, so the rule is proved on a
	// machine that will make no junction of its own.
	h.c.extractFS = extractFS{isLink: func(p string) bool { return samePath(p, standing) }}

	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractReplace, nil)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" {
		t.Fatalf("the extract failed as a whole: %+v", o)
	}
	by := outcomesByName(o)
	r, ok := by["Enfold"]
	if !ok || r.Outcome != "failed" || r.Code != CodeDestinationLink {
		t.Fatalf("the folder standing in for a link came out as %+v", r)
	}
	// Reported once, for the top: nothing beneath a link is attempted or
	// listed.
	if _, listed := by["Enfold/vault.eks"]; listed {
		t.Fatalf("the subtree under the link was listed: %+v", o.Results)
	}
	if _, err := os.Stat(filepath.Join(standing, "vault.eks")); !os.IsNotExist(err) {
		t.Fatalf("the extract wrote through the link: %v", err)
	}
}

// TestTheVaultIsNeverADestination: an extract writes a whole tree, and a
// tree can reach down onto the vault, so the destination is judged by the
// same rule as a source and before any record is planned.
func TestTheVaultIsNeverADestination(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Destinations")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))
	data := h.c.deps.DataDir

	for _, dir := range []string{
		filepath.Join(data, "out"),       // inside the data folder
		data,                             // the data folder itself
		h.dir,                            // above the data folder and the vault
		filepath.Dir(filepath.Dir(data)), // further above still
	} {
		if _, e := h.c.Extract(id, []string{rootID}, dir, ExtractSkip, nil); !isCode(e, CodeSourceIsVault) {
			t.Fatalf("Extract into %q: %v", dir, e)
		}
	}
	if runtime.GOOS == "windows" {
		if _, e := h.c.Extract(id, []string{rootID}, strings.ToUpper(data), ExtractSkip, nil); !isCode(e, CodeSourceIsVault) {
			t.Fatal("an extract into the data folder in another case was allowed")
		}
	}

	// A destination that is merely named like the data folder is a
	// destination like any other.
	out := data + "2"
	if err := os.MkdirAll(out, 0o700); err != nil {
		t.Fatal(err)
	}
	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractSkip, nil)
	if e != nil {
		t.Fatalf("extract beside the data folder: %v", e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != "" {
		t.Fatalf("extract beside the data folder: %+v", o)
	}
	if _, err := os.Stat(filepath.Join(out, "a.txt")); err != nil {
		t.Fatalf("the extract wrote nothing beside the data folder: %v", err)
	}
}

// TestTheStagingRootNeverReachesTheVault is the sanity assertion, not a
// refusal any user meets: the shell stages under %TEMP%\Enfold\drag. A root
// that IS the data folder, or that stands above it or above the vault, is
// refused; a folder of its own inside the data folder — what a caller
// naming no root gets — is allowed, since every staged file lands two
// levels down under <root>\<id>\items and can never land on vault.eks.
func TestTheStagingRootNeverReachesTheVault(t *testing.T) {
	h := newHarness(t, nil, nil)
	data := h.c.deps.DataDir
	for _, root := range []string{data, h.dir, filepath.Dir(data)} {
		if e := h.c.refuseStagingRoot(root); !isCode(e, CodeSourceIsVault) {
			t.Fatalf("a staging root at %q was allowed: %v", root, e)
		}
	}
	for _, root := range []string{filepath.Join(data, "drag"), data + "2", outDir(t)} {
		if e := h.c.refuseStagingRoot(root); e != nil {
			t.Fatalf("a staging root at %q was refused: %v", root, e)
		}
	}
	// The default is the one a caller naming no root gets, and it must be
	// one of the allowed ones.
	if e := h.c.refuseStagingRoot(h.c.dragRoot()); e != nil {
		t.Fatalf("the default staging root is refused: %v", e)
	}
}

// TestResolveLinksKeepsATailThatIsNotThereYet: an extract's destination may
// be a folder the extract itself makes, and a junction above it still has
// to be seen, so the resolution follows what exists and joins the rest back
// on rather than giving up on the whole path.
func TestResolveLinksKeepsATailThatIsNotThereYet(t *testing.T) {
	dir := t.TempDir()
	real := resolveLinks(dir)
	if !filepath.IsAbs(real) {
		t.Fatalf("resolveLinks(%q) = %q, which is not absolute", dir, real)
	}
	deep := filepath.Join(dir, "not", "there", "yet")
	if got, want := resolveLinks(deep), filepath.Join(real, "not", "there", "yet"); got != want {
		t.Fatalf("resolveLinks(%q) = %q, want %q", deep, got, want)
	}
	// Resolving what has already been resolved changes nothing: the
	// comparison rests on one spelling, and a second pass must find it.
	if got := resolveLinks(real); got != real {
		t.Fatalf("resolveLinks is not settled: %q became %q", real, got)
	}
	if got := resolveLinks(dir + string(filepath.Separator)); got != real {
		t.Fatalf("a trailing separator changed the answer: %q, want %q", got, real)
	}
	// Nothing of the path on disk at all: its absolute self, and no panic
	// walking to the volume's root looking for something that exists.
	nowhere := filepath.Join(dir, "gone")
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if got := resolveLinks(nowhere); !filepath.IsAbs(got) || filepath.Base(got) != "gone" {
		t.Fatalf("resolveLinks of a path with nothing on disk = %q", got)
	}
}
