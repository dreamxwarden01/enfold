//go:build windows

package piv

import (
	"errors"
	"fmt"
	"reflect"

	pivgo "github.com/go-piv/piv-go/v2/piv"
)

// piv-go keeps its PC/SC handles unexported; this file reaches them by
// reflection, for two things. One is the package's own release: a reset
// on the exclusive handle itself (resetHandleReal), which Close uses so
// that no other process can connect between piv-go's leave-card close
// and a reset connection — a window measured to be winnable (DESIGN.md
// §11 trap 27). The other is an experiment, not an API: Interrupt, which
// tools/pivtool touchabort used to measure that a touch wait cannot be
// cut short from another goroutine (trap 23) — SCardCancel is accepted
// and does nothing to a transmit, and a disconnect queues behind it.

var procSCardCancel = winscard.NewProc("SCardCancel")

// resetHandleReal resets the card on piv-go's own exclusive handle:
// SCardDisconnect(SCARD_RESET_CARD). Since the handle is the exclusive
// one, nobody else holds the card at any moment before the reset — there
// is no gap for a spinning SCardConnect to win. piv-go's Close afterwards
// answers ERROR_INVALID_HANDLE for the disconnect and still releases its
// context; the caller ignores it. An error here — no piv-go device, a
// field that a newer piv-go renamed, the card gone — leaves the caller to
// the two-step release.
func resetHandleReal(dev device) error {
	yk, ok := dev.(*pivgo.YubiKey)
	if !ok {
		return fmt.Errorf("%w: not a piv-go device", ErrParams)
	}
	h, err := handleField(yk, "h", "handle")
	if err != nil {
		return err
	}
	r, _, _ := procSCardDisconnect.Call(h, scardResetCard)
	return scCheck("SCardDisconnect", r)
}

// InterruptMode is what Interrupt does to the connection under the
// operation in flight.
type InterruptMode string

const (
	// InterruptCancel calls SCardCancel on piv-go's resource-manager
	// context: what a pending SCardGetStatusChange answers to; whether a
	// pending SCardTransmit does is the question.
	InterruptCancel InterruptMode = "cancel"
	// InterruptReset calls SCardDisconnect(SCARD_RESET_CARD) on piv-go's
	// card handle from this goroutine: the card is powered down and up,
	// which should end whatever the applet was waiting for.
	InterruptReset InterruptMode = "reset"
	// InterruptClose calls piv-go's own Close (disconnect with LEAVE_CARD,
	// then the context released).
	InterruptClose InterruptMode = "close"
	// InterruptTwoStep is Close the long way, on purpose: piv-go's
	// leave-card close, then the reset connection — the fallback, measured
	// on its own (trap 27).
	InterruptTwoStep InterruptMode = "twostep"
)

// Interrupt applies mode to the connection under whatever operation is in
// flight, without the operation lock — the point is to reach an operation
// that holds it — and marks the Card closed, so that nothing after the
// operation returns uses the connection again. The operation in flight
// returns whatever the transport gives it. Caller runs the operation on
// another goroutine and measures.
func (c *Card) Interrupt(mode InterruptMode) error {
	yk, ok := c.dev.(*pivgo.YubiKey)
	if !ok {
		return fmt.Errorf("%w: not a piv-go device", ErrParams)
	}
	if mode == InterruptTwoStep {
		c.st.Lock()
		c.forceLongWay = true
		c.st.Unlock()
		return c.Close()
	}
	c.st.Lock()
	c.closed = true
	c.st.Unlock()
	switch mode {
	case InterruptCancel:
		ctx, err := handleField(yk, "ctx", "ctx")
		if err != nil {
			return err
		}
		r, _, _ := procSCardCancel.Call(ctx)
		return scCheck("SCardCancel", r)
	case InterruptReset:
		h, err := handleField(yk, "h", "handle")
		if err != nil {
			return err
		}
		r, _, _ := procSCardDisconnect.Call(h, scardResetCard)
		return scCheck("SCardDisconnect", r)
	case InterruptClose:
		return yk.Close()
	}
	return fmt.Errorf("%w: mode %q", ErrParams, mode)
}

// handleField reads yk.<outer>.<inner>, a syscall.Handle behind an
// unexported pointer field, by reflection: this file is the only place
// that looks inside piv-go, and only for the experiment.
func handleField(yk *pivgo.YubiKey, outer, inner string) (uintptr, error) {
	v := reflect.ValueOf(yk).Elem().FieldByName(outer)
	if !v.IsValid() || v.IsNil() {
		return 0, errors.New("piv-go: field " + outer + " not found or nil")
	}
	f := v.Elem().FieldByName(inner)
	if !f.IsValid() || f.Kind() != reflect.Uintptr {
		return 0, errors.New("piv-go: field " + outer + "." + inner + " not a handle")
	}
	return uintptr(f.Uint()), nil
}
