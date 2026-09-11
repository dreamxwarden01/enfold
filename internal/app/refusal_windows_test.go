//go:build windows

package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// What Windows itself answers (checked on the dev machine 2026-09-10): a
// component of 300 UTF-16 units is ERROR_INVALID_NAME (123), not
// ERROR_FILENAME_EXCED_RANGE (206) — NTFS refuses the component, and the
// path as a whole was within what a long-path-aware process may give. Both
// are a refusal here, and ENAMETOOLONG with them.
func TestRefusedByVolumeOnWindows(t *testing.T) {
	err := os.Mkdir(filepath.Join(outDir(t), strings.Repeat("n", 300)), 0o700)
	if err == nil {
		t.Skip("this volume takes a 300-unit name")
	}
	if !errors.Is(err, windows.ERROR_INVALID_NAME) {
		t.Fatalf("a 300-unit component answered %v, not ERROR_INVALID_NAME", err)
	}
	if !refusedByVolume(err) {
		t.Fatalf("Windows' own refusal is not read as one: %v", err)
	}
	for _, e := range []error{windows.ERROR_FILENAME_EXCED_RANGE, windows.ERROR_INVALID_NAME} {
		if !refusedByVolume(&os.LinkError{Op: "rename", Old: "a", New: "b", Err: e}) {
			t.Fatalf("%v is not read as a refusal", e)
		}
	}
	if refusedByVolume(&os.LinkError{Op: "rename", Old: "a", New: "b", Err: windows.ERROR_ACCESS_DENIED}) {
		t.Fatal("a denied access is read as a refusal")
	}
}

// The destination root itself refused — a 300-unit folder name the volume
// will not make — is the operation's error, before any outcome; and the
// error Windows' MoveFileEx answers for a name it will not take is read the
// same way as ENAMETOOLONG through the placement.
func TestExtractRootRefusedIsTheOperationsError(t *testing.T) {
	h := newHarness(t, nil, nil)
	h.unlockWithPassword()
	id := h.openArchive(t, "Root")
	h.add(t, id, rootID, PolicySkip, h.src(t, "a.txt", "a"))

	root := filepath.Join(outDir(t), strings.Repeat("n", 300))
	opID, e := h.c.Extract(id, []string{rootID}, root, ExtractSkip, nil)
	if e != nil {
		t.Fatal(e)
	}
	if o := h.rec.waitOp(t, opID); o.Error != CodeFilePathRefused || len(o.Results) != 0 {
		t.Fatalf("the refused root: %+v", o)
	}

	h.c.extractFS = refusing(windows.ERROR_FILENAME_EXCED_RANGE, map[string]bool{"a.txt": true}, nil)
	opID, _ = h.c.Extract(id, []string{rootID}, outDir(t), ExtractSkip, nil)
	if r := h.rec.waitOp(t, opID).Results[0]; r.Outcome != "name_refused" || r.Code != CodeFileNameRefused {
		t.Fatalf("ERROR_FILENAME_EXCED_RANGE on placement: %+v", r)
	}
	h.c.extractFS = refusing(windows.ERROR_INVALID_NAME, nil, map[string]bool{"d": true})
	if _, e := h.c.CreateFolder(id, rootID, "d"); e != nil {
		t.Fatal(e)
	}
	opID, _ = h.c.Extract(id, []string{rootID}, outDir(t), ExtractSkip, nil)
	if r := outcomesByName(h.rec.waitOp(t, opID))["d"]; r.Outcome != "name_refused" || r.Code != CodeFileNameRefused || !r.IsDir {
		t.Fatalf("ERROR_INVALID_NAME on mkdir: %+v", r)
	}
}
