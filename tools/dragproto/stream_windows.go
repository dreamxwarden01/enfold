//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// One IStream per file per GetData call. Everything the interface has to get
// right lives in the Go core below -- read, seek, stat, clone -- so that it can
// be tested without COM; the callbacks under it are adapters that translate the
// core's answers into HRESULTs.

// hresultError carries the exact HRESULT a stream method must return, so a test
// can assert the code COM would see rather than a Go error string.
type hresultError uintptr

func (e hresultError) Error() string { return hrName(uintptr(e)) }

var streamSeq atomic.Int64

type fileStream struct {
	// file and id are fixed at creation; the file itself is never written. Only
	// that makes Stat and the log name answerable without the mutex, which is
	// what keeps a "how big is this?" from queueing behind a slow read.
	file *synthFile
	id   int64

	// Everything the stream is: one sequence of bytes and one seek pointer,
	// under one mutex. Two callers of one stream take turns; with -agile they
	// can be on different threads, and the mutex is what makes that the same
	// thing as taking turns on one.
	mu   sync.Mutex
	pos  int64
	prod *producer
	thr  readThrottle

	// statCalls is counted outside the mutex on purpose: Stat answers from the
	// file's immutable size and name, so it must not queue behind a Read that
	// is blocked on a slow producer. A target asking "how big is this?" on the
	// STA while a background thread is mid-read is exactly the case -delay is
	// there to provoke, and it must not be the thing that hangs the STA.
	statCalls atomic.Int64

	// reads and served mirror the throttle's counters, which live under the
	// mutex. The mirror exists so that the inventory a forced close prints can
	// say how far each stream got without queueing behind a Read that is
	// blocked on a slow producer -- the one moment the numbers are most wanted
	// is the one moment the mutex may be held for seconds.
	reads  atomic.Int64
	served atomic.Int64

	// revoked is set when the program is torn down while the target still holds
	// this stream. From then on every method answers STG_E_REVERTED instead of
	// touching state that is being dismantled; see revoke.
	revoked      atomic.Bool
	revokedCalls atomic.Int64
}

func newFileStream(f *synthFile, pos int64) *fileStream {
	s := &fileStream{file: f, id: streamSeq.Add(1), pos: pos}
	// A stream handed out after the transfer was torn down is born revoked: the
	// target asking for one more file after a forced close must get an answer,
	// not bytes.
	if transferRevoked.Load() {
		s.revoked.Store(true)
	}
	return s
}

// revoke makes every later call on this stream answer STG_E_REVERTED.
//
// It deliberately takes no lock. It is called from the window thread while it
// is tearing the program down, and the mutex it would want may be held for as
// long as a slow producer takes; a teardown that can block is not a teardown.
// Nothing is freed here either -- Go frees nothing that is still reachable --
// so a Read that is already inside the mutex finishes normally and it is only
// the calls after it that are refused.
func (s *fileStream) revoke() {
	if s.revoked.Swap(true) {
		return
	}
	logf("%s: revoked after %d read(s), %d bytes served; later calls answer STG_E_REVERTED",
		s.logName(), s.reads.Load(), s.served.Load())
}

// gone is the guard at the top of every stream method. The refusal is logged
// only a few times per stream: a target that keeps calling after a revoke would
// otherwise fill the log with the same line, and the first one has already said
// everything.
func (s *fileStream) gone(method string) bool {
	if !s.revoked.Load() {
		return false
	}
	if n := s.revokedCalls.Add(1); n <= 5 {
		logf("%s::%s -> STG_E_REVERTED (the source was torn down while the target still held this stream)",
			s.logName(), method)
	}
	return true
}

func (s *fileStream) logName() string {
	return fmt.Sprintf("IStream#%d(%s)", s.id, s.file.name)
}

// read fills dst from the current position, blocking until it is full or the
// end of the file is reached, and reports how many bytes it produced. A short
// count is EOF and nothing else: the generator cannot fail.
func (s *fileStream) read(dst []byte) int {
	if s.pos >= s.file.size || len(dst) == 0 {
		return 0
	}
	if want := s.file.size - s.pos; want < int64(len(dst)) {
		dst = dst[:want]
	}
	if s.prod == nil {
		s.prod = newProducer(s.file, s.pos)
	}
	n := s.prod.read(dst)
	s.pos += int64(n)
	return n
}

