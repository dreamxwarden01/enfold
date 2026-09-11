//go:build windows

package main

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// TestMain silences the COM log: these tests exercise the Go core, and the log
// is written for a human watching a real drag.
func TestMain(m *testing.M) {
	logWriter = io.Discard
	os.Exit(m.Run())
}

// sdkFileDescriptorW is FILEDESCRIPTORW as Windows SDK 10.0.26100.0 declares it
// for x64. The numbers were printed by compiling sizeof/offsetof against
// shlobj_core.h, not recalled, and this table is the contract the encoder is
// held to.
var sdkFileDescriptorW = []struct {
	field  string
	offset int
	size   int
}{
	{"dwFlags", 0, 4},
	{"clsid", 4, 16},
	{"sizel", 20, 8},
	{"pointl", 28, 8},
	{"dwFileAttributes", 36, 4},
	{"ftCreationTime", 40, 8},
	{"ftLastAccessTime", 48, 8},
	{"ftLastWriteTime", 56, 8},
	{"nFileSizeHigh", 64, 4},
	{"nFileSizeLow", 68, 4},
	{"cFileName", 72, 520}, // WCHAR[MAX_PATH], MAX_PATH == 260
}

func TestFileDescriptorWLayoutConstants(t *testing.T) {
	if fileDescriptorWSize != 592 {
		t.Fatalf("fileDescriptorWSize = %d, want 592", fileDescriptorWSize)
	}
	if fileGroupDescWHdr != 4 {
		t.Fatalf("fileGroupDescWHdr = %d, want 4 (UINT cItems)", fileGroupDescWHdr)
	}
	if fdFileNameUnits != 260 {
		t.Fatalf("fdFileNameUnits = %d, want MAX_PATH (260)", fdFileNameUnits)
	}
	// The fields must tile the struct end to end with no gap and no overlap.
	end := 0
	for _, f := range sdkFileDescriptorW {
		if f.offset != end {
			t.Errorf("%s starts at %d, previous field ended at %d", f.field, f.offset, end)
		}
		end = f.offset + f.size
	}
	if end != fileDescriptorWSize {
		t.Errorf("fields end at %d, struct is %d bytes", end, fileDescriptorWSize)
	}
	// The constants the encoder writes through must agree with that table.
	for _, tc := range []struct {
		name  string
		got   int
		field string
	}{
		{"fdOffFlags", fdOffFlags, "dwFlags"},
		{"fdOffCLSID", fdOffCLSID, "clsid"},
		{"fdOffSizeL", fdOffSizeL, "sizel"},
		{"fdOffPointL", fdOffPointL, "pointl"},
		{"fdOffFileAttributes", fdOffFileAttributes, "dwFileAttributes"},
		{"fdOffCreationTime", fdOffCreationTime, "ftCreationTime"},
		{"fdOffLastAccessTime", fdOffLastAccessTime, "ftLastAccessTime"},
		{"fdOffLastWriteTime", fdOffLastWriteTime, "ftLastWriteTime"},
		{"fdOffFileSizeHigh", fdOffFileSizeHigh, "nFileSizeHigh"},
		{"fdOffFileSizeLow", fdOffFileSizeLow, "nFileSizeLow"},
		{"fdOffFileName", fdOffFileName, "cFileName"},
	} {
		want := -1
		for _, f := range sdkFileDescriptorW {
			if f.field == tc.field {
				want = f.offset
			}
		}
		if tc.got != want {
			t.Errorf("%s = %d, SDK offsetof(%s) = %d", tc.name, tc.got, tc.field, want)
		}
	}
}

