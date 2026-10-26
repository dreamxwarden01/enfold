package app

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"unicode/utf16"
)

// hdrop builds a CF_HDROP block as Explorer leaves one on the clipboard:
// the DROPFILES header with pFiles 20 and fWide 1, then each name in
// UTF-16 with its NUL, then the list's closing NUL.
func hdrop(names ...string) []byte {
	b := make([]byte, dropFilesHeader)
	binary.LittleEndian.PutUint32(b[0:], dropFilesHeader)
	binary.LittleEndian.PutUint32(b[16:], 1)
	for _, n := range names {
		for _, u := range utf16.Encode([]rune(n)) {
			b = binary.LittleEndian.AppendUint16(b, u)
		}
		b = binary.LittleEndian.AppendUint16(b, 0)
	}
	return binary.LittleEndian.AppendUint16(b, 0)
}

func dword(v uint32) []byte { return binary.LittleEndian.AppendUint32(nil, v) }

// TestParseHDrop: the names come back in order, a name outside the BMP
// whole; an empty list is no paths; slack after the closing NUL — a
// GlobalAlloc block is rounded up — is ignored (APP.md §6, Paste).
func TestParseHDrop(t *testing.T) {
	names := []string{`D:\Pictures\IMG_0001.HEIC`, `D:\Pictures\2024`, `C:\Users\me\Desktop\😀 notes.txt`}
	got, err := ParseHDrop(hdrop(names...))
	if err != nil || !reflect.DeepEqual(got, names) {
		t.Fatalf("got %q, %v", got, err)
	}
	if got, err := ParseHDrop(hdrop()); err != nil || len(got) != 0 {
		t.Fatalf("an empty list: %q, %v", got, err)
	}
	padded := append(hdrop(`D:\a.txt`), make([]byte, 12)...)
	if got, err := ParseHDrop(padded); err != nil || !reflect.DeepEqual(got, []string{`D:\a.txt`}) {
		t.Fatalf("a rounded-up block: %q, %v", got, err)
	}
	// pFiles past the header: a writer that leaves room after it.
	far := hdrop(`D:\b.txt`)
	far = append(far[:dropFilesHeader:dropFilesHeader], append(make([]byte, 8), far[dropFilesHeader:]...)...)
	binary.LittleEndian.PutUint32(far[0:], dropFilesHeader+8)
	if got, err := ParseHDrop(far); err != nil || !reflect.DeepEqual(got, []string{`D:\b.txt`}) {
		t.Fatalf("names further on: %q, %v", got, err)
	}
}

// TestParseHDropRefuses: the clipboard is any process's to fill, so a block
// that does not hold what its header says is refused, never read past.
func TestParseHDropRefuses(t *testing.T) {
	whole := hdrop(`D:\a.txt`, `D:\b.txt`)
	narrow := hdrop(`D:\a.txt`)
	binary.LittleEndian.PutUint32(narrow[16:], 0)
	if _, err := ParseHDrop(narrow); !errors.Is(err, ErrHDropNarrow) {
		t.Fatalf("an ANSI block: %v", err)
	}
	pastEnd := hdrop(`D:\a.txt`)
	binary.LittleEndian.PutUint32(pastEnd[0:], uint32(len(pastEnd)+2))
	inHeader := hdrop(`D:\a.txt`)
	binary.LittleEndian.PutUint32(inHeader[0:], 4)
	for name, b := range map[string][]byte{
		"empty":                nil,
		"a short header":       whole[:12],
		"pFiles past the end":  pastEnd,
		"pFiles in the header": inHeader,
		"no closing NUL":       whole[:len(whole)-2],
		"cut inside a name":    whole[:dropFilesHeader+6],
		"an odd tail":          whole[:len(whole)-1],
	} {
		if got, err := ParseHDrop(b); err == nil {
			t.Errorf("%s: read %q", name, got)
		}
	}
}

// TestParseDropEffect: Explorer's Cut writes DROPEFFECT_MOVE, its Copy
// COPY|LINK; a missing or short block is a copy.
func TestParseDropEffect(t *testing.T) {
	for _, c := range []struct {
		b    []byte
		move bool
	}{
		{dword(2), true}, {dword(5), false}, {dword(1), false}, {dword(3), true},
		{nil, false}, {[]byte{2, 0}, false}, {append(dword(2), 0, 0, 0, 0), true},
	} {
		if got := ParseDropEffect(c.b); got != c.move {
			t.Errorf("% x: move %v, want %v", c.b, got, c.move)
		}
	}
}

// TestClipboardOf: each path is stat'ed for its kind; one that is gone is
// left out, one that cannot be stat'ed otherwise is offered as a file; a
// clipboard of nothing that exists answers an empty list, not null, with
// Move false (APP.md §3, §6).
func TestClipboardOf(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "2024")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(dir, "gone.txt")
	got := ClipboardOf([]string{file, gone, "", sub}, true, os.Stat)
	want := Clipboard{Files: []ClipFile{{Path: file}, {Path: sub, IsDir: true}}, Move: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	refused := func(string) (fs.FileInfo, error) { return nil, fs.ErrPermission }
	if got := ClipboardOf([]string{`\\server\share\x`}, false, refused); !reflect.DeepEqual(got.Files, []ClipFile{{Path: `\\server\share\x`}}) {
		t.Fatalf("an unreadable path: %+v", got)
	}
	for _, paths := range [][]string{nil, {gone}} {
		got := ClipboardOf(paths, true, os.Stat)
		if got.Files == nil || len(got.Files) != 0 || got.Move {
			t.Fatalf("%q: %+v", paths, got)
		}
	}
}
