//go:build windows

package piv

import (
	"errors"
	"fmt"
	"reflect"

	pivgo "github.com/go-piv/piv-go/v2/piv"
)

// An experiment, not an API: can a card call in flight — a touch wait —
// be cut short from another goroutine? PC/SC offers two levers that the
// package cannot otherwise reach, since piv-go keeps its handles
// unexported: SCardCancel on piv-go's context, and SCardDisconnect on its
// card handle with a reset. tools/pivtool touchabort measures both on the
// test key (DESIGN.md §11 trap 23; DECISIONS 2026-09-07). Measured on a
// YubiKey 5.7.4: none of the three cuts the wait short — SCardCancel is
// accepted and does nothing to a transmit, and a disconnect (reset or
// not) queues behind the transmit and returns with it — the key gives up
// on its own after about 14.3 s. Nothing in the app calls this.

var procSCardCancel = winscard.NewProc("SCardCancel")

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
