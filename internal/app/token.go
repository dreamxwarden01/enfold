package app

import (
	"errors"
	"fmt"

	"github.com/dreamxwarden01/enfold/internal/keystore"
)

// The token surface the core drives, declared app-side so that the core
// compiles and is tested everywhere and only one windows-tagged adapter
// imports internal/piv (APP.md §10). The names mirror internal/piv; the
// adapter is a straight translation.

// Cards enumerates readers and opens one.
type Cards interface {
	Readers() ([]string, error)
	Open(reader string) (Card, error)
}

// Card is one open, exclusively held token.
type Card interface {
	Serial() uint32
	// PINState: retries and whether the card is verified; no retry consumed.
	PINState() (PINStatus, error)
	// VerifyPIN sends one VERIFY — the PIN alone, no touch — so that the
	// ceremony knows a wrong PIN before anything else moves (APP.md §2.2
	// Probing). A right PIN stands for the agreement that follows, which
	// then sends none; a wrong one costs a retry and answers
	// *TokenPINError, a card with none left ErrTokenPINBlocked. No second
	// attempt inside: the ceremony asks again.
	VerifyPIN(pin string) (PINStatus, error)
	// Keys lists every allowlisted slot holding a key; no PIN, no touch.
	Keys() ([]KeyInfo, error)
	Inspect(slot Slot) (KeyInfo, error)
	FirstEmptySlot() (Slot, error)
	ProtectedManagementKey(pin string) ([]byte, error)
	Generate(mgmtKey []byte, o GenerateOptions) (KeyInfo, error)
	// Token binds the slot holding pub to a Prompter; the result is what the
	// keystore's HardwareCredential takes.
	Token(pub []byte, p Prompter) (keystore.Token, error)
	// Close releases and resets the card; ErrTokenResetFailed when the reset
	// could not be done and the card is still verified.
	Close() error
}

// PINStatus mirrors piv.PINStatus.
type PINStatus struct {
	Verified     bool `json:"verified"`
	Retries      int  `json:"retries"`
	RetriesKnown bool `json:"retriesKnown"`
}

// Blocked: no retries remain.
func (s PINStatus) Blocked() bool { return s.RetriesKnown && s.Retries == 0 }

// Slot is a PIV key slot number; only 9d and 82–95 are valid.
type Slot uint8

func (s Slot) String() string { return fmt.Sprintf("%02x", uint8(s)) }

// KeyInfo describes what a slot holds, as the core needs it.
type KeyInfo struct {
	Slot        Slot
	PublicKey   []byte // 65 bytes for a P-256 key; nil otherwise
	Usable      bool
	WhyNot      string
	Certificate bool
	Algorithm   string
	PINPolicy   string
	TouchPolicy string
}

// GenerateOptions mirrors piv.GenerateOptions with the PIN policy fixed to
// once (the design's policy, DESIGN §3).
type GenerateOptions struct {
	Slot      Slot
	Overwrite bool
}

// Prompter is the ceremony's side of the token: the PIN with its status,
// and the touch cue.
type Prompter interface {
	PIN(status PINStatus) (string, error)
	Touch(req TouchRequest)
}

// TouchRequest numbers the operation about to wait for a touch.
type TouchRequest struct {
	N        int
	PINAsked bool
}

// TokenPINError: wrong PIN, with the retries left.
type TokenPINError struct {
	Retries int
}

func (e *TokenPINError) Error() string { return fmt.Sprintf("token: wrong PIN (%d left)", e.Retries) }

// TokenOccupiedError: the slot holds something and Overwrite was not given.
type TokenOccupiedError struct {
	Slot Slot
	Key  KeyInfo
}

func (e *TokenOccupiedError) Error() string { return "token: slot " + e.Slot.String() + " occupied" }

// Sentinels the adapter wraps piv's into. The core never imports piv.
var (
	ErrTokenNoService      = errors.New("token: smart card service not running")
	ErrTokenNoReader       = errors.New("token: no reader")
	ErrTokenReset          = errors.New("token: the card was reset under the connection")
	ErrTokenNoCard         = errors.New("token: no card, or the card went away")
	ErrTokenBusy           = errors.New("token: in use by another program")
	ErrTokenNoPIV          = errors.New("token: no PIV application")
	ErrTokenUnsupported    = errors.New("token: not supported")
	ErrTokenNoKey          = errors.New("token: holds none of this vault's keys")
	ErrTokenNotUsable      = errors.New("token: key not usable")
	ErrTokenPINBlocked     = errors.New("token: PIN blocked")
	ErrTokenPINRequired    = errors.New("token: PIN wanted again")
	ErrTokenTouch          = errors.New("token: not touched in time")
	ErrTokenTooMany        = errors.New("token: operation limit for this handle")
	ErrTokenResetFailed    = errors.New("token: released but still verified")
	ErrTokenFull           = errors.New("token: no empty slot")
	ErrTokenNoProtectedKey = errors.New("token: no PIN-protected management key")
	ErrTokenManagementKey  = errors.New("token: management key refused")
	ErrTokenCancelled      = errors.New("token: cancelled")
	ErrTokenClosed         = errors.New("token: closed")
)
