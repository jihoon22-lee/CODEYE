package report

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// W23 finding identity (docs/10 §5, docs/40). This file is the only place
// that defines fingerprint semantics; decoders call ToolFingerprint or
// setModelFingerprint and never hash their own fields.
//
// A fingerprint identifies a lasting issue across runs of one project and
// source variant. Its inputs are the producer and analyzer, the rule (tool)
// or cause class (model), the canonical snapshot paths, the cited symbols,
// a normalized code-context hash when a producer supplies one, and a
// normalized condition class. Line numbers, columns, snapshot/run/attempt
// IDs, model-written titles, verifier categories and evidence order are never
// inputs, so a line move or a reworded title keeps the fingerprint. The
// project and source variant are the namespace in which fingerprints are
// compared (comparison and dispositions are per project, baselines per source
// variant); they are not hashed, so the same identity is used on every branch.
//
// Fingerprints are not unique inside a report: two diagnostics with the same
// rule and message in one file share one. Consumers must treat a fingerprint
// as a group key, never as a one-to-one pairing.
const (
	// FingerprintScheme prefixes every fingerprint of this definition.
	// Reports of builder version 2 and earlier carry other formats
	// ("go-vet-fp-v1:…" or a bare digest) that are kept as stored and are not
	// comparable with this scheme.
	FingerprintScheme = "fp2"
	// FingerprintSince is the first builder version that writes fp2.
	FingerprintSince = 3
	// ContextUnavailable marks a basis without a normalized code-context
	// hash: the controller holds no snapshot bytes and current producers
	// (go vet observations, review records) do not record one.
	ContextUnavailable = "unavailable"

	ProducerTool  = "tool"
	ProducerModel = "model"
	// AnalyzerReview is the analyzer of model review candidates.
	AnalyzerReview = "review"
	// CauseCandidate is the cause class of a model candidate. Verifier
	// categories are not used: they exist only when verification ran.
	CauseCandidate = "candidate"
)

// FingerprintBasis is the recorded, non-secret part of the fingerprint
// input. The normalized condition is hashed but not stored, because for tool
// findings it is tool text (code). Symbols are removed from the code-free
// export like every other symbol.
type FingerprintBasis struct {
	Scheme   string   `json:"scheme"`
	Producer string   `json:"producer"`
	Analyzer string   `json:"analyzer"`
	Rule     string   `json:"rule"`
	Paths    []string `json:"paths"`
	Symbols  []string `json:"symbols"`
	Context  string   `json:"context"`
	// MoveKey is the same identity without the paths. Equal move keys on a
	// new and a vanished entry are a candidate move or rename only; they are
	// never merged automatically. Empty when nothing but the path would
	// identify the finding (a model candidate without symbol or observation).
	MoveKey string `json:"move_key"`
}

var (
	fingerprintPattern = regexp.MustCompile(`^fp2:[0-9a-f]{64}$`)
	// A file position inside a message: "dir/file.go:12" or ":12:3".
	positionPattern = regexp.MustCompile(`(?:[A-Za-z]:)?[\w./\\-]*\.\w+:\d+(?::\d+)?`)
	linePattern     = regexp.MustCompile(`(?i)\b(lines?) \d+(?:\s*[-–]\s*\d+)?`)
	spacePattern    = regexp.MustCompile(`\s+`)
)

// ValidFingerprint reports an fp2 fingerprint.
func ValidFingerprint(s string) bool { return fingerprintPattern.MatchString(s) }

// NormalizeCondition reduces a tool message to its condition class: file
// positions and "line N" references (which move with unrelated edits and
// carry workspace paths) are replaced and whitespace is collapsed. Other
// text, including identifiers and literal values, is kept: it is what tells
// two diagnostics of one rule apart.
func NormalizeCondition(message string) string {
	s := positionPattern.ReplaceAllString(message, "<pos>")
	s = linePattern.ReplaceAllString(s, "$1 <n>")
	return strings.TrimSpace(spacePattern.ReplaceAllString(s, " "))
}

func sortedUnique(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	slices.Sort(out)
	return out
}

