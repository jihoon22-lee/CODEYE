package report

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

// Fixed template texts of findings decoded from the generic analyzer
// envelope (docs/ANALYZERS.md). They name no tool and hold no code, so the code-free
// export keeps them verbatim (safeTemplates).
const (
	textToolSeverityReason = "분석 도구 진단의 심각도는 검토 전 기본값 low이다. 도구가 표시한 원문 등급은 관찰에 그대로 남기며 영향을 검토하지 않았다."
	textToolCondition      = "정적 분석 도구가 이 snapshot 위치에 진단을 보고했다."
	textToolVerification   = "수정 뒤 이 보고서의 실행 기록과 같은 argv로 같은 도구를 다시 실행해 진단이 사라지는지 확인한다."
	textToolEvidence       = "도구 출력 stream의 원시 진단(레코드 순번과 byte 범위로 위치 지정)."
	textRejectImpact       = "문자열·숫자·일반 객체처럼 Error가 아닌 값을 Promise의 거부 사유로 쓰면, 오류 처리부가 Error의 메시지와 스택을 기대할 때 실패 원인을 추적하기 어려울 수 있다."
	textRejectAction       = "근거에 표시된 거부 호출의 인자와 호출부의 오류 처리 계약을 확인한다. 호출부가 원래 값을 읽는다면 그 동작을 보존하면서 Error 객체를 전달할 수 있는지 검토한다. 이미 받은 Error 객체는 메시지와 스택을 보존하도록 그대로 전달한다."
	textFormatImpact       = "현재 파일의 서식이 기록된 gofmt 버전의 출력과 다르다."
	textFormatAction       = "해당 파일에 gofmt를 적용한 뒤 변경된 서식을 검토한다."
	textFormatVerification = "같은 버전의 gofmt -l로 해당 파일을 다시 검사해 출력이 없는지 확인한다."
	textFormatScope        = "파일 전체의 비교 결과이며 표시 위치는 파일 첫 줄이다."
	textRejectVerification = "해당 실패 경로를 실행해 기존 오류 처리 계약과 사용자 안내 문구가 유지되고 오류 처리부가 메시지와 스택을 얻는지 확인한다."
)

func isPromiseRejectDiagnostic(analyzerID, ruleID string) bool {
	return analyzerID == "javascript.eslint" && ruleID == "prefer-promise-reject-errors" ||
		analyzerID == "javascript.eslint_typed" && ruleID == "@typescript-eslint/prefer-promise-reject-errors"
}

// Limitation codes of the generic decoder.
const (
	LimColumnsNotBytes = "observation_columns_not_bytes"
	LimRawSeverityText = "observation_raw_severity_omitted"
)

