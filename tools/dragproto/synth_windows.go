//go:build windows

package main

import (
	"encoding/binary"
	"fmt"
	"sync"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// The virtual files are synthetic, and deliberately so: the prototype is here
// to answer questions about Explorer and about COM, not about the archive. The
// bytes come from a counter-based pattern rather than a buffer, so a 5 GiB file
// costs nothing to offer and the receiver can still be checked -- the same
// generator, streamed through SHA-256, says what the dropped file must hash to.

// synthFile is one virtual file in the drag.
type synthFile struct {
	// name is what goes into FILEDESCRIPTORW.cFileName, verbatim.
	name string
	size int64
	seed uint64
	// delay is how long the producer takes per MiB, to stand in for a slow
	// decrypt. It runs on the producer's goroutine, never on the caller's.
	delay time.Duration
}

// The pattern is printable, 64-byte lines ending in '\n', so that the small
// file really is a text file when it lands and the big one can be eyeballed in
// any editor. Every byte is a pure function of its own offset, which is what
// makes Seek free and a second pass over the same range reproducible.
const (
	patternLine     = 64
	patternAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
)

// patternBlock returns the eight raw bytes, as a word, that back file offsets
// [8*block, 8*block+8). It is SplitMix64's finalising mix over a
// golden-ratio-strided counter: cheap, and good enough that a truncated or
// duplicated range shows up in the hash.
func patternBlock(seed, block uint64) uint64 {
	z := seed + (block+1)*0x9E3779B97F4A7C15
	z = (z ^ (z >> 30)) * 0xBF58476D1CE4E5B9
	z = (z ^ (z >> 27)) * 0x94D049BB133111EB
	return z ^ (z >> 31)
}

// patternAt fills dst with the file's bytes starting at absolute offset off.
// Random access is the point: Seek can jump anywhere without generating what
// came before.
func patternAt(seed uint64, off int64, dst []byte) {
	if off < 0 {
		panic("dragproto: negative pattern offset")
	}
	var word [8]byte
	pos := 0
	cur := off
	for pos < len(dst) {
		binary.LittleEndian.PutUint64(word[:], patternBlock(seed, uint64(cur/8)))
		n := copy(dst[pos:], word[cur%8:])
		pos += n
		cur += int64(n)
	}
	for i := range dst {
		if (off+int64(i))%patternLine == patternLine-1 {
			dst[i] = '\n'
		} else {
			dst[i] = patternAlphabet[dst[i]&0x3F]
		}
	}
}

// ---------------------------------------------------------------------------
// The producer.

const (
	// A chunk plus a queue of four is the whole per-stream budget: about
	// 1.25 MiB in flight however large the file is. Task Manager is the test.
	producerChunk = 256 << 10
	producerDepth = 4
)

// producer generates a file's bytes ahead of the reader on a goroutine of its
// own and hands them over through a bounded channel. This is the shape APP.md
// §3 asks of the real thing -- "decryption runs on a goroutine of its own into
// a bounded buffer the stream's Read drains" -- and it is what lets -delay
// simulate a slow producer without the delay ever running on the STA. Read
// itself only copies out of the queue, so a Read never computes more than it
// returns.
type producer struct {
	file *synthFile
	out  chan []byte
	stop chan struct{}
	once sync.Once
	cur  []byte
	// base is the offset the first byte the producer ever emits corresponds to;
	// kept for the log and for the invariant check in the stream.
	base int64
}

func newProducer(f *synthFile, off int64) *producer {
	p := &producer{
		file: f,
		out:  make(chan []byte, producerDepth),
		stop: make(chan struct{}),
		base: off,
	}
	go p.run(off)
	return p
}

func (p *producer) run(off int64) {
	defer close(p.out)
	for off < p.file.size {
		// Check for a stop before generating anything. Relying on the selects
		// below alone would work, but only probabilistically: with a drain
		// running, a select whose send and stop cases are both ready picks
		// either, so a retired producer could generate another chunk or two
		// before noticing. A seek should cost nothing.
		select {
		case <-p.stop:
			return
		default:
		}
		n := int64(producerChunk)
		if r := p.file.size - off; r < n {
			n = r
		}
		buf := make([]byte, n)
		patternAt(p.file.seed, off, buf)
		if p.file.delay > 0 {
			d := time.Duration(float64(p.file.delay) * float64(n) / float64(1<<20))
			select {
			case <-time.After(d):
			case <-p.stop:
				return
			}
		}
		select {
		case p.out <- buf:
		case <-p.stop:
			return
		}
		off += n
	}
}

// read drains up to len(dst) bytes, blocking until the buffer is full or the
// file is exhausted. It never generates anything itself.
func (p *producer) read(dst []byte) int {
	n := 0
	for n < len(dst) {
		if len(p.cur) == 0 {
			buf, ok := <-p.out
			if !ok {
				break
			}
			p.cur = buf
		}
		k := copy(dst[n:], p.cur)
		p.cur = p.cur[k:]
		n += k
	}
	return n
}

// close stops the goroutine and drops whatever it had queued.
func (p *producer) close() {
	p.once.Do(func() {
		close(p.stop)
		p.cur = nil
		// Drain so the goroutine's pending send, if any, can complete and the
		// goroutine can see stop and return.
		go func() {
			for range p.out {
			}
		}()
	})
}

// ---------------------------------------------------------------------------
// FILEGROUPDESCRIPTORW.
//
// Offsets and sizes below were not recalled: they were printed by compiling
// offsetof() against shlobj_core.h from Windows SDK 10.0.26100.0 for x64.
// FILEDESCRIPTORW is 592 bytes there, and FILEGROUPDESCRIPTORW is a UINT count
// followed by the descriptors back to back.

const (
	fileDescriptorWSize = 592
	fileGroupDescWHdr   = 4 // UINT cItems

	fdOffFlags          = 0
	fdOffCLSID          = 4
	fdOffSizeL          = 20
	fdOffPointL         = 28
	fdOffFileAttributes = 36
	fdOffCreationTime   = 40
	fdOffLastAccessTime = 48
	fdOffLastWriteTime  = 56
	fdOffFileSizeHigh   = 64
	fdOffFileSizeLow    = 68
	fdOffFileName       = 72

	// cFileName is WCHAR[MAX_PATH]; the last unit is reserved for the NUL.
	fdFileNameUnits = 260
)

// descriptorFlags is what the prototype claims is valid. FD_FILESIZE so the
// copy dialog has a total to count towards -- without it Explorer only learns
// each file's length as it reads it, and the progress bar has nothing to draw
// -- FD_WRITESTIME for a plausible timestamp, FD_ATTRIBUTES because
// dwFileAttributes is filled, and FD_PROGRESSUI to ask for the progress UI at
// all.
//
// FD_UNICODE is set too. It is arguably redundant -- the format is registered
// as "FileGroupDescriptorW", which already says which descriptor this is -- but
// the flag is what the structure itself documents as saying so ("Windows Vista
// and later. The descriptor is Unicode"), shipping sources set it, and a flag
// that says something true about the bytes costs nothing. Being the odd source
// out is not a thing to discover halfway through a drag test.
const descriptorFlags = fdAttributes | fdFileSize | fdWritesTime | fdProgressUI | fdUnicode

// encodeFileGroupDescriptorW lays out FILEGROUPDESCRIPTORW followed by one
// FILEDESCRIPTORW per file. write is the last-write time every descriptor
// claims; it is a parameter rather than time.Now so that a test can pin it.
func encodeFileGroupDescriptorW(files []synthFile, write time.Time) []byte {
	buf := make([]byte, fileGroupDescWHdr+fileDescriptorWSize*len(files))
	binary.LittleEndian.PutUint32(buf[0:], uint32(len(files)))
	ft := windows.NsecToFiletime(write.UnixNano())
	for i, f := range files {
		d := buf[fileGroupDescWHdr+i*fileDescriptorWSize:][:fileDescriptorWSize]
		binary.LittleEndian.PutUint32(d[fdOffFlags:], descriptorFlags)
		binary.LittleEndian.PutUint32(d[fdOffFileAttributes:], fileAttributeNormal)
		binary.LittleEndian.PutUint32(d[fdOffLastWriteTime:], ft.LowDateTime)
		binary.LittleEndian.PutUint32(d[fdOffLastWriteTime+4:], ft.HighDateTime)
		binary.LittleEndian.PutUint32(d[fdOffFileSizeHigh:], uint32(uint64(f.size)>>32))
		binary.LittleEndian.PutUint32(d[fdOffFileSizeLow:], uint32(uint64(f.size)))
		for j, u := range encodeFileName(f.name) {
			binary.LittleEndian.PutUint16(d[fdOffFileName+2*j:], u)
		}
	}
	return buf
}

// encodeFileName renders a name as NUL-terminated UTF-16 that fits cFileName.
// Truncation stops short of splitting a surrogate pair, because half a pair is
// not a character and Explorer would render it as a replacement glyph in the
// very dialog the drag test is about.
func encodeFileName(name string) []uint16 {
	u := utf16.Encode([]rune(name))
	if len(u) > fdFileNameUnits-1 {
		u = u[:fdFileNameUnits-1]
		if n := len(u); n > 0 && u[n-1] >= 0xD800 && u[n-1] <= 0xDBFF {
			u = u[:n-1]
		}
	}
	return append(u, 0)
}

// ---------------------------------------------------------------------------
// The file set.

// fileSetConfig is the -n / -size / -delay / -folder flags, gathered so that
// buildFileSet can be tested without a command line.
type fileSetConfig struct {
	count  int
	size   int64
	delay  time.Duration
	folder bool
}

// protoFolder is the relative folder -folder puts the files under. Whether
// Explorer honours a relative path in cFileName at all is one of the things the
// prototype is for: the documentation for FILEDESCRIPTORW says only "the
// null-terminated string that contains the name of the file", and says nothing
// about path separators either way.
const protoFolder = `Proto\`

// buildFileSet turns the flags into the files the drag will offer. File 0 is
// always the big one, because that is the case the design is unsure about;
// file 1 is the small text file, so that a drop can be checked in a second; any
// further files are 1 MiB fillers that exist only to make the descriptor hold
// more than two entries.
func buildFileSet(c fileSetConfig) []synthFile {
	if c.count < 1 {
		c.count = 1
	}
	prefix := ""
	if c.folder {
		prefix = protoFolder
	}
	files := make([]synthFile, 0, c.count)
	files = append(files, synthFile{
		name:  prefix + "enfold-proto-" + humanSize(c.size) + ".bin",
		size:  c.size,
		seed:  0x656E666F6C640001,
		delay: c.delay,
	})
	if c.count >= 2 {
		files = append(files, synthFile{
			name:  prefix + "enfold-proto-small.txt",
			size:  1 << 10,
			seed:  0x656E666F6C640002,
			delay: c.delay,
		})
	}
	for i := 2; i < c.count; i++ {
		files = append(files, synthFile{
			name:  fmt.Sprintf("%senfold-proto-extra-%d.bin", prefix, i-1),
			size:  1 << 20,
			seed:  0x656E666F6C640002 + uint64(i),
			delay: c.delay,
		})
	}
	return files
}

// humanSize renders a byte count the way the default file is named:
// 5368709120 becomes "5GiB". Sizes that are not a whole number of units keep
// their exact byte count, so a name never claims a size the file does not have.
func humanSize(n int64) string {
	units := []struct {
		scale int64
		name  string
	}{
		{1 << 40, "TiB"},
		{1 << 30, "GiB"},
		{1 << 20, "MiB"},
		{1 << 10, "KiB"},
	}
	for _, u := range units {
		if n >= u.scale && n%u.scale == 0 {
			return fmt.Sprintf("%d%s", n/u.scale, u.name)
		}
	}
	return fmt.Sprintf("%dB", n)
}
