//go:build !race

package archive

// raceOn is true under the race detector (race_on_test.go).
const raceOn = false
