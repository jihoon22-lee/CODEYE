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
// of the static composite (an analyzer obsdoc.IsSecretAnalyzer names) runs
// before every review role (the worker runs tasks in plan order and the
// review handlers append their roles after the static plan), so its
// envelope is terminal when a role reads it. Three cases:
//
//   - not run (the stand-in's unsupported/not_run envelope, or no secrets
//     task planned): inputs are sent unscreened and the record says so
//     (model_input_not_secret_screened), never silently;
//   - ran but unusable (failed, timed out, cancelled, partial parse, a
//     missing or invalid envelope): fail closed, the role is skipped with
//     secret_screen_failed and nothing reaches a model;
//   - succeeded with a complete parse: every file it reported is withheld
//     from every excerpt (secret_files_withheld).
type screenState int

const (
	screenUnscreened screenState = iota
	screenApplied
	screenFailed
)

const (
	limNotSecretScreened     = "model_input_not_secret_screened"
	limSecretWithheld        = "secret_files_withheld"
	reasonSecretScreenFailed = "secret_screen_failed"
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
		out.state = screenFailed
		return out
	}
	var names []string
	lister, known := static.(descriptorLister)
	if known {
		for _, d := range lister.Descriptors() {
			if obsdoc.IsSecretAnalyzer(d.ID) {
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
	applied, notRun := false, false
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
		case !known && (err != nil || !obsdoc.IsSecretAnalyzer(d.Analyzer.ID)):
			// Without descriptors only a readable envelope names its analyzer.
			continue
		case err != nil || !obsdoc.IsSecretAnalyzer(d.Analyzer.ID):
			out.state = screenFailed
			return out
		case d.Outcome.TaskStatus == domain.TaskUnsupported && d.Outcome.ExitClass == obsdoc.ExitNotRun:
			notRun = true
		case d.Outcome.TaskStatus == domain.TaskSucceeded && !d.Outcome.PartialParse && d.Parse.OK:
			applied = true
			for _, o := range d.Observations {
				if o.Path != nil {
					out.withheld[*o.Path] = true
				}
				if o.Location != nil {
					out.withheld[o.Location.Path] = true
				}
			}
		default:
			out.state = screenFailed
			return out
		}
	}
	if applied && !notRun {
		out.state = screenApplied
	}
	return out
}