func TestEncodeFileGroupDescriptorW(t *testing.T) {
	write := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	files := []synthFile{
		{name: `Proto\enfold-proto-5GiB.bin`, size: 5 << 30},
		{name: "enfold-proto-small.txt", size: 1 << 10},
	}
	blob := encodeFileGroupDescriptorW(files, write)

	wantLen := fileGroupDescWHdr + fileDescriptorWSize*len(files)
	if len(blob) != wantLen {
		t.Fatalf("blob is %d bytes, want %d", len(blob), wantLen)
	}
	if got := binary.LittleEndian.Uint32(blob[0:]); got != uint32(len(files)) {
		t.Errorf("cItems = %d, want %d", got, len(files))
	}

	for i, f := range files {
		d := blob[fileGroupDescWHdr+i*fileDescriptorWSize:][:fileDescriptorWSize]

		if got := binary.LittleEndian.Uint32(d[fdOffFlags:]); got != descriptorFlags {
			t.Errorf("file %d dwFlags = 0x%08X, want 0x%08X", i, got, uint32(descriptorFlags))
		}
		// Every flag set must name a field the encoder actually filled, and
		// every field filled must have its flag set.
		if descriptorFlags&fdFileSize == 0 || descriptorFlags&fdWritesTime == 0 ||
			descriptorFlags&fdProgressUI == 0 || descriptorFlags&fdAttributes == 0 {
			t.Errorf("descriptorFlags 0x%08X is missing one of FD_FILESIZE|FD_WRITESTIME|FD_PROGRESSUI|FD_ATTRIBUTES",
				uint32(descriptorFlags))
		}
		// FD_UNICODE says of the descriptor what the W in the format's name
		// says: these are WCHARs. Redundant, set anyway, and shipping sources
		// set it.
		if descriptorFlags&fdUnicode == 0 {
			t.Errorf("descriptorFlags 0x%08X does not set FD_UNICODE", uint32(descriptorFlags))
		}
		if descriptorFlags&(fdCLSID|fdSizePoint|fdCreateTime|fdAccessTime) != 0 {
			t.Errorf("descriptorFlags claims a field the encoder leaves zero")
		}

		if got := binary.LittleEndian.Uint32(d[fdOffFileAttributes:]); got != fileAttributeNormal {
			t.Errorf("file %d dwFileAttributes = 0x%X, want FILE_ATTRIBUTE_NORMAL", i, got)
		}

		hi := binary.LittleEndian.Uint32(d[fdOffFileSizeHigh:])
		lo := binary.LittleEndian.Uint32(d[fdOffFileSizeLow:])
		if got := int64(uint64(hi)<<32 | uint64(lo)); got != f.size {
			t.Errorf("file %d size = %d (hi=%d lo=%d), want %d", i, got, hi, lo, f.size)
		}
		// The 5 GiB file is the reason the split matters: it does not fit in
		// nFileSizeLow alone.
		if i == 0 && hi == 0 {
			t.Errorf("file 0 is %d bytes but nFileSizeHigh is zero", f.size)
		}

		// FILETIME: 100-nanosecond intervals since 1601-01-01, low then high.
		lowT := binary.LittleEndian.Uint32(d[fdOffLastWriteTime:])
		highT := binary.LittleEndian.Uint32(d[fdOffLastWriteTime+4:])
		ft := uint64(highT)<<32 | uint64(lowT)
		wantFT := uint64(write.UnixNano()/100) + 116444736000000000
		if ft != wantFT {
			t.Errorf("file %d ftLastWriteTime = %d, want %d", i, ft, wantFT)
		}

		// cFileName: UTF-16, NUL-terminated, and nothing after the NUL.
		want := utf16.Encode([]rune(f.name))
		for j, u := range want {
			got := binary.LittleEndian.Uint16(d[fdOffFileName+2*j:])
			if got != u {
				t.Fatalf("file %d cFileName[%d] = 0x%04X, want 0x%04X", i, j, got, u)
			}
		}
		if got := binary.LittleEndian.Uint16(d[fdOffFileName+2*len(want):]); got != 0 {
			t.Errorf("file %d cFileName is not NUL-terminated at %d", i, len(want))
		}
		for j := len(want) + 1; j < fdFileNameUnits; j++ {
			if got := binary.LittleEndian.Uint16(d[fdOffFileName+2*j:]); got != 0 {
				t.Fatalf("file %d cFileName[%d] = 0x%04X after the NUL, want 0", i, j, got)
				break
			}
		}

		// Fields no flag claims must be left zero.
		for _, z := range []struct {
			name string
			off  int
			n    int
		}{
			{"clsid", fdOffCLSID, 16},
			{"sizel", fdOffSizeL, 8},
			{"pointl", fdOffPointL, 8},
			{"ftCreationTime", fdOffCreationTime, 8},
			{"ftLastAccessTime", fdOffLastAccessTime, 8},
		} {
			if !bytes.Equal(d[z.off:z.off+z.n], make([]byte, z.n)) {
				t.Errorf("file %d %s is not zero", i, z.name)
			}
		}
	}
}

