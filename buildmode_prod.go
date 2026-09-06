//go:build production

package main

// production is set by the `production` build tag the Taskfile passes for
// release builds: the security headers are applied and nothing of the dev
// tooling exists.
const production = true
