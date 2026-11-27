package report

import (
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// Leads and their follow-up (docs/REPORT.md#단서-후속-조사): the investigate
// records that name leads and the follow-up records (review record 0.9), the
// final candidates a follow-up hands to its verify, and the code review
// section's lead_followups object, which counts from the records and the
// task rows what was asked, resolved and sent.

// LeadsSince is the first builder that reads schema 0.9 records and writes
// lead_followups.
const LeadsSince = 26

// LeadFollowups is one run's leads and follow-up tasks, from structured
// records only: no count comes from model prose.
type LeadFollowups struct {
	// LeadsAsked are the leads the accepted investigations named;
	// LeadsResolved those the platform resolved from the snapshot and
	// LeadsUnresolved the rest by reason (record.LeadReasons).
	LeadsAsked      int            `json:"leads_asked"`
	LeadsResolved   int            `json:"leads_resolved"`
	LeadsUnresolved map[string]int `json:"leads_unresolved"`
	// TasksPlanned are the planned follow-up tasks; TasksRun those with at
	// least one model call in coverage.review_calls and TasksAccepted those
	// whose record the report kept. TasksNotRun counts the others by task
	// reason (their status without one): leads_not_offered, no_leads,
	// no_leads_resolved, leads_not_admitted, leads_slot_not_run, the reasons
	// of a follow-up whose investigation did not run, and a failure before
	// any call.
	TasksPlanned  int            `json:"tasks_planned"`
	TasksRun      int            `json:"tasks_run"`
	TasksAccepted int            `json:"tasks_accepted"`
	TasksNotRun   map[string]int `json:"tasks_not_run"`
	// Calls are the follow-ups' model calls in coverage.review_calls.
	Calls int `json:"calls"`
	// NewExcerpts are the resolved excerpts the accepted follow-ups sent and
	// NewLines the union, per file, of their lines that no excerpt of the
	// investigation they follow holds (record.LeadNewLineSet).
	NewExcerpts int `json:"new_excerpts"`
	NewLines    int `json:"new_lines"`
	// Dispositions count what the accepted follow-ups did with each
	// candidate (record.Dispositions): kept, refined, contested, added.
	Dispositions map[string]int `json:"dispositions"`
}

// LeadsReviewDecoder reads a record of schema 0.9: an investigation that
// names leads (the whole-run investigate task or a slice's) or a follow-up
// attached to its own optional task.
type LeadsReviewDecoder struct{}

func (LeadsReviewDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: record.MediaType, SchemaVersion: record.SchemaVersionLeads}
}

func (LeadsReviewDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	if art.Role != "other" {
		return Contribution{}, invalidf("lead record attached to unexpected role")
	}
	stage, slice := "", ""
	if s, ok := record.LeadsTask(ctx.Task.Key); ok && !ctx.Task.Required {
		stage, slice = record.StageLeads, s
	} else if st, key, ok := record.SliceTask(ctx.Task.Key); ok && st == record.StageInvestigate {
		stage, slice = st, key
	} else if ctx.Task.Key == "review."+record.StageInvestigate {
		stage = record.StageInvestigate
	} else {
		return Contribution{}, invalidf("lead record attached to unexpected task")
	}
	r, err := record.Decode(body, record.Expect{Snapshot: ctx.Snapshot.ManifestDigest,
		RunID: ctx.RunID, AttemptID: ctx.AttemptID, Stage: stage, Slice: slice, Lookup: ctx.Source.Lookup, ShippedSource: ctx.Source.shippedSource()})
	if err != nil {
		return Contribution{}, err
	}
	return Contribution{Review: &ReviewResult{Accepted: r.Accepted(), Record: &r}, Limitations: r.PublicLimitations()}, nil
}

// isLeadsTask reports a follow-up task key (review.leads, review.leads.sNNN).
func isLeadsTask(key string) bool {
	_, ok := record.LeadsTask(key)
	return ok
}

// isLeadsSlotTask reports a slot's follow-up task (review.leads.sNNN), whose
// calls a scope artifact does not count before the slices.
func isLeadsSlotTask(key string) bool {
	slot, ok := record.LeadsTask(key)
	return ok && slot != ""
}

// investigationOf is the investigate task key of a follow-up task.
func investigationOf(leadsKey string) string {
	slot, _ := record.LeadsTask(leadsKey)
	if slot == "" {
		return "review." + record.StageInvestigate
	}
	return "review." + record.StageInvestigate + "." + slot
}

// leadsLineage checks an accepted follow-up record against the investigate
// record it names: that record is accepted and the follow-up matches it
// (record.LeadFollowupMatches).
func leadsLineage(rec *record.Record, key string, states map[string]*taskState) bool {
	inv := investigationOf(key)
	st := states[inv]
	if !reviewAccepted(st) || !slices.ContainsFunc(rec.Upstream, func(u record.Upstream) bool {
		return u.Source == inv && u.State == record.UpstreamAccepted
	}) {
		return false
	}
	return record.LeadFollowupMatches(*rec, *st.reviewResult.Record)
}

