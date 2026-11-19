package report

import (
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/deps/depdoc"
)

// DependenciesDecoder validates the record every deps.* task writes against
// the attempt, the task and the snapshot manifest. It adds no observation,
// finding or tool coverage; the accepted record is one row of the
// environment section's dependency list. Without a decoder the builder
// would flag the media type as unrecognized and downgrade the report.
type DependenciesDecoder struct{}

func (DependenciesDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: depdoc.MediaType, SchemaVersion: depdoc.SchemaVersion, Kind: depdoc.Kind}
}

func (DependenciesDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if !depdoc.ValidTaskKey(ctx.Task.Key) || art.Role != "other" {
		return Contribution{}, invalidf("dependency record attached to unexpected task or role")
	}
	rec, err := depdoc.Decode(body, depdoc.Expect{Snapshot: ctx.Snapshot.ManifestDigest, RunID: ctx.RunID, AttemptID: ctx.AttemptID, TaskKey: ctx.Task.Key, Lookup: ctx.Source.Lookup})
	if err != nil {
		return Contribution{}, err
	}
	if rec.TaskStatus != ctx.Task.Status {
		return Contribution{}, invalidf("dependency record status differs from the task")
	}
	return Contribution{Dependencies: &rec}, nil
}

// Dependency gap codes (docs/REPORT.md#분석-범위와-한계).
const (
	GapDependenciesNotPrepared = "dependencies_not_prepared"
	GapDependenciesNotFetched  = "dependencies_not_fetched"
	GapVersionsNotPinned       = "dependency_versions_not_pinned"
)

// dependencyReasons are analyzer task reasons that mean "a dependency the
// code imports could not be resolved".
var dependencyReasons = map[string]bool{"dependency_unavailable": true}

// dependencies lists the deps.* tasks in key order: the accepted record's
// facts, or only the task's when its record did not reach the report.
func (c sectionContext) dependencies() []EnvDependency {
	var out []EnvDependency
	for _, k := range c.keys {
		if !depdoc.IsTask(k) {
			continue
		}
		st := c.states[k]
		d := EnvDependency{TaskKey: k, TaskStatus: st.facts.Status, TaskReason: st.facts.Reason, Result: DiscoveryMissing, Limitations: []string{}, FailedItems: []EnvDependencyFailure{}}
		switch {
		case st.rejected:
			d.Result = DiscoveryRejected
		case st.dependencies != nil:
			r := st.dependencies
			d.Result = DiscoveryAccepted
			d.Module, d.Ecosystem, d.Path = strPtr(r.Module.ID), strPtr(r.Module.Ecosystem), strPtr(r.Module.Path)
			d.Manager, d.ManagerVersion = strPtr(r.Manager.Name), r.Manager.Version
			d.State, d.Reason = strPtr(r.State), r.Reason
			d.Pinned = &r.Pinned
			if r.Lockfile != nil {
				d.Lockfile = strPtr(r.Lockfile.Path)
			}
			d.Declared, d.Prepared, d.Failed = r.Counts.Declared, &r.Counts.Prepared, &r.Counts.Failed
			d.CacheKind = strPtr(r.Cache.Kind)
			for _, f := range r.FailedItems {
				d.FailedItems = append(d.FailedItems, EnvDependencyFailure{Name: f.Name, Reason: f.Reason})
			}
			d.Limitations = append(d.Limitations, r.Limitations...)
		}
		out = append(out, d)
	}
	return out
}

// dependencyGaps are the environment gaps of the dependency tasks and of
// analyzer tasks that could not resolve dependencies no task fetched.
func (c sectionContext) dependencyGaps(add func(section, code string, subject, detail *string)) {
	r := c.r
	if r.Environment == nil {
		return
	}
	fetched := map[string]bool{}
	for _, d := range r.Environment.Dependencies {
		if d.Ecosystem != nil {
			fetched[*d.Ecosystem] = true
		}
		nothing := d.TaskStatus == string(domain.TaskSkippedPolicy) && d.TaskReason != nil && domain.NothingToFetch(*d.TaskReason)
		if d.TaskStatus != string(domain.TaskSucceeded) && !nothing {
			add(SectionEnvironment, GapDependenciesNotPrepared, strPtr(d.TaskKey), d.TaskReason)
		}
		if d.Pinned != nil && !*d.Pinned || hasCode(d.Limitations, depdoc.LimVersionsNotPinned) {
			add(SectionEnvironment, GapVersionsNotPinned, strPtr(d.TaskKey), nil)
		}
	}
	// Python type checkers succeed with unresolved imports; their coverage
	// says no third-party package was installed.
	unresolved := map[string]bool{}
	for _, tc := range r.Coverage.Tools {
		if tc.Packages[pythonImportUnresolved] > 0 && hasCode(tc.Limitations, pythonNoThirdParty) {
			unresolved[tc.TaskKey] = true
		}
	}
	if s := r.StaticAnalysis; s != nil {
		for _, l := range s.Languages {
			if l.Ecosystem == nil || fetched[*l.Ecosystem] || hasDependencyTask(c.keys, *l.Ecosystem) {
				continue
			}
			for _, t := range l.Tools {
				switch {
				case t.TaskReason != nil && dependencyReasons[*t.TaskReason]:
					add(SectionEnvironment, GapDependenciesNotFetched, strPtr(t.TaskKey), t.TaskReason)
				case unresolved[t.TaskKey]:
					add(SectionEnvironment, GapDependenciesNotFetched, strPtr(t.TaskKey), strPtr(pythonImportsUnresolved))
				}
			}
		}
	}
}

// The Python type checkers' codes for imports no installed package
// resolved (docs/ANALYZERS.md#importstub-공백).
const (
	pythonImportUnresolved  = "import_unresolved"
	pythonNoThirdParty      = "third_party_packages_not_installed"
	pythonImportsUnresolved = "imports_unresolved"
)

func hasDependencyTask(keys []string, ecosystem string) bool {
	base := depdoc.TaskKey(ecosystem, "")
	for _, k := range keys {
		if k == base || strings.HasPrefix(k, base+".") {
			return true
		}
	}
	return false
}

func hasCode(list []string, code string) bool {
	for _, x := range list {
		if x == code {
			return true
		}
	}
	return false
}