// fingerprint hashes the basis with the hidden condition. Fields are joined
// with NUL and lists with newline; none of them may contain either.
func fingerprint(b *FingerprintBasis, condition string) string {
	b.Scheme = FingerprintScheme
	b.Paths, b.Symbols = sortedUnique(b.Paths), sortedUnique(b.Symbols)
	if b.Context == "" {
		b.Context = ContextUnavailable
	}
	part := func(withPaths bool) string {
		fields := []string{"codeye-fingerprint", b.Scheme, b.Producer, b.Analyzer, b.Rule, "", strings.Join(b.Symbols, "\n"), b.Context, condition}
		if withPaths {
			fields[5] = strings.Join(b.Paths, "\n")
		} else {
			fields[0] = "codeye-move-key"
		}
		return domain.Digest([]byte(strings.Join(fields, "\x00")))
	}
	b.MoveKey = ""
	if b.Producer == ProducerTool || len(b.Symbols) > 0 || condition != "" {
		b.MoveKey = FingerprintScheme + ":" + part(false)
	}
	return FingerprintScheme + ":" + part(true)
}

// ToolFingerprint is the identity of a tool diagnostic: analyzer, rule,
// snapshot path, optional enclosing symbol and the normalized message.
func ToolFingerprint(analyzer, rule, path, symbol, message string) (string, FingerprintBasis) {
	b := FingerprintBasis{Producer: ProducerTool, Analyzer: analyzer, Rule: rule, Paths: []string{path}}
	if symbol != "" {
		b.Symbols = []string{symbol}
	}
	return fingerprint(&b, NormalizeCondition(message)), b
}

// setModelFingerprint sets the identity of a model candidate from what the
// platform, not the model's wording, fixed: the code/test paths and symbols
// of the excerpts the candidate cited (as a set, so citation order does not
// matter) and, for a candidate interpreting tool observations, the condition
// classes of those observations. Document-only citations fall back to the
// document paths. Candidates of one file without symbols share a fingerprint
// and are reported as ambiguous groups.
func setModelFingerprint(f *Finding, c record.Candidate, catalog map[string]record.Excerpt, linked []Observation) {
	b := FingerprintBasis{Producer: ProducerModel, Analyzer: AnalyzerReview, Rule: CauseCandidate}
	var docs []string
	for _, id := range c.Evidence {
		e := catalog[id]
		switch e.Kind {
		case "code", "test":
			b.Paths = append(b.Paths, e.Path)
			b.Symbols = append(b.Symbols, e.Symbol)
		default:
			docs = append(docs, e.Path)
		}
	}
	if len(b.Paths) == 0 {
		b.Paths = docs
	}
	var conditions []string
	for _, o := range linked {
		p := ""
		if o.Location != nil {
			p = o.Location.Path
		}
		msg := ""
		if o.RawMessage != nil {
			msg = NormalizeCondition(*o.RawMessage)
		}
		conditions = append(conditions, strings.Join([]string{o.Tool, o.RuleID, p, msg}, "\x1f"))
	}
	f.Fingerprint = fingerprint(&b, strings.Join(sortedUnique(conditions), "\x1e"))
	f.FingerprintBasis = &b
}

// linkedObservations returns the report observations a candidate cites.
func linkedObservations(obs map[string]Observation, ids []string) []Observation {
	var out []Observation
	for _, id := range ids {
		if o, ok := obs[id]; ok {
			out = append(out, o)
		}
	}
	return out
}

