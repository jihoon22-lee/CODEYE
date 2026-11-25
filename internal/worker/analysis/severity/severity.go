// Package severity is the default severity of tool diagnostics
// (docs/ANALYZERS.md#기본-심각도). Each analyzer pack keeps a table next to its
// rule pack that maps a rule (exact ID or prefix) and, as a fallback, the
// tool's own grade to a rule family; the family decides the severity. The
// mapping reads structured data only (analyzer, rule pack, rule ID, raw
// grade); no model takes part. A table names the rule-pack versions it was
// written for: a diagnostic of another rule-pack version, an unknown rule or
// an unknown analyzer keeps low. critical is never a default.
package severity

import (
	"slices"
	"strings"
)

// Severities a table can assign.
const (
	Info   = "info"
	Low    = "low"
	Medium = "medium"
	High   = "high"
)

// Family is a group of rules that report the same kind of construct. Merge
// says two tools reporting rules of this family on the same line of the
// same file report the same construct (triage groups them, never the
// findings, docs/REVIEW.md#도구-진단-분류).
type Family struct {
	Name     string
	Severity string
	Merge    bool
	// Lowered is the severity of the family on a path that is not shipped
	// code (a test, example, benchmark, generated or vendored file, see
	// PathRule): never above Severity.
	Lowered string
	// Label is the Korean name used in the fixed severity reason.
	Label string
}

// PathRule names the rule that lowers the default severity of a diagnostic
// on a path that is not shipped code (Family.Lowered). It is part of the ID
// a report tool row records (Table.RecordedID): change it with any change
// of a family's Lowered severity or of what counts as such a path, so that a
// comparison tells a rule difference from a change of a finding.
const PathRule = "paths.1"

// The families, most severe first. The rationale of each is in
// docs/ANALYZERS.md#기본-심각도.
var families = []Family{
	{"null_dereference", High, true, Low, "널·nil 역참조"},
	{"memory_safety", High, true, Low, "메모리 안전(범위 밖 접근, 해제 뒤 사용, 초기화하지 않은 값)"},
	{"undefined_behavior", High, true, Low, "정의되지 않은 동작"},
	{"concurrency", High, true, Low, "동시성(경합, 잠금 복사, 공유 변수 포획)"},
	{"injection", High, true, Low, "입력 주입(명령·SQL·경로·코드 실행)"},
	{"resource_leak", Medium, true, Low, "자원 누수(파일·연결·잠금·취소 함수 해제 누락)"},
	{"unchecked_error", Medium, true, Low, "오류 무시"},
	{"exception_handling", Medium, false, Low, "예외 처리(빈 처리부, 너무 넓은 처리, 처리 밖 예외)"},
	{"logic_error", Medium, true, Low, "의심스러운 논리(항상 같은 비교, 자기 대입, 잘못된 형식 문자열)"},
	{"api_misuse", Medium, true, Low, "API 오용(잘못된 인자, 계약 위반)"},
	{"type_error", Medium, false, Low, "타입·컴파일 오류"},
	{"security_weakness", Medium, false, Low, "보안 약점(약한 암호, 고정 자격증명, 안전하지 않은 설정)"},
	{"panic_path", Low, false, Info, "중단 경로(unwrap·panic·assert)"},
	{"deprecated_api", Low, false, Low, "사용 중단 API"},
	{"unused_code", Low, true, Low, "쓰지 않는 코드·값"},
	{"performance", Low, false, Info, "성능"},
	{"maintainability", Low, false, Info, "유지보수(복잡한 구조, 의존 규칙, 시험 코드 형태)"},
	{"documentation", Info, false, Info, "문서 주석"},
	{"style", Info, false, Info, "스타일·명명·단순화"},
	{"formatting", Info, false, Info, "서식"},
}

// Families lists every family, most severe first.
func Families() []Family { return slices.Clone(families) }

// FamilyOf returns a family by name.
func FamilyOf(name string) (Family, bool) {
	i := slices.IndexFunc(families, func(f Family) bool { return f.Name == name })
	if i < 0 {
		return Family{}, false
	}
	return families[i], true
}

