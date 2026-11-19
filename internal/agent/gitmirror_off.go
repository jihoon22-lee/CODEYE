//go:build !codeye_fixture

package agent

// testGitMirror leaves a Git job's repository URL unchanged; only test
// builds (gitmirror_on.go) redirect it to a local mirror.
func testGitMirror(url, _ string) (string, string) { return url, "" }
