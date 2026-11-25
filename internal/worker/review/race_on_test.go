//go:build race

package review

// raceDetector: the tests run with -race. The concurrent review harness then
// runs fewer degrees of parallelism and one seed; without -race it runs the
// whole matrix.
const raceDetector = true