// seek implements IStream::Seek. STREAM_SEEK_SET reads dlibMove as unsigned per
// the documentation, so a value with the top bit set names an offset past 2^63
// rather than a negative one; no file here is that long, so it is refused
// instead of wrapping. Seeking past the end is explicitly not an error
// ("It is not, however, an error to seek past the end of the stream"); seeking
// before the beginning is.
func (s *fileStream) seek(move int64, origin uint32) (int64, error) {
	var base int64
	switch origin {
	case streamSeekSet:
		if move < 0 {
			return s.pos, hresultError(stgEInvalidFunction)
		}
		base = 0
	case streamSeekCur:
		base = s.pos
	case streamSeekEnd:
		base = s.file.size
	default:
		return s.pos, hresultError(stgEInvalidFunction)
	}
	np := base + move
	// base and move are both bounded well below 2^62 in practice, but a hostile
	// or buggy caller can hand over anything; catch the wrap rather than seek
	// somewhere arbitrary.
	if (move > 0 && np < base) || (move < 0 && np > base) {
		return s.pos, hresultError(stgEInvalidFunction)
	}
	if np < 0 {
		return s.pos, hresultError(stgEInvalidFunction)
	}
	if np != s.pos {
		// The producer is positional: it can only hand back the bytes that
		// follow where it started, so a jump retires it. The next read starts a
		// fresh one, which is also what makes a seek cheap.
		if s.prod != nil {
			s.prod.close()
			s.prod = nil
		}
		s.pos = np
	}
	return s.pos, nil
}

// statName is what Stat reports as the stream's name: the leaf, not the
// relative path in the descriptor. A consumer that used the descriptor's
// "Proto\x.bin" verbatim as a file name would produce something unnameable,
// and the stream is one file however the descriptor files it.
func (s *fileStream) statName() string {
	n := s.file.name
	if i := strings.LastIndexAny(n, `\/`); i >= 0 {
		n = n[i+1:]
	}
	return n
}

// clone returns a stream over the same bytes with its own seek pointer, set to
// this stream's current position, as IStream::Clone documents.
func (s *fileStream) clone() *fileStream {
	return newFileStream(s.file, s.pos)
}

func (s *fileStream) comDestroy() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.prod != nil {
		s.prod.close()
		s.prod = nil
	}
	logf("%s destroyed after %d reads, %d bytes, %d Stat calls, final pos %d",
		s.logName(), s.thr.calls, s.thr.total, s.statCalls.Load(), s.pos)
}

// ---------------------------------------------------------------------------
// The COM side.

func newStreamObject(f *synthFile, pos int64) *comObject {
	return newStreamCOMObject(newFileStream(f, pos))
}

// newStreamCOMObject wraps a stream in its COM object. Clone needs exactly what
// GetData needs, so there is one place that says what an IStream of this package
// is -- and one place that decides whether it is agile.
func newStreamCOMObject(s *fileStream) *comObject {
	return newTransferCOMObject(s.logName(), s, comIface{
		name: "IStream",
		vtbl: vtblIStream(),
		// ISequentialStream is IStream's base, and its methods are the first
		// two of IStream's own, so the same pointer answers both.
		iids: []windows.GUID{iidIStream, iidISequentialStream},
	})
}

func streamOf(this uintptr) (*fileStream, bool) {
	o, _, ok := comLookup(this)
	if !ok {
		return nil, false
	}
	s, ok := o.impl.(*fileStream)
	return s, ok
}