func TestEncodeFileNameTruncation(t *testing.T) {
	short := encodeFileName("abc")
	if len(short) != 4 || short[3] != 0 {
		t.Fatalf("encodeFileName(%q) = %v, want 3 units plus a NUL", "abc", short)
	}

	long := encodeFileName(strings.Repeat("x", 400))
	if len(long) != fdFileNameUnits {
		t.Fatalf("long name encoded to %d units, want %d (cFileName full, NUL included)",
			len(long), fdFileNameUnits)
	}
	if long[len(long)-1] != 0 {
		t.Fatalf("truncated name is not NUL-terminated")
	}

	// A name whose 259th unit would be the high half of a surrogate pair must
	// lose the whole pair, not half of it.
	name := strings.Repeat("x", fdFileNameUnits-2) + "\U0001F600" + "tail"
	u := encodeFileName(name)
	if u[len(u)-1] != 0 {
		t.Fatalf("not NUL-terminated")
	}
	for i, c := range u[:len(u)-1] {
		if c >= 0xD800 && c <= 0xDBFF {
			if i+1 >= len(u)-1 || u[i+1] < 0xDC00 || u[i+1] > 0xDFFF {
				t.Fatalf("unit %d is an unpaired high surrogate 0x%04X", i, c)
			}
		}
	}
}

func TestPatternDeterminism(t *testing.T) {
	const seed = 0x0123456789ABCDEF
	const n = 4096

	whole := make([]byte, n)
	patternAt(seed, 0, whole)

	// The same bytes must come out however the range is cut up, because Seek
	// relies on being able to resume anywhere.
	for _, chunk := range []int{1, 3, 7, 8, 64, 65, 1000} {
		got := make([]byte, n)
		for off := 0; off < n; off += chunk {
			end := off + chunk
			if end > n {
				end = n
			}
			patternAt(seed, int64(off), got[off:end])
		}
		if !bytes.Equal(got, whole) {
			t.Fatalf("chunked by %d differs from one pass", chunk)
		}
	}

	// And from a non-zero starting offset.
	tail := make([]byte, 300)
	patternAt(seed, 1000, tail)
	if !bytes.Equal(tail, whole[1000:1300]) {
		t.Fatalf("pattern at offset 1000 differs from the same range of a full pass")
	}

	// A different seed must give different bytes, or a swapped file would hash
	// the same as the one it replaced.
	other := make([]byte, n)
	patternAt(seed+1, 0, other)
	if bytes.Equal(other, whole) {
		t.Fatalf("two seeds produced identical bytes")
	}

	// Shape: newline in the last column, printable elsewhere.
	for i, b := range whole {
		if i%patternLine == patternLine-1 {
			if b != '\n' {
				t.Fatalf("byte %d is 0x%02X, want '\\n'", i, b)
			}
		} else if !strings.ContainsRune(patternAlphabet, rune(b)) {
			t.Fatalf("byte %d is 0x%02X, outside the alphabet", i, b)
		}
	}
}

// TestGeneratorGoldenDigests pins the generator against an independent
// implementation. The two digests below were produced by a separate Python
// transcription of the same rule -- SplitMix64's finalising mix over a
// golden-ratio-strided counter, rendered into 64-byte printable lines -- not by
// running this code. They matter because the SHA-256 the program prints at
// start is the only thing the user has to check a dropped file against: if the
// generator ever drifts, the printed digest drifts with it and the check
// silently becomes meaningless. A golden value from outside the package is what
// makes that impossible.
func TestGeneratorGoldenDigests(t *testing.T) {
	files := buildFileSet(fileSetConfig{count: 2, size: 5 << 30})
	small := files[1]
	if small.size != 1<<10 {
		t.Fatalf("the small file is %d bytes, the golden digest is for 1024", small.size)
	}
	if got := fileSHA256(&small); got != "650c501c8cb6afb60431755a694e66ec2d58785aa495d24e9f0c6cbb52d1e837" {
		t.Errorf("enfold-proto-small.txt sha256 = %s, golden says 650c501c...e837", got)
	}

	// The first MiB of the big file's seed, so the other seed is pinned too
	// without hashing 5 GiB.
	head := synthFile{name: "head", size: 1 << 20, seed: files[0].seed}
	if got := fileSHA256(&head); got != "155ded199974568dad3cc4ab619df9cc21714e18c19b7fe767e8323b5f296c37" {
		t.Errorf("first MiB of the big file sha256 = %s, golden says 155ded19...6c37", got)
	}
}

