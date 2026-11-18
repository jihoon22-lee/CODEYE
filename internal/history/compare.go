// Package history compares two published report revisions of one project
// and overlays human dispositions (W23, docs/40). It is pure: the caller
// loads the immutable report bodies, the run facts and the review rows. Every
// state and count is derived from those structured records; no model text is
// read.
package history

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// Comparison states (docs/10 §5).
const (
	StateNew              = "new"
	StatePersisting       = "persisting"
	StateChanged          = "changed"
	StateAbsentUnverified = "absent_unverified"
	StateResolvedObserved = "resolved_observed"
)

// States lists the comparison states in display order.
var States = []string{StateNew, StatePersisting, StateChanged, StateAbsentUnverified, StateResolvedObserved}

// Reason codes. On a vanished entry they say why it is not
// resolved_observed; on a new entry, why the baseline could not have seen it.
const (
	ReasonSourceVariant      = "source_variant_differs"
	ReasonScopeReduced       = "scope_reduced"
	ReasonCheckNotRun        = "check_not_run"
	ReasonCheckFailed        = "check_failed"
	ReasonRulesChanged       = "rules_changed"
	ReasonRulesUnknown       = "rules_unknown"
	ReasonLocationNotChecked = "location_not_inspected"
	ReasonModelNotPerformed  = "model_review_not_performed"
	ReasonModelPartial       = "model_review_partial"
	// A model that does not raise a candidate again has not observed that
	// it was fixed: model candidates are never resolved_observed.
	ReasonModelAbsence = "model_absence_not_observation"
	// W19 rule context (docs/39 §5.1, docs/40 §3.3). A tool finding is
	// resolved only when its rule pack and selection and the handling of
	// repository configuration and in-source suppressions are known and
	// equal on both sides.
	ReasonRuleContextUnknown = "rule_context_unknown"
	ReasonRuleContextChanged = "rule_context_changed"
	// The observing side reported this diagnostic as suppressed in source:
	// it was hidden, not fixed (on a new entry: the baseline had hidden it).
	ReasonSuppressedInSource = "suppressed_in_source"
	// The current side applies in-source suppressions of this rule without
	// reporting them (all rules under honored, or the pack's
	// unreported_suppression_rules), so a vanished diagnostic may have been
	// suppressed, not fixed.
	ReasonSuppressionsHidden = "source_suppressions_hidden"
)

// Not-comparable reasons of a whole comparison.
const (
	NotComparableNoBaseline = "no_baseline"
	NotComparableScheme     = "fingerprint_scheme_differs"
)

// Side is one published report revision with the run facts it was made for.
type Side struct {
	RunID     string
	Revision  int
	CreatedAt time.Time
	Profile   string
	Scope     domain.Scope
	// Source is the canonical source declaration of the run's project
	// revision: the source variant (Git repository+branch, or root+path).
	Source json.RawMessage
	Report report.Report
}

// Occurrence is one finding of an entry, without its text.
type Occurrence struct {
	FindingID   string `json:"finding_id"`
	Path        string `json:"path"`
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	Severity    string `json:"severity"`
	Validation  string `json:"validation"`
	Category    string `json:"category"`
	Disposition string `json:"disposition"`
}

// Entry is one fingerprint across both revisions. A fingerprint may cover
// several findings on a side (ambiguous); then the occurrences are not
// paired one to one.
type Entry struct {
	Fingerprint    string       `json:"fingerprint"`
	State          string       `json:"state"`
	Reason         *string      `json:"reason"`
	Ambiguous      bool         `json:"ambiguous"`
	Producer       string       `json:"producer"`
	Analyzer       string       `json:"analyzer"`
	Rule           string       `json:"rule"`
	Paths          []string     `json:"paths"`
	Current        []Occurrence `json:"current"`
	Baseline       []Occurrence `json:"baseline"`
	Changes        []string     `json:"changes"`
	MoveCandidates []string     `json:"move_candidates"`
	Review         *ReviewView  `json:"review"`

	basis   report.FingerprintBasis
	moveKey string
}

