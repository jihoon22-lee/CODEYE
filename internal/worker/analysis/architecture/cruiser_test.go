package architecture

import (
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack/packtest"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// The module list of dependency-cruiser 18.8 holds the dependency targets it
// did not follow next to the cruised sources: a Node core module, a package
// that did not resolve and a path doNotFollow stops at. Real projects import
// such targets, so they must not make the output unparsed.
func TestParseCruiserSkipsTargetsThatWereNotFollowed(t *testing.T) {
	fx := packtest.NewFixture(t, "", map[string]string{"a.js": "require('./b.js');\nrequire('fs');\nrequire('left-pad');\n", "b.js": "module.exports = 1;\n"}, domain.Scope{})
	var files []source.Entry
	for _, e := range fx.Job.Snapshot.Manifest.Entries {
		if e.Kind == "file" {
			files = append(files, e)
		}
	}
	body := []byte(`{"modules":[
 {"source":"a.js","dependencies":[{"couldNotResolve":false},{"couldNotResolve":false},{"couldNotResolve":true}],"valid":true},
 {"source":"b.js","dependencies":[],"valid":true},
 {"source":"fs","followable":false,"coreModule":true,"couldNotResolve":false,"matchesDoNotFollow":false,"dependencies":[]},
 {"source":"left-pad","followable":false,"coreModule":false,"couldNotResolve":true,"matchesDoNotFollow":false,"dependencies":[]},
 {"source":"../package.json","followable":false,"coreModule":false,"couldNotResolve":true,"matchesDoNotFollow":true,"dependencies":[]}],
 "summary":{"violations":[],"totalCruised":5}}`)
	doc := obsdoc.New(obsdoc.Analyzer{ID: "javascript.dependency_cruiser", Ecosystem: "javascript"}, obsdoc.Component{ID: "x", Version: "1"}, obsdoc.Component{ID: "x", Version: "1"}, "dependency-cruiser", fx.Job.Snapshot.Digest)
	var outcome anypack.Outcome
	parseCruiser(&anypack.Run{Job: fx.Job}, files, &doc, &outcome, body)
	if outcome.Failed() || len(doc.Coverage.ProcessedPaths) != 2 || doc.Coverage.Units["dependency_targets_not_followed"] != 3 ||
		doc.Coverage.Units["dependency_unresolved"] != 1 || doc.Parse.ShapeErrors != 0 {
		t.Fatalf("outcome %+v coverage %+v parse %+v", outcome, doc.Coverage, doc.Parse)
	}

	// A listed path that is neither a selected source nor a target that
	// was not followed is still a shape error.
	doc = obsdoc.New(doc.Analyzer, doc.Adapter, doc.Parser, doc.TaskKey, doc.ManifestDigest)
	outcome = anypack.Outcome{}
	parseCruiser(&anypack.Run{Job: fx.Job}, files, &doc, &outcome, []byte(`{"modules":[{"source":"a.js","dependencies":[]},{"source":"c.js","dependencies":[]}],"summary":{"violations":[],"totalCruised":2}}`))
	if outcome.Reason != anypack.ReasonOutputUnparsed || doc.Parse.ShapeErrors != 1 {
		t.Fatalf("unknown source: %+v %+v", outcome, doc.Parse)
	}

	// Only targets and no source: nothing was analyzed.
	doc = obsdoc.New(doc.Analyzer, doc.Adapter, doc.Parser, doc.TaskKey, doc.ManifestDigest)
	outcome = anypack.Outcome{}
	parseCruiser(&anypack.Run{Job: fx.Job}, files, &doc, &outcome, []byte(`{"modules":[{"source":"fs","coreModule":true,"followable":false,"dependencies":[]}],"summary":{"violations":[],"totalCruised":1}}`))
	if outcome.Reason != "dependency_cruiser_no_parsed_sources" {
		t.Fatalf("targets only: %+v", outcome)
	}
}
