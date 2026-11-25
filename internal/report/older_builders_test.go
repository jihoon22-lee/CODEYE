package report

import (
	"bytes"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// fileOnlyLedgerForTest turns a current report into one an older builder
// wrote: the ledger of its version and, before CitedLinesSince, the review
// artifacts under the decoders of the earlier record schemas.
func fileOnlyLedgerForTest(r *Report) {
	if version, err := strconv.Atoi(r.Builder.Version); err == nil && version < TriageSince {
		withoutTriageForTest(r)
	}
	if version, err := strconv.Atoi(r.Builder.Version); err == nil && version < CitedLinesSince {
		cited := (CitedReviewDecoder{}).Key().String()
		for i := range r.Artifacts {
			a := &r.Artifacts[i]
			if a.Decoder == nil || *a.Decoder != cited {
				continue
			}
			key := (ReviewDecoder{}).Key().String()
			if _, _, ok := record.SliceTask(deref(a.TaskKey)); ok {
				key = (SliceReviewDecoder{}).Key().String()
			} else if deref(a.TaskKey) == record.TaskContract {
				key = (ContractReviewDecoder{}).Key().String()
			}
			a.Decoder = &key
		}
	}
	if r.CodeReview == nil || r.CodeReview.Ledger == nil {
		return
	}
	if r.Builder.Version == "5" {
		r.CodeReview.Ledger = nil
	} else {
		ledger := r.CodeReview.Ledger
		ledger.Files = nil
		for i := range ledger.Modules {
			ledger.Modules[i].FilesAccepted, ledger.Modules[i].FilesRequested = nil, nil
		}
		ledger.Status = ledgerStatus(*ledger, r.Tasks)
	}
	overview := overviewOf(*r)
	r.Overview = &overview
}

// withoutTriageForTest removes what the diagnostic triage slots added to a
// current report: an earlier builder's plan had no triage tasks. The
// example reports' triage made no finding.
func withoutTriageForTest(r *Report) {
	triage := func(key *string) bool { return key != nil && isTriageTask(*key) }
	r.Tasks = slices.DeleteFunc(r.Tasks, func(t Task) bool { return isTriageTask(t.Key) })
	r.Artifacts = slices.DeleteFunc(r.Artifacts, func(a Artifact) bool { return triage(a.TaskKey) })
	calls := len(r.Coverage.ReviewCalls)
	r.Coverage.ReviewCalls = slices.DeleteFunc(r.Coverage.ReviewCalls, func(c ReviewCall) bool { return isTriageTask(c.TaskKey) })
	removed := calls - len(r.Coverage.ReviewCalls)
	r.Coverage.AIReview.OptionalTasks = slices.DeleteFunc(r.Coverage.AIReview.OptionalTasks, func(t AIReviewTask) bool { return isTriageTask(t.Key) })
	if cr := r.CodeReview; cr != nil {
		cr.DiagnosticTriage = nil
		cr.Calls, cr.AcceptedCalls = 0, 0
		for _, c := range r.Coverage.ReviewCalls {
			cr.Calls++
			if c.Outcome == record.OutcomeAccepted {
				cr.AcceptedCalls++
			}
		}
		if l := cr.Ledger; l != nil && l.CallsUsed != nil {
			n := *l.CallsUsed - removed
			l.CallsUsed = &n
		}
	}
}

func countAddedCallForTest(r *Report) {
	if r.CodeReview != nil && r.CodeReview.Ledger != nil && r.CodeReview.Ledger.CallsUsed != nil {
		n := *r.CodeReview.Ledger.CallsUsed + 1
		r.CodeReview.Ledger.CallsUsed = &n
	}
}

// CODEYE_REAL_REPORT names a stored report.json of a real run (kept outside
// the repository). It must still read and render; the test logs the line
// count of each Markdown form.
func TestRealReportRenders(t *testing.T) {
	path := os.Getenv("CODEYE_REAL_REPORT")
	if path == "" {
		t.Skip("CODEYE_REAL_REPORT not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	r, err := Unmarshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := Digest(raw)
	for _, detail := range []string{DetailSummary, DetailFull} {
		md := RenderMarkdownAt(r, digest, Korean, detail)
		html, err := RenderHTMLAt(r, digest, Korean, detail)
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(md, []byte("%!")) || bytes.Contains(html, []byte("%!")) {
			t.Fatalf("format error in the %s rendering", detail)
		}
		checkInPageLinks(t, html)
		checkTableWidths(t, string(md))
		t.Logf("builder %s, %s: markdown %d lines, html %d bytes", r.Builder.Version, detail, strings.Count(string(md), "\n"), len(html))
		if out := os.Getenv("CODEYE_REAL_REPORT_OUT"); out != "" {
			if err := os.WriteFile(out+"."+detail+".md", md, 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
}