// LeadRefinement is the lineage of a finding whose candidate a lead
// follow-up refined: the follow-up task, the investigation's candidate it
// replaced, the lead whose resolved code the refinement rests on and that
// candidate's title, so a reader sees what changed. The follow-up record
// keeps the whole original text (record.LeadDisposition.Original).
type LeadRefinement struct {
	TaskKey       string `json:"task_key"`
	CandidateID   string `json:"candidate_id"`
	Lead          string `json:"lead"`
	OriginalTitle string `json:"original_title"`
}

// leadRefinements maps each refined final candidate of an accepted
// follow-up record (nil: none) to its lineage.
func leadRefinements(fu *record.Record, task string) map[string]LeadRefinement {
	if fu == nil || fu.LeadFollowup == nil {
		return nil
	}
	out := map[string]LeadRefinement{}
	for _, d := range fu.LeadFollowup.Dispositions {
		if d.Disposition == record.DispositionRefined && d.Original != nil {
			out[d.FinalID] = LeadRefinement{TaskKey: task, CandidateID: d.CandidateID, Lead: d.Lead, OriginalTitle: d.Original.Title}
		}
	}
	return out
}

// leadsFor is the accepted follow-up record of an investigation (slot "" for
// the whole run), nil without one.
func leadsFor(states map[string]*taskState, slot string) *record.Record {
	if st := states[record.LeadsTaskKey(slot)]; reviewAccepted(st) && st.reviewResult.Record.LeadFollowup != nil {
		return st.reviewResult.Record
	}
	return nil
}

// leadsKey is the task key of the follow-up of a verify record's
// investigation.
func leadsKeyOf(rec *record.Record) string {
	if rec.Slice == nil {
		return record.TaskLeads
	}
	return record.LeadsTaskKey(rec.Slice.Key)
}

// leadFollowups counts the run's leads and follow-ups; nil when the plan
// held no follow-up task.
func (c sectionContext) leadFollowups() *LeadFollowups {
	l := &LeadFollowups{LeadsUnresolved: map[string]int{}, TasksNotRun: map[string]int{}, Dispositions: map[string]int{}}
	calls := leadCallsByTask(c.r.Coverage.ReviewCalls)
	lines := map[string]map[int]bool{}
	for k, st := range c.states {
		if st == nil {
			continue
		}
		if isLeadsTask(k) {
			l.TasksPlanned++
			if calls[k] > 0 {
				l.TasksRun++
			} else {
				l.TasksNotRun[notRunReason(st.facts)]++
			}
			if reviewAccepted(st) && st.reviewResult.Record.LeadFollowup != nil {
				l.TasksAccepted++
				rec := st.reviewResult.Record
				f := rec.LeadFollowup
				l.NewExcerpts += len(f.New)
				if inv := c.states[investigationOf(k)]; reviewAccepted(inv) {
					for file, set := range record.LeadNewLineSet(f.New, excerptCatalog(rec.Evidence), inv.reviewResult.Record.Evidence) {
						if lines[file] == nil {
							lines[file] = map[int]bool{}
						}
						for n := range set {
							lines[file][n] = true
						}
					}
				}
				for _, d := range f.Dispositions {
					l.Dispositions[d.Disposition]++
				}
			}
			continue
		}
		if stage, _, ok := record.SliceTask(k); (ok && stage == record.StageInvestigate) || k == "review."+record.StageInvestigate {
			if !reviewAccepted(st) {
				continue
			}
			for _, x := range st.reviewResult.Record.Leads {
				l.LeadsAsked++
				if x.Resolved() {
					l.LeadsResolved++
				} else {
					l.LeadsUnresolved[x.Reason]++
				}
			}
		}
	}
	if l.TasksPlanned == 0 {
		return nil
	}
	for _, n := range calls {
		l.Calls += n
	}
	l.NewLines = record.LineCount(lines)
	return l
}

// excerptCatalog indexes evidence by ID.
func excerptCatalog(list []record.Excerpt) map[string]record.Excerpt {
	out := make(map[string]record.Excerpt, len(list))
	for _, e := range list {
		out[e.ID] = e
	}
	return out
}

// leadCallsByTask counts the follow-up calls of each follow-up task.
func leadCallsByTask(calls []ReviewCall) map[string]int {
	out := map[string]int{}
	for _, call := range calls {
		if isLeadsTask(call.TaskKey) {
			out[call.TaskKey]++
		}
	}
	return out
}

// notRunReason is the key of a task that made no call: its reason, else its
// status.
func notRunReason(f TaskFacts) string {
	if f.Reason != nil && *f.Reason != "" {
		return *f.Reason
	}
	return f.Status
}

var leadCandidateID = regexp.MustCompile(`^investigate_[1-9][0-9]*$`)

