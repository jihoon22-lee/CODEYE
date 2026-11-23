package report

import (
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// requiredReviewKeys derives the review tasks the verdict needs from the
// planned tasks: the review.* tasks with required_for_verdict are required,
// the others optional, in stage order. A plan without a review task requires
// none.
func requiredReviewKeys(states map[string]*taskState) (required, optional []string) {
	var keys []string
	for k := range states {
		if strings.HasPrefix(k, "review.") {
			keys = append(keys, k)
		}
	}
	order := map[string]int{}
	for i, stage := range record.Stages {
		order["review."+stage] = i
	}
	sort.Slice(keys, func(a, b int) bool {
		x, okx := order[keys[a]]
		y, oky := order[keys[b]]
		if okx != oky {
			return okx
		}
		if x != y {
			return x < y
		}
		return keys[a] < keys[b]
	})
	for _, k := range keys {
		if states[k].facts.Required {
			required = append(required, k)
		} else {
			optional = append(optional, k)
		}
	}
	return required, optional
}

// ResultAcceptedIncomplete is the result of a verify task that failed only
// because some candidates stayed unreviewed (record.ReasonOutputIncomplete,
// record.ReasonInputIncomplete) while its accepted record, whose lineage
// the report checked, carries the verdicts of the others. The report
// publishes those verdicts and counts the task toward a performed AI
// review; the task stays failed, so the report is partial
// (docs/DECISIONS.md A59). Builders before VerifyIncompleteSince never set it.
const (
	ResultAcceptedIncomplete = "accepted_incomplete"
	VerifyIncompleteSince    = 21
)

// LimReviewVerificationIncomplete marks a report whose model findings
// carry the verdicts of an incomplete verify task: the candidates it did
// not verify are unreviewed with their reason.
const LimReviewVerificationIncomplete = "review_verification_incomplete"

// verifyIncomplete reports a whole-run or slice verify task whose accepted
// record the report uses although the task failed (ResultAcceptedIncomplete).
func verifyIncomplete(k string, st *taskState) bool {
	verify := "review." + record.StageVerify
	if st == nil || (k != verify && !strings.HasPrefix(k, verify+".")) || st.rejected || st.facts.Status != string(domain.TaskFailed) ||
		st.facts.Reason == nil || (*st.facts.Reason != record.ReasonOutputIncomplete && *st.facts.Reason != record.ReasonInputIncomplete) {
		return false
	}
	return st.reviewResult != nil && st.reviewResult.Record != nil && st.reviewResult.Record.Accepted() && st.reviewResult.Record.Stage == record.StageVerify
}

// validIncompleteVerify checks every accepted_incomplete review task: a
// builder that sets it, a verify task (whole run or slice) that failed for
// unreviewed candidates, and the report limitation that says so.
func validIncompleteVerify(r Report) error {
	version, _ := strconv.Atoi(r.Builder.Version)
	verify := "review." + record.StageVerify
	seen := false
	for _, t := range append(slices.Clone(r.Coverage.AIReview.RequiredTasks), r.Coverage.AIReview.OptionalTasks...) {
		if t.Result != ResultAcceptedIncomplete {
			continue
		}
		seen = true
		reason := deref(t.Reason)
		if version < VerifyIncompleteSince || (t.Key != verify && !strings.HasPrefix(t.Key, verify+".")) || deref(t.TaskStatus) != string(domain.TaskFailed) ||
			(reason != record.ReasonOutputIncomplete && reason != record.ReasonInputIncomplete) {
			return invalidf("review task %s is accepted_incomplete without an incomplete verify task or its builder", t.Key)
		}
	}
	if seen != slices.Contains(r.Limitations, LimReviewVerificationIncomplete) {
		return invalidf("%s does not match the incomplete verify tasks", LimReviewVerificationIncomplete)
	}
	return nil
}

// verdictsUsable reports a verify task whose verdicts the findings carry: a
// succeeded task with an accepted record, or an incomplete one.
func verdictsUsable(k string, st *taskState) bool {
	return reviewAccepted(st) || verifyIncomplete(k, st)
}

func reviewItem(k string, st *taskState) (AIReviewTask, bool) {
	item := AIReviewTask{Key: k}
	if st == nil {
		item.Result = "not_planned"
		return item, false
	}
	s := st.facts.Status
	item.TaskStatus = &s
	if st.facts.Reason != nil {
		reason := *st.facts.Reason
		item.Reason = &reason
	}
	switch {
	case st.facts.Status == string(domain.TaskSkippedPolicy):
		item.Result = "skipped"
	case verifyIncomplete(k, st):
		item.Result = ResultAcceptedIncomplete
		return item, true
	case st.facts.Status != string(domain.TaskSucceeded):
		item.Result = "not_succeeded"
	case st.rejected:
		item.Result = "result_rejected"
	case k == record.TaskScope && scopeAccepted(st):
		item.Result = "accepted"
		return item, true
	case !st.review:
		item.Result = "result_missing"
	default:
		item.Result = "accepted"
		return item, true
	}
	return item, false
}

func decideAIReview(states map[string]*taskState) AIReview {
	out := AIReview{RequiredTasks: []AIReviewTask{}}
	required, optional := requiredReviewKeys(states)
	accepted, planned := 0, 0
	for _, k := range required {
		item, ok := reviewItem(k, states[k])
		if states[k] != nil {
			planned++
		}
		if ok {
			accepted++
		}
		out.RequiredTasks = append(out.RequiredTasks, item)
	}
	for _, k := range optional {
		item, _ := reviewItem(k, states[k])
		out.OptionalTasks = append(out.OptionalTasks, item)
	}
	reason := func(s string) *string { return &s }
	switch {
	case len(required) > 0 && accepted == len(required):
		out.Status = AIReviewPerformed
	case accepted > 0:
		out.Status, out.Reason = AIReviewPartial, reason("required_review_incomplete")
	case planned == 0:
		out.Status, out.Reason = AIReviewNotPerformed, reason("review_tasks_not_planned")
	default:
		out.Status, out.Reason = AIReviewNotPerformed, reason("review_results_not_accepted")
	}
	return out
}