func streamRead(this uintptr, pv *byte, cb uintptr, pcbRead *uint32) uintptr {
	s, ok := streamOf(this)
	if !ok {
		logf("IStream::Read on unknown this=0x%X", this)
		return eUnexpected
	}
	n := uint32(cb)
	if pv == nil && n != 0 {
		return stgEInvalidPointer
	}
	if s.gone("Read") {
		// The count is set even on the refusal: a caller that reads it anyway
		// must not find whatever was in it before.
		if pcbRead != nil {
			*pcbRead = 0
		}
		return stgEReverted
	}
	// The mutex is held across s.read, which blocks on the producer -- so under
	// -delay a Read holds it for as long as the producer takes. That is the
	// design (the file is one sequence of bytes and two concurrent readers of
	// one stream would interleave), but it means a stall here is this
	// prototype's stall, not the shell's; see "what -delay measures" in the
	// README before reading a hang as a finding about Explorer.
	s.mu.Lock()
	start := s.pos
	got := 0
	if n > 0 {
		got = s.read(unsafe.Slice(pv, int(n)))
	}
	log, calls, total := s.thr.want(int64(got))
	s.mu.Unlock()
	// The lock-free mirror of the two numbers above, for the inventory a forced
	// close prints; see the fields. Add rather than Store, so that two threads
	// leaving the mutex in either order still leave the totals right.
	s.reads.Add(1)
	s.served.Add(int64(got))

	// pcbRead is optional on IStream::Read, but the byte count is the only way
	// a caller learns a short read, so set it whenever it is there -- always,
	// including on the zero-byte read at EOF.
	if pcbRead != nil {
		*pcbRead = uint32(got)
	}
	hr := uintptr(sOK)
	if uint32(got) < n {
		// "The value returned in pcbRead is less than the number of bytes
		// requested in cb. This indicates the end of the stream has been
		// reached."
		hr = sFALSE
	}
	if log {
		logf("%s::Read(cb=%d) at pos=%d -> %d bytes, %s [call %d, %d bytes total]",
			s.logName(), n, start, got, hrName(hr), calls, total)
	}
	return hr
}

// streamWrite refuses: the stream is a view of a file being handed out, opened
// STGM_READ, and STG_E_ACCESSDENIED is what the documentation gives for
// "the caller does not have enough permissions".
func streamWrite(this uintptr, pv *byte, cb uintptr, pcbWritten *uint32) uintptr {
	if pcbWritten != nil {
		*pcbWritten = 0
	}
	if s, ok := streamOf(this); ok {
		if s.gone("Write") {
			return stgEReverted
		}
		logf("%s::Write(cb=%d) -> STG_E_ACCESSDENIED (read-only)", s.logName(), uint32(cb))
	}
	return stgEAccessDenied
}

func streamSeek(this uintptr, dlibMove uintptr, dwOrigin uintptr, plibNewPosition *uint64) uintptr {
	s, ok := streamOf(this)
	if !ok {
		return eUnexpected
	}
	if s.gone("Seek") {
		return stgEReverted
	}
	move := int64(dlibMove)
	origin := uint32(dwOrigin)
	s.mu.Lock()
	np, err := s.seek(move, origin)
	s.mu.Unlock()
	if err != nil {
		logf("%s::Seek(%d, %s) -> %s", s.logName(), move, seekOriginName(origin),
			hrName(uintptr(err.(hresultError))))
		return uintptr(err.(hresultError))
	}
	if plibNewPosition != nil {
		*plibNewPosition = uint64(np)
	}
	logf("%s::Seek(%d, %s) -> pos %d", s.logName(), move, seekOriginName(origin), np)
	return sOK
}

func seekOriginName(o uint32) string {
	switch o {
	case streamSeekSet:
		return "STREAM_SEEK_SET"
	case streamSeekCur:
		return "STREAM_SEEK_CUR"
	case streamSeekEnd:
		return "STREAM_SEEK_END"
	}
	return fmt.Sprintf("origin %d", o)
}

func streamSetSize(this uintptr, libNewSize uintptr) uintptr {
	if s, ok := streamOf(this); ok {
		if s.gone("SetSize") {
			return stgEReverted
		}
		logf("%s::SetSize(%d) -> STG_E_ACCESSDENIED (read-only)", s.logName(), uint64(libNewSize))
	}
	return stgEAccessDenied
}

// iStreamWriteSlot is IStream::Write's index in IStreamVtbl: QueryInterface,
// AddRef, Release, Read, Write. CopyTo calls out through it.
const iStreamWriteSlot = 4

