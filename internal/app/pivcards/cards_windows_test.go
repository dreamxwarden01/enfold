//go:build windows

package pivcards

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dreamxwarden01/enfold/internal/app"
	"github.com/dreamxwarden01/enfold/internal/piv"
)

// The core's own error, answered by its prompter and carried back by piv
// under a cancel, must reach the core intact: it is what sends a flow
// back to waiting for the key rather than to a cancel.
func TestWrapKeepsTheChain(t *testing.T) {
	err := wrap(fmt.Errorf("%w: %w", piv.ErrCancelled, app.ErrTokenNoCard))
	if !errors.Is(err, app.ErrTokenCancelled) || !errors.Is(err, app.ErrTokenNoCard) {
		t.Fatalf("chain lost: %v", err)
	}
	if wrap(nil) != nil {
		t.Fatal("nil")
	}
	if err := wrap(fmt.Errorf("%w: x", piv.ErrCardReset)); !errors.Is(err, app.ErrTokenReset) {
		t.Fatalf("reset: %v", err)
	}
}
