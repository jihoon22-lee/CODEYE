package depdoc

import "path/filepath"

// AttemptTree is where a dependency task keeps what it prepared for this
// attempt only (cache kind attempt): a directory of its own task scratch,
// removed with the attempt. The record names no path; the analyzers of the
// same attempt derive it from the scratch and the task key.
func AttemptTree(scratch, key string) string {
	return filepath.Join(scratch, "deps", key, "tree")
}