// streamCopyTo is implemented rather than refused because it is a read: a
// consumer that uses it instead of Read must still get the bytes. pstm is an
// IStream*, which in memory is a pointer to a cell holding the vtable address,
// so **uintptr reaches the vtable without ever reinterpreting an integer as a
// pointer.
func streamCopyTo(this uintptr, pstm **uintptr, cb uintptr, pcbRead *uint64, pcbWritten *uint64) uintptr {
	s, ok := streamOf(this)
	if !ok {
		return eUnexpected
	}
	if pcbRead != nil {
		*pcbRead = 0
	}
	if pcbWritten != nil {
		*pcbWritten = 0
	}
	if pstm == nil {
		return stgEInvalidPointer
	}
	if s.gone("CopyTo") {
		return stgEReverted
	}
	want := uint64(cb)
	logf("%s::CopyTo(cb=%d) begins", s.logName(), want)

	vtbl := unsafe.Slice(*pstm, iStreamWriteSlot+1)
	writeFn := vtbl[iStreamWriteSlot]
	dstThis := uintptr(unsafe.Pointer(pstm))

	// The mutex is taken and given back per chunk rather than held across the
	// whole copy, because the Write below leaves this package: holding a lock
	// across an outbound COM call is how deadlocks are made, and a destination
	// whose Write called back into this stream would find it locked by itself.
	// The price is that two concurrent CopyTo calls on one stream would
	// interleave their chunks, which -agile makes possible where it was not
	// before. Nothing observed does it -- Explorer reads, it does not CopyTo --
	// and a deadlock would be worse than an interleave, so this is recorded
	// rather than locked away.
	buf := make([]byte, producerChunk)
	var read, wrote uint64
	hr := uintptr(sOK)
	for read < want {
		n := len(buf)
		if r := want - read; r < uint64(n) {
			n = int(r)
		}
		s.mu.Lock()
		got := s.read(buf[:n])
		s.mu.Unlock()
		if got == 0 {
			break
		}
		read += uint64(got)
		// put is the destination's pcbWritten. It goes out through
		// syscallPinned rather than syscall.SyscallN so that it is on the heap
		// for the duration of the call: the destination's Write is very likely
		// another Go callback, a callback can grow and so move the stack, and a
		// moved `put` would be written by an address nothing reads any more.
		// See syscallPinned; this is not theoretical, it reproduces.
		var put uint32
		r, _, _ := syscallPinned(writeFn, dstThis,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(got), uintptr(unsafe.Pointer(&put)))
		wrote += uint64(put)
		if uint32(r) != sOK || int(put) != got {
			hr = uintptr(uint32(r))
			if hr == sOK {
				hr = stgEMediumFull
			}
			break
		}
	}
	if pcbRead != nil {
		*pcbRead = read
	}
	if pcbWritten != nil {
		*pcbWritten = wrote
	}
	logf("%s::CopyTo -> read %d, wrote %d, %s", s.logName(), read, wrote, hrName(hr))
	return hr
}

// streamCommit is a flush. "If the stream object is open in direct mode,
// IStream::Commit has no effect other than flushing all memory buffers"; there
// are no buffers to flush on the way out of a read-only stream, so S_OK is the
// whole of it.
func streamCommit(this uintptr, grfCommitFlags uintptr) uintptr {
	if s, ok := streamOf(this); ok {
		if s.gone("Commit") {
			return stgEReverted
		}
		logf("%s::Commit(0x%X) -> S_OK (nothing buffered)", s.logName(), uint32(grfCommitFlags))
	}
	return sOK
}

// streamRevert: "On streams open in direct mode ... this method has no effect."
// The docs give it no failure code for a non-transacted stream, so S_OK.
func streamRevert(this uintptr) uintptr {
	if s, ok := streamOf(this); ok {
		if s.gone("Revert") {
			return stgEReverted
		}
		logf("%s::Revert -> S_OK (not transacted)", s.logName())
	}
	return sOK
}

// streamLockRegion / streamUnlockRegion: range locking is optional, and
// "The STG_E_INVALIDFUNCTION error is returned if the requested type of locking
// is not supported." That is the documented way to say no -- not E_NOTIMPL,
// which callers are not told to expect here.
func streamLockRegion(this uintptr, libOffset uintptr, cb uintptr, dwLockType uintptr) uintptr {
	if s, ok := streamOf(this); ok {
		if s.gone("LockRegion") {
			return stgEReverted
		}
		logf("%s::LockRegion(off=%d, cb=%d, type=%d) -> STG_E_INVALIDFUNCTION",
			s.logName(), uint64(libOffset), uint64(cb), uint32(dwLockType))
	}
	return stgEInvalidFunction
}