func TestProducerMatchesPattern(t *testing.T) {
	f := &synthFile{name: "t.bin", size: 1 << 20, seed: 99}
	p := newProducer(f, 0)
	defer p.close()

	got := make([]byte, f.size)
	read := 0
	buf := make([]byte, 3000) // deliberately not a divisor of the chunk size
	for read < len(got) {
		n := p.read(buf)
		if n == 0 {
			break
		}
		copy(got[read:], buf[:n])
		read += n
	}
	if read != len(got) {
		t.Fatalf("producer gave %d bytes, want %d", read, len(got))
	}
	want := make([]byte, f.size)
	patternAt(f.seed, 0, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("producer output differs from the generator")
	}
	if n := p.read(buf); n != 0 {
		t.Fatalf("producer gave %d bytes past the end of the file", n)
	}
}

func TestProducerStartsAtOffset(t *testing.T) {
	f := &synthFile{name: "t.bin", size: 1 << 16, seed: 7}
	const off = 12345
	p := newProducer(f, off)
	defer p.close()

	got := make([]byte, 5000)
	if n := p.read(got); n != len(got) {
		t.Fatalf("read %d bytes, want %d", n, len(got))
	}
	want := make([]byte, len(got))
	patternAt(f.seed, off, want)
	if !bytes.Equal(got, want) {
		t.Fatalf("producer started at the wrong offset")
	}
}

func TestBuildFileSet(t *testing.T) {
	def := buildFileSet(fileSetConfig{count: 2, size: 5 << 30})
	if len(def) != 2 {
		t.Fatalf("default set has %d files, want 2", len(def))
	}
	if def[0].name != "enfold-proto-5GiB.bin" || def[0].size != 5<<30 {
		t.Errorf("file 0 = %q/%d, want enfold-proto-5GiB.bin/%d", def[0].name, def[0].size, int64(5)<<30)
	}
	if def[1].name != "enfold-proto-small.txt" || def[1].size != 1<<10 {
		t.Errorf("file 1 = %q/%d, want enfold-proto-small.txt/1024", def[1].name, def[1].size)
	}
	if def[0].seed == def[1].seed {
		t.Errorf("both files share a seed, so they would have identical contents")
	}

	one := buildFileSet(fileSetConfig{count: 1, size: 1 << 20})
	if len(one) != 1 || one[0].name != "enfold-proto-1MiB.bin" {
		t.Errorf("-n 1 -size 1MiB gave %v", one)
	}

	sub := buildFileSet(fileSetConfig{count: 4, size: 1 << 20, folder: true})
	if len(sub) != 4 {
		t.Fatalf("-n 4 gave %d files", len(sub))
	}
	seeds := map[uint64]bool{}
	for _, f := range sub {
		if !strings.HasPrefix(f.name, protoFolder) {
			t.Errorf("-folder: %q has no %q prefix", f.name, protoFolder)
		}
		if seeds[f.seed] {
			t.Errorf("duplicate seed %#x", f.seed)
		}
		seeds[f.seed] = true
	}

	del := buildFileSet(fileSetConfig{count: 1, size: 1 << 20, delay: 5 * time.Millisecond})
	if del[0].delay != 5*time.Millisecond {
		t.Errorf("delay not carried into the file set")
	}
}

// ---------------------------------------------------------------------------
// -file: real files instead of synthetic ones.

// writeSource makes a file that stands in for the user's video: bytes that are
// not the generator's, under t.TempDir() and nowhere else.
func writeSource(t *testing.T, name string, size int) (path string, want []byte) {
	t.Helper()
	want = make([]byte, size)
	for i := range want {
		want[i] = byte(i*7 + 11)
	}
	path = filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}
	// A modification time in the past, so that "the copy carries the source's"
	// is a claim the test can tell from "the copy was made just now".
	old := time.Now().Add(-72 * time.Hour).Truncate(time.Second)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
	return path, want
}

