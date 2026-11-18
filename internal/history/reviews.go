package history

import (
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/report"
)

// Review is the latest human review version of one fingerprint in a
// project (an append-only row of finding_reviews).
type Review struct {
	Fingerprint string
	Version     int64
	Disposition string
	Reason      string
	PathPrefix  *string
	ExpiresAt   *time.Time
	ActorID     string
	CreatedAt   time.Time
	// The finding the person judged: run, report revision and finding ID.
	RunID          string
	ReportRevision int
	FindingID      string
}

// ReviewView is a review as served with a comparison.
type ReviewView struct {
	Version        int64      `json:"version"`
	Disposition    string     `json:"disposition"`
	Reason         string     `json:"reason"`
	PathPrefix     *string    `json:"path_prefix"`
	ExpiresAt      *time.Time `json:"expires_at"`
	Expired        bool       `json:"expired"`
	ActorID        string     `json:"actor_id"`
	CreatedAt      time.Time  `json:"created_at"`
	RunID          string     `json:"run_id"`
	ReportRevision int        `json:"report_revision"`
	FindingID      string     `json:"finding_id"`
}

func viewOf(r Review, now time.Time) *ReviewView {
	return &ReviewView{Version: r.Version, Disposition: r.Disposition, Reason: r.Reason, PathPrefix: r.PathPrefix, ExpiresAt: r.ExpiresAt,
		Expired: r.ExpiresAt != nil && !r.ExpiresAt.After(now), ActorID: r.ActorID, CreatedAt: r.CreatedAt, RunID: r.RunID, ReportRevision: r.ReportRevision, FindingID: r.FindingID}
}

// How the latest review relates to one finding now.
const (
	MatchNone       = "none"         // no review for the fingerprint
	MatchApplied    = "applied"      // the review's disposition is in effect
	MatchCandidate  = "candidate"    // ambiguous or weak identity: needs a person to confirm on this finding
	MatchExpired    = "expired"      // the review's expiry passed: open again
	MatchOutOfScope = "out_of_scope" // the review's path scope does not cover the finding
	MatchOpen       = "reverted"     // the latest review set the finding back to open
)

// FindingView is one finding of the current revision with its comparison
// state and dispositions: the one stamped at publication (immutable) and the
// one in effect now.
type FindingView struct {
	FindingID              string      `json:"finding_id"`
	Fingerprint            *string     `json:"fingerprint"`
	State                  *string     `json:"state"`
	PublicationDisposition string      `json:"publication_disposition"`
	CurrentDisposition     string      `json:"current_disposition"`
	ReviewMatch            string      `json:"review_match"`
	Review                 *ReviewView `json:"review"`
}

// Counts are the summary counts of one revision under one disposition view.
// They are recomputed with report.ComputeSummary, so they follow the same
// rules as the stored summary.
type Counts struct {
	Total      int `json:"total_findings"`
	Actionable int `json:"actionable_findings"`
	Pending    int `json:"pending_candidates"`
	Refuted    int `json:"refuted_findings"`
	Hidden     int `json:"hidden_findings"`
}

func countsOf(findings []report.Finding, obs []report.Observation) Counts {
	s := report.ComputeSummary(findings, obs)
	return Counts{Total: s.TotalFindings, Actionable: s.ActionableFindings, Pending: s.PendingCandidates, Refuted: s.RefutedFindings, Hidden: s.HiddenFindings}
}

// Effective decides the disposition of finding f of revision side now. A
// non-open review is applied when it is not expired, its path scope covers
// the finding and either the person set it on this very finding (same run,
// revision and finding ID) or the fingerprint names exactly one finding of
// this revision with a strong identity (report.DispositionCarries).
func Effective(r *Review, f report.Finding, side Side, matches int, now time.Time) (string, string) {
	if r == nil {
		return "open", MatchNone
	}
	switch {
	case r.Disposition == "open":
		return "open", MatchOpen
	case r.ExpiresAt != nil && !r.ExpiresAt.After(now):
		return "open", MatchExpired
	case f.FingerprintBasis == nil || !report.ReviewApplies(r.PathPrefix, f.FingerprintBasis.Paths):
		return "open", MatchOutOfScope
	case (r.RunID == side.RunID && r.ReportRevision == side.Revision && r.FindingID == f.ID) || report.DispositionCarries(f.FingerprintBasis, matches):
		return r.Disposition, MatchApplied
	}
	return "open", MatchCandidate
}

// Overlay is the per-finding and summary view of the current revision.
type Overlay struct {
	Findings []FindingView `json:"findings"`
	Summary  struct {
		AtPublication Counts `json:"at_publication"`
		Current       Counts `json:"current"`
	} `json:"summary"`
}

// ApplyReviews sets the review of every entry and returns the per-finding
// view of cur. reviews are keyed by fingerprint; now is the database clock.
// Findings without an fp2 identity keep their stored disposition.
func ApplyReviews(c *Comparison, cur Side, reviews map[string]Review, now time.Time) Overlay {
	var o Overlay
	o.Findings = []FindingView{}
	state := map[string]string{}
	for i := range c.Entries {
		e := &c.Entries[i]
		state[e.Fingerprint] = e.State
		if r, ok := reviews[e.Fingerprint]; ok {
			e.Review = viewOf(r, now)
		}
	}
	matches := map[string]int{}
	for _, f := range cur.Report.Findings {
		matches[f.Fingerprint]++
	}
	current := make([]report.Finding, len(cur.Report.Findings))
	for i, f := range cur.Report.Findings {
		v := FindingView{FindingID: f.ID, PublicationDisposition: f.Disposition, CurrentDisposition: f.Disposition, ReviewMatch: MatchNone}
		if f.FingerprintBasis != nil && report.ValidFingerprint(f.Fingerprint) {
			fp := f.Fingerprint
			v.Fingerprint = &fp
			if s, ok := state[fp]; ok {
				st := s
				v.State = &st
			}
			var r *Review
			if x, ok := reviews[fp]; ok {
				r = &x
				v.Review = viewOf(x, now)
			}
			v.CurrentDisposition, v.ReviewMatch = Effective(r, f, cur, matches[fp], now)
		}
		g := f
		g.Disposition = v.CurrentDisposition
		current[i] = g
		o.Findings = append(o.Findings, v)
	}
	o.Summary.AtPublication = countsOf(cur.Report.Findings, cur.Report.Observations)
	o.Summary.Current = countsOf(current, cur.Report.Observations)
	return o
}
