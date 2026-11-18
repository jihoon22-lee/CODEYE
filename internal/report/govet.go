package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
)

// PolicyGoVet is the deterministic mapping from go vet analyzers to finding
// categories. go vet reports no severity; the policy never raises one.
const PolicyGoVet = "go-vet-v1"

var goVetCategory = map[string]string{
	"appends": "correctness", "asmdecl": "correctness", "assign": "correctness", "atomic": "concurrency",
	"bools": "correctness", "buildtag": "maintainability", "cgocall": "correctness", "composites": "maintainability",
	"copylocks": "concurrency", "defers": "correctness", "directive": "maintainability", "errorsas": "error_handling",
	"framepointer": "correctness", "hostport": "correctness", "httpresponse": "resource_lifecycle", "ifaceassert": "correctness",
	"loopclosure": "concurrency", "lostcancel": "resource_lifecycle", "nilfunc": "correctness", "printf": "correctness",
	"shift": "correctness", "sigchanyzer": "concurrency", "slog": "correctness", "stdmethods": "api_contract",
	"stdversion": "api_contract", "stringintconv": "correctness", "structtag": "api_contract", "testinggoroutine": "concurrency",
	"tests": "test_gap", "timeformat": "correctness", "unmarshal": "correctness", "unreachable": "maintainability",
	"unsafeptr": "correctness", "unusedresult": "correctness", "waitgroup": "concurrency",
}

// Fixed template texts of tool-derived findings. They contain no code and no
// tool text, so the code-free export keeps them verbatim (see safeText).
const (
	textSeverityReason   = "정책 go-vet-v1: go vet은 심각도를 제공하지 않으며 영향을 검토하지 않았다. 검토 전 기본값 low이다."
	textSeverityUnmapped = "정책 go-vet-v1: 분류표에 없는 analyzer라 정확성 범주로 두었다. go vet은 심각도를 제공하지 않으며 영향을 검토하지 않았다. 검토 전 기본값 low이다."
	textCondition        = "go vet이 이 snapshot 위치에 진단을 보고했다."
	textImpact           = "도구가 보고한 진단이다. 실제 영향은 검토하지 않았다(validation=unreviewed)."
	textRecommendation   = "도구 메시지와 해당 snapshot 코드를 확인한다."
	textVerification     = "수정 뒤 이 보고서의 실행 기록과 같은 argv로 go vet을 다시 실행해 진단이 사라지는지 확인한다."
	textLimitManual      = "사람이나 AI가 검토하지 않은 도구 진단이다."
	textLimitStaticOnly  = "정적 도구 관찰만 포함하며 실행으로 재현하지 않았다."
	textEvidenceTool     = "go vet stdout의 원시 진단(JSON pointer로 위치 지정)."
	textEvidenceCode     = "진단 위치. 경로와 blob hash를 snapshot manifest와 대조했다. 줄 범위는 노드가 manifest hash와 같은 snapshot 파일로 확인했다."
	textOmitted          = "원문 생략(코드 미포함 내보내기)."
)

// GoVetDecoder decodes the W08 go vet observations artifact.
type GoVetDecoder struct{}

func (GoVetDecoder) Key() DecoderKey {
	return DecoderKey{MediaType: analysis.MediaObservations, SchemaVersion: govet.DocumentSchemaVersion, Kind: govet.DocumentKind}
}

