package report

import (
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

// reviewFinding turns one accepted review candidate and its verdict into a
// finding; v is the platform-validated verification, or the zero value when
// none was accepted.

func reviewFinding(snapshot string, c record.Candidate, v record.Verification, catalog map[string]record.Excerpt) Finding {
	title := c.Title
	if utf8.RuneCountInString(title) < 5 {
		title = "검토 후보 · " + title
	}
	f := Finding{SchemaVersion: SchemaVersion, ID: "review_" + c.ID, Title: title,
		Category: "correctness", Severity: "info", SeverityReason: "검증 결과에서 심각도를 확인하지 못했습니다.",
		Validation: "unreviewed", Disposition: "open", Impact: c.Impact,
		Conditions:         []Condition{{Statement: c.Condition, Status: "assumed", EvidenceIDs: c.Evidence}},
		Claims:             []Claim{{Assertion: c.Title, Kind: "inferred", EvidenceIDs: c.Evidence}},
		CounterevidenceIDs: []string{}, ObservationIDs: []string{}, Limitations: append([]string{}, v.Limitations...),
		Recommendation: Recommendation{Summary: c.Improvement, Tradeoffs: []string{}},
		Verification:   []string{"호출자와 테스트에서 후보의 발생 조건을 확인하세요."}}
	if v.Verdict != "" && v.Verdict != record.VerdictUnreviewed {
		f.Validation, f.Category, f.Severity, f.SeverityReason = v.Verdict, v.Category, v.Severity, v.SeverityReason
		f.Impact, f.Verification = v.Impact, v.NextVerification
		f.CounterevidenceIDs = append(f.CounterevidenceIDs, v.Counterevidence...)
		for _, cond := range v.ValidConditions {
			f.Conditions = append(f.Conditions, Condition{Statement: cond, Status: "assumed", EvidenceIDs: v.SupportingEvidence})
		}
		for _, cond := range v.InvalidConditions {
			f.Conditions = append(f.Conditions, Condition{Statement: "후보가 성립하지 않는 조건 · " + cond, Status: "assumed", EvidenceIDs: v.Counterevidence})
		}
		f.Limitations = append(f.Limitations, v.UnresolvedAssumptions...)
		if len(v.SupportingEvidence) != 0 {
			f.Claims = append(f.Claims, Claim{Assertion: v.Impact, Kind: "inferred", EvidenceIDs: v.SupportingEvidence})
		}
		if len(v.Counterevidence) != 0 && len(v.InvalidConditions) != 0 {
			f.Claims = append(f.Claims, Claim{Assertion: strings.Join(v.InvalidConditions, " "), Kind: "inferred", EvidenceIDs: v.Counterevidence})
		}
	}
	if f.Validation == record.VerdictUnreviewed {
		f.Limitations = append(f.Limitations, "후보가 성립하지 않을 가능성 · "+c.Counterpoint)
	}
	seen := map[string]bool{}
	for _, refs := range [][]string{c.Evidence, v.SupportingEvidence, v.Counterevidence} {
		for _, id := range refs {
			if seen[id] {
				continue
			}
			seen[id] = true
			e := catalog[id]
			loc := Location{SnapshotID: snapshot, Path: e.Path, StartLine: e.Start, EndLine: e.End, Symbol: e.Symbol}
			f.Locations = append(f.Locations, loc)
			kind := "code"
			if e.Kind == "document" || e.Kind == "config" {
				kind = "document"
			}
			f.Evidence = append(f.Evidence, Evidence{ID: id, Kind: kind, Description: "고정 스냅샷의 " + e.Kind + " 발췌 위치", Location: &loc, BlobSHA256: e.SHA256})
		}
	}
	setModelFingerprint(&f, c, catalog, nil)
	return f
}
