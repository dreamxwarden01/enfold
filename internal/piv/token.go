//go:build windows

package piv

import (
	"bytes"
	"crypto/ecdh"
	"errors"
	"fmt"

	pivgo "github.com/go-piv/piv-go/v2/piv"

	"github.com/dreamxwarden01/enfold/internal/kdf"
)

// Prompter is the UI's side of the ceremony (DESIGN.md §10): the PIN first,
// with the retries shown, then an unmistakable "touch the key now".
type Prompter interface {
	// PIN asks for the PIN. status says what to show: the retries left, or
	// that the count is unreadable because the card is already verified
	// (never "0 left" then). An error cancels the operation (ErrCancelled).
	PIN(status PINStatus) (string, error)
	// Touch is called immediately before an operation the card will hold
	// for a touch, and must not block. The request numbers the operation on
	// this Token, so that a run of prompts is countable rather than
	// identical; a UI can offer to cancel when the number is surprising.
	Touch(req TouchRequest)
}

// TouchRequest describes the operation about to wait for a touch.
type TouchRequest struct {
	Slot Slot
	// N is the ordinal of this operation on the Token, from 1.
	N int
	// PINAsked: a PIN was just accepted for this operation.
	PINAsked bool
}

// MaxOperations is how many ECDH operations one Token performs before
// refusing with ErrTooManyOperations. An unlock needs one; a fresh Token
// is a fresh user action.
const MaxOperations = 8

// Token is one usable key on an open Card, driven through a Prompter. It
// implements keystore.Token: PublicKey is what the slot record stores, and
// ECDH is where the PIN and the touch happen. Used by one goroutine at a
// time, and finished when its Card closes.
type Token struct {
	c    *Card
	info KeyInfo
	p    Prompter
	n    int
}

// PublicKey is a copy of the 65-byte uncompressed point.
func (t *Token) PublicKey() []byte { return bytes.Clone(t.info.PublicKey) }

// Info describes the key.
func (t *Token) Info() KeyInfo { return t.info }

// ECDH returns the 32-byte X coordinate of the shared point with epk, an
// uncompressed P-256 point, which is validated here again before it
// reaches the token (DESIGN.md §11 trap 2). The ceremony: the card is
// asked whether it is verified; the PIN is prompted for — with the
// operation lock released, so that the caller can keep the exclusive
// connection alive meanwhile (a probe every few seconds; DESIGN.md §11
// trap 25) — and verified by this package when the key's policy needs it:
// always, or once and not verified by this Card (a verified state this
// Card did not create — a reset that did not take, another program's
// VERIFY — is not trusted), with no second attempt inside; the touch
// prompt goes up; the token does the agreement. When the host reset the
// card under the connection, the package reconnects and repeats once,
// with the PIN already collected: it never reached the card. The result
// is piv-go's own buffer, so the caller's zeroing reaches it.
//
// Errors: *PINError (wrong PIN, retries left), ErrPINBlocked, ErrCancelled,
// ErrTouch, ErrPINRequired, ErrNoCard, ErrClosed, ErrInUse,
// ErrTooManyOperations, ErrParams.
func (t *Token) ECDH(epk []byte) ([]byte, error) {
	peer, err := ecdh.P256().NewPublicKey(epk)
	if err != nil {
		return nil, fmt.Errorf("%w: epk: %v", ErrParams, err)
	}
	release, err := t.c.acquire()
	if err != nil {
		return nil, err
	}
	if t.n >= MaxOperations {
		release()
		return nil, ErrTooManyOperations
	}
	if !t.c.beginPrompt() {
		release()
		return nil, ErrInUse
	}
	defer t.c.endPrompt()
	t.n++
	release()
	var pin string
	for attempt := 0; ; attempt++ {
		h, err := t.ecdhOnce(peer, &pin)
		if errors.Is(err, ErrCardReset) && attempt == 0 {
			release, aerr := t.c.acquire()
			if aerr != nil {
				return nil, aerr
			}
			rerr := t.c.reconnectLocked()
			release()
			if rerr != nil {
				return nil, rerr
			}
			continue
		}
		return h, err
	}
}

// ecdhOnce is one attempt of ECDH: the card's state, the prompt with the
// operation lock released for it, then VERIFY, touch and the agreement
// under the lock. A PIN collected by an earlier attempt is used again
// without a prompt.
func (t *Token) ecdhOnce(peer *ecdh.PublicKey, pin *string) ([]byte, error) {
	release, err := t.c.acquire()
	if err != nil {
		return nil, err
	}
	st, err := t.c.pinState()
	release()
	if err != nil {
		return nil, err
	}
	needPIN := t.info.PINPolicy == PINPolicyAlways || !st.Verified || !t.c.verifiedByUs()
	if needPIN && *pin == "" {
		if st.Blocked() {
			return nil, ErrPINBlocked
		}
		p, err := t.p.PIN(st)
		if err != nil {
			// The prompter's own error stays in the chain: a caller that
			// ended the prompt because its probe found the key gone must
			// see that identity, not a cancel.
			if errors.Is(err, ErrCancelled) {
				return nil, err
			}
			return nil, fmt.Errorf("%w: %w", ErrCancelled, err)
		}
		if err := checkPIN(p); err != nil {
			return nil, err
		}
		*pin = p
	}
	// The caller's keep-alive probe may be on the card this instant: the
	// operation waits for it rather than refusing.
	release, err = t.c.acquireWait()
	if err != nil {
		return nil, err
	}
	defer release()
	if needPIN {
		t.c.markDirty(dirtyPIN)
		if err := t.c.dev.VerifyPIN(*pin); err != nil {
			return nil, mapErr(err)
		}
		t.c.markVerifiedByUs()
	} else {
		t.c.markDirty(dirtyPIN) // a verified state is being used; Close clears it
	}
	t.p.Touch(TouchRequest{Slot: t.info.Slot, N: t.n, PINAsked: needPIN})
	ps, err := t.info.Slot.pivSlot()
	if err != nil {
		return nil, err
	}
	// The PIN is this package's business (done above); piv-go must neither
	// prompt nor verify, so it is told the key needs no PIN.
	priv, err := t.c.dev.PrivateKey(ps, t.info.ecdsaPub, pivgo.KeyAuth{PINPolicy: pivgo.PINPolicyNever})
	if err != nil {
		return nil, mapErr(err)
	}
	e, ok := priv.(ecdher)
	if !ok {
		return nil, fmt.Errorf("%w: slot key of type %T", ErrUnsupported, priv)
	}
	h, err := e.ECDH(peer)
	if err != nil {
		if sw, ok := statusWord(err); ok && sw == 0x6982 {
			// "Security status not satisfied" is the card's word for both a
			// missing touch and an unsatisfied PIN policy. The empty VERIFY
			// tells them apart after the fact, at no cost.
			st, e2 := t.c.pinState()
			switch {
			case e2 != nil && isTransport(e2):
				// A reset met here is the caller's to heal, not a missed touch.
				return nil, e2
			case e2 == nil && !st.Verified:
				return nil, fmt.Errorf("%w: %v", ErrPINRequired, err)
			}
			return nil, fmt.Errorf("%w: %v", ErrTouch, err)
		}
		return nil, mapErr(err)
	}
	if len(h) != 32 {
		kdf.Zero(h)
		return nil, fmt.Errorf("%w: shared secret of %d bytes", ErrUnsupported, len(h))
	}
	return h, nil
}
