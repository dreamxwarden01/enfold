//go:build windows

// Package pivcards adapts internal/piv to the app core's token interfaces.
// It is the only importer of internal/piv (APP.md §10): everything the
// core needs is translated here, error by error, so the core stays
// buildable and testable on every platform.
package pivcards

import (
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/app"
	"github.com/dreamxwarden01/enfold/internal/keystore"
	"github.com/dreamxwarden01/enfold/internal/piv"
)

type cards struct{}

// New returns the hardware token source.
func New() app.Cards { return cards{} }

func (cards) Readers() ([]string, error) {
	r, err := piv.Readers()
	return r, wrap(err)
}

func (cards) Open(reader string) (app.Card, error) {
	c, err := piv.Open(reader)
	if err != nil {
		return nil, wrap(err)
	}
	return &card{c: c}, nil
}

type card struct {
	c *piv.Card
}

func (k *card) Serial() uint32 { return k.c.Serial() }

func (k *card) PINState() (app.PINStatus, error) {
	st, err := k.c.PINState()
	return app.PINStatus{Verified: st.Verified, Retries: st.Retries, RetriesKnown: st.RetriesKnown}, wrap(err)
}

func (k *card) Keys() ([]app.KeyInfo, error) {
	keys, err := k.c.Keys()
	if err != nil {
		return nil, wrap(err)
	}
	out := make([]app.KeyInfo, 0, len(keys))
	for _, ki := range keys {
		out = append(out, info(ki))
	}
	return out, nil
}

func info(ki piv.KeyInfo) app.KeyInfo {
	return app.KeyInfo{
		Slot: app.Slot(ki.Slot), PublicKey: ki.PublicKey, Usable: ki.Usable(), WhyNot: ki.WhyNotUsable(),
		Certificate: ki.Certificate, Algorithm: ki.Algorithm.String(), PINPolicy: ki.PINPolicy.String(), TouchPolicy: ki.TouchPolicy.String(),
	}
}

func (k *card) Inspect(slot app.Slot) (app.KeyInfo, error) {
	ki, err := k.c.Inspect(piv.Slot(slot))
	if err != nil {
		return app.KeyInfo{}, wrap(err)
	}
	return info(ki), nil
}

func (k *card) FirstEmptySlot() (app.Slot, error) {
	s, err := k.c.FirstEmptySlot()
	return app.Slot(s), wrap(err)
}

func (k *card) ProtectedManagementKey(pin string) ([]byte, error) {
	b, err := k.c.ProtectedManagementKey(pin)
	return b, wrap(err)
}

func (k *card) Generate(mgmtKey []byte, o app.GenerateOptions) (app.KeyInfo, error) {
	ki, err := k.c.Generate(mgmtKey, piv.GenerateOptions{Slot: piv.Slot(o.Slot), PINPolicy: piv.PINPolicyOnce, Overwrite: o.Overwrite})
	if err != nil {
		return app.KeyInfo{}, wrap(err)
	}
	return info(ki), nil
}

func (k *card) Token(pub []byte, p app.Prompter) (keystore.Token, error) {
	t, err := k.c.Token(pub, &prompter{p: p})
	if err != nil {
		return nil, wrap(err)
	}
	return &token{t: t}, nil
}

func (k *card) Close() error { return wrap(k.c.Close()) }

// token wraps the piv Token so that its errors are the core's.
type token struct {
	t *piv.Token
}

func (t *token) PublicKey() []byte { return t.t.PublicKey() }

func (t *token) ECDH(epk []byte) ([]byte, error) {
	h, err := t.t.ECDH(epk)
	return h, wrap(err)
}

// prompter adapts the core's Prompter to piv's.
type prompter struct {
	p app.Prompter
}

func (p *prompter) PIN(st piv.PINStatus) (string, error) {
	return p.p.PIN(app.PINStatus{Verified: st.Verified, Retries: st.Retries, RetriesKnown: st.RetriesKnown})
}

func (p *prompter) Touch(req piv.TouchRequest) {
	p.p.Touch(app.TouchRequest{N: req.N, PINAsked: req.PINAsked})
}

// wrap translates piv's errors into the core's sentinels, keeping the
// original — and whatever it wraps, such as the core's own error that a
// prompter answered with — in the chain.
func wrap(err error) error {
	if err == nil {
		return nil
	}
	var pe *piv.PINError
	if errors.As(err, &pe) {
		return &app.TokenPINError{Retries: pe.Retries}
	}
	var oe *piv.OccupiedError
	if errors.As(err, &oe) {
		return &app.TokenOccupiedError{Slot: app.Slot(oe.Key.Slot), Key: info(oe.Key)}
	}
	for _, m := range table {
		if errors.Is(err, m.from) {
			return fmt.Errorf("%w: %w", m.to, err)
		}
	}
	return err
}

var table = []struct {
	from, to error
}{
	{piv.ErrNoService, app.ErrTokenNoService},
	{piv.ErrNoReader, app.ErrTokenNoReader},
	{piv.ErrNoCard, app.ErrTokenNoCard},
	{piv.ErrCardReset, app.ErrTokenReset},
	{piv.ErrBusy, app.ErrTokenBusy},
	{piv.ErrInUse, app.ErrTokenBusy},
	{piv.ErrNoPIVApplet, app.ErrTokenNoPIV},
	{piv.ErrUnsupported, app.ErrTokenUnsupported},
	{piv.ErrClosed, app.ErrTokenClosed},
	{piv.ErrNoKey, app.ErrTokenNoKey},
	{piv.ErrNotUsable, app.ErrTokenNotUsable},
	{piv.ErrPINBlocked, app.ErrTokenPINBlocked},
	{piv.ErrPINRequired, app.ErrTokenPINRequired},
	{piv.ErrTouch, app.ErrTokenTouch},
	{piv.ErrTooManyOperations, app.ErrTokenTooMany},
	{piv.ErrResetFailed, app.ErrTokenResetFailed},
	{piv.ErrFull, app.ErrTokenFull},
	{piv.ErrNoProtectedKey, app.ErrTokenNoProtectedKey},
	{piv.ErrManagementKey, app.ErrTokenManagementKey},
	{piv.ErrCancelled, app.ErrTokenCancelled},
	{piv.ErrEmpty, app.ErrTokenNoKey},
	{piv.ErrForbiddenSlot, app.ErrTokenUnsupported},
	{piv.ErrParams, app.ErrTokenUnsupported},
}
