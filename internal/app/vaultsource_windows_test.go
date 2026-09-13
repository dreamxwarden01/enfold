//go:build windows

package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// The spellings Windows allows for one file, and the refusal that has to
// see through all of them (APP.md §3, the outside review of 2026-09-13,
// findings 1 and 2).

// shortSpelling is p with its 8.3 short names, and "" when the volume makes
// none — 8dot3name creation can be turned off per volume, and then there is
// nothing here to prove.
func shortSpelling(t *testing.T, p string) string {
	t.Helper()
	u, err := windows.UTF16PtrFromString(p)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 1024)
	n, err := windows.GetShortPathName(u, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) >= len(buf) {
		return ""
	}
	short := windows.UTF16ToString(buf[:n])
	if strings.EqualFold(short, p) {
		return ""
	}
	return short
}

// TestEverySpellingOfTheVaultIsRefused: an extended-length prefix, an 8.3
// short name and a trailing separator are three ways of writing paths the
// comparison must already have agreed on. The first is the one that used to
// get through — filepath.Rel reads \\?\C as a volume of its own and answers
// that \\?\C:\…\Enfold and C:\…\Enfold are unrelated.
func TestEverySpellingOfTheVaultIsRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Spellings")
	data := h.c.deps.DataDir
	settings := filepath.Join(data, "settings.json")

	refuseFile := func(name, p string) {
		t.Helper()
		if _, e := h.c.AddFiles(id, rootID, []string{p}, PolicySkip); !isCode(e, CodeSourceIsVault) {
			t.Fatalf("AddFiles of %s (%q): %v", name, p, e)
		}
	}
	refuseDir := func(name, p string) {
		t.Helper()
		if _, e := h.c.AddFolder(id, rootID, p, PolicySkip); !isCode(e, CodeSourceIsVault) {
			t.Fatalf("AddFolder of %s (%q): %v", name, p, e)
		}
		if _, e := h.c.Extract(id, []string{rootID}, p, ExtractSkip, nil); !isCode(e, CodeSourceIsVault) {
			t.Fatalf("Extract into %s (%q): %v", name, p, e)
		}
	}

	// The extended-length prefix, on the vault and on the data folder.
	refuseFile(`the \\?\ spelling of the vault`, `\\?\`+h.vault)
	refuseFile(`the \\?\ spelling of a file in the data folder`, `\\?\`+settings)
	refuseDir(`the \\?\ spelling of the data folder`, `\\?\`+data)
	// The device namespace reaches the same objects by another door.
	refuseFile(`the \\.\ spelling of the vault`, `\\.\`+h.vault)

	// A trailing separator is the same folder.
	refuseDir("the data folder with a trailing separator", data+`\`)

	// The 8.3 short name: a second name for the same folder that folding
	// case does not make equal. The temporary directory's own name is long
	// enough to have one.
	if short := shortSpelling(t, h.vault); short == "" {
		t.Log("this volume makes no 8.3 short names; the short spelling is not exercised")
	} else {
		refuseFile("the 8.3 spelling of the vault", short)
	}
	if short := shortSpelling(t, data); short == "" {
		t.Log("this volume makes no 8.3 short names; the short spelling is not exercised")
	} else {
		refuseDir("the 8.3 spelling of the data folder", short)
		refuseFile("the 8.3 spelling of a file in the data folder", filepath.Join(short, "settings.json"))
		// And the two spellings meet at one string, which is what the
		// comparison rests on.
		if a, b := resolveLinks(short), resolveLinks(data); !samePath(a, b) {
			t.Fatalf("the short and long spellings resolve apart: %q and %q", a, b)
		}
	}

	// A sibling is still a sibling, however it is spelled.
	beside := data + "2"
	if err := os.MkdirAll(beside, 0o700); err != nil {
		t.Fatal(err)
	}
	near := filepath.Join(beside, "notes.txt")
	if err := os.WriteFile(near, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, e := h.c.AddFiles(id, rootID, []string{`\\?\` + near}, PolicySkip); e != nil {
		t.Fatalf("the \\\\?\\ spelling of a file beside the data folder was refused: %v", e)
	}
}

// TestAJunctionOntoTheVaultsPlaceIsRefused: a junction needs no privilege,
// unlike a symbolic link, so the link-following half of the check can be
// proved here rather than skipped. A junction names a directory, so the
// vault file is reached through one rather than pointed at by one.
func TestAJunctionOntoTheVaultsPlaceIsRefused(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Junction sources")

	toData := filepath.Join(h.dir, "link-to-data")
	if err := exec.Command("cmd", "/c", "mklink", "/J", toData, h.c.deps.DataDir).Run(); err != nil {
		t.Skipf("this machine will not make a junction: %v", err)
	}
	toVaultFolder := filepath.Join(h.dir, "link-to-the-vaults-folder")
	if err := exec.Command("cmd", "/c", "mklink", "/J", toVaultFolder, h.dir).Run(); err != nil {
		t.Skipf("this machine will not make a junction: %v", err)
	}

	if _, e := h.c.AddFolder(id, rootID, toData, PolicySkip); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("AddFolder of a junction onto the data folder: %v", e)
	}
	if _, e := h.c.AddFiles(id, rootID, []string{filepath.Join(toData, "settings.json")}, PolicySkip); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("AddFiles of a file in the data folder reached through a junction: %v", e)
	}
	if _, e := h.c.AddFiles(id, rootID, []string{filepath.Join(toVaultFolder, "vault.eks")}, PolicySkip); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("AddFiles of the vault reached through a junction: %v", e)
	}
	if _, e := h.c.Extract(id, []string{rootID}, toData, ExtractSkip, nil); !isCode(e, CodeSourceIsVault) {
		t.Fatalf("Extract into a junction onto the data folder: %v", e)
	}
}

// TestAnExtractNeverDescendsIntoARealJunction is the same rule as the seam
// test, proved against a junction Windows actually made. mklink /J needs no
// privilege, unlike a symbolic link, but a machine may still refuse it — a
// policy, a volume that is not NTFS — so this skips rather than fails.
func TestAnExtractNeverDescendsIntoARealJunction(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Junctions")
	h.src(t, "Enfold/vault.eks", "the tree's own copy")
	h.addFolder(t, id, rootID, filepath.Join(h.dir, "src", "Enfold"), PolicySkip)

	// What the junction points at stands in for the data folder: the test
	// never goes near the real one.
	target := filepath.Join(h.dir, "pretend-data")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	precious := filepath.Join(target, "vault.eks")
	if err := os.WriteFile(precious, []byte("the vault"), 0o600); err != nil {
		t.Fatal(err)
	}

	out := outDir(t)
	link := filepath.Join(out, "Enfold")
	if err := exec.Command("cmd", "/c", "mklink", "/J", link, target).Run(); err != nil {
		t.Skipf("this machine will not make a junction: %v", err)
	}
	if !isReparsePoint(link) {
		t.Fatal("the junction is not reported as a reparse point")
	}

	opID, e := h.c.Extract(id, []string{rootID}, out, ExtractReplace, nil)
	if e != nil {
		t.Fatal(e)
	}
	o := h.rec.waitOp(t, opID)
	if o.Error != "" {
		t.Fatalf("the extract failed as a whole: %+v", o)
	}
	if r := outcomesByName(o)["Enfold"]; r.Outcome != "failed" || r.Code != CodeDestinationLink {
		t.Fatalf("the junction came out as %+v", r)
	}
	b, err := os.ReadFile(precious)
	if err != nil || string(b) != "the vault" {
		t.Fatalf("the extract wrote through the junction: %q, %v", b, err)
	}
}