// Difference is one environment, rule, pack or scope difference.
type Difference struct {
	Key      string  `json:"key"`
	Baseline *string `json:"baseline"`
	Current  *string `json:"current"`
}

// Comparison of a current revision against a baseline revision.
type Comparison struct {
	Comparable          bool           `json:"comparable"`
	NotComparableReason *string        `json:"not_comparable_reason"`
	Differences         []Difference   `json:"differences"`
	Counts              map[string]int `json:"counts"`
	Entries             []Entry        `json:"entries"`
	// Unidentified counts findings without an fp2 basis (not compared).
	Unidentified struct {
		Current  int `json:"current"`
		Baseline int `json:"baseline"`
	} `json:"unidentified"`
}

func ptr(s string) *string { return &s }

// fp2 reports a revision written with the fp2 fingerprint scheme.
func fp2(r report.Report) bool {
	n, err := strconv.Atoi(r.Builder.Version)
	return err == nil && n >= report.FingerprintSince
}

// NoBaseline is the comparison when no baseline revision exists.
func NoBaseline() Comparison {
	c := Comparison{NotComparableReason: ptr(NotComparableNoBaseline), Differences: []Difference{}, Counts: zeroCounts(), Entries: []Entry{}}
	return c
}

func zeroCounts() map[string]int {
	m := map[string]int{}
	for _, s := range States {
		m[s] = 0
	}
	return m
}

// Compare derives the comparison of cur against base.
func Compare(cur, base Side) Comparison {
	c := Comparison{Differences: differences(cur, base), Counts: zeroCounts(), Entries: []Entry{}}
	if !fp2(cur.Report) || !fp2(base.Report) {
		c.NotComparableReason = ptr(NotComparableScheme)
		return c
	}
	c.Comparable = true
	byFP := map[string]*Entry{}
	var order []string
	add := func(f report.Finding, current bool) {
		if f.FingerprintBasis == nil || !report.ValidFingerprint(f.Fingerprint) {
			if current {
				c.Unidentified.Current++
			} else {
				c.Unidentified.Baseline++
			}
			return
		}
		e := byFP[f.Fingerprint]
		if e == nil {
			b := *f.FingerprintBasis
			e = &Entry{Fingerprint: f.Fingerprint, Producer: b.Producer, Analyzer: b.Analyzer, Rule: b.Rule, Paths: append([]string{}, b.Paths...),
				Current: []Occurrence{}, Baseline: []Occurrence{}, Changes: []string{}, MoveCandidates: []string{}, basis: b, moveKey: b.MoveKey}
			byFP[f.Fingerprint] = e
			order = append(order, f.Fingerprint)
		}
		o := occurrence(f)
		if current {
			e.Current = append(e.Current, o)
		} else {
			e.Baseline = append(e.Baseline, o)
		}
	}
	for _, f := range cur.Report.Findings {
		add(f, true)
	}
	for _, f := range base.Report.Findings {
		add(f, false)
	}
	slices.Sort(order)
	for _, fp := range order {
		e := byFP[fp]
		e.Ambiguous = len(e.Current) > 1 || len(e.Baseline) > 1
		switch {
		case len(e.Baseline) == 0:
			e.State = StateNew
			if r := unverified(fp, e.basis, base, cur, false); r != "" {
				e.Reason = ptr(r)
			}
		case len(e.Current) == 0:
			if r := unverified(fp, e.basis, cur, base, true); r != "" {
				e.State, e.Reason = StateAbsentUnverified, ptr(r)
			} else {
				e.State = StateResolvedObserved
			}
		default:
			e.Changes = changes(e.Current, e.Baseline)
			e.State = StatePersisting
			for _, ch := range e.Changes {
				if ch != "lines" {
					e.State = StateChanged
				}
			}
		}
	}
	// A new entry and a vanished one with the same move key and other paths
	// may be one finding that moved; they are linked, never merged.
	for _, fp := range order {
		e := byFP[fp]
		if e.moveKey == "" || (e.State != StateNew && e.State != StateAbsentUnverified && e.State != StateResolvedObserved) {
			continue
		}
		for _, other := range order {
			o := byFP[other]
			if other == fp || o.moveKey != e.moveKey || slices.Equal(o.Paths, e.Paths) || (e.State == StateNew) == (o.State == StateNew) ||
				(o.State != StateNew && o.State != StateAbsentUnverified && o.State != StateResolvedObserved) {
				continue
			}
			e.MoveCandidates = append(e.MoveCandidates, other)
		}
	}
	for _, fp := range order {
		e := byFP[fp]
		c.Counts[e.State]++
		c.Entries = append(c.Entries, *e)
	}
	return c
}

