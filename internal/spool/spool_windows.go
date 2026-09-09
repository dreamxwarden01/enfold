//go:build windows

package spool

import (
	"errors"
	"fmt"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The spooler API, from winspool.drv. Only the four calls a read needs are
// bound, and none of them submits anything.
var (
	winspool         = windows.NewLazySystemDLL("winspool.drv")
	procEnumPrinters = winspool.NewProc("EnumPrintersW")
	procOpenPrinter  = winspool.NewProc("OpenPrinterW")
	procEnumJobs     = winspool.NewProc("EnumJobsW")
	procClosePrinter = winspool.NewProc("ClosePrinter")
)

// EnumPrinters flags (winspool.h): the printers installed on this machine
// and the network printers this user is connected to — between them, every
// queue a print from the WebView can reach, Microsoft Print to PDF included.
const (
	printerEnumLocal       = 0x00000002
	printerEnumConnections = 0x00000004
)

// printerInfo4 is PRINTER_INFO_4W: the level that lists names without
// asking the spooler for a queue's whole state, which is all a snapshot
// needs.
type printerInfo4 struct {
	PrinterName *uint16
	ServerName  *uint16
	Attributes  uint32
}

// jobInfo1 is JOB_INFO_1W. Only JobID is read; the rest is declared so that
// the record's size and the fields' offsets are the spooler's own.
type jobInfo1 struct {
	JobID        uint32
	PrinterName  *uint16
	MachineName  *uint16
	UserName     *uint16
	Document     *uint16
	Datatype     *uint16
	Status       *uint16
	StatusCode   uint32
	Priority     uint32
	Position     uint32
	TotalPages   uint32
	PagesPrinted uint32
	Submitted    windows.Systemtime
}

// maxJobs bounds one EnumJobs call. A queue longer than this would only
// hide older jobs, never a new one — the answer is about ids that were not
// there before.
const maxJobs = 4096

// wordSize is the alignment every buffer these calls write into is given:
// the fixed-size records hold pointers, so a byte slice would not do.
const wordSize = int(unsafe.Sizeof(uintptr(0)))

// buffer allocates n bytes aligned for a pointer-holding record.
func buffer(n uint32) []uintptr {
	if n == 0 {
		return nil
	}
	return make([]uintptr, (int(n)+wordSize-1)/wordSize)
}

// snapshot lists every job of every local and connected printer. A printer
// that will not open is skipped — one queue nobody may read must not stop
// the answer — but a machine whose printers all refuse, or whose spooler
// will not enumerate at all, is an error: the page then asks (APP.md §6).
func snapshot() (Set, error) {
	// LazyProc.Call resolves through mustFind, which panics when the DLL or
	// the entry point is not there — and the call arrives on the bound
	// goroutine of Shell.PrintBegin / PrintEnd, which has no recover. The
	// package's contract is that an unreadable spooler is an error the page
	// falls back on, never a crash, so every entry point is resolved first.
	for _, p := range []*windows.LazyProc{procEnumPrinters, procOpenPrinter, procEnumJobs, procClosePrinter} {
		if err := p.Find(); err != nil {
			return nil, fmt.Errorf("%w: %v", ErrUnsupported, err)
		}
	}
	printers, err := enumPrinters()
	if err != nil {
		return nil, err
	}
	out := Set{}
	var firstErr error
	opened := 0
	for _, name := range printers {
		ids, err := printerJobs(name)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		opened++
		for _, id := range ids {
			out[Job{Printer: name, ID: id}] = struct{}{}
		}
	}
	if opened == 0 && len(printers) > 0 {
		return nil, firstErr
	}
	return out, nil
}

// enumPrinters is the two-call pattern every one of these takes: ask with no
// buffer, be told the size, ask again.
func enumPrinters() ([]string, error) {
	var needed, returned uint32
	call := func(buf []uintptr, size uint32) (uintptr, error) {
		var p uintptr
		if len(buf) > 0 {
			p = uintptr(unsafe.Pointer(&buf[0]))
		}
		r, _, err := procEnumPrinters.Call(
			uintptr(printerEnumLocal|printerEnumConnections), 0, 4,
			p, uintptr(size), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)),
		)
		return r, err
	}
	if r, err := call(nil, 0); r == 0 {
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			return nil, fmt.Errorf("spool: EnumPrinters: %w", err)
		}
	}
	if needed == 0 {
		return nil, nil // no printers at all
	}
	buf := buffer(needed)
	if r, err := call(buf, needed); r == 0 {
		return nil, fmt.Errorf("spool: EnumPrinters: %w", err)
	}
	if returned == 0 {
		return nil, nil
	}
	recs := unsafe.Slice((*printerInfo4)(unsafe.Pointer(&buf[0])), int(returned))
	names := make([]string, 0, len(recs))
	for i := range recs {
		if recs[i].PrinterName != nil {
			names = append(names, windows.UTF16PtrToString(recs[i].PrinterName))
		}
	}
	runtime.KeepAlive(buf)
	return names, nil
}

// printerJobs is the ids standing in one printer's queue.
func printerJobs(name string) ([]uint32, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	var h windows.Handle
	if r, _, err := procOpenPrinter.Call(uintptr(unsafe.Pointer(p)), uintptr(unsafe.Pointer(&h)), 0); r == 0 {
		return nil, fmt.Errorf("spool: OpenPrinter %q: %w", name, err)
	}
	defer procClosePrinter.Call(uintptr(h))
	var needed, returned uint32
	call := func(buf []uintptr, size uint32) (uintptr, error) {
		var b uintptr
		if len(buf) > 0 {
			b = uintptr(unsafe.Pointer(&buf[0]))
		}
		r, _, err := procEnumJobs.Call(
			uintptr(h), 0, maxJobs, 1,
			b, uintptr(size), uintptr(unsafe.Pointer(&needed)), uintptr(unsafe.Pointer(&returned)),
		)
		return r, err
	}
	if r, err := call(nil, 0); r == 0 {
		if !errors.Is(err, windows.ERROR_INSUFFICIENT_BUFFER) {
			return nil, fmt.Errorf("spool: EnumJobs %q: %w", name, err)
		}
	}
	if needed == 0 {
		return nil, nil // an empty queue
	}
	buf := buffer(needed)
	if r, err := call(buf, needed); r == 0 {
		return nil, fmt.Errorf("spool: EnumJobs %q: %w", name, err)
	}
	if returned == 0 {
		return nil, nil
	}
	recs := unsafe.Slice((*jobInfo1)(unsafe.Pointer(&buf[0])), int(returned))
	ids := make([]uint32, 0, len(recs))
	for i := range recs {
		ids = append(ids, recs[i].JobID)
	}
	runtime.KeepAlive(buf)
	return ids, nil
}
