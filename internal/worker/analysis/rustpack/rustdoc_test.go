package rustpack

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

func TestRealRustdocPublicItemLinks(t *testing.T) {
	for _, tc := range []struct {
		link string
		hits int
	}{{"MissingItem", 1}, {"build", 0}} {
		t.Run(tc.link, func(t *testing.T) {
			r, d, _, _, _ := realRun(t, KindDoc, "", "cargo-doc", "", domain.Scope{Kind: "full"}, map[string]string{"Cargo.toml": "[package]\nname=\"manual\"\nversion=\"0.1.0\"\nedition=\"2021\"\n", "Cargo.lock": "version = 3\n[[package]]\nname=\"manual\"\nversion=\"0.1.0\"\n", "build.rs": "fn main() {}\n", "src/lib.rs": "//! For details see [`" + tc.link + "`].\npub fn build() {}\n"})
			if r.Status != domain.TaskSucceeded || len(obsOf(d, "rustdoc::broken_intra_doc_links")) != tc.hits {
				t.Fatalf("task %v reason %s, observations %v, limits %v", r.Status, r.Reason, d.Observations, d.Limitations)
			}
			for _, path := range d.Coverage.ProcessedPaths {
				if path == "build.rs" {
					t.Fatal("executed build script counted as documented")
				}
			}

		})
	}
}
