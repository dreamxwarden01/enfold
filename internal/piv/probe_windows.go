//go:build windows

package piv

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// An experiment, not an API: the view from ANOTHER process of a card this
// program holds. Can the other process connect at all — in shared,
// exclusive or direct mode — and if it can, is the card PIN-verified for
// it? tools/pivtool probe runs it against tools/pivtool hold, on the test
// key (DESIGN.md §11 trap 27; DECISIONS 2026-09-07 "Takeover"). Nothing
// in the app calls this.

// ShareMode is SCardConnect's share mode.
type ShareMode string

const (
	ShareShared    ShareMode = "shared"
	ShareExclusive ShareMode = "exclusive"
	ShareDirect    ShareMode = "direct"
)

const scardShareDirect = 3

// ProbeResult is what the other process saw.
type ProbeResult struct {
	Connected bool
	Err       error // the connect's error when not connected
	Serial    uint32
	Verified  bool
	Retries   int
	Blocked   bool
	Took      time.Duration
}

func (r ProbeResult) String() string {
	if !r.Connected {
		return fmt.Sprintf("not connected: %v", r.Err)
	}
	return fmt.Sprintf("connected: serial=%d verified=%v retries=%d blocked=%v", r.Serial, r.Verified, r.Retries, r.Blocked)
}

// apduGetSerial is YubiKey's GET SERIAL (firmware 5): four bytes.
var apduGetSerial = []byte{0x00, 0xf8, 0x00, 0x00}

// Probe connects once in the given mode and, connected, asks the card for
// its serial and its PIN state with the retry-free empty VERIFY — nothing
// that changes the card — then disconnects leaving it as it was, so that
// what the holder sees afterwards is undisturbed.
func Probe(reader string, share ShareMode) ProbeResult {
	t0 := time.Now()
	res := ProbeResult{}
	ctx, err := newSCContext()
	if err != nil {
		res.Err = err
		return res
	}
	defer ctx.release()
	var h *scHandle
	switch share {
	case ShareShared:
		h, err = ctx.connect(reader, scardShareShared)
	case ShareExclusive:
		h, err = ctx.connect(reader, scardShareExclusive)
	case ShareDirect:
		h, err = ctx.connectDirect(reader)
	default:
		res.Err = fmt.Errorf("%w: share mode %q", ErrParams, share)
		return res
	}
	res.Took = time.Since(t0)
	if err != nil {
		res.Err = err
		return res
	}
	res.Connected = true
	defer h.disconnect(scardLeaveCard)
	if share == ShareDirect {
		return res // no protocol: nothing can be transmitted
	}
	if err := h.begin(); err != nil {
		res.Err = err
		return res
	}
	defer h.end(scardLeaveCard)
	if _, err := h.transmit(apduSelectPIV); err != nil {
		res.Err = fmt.Errorf("select: %w", err)
		return res
	}
	if b, err := h.transmit(apduGetSerial); err == nil && len(b) == 4 {
		res.Serial = binary.BigEndian.Uint32(b)
	}
	_, err = h.transmit(apduVerifyEmpty)
	var st *apduStatus
	switch {
	case err == nil:
		res.Verified = true
	case errors.As(err, &st) && st.sw&0xfff0 == 0x63c0:
		res.Retries = int(st.sw & 0x0f)
	case errors.As(err, &st) && st.sw == 0x6983:
		res.Blocked = true
	default:
		res.Err = fmt.Errorf("empty verify: %w", err)
	}
	return res
}

// ProbeSpin is Probe in a tight loop until it connects or the deadline
// passes: the race for whatever window the holder leaves. Returns the
// first connected result (or the last failure) and how many attempts it
// took.
func ProbeSpin(reader string, share ShareMode, deadline time.Duration) (ProbeResult, int) {
	t0 := time.Now()
	n := 0
	var last ProbeResult
	for time.Since(t0) < deadline {
		n++
		last = Probe(reader, share)
		if last.Connected {
			last.Took = time.Since(t0)
			return last, n
		}
	}
	last.Took = time.Since(t0)
	return last, n
}

// connectDirect opens the reader with SCARD_SHARE_DIRECT and no protocol.
func (c *scContext) connectDirect(reader string) (*scHandle, error) {
	rp, err := windows.UTF16PtrFromString(reader)
	if err != nil {
		return nil, fmt.Errorf("%w: reader name: %v", ErrParams, err)
	}
	var (
		h     uintptr
		proto uint32
	)
	r, _, _ := procSCardConnectW.Call(c.ctx, uintptr(unsafe.Pointer(rp)), scardShareDirect, 0,
		uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&proto)))
	if err := scCheck("SCardConnectW", r); err != nil {
		return nil, err
	}
	return &scHandle{h: h}, nil
}
