//go:build !windows

package dragout

// The native drag is Windows' alone (docs/SCOPE.md); elsewhere the package
// compiles so that the core, which imports it for the plan's types and the
// scavenge, does too.

// Drag is one native drag; it never exists here.
type Drag struct{}

// InitOLE has no OLE to initialise.
func InitOLE() error { return ErrUnsupported }

// UninitOLE has nothing to balance.
func UninitOLE() {}

// Begin answers ErrUnsupported: there is no drag to run.
func Begin(Options) (*Drag, error) { return nil, ErrUnsupported }

// Run is never reached: Begin never hands out a Drag.
func (d *Drag) Run() (Result, error) { return Result{}, ErrUnsupported }

// Cancel has nothing to cancel.
func (d *Drag) Cancel() {}
