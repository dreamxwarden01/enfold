//go:build windows

package app

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

// strayVerdict judges a failed removal of a test's folder (tempdir_test.go):
// wait is whether another try may succeed, and ours names what this process
// made that is still there.
//
// Only a folder that is not empty is judged here. After RemoveAll emptied
// it, it holds something made since, and the owner says by whom: the
// folder's own, which is this process's, or another account's. Only a file
// is counted as ours: the folders are all ours, and one is left either for
// what is in it or because a stray in it went after RemoveAll gave up on it.
func strayVerdict(root string, err error) (wait bool, ours []string) {
	if !errors.Is(err, windows.ERROR_DIR_NOT_EMPTY) {
		return false, nil
	}
	me := ownerOf(root)
	if me == nil {
		return true, nil
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		// A stray that is gone by now has no owner left to read.
		if o := ownerOf(p); o != nil && o.Equals(me) {
			rel, _ := filepath.Rel(root, p)
			ours = append(ours, rel)
		}
		return nil
	})
	return len(ours) == 0, ours
}

// ownerOf is the owner of the file or folder at p, nil when it cannot be
// read — when the file is gone, most often.
func ownerOf(p string) *windows.SID {
	sd, err := windows.GetNamedSecurityInfo(p, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION)
	if err != nil {
		return nil
	}
	sid, _, err := sd.Owner()
	if err != nil {
		return nil
	}
	return sid
}

// The detector half of the removal: a file this process made, in a folder
// that is not empty after RemoveAll, is named and never waited out; the
// folders, ours all, are not counted once what was in them has gone; and a
// handle still open is not judged here at all.
func TestStrayVerdictNamesWhatThisProcessMade(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "001", "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	late := filepath.Join(data, "settings.json.tmp")
	if err := os.WriteFile(late, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	notEmpty := &fs.PathError{Op: "unlinkat", Path: root, Err: windows.ERROR_DIR_NOT_EMPTY}
	wait, ours := strayVerdict(root, notEmpty)
	if wait || len(ours) != 1 || ours[0] != filepath.Join("001", "data", "settings.json.tmp") {
		t.Fatalf("a file of ours: wait %v, ours %q", wait, ours)
	}
	if err := os.Remove(late); err != nil {
		t.Fatal(err)
	}
	if wait, ours := strayVerdict(root, notEmpty); !wait || len(ours) != 0 {
		t.Fatalf("only folders of ours left: wait %v, ours %q", wait, ours)
	}
	sharing := &fs.PathError{Op: "unlinkat", Path: root, Err: windows.ERROR_SHARING_VIOLATION}
	if wait, ours := strayVerdict(root, sharing); wait || len(ours) != 0 {
		t.Fatalf("a sharing violation: wait %v, ours %q", wait, ours)
	}
}