// goVetProcessedPaths lists the snapshot files of packages go vet analyzed,
// for a task the controller accepted as succeeded. Comparison uses it to
// decide that a vanished finding's location was actually inspected again.
func goVetProcessedPaths(d *govet.Document, ctx DecodeContext) []string {
	if ctx.Task.Status != string(domain.TaskSucceeded) {
		return nil
	}
	var out []string
	for _, pkg := range d.Inventory {
		if pkg.State != govet.PackageAnalyzedClean && pkg.State != govet.PackageAnalyzedWithFindings {
			continue
		}
		for _, p := range pkg.GoFiles {
			if _, ok := ctx.Source.Lookup(p); ok {
				out = append(out, p)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return sortedUnique(out)
}

// ReviewFact is a human disposition loaded at publication (the latest
// review version of a fingerprint in the project).
type ReviewFact struct {
	Fingerprint string
	Version     int64
	Disposition string
	PathPrefix  *string
	ExpiresAt   *time.Time // nil: no expiry
}

// ReviewApplies reports whether a review with this path scope covers a
// finding with these basis paths: every path must be the prefix or below it.
func ReviewApplies(prefix *string, paths []string) bool {
	if prefix == nil {
		return true
	}
	if len(paths) == 0 {
		return false
	}
	for _, p := range paths {
		if p != *prefix && !strings.HasPrefix(p, strings.TrimSuffix(*prefix, "/")+"/") {
			return false
		}
	}
	return true
}

// applyDispositions stamps the dispositions in effect at publication time
// (in.Now) on fp2 findings. Expired reviews and reviews whose path scope does
// not cover the finding leave it open. The stored revision keeps these values
// forever; later changes are served separately as the current disposition.
func applyDispositions(r *Report, reviews []ReviewFact, now time.Time) {
	by := map[string]ReviewFact{}
	for _, rv := range reviews {
		by[rv.Fingerprint] = rv
	}
	for i := range r.Findings {
		f := &r.Findings[i]
		rv, ok := by[f.Fingerprint]
		if !ok || f.FingerprintBasis == nil || rv.Disposition == "open" || (rv.ExpiresAt != nil && !rv.ExpiresAt.After(now)) || !ReviewApplies(rv.PathPrefix, f.FingerprintBasis.Paths) {
			continue
		}
		v := rv.Version
		f.Disposition, f.DispositionVersion = rv.Disposition, &v
	}
}

// ValidDisposition reports a finding disposition (docs/04 §2).
func ValidDisposition(d string) bool { return dispositions[d] }

// HiddenDisposition reports a disposition that removes a finding from the
// action counts and priorities (it stays in totals and comparison).
func HiddenDisposition(d string) bool { return hiddenDisposition[d] }

// validBasis checks a recorded fp2 basis (only findings that carry one; older
// revisions are read as stored).
func validBasis(f Finding) bool {
	b := f.FingerprintBasis
	if b == nil {
		return f.DispositionVersion == nil
	}
	if !ValidFingerprint(f.Fingerprint) || b.Scheme != FingerprintScheme || (b.Producer != ProducerTool && b.Producer != ProducerModel) ||
		b.Analyzer == "" || b.Rule == "" || len(b.Paths) == 0 || b.Symbols == nil || b.Context == "" || (b.MoveKey != "" && !ValidFingerprint(b.MoveKey)) {
		return false
	}
	for _, p := range b.Paths {
		if domain.ValidateEvidencePath(p) != nil {
			return false
		}
	}
	return (f.DispositionVersion == nil) == (f.Disposition == "open") && (f.DispositionVersion == nil || *f.DispositionVersion >= 1)
}

func checkReviews(reviews []ReviewFact) error {
	seen := map[string]bool{}
	for _, rv := range reviews {
		if !ValidFingerprint(rv.Fingerprint) || seen[rv.Fingerprint] || rv.Version < 1 || !ValidDisposition(rv.Disposition) ||
			(rv.PathPrefix != nil && domain.ValidateEvidencePath(*rv.PathPrefix) != nil) {
			return invalidf("review facts are invalid or duplicated")
		}
		seen[rv.Fingerprint] = true
	}
	return nil
}

// writeReviews adds the review facts to the input digest (sorted; nothing
// when there are none).
func writeReviews(w func(...string), reviews []ReviewFact) {
	sorted := append([]ReviewFact(nil), reviews...)
	slices.SortFunc(sorted, func(a, b ReviewFact) int { return strings.Compare(a.Fingerprint, b.Fingerprint) })
	for _, rv := range sorted {
		expires, prefix := "-", "-"
		if rv.ExpiresAt != nil {
			expires = rv.ExpiresAt.UTC().Format(time.RFC3339Nano)
		}
		if rv.PathPrefix != nil {
			prefix = *rv.PathPrefix
		}
		w("review", rv.Fingerprint, strconv.FormatInt(rv.Version, 10), rv.Disposition, prefix, expires)
	}
}