func streamUnlockRegion(this uintptr, libOffset uintptr, cb uintptr, dwLockType uintptr) uintptr {
	if s, ok := streamOf(this); ok {
		if s.gone("UnlockRegion") {
			return stgEReverted
		}
		logf("%s::UnlockRegion(off=%d, cb=%d, type=%d) -> STG_E_INVALIDFUNCTION",
			s.logName(), uint64(libOffset), uint64(cb), uint32(dwLockType))
	}
	return stgEInvalidFunction
}

func streamStat(this uintptr, pstatstg *statStg, grfStatFlag uintptr) uintptr {
	s, ok := streamOf(this)
	if !ok {
		return eUnexpected
	}
	if pstatstg == nil {
		return stgEInvalidPointer
	}
	if s.gone("Stat") {
		return stgEReverted
	}
	flag := uint32(grfStatFlag)
	if flag != statFlagDefault && flag != statFlagNoName && flag != statFlagNoOpen {
		logf("%s::Stat(grfStatFlag=%d) -> STG_E_INVALIDFLAG", s.logName(), flag)
		return stgEInvalidFlag
	}
	s.statCalls.Add(1)
	size := s.file.size
	name := s.statName()

	*pstatstg = statStg{}
	pstatstg.typ = stgTyStream
	pstatstg.cbSize = uint64(size)
	pstatstg.grfMode = stgmRead
	if flag != statFlagNoName {
		// pwcsName is the caller's to CoTaskMemFree, so it has to come from
		// CoTaskMemAlloc and nowhere else.
		u, err := windows.UTF16FromString(name)
		if err == nil {
			nbytes := uintptr(len(u) * 2)
			p := coTaskMemAlloc(nbytes)
			if p == 0 {
				return stgEMediumFull
			}
			copyIntoNative(p, unsafe.Slice((*byte)(unsafe.Pointer(&u[0])), int(nbytes)))
			pstatstg.pwcsName = p
		}
	}
	logf("%s::Stat(flag=%d) -> cbSize=%d, name=%q", s.logName(), flag, size, name)
	return sOK
}

func streamClone(this uintptr, ppstm *uintptr) uintptr {
	s, ok := streamOf(this)
	if !ok {
		return eUnexpected
	}
	if ppstm == nil {
		return stgEInvalidPointer
	}
	*ppstm = 0
	if s.gone("Clone") {
		return stgEReverted
	}
	s.mu.Lock()
	c := s.clone()
	s.mu.Unlock()
	o := newStreamCOMObject(c)
	*ppstm = o.unknown()
	logf("%s::Clone -> %s at pos %d", s.logName(), c.logName(), c.pos)
	return sOK
}

// The vtable is built on first use, inside the Once, rather than held in a
// package variable: Clone creates another stream, which needs the vtable, and a
// variable would make that an initialisation cycle.
var (
	streamVtblOnce sync.Once
	streamVtblAddr uintptr
)

func vtblIStream() uintptr {
	streamVtblOnce.Do(func() {
		streamVtblAddr = pinVtbl(iStreamVtbl{
			QueryInterface: cbQueryInterface,
			AddRef:         cbAddRef,
			Release:        cbRelease,
			Read:           syscall.NewCallback(streamRead),
			Write:          syscall.NewCallback(streamWrite),
			Seek:           syscall.NewCallback(streamSeek),
			SetSize:        syscall.NewCallback(streamSetSize),
			CopyTo:         syscall.NewCallback(streamCopyTo),
			Commit:         syscall.NewCallback(streamCommit),
			Revert:         syscall.NewCallback(streamRevert),
			LockRegion:     syscall.NewCallback(streamLockRegion),
			UnlockRegion:   syscall.NewCallback(streamUnlockRegion),
			Stat:           syscall.NewCallback(streamStat),
			Clone:          syscall.NewCallback(streamClone),
		})
	})
	return streamVtblAddr
}
