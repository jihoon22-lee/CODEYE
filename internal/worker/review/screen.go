package review

import (
	"context"
	"slices"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Secret screening of model inputs (docs/ANALYZERS.md §보안). The secrets task
// of the static composite (an analyzer of purpose secrets, docs/ANALYZERS.md#분석기-목적) runs
// before every review role (the worker runs tasks in plan order and the
// review handlers append their roles after the static plan), so its
// envelope is terminal when a role reads it. Two outcomes:
//
//   - succeeded with a complete parse: every file it reported is withheld
//     from every excerpt (secret_files_withheld);
//   - anything else (no secrets task planned, the stand-in's
//     unsupported/not_run envelope, or a task that ran but whose result is
//     unusable: failed, timed out, cancelled, partial parse, a missing or
//     invalid envelope): inputs are sent through the read filters
//     (sensitive-value patterns, withheld files) and the record says so
//     (model_input_not_secret_screened), never silently. What a secrets
//     result did report is still withheld: the files of a completed scan,
//     and the observation paths of a failed or partial envelope that still
//     decodes (bound to the manifest). Only a result nothing can be read
//     from withholds nothing. The report shows the secrets task's own status
//     and reason next to that state.
type screenState int

const (
	screenUnscreened screenState = iota
	screenApplied
)

const (
	limNotSecretScreened = "model_input_not_secret_screened"
	limSecretWithheld    = "secret_files_withheld"
)

type secretScreen struct {
	state    screenState
	withheld map[string]bool
}

// descriptorLister is the static composite (pack.Composite): it names the
// analyzer of every task, so a secrets task without a readable envelope is
// still recognized.
type descriptorLister interface{ Descriptors() []pack.Descriptor }

func screenSecrets(ctx context.Context, static analysis.Handler, j analysis.Job) secretScreen {
	out := secretScreen{withheld: map[string]bool{}}
	if static == nil {
		return out
	}
	plan, err := static.Plan(ctx, j)
	if err != nil {
		return out
	}
	var names []string
	lister, known := static.(descriptorLister)
	if known {
		for _, d := range lister.Descriptors() {
			if d.Purpose == obsdoc.PurposeSecrets {
				names = append(names, d.Name)
			}
		}
	}
	index := map[string]string{}
	for _, e := range j.Snapshot.Manifest.Entries {
		if e.Kind == "file" && e.ExcludedReason == "" && e.SHA256 != "" {
			index[e.Path] = e.SHA256
		}
	}
	lookup := func(p string) (string, bool) { s, ok := index[p]; return s, ok }
	applied, notRun, unusable := false, false, false
	withhold := func(d obsdoc.Document) {
		for _, o := range d.Observations {
			if o.Path != nil {
				out.withheld[*o.Path] = true
			}
			if o.Location != nil {
				out.withheld[o.Location.Path] = true
			}
		}
	}
	for _, t := range plan {
		if !record.AnalyzerTaskKey(t.Key) {
			continue
		}
		secretTask := slices.ContainsFunc(names, func(n string) bool { return pack.Owns(n, t.Key) })
		if known && !secretTask {
			continue
		}
		var d obsdoc.Document
		b, err := readBounded(obsdoc.ArtifactPath(j.Scratch, t.Key), obsdoc.MaxBytes)
		if err == nil {
			d, err = obsdoc.Decode(b, obsdoc.Expect{TaskKey: t.Key, Snapshot: j.Snapshot.Digest, Lookup: lookup})
		}
		switch {
		case !known && (err != nil || !d.Analyzer.IsSecrets()):
			// Without descriptors only a readable envelope names its analyzer.
			continue
		case err != nil || !d.Analyzer.IsSecrets():
			// Nothing can be read from it: unscreened, with what other
			// secrets results reported still withheld.
			unusable = true
		case d.Outcome.TaskStatus == domain.TaskUnsupported && d.Outcome.ExitClass == obsdoc.ExitNotRun:
			notRun = true
		case d.Outcome.TaskStatus == domain.TaskSucceeded && !d.Outcome.PartialParse && d.Parse.OK:
			applied = true
			withhold(d)
		default:
			// Failed or partial but readable: unscreened, and every file it
			// did report is withheld all the same.
			unusable = true
			withhold(d)
		}
	}
	if applied && !notRun && !unusable {
		out.state = screenApplied
	}
	return out
}
