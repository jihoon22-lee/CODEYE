package report

import (
	"sort"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Pull-request focus (docs/REPORT.md#pull-request-보고서, builder 9): a run of a
// pull request analyzes the whole snapshot, and the report says how much of
// the pull request the review saw and which findings and tool diagnostics
// lie in its changes, apart from the pre-existing ones elsewhere. The
// section is computed from the run's pin and the report's own findings,
// observations and review records; it changes no count, status or verdict.

// PullRequestSince is the first builder version that writes the section.
const PullRequestSince = 9

// Where a finding or tool diagnostic lies relative to the pull request
// (PullRequestItem.Scope and the PullRequestCounts fields).
const (
	// ScopeChangedLines: on a line the pull request adds or changes.
	ScopeChangedLines = "changed_lines"
	// ScopeChangedFile: in a changed file whose changed lines GitHub did not
	// list (a diff too large to show): counted in the changes at file level.
	ScopeChangedFile = "changed_file"
	// ScopeChangedFileOtherLines: in a changed file, outside its changed
	// lines (pre-existing).
	ScopeChangedFileOtherLines = "changed_file_other_lines"
	// ScopeElsewhere: in a file the pull request does not change.
	ScopeElsewhere = "elsewhere"
)

// Line bases of the section: every listed changed file has its changed
// lines, none has, or some have.
const (
	LineBasisLines = "lines"
	LineBasisFiles = "files"
	LineBasisMixed = "mixed"
)

// Note of a pull-request report: the changed files were reviewed first and
// the rest of the repository was not necessarily reviewed at the same depth.
const NotePullRequestFocus = "pull_request_focus"

// PullRequestSection is the report's pull-request block.
type PullRequestSection struct {
	Number         int    `json:"number"`
	URL            string `json:"url"`
	Title          string `json:"title"`
	Repository     string `json:"repository"`
	HeadRepository string `json:"head_repository"`
	BaseRef        string `json:"base_ref"`
	HeadRef        string `json:"head_ref"`
	BaseSHA        string `json:"base_sha"`
	HeadSHA        string `json:"head_sha"`
	ResolvedAt     string `json:"resolved_at"`
	// HeadAnalyzed: the snapshot's commit is the pinned head.
	HeadAnalyzed bool `json:"head_analyzed"`
	// ChangedFiles is the pull request's own count; FilesListed what GitHub
	// listed (FilesTruncated when fewer).
	ChangedFiles      int               `json:"changed_files"`
	FilesListed       int               `json:"files_listed"`
	FilesTruncated    bool              `json:"files_truncated"`
	FilesRemoved      int               `json:"files_removed"`
	LineBasis         string            `json:"line_basis"`
	FilesWithoutLines int               `json:"files_without_lines"`
	Review            PullRequestReview `json:"review"`
	Findings          PullRequestCounts `json:"findings"`
	Observations      PullRequestCounts `json:"observations"`
	// Items are the findings and tool diagnostics in the changes (changed
	// lines, or changed files without line information), findings first.
	Items []PullRequestItem `json:"items"`
}

// PullRequestReview says how much of the pull request the model saw.
type PullRequestReview struct {
	// Reviewable are the listed changed files that exist at the head.
	Reviewable int `json:"reviewable"`
	// InSnapshot are those the snapshot holds as readable files (the rest
	// are excluded by the snapshot rules: secrets, generated or cache
	// directories, links).
	InSnapshot int `json:"in_snapshot"`
	// SentToModel are those a model review record shows were sent; null
	// when the run planned no model review.
	SentToModel *int `json:"sent_to_model"`
	// NotSent lists the reviewable changed files the model did not see
	// (empty when SentToModel is null), at most 50.
	NotSent      []string `json:"not_sent"`
	NotSentTotal int      `json:"not_sent_total"`
}

// PullRequestCounts counts items by scope.
type PullRequestCounts struct {
	ChangedLines          int `json:"changed_lines"`
	ChangedFile           int `json:"changed_file"`
	ChangedFileOtherLines int `json:"changed_file_other_lines"`
	Elsewhere             int `json:"elsewhere"`
}

// InChanges is the count in the pull request's changes.
func (c PullRequestCounts) InChanges() int { return c.ChangedLines + c.ChangedFile }

// PreExisting is the count outside them.
func (c PullRequestCounts) PreExisting() int { return c.ChangedFileOtherLines + c.Elsewhere }

func (c *PullRequestCounts) add(scope string) {
	switch scope {
	case ScopeChangedLines:
		c.ChangedLines++
	case ScopeChangedFile:
		c.ChangedFile++
	case ScopeChangedFileOtherLines:
		c.ChangedFileOtherLines++
	default:
		c.Elsewhere++
	}
}

// PullRequestItem is one finding or tool diagnostic in the changes.
type PullRequestItem struct {
	Kind  string `json:"kind"` // finding | observation
	ID    string `json:"id"`
	Scope string `json:"scope"`
	Path  string `json:"path"`
	Line  int    `json:"line"`
}

const maxNotSentListed = 50

// changeIndex answers where a location lies.
type changeIndex map[string]domain.PullRequestFile

func (x changeIndex) scope(path string, start, end int) string {
	f, ok := x[path]
	if !ok || !f.Reviewable() {
		return ScopeElsewhere
	}
	if !f.LinesKnown {
		return ScopeChangedFile
	}
	if end < start {
		end = start
	}
	for _, r := range f.Lines {
		if start <= r[1] && end >= r[0] {
			return ScopeChangedLines
		}
	}
	return ScopeChangedFileOtherLines
}

// rank orders scopes: the first is the most specific.
func scopeRank(s string) int {
	switch s {
	case ScopeChangedLines:
		return 0
	case ScopeChangedFile:
		return 1
	case ScopeChangedFileOtherLines:
		return 2
	}
	return 3
}

// pullRequestSection builds the section of a pull-request run (nil
// otherwise). sent are the snapshot paths the model review records show as
// sent, reviewRan whether a model review task ran (not unsupported or
// skipped by policy).
func pullRequestSection(r *Report, pin *domain.PullRequest, inSnapshot map[string]string, sent map[string]bool, reviewRan bool) *PullRequestSection {
	if pin == nil {
		return nil
	}
	s := &PullRequestSection{Number: pin.Number, URL: pin.URL, Title: pin.Title, Repository: pin.Host + "/" + pin.Repository, HeadRepository: pin.HeadRepository,
		BaseRef: pin.BaseRef, HeadRef: pin.HeadRef, BaseSHA: pin.BaseSHA, HeadSHA: pin.HeadSHA, ResolvedAt: pin.ResolvedAt.UTC().Format(time.RFC3339),
		HeadAnalyzed: r.Inputs.CommitSHA != nil && *r.Inputs.CommitSHA == pin.HeadSHA,
		ChangedFiles: pin.ChangedFiles, FilesListed: len(pin.Files), FilesTruncated: pin.FilesTruncated, Items: []PullRequestItem{}}
	idx := changeIndex{}
	withLines := 0
	var notSent []string
	for _, f := range pin.Files {
		idx[f.Path] = f
		if !f.Reviewable() {
			s.FilesRemoved++
			continue
		}
		if f.LinesKnown {
			withLines++
		} else {
			s.FilesWithoutLines++
		}
		s.Review.Reviewable++
		if _, ok := inSnapshot[f.Path]; ok {
			s.Review.InSnapshot++
		}
		if reviewRan && !sent[f.Path] {
			notSent = append(notSent, f.Path)
		}
	}
	switch {
	case s.FilesWithoutLines == 0:
		s.LineBasis = LineBasisLines
	case withLines == 0:
		s.LineBasis = LineBasisFiles
	default:
		s.LineBasis = LineBasisMixed
	}
	s.Review.NotSent = []string{}
	if reviewRan {
		n := s.Review.Reviewable - len(notSent)
		s.Review.SentToModel = &n
		sort.Strings(notSent)
		s.Review.NotSentTotal = len(notSent)
		s.Review.NotSent = append(s.Review.NotSent, notSent[:min(len(notSent), maxNotSentListed)]...)
	}
	for _, f := range r.Findings {
		best, path, line := ScopeElsewhere, "", 0
		for _, l := range f.Locations {
			if sc := idx.scope(l.Path, l.StartLine, l.EndLine); path == "" || scopeRank(sc) < scopeRank(best) {
				best, path, line = sc, l.Path, l.StartLine
			}
		}
		s.Findings.add(best)
		if best == ScopeChangedLines || best == ScopeChangedFile {
			s.Items = append(s.Items, PullRequestItem{Kind: "finding", ID: f.ID, Scope: best, Path: path, Line: line})
		}
	}
	for _, o := range r.Observations {
		if o.Metric != nil || o.Suppressed {
			continue
		}
		sc := ScopeElsewhere
		path, line := "", 0
		if o.Location != nil {
			sc, path, line = idx.scope(o.Location.Path, o.Location.StartLine, o.Location.EndLine), o.Location.Path, o.Location.StartLine
		}
		s.Observations.add(sc)
		if sc == ScopeChangedLines || sc == ScopeChangedFile {
			s.Items = append(s.Items, PullRequestItem{Kind: "observation", ID: o.ID, Scope: sc, Path: path, Line: line})
		}
	}
	return s
}

// validPullRequestSection checks the section's own consistency.
func validPullRequestSection(r Report) error {
	s := r.PullRequest
	if s == nil {
		return nil
	}
	switch {
	case s.Number < 1 || !domain.ValidObjectID(s.BaseSHA) || !domain.ValidObjectID(s.HeadSHA) || s.Items == nil || s.Review.NotSent == nil:
		return invalidf("pull_request section is invalid")
	case s.LineBasis != LineBasisLines && s.LineBasis != LineBasisFiles && s.LineBasis != LineBasisMixed:
		return invalidf("pull_request line basis is invalid")
	case s.FilesListed != s.FilesRemoved+s.Review.Reviewable || s.Review.InSnapshot > s.Review.Reviewable:
		return invalidf("pull_request file counts are inconsistent")
	case s.Review.SentToModel != nil && (*s.Review.SentToModel < 0 || *s.Review.SentToModel+s.Review.NotSentTotal != s.Review.Reviewable):
		return invalidf("pull_request review counts are inconsistent")
	}
	findings, observations := 0, 0
	for _, o := range r.Observations {
		if o.Metric == nil && !o.Suppressed {
			observations++
		}
	}
	findings = len(r.Findings)
	f, o := s.Findings, s.Observations
	if f.InChanges()+f.PreExisting() != findings || o.InChanges()+o.PreExisting() != observations || len(s.Items) != f.InChanges()+o.InChanges() {
		return invalidf("pull_request item counts are inconsistent")
	}
	return nil
}

// pullRequestParas are the overview sentences of a pull-request report:
// which pull request and commits, how many changed files the model saw, the
// line basis and the counts in and outside the changes. Nothing for any
// other report.
func pullRequestParas(r Report, l Locale) []string {
	s := r.PullRequest
	if s == nil {
		return nil
	}
	repo := strings.TrimPrefix(s.Repository, "github.com/")
	var out []string
	if s.HeadRepository != "" && !strings.EqualFold(s.HeadRepository, repo) {
		out = append(out, tf(l, "pr.lead_fork", s.Number, repo, s.HeadRepository, s.HeadRef, s.BaseRef, short(s.HeadSHA), short(s.BaseSHA)))
	} else {
		out = append(out, tf(l, "pr.lead", s.Number, repo, s.HeadRef, s.BaseRef, short(s.HeadSHA), short(s.BaseSHA)))
	}
	if !s.HeadAnalyzed {
		out = append(out, tf(l, "pr.head_differs", short(s.HeadSHA)))
	}
	if s.Review.SentToModel != nil {
		out = append(out, tf(l, "pr.files", s.Review.Reviewable, *s.Review.SentToModel))
	} else {
		out = append(out, tf(l, "pr.files_no_model", s.Review.Reviewable))
	}
	if s.FilesRemoved > 0 {
		out = append(out, tf(l, "pr.files_removed", s.FilesRemoved))
	}
	if n := s.Review.Reviewable - s.Review.InSnapshot; n > 0 {
		out = append(out, tf(l, "pr.files_missing", n))
	}
	if s.FilesTruncated {
		out = append(out, tf(l, "pr.truncated", s.ChangedFiles, s.FilesListed))
	}
	switch s.LineBasis {
	case LineBasisLines:
		out = append(out, l.Label("pr.basis_lines"))
	case LineBasisMixed:
		out = append(out, tf(l, "pr.basis_mixed", s.FilesWithoutLines))
	default:
		out = append(out, l.Label("pr.basis_files"))
	}
	out = append(out, tf(l, "pr.counts", s.Findings.InChanges(), s.Observations.InChanges(), s.Findings.PreExisting(), s.Observations.PreExisting()))
	return append(out, l.Label("pr.depth"))
}
