package app

import (
	"encoding/binary"
	"errors"
	"io/fs"
	"unicode/utf16"
)

// ClipFile is one path the clipboard names (APP.md §3, Shell.ClipboardFiles;
// §6, Paste): the path as Explorer put it there and whether it is a folder,
// so that the page sends files to AddFiles and each folder to AddFolder as
// it does a drop's (dropPayload's IsDir, for the same reason: the page
// cannot stat a path itself).
type ClipFile struct {
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// Clipboard is what Shell.ClipboardFiles answers: the files a copy or a cut
// in Explorer left on the clipboard (CF_HDROP), and whether it was a cut
// ("Preferred DropEffect" carrying DROPEFFECT_MOVE). Move only words the
// Paste item's caption: a paste adds, and nothing is ever deleted from the
// user's disk by one (ruled 2026-10-26). Files is empty, never null, when
// the clipboard holds no files.
type Clipboard struct {
	Files []ClipFile `json:"files"`
	Move  bool       `json:"move"`
}

// The DROPFILES header CF_HDROP starts with: pFiles (DWORD, the offset of
// the names from the block's start), pt (POINT, two LONGs), fNC (BOOL) and
// fWide (BOOL) — 20 bytes, the names after it.
const (
	dropFilesHeader = 20
	// dropEffectMove is DROPEFFECT_MOVE, the bit Explorer's Cut writes to
	// "Preferred DropEffect" (its Copy writes COPY|LINK, 5).
	dropEffectMove = 2
)

// ErrHDropNarrow is ParseHDrop's answer to a block whose names are in the
// ANSI code page (fWide 0): there is no code page to decode them with here,
// so the shell asks DragQueryFileW, which converts them, instead.
var ErrHDropNarrow = errors.New("hdrop: narrow names")

// errHDropMalformed is a block whose header or names do not fit in it.
var errHDropMalformed = errors.New("hdrop: malformed block")

// ParseHDrop reads the paths a CF_HDROP block names, bounded by the block
// itself: the clipboard is any process's to fill, so a header that points
// past the end, or names that never reach their double NUL, are refused
// rather than followed (DragQueryFileW trusts the terminator and is given
// no size). An empty list — the double NUL at once — is no paths and no
// error. Wide names only; see ErrHDropNarrow.
func ParseHDrop(block []byte) ([]string, error) {
	if len(block) < dropFilesHeader {
		return nil, errHDropMalformed
	}
	off := binary.LittleEndian.Uint32(block[0:4])
	if binary.LittleEndian.Uint32(block[16:20]) == 0 {
		return nil, ErrHDropNarrow
	}
	if off < dropFilesHeader || uint64(off) > uint64(len(block)) {
		return nil, errHDropMalformed
	}
	rest := block[off:]
	var paths []string
	var name []uint16
	for i := 0; i+1 < len(rest); i += 2 {
		u := binary.LittleEndian.Uint16(rest[i:])
		if u != 0 {
			name = append(name, u)
			continue
		}
		if len(name) == 0 {
			// The second NUL of the pair, or the first of an empty list.
			return paths, nil
		}
		paths = append(paths, string(utf16.Decode(name)))
		name = name[:0]
	}
	return nil, errHDropMalformed
}

// ParseDropEffect reads the "Preferred DropEffect" DWORD: true when it
// carries DROPEFFECT_MOVE, which is what a cut in Explorer leaves. A block
// too short to hold the DWORD is read as a copy.
func ParseDropEffect(block []byte) bool {
	if len(block) < 4 {
		return false
	}
	return binary.LittleEndian.Uint32(block)&dropEffectMove != 0
}

// ClipboardOf turns the paths the clipboard named into the answer, stat'ing
// each with stat (os.Stat in the shell) for its kind. A path that no longer
// exists — deleted or moved since it was copied — is left out, so a stale
// clipboard reads as nothing to paste; one that cannot be stat'ed for
// another reason (access refused, a share that answered nothing) is offered
// as a file, as a drop offers it, and the add reports it (APP.md §3).
func ClipboardOf(paths []string, move bool, stat func(string) (fs.FileInfo, error)) Clipboard {
	out := Clipboard{Files: []ClipFile{}, Move: move}
	for _, p := range paths {
		if p == "" {
			continue
		}
		fi, err := stat(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		out.Files = append(out.Files, ClipFile{Path: p, IsDir: err == nil && fi.IsDir()})
	}
	if len(out.Files) == 0 {
		out.Move = false
	}
	return out
}
