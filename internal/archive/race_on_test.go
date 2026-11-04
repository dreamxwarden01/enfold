//go:build race

package archive

// raceOn is true under the race detector, which slows the planner's walk
// several times over: the timing tests allow for it rather than skip.
const raceOn = true