func (GoVetDecoder) Decode(ctx DecodeContext, art ArtifactRef, body []byte) (Contribution, error) {
	var out Contribution
	if art.Role != analysis.RoleObservations {
		return out, invalidf("go vet document must have role observations")
	}
	var d govet.Document
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&d); err != nil || !json.Valid(body) {
		return out, invalidf("go vet document is not valid JSON of the expected shape")
	}
	if d.SchemaVersion != govet.DocumentSchemaVersion || d.Kind != govet.DocumentKind || d.AnalyzerID != govet.AnalyzerID || d.ParserID != govet.ParserID {
		return out, invalidf("go vet document header mismatch")
	}
	if d.TaskKey != ctx.Task.Key || d.ManifestDigest != ctx.Snapshot.ManifestDigest {
		return out, invalidf("go vet document is bound to another task or snapshot")
	}
	if d.Outcome.TaskStatus == domain.TaskSucceeded && ctx.Task.Status != string(domain.TaskSucceeded) {
		// The worker downgraded the handler outcome; the DB row is the authority.
		out.Limitations = append(out.Limitations, LimTaskStatusMismatch+":"+ctx.Task.Key)
	}
	if err := validateGoVetDocument(&d, ctx.Task.Status); err != nil {
		return out, err
	}
	// Raw references bind per task: this task's go.vet execution stdout artifact.
	vet, haveVet := ctx.Executions["go.vet"]
	var stdout *ArtifactRef
	if haveVet {
		stdout = vet.Stdout
	}
	var docVet *govet.ExecSummary
	for i := range d.Executions {
		if d.Executions[i].Name == "go.vet" {
			docVet = &d.Executions[i]
		}
	}
	if len(d.Observations) > 0 || d.Outcome.TaskStatus == domain.TaskSucceeded {
		if stdout == nil || stdout.Role != analysis.RoleStdout || docVet == nil || docVet.Stdout.SHA256 != stdout.SHA256 || docVet.Stdout.CapturedBytes != stdout.SizeBytes {
			return out, invalidf("observations are not bound to this task's go.vet stdout artifact")
		}
	}
	groups := map[string][]Observation{}
	var order []string
	for _, o := range d.Observations {
		if o.Tool != govet.AnalyzerID || o.RuleID == "" || o.RawRef.StreamSHA256 != stdout.SHA256 || o.RawRef.Execution != "go.vet" || o.RawRef.Stream != "stdout" {
			return out, invalidf("observation %s raw reference does not match", o.ID)
		}
		if !utf8.ValidString(o.RawMessage) {
			return out, invalidf("observation message is not UTF-8")
		}
		msg := o.RawMessage
		obs := Observation{ID: o.ID, TaskKey: ctx.Task.Key, TaskExecutionID: ctx.TaskExecutionID, Tool: o.Tool, ToolVersion: o.ToolVersion, RuleID: o.RuleID,
			RawSeverity: nil, RawMessage: &msg, LocationState: o.LocationState, EvidenceState: EvidenceUnresolved, SuggestedFixes: o.SuggestedFixes,
			RawRef: RawRef{ArtifactID: stdout.ID, SHA256: stdout.SHA256, StreamIndex: o.RawRef.StreamIndex, JSONPointer: o.RawRef.JSONPointer, ByteStart: o.RawRef.ByteStart, ByteEnd: o.RawRef.ByteEnd}}
		if o.RawRef.ByteStart < 0 || o.RawRef.ByteEnd <= o.RawRef.ByteStart || o.RawRef.ByteEnd > stdout.SizeBytes {
			return out, invalidf("observation byte range is outside the stdout artifact")
		}
		if (o.LocationState == govet.LocationResolved) != (o.Location != nil) {
			return out, invalidf("observation location state is inconsistent")
		}
		if o.Location != nil {
			l := o.Location
			if o.Path == nil || *o.Path != l.Path || domain.ValidateEvidencePath(l.Path) != nil || l.StartCol < 0 || l.EndCol < 0 || l.StartLine < 1 || l.EndLine < l.StartLine || l.ColumnUnit != "byte" || !hex64.MatchString(l.BlobSHA256) {
				return out, invalidf("observation location is malformed")
			}
			obs.Location = &ObservationLocation{SnapshotID: ctx.Snapshot.ID, Path: l.Path, StartLine: l.StartLine, EndLine: l.EndLine, StartCol: l.StartCol, EndCol: l.EndCol, ColumnUnit: l.ColumnUnit, BlobSHA256: l.BlobSHA256}
			if sha, ok := ctx.Source.Lookup(l.Path); ok && sha == l.BlobSHA256 {
				obs.EvidenceState = EvidenceVerified
				k := strings.Join([]string{o.RuleID, l.Path, itoa(l.StartLine), msg}, "\x00")
				if _, seen := groups[k]; !seen {
					order = append(order, k)
				}
				groups[k] = append(groups[k], obs)
			} else {
				obs.EvidenceState = EvidenceManifestMismatch
			}
		}
		out.Observations = append(out.Observations, obs)
	}
	sort.Strings(order)
	for _, k := range order {
		out.Findings = append(out.Findings, goVetFinding(ctx, groups[k]))
	}
	tc := ToolCoverage{TaskKey: ctx.Task.Key, Analyzer: govet.AnalyzerID, ToolVersion: d.ToolVersion, ExitClass: d.Outcome.ExitClass, PartialParse: d.Outcome.PartialParse,
		FilesInScope: d.Coverage.GoFilesInScope, ProcessedFiles: d.Coverage.ProcessedGoFiles, IgnoredFiles: d.Coverage.IgnoredGoFiles, UnlistedFiles: d.Coverage.UnlistedGoFiles,
		Packages: map[string]int{}, StderrFailures: len(d.StderrFailures), AnalyzerErrors: len(d.AnalyzerErrors), Limitations: []string{}}
	for k, v := range d.Coverage.Packages {
		tc.Packages[k] = v
	}
	// A task the controller did not accept as succeeded never contributes a
	// processed-file count, whatever the document says.
	if ctx.Task.Status != string(domain.TaskSucceeded) {
		tc.ProcessedFiles = nil
	}
	tc.ProcessedPaths = goVetProcessedPaths(&d, ctx)
	for _, l := range d.Limitations {
		if codePattern.MatchString(l) {
			tc.Limitations = append(tc.Limitations, l)
		}
	}
	out.Tool = &tc
	return out, nil
}