// Rule maps a rule ID to a family. A pattern ending in "*" matches the IDs
// that start with the rest; any other pattern matches one ID exactly.
type Rule struct {
	Pattern string
	Family  string
	// Cap is the highest default severity of the rule's diagnostics ("" =
	// the family's). A table sets it when what the rule was measured to
	// report does not support the family's severity (the rationale is next
	// to the table); the family still decides same-line merging.
	Cap string
}

func (r Rule) matches(id string) bool {
	if p, ok := strings.CutSuffix(r.Pattern, "*"); ok {
		return strings.HasPrefix(id, p)
	}
	return r.Pattern == id
}

// Table is one analyzer's default-severity table. The first matching rule
// wins; Grades maps the tool's own grade of a rule no rule matched.
type Table struct {
	Analyzer string
	// RulePack and RulePackVersions are the rule pack the table was
	// written for (the envelope's rule_pack id and version).
	RulePack         string
	RulePackVersions []string
	// Version is the table's own version; change it with any mapping.
	Version string
	Rules   []Rule
	Grades  map[string]string
	// Default is the family of every rule neither Rules nor Grades placed
	// ("": such a rule keeps low as unknown).
	Default string
}

// ID names the table: <rule pack>/<table version>.
func (t Table) ID() string { return t.RulePack + "/" + t.Version }

// RecordedID names what decided the severity of a report tool row's
// findings: the table and the path rule (<rule pack>/<table version>+<path
// rule>). A comparison treats a difference in either as one tool-context
// difference, not as a change of each finding.
func (t Table) RecordedID() string { return t.ID() + "+" + PathRule }

// Result is the default severity of one diagnostic.
type Result struct {
	Severity string
	// Family is "" when no table rule or grade applied.
	Family string
	// Table is the ID of the table that applied, "" when none did.
	Table string
	// Mismatch says the analyzer's table exists but was written for
	// another rule-pack version.
	Mismatch bool
	// Capped says the table rule's Cap is below the family's severity:
	// Severity is the cap and the reason says so.
	Capped bool
	// Lowered says the diagnostic is on a path that is not shipped code and
	// the family's Lowered severity is below its Severity (after a Cap):
	// Severity is the lowered one and the reason says so.
	Lowered bool
}

// Known reports whether a family decided the severity.
func (r Result) Known() bool { return r.Family != "" }

// Lookup is the default severity of a diagnostic of analyzer with the rule
// pack id and version it ran with. notShipped says the diagnostic's file is
// not shipped code (record.PathClass): a family then gives its Lowered
// severity. A rule no table knows keeps low either way.
func Lookup(tables []Table, analyzer, rulePack, rulePackVersion, rule, grade string, notShipped bool) Result {
	out := Result{Severity: Low}
	for _, t := range tables {
		if t.Analyzer != analyzer || t.RulePack != rulePack {
			continue
		}
		if !slices.Contains(t.RulePackVersions, rulePackVersion) {
			out.Mismatch = true
			continue
		}
		name, limit := "", ""
		for _, r := range t.Rules {
			if r.matches(rule) {
				name, limit = r.Family, r.Cap
				break
			}
		}
		if name == "" && grade != "" {
			name = t.Grades[strings.ToLower(grade)]
		}
		if name == "" {
			name = t.Default
		}
		f, ok := FamilyOf(name)
		if !ok {
			return Result{Severity: Low, Table: t.ID()}
		}
		res := Result{Severity: f.Severity, Family: f.Name, Table: t.ID()}
		if limit != "" && Rank(limit) < Rank(res.Severity) {
			res.Severity, res.Capped = limit, true
		}
		// The path rule lowers what the table gave, capped or not.
		if notShipped && Rank(f.Lowered) < Rank(res.Severity) {
			res.Severity, res.Lowered = f.Lowered, true
		}
		return res
	}
	return out
}

// TableFor is the table of analyzer written for the rule pack id and
// version, if there is one.
func TableFor(tables []Table, analyzer, rulePack, rulePackVersion string) (Table, bool) {
	for _, t := range tables {
		if t.Analyzer == analyzer && t.RulePack == rulePack && slices.Contains(t.RulePackVersions, rulePackVersion) {
			return t, true
		}
	}
	return Table{}, false
}