// validateLeadRefinements checks the lineage of refined findings: only in a
// builder-26 report, on an investigation finding whose follow-up task (the
// whole run's or its slot's) succeeded, naming an investigation candidate
// and a lead, with the original title as model text (the omission text in a
// code-free export); and one finding for every refined disposition the
// lead_followups object counts.
func validateLeadRefinements(r Report) error {
	succeeded := map[string]bool{}
	for _, t := range r.Tasks {
		succeeded[t.Key] = t.Status == string(domain.TaskSucceeded)
	}
	limit := proseLimit(r.Builder.Version)
	n := 0
	for _, f := range r.Findings {
		x := f.LeadRefinement
		if x == nil {
			continue
		}
		n++
		task := record.TaskLeads
		if strings.HasPrefix(f.ID, "review_s") && len(f.ID) > len("review_s000_") {
			task = record.LeadsTaskKey(f.ID[len("review_") : len("review_")+4])
		}
		if !builderAtLeast(r.Builder.Version, LeadsSince) || !investigateFindingID.MatchString(f.ID) || x.TaskKey != task || !succeeded[task] ||
			!leadCandidateID.MatchString(x.CandidateID) || !record.ValidLeadID(x.Lead) ||
			!record.ProseText(x.OriginalTitle, limit) || (x.OriginalTitle == textOmitted) == r.Export.IncludeCode {
			return invalidf("finding %s: lead_refinement is invalid", f.ID)
		}
	}
	want := 0
	if r.CodeReview != nil && r.CodeReview.LeadFollowups != nil {
		want = r.CodeReview.LeadFollowups.Dispositions[record.DispositionRefined]
	}
	if n != want {
		return invalidf("%d refined findings for %d refined lead dispositions", n, want)
	}
	return nil
}

// validateLeadFollowups checks the object against the report: present only
// in a builder-26 report that planned follow-up tasks, the task counts
// follow from the task rows, coverage.review_calls and the AI review items,
// and the lead counts add up with known reasons.
func validateLeadFollowups(r Report) error {
	byTask := leadCallsByTask(r.Coverage.ReviewCalls)
	planned, notRun, run, accepted := 0, map[string]int{}, 0, 0
	for _, t := range r.Tasks {
		if !isLeadsTask(t.Key) {
			continue
		}
		planned++
		if byTask[t.Key] > 0 {
			run++
		} else {
			notRun[notRunReason(TaskFacts{Status: t.Status, Reason: t.Reason})]++
		}
	}
	for _, t := range append(slices.Clone(r.Coverage.AIReview.RequiredTasks), r.Coverage.AIReview.OptionalTasks...) {
		if isLeadsTask(t.Key) && t.Result == "accepted" {
			accepted++
		}
	}
	var l *LeadFollowups
	if r.CodeReview != nil {
		l = r.CodeReview.LeadFollowups
	}
	switch {
	case l == nil && (planned == 0 || !builderAtLeast(r.Builder.Version, LeadsSince) || r.CodeReview == nil):
		return nil
	case l == nil:
		return invalidf("builder %s report with follow-up tasks but no lead_followups", r.Builder.Version)
	case planned == 0 || !builderAtLeast(r.Builder.Version, LeadsSince):
		return invalidf("lead_followups without follow-up tasks or in a builder %s revision", r.Builder.Version)
	}
	calls := 0
	for _, n := range byTask {
		calls += n
	}
	unresolved := 0
	for reason, n := range l.LeadsUnresolved {
		if !record.LeadReason(reason) || n <= 0 {
			return invalidf("lead_followups.leads_unresolved has %q", reason)
		}
		unresolved += n
	}
	for d, n := range l.Dispositions {
		if !slices.Contains(record.Dispositions, d) || n <= 0 {
			return invalidf("lead_followups.dispositions has %q", d)
		}
	}
	notRunTotal := 0
	for reason, n := range l.TasksNotRun {
		if n <= 0 || !record.ValidCode(reason) {
			return invalidf("lead_followups.tasks_not_run has %q", reason)
		}
		notRunTotal += n
		if notRun[reason] != n {
			return invalidf("lead_followups.tasks_not_run.%s does not match the task rows", reason)
		}
	}
	switch {
	case l.TasksPlanned != planned || l.TasksRun != run || l.TasksRun+notRunTotal != planned:
		return invalidf("lead_followups task counts do not match the task rows and calls")
	case l.TasksAccepted != accepted || l.TasksAccepted > l.TasksRun:
		return invalidf("lead_followups.tasks_accepted does not match the AI review items")
	case l.Calls != calls:
		return invalidf("lead_followups.calls does not match coverage.review_calls")
	case l.LeadsAsked != l.LeadsResolved+unresolved || l.LeadsResolved < 0 || l.NewExcerpts < 0 || l.NewLines < 0 ||
		(l.NewExcerpts == 0 && l.NewLines != 0) || (l.TasksAccepted == 0 && (l.NewExcerpts != 0 || len(l.Dispositions) != 0)):
		return invalidf("lead_followups lead counts do not add up")
	}
	return nil
}

// leadReasonsSorted lists a count map's keys in order (rendering).
func leadReasonsSorted(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
