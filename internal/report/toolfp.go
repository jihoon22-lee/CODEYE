package report

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// ToolFingerprintVersion prefixes fingerprints of tool-reported findings
// decoded from the generic analyzer envelope (docs/39).
const ToolFingerprintVersion = "tool-fp-v1"

// ToolFingerprint is the one place that derives the cross-run identity of a
// finding reported by an analyzer of the R1 packs: analyzer id, rule id,
// snapshot-relative path and a context that does not move with unrelated
// edits (the masked raw message, or a symbol when the tool reports one).
// Line numbers are deliberately not part of it. Run comparison (W23) may
// revise this helper; the go vet fingerprint keeps its own v1 rule.
func ToolFingerprint(analyzerID, ruleID, path, context string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{ToolFingerprintVersion, analyzerID, ruleID, path, context}, "\x00")))
	return ToolFingerprintVersion + ":" + hex.EncodeToString(sum[:])
}