// Fixed reason texts. They name no tool, rule or code, so a code-free export
// keeps them (Reasons).
const (
	reasonPrefix   = "기본 심각도: 규칙 계열 '"
	reasonSuffix   = "'에 정한 등급이다. 분석기 규칙 팩의 심각도 표에서 구조화 데이터로만 정했고 이 위치의 영향은 검토하지 않았다. 도구가 표시한 원문 등급은 관찰에 그대로 남는다."
	ReasonUnknown  = "기본 심각도 low: 이 규칙은 분석기의 심각도 표에 없다. 도구가 표시한 원문 등급은 관찰에 그대로 남고 이 위치의 영향은 검토하지 않았다."
	ReasonMismatch = "기본 심각도 low: 이 진단을 만든 규칙 팩 버전에 맞는 심각도 표가 없다. 도구가 표시한 원문 등급은 관찰에 그대로 남고 이 위치의 영향은 검토하지 않았다."
)

// cappedSuffix ends the reason of a severity a table rule's Cap held below
// the family's.
const cappedSuffix = "'에 속하지만, 이 규칙이 실제 코드에서 보고하는 것의 정확도가 계열 등급을 뒷받침한다는 측정이 없어 분석기 규칙 팩의 심각도 표가 계열보다 낮게 정한 등급이다. 구조화 데이터로만 정했고 이 위치의 영향은 검토하지 않았다. 도구가 표시한 원문 등급은 관찰에 그대로 남는다."

// loweredSuffix ends the reason of a severity the path rule lowered.
const loweredSuffix = "'에 정한 등급을, 이 위치가 시험·예제·벤치마크·생성물·외부 코드 경로여서 경로 규칙(" + PathRule + ")에 따라 낮춘 것이다. 분석기 규칙 팩의 심각도 표와 파일 경로만으로 정했고 이 위치의 영향은 검토하지 않았다. 도구가 표시한 원문 등급은 관찰에 그대로 남는다."

// Reason is the fixed sentence that explains r.
func Reason(r Result) string {
	if f, ok := FamilyOf(r.Family); ok {
		if r.Lowered {
			return reasonPrefix + f.Label + loweredSuffix
		}
		if r.Capped {
			return reasonPrefix + f.Label + cappedSuffix
		}
		return reasonPrefix + f.Label + reasonSuffix
	}
	if r.Mismatch {
		return ReasonMismatch
	}
	return ReasonUnknown
}

// Reasons is every sentence Reason can return.
func Reasons() []string {
	out := []string{ReasonUnknown, ReasonMismatch}
	for _, f := range families {
		out = append(out, reasonPrefix+f.Label+reasonSuffix)
		if f.Severity != Info {
			out = append(out, reasonPrefix+f.Label+cappedSuffix)
		}
		if Rank(f.Lowered) < Rank(f.Severity) {
			out = append(out, reasonPrefix+f.Label+loweredSuffix)
		}
	}
	return out
}

// Rank orders severities: info 1 through high 4, anything else 0.
func Rank(s string) int {
	switch s {
	case Info:
		return 1
	case Low:
		return 2
	case Medium:
		return 3
	case High:
		return 4
	}
	return 0
}

// Validate reports the first malformed part of a table: an unknown family,
// an empty or repeated pattern, a cap that is not below its family's
// severity, a table without its rule pack.
func (t Table) Validate() string {
	if t.Analyzer == "" || t.RulePack == "" || len(t.RulePackVersions) == 0 || t.Version == "" {
		return "table identity"
	}
	seen := map[string]bool{}
	for _, r := range t.Rules {
		f, ok := FamilyOf(r.Family)
		if !ok {
			return "family " + r.Family
		}
		if r.Cap != "" && (Rank(r.Cap) == 0 || Rank(r.Cap) >= Rank(f.Severity)) {
			return "cap " + r.Pattern
		}
		if r.Pattern == "" || r.Pattern == "*" || seen[r.Pattern] {
			return "pattern " + r.Pattern
		}
		seen[r.Pattern] = true
	}
	for g, name := range t.Grades {
		if _, ok := FamilyOf(name); !ok || g != strings.ToLower(g) {
			return "grade " + g
		}
	}
	if _, ok := FamilyOf(t.Default); t.Default != "" && !ok {
		return "default " + t.Default
	}
	return ""
}