// Validate the W08 cross-field contract before using an artifact for coverage
// or a verdict. A well-formed envelope alone cannot attest to a clean run.
func validateGoVetDocument(d *govet.Document, acceptedStatus string) error {
	if !d.Outcome.TaskStatus.Terminal() || len(d.Observations) != d.Parse.Observations || d.Observations == nil || d.Inventory == nil || d.Executions == nil || d.Coverage.Packages == nil {
		return invalidf("invalid go vet outcome, inventory or observation count")
	}
	unresolved := 0
	for _, o := range d.Observations {
		if o.LocationState != govet.LocationResolved {
			unresolved++
		}
		if o.RawSeverity != nil || o.RawRef.StreamIndex < 0 || o.SuggestedFixes < 0 || !strings.HasPrefix(o.RawRef.JSONPointer, "/") {
			return invalidf("invalid go vet observation metadata")
		}
	}
	if unresolved != d.Parse.UnresolvedObservations || d.Coverage.GoFilesInScope < 0 || d.Coverage.IgnoredGoFiles < 0 || d.Coverage.UnlistedGoFiles < 0 || (d.Coverage.ProcessedGoFiles != nil && *d.Coverage.ProcessedGoFiles < 0) {
		return invalidf("invalid go vet coverage counts")
	}
	states := map[string]int{}
	for _, pkg := range d.Inventory {
		states[pkg.State]++
	}
	if !maps.Equal(states, d.Coverage.Packages) {
		return invalidf("go vet package counts do not match inventory")
	}
	if d.Outcome.TaskStatus != domain.TaskSucceeded {
		if acceptedStatus == string(domain.TaskSucceeded) {
			return invalidf("go vet result does not attest to accepted success")
		}
		return nil
	}
	expectedClass := govet.ExitSuccessWithoutFindings
	if len(d.Observations) > 0 {
		expectedClass = govet.ExitSuccessWithFindings
	}
	if !d.Parse.OK || d.Parse.UnparsedBytes != 0 || d.Outcome.PartialParse || d.Outcome.ExitClass != expectedClass || len(d.WorkspaceDrift) != 0 || states[govet.PackageAnalyzedClean]+states[govet.PackageAnalyzedWithFindings] == 0 {
		return invalidf("incomplete go vet result claims success")
	}
	names := []string{"go.version", "go.modedit", "go.list", "go.vet"}
	if len(d.Executions) != len(names) {
		return invalidf("missing go vet execution")
	}
	for i, e := range d.Executions {
		if names[i] != e.Name || e.Status != "exited" || e.ExitCode == nil || *e.ExitCode != 0 || e.Cleanup != "confirmed" || e.Stdout.Truncated || e.Stdout.Incomplete {
			return invalidf("go vet claims success without clean executions")
		}
	}
	return nil
}

func goVetFinding(ctx DecodeContext, obs []Observation) Finding {
	first := obs[0]
	l := first.Location
	msg := *first.RawMessage
	fp, basis := ToolFingerprint(govet.AnalyzerID, first.RuleID, l.Path, "", msg)
	idSum := sha256.Sum256([]byte(strings.Join([]string{ctx.Task.Key, first.RuleID, l.Path, itoa(l.StartLine), msg}, "\x00")))
	id := "fnd_" + hex.EncodeToString(idSum[:])[:20]
	category, reason := goVetCategory[first.RuleID], textSeverityReason
	if category == "" {
		category, reason = "correctness", textSeverityUnmapped
	}
	loc := Location{SnapshotID: ctx.Snapshot.ID, Path: l.Path, StartLine: l.StartLine, EndLine: l.EndLine}
	codeID := "ev_" + id[4:] + "_code"
	f := Finding{SchemaVersion: "0.1", ID: id, Title: truncateRunes("go vet "+first.RuleID+": "+msg, 160), Category: category, Severity: "low", SeverityReason: reason,
		Validation: "unreviewed", Disposition: "open", Impact: textImpact, Locations: []Location{loc},
		Evidence:           []Evidence{{ID: codeID, Kind: "code", Description: textEvidenceCode, Location: &loc, BlobSHA256: l.BlobSHA256}},
		CounterevidenceIDs: []string{}, ObservationIDs: []string{}, Fingerprint: fp, FingerprintBasis: &basis,
		Recommendation: Recommendation{Summary: textRecommendation, Tradeoffs: []string{}}, Verification: []string{textVerification},
		Limitations: []string{textLimitManual, textLimitStaticOnly}}
	var toolIDs []string
	for _, o := range obs {
		ptr := o.RawRef.JSONPointer
		evID := "ev_" + strings.TrimPrefix(o.ID, "obs_") + "_tool"
		f.Evidence = append(f.Evidence, Evidence{ID: evID, Kind: "tool", Description: textEvidenceTool, ArtifactID: o.RawRef.ArtifactID, TaskExecutionID: o.TaskExecutionID, JSONPointer: &ptr})
		f.ObservationIDs = append(f.ObservationIDs, o.ID)
		toolIDs = append(toolIDs, evID)
	}
	f.Conditions = []Condition{{Statement: textCondition, Status: "verified", EvidenceIDs: append([]string{}, toolIDs...)}}
	f.Claims = []Claim{{Assertion: "go vet " + first.RuleID + " 진단: " + msg, Kind: "observed", EvidenceIDs: append(append([]string{}, toolIDs...), codeID)}}
	return f
}

func truncateRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
