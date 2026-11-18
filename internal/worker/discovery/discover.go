// Package discovery collects module, environment and document facts from an
// immutable W04 snapshot without a model (W15, docs/37 §5). The pure part
// (module detection, manifest parsing, verdicts, document inventory) works on
// the verified manifest and a read function; the handler supplies reads
// through os.Root on the read-only snapshot and runs probes only through the
// attempt's executor.Runner. Repository documents, including agent
// instructions, are data: nothing read here can change tools, permissions,
// network policy or the plan.
package discovery

import (
	"errors"
	"path"
	"slices"
	"sort"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// Input identifies the snapshot and attempt a document is derived for.
type Input struct {
	Manifest         source.Manifest
	Digest           string
	Scope            domain.Scope
	RunID, AttemptID string
}

var (
	errScope     = errors.New("scope_unsupported")
	errIntegrity = errors.New("snapshot_integrity_failed")
)

func (in Input) document(kind string) (envdoc.Document, error) {
	scope, ok := normalScope(in.Scope)
	if !ok {
		return envdoc.Document{}, errScope
	}
	modules, lims := DetectModules(in.Manifest, scope)
	return envdoc.Document{SchemaVersion: envdoc.SchemaVersion, Kind: kind, Snapshot: in.Digest, RunID: in.RunID, AttemptID: in.AttemptID,
		Scope: scope, Modules: modules, Limitations: lims}, nil
}

// Expect returns the validation expectation for documents of this input.
func (in Input) Expect(kind string) envdoc.Expect {
	files := included(in.Manifest)
	return envdoc.Expect{Snapshot: in.Digest, RunID: in.RunID, AttemptID: in.AttemptID, Kind: kind,
		Lookup: func(p string) (string, bool) { e, ok := files[p]; return e.SHA256, ok }}
}

func uniqueSorted(list []string) []string {
	out := append([]string{}, list...)
	sort.Strings(out)
	return slices.Compact(out)
}

// Environment builds the environment document. observe is called once with
// the tools the in-scope modules need; its observations are this node's only.
func Environment(in Input, read readFunc, observe func(tools []string) Observed) (envdoc.Document, Observed, error) {
	doc, err := in.document(envdoc.KindEnvironment)
	if err != nil {
		return envdoc.Document{}, Observed{}, err
	}
	files := included(in.Manifest)
	env := envdoc.Environment{Manifests: []envdoc.ManifestRead{}, Requirements: []envdoc.Requirement{}, Observations: []envdoc.Observation{}, Verdicts: []envdoc.Verdict{}}
	lims := doc.Limitations
	byModule := map[string][]envdoc.Requirement{}
	unreadable := map[string]bool{} // module id + NUL + tool
	var need []string
	for _, m := range doc.Modules {
		if !m.InScope {
			continue
		}
		need = append(need, envdoc.PrimaryTools[m.Ecosystem]...)
		for _, p := range m.Manifests {
			e := files[p]
			mr := envdoc.ManifestRead{Path: p, SHA256: e.SHA256}
			switch {
			case *e.Size > maxManifestBytes:
				mr.Status = envdoc.ManifestTooLarge
				lims = append(lims, LimManifestNotRead)
			case e.Encoding != "utf-8":
				mr.Status = envdoc.ManifestNotUTF8
				lims = append(lims, LimManifestNotRead)
			default:
				b, err := read(e)
				if err != nil {
					return envdoc.Document{}, Observed{}, errIntegrity
				}
				decl, parsed, perr := parseManifest(p, b)
				switch {
				case !parsed:
					mr.Status = envdoc.ManifestNoParser
				case perr != nil:
					mr.Status = envdoc.ManifestMalformed
					lims = append(lims, LimManifestMalformed)
				default:
					mr.Status = envdoc.ManifestParsed
					for _, d := range decl {
						r := envdoc.Requirement{ID: envdoc.RequirementID(d.Kind, p), Module: m.ID, Tool: envdoc.ToolOf[d.Kind], Kind: d.Kind,
							Constraint: d.Constraint, Comparison: envdoc.Classify(d.Kind, d.Constraint),
							Source:     envdoc.Location{Path: p, SHA256: e.SHA256, StartLine: d.Start, EndLine: d.End},
							EvidenceID: record.ExcerptID(in.Digest, p, e.SHA256, d.Start, d.End)}
						byModule[m.ID] = append(byModule[m.ID], r)
						env.Requirements = append(env.Requirements, r)
						need = append(need, r.Tool)
						lims = append(lims, d.Limitations...)
						if r.Comparison == envdoc.CompareUnsupported {
							lims = append(lims, LimConstraintNotCompared)
						}
						if d.Kind == "go_work_directive" || d.Kind == "go_work_toolchain" {
							lims = append(lims, LimGoWorkIgnoredByGoVet)
						}
					}
				}
			}
			if mr.Status == envdoc.ManifestMalformed || mr.Status == envdoc.ManifestNotUTF8 || mr.Status == envdoc.ManifestTooLarge {
				unreadable[m.ID+"\x00"+envdoc.DeclaringTool(path.Base(p))] = true
			}
			env.Manifests = append(env.Manifests, mr)
		}
	}
	need = uniqueSorted(need)
	observed := Observed{}
	if len(need) > 0 {
		observed = observe(need)
	}
	if observed.Err != nil {
		return envdoc.Document{}, observed, observed.Err
	}
	obs := map[string]envdoc.Observation{}
	for _, o := range observed.Observations {
		if !slices.Contains(need, o.Tool) {
			continue
		}
		if o.Status == envdoc.ObsObserved && (o.Version == nil || !envdoc.ComparableObservation(o.Tool, *o.Version)) {
			o = probeFailed(o.Tool, DetailOutputUnrecognized, o.Execution)
		}
		obs[o.Tool] = o
	}
	for _, t := range need {
		o, ok := obs[t]
		if !ok {
			o = notProbed(t) // an observer that skips a tool never implies a value
			obs[t] = o
		}
		env.Observations = append(env.Observations, o)
		switch o.Status {
		case envdoc.ObsNotProbed:
			lims = append(lims, LimToolNotProbed)
		case envdoc.ObsNotFound, envdoc.ObsProbeFailed:
			lims = append(lims, LimToolProbeFailed)
		}
	}
	for _, m := range doc.Modules {
		if !m.InScope {
			continue
		}
		declaredTools := map[string]bool{}
		for _, r := range byModule[m.ID] {
			declaredTools[r.Tool] = true
			id := r.ID
			env.Verdicts = append(env.Verdicts, verdict(m.ID, &r, &id, obs[r.Tool]))
		}
		for _, t := range envdoc.PrimaryTools[m.Ecosystem] {
			if !declaredTools[t] {
				v := verdict(m.ID, nil, nil, obs[t])
				if unreadable[m.ID+"\x00"+t] {
					reason := envdoc.ReasonDeclarationUnreadable
					v.Reason = &reason
				}
				env.Verdicts = append(env.Verdicts, v)
			}
		}
	}
	sort.Slice(env.Manifests, func(i, j int) bool { return env.Manifests[i].Path < env.Manifests[j].Path })
	sort.Slice(env.Requirements, func(i, j int) bool { return env.Requirements[i].ID < env.Requirements[j].ID })
	sort.Slice(env.Verdicts, func(i, j int) bool {
		a, b := env.Verdicts[i], env.Verdicts[j]
		if a.Module != b.Module {
			return a.Module < b.Module
		}
		if a.Tool != b.Tool {
			return a.Tool < b.Tool
		}
		return deref(a.Requirement) < deref(b.Requirement)
	})
	doc.Environment = &env
	doc.Limitations = uniqueSorted(lims)
	return doc, observed, nil
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// verdict compares one requirement (nil: none declared) with the node
// observation of its tool. Reasons follow a fixed precedence: not_declared,
// unsupported_constraint, not_observed.
func verdict(module string, r *envdoc.Requirement, id *string, o envdoc.Observation) envdoc.Verdict {
	v := envdoc.Verdict{Module: module, Tool: o.Tool, Requirement: id}
	unknown := func(reason string) envdoc.Verdict {
		v.Status, v.Reason = envdoc.VerdictUnknown, &reason
		return v
	}
	switch {
	case r == nil:
		return unknown(envdoc.ReasonNotDeclared)
	case r.Comparison == envdoc.CompareUnsupported:
		return unknown(envdoc.ReasonUnsupportedConstraint)
	case o.Status != envdoc.ObsObserved || o.Version == nil:
		return unknown(envdoc.ReasonNotObserved)
	}
	v.Status = envdoc.Decide(r.Kind, r.Constraint, *o.Version)
	return v
}

// Documents builds the documents document.
func Documents(in Input, budget Budget, read readFunc) (envdoc.Document, error) {
	if !budget.valid() {
		return envdoc.Document{}, errors.New("invalid document budget")
	}
	doc, err := in.document(envdoc.KindDocuments)
	if err != nil {
		return envdoc.Document{}, err
	}
	ds, lims, err := Inventory(in.Manifest, doc.Scope, doc.Modules, budget, read)
	if err != nil {
		return envdoc.Document{}, errIntegrity
	}
	doc.Documents = &ds
	doc.Limitations = uniqueSorted(append(doc.Limitations, lims...))
	return doc, nil
}
