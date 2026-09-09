//go:build !windows

package spool

// snapshot has no counterpart outside Windows: the print spooler this
// package reads is winspool.drv's, and Enfold ships on Windows (SCOPE.md).
// The page falls back to its second confirmation, which is what an
// unreadable spooler has always meant (APP.md §6).
func snapshot() (Set, error) { return nil, ErrUnsupported }
