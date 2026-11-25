//go:build !race

package review

// raceDetector: the tests run without -race (see race_on_test.go).
const raceDetector = false
