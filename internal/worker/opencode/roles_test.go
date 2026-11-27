package opencode

import (
	"testing"

	reviewpack "github.com/jihoon22-lee/CODEYE/prompts/review"
)

// Every model role of the review instruction pack is a role a request may
// name; a role the pack does not have is refused before OpenCode starts. A
// role added to the pack and not here fails in the real worker only
// (invalid_review_role), which the scripted review tests do not reach.
func TestEveryPackRoleIsAReviewRole(t *testing.T) {
	for _, role := range append(reviewpack.Roles, "probe") {
		if !ReviewRole(role) {
			t.Errorf("pack role %q is refused", role)
		}
	}
	// The lead follow-up (docs/REVIEW.md#단서-후속-조사) is a pack role the
	// adapter must take; it once refused it with invalid_review_role.
	if !ReviewRole("leads") {
		t.Error("lead follow-up role is refused")
	}
	for _, role := range []string{"", "scope", "Triage", "review.triage"} {
		if ReviewRole(role) {
			t.Errorf("role %q is accepted", role)
		}
	}
}

// Every tested release is tested and the reference Version is one of them;
// another release and an unreported version are told apart.
func TestTestedVersions(t *testing.T) {
	if !VersionTested(Version) || VersionUntested(Version) {
		t.Fatalf("reference %s is not tested", Version)
	}
	for _, v := range TestedVersions {
		if VersionUntested(v) {
			t.Errorf("%s is untested", v)
		}
	}
	if !VersionUntested("1.20.0") || VersionUntested("") || TestedVersionList() != "1.19.5, 1.19.6" {
		t.Fatalf("untested 1.20.0 %v, empty %v, list %q", VersionUntested("1.20.0"), VersionUntested(""), TestedVersionList())
	}
}
