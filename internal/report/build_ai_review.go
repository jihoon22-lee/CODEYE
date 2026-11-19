package report

import (
	"sort"
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
	for i, stage := range record.RoleStages {
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
	case st.facts.Status != string(domain.TaskSucceeded):
		item.Result = "not_succeeded"
	case st.rejected:
		item.Result = "result_rejected"
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
