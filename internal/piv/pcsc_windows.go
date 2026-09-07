//go:build windows

package piv

import (
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// A thin PC/SC layer of our own, beside piv-go's. piv-go talks to
// winscard.dll too, but hides the handle and the return codes, and its Open
// leaks an exclusive connection when the card is pulled between connect and
// transaction, or when the PIV applet is missing (v2.6.0 piv.go:172–179).
// So every reader is probed here first — connect, SELECT the PIV
// application, read the version, disconnect — with typed errors from the
// real return codes, and piv-go is only handed a reader that just answered.
// The same layer resets the card when a Card closes (DESIGN.md §11 trap 14),
// which piv-go cannot do: it only ever leaves the card as it is.

var (
	winscard                  = windows.NewLazySystemDLL("winscard.dll")
	procSCardEstablishContext = winscard.NewProc("SCardEstablishContext")
	procSCardReleaseContext   = winscard.NewProc("SCardReleaseContext")
	procSCardListReadersW     = winscard.NewProc("SCardListReadersW")
	procSCardConnectW         = winscard.NewProc("SCardConnectW")
	procSCardDisconnect       = winscard.NewProc("SCardDisconnect")
	procSCardBeginTransaction = winscard.NewProc("SCardBeginTransaction")
	procSCardEndTransaction   = winscard.NewProc("SCardEndTransaction")
	procSCardTransmit         = winscard.NewProc("SCardTransmit")
)

const (
	scardScopeSystem    = 2
	scardShareExclusive = 1
	scardShareShared    = 2
	scardProtocolT1     = 2
	scardLeaveCard      = 0
	scardResetCard      = 1

	// Return codes that the package tells apart. Everything else is reported
	// with its number.
	scardENoService        = 0x8010001D
	scardEServiceStopped   = 0x8010001E
	scardENoReadersAvail   = 0x8010002E
	scardEUnknownReader    = 0x80100009
	scardEReaderUnavail    = 0x80100017
	scardESharingViolation = 0x8010000B
	scardENoSmartcard      = 0x8010000C
	scardWRemovedCard      = 0x80100069
	scardWUnpoweredCard    = 0x80100067
	scardWResetCard        = 0x80100068
	scardWUnresponsive     = 0x80100066
	scardEProtoMismatch    = 0x8010000F
	scardECommDataLost     = 0x8010002F
	scardENotReady         = 0x80100010
	scardESystemCancelled  = 0x80100012
	scardECommError        = 0x80100013
	scardEShutdown         = 0x80100018
	scardEUnexpected       = 0x8010001F

	// scardFacility is the high half of every SCARD_ return code; a code
	// without it is a Win32 system error (DESIGN.md §11 trap 26).
	scardFacility = 0x8010
)

// scError is a winscard return code.
type scError struct {
	call string
	rc   uint32
}

func (e *scError) Error() string {
	return fmt.Sprintf("%s: 0x%08x", e.call, e.rc)
}

// sentinel maps the return codes the caller can act on to the package's
// errors, so a PC/SC failure surfaces as ErrBusy, ErrNoCard and so on
// rather than a number.
func (e *scError) sentinel() error {
	switch e.rc {
	case scardENoService, scardEServiceStopped, scardESystemCancelled, scardEShutdown:
		return ErrNoService
	case scardENoReadersAvail, scardEUnknownReader, scardEReaderUnavail:
		return ErrNoReader
	case scardESharingViolation:
		return ErrBusy
	case scardWResetCard:
		return ErrCardReset
	case scardENoSmartcard, scardWRemovedCard, scardWUnpoweredCard, scardWUnresponsive, scardEProtoMismatch, scardECommDataLost,
		scardENotReady, scardECommError, scardEUnexpected:
		return ErrNoCard
	}
	if e.rc>>16 != scardFacility {
		// A Win32 code rather than one of the facility's (DESIGN.md §11
		// trap 26): from a call that addresses a card or a card handle
		// the device is not talking — a key pulled with the request on
		// the wire answers ERROR_GEN_FAILURE or ERROR_BAD_COMMAND before
		// the resource manager has noticed — while from the context or
		// the reader list it is the service or the session
		// (ERROR_BROKEN_PIPE: a remote session without smart-card
		// redirection), which the waiting state treats as no reader.
		switch e.call {
		case "SCardEstablishContext", "SCardListReadersW":
			return ErrNoService
		}
		return ErrNoCard
	}
	return nil
}

// wrap turns a return code into an error the package's callers can test
// with errors.Is.
func scCheck(call string, r uintptr) error {
	rc := uint32(r)
	if rc == 0 {
		return nil
	}
	e := &scError{call: call, rc: rc}
	if s := e.sentinel(); s != nil {
		return fmt.Errorf("%w (%v)", s, e)
	}
	return e
}

type scContext struct {
	ctx uintptr
}

func newSCContext() (*scContext, error) {
	var ctx uintptr
	r, _, _ := procSCardEstablishContext.Call(scardScopeSystem, 0, 0, uintptr(unsafe.Pointer(&ctx)))
	if err := scCheck("SCardEstablishContext", r); err != nil {
		return nil, err
	}
	return &scContext{ctx: ctx}, nil
}

func (c *scContext) release() {
	procSCardReleaseContext.Call(c.ctx)
}

// readers lists every reader the resource manager knows. No readers at all
// is an empty list, not an error.
func (c *scContext) readers() ([]string, error) {
	var n uint32
	r, _, _ := procSCardListReadersW.Call(c.ctx, 0, 0, uintptr(unsafe.Pointer(&n)))
	if uint32(r) == scardENoReadersAvail {
		return nil, nil
	}
	if err := scCheck("SCardListReadersW", r); err != nil {
		return nil, err
	}
	buf := make([]uint16, n)
	r, _, _ = procSCardListReadersW.Call(c.ctx, 0, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
	if uint32(r) == scardENoReadersAvail {
		return nil, nil
	}
	if err := scCheck("SCardListReadersW", r); err != nil {
		return nil, err
	}
	var out []string
	start := 0
	for i := 0; i < int(n) && i < len(buf); i++ {
		if buf[i] != 0 {
			continue
		}
		if i == start { // the double NUL that ends the multi-string
			break
		}
		out = append(out, windows.UTF16ToString(buf[start:i]))
		start = i + 1
	}
	return out, nil
}

// scHandle is one connection to a card.
type scHandle struct {
	h uintptr
}

// connect opens the reader with the given share mode over T=1.
func (c *scContext) connect(reader string, share uintptr) (*scHandle, error) {
	rp, err := windows.UTF16PtrFromString(reader)
	if err != nil {
		return nil, fmt.Errorf("%w: reader name: %v", ErrParams, err)
	}
	var (
		h     uintptr
		proto uint32
	)
	r, _, _ := procSCardConnectW.Call(c.ctx, uintptr(unsafe.Pointer(rp)), share, scardProtocolT1,
		uintptr(unsafe.Pointer(&h)), uintptr(unsafe.Pointer(&proto)))
	if err := scCheck("SCardConnectW", r); err != nil {
		return nil, err
	}
	return &scHandle{h: h}, nil
}

func (h *scHandle) disconnect(disposition uintptr) error {
	r, _, _ := procSCardDisconnect.Call(h.h, disposition)
	return scCheck("SCardDisconnect", r)
}

func (h *scHandle) begin() error {
	r, _, _ := procSCardBeginTransaction.Call(h.h)
	return scCheck("SCardBeginTransaction", r)
}

func (h *scHandle) end(disposition uintptr) error {
	r, _, _ := procSCardEndTransaction.Call(h.h, disposition)
	return scCheck("SCardEndTransaction", r)
}

// scardIORequest is SCARD_IO_REQUEST for T=1: the protocol and the
// structure's own length.
type scardIORequest struct {
	protocol uint32
	length   uint32
}

var pciT1 = &scardIORequest{protocol: scardProtocolT1, length: 8}

// apduStatus is a card's status word on a command that was transmitted but
// refused.
type apduStatus struct {
	sw uint16
}

func (e *apduStatus) Error() string { return fmt.Sprintf("card status %04x", e.sw) }

// transmit sends one APDU and returns the response data, following 61xx
// "more data" chains with GET RESPONSE. A status other than 9000 is an
// *apduStatus error.
func (h *scHandle) transmit(apdu []byte) ([]byte, error) {
	var out []byte
	for {
		resp := make([]byte, 258)
		n := uint32(len(resp))
		r, _, _ := procSCardTransmit.Call(h.h, uintptr(unsafe.Pointer(pciT1)), uintptr(unsafe.Pointer(&apdu[0])), uintptr(len(apdu)),
			0, uintptr(unsafe.Pointer(&resp[0])), uintptr(unsafe.Pointer(&n)))
		if err := scCheck("SCardTransmit", r); err != nil {
			return nil, err
		}
		if n < 2 {
			return nil, fmt.Errorf("%w: response of %d bytes", ErrUnsupported, n)
		}
		sw1, sw2 := resp[n-2], resp[n-1]
		out = append(out, resp[:n-2]...)
		switch {
		case sw1 == 0x90 && sw2 == 0x00:
			return out, nil
		case sw1 == 0x61:
			apdu = []byte{0x00, 0xc0, 0x00, 0x00, sw2}
		default:
			return nil, &apduStatus{sw: uint16(sw1)<<8 | uint16(sw2)}
		}
	}
}

var (
	// SELECT the PIV application (piv-go's aidPIV) and Yubico's GET VERSION.
	apduSelectPIV  = []byte{0x00, 0xa4, 0x04, 0x00, 0x05, 0xa0, 0x00, 0x00, 0x03, 0x08}
	apduGetVersion = []byte{0x00, 0xfd, 0x00, 0x00}
	// VERIFY with no data: answers 9000 when the PIN is already verified,
	// 63Cn with the retries otherwise. Consumes no retry.
	apduVerifyEmpty = []byte{0x00, 0x20, 0x00, 0x80}
)

// readersReal lists the readers whose name says YubiKey.
func readersReal() ([]string, error) {
	ctx, err := newSCContext()
	if err != nil {
		return nil, err
	}
	defer ctx.release()
	all, err := ctx.readers()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, r := range all {
		if strings.Contains(strings.ToLower(r), "yubi") {
			out = append(out, r)
		}
	}
	return out, nil
}

// preflightReal is the probe before piv-go connects: an exclusive
// connection, the PIV SELECT and the version, then a disconnect that resets
// the card — a warm reset, so the Card that follows starts unverified
// whatever any program left behind. A reader that fails the SELECT is
// disconnected leaving the card alone: it belongs to some other
// application's card, whose state is not ours to clear.
func preflightReal(reader string) (Version, error) {
	ctx, err := newSCContext()
	if err != nil {
		return Version{}, err
	}
	defer ctx.release()
	h, err := ctx.connect(reader, scardShareExclusive)
	if err != nil {
		return Version{}, err
	}
	disposition := uintptr(scardLeaveCard)
	defer func() { h.disconnect(disposition) }()
	if err := h.begin(); err != nil {
		return Version{}, err
	}
	defer h.end(scardLeaveCard)
	if _, err := h.transmit(apduSelectPIV); err != nil {
		var st *apduStatus
		if errors.As(err, &st) {
			return Version{}, fmt.Errorf("%w (SELECT answered %04x)", ErrNoPIVApplet, st.sw)
		}
		return Version{}, err
	}
	v, err := h.transmit(apduGetVersion)
	if err != nil {
		var st *apduStatus
		if errors.As(err, &st) {
			return Version{}, fmt.Errorf("%w: GET VERSION answered %04x", ErrUnsupported, st.sw)
		}
		return Version{}, err
	}
	if len(v) != 3 {
		return Version{}, fmt.Errorf("%w: GET VERSION returned %d bytes", ErrUnsupported, len(v))
	}
	disposition = scardResetCard
	return Version{Major: int(v[0]), Minor: int(v[1]), Patch: int(v[2])}, nil
}

// Verified asks the card in the reader whether it is PIN-verified right
// now, over a shared connection that sends only the SELECT and the empty
// VERIFY and leaves the card as it is — no reset, no retry consumed, no
// touch. It is the check that Close's reset worked, from outside the Card:
// after a Close that verified anything, this must be false.
func Verified(reader string) (bool, error) {
	ctx, err := newSCContext()
	if err != nil {
		return false, err
	}
	defer ctx.release()
	h, err := ctx.connect(reader, scardShareShared)
	if err != nil {
		return false, err
	}
	defer h.disconnect(scardLeaveCard)
	if err := h.begin(); err != nil {
		return false, err
	}
	defer h.end(scardLeaveCard)
	if _, err := h.transmit(apduSelectPIV); err != nil {
		var st *apduStatus
		if errors.As(err, &st) {
			return false, fmt.Errorf("%w (SELECT answered %04x)", ErrNoPIVApplet, st.sw)
		}
		return false, err
	}
	_, err = h.transmit(apduVerifyEmpty)
	if err == nil {
		return true, nil
	}
	var st *apduStatus
	if errors.As(err, &st) && (st.sw&0xfff0 == 0x63c0 || st.sw == 0x6983) {
		return false, nil
	}
	return false, err
}

// prepareResetReal is Close's reset in two halves. The first — the
// context and the reader name — runs before piv-go lets go of the card, so
// that only a connect and a disconnect sit between piv-go's LEAVE_CARD and
// the reset. The second, returned as a function, connects shared — the
// mode that was measured to work, and the one with the fewest ways to be
// refused, since no APDU is sent — and drops the connection with
// SCARD_RESET_CARD. When that cannot be done, it reconnects and asks the
// card whether it is still PIN-verified, so the caller warns only when
// there is something to warn about; the management-key authentication
// cannot be asked about, and the caller decides on that from what it did.
//
// done: the reset happened. verified: the reset did not happen and the card
// is still PIN-verified, or could not be asked (which counts the same).
func prepareResetReal(reader string) func() (done, verified bool, err error) {
	ctx, err := newSCContext()
	if err != nil {
		if gone(err) {
			return func() (bool, bool, error) { return true, false, nil } // no service: the reader left
		}
		return func() (bool, bool, error) { return false, true, fmt.Errorf("%w: %v", ErrResetFailed, err) }
	}
	return func() (bool, bool, error) {
		defer ctx.release()
		h, err := ctx.connect(reader, scardShareShared)
		if err == nil {
			if err := h.disconnect(scardResetCard); err == nil {
				return true, false, nil
			}
		} else if gone(err) {
			// The card, the reader or the service is not there: an
			// unpowered card holds nothing of ours, so there is nothing
			// to reset and nothing to warn of (DESIGN.md §11 trap 26).
			return true, false, nil
		}
		// The reset did not happen. Is the card verified?
		h, err = ctx.connect(reader, scardShareShared)
		if err != nil {
			if gone(err) {
				return true, false, nil
			}
			return false, true, fmt.Errorf("%w: %v", ErrResetFailed, err)
		}
		defer h.disconnect(scardLeaveCard)
		if err := h.begin(); err != nil {
			return false, true, fmt.Errorf("%w: %v", ErrResetFailed, err)
		}
		defer h.end(scardLeaveCard)
		if _, err := h.transmit(apduSelectPIV); err != nil {
			return false, true, fmt.Errorf("%w: %v", ErrResetFailed, err)
		}
		_, err = h.transmit(apduVerifyEmpty)
		if err == nil {
			return false, true, fmt.Errorf("%w: the card is still PIN-verified", ErrResetFailed)
		}
		var st *apduStatus
		if errors.As(err, &st) && (st.sw&0xfff0 == 0x63c0 || st.sw == 0x6983) {
			return false, false, nil // no PIN is left; the caller judges the rest
		}
		return false, true, fmt.Errorf("%w: %v", ErrResetFailed, err)
	}
}