// TestSourceFilesDescribesTheRealFile: the descriptor and the DROPFILES list
// take the source's own base name, its real size and its real modification
// time, because the whole point of -file is that what lands at the destination
// is indistinguishable from the original.
func TestSourceFilesDescribesTheRealFile(t *testing.T) {
	path, want := writeSource(t, "holiday.mp4", 4096)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	files, err := sourceFiles([]string{path}, false, 3*time.Millisecond)
	if err != nil {
		t.Fatalf("sourceFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("sourceFiles gave %d file(s), want 1", len(files))
	}
	f := files[0]
	if f.name != "holiday.mp4" {
		t.Errorf("the offered name is %q, want the source's base name", f.name)
	}
	if f.size != int64(len(want)) {
		t.Errorf("the offered size is %d, want %d", f.size, len(want))
	}
	if !f.modTime.Equal(info.ModTime()) {
		t.Errorf("the offered modification time is %s, want the source's %s", f.modTime, info.ModTime())
	}
	if f.source != path {
		t.Errorf("the source is recorded as %q, want %q", f.source, path)
	}
	if f.delay != 3*time.Millisecond {
		t.Errorf("-delay is not carried onto a -file entry")
	}

	// And the descriptor uses that time rather than the set's.
	blob := encodeFileGroupDescriptorW(files, time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	d := blob[fileGroupDescWHdr:]
	ft := uint64(binary.LittleEndian.Uint32(d[fdOffLastWriteTime+4:]))<<32 |
		uint64(binary.LittleEndian.Uint32(d[fdOffLastWriteTime:]))
	if wantFT := uint64(info.ModTime().UnixNano()/100) + 116444736000000000; ft != wantFT {
		t.Errorf("the descriptor's ftLastWriteTime is %d, want the source's %d", ft, wantFT)
	}

	// -folder still puts it under the relative folder, by name only.
	sub, err := sourceFiles([]string{path}, true, 0)
	if err != nil {
		t.Fatal(err)
	}
	if sub[0].name != protoFolder+"holiday.mp4" {
		t.Errorf("-folder -file gave the name %q", sub[0].name)
	}
}

// TestSourceFilesRefusesWhatItCannotOffer: every refusal happens before a window
// exists, because a drag that discovers this halfway has already handed a name
// to a target.
func TestSourceFilesRefusesWhatItCannotOffer(t *testing.T) {
	dir := t.TempDir()
	if _, err := sourceFiles([]string{filepath.Join(dir, "no-such-file.mp4")}, false, 0); err == nil {
		t.Error("a path that does not exist was accepted")
	} else if !strings.Contains(err.Error(), "-file ") {
		t.Errorf("the refusal does not name the flag: %v", err)
	}
	if _, err := sourceFiles([]string{dir}, false, 0); err == nil {
		t.Error("a directory was accepted as a file to offer")
	} else if !strings.Contains(err.Error(), "directory") {
		t.Errorf("the refusal does not say it is a directory: %v", err)
	}
	// One bad path among good ones refuses the whole run: a set that silently
	// dropped an entry would be measured as if it were complete.
	good, _ := writeSource(t, "good.bin", 16)
	if _, err := sourceFiles([]string{good, filepath.Join(dir, "missing.bin")}, false, 0); err == nil {
		t.Error("a set with one missing path was accepted")
	}
}

// TestProducerServesASourceFile: the virtual-file route reads the source rather
// than the generator, from whatever offset the stream is at.
func TestProducerServesASourceFile(t *testing.T) {
	path, want := writeSource(t, "clip.mp4", producerChunk+777)
	f := &synthFile{name: "clip.mp4", size: int64(len(want)), source: path}

	got := make([]byte, len(want))
	p := newProducer(f, 0)
	if n := p.read(got); n != len(want) {
		t.Fatalf("the producer served %d bytes, want %d", n, len(want))
	}
	p.close()
	if !bytes.Equal(got, want) {
		t.Fatal("the producer served bytes that are not the source's")
	}

	// From an offset, which is what a seek then a read does.
	const off = producerChunk + 13
	tail := make([]byte, len(want)-off)
	p = newProducer(f, off)
	if n := p.read(tail); n != len(tail) {
		t.Fatalf("the producer served %d bytes from offset %d, want %d", n, off, len(tail))
	}
	p.close()
	if !bytes.Equal(tail, want[off:]) {
		t.Fatal("the producer served the wrong bytes from an offset")
	}
}

func TestHumanSizeAndParseSize(t *testing.T) {
	for _, tc := range []struct {
		n    int64
		want string
	}{
		{5 << 30, "5GiB"},
		{1 << 20, "1MiB"},
		{1 << 10, "1KiB"},
		{1536, "1536B"}, // not a whole number of KiB, so say the bytes
		{3, "3B"},
	} {
		if got := humanSize(tc.n); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
	for _, tc := range []struct {
		in   string
		want int64
	}{
		{"5GiB", 5 << 30},
		{"256MiB", 256 << 20},
		{"64m", 64 << 20},
		{"1024", 1024},
	} {
		got, err := parseSize(tc.in)
		if err != nil || got != tc.want {
			t.Errorf("parseSize(%q) = %d, %v; want %d", tc.in, got, err, tc.want)
		}
	}
	for _, bad := range []string{"", "0", "-5", "banana", "5PiB"} {
		if _, err := parseSize(bad); err == nil {
			t.Errorf("parseSize(%q) accepted a bad size", bad)
		}
	}
}