// rawSeverityPattern is the tool grade a report observation may carry
// ("error", "warning", "note"): a short word, never free text, so the
// code-free export can keep it. Other grades stay in the envelope only.
var rawSeverityPattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,31}$`)

// AnalyzerDecoder decodes the generic analyzer observations envelope of the
// analyzer packs (obsdoc, kind analyzer_observations). Observations become report
// observations; resolved ones verified against the source manifest, not
// suppressed in source and not structure metrics become tool-reported
// findings that stay unreviewed
// (pending, never actionable without review). The row records the
// envelope's rule context (docs/ANALYZERS.md#규칙-문맥과-소스-안-억제). Only observations identical in analyzer, rule, location
// and message are grouped into one finding; a different rule or message on
// the same line stays a separate finding (TC-09).
type AnalyzerDecoder struct{}

func (AnalyzerDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: analysis.MediaObservations, SchemaVersion: obsdoc.SchemaVersion, Kind: obsdoc.Kind}
}

func (AnalyzerDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	var out Contribution
	if art.Role != analysis.RoleObservations {
		return out, invalidf("analyzer document must have role observations")
	}
	d, err := obsdoc.Decode(body, obsdoc.Expect{TaskKey: ctx.Task.Key, Snapshot: ctx.Snapshot.ManifestDigest, Lookup: ctx.Source.Lookup})
	if err != nil {
		return out, invalidf("analyzer document: %v", err)
	}
	if d.Analyzer.Required != ctx.Task.Required {
		return out, invalidf("analyzer document required flag differs from the accepted task")
	}
	accepted := ctx.Task.Status == string(domain.TaskSucceeded)
	switch {
	case d.Outcome.TaskStatus == domain.TaskSucceeded && !accepted:
		// The worker downgraded the handler outcome; the DB row is the authority.
		out.Limitations = append(out.Limitations, LimTaskStatusMismatch+":"+ctx.Task.Key)
	case d.Outcome.TaskStatus != domain.TaskSucceeded && accepted:
		return out, invalidf("analyzer result does not attest to accepted success")
	}
	// Every execution the document names must be the worker's record of it,
	// bound by the stream artifacts' hash and size.
	streams := map[string]*ArtifactRef{}
	for _, e := range d.Executions {
		ref, ok := ctx.Executions[e.Name]
		if !ok {
			return out, invalidf("analyzer document names an unrecorded execution %s", e.Name)
		}
		if accepted && (ref.Manifest.Status != executor.StatusExited || ref.Manifest.ExitCode == nil || e.ExitCode == nil || *ref.Manifest.ExitCode != *e.ExitCode) {
			return out, invalidf("analyzer document execution %s differs from the worker record", e.Name)
		}
		for stream, pair := range map[string]struct {
			art *ArtifactRef
			doc obsdoc.StreamRef
		}{"stdout": {ref.Stdout, e.Stdout}, "stderr": {ref.Stderr, e.Stderr}} {
			if pair.art == nil {
				continue
			}
			if pair.art.SHA256 != pair.doc.SHA256 || pair.art.SizeBytes != pair.doc.CapturedBytes {
				return out, invalidf("analyzer document execution %s %s is not the recorded stream", e.Name, stream)
			}
			streams[e.Name+"/"+stream] = pair.art
		}
	}
	groups := map[string][]Observation{}
	categories := map[string]string{}
	var order []string
	for _, o := range d.Observations {
		a := streams[o.RawRef.Execution+"/"+o.RawRef.Stream]
		if a == nil || a.SHA256 != o.RawRef.StreamSHA256 || o.RawRef.ByteEnd > a.SizeBytes {
			return out, invalidf("observation %s is not bound to a recorded stream artifact", o.ID)
		}
		msg := o.RawMessage
		severity := o.RawSeverity
		if severity != nil && !rawSeverityPattern.MatchString(*severity) {
			severity = nil
			out.Limitations = append(out.Limitations, LimRawSeverityText+":"+ctx.Task.Key)
		}
		obs := Observation{ID: o.ID, TaskKey: ctx.Task.Key, TaskExecutionID: ctx.TaskExecutionID, Tool: d.Analyzer.ID, ToolVersion: d.Tool.Version, RuleID: o.RuleID,
			RawSeverity: severity, RawMessage: &msg, LocationState: o.LocationState, EvidenceState: EvidenceUnresolved, SuggestedFixes: o.SuggestedFixes, Suppressed: o.Suppressed,
			RawRef: RawRef{ArtifactID: a.ID, SHA256: a.SHA256, StreamIndex: o.RawRef.RecordIndex, JSONPointer: o.RawRef.JSONPointer, ByteStart: o.RawRef.ByteStart, ByteEnd: o.RawRef.ByteEnd}}
		if obs.Tool == "cpp.cppcheck" || obs.Tool == "cpp.clang_tidy" || obs.Tool == "cpp.gcc_analyzer" {
			for _, r := range o.Related {
				kind := r.Kind
				if kind == "" {
					kind = obsdoc.RelatedPosition
					if obs.Tool == "cpp.clang_tidy" {
						kind = obsdoc.RelatedNote
					}
				}
				rel := RelatedObservation{Kind: kind, LocationState: r.LocationState}
				if r.RawMessage != "" && sourceexcerpt.SafeDiagnosticText(r.RawMessage) {
					rel.RawMessage = strPtr(r.RawMessage)
				}
				if l := r.Location; l != nil && slices.Contains(d.WorkspaceDrift, l.Path) {
					rel.LocationState = obsdoc.LocationSourceDrift
				} else if l != nil {
					rel.Location = &ObservationLocation{SnapshotID: ctx.Snapshot.ID, Path: l.Path, StartLine: l.StartLine, EndLine: l.EndLine, ColumnUnit: "byte", BlobSHA256: l.BlobSHA256}
					if l.ColumnUnit == "byte" {
						rel.Location.StartCol, rel.Location.EndCol = l.StartCol, l.EndCol
					}
				}
				if span := r.RawSpan; span != nil {
					rel.RawRef = &RawRef{ArtifactID: a.ID, SHA256: a.SHA256, StreamIndex: o.RawRef.RecordIndex, ByteStart: span.Start, ByteEnd: span.End}
				}
				obs.Related = append(obs.Related, rel)
			}
		}
		if m := o.Metric; m != nil {
			obs.Metric = &ObservationMetric{Name: m.Name, Value: m.Value, Threshold: m.Threshold, Unit: m.Unit}
		}
		if l := o.Location; l != nil {
			startCol, endCol := l.StartCol, l.EndCol
			if l.ColumnUnit != "byte" {
				// The report column unit is bytes; other units are not converted.
				startCol, endCol = 0, 0
				out.Limitations = append(out.Limitations, LimColumnsNotBytes+":"+ctx.Task.Key)
			}
			obs.Location = &ObservationLocation{SnapshotID: ctx.Snapshot.ID, Path: l.Path, StartLine: l.StartLine, EndLine: l.EndLine, StartCol: startCol, EndCol: endCol, ColumnUnit: "byte", BlobSHA256: l.BlobSHA256}
			if sha, ok := ctx.Source.Lookup(l.Path); ok && sha == l.BlobSHA256 {
				obs.EvidenceState = EvidenceVerified
			} else {
				obs.EvidenceState = EvidenceManifestMismatch
			}
			// A diagnostic hidden by an in-source suppression stays an
			// observation (the comparison reads it) and never becomes a
			// finding; a structure metric is no defect and never one either.
			if obs.EvidenceState == EvidenceVerified && !o.Suppressed && o.Metric == nil {
				k := strings.Join([]string{o.RuleID, l.Path, itoa(l.StartLine), itoa(startCol), msg}, "\x00")
				if _, seen := groups[k]; !seen {
					order = append(order, k)
				}
				groups[k] = append(groups[k], obs)
				categories[k] = o.Category
			}
		}
		if obs.Metric != nil && obs.Metric.Name == "duplicated_lines" {
			switch {
			case len(o.Related) != 1 || o.Related[0].Location == nil:
				obs.EvidenceState = EvidenceUnresolved
			default:
				l := o.Related[0].Location
				if sha, ok := ctx.Source.Lookup(l.Path); ok && sha == l.BlobSHA256 {
					obs.Metric.Counterpart = &ObservationLocation{SnapshotID: ctx.Snapshot.ID, Path: l.Path, StartLine: l.StartLine, EndLine: l.EndLine, ColumnUnit: "byte", BlobSHA256: l.BlobSHA256}
				} else {
					obs.EvidenceState = EvidenceManifestMismatch
				}
			}
		}
		out.Observations = append(out.Observations, obs)
	}
	sort.Strings(order)
	for _, k := range order {
		out.Findings = append(out.Findings, toolFinding(ctx, d.Analyzer.ID, categories[k], groups[k]))
	}
	tc := ToolCoverage{TaskKey: ctx.Task.Key, Analyzer: d.Analyzer.ID, ToolVersion: d.Tool.Version, ExitClass: d.Outcome.ExitClass, PartialParse: d.Outcome.PartialParse,
		IgnoredFiles: d.Coverage.FilesSkipped, UnlistedFiles: d.Coverage.FilesUnlisted, Packages: map[string]int{}, StderrFailures: len(d.ToolErrors), AnalyzerErrors: 0, Limitations: []string{},
		RuleContext: ruleContextOf(d.RulePack)}
	if d.Coverage.FilesInScope != nil {
		tc.FilesInScope = *d.Coverage.FilesInScope
	} else {
		tc.Limitations = append(tc.Limitations, "files_in_scope_unknown")
	}
	for k, v := range d.Coverage.Units {
		tc.Packages[k] = v
	}
	// Processed files count only for a task the controller accepted as
	// succeeded, or one that ran nothing (known zero). The paths themselves
	// (locations inspected again) are recorded only for an accepted
	// success.
	if paths := d.Coverage.ProcessedPaths; paths != nil && (accepted || d.Outcome.ExitClass == obsdoc.ExitNotRun) {
		n := len(paths)
		tc.ProcessedFiles = &n
		if accepted && n > 0 {
			tc.ProcessedPaths = sortedUnique(paths)
		}
	}
	for _, l := range d.Limitations {
		if codePattern.MatchString(l) {
			tc.Limitations = append(tc.Limitations, l)
		}
	}
	out.Tool = &tc
	purpose := d.Analyzer.Purpose
	if purpose == "" {
		purpose = PurposeDiagnostics
	}
	out.Analyzer = &AnalyzerIdentity{ID: d.Analyzer.ID, Ecosystem: d.Analyzer.Ecosystem, Purpose: purpose, Tool: d.Tool.Name, RulePack: d.RulePack.ID + "@" + d.RulePack.Version}
	return out, nil
}

// toolFinding builds the unreviewed finding of identical observations.
func toolFinding(ctx DecodeContext, analyzerID, category string, obs []Observation) Finding {
	first := obs[0]
	l := first.Location
	msg := *first.RawMessage
	idSum := sha256.Sum256([]byte(strings.Join([]string{ctx.Task.Key, analyzerID, first.RuleID, l.Path, itoa(l.StartLine), itoa(l.StartCol), msg}, "\x00")))
	id := "fnd_" + hex.EncodeToString(idSum[:])[:20]
	if category == "" {
		category = "correctness"
	}
	loc := Location{SnapshotID: ctx.Snapshot.ID, Path: l.Path, StartLine: l.StartLine, EndLine: l.EndLine}
	codeID := "ev_" + id[4:] + "_code"
	fp, basis := ToolFingerprint(analyzerID, first.RuleID, l.Path, "", msg)
	f := Finding{SchemaVersion: "0.1", ID: id, Title: truncateRunes(analyzerID+" "+first.RuleID+": "+msg, 160), Category: category, Severity: "low", SeverityReason: textToolSeverityReason,
		Validation: "unreviewed", Disposition: "open", Impact: textImpact, Locations: []Location{loc},
		Evidence:           []Evidence{{ID: codeID, Kind: "code", Description: textEvidenceCode, Location: &loc, BlobSHA256: l.BlobSHA256}},
		CounterevidenceIDs: []string{}, ObservationIDs: []string{}, Fingerprint: fp, FingerprintBasis: &basis,
		Recommendation: Recommendation{Summary: textRecommendation, Tradeoffs: []string{}}, Verification: []string{textToolVerification},
		Limitations: []string{textLimitManual, textLimitStaticOnly}}
	if isPromiseRejectDiagnostic(analyzerID, first.RuleID) {
		f.Impact = textRejectImpact
		f.Recommendation.Summary = textRejectAction
		f.Verification = []string{textRejectVerification}
	}
	if analyzerID == "go.gofmt" && first.RuleID == "format_mismatch" {
		f.Impact = textFormatImpact
		f.Recommendation.Summary = textFormatAction
		f.Verification = []string{textFormatVerification}
		f.Limitations = []string{textFormatScope}
	}
	var toolIDs []string
	for _, o := range obs {
		evID := "ev_" + strings.TrimPrefix(o.ID, "obs_") + "_tool"
		ev := Evidence{ID: evID, Kind: "tool", Description: textToolEvidence, ArtifactID: o.RawRef.ArtifactID, TaskExecutionID: o.TaskExecutionID}
		if o.RawRef.JSONPointer != "" {
			ptr := o.RawRef.JSONPointer
			ev.JSONPointer = &ptr
		}
		f.Evidence = append(f.Evidence, ev)
		f.ObservationIDs = append(f.ObservationIDs, o.ID)
		toolIDs = append(toolIDs, evID)
	}
	f.Conditions = []Condition{{Statement: textToolCondition, Status: "verified", EvidenceIDs: append([]string{}, toolIDs...)}}
	f.Claims = []Claim{{Assertion: analyzerID + " " + first.RuleID + " 진단: " + msg, Kind: "observed", EvidenceIDs: append(append([]string{}, toolIDs...), codeID)}}
	return f
}
