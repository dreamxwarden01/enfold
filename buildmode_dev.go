//go:build !production

package main

// A dev build (wails3 dev): Vite serves the page from its own origin, so
// the asset middleware's headers would not reach the document. Dev builds
// have debug logging and DevTools on, and may only be pointed at a
// throwaway vault (docs/APP.md §1).
const production = false
