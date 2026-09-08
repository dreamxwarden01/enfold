//go:build !windows

package app

// isLocalFixed has no volume API to ask outside Windows, and guessing from a
// path's text is exactly what APP.md §13 forbids: nothing is probed unless
// the shell supplies a Deps.Volumes of its own.
func isLocalFixed(string) bool { return false }