func occurrence(f report.Finding) Occurrence {
	o := Occurrence{FindingID: f.ID, Severity: f.Severity, Validation: f.Validation, Category: f.Category, Disposition: f.Disposition}
	if len(f.Locations) > 0 {
		l := f.Locations[0]
		o.Path, o.StartLine, o.EndLine = l.Path, l.StartLine, l.EndLine
	}
	return o
}

// changes lists what differs between the two occurrence multisets. Only
// "lines" (a move inside the file) keeps the entry persisting.
func changes(cur, base []Occurrence) []string {
	out := []string{}
	if len(cur) != len(base) {
		out = append(out, "count")
	}
	field := func(name string, get func(Occurrence) string) {
		a, b := make([]string, 0, len(cur)), make([]string, 0, len(base))
		for _, o := range cur {
			a = append(a, get(o))
		}
		for _, o := range base {
			b = append(b, get(o))
		}
		slices.Sort(a)
		slices.Sort(b)
		if !slices.Equal(a, b) {
			out = append(out, name)
		}
	}
	field("severity", func(o Occurrence) string { return o.Severity })
	field("validation", func(o Occurrence) string { return o.Validation })
	field("category", func(o Occurrence) string { return o.Category })
	field("lines", func(o Occurrence) string {
		return o.Path + ":" + strconv.Itoa(o.StartLine) + "-" + strconv.Itoa(o.EndLine)
	})
	return out
}

// covers reports that a run scope includes every path.
func covers(s domain.Scope, paths []string) bool {
	if s.Kind == "full" {
		return true
	}
	if s.Kind != "paths" {
		return false
	}
	for _, p := range paths {
		ok := false
		for _, root := range s.Paths {
			r := strings.TrimSuffix(strings.TrimPrefix(root, "./"), "/")
			if r == "" || r == "." || p == r || strings.HasPrefix(p, r+"/") {
				ok = true
			}
		}
		if !ok {
			return false
		}
	}
	return true
}

func succeededTool(r report.Report, tc report.ToolCoverage) bool {
	status := ""
	for _, t := range r.Tasks {
		if t.Key == tc.TaskKey {
			status = t.Status
		}
	}
	return status == "succeeded" && tc.ProcessedFiles != nil && !tc.PartialParse &&
		(tc.ExitClass == "success_with_findings" || tc.ExitClass == "success_without_findings") &&
		!slices.Contains(r.Limitations, report.LimArtifactRejected+":"+tc.TaskKey)
}

func toolsOf(r report.Report, analyzer string) []report.ToolCoverage {
	var out []report.ToolCoverage
	for _, tc := range r.Coverage.Tools {
		if tc.Analyzer == analyzer {
			out = append(out, tc)
		}
	}
	return out
}

func versions(tcs []report.ToolCoverage) ([]string, bool) {
	var out []string
	for _, tc := range tcs {
		if tc.ToolVersion == nil || *tc.ToolVersion == "" {
			return nil, false
		}
		out = append(out, *tc.ToolVersion)
	}
	slices.Sort(out)
	return slices.Compact(out), true
}

