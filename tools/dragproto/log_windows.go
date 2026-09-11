//go:build windows

package main

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/sys/windows"
)

// The log is the whole point of the prototype: it is the only way to see which
// calls Explorer actually makes, in what order, and on which thread. The thread
// id is on every line because the question the docs pose -- whether a slow read
// runs on the STA or on a background extraction thread -- is answered by
// nothing else.

var (
	logMu    sync.Mutex
	logStart = time.Now()
	// logWriter is stderr for the real run and io.Discard under test, where the
	// COM traffic would bury the test output it is meant to accompany.
	logWriter io.Writer = os.Stderr
)

func logf(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	logMu.Lock()
	defer logMu.Unlock()
	fmt.Fprintf(logWriter, "%9.3fs [tid %5d] %s\n",
		time.Since(logStart).Seconds(), windows.GetCurrentThreadId(), msg)
}

// hrName renders an HRESULT the way the SDK names it, so that a log line can be
// grepped for a symbol rather than a hex number.
func hrName(hr uintptr) string {
	v := uint32(hr)
	names := map[uint32]string{
		sOK:                        "S_OK",
		sFALSE:                     "S_FALSE",
		eNotImpl:                   "E_NOTIMPL",
		eNoInterface:               "E_NOINTERFACE",
		ePointer:                   "E_POINTER",
		eFail:                      "E_FAIL",
		eUnexpected:                "E_UNEXPECTED",
		eInvalidArg:                "E_INVALIDARG",
		eOutOfMemory:               "E_OUTOFMEMORY",
		dvEFormatEtc:               "DV_E_FORMATETC",
		dvELIndex:                  "DV_E_LINDEX",
		dvETymed:                   "DV_E_TYMED",
		dvEDvAspect:                "DV_E_DVASPECT",
		dataSSameFormatEtc:         "DATA_S_SAMEFORMATETC",
		oleEAdviseNotSupp:          "OLE_E_ADVISENOTSUPPORTED",
		stgEInvalidFunction:        "STG_E_INVALIDFUNCTION",
		stgEAccessDenied:           "STG_E_ACCESSDENIED",
		stgEInvalidPointer:         "STG_E_INVALIDPOINTER",
		stgEMediumFull:             "STG_E_MEDIUMFULL",
		stgEInvalidFlag:            "STG_E_INVALIDFLAG",
		stgEReverted:               "STG_E_REVERTED",
		dragDropSDrop:              "DRAGDROP_S_DROP",
		dragDropSCancel:            "DRAGDROP_S_CANCEL",
		dragDropSUseDefaultCursors: "DRAGDROP_S_USEDEFAULTCURSORS",
	}
	if n, ok := names[v]; ok {
		return fmt.Sprintf("%s (0x%08X)", n, v)
	}
	return fmt.Sprintf("0x%08X", v)
}

// effectName renders a DROPEFFECT mask.
func effectName(e uint32) string {
	if e == 0 {
		return "DROPEFFECT_NONE"
	}
	s := ""
	add := func(bit uint32, name string) {
		if e&bit != 0 {
			if s != "" {
				s += "|"
			}
			s += name
		}
	}
	add(dropEffectCopy, "DROPEFFECT_COPY")
	add(dropEffectMove, "DROPEFFECT_MOVE")
	add(dropEffectLink, "DROPEFFECT_LINK")
	add(0x80000000, "DROPEFFECT_SCROLL")
	if s == "" {
		s = fmt.Sprintf("0x%08X", e)
	}
	return s
}

// tymedName renders a TYMED mask; GetData callers routinely OR several together.
func tymedName(t uint32) string {
	s := ""
	add := func(bit uint32, name string) {
		if t&bit != 0 {
			if s != "" {
				s += "|"
			}
			s += name
		}
	}
	add(1, "TYMED_HGLOBAL")
	add(2, "TYMED_FILE")
	add(4, "TYMED_ISTREAM")
	add(8, "TYMED_ISTORAGE")
	add(16, "TYMED_GDI")
	add(32, "TYMED_MFPICT")
	add(64, "TYMED_ENHMF")
	if s == "" {
		if t == 0 {
			return "TYMED_NULL"
		}
		s = fmt.Sprintf("0x%08X", t)
	}
	return s
}

// readThrottle keeps one stream's Read logging from drowning the log: the first
// readThrottleFirst calls are printed in full, after which one line per
// readThrottleBytes of progress. A 5 GiB file read in 64 KiB chunks is 81920
// calls; the shape of the traffic is visible from ten of them plus a heartbeat.
const (
	readThrottleFirst = 10
	readThrottleBytes = 64 << 20
)

type readThrottle struct {
	calls    int64
	total    int64
	nextMark int64
}

// want reports whether this Read should be logged, and returns the running call
// and byte totals to put in the line.
func (t *readThrottle) want(n int64) (log bool, calls, total int64) {
	t.calls++
	t.total += n
	switch {
	case t.calls <= readThrottleFirst:
		log = true
	case t.total >= t.nextMark:
		log = true
	}
	if log {
		for t.nextMark <= t.total {
			t.nextMark += readThrottleBytes
		}
	}
	return log, t.calls, t.total
}