// ruleContexts are the rule-context keys of analyzer's rows and whether all
// of them are known. Like versions, every row counts (the
// tool:<analyzer>:rule_context difference shows the same rows). A row
// without a context is unknown, except go vet's, which report.RuleContextOf
// synthesizes (revisions before builder 4 and the go vet document carry
// none).
func ruleContexts(r report.Report, analyzer string) (keys []string, known bool) {
	known = true
	for _, tc := range toolsOf(r, analyzer) {
		c := report.RuleContextOf(tc)
		if c == nil || !c.Known() {
			known = false
			continue
		}
		keys = append(keys, c.Key())
	}
	slices.Sort(keys)
	return slices.Compact(keys), known
}

// suppressionsHidden reports a row of analyzer that ran and may hide an
// in-source suppression of rule without reporting it: suppressions honored,
// or reported except for this rule (staticcheck U1000).
func suppressionsHidden(r report.Report, analyzer, rule string) bool {
	for _, tc := range toolsOf(r, analyzer) {
		if c := report.RuleContextOf(tc); tc.ExitClass != "not_run" && c != nil && c.HidesSuppressionsOf(rule) {
			return true
		}
	}
	return false
}

// reviewRules is the role/model/pack identity of the accepted review calls.
func reviewRules(r report.Report) []string {
	var out []string
	for _, c := range r.Coverage.ReviewCalls {
		out = append(out, c.Role+"\x00"+c.Model+"\x00"+c.PackDigest)
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// unverified returns why observed did not run the check that produced (or
// would produce) this basis over its location with the rules of other, or
// "" when it did. For a vanished entry observed is the current side; for a
// new entry it is the baseline.
func unverified(fp string, b report.FingerprintBasis, observed, other Side, vanished bool) string {
	if !bytes.Equal(observed.Source, other.Source) {
		return ReasonSourceVariant
	}
	if !covers(observed.Scope, b.Paths) {
		return ReasonScopeReduced
	}
	if b.Producer == report.ProducerModel {
		switch observed.Report.Coverage.AIReview.Status {
		case report.AIReviewPerformed:
		case report.AIReviewPartial:
			return ReasonModelPartial
		default:
			return ReasonModelNotPerformed
		}
		if !slices.Equal(reviewRules(observed.Report), reviewRules(other.Report)) {
			return ReasonRulesChanged
		}
		if !vanished {
			return ""
		}
		return ReasonModelAbsence
	}
	tcs := toolsOf(observed.Report, b.Analyzer)
	if len(tcs) == 0 {
		for _, tc := range toolsOf(other.Report, b.Analyzer) {
			for _, t := range observed.Report.Tasks {
				if t.Key == tc.TaskKey {
					// Planned, but no accepted result (failed task, rejected
					// artifact or a document no parser understood).
					return ReasonCheckFailed
				}
			}
		}
		return ReasonCheckNotRun
	}
	failed := false
	for _, tc := range tcs {
		if !succeededTool(observed.Report, tc) {
			failed = true
		}
	}
	a, okA := versions(tcs)
	o, okO := versions(toolsOf(other.Report, b.Analyzer))
	inspected := true
	for _, p := range b.Paths {
		seen := false
		for _, tc := range tcs {
			if succeededTool(observed.Report, tc) && slices.Contains(tc.ProcessedPaths, p) {
				seen = true
			}
		}
		inspected = inspected && seen
	}
	ca, knownA := ruleContexts(observed.Report, b.Analyzer)
	co, knownO := ruleContexts(other.Report, b.Analyzer)
	// Precedence (docs/40 §3.3): the location was not inspected; the
	// observing side reported the diagnostic suppressed; the tool version;
	// the rule context; unreported suppressions on the current side.
	switch {
	case !inspected && failed:
		return ReasonCheckFailed
	case !inspected:
		return ReasonLocationNotChecked
	case report.SuppressedFingerprints(observed.Report, b.Analyzer)[fp]:
		return ReasonSuppressedInSource
	case !okA || (!okO && len(toolsOf(other.Report, b.Analyzer)) > 0):
		return ReasonRulesUnknown
	case okO && len(o) > 0 && !slices.Equal(a, o):
		return ReasonRulesChanged
	case !knownA || (!knownO && len(toolsOf(other.Report, b.Analyzer)) > 0):
		return ReasonRuleContextUnknown
	case knownO && len(co) > 0 && !slices.Equal(ca, co):
		return ReasonRuleContextChanged
	case vanished && suppressionsHidden(observed.Report, b.Analyzer, b.Rule):
		return ReasonSuppressionsHidden
	}
	return ""
}

func sourceLabel(raw json.RawMessage) *string {
	var s domain.Source
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return nil
	}
	if s.Kind == domain.SourceGit {
		return ptr("git " + s.RepositoryURL + " @" + s.Branch)
	}
	return ptr("local " + s.SourceRootID + ":" + s.RelativePath)
}

func scopeLabel(s domain.Scope) string {
	if s.Kind == "paths" {
		return "paths: " + strings.Join(s.Paths, ", ")
	}
	return s.Kind
}

func differences(cur, base Side) []Difference {
	out := []Difference{}
	diff := func(key string, b, c *string) {
		if (b == nil) != (c == nil) || (b != nil && *b != *c) {
			out = append(out, Difference{Key: key, Baseline: b, Current: c})
		}
	}
	if !bytes.Equal(cur.Source, base.Source) {
		diff("source_variant", sourceLabel(base.Source), sourceLabel(cur.Source))
	}
	revision := func(s Side) *string {
		if s.Report.Inputs.CommitSHA != nil {
			return ptr(*s.Report.Inputs.CommitSHA)
		}
		return ptr(s.Report.Inputs.ManifestSHA256)
	}
	diff("source_revision", revision(base), revision(cur))
	diff("profile", ptr(base.Profile), ptr(cur.Profile))
	diff("scope", ptr(scopeLabel(base.Scope)), ptr(scopeLabel(cur.Scope)))
	diff("builder_version", ptr(base.Report.Builder.Version), ptr(cur.Report.Builder.Version))
	analyzers := map[string]bool{}
	for _, s := range []Side{base, cur} {
		for _, tc := range s.Report.Coverage.Tools {
			analyzers[tc.Analyzer] = true
		}
	}
	names := make([]string, 0, len(analyzers))
	for a := range analyzers {
		names = append(names, a)
	}
	slices.Sort(names)
	for _, a := range names {
		label := func(s Side, what string) *string {
			tcs := toolsOf(s.Report, a)
			if len(tcs) == 0 {
				return nil
			}
			var parts []string
			for _, tc := range tcs {
				switch what {
				case "rule_context":
					v := "unknown"
					if c := report.RuleContextOf(tc); c != nil {
						v = c.Label()
					}
					parts = append(parts, v)
				case "version":
					v := "unknown"
					if tc.ToolVersion != nil {
						v = *tc.ToolVersion
					}
					parts = append(parts, v)
				case "result":
					state := "failed"
					if succeededTool(s.Report, tc) {
						state = "succeeded"
					}
					parts = append(parts, tc.TaskKey+"="+state+"/"+tc.ExitClass)
				case "processed_files":
					n := "unknown"
					if tc.ProcessedFiles != nil {
						n = strconv.Itoa(*tc.ProcessedFiles)
					}
					parts = append(parts, tc.TaskKey+"="+n+"/"+strconv.Itoa(tc.FilesInScope))
				}
			}
			slices.Sort(parts)
			return ptr(strings.Join(slices.Compact(parts), ", "))
		}
		for _, what := range []string{"version", "rule_context", "result", "processed_files"} {
			diff("tool:"+a+":"+what, label(base, what), label(cur, what))
		}
	}
	diff("ai_review", ptr(base.Report.Coverage.AIReview.Status), ptr(cur.Report.Coverage.AIReview.Status))
	roles := map[string][2]*string{}
	for i, s := range []Side{base, cur} {
		for _, call := range s.Report.Coverage.ReviewCalls {
			v := roles[call.Role]
			v[i] = ptr(call.Model + " / " + call.PackDigest)
			roles[call.Role] = v
		}
	}
	keys := make([]string, 0, len(roles))
	for k := range roles {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		diff("review:"+k, roles[k][0], roles[k][1])
	}
	return out
}
