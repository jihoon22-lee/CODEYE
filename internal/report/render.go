package report

import (
	"bytes"
	"html/template"
	"strconv"
	"strings"
)

// Formats the API can export. JSON is the stored canonical document (or its
// code-free projection); Markdown and HTML are rendered from the same Report
// value, so every format shows the same Summary.
const (
	FormatJSON     = "json"
	FormatMarkdown = "markdown"
	FormatHTML     = "html"
)

// Formats lists the supported export formats.
var Formats = []string{FormatJSON, FormatMarkdown, FormatHTML}

// view is the shared, already-translated model of both text formats.
type view struct {
	R             Report
	L             Locale
	Digest        string
	DigestShort   string
	ManifestShort string
	Banner        string
	Counts        []countRow
	Limitations   []string
	Notes         []string
	Processed     string
	Message       map[string]string // observation id -> message or omission label
}

type countRow struct{ Name, Label, Value string }

func newView(r Report, digest string, l Locale) view {
	v := view{R: r, L: l, Digest: digest, DigestShort: short(digest), ManifestShort: short(r.Inputs.ManifestSHA256), Message: map[string]string{}}
	switch r.Status {
	case StatusPartial:
		v.Banner = l.Label("banner_partial")
	case StatusFailed:
		v.Banner = l.Label("banner_failed")
	case StatusCancelled:
		v.Banner = l.Label("banner_cancelled")
	default:
		v.Banner = l.Label("banner_completed")
	}
	s := r.Summary
	v.Counts = []countRow{
		{"raw_observations", "원시 도구 진단", strconv.Itoa(s.RawObservations)},
		{"unresolved_observations", "위치 미확인 진단", strconv.Itoa(s.UnresolvedObservations)},
		{"total_findings", "항목", strconv.Itoa(s.TotalFindings)},
		{"actionable_findings", "조치 항목(supported/reproduced)", strconv.Itoa(s.ActionableFindings)},
		{"pending_candidates", "미확정 후보(unreviewed/inconclusive)", strconv.Itoa(s.PendingCandidates)},
		{"refuted_findings", "기각(refuted)", strconv.Itoa(s.RefutedFindings)},
	}
	if s.MetricObservations > 0 {
		// Structure metrics (docs/ANALYZERS.md) are a subset of the raw
		// observations, shown apart and never as defects.
		v.Counts = append(v.Counts, countRow{"metric_observations", l.Label("metric_observations"), strconv.Itoa(s.MetricObservations)})
	}
	for _, c := range r.Limitations {
		v.Limitations = append(v.Limitations, l.Code(c))
	}
	for _, c := range r.Coverage.Notes {
		v.Notes = append(v.Notes, l.Code(c))
	}
	v.Processed = l.Label("unknown")
	if r.Coverage.ToolProcessedFiles != nil {
		v.Processed = strconv.Itoa(*r.Coverage.ToolProcessedFiles)
	}
	for _, o := range r.Observations {
		if o.RawMessage != nil {
			v.Message[o.ID] = *o.RawMessage
		} else {
			v.Message[o.ID] = l.Label("message_omitted")
		}
		if m := o.Metric; m != nil {
			v.Message[o.ID] = "[" + l.Label("metric_marker") + ": " + m.Name + " " + strconv.Itoa(m.Value) + " >= " + strconv.Itoa(m.Threshold) + " " + m.Unit + "] " + v.Message[o.ID]
		}
	}
	return v
}

func short(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// --- Markdown ----------------------------------------------------------------

// RenderMarkdown renders r. Every dynamic value is either escaped plain text
// or a code span, so tool text cannot create links, images, HTML or table
// structure.
func RenderMarkdown(r Report, digest string, l Locale) []byte {
	if r.hasSections() {
		return renderMarkdownSections(r, digest, l)
	}
	v := newView(r, digest, l)
	var b strings.Builder
	p := func(s ...string) {
		for _, x := range s {
			b.WriteString(x)
		}
		b.WriteByte('\n')
	}
	mdHeader(p, r, v, l)
	p("## ", mdText(l.Label("summary")))
	p()
	p("| ", mdText(l.Label("raw_to_findings")), " | 수 |")
	p("|---|---|")
	for _, c := range v.Counts {
		p("| ", mdText(c.Label), " | ", c.Value, " |")
	}
	p()
	if len(r.Summary.Priorities) > 0 {
		p("우선 확인:")
		for _, id := range r.Summary.Priorities {
			p("- ", mdCode(id))
		}
		p()
	}
	mdAIReview(p, r, l, "## ")
	mdCoverage(p, r, v, l, "## ")
	mdTasks(p, r, l, "## ")
	p("## ", mdText(l.Label("findings")))
	p()
	p(mdText(l.Label("code_excerpt_none")))
	p()
	if len(r.Findings) == 0 {
		p(mdText(l.Label("no_findings")))
		p()
	}
	for _, f := range r.Findings {
		mdFinding(p, f, "### ", false)
	}
	p("## ", mdText(l.Label("observations")))
	p()
	if len(r.Observations) == 0 {
		p(mdText(l.Label("no_observations")))
	} else {
		p("| id | task | rule | 위치 | 상태 | 근거 | 메시지 |")
		p("|---|---|---|---|---|---|---|")
		for _, o := range r.Observations {
			loc := l.Label("none")
			if o.Location != nil {
				loc = o.Location.Path + ":" + strconv.Itoa(o.Location.StartLine) + ":" + strconv.Itoa(o.Location.StartCol)
			}
			p("| ", mdCode(o.ID), " | ", mdCode(o.TaskKey), " | ", mdCode(o.RuleID), " | ", mdCode(loc), " | ", mdCode(o.LocationState), " | ", mdCode(o.EvidenceState), " | ", mdCode(v.Message[o.ID]), " |")
		}
	}
	p()
	mdAppendix(p, r, v, l, "## ")
	mdTechnical(p, r, v, l)
	p("digest sha256: ", mdCode(digest))
	return []byte(b.String())
}

// mdHeader is the identity table and status banner both layouts start with.
func mdHeader(p func(...string), r Report, v view, l Locale) {
	p("# ", mdText(l.Label("title")))
	p()
	p("| 항목 | 값 |")
	p("|---|---|")
	row := func(k, val string) { p("| ", mdText(k), " | ", val, " |") }
	for _, field := range v.Identity() {
		row(field.Label, mdText(field.Value))
	}
	p()
	p("> ", mdText(v.Banner))
	p()
}

// Identity shows only recorded facts. Older revisions have a publication time
// but no worker interval or project metadata.
func (v view) Identity() []countRow {
	var fields []countRow
	add := func(key, value string) {
		if value != "" {
			fields = append(fields, countRow{Name: key, Label: v.L.Label(key), Value: value})
		}
	}
	if p := v.R.Project; p != nil {
		add("identity.project", p.Name)
		add("identity.source", p.Source)
		add("identity.start", p.StartedAt)
		add("identity.finish", p.FinishedAt)
	}
	if v.R.Inputs.CommitSHA != nil {
		add("identity.commit", *v.R.Inputs.CommitSHA)
	}
	add("identity.status", v.L.Label("status."+v.R.Status))
	add("identity.verdict", v.L.Label("verdict."+v.R.QualityVerdict))
	if v.R.Project == nil || v.R.Project.FinishedAt == "" {
		add("identity.published", v.R.CreatedAt)
	}
	return fields
}

func mdTechnical(p func(...string), r Report, v view, l Locale) {
	p("### ", mdText(l.Label("identity.technical")))
	p()
	p("| 항목 | 값 |")
	p("|---|---|")
	row := func(k, val string) { p("| ", mdText(k), " | ", val, " |") }
	row("report", mdCode(r.ID)+" rev "+mdCode(strconv.Itoa(r.Revision)))
	row("digest", mdCode(v.DigestShort))
	row("status", mdCode(r.Status))
	row("quality_verdict", mdCode(r.QualityVerdict))
	row("run", mdCode(r.RunID))
	row("attempt", mdCode(r.Attempt.ID)+" #"+mdCode(strconv.FormatInt(r.Attempt.Number, 10))+" epoch "+mdCode(strconv.FormatInt(r.Attempt.LeaseEpoch, 10)))
	row("snapshot", mdCode(r.Inputs.SnapshotID)+" manifest "+mdCode(v.ManifestShort))
	if r.Inputs.CommitSHA != nil {
		row("commit", mdCode(*r.Inputs.CommitSHA))
	}
	row("profile", mdCode(r.Inputs.Profile))
	row("created_at", mdCode(r.CreatedAt))
	if r.Export.IncludeCode {
		row("export", mdText(l.Label("export_full")))
	} else {
		row("export", mdText(l.Label("export_codefree")))
	}
	p()
}

func mdAIReview(p func(...string), r Report, l Locale, h string) {
	p(h, mdText(l.Label("ai_review")))
	p()
	ai := r.Coverage.AIReview
	reason := ""
	if ai.Reason != nil {
		reason = " " + mdCode(*ai.Reason)
	}
	p("상태: ", mdCode(ai.Status), reason)
	p()
	p("| task | 상태 | 결과 |")
	p("|---|---|---|")
	for _, t := range ai.RequiredTasks {
		st := l.Label("none")
		if t.TaskStatus != nil {
			st = *t.TaskStatus
		}
		reason := ""
		if t.Reason != nil {
			reason = " " + mdCode(*t.Reason)
		}
		p("| ", mdCode(t.Key), " | ", mdCode(st), " | ", mdCode(t.Result), reason, " |")
	}
	p()
	if len(ai.OptionalTasks) > 0 {
		p(mdText(l.Label("ai_review_optional")))
		p()
		p("| task | 상태 | 결과 |")
		p("|---|---|---|")
		for _, t := range ai.OptionalTasks {
			st, reason := l.Label("none"), ""
			if t.TaskStatus != nil {
				st = *t.TaskStatus
			}
			if t.Reason != nil {
				reason = " " + mdCode(*t.Reason)
			}
			p("| ", mdCode(t.Key), " | ", mdCode(st), " | ", mdCode(t.Result), reason, " |")
		}
		p()
	}
	for _, c := range r.Coverage.ReviewCalls {
		pack := ""
		if c.PackID != "" {
			pack = " " + mdCode(c.PackID+"@"+c.PackVersion)
		}
		p("- 모델 호출 ", mdCode(c.Role), " · ", mdCode(c.Model), " · OpenCode ", mdCode(c.Version), " · ", mdCode(c.Outcome), " · 지침", pack, " ", mdCode(c.PackDigest))
	}
	p()
}

func mdCoverage(p func(...string), r Report, v view, l Locale, h string) {
	p(h, mdText(l.Label("coverage")))
	p()
	p("- scope: ", mdCode(r.Coverage.ScopeMode))
	p("- inventory_files: ", strconv.Itoa(r.Coverage.InventoryFiles))
	p("- tool_processed_files: ", mdText(v.Processed))
	llm := l.Label("unknown")
	if r.Coverage.LLMExposedFiles != nil {
		llm = strconv.Itoa(*r.Coverage.LLMExposedFiles)
	}
	p("- llm_exposed_files: ", mdText(llm))
	p("- test_status: ", mdCode(r.Coverage.TestStatus))
	for _, n := range v.Notes {
		p("- ", mdText(n))
	}
	p()
	if len(r.Coverage.Tools) > 0 {
		p("| task | 도구 | 버전 | exit class | 범위 파일 | 처리 파일 | 제외 | 미목록 | 패키지 상태 |")
		p("|---|---|---|---|---|---|---|---|---|")
		for _, t := range r.Coverage.Tools {
			ver := l.Label("unknown")
			if t.ToolVersion != nil {
				ver = *t.ToolVersion
			}
			proc := l.Label("unknown")
			if t.ProcessedFiles != nil {
				proc = strconv.Itoa(*t.ProcessedFiles)
			}
			p("| ", mdCode(t.TaskKey), " | ", mdCode(t.Analyzer), " | ", mdCode(ver), " | ", mdCode(t.ExitClass), " | ", strconv.Itoa(t.FilesInScope), " | ", mdText(proc), " | ",
				strconv.Itoa(t.IgnoredFiles), " | ", strconv.Itoa(t.UnlistedFiles), " | ", mdCode(packages(t.Packages)), " |")
		}
		p()
	}
}

func mdTasks(p func(...string), r Report, l Locale, h string) {
	p(h, mdText(l.Label("tasks")))
	p()
	p("| task | 필수 | 상태 | 사유 |")
	p("|---|---|---|---|")
	for _, t := range r.Tasks {
		reason := l.Label("none")
		if t.Reason != nil {
			reason = *t.Reason
		}
		p("| ", mdCode(t.Key), " | ", strconv.FormatBool(t.RequiredForVerdict), " | ", mdCode(t.Status), " | ", mdCode(reason), " |")
	}
	p()
}

// mdFinding renders one finding under heading level h.
func mdFinding(p func(...string), f Finding, h string, sectioned bool) {
	p(h, mdText(f.Title))
	p()
	p("- id: ", mdCode(f.ID))
	p("- category / severity: ", mdCode(f.Category), " / ", mdCode(f.Severity), " — ", mdText(f.SeverityReason))
	p("- validation / disposition: ", mdCode(f.Validation), " / ", mdCode(f.Disposition))
	for _, e := range f.Evidence {
		if e.Excerpt != nil && e.Location != nil {
			mdCitations(p, []sourceCitation{citeSource(e.Location.Path, e.Location.StartLine, e.Location.EndLine, e.Excerpt)})
		}
	}
	for _, loc := range f.Locations {
		p("- location: ", mdCode(loc.Path+":"+strconv.Itoa(loc.StartLine)+"-"+strconv.Itoa(loc.EndLine)))
	}
	for _, cond := range f.Conditions {
		p("- 발생 조건 (", mdCode(cond.Status), ") ", mdText(cond.Statement), " ", mdCode(strings.Join(cond.EvidenceIDs, ",")))
	}
	if len(f.CounterevidenceIDs) > 0 {
		p("- 반대 근거 ", mdCode(strings.Join(f.CounterevidenceIDs, ",")))
	}
	p("- impact: ", mdText(f.Impact))
	for _, c := range f.Claims {
		p("- claim (", mdCode(c.Kind), "): ", mdText(c.Assertion), " [", mdCode(strings.Join(c.EvidenceIDs, ",")), "]")
	}
	for _, e := range f.Evidence {
		ref := e.ArtifactID
		if e.Location != nil {
			loc := e.Location
			ref = loc.Path + ":" + strconv.Itoa(loc.StartLine)
			if sectioned {
				ref = sourceRange(loc.Path, loc.StartLine, loc.EndLine)
			}
			ref += " sha256 " + short(e.BlobSHA256)
		} else if e.JSONPointer != nil {
			ref += " " + *e.JSONPointer
		}
		p("- evidence ", mdCode(e.ID), " (", mdCode(e.Kind), "): ", mdText(e.Description), " ", mdCode(ref))
	}
	if len(f.ObservationIDs) > 0 {
		p("- observations: ", mdCode(strings.Join(f.ObservationIDs, ",")))
	}
	p("- recommendation: ", mdText(f.Recommendation.Summary))
	for _, x := range f.Verification {
		p("- verification: ", mdText(x))
	}
	for _, x := range f.Limitations {
		p("- limitation: ", mdText(x))
	}
	p()
}

// mdAppendix is the executions, artifacts and limitations of both layouts.
func mdAppendix(p func(...string), r Report, v view, l Locale, h string) {
	p(h, mdText(l.Label("executions")))
	p()
	p("| 실행 | 상태 | exit | 정리 | argv | cwd | stdout | stderr |")
	p("|---|---|---|---|---|---|---|---|")
	for _, e := range r.Executions {
		exit := l.Label("none")
		if e.ExitCode != nil {
			exit = strconv.Itoa(*e.ExitCode)
		}
		p("| ", mdCode(e.ID), " | ", mdCode(e.Status), " | ", mdCode(exit), " | ", mdCode(e.Cleanup), " | ", mdCode(strings.Join(e.Argv, " ")), " | ", mdCode(e.Cwd), " | ",
			mdCode(artRef(e.StdoutArtifactID, e.StdoutTruncated)), " | ", mdCode(artRef(e.StderrArtifactID, e.StderrTruncated)), " |")
	}
	p()
	p(h, mdText(l.Label("artifacts")))
	p()
	p("| id | task | role | media | sha256 | bytes | decoder |")
	p("|---|---|---|---|---|---|---|")
	for _, a := range r.Artifacts {
		task, dec := "-", "-"
		if a.TaskKey != nil {
			task = *a.TaskKey
		}
		if a.Decoder != nil {
			dec = *a.Decoder
		}
		p("| ", mdCode(a.ID), " | ", mdCode(task), " | ", mdCode(a.Role), " | ", mdCode(a.MediaType), " | ", mdCode(a.SHA256), " | ", strconv.FormatInt(a.SizeBytes, 10), " | ", mdCode(dec), " |")
	}
	p()
	p(h, mdText(l.Label("limitations")))
	p()
	for _, x := range v.Limitations {
		p("- ", mdText(x))
	}
	p()
}

func artRef(id *string, truncated bool) string {
	if id == nil {
		return "-"
	}
	if truncated {
		return *id + " (truncated)"
	}
	return *id
}

func packages(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.Itoa(m[k]))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func oneLine(s string) string {
	s = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ", "\t", " ", " ", " ", " ", " ").Replace(s)
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteRune(' ')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// mdText escapes plain text: CommonMark punctuation gets a backslash, HTML
// metacharacters become entities and table pipes are escaped.
func mdText(s string) string {
	s = oneLine(s)
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '\\', '`', '*', '_', '{', '}', '[', ']', '(', ')', '#', '+', '-', '!', '|', '~', ':', '.', '"', '\'', '=', '$', '%', '^':
			b.WriteByte('\\')
			b.WriteRune(r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func longestBacktickRun(s string) int {
	longest, run := 0, 0
	for _, r := range s {
		if r == '`' {
			run++
			if run > longest {
				longest = run
			}
		} else {
			run = 0
		}
	}
	return longest
}

// mdCode renders a code span whose fence is longer than any backtick run in s.
// Pipes are escaped for table cells (GFM unescapes them inside code spans).
func mdCode(s string) string {
	s = strings.ReplaceAll(oneLine(s), "|", "\\|")
	fence := strings.Repeat("`", longestBacktickRun(s)+1)
	if s == "" {
		return fence + " " + fence
	}
	pad := ""
	if strings.HasPrefix(s, "`") || strings.HasSuffix(s, "`") || strings.HasPrefix(s, " ") {
		pad = " "
	}
	return fence + pad + s + pad + fence
}

// --- HTML --------------------------------------------------------------------

// HTMLContentSecurityPolicy is sent as a header and repeated in a meta tag so
// a saved file keeps the policy: no script, no external fetch of any kind.
const HTMLContentSecurityPolicy = "default-src 'none'; style-src 'unsafe-inline'; img-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

const exportCSS = `body{box-sizing:border-box;margin:0 auto;max-width:76rem;padding:clamp(1rem,4vw,3rem);font-family:system-ui,sans-serif;line-height:1.65;color:#192b2a;background:#fff;overflow-wrap:anywhere}
h1{margin:0;padding:1.15rem 1.5rem;border-radius:.7rem;background:#153f39;color:#f4fbf8;font-size:clamp(1.35rem,3vw,2rem);letter-spacing:-.03em}
h2{margin-top:2.6rem;border-bottom:2px solid #25483d;padding-bottom:.55rem}h3{margin:1.5rem 0 .5rem}
table{width:100%;border-collapse:collapse;margin:.65rem 0 1.2rem;font-variant-numeric:tabular-nums}td,th{border-bottom:1px solid #d9e3df;padding:.5rem .65rem;text-align:left;vertical-align:top}th{background:#eff4f1;color:#334c43}
body>table:first-of-type th{width:8rem}.banner{border-left:3px solid #178066;padding:.65rem .95rem;background:#eff7f4}
code,pre{font-family:ui-monospace,monospace;white-space:pre-wrap;overflow-wrap:anywhere}pre{background:#172f2d;color:#eff6f1;border-radius:.6rem;padding:.95rem;tab-size:4}
a,summary{color:#116955}summary{cursor:pointer;font-weight:650}.model{color:#3e6357;font-size:.86em}.finding{border:1px solid #ceded6;padding:.4rem 1rem;margin:1rem 0;border-radius:.65rem}
.scroll{overflow-x:auto}nav ol{display:flex;flex-wrap:wrap;gap:.45rem 1.2rem;padding:.6rem 0;list-style:none;font-size:.92rem}
@media print{body{max-width:none;padding:0}h1{color:#172f2d;background:white}h2,h3,summary{break-after:avoid}pre,tr{break-inside:avoid}}
`

// htmlFuncs are the template functions of both HTML layouts.
var htmlFuncs = template.FuncMap{
	"itoa": strconv.Itoa,
	"i64":  func(n int64) string { return strconv.FormatInt(n, 10) },
	"deref": func(p *string, def string) string {
		if p == nil {
			return def
		}
		return *p
	},
	"derefInt": func(p *int, def string) string {
		if p == nil {
			return def
		}
		return strconv.Itoa(*p)
	},
	"join":     strings.Join,
	"short":    short,
	"code":     func(l Locale, c string) string { return l.Code(c) },
	"packages": packages,
	"artref":   artRef,
	"pair":     func(l Locale, p secPart) partArgs { return partArgs{L: l, P: p} },
}

var htmlTemplate = template.Must(template.New("report").Funcs(htmlFuncs).Parse(`<!DOCTYPE html>
<html lang="ko">
<head>
<meta charset="utf-8">
<meta http-equiv="Content-Security-Policy" content="` + HTMLContentSecurityPolicy + `">
<meta name="referrer" content="no-referrer">
<title>{{with .R.Project}}{{.Name}} · {{end}}{{.L.Label "title"}}</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style>` + exportCSS + `</style>
</head>
<body>
<h1>{{.L.Label "title"}}</h1>
<table>
{{range .Identity}}<tr><th scope="row">{{.Label}}</th><td>{{.Value}}</td></tr>{{end}}
</table>
<p class="banner" role="status">{{.Banner}}</p>
<h2 id="summary">{{.L.Label "summary"}}</h2>
<table><tr><th>{{.L.Label "raw_to_findings"}}</th><th>수</th></tr>
{{range .Counts}}<tr><td>{{.Label}}</td><td data-count="{{.Name}}">{{.Value}}</td></tr>
{{end}}</table>
{{if .R.Summary.Priorities}}<p>우선 확인:</p><ul>{{range .R.Summary.Priorities}}<li><a href="#{{.}}"><code>{{.}}</code></a></li>{{end}}</ul>{{end}}
<h2 id="ai-review">{{.L.Label "ai_review"}}</h2>
<p>상태: <code data-field="ai_review">{{.R.Coverage.AIReview.Status}}</code> {{with .R.Coverage.AIReview.Reason}}<code>{{.}}</code>{{end}}</p>
<table><tr><th>task</th><th>상태</th><th>결과</th></tr>
{{range .R.Coverage.AIReview.RequiredTasks}}<tr><td><code>{{.Key}}</code></td><td><code>{{deref .TaskStatus "없음"}}</code></td><td><code>{{.Result}}</code>{{with .Reason}} <code>{{.}}</code>{{end}}</td></tr>
{{end}}</table>
{{if .R.Coverage.AIReview.OptionalTasks}}<p>{{.L.Label "ai_review_optional"}}</p>
<table><tr><th>task</th><th>상태</th><th>결과</th></tr>
{{range .R.Coverage.AIReview.OptionalTasks}}<tr><td><code>{{.Key}}</code></td><td><code>{{deref .TaskStatus "없음"}}</code></td><td><code>{{.Result}}</code>{{with .Reason}} <code>{{.}}</code>{{end}}</td></tr>
{{end}}</table>{{end}}
{{if .R.Coverage.ReviewCalls}}<ul>{{range .R.Coverage.ReviewCalls}}<li>모델 호출 <code>{{.Role}}</code> · <code>{{.Model}}</code> · OpenCode <code>{{.Version}}</code> · <code>{{.Outcome}}</code> · 지침{{if .PackID}} <code>{{.PackID}}@{{.PackVersion}}</code>{{end}} <code>{{.PackDigest}}</code></li>{{end}}</ul>{{end}}
<h2 id="coverage">{{.L.Label "coverage"}}</h2>
<ul>
<li>scope: <code>{{.R.Coverage.ScopeMode}}</code></li>
<li>inventory_files: <span data-count="inventory_files">{{.R.Coverage.InventoryFiles}}</span></li>
<li>tool_processed_files: <span data-count="tool_processed_files">{{.Processed}}</span></li>
<li>llm_exposed_files: {{derefInt .R.Coverage.LLMExposedFiles "미확정"}}</li>
<li>test_status: <code>{{.R.Coverage.TestStatus}}</code></li>
{{range .Notes}}<li>{{.}}</li>{{end}}
</ul>
{{if .R.Coverage.Tools}}<div class="scroll"><table><tr><th>task</th><th>도구</th><th>버전</th><th>exit class</th><th>범위 파일</th><th>처리 파일</th><th>제외</th><th>미목록</th><th>패키지 상태</th></tr>
{{range .R.Coverage.Tools}}<tr><td><code>{{.TaskKey}}</code></td><td><code>{{.Analyzer}}</code></td><td><code>{{deref .ToolVersion "미확정"}}</code></td><td><code>{{.ExitClass}}</code></td><td>{{.FilesInScope}}</td><td>{{derefInt .ProcessedFiles "미확정"}}</td><td>{{.IgnoredFiles}}</td><td>{{.UnlistedFiles}}</td><td><code>{{packages .Packages}}</code></td></tr>
{{end}}</table></div>{{end}}
<h2 id="tasks">{{.L.Label "tasks"}}</h2>
<table><tr><th>task</th><th>필수</th><th>상태</th><th>사유</th></tr>
{{range .R.Tasks}}<tr><td><code>{{.Key}}</code></td><td>{{.RequiredForVerdict}}</td><td><code>{{.Status}}</code></td><td><code>{{deref .Reason "없음"}}</code></td></tr>
{{end}}</table>
<h2 id="findings">{{.L.Label "findings"}}</h2>
<p>{{.L.Label "code_excerpt_none"}}</p>
{{if not .R.Findings}}<p>{{.L.Label "no_findings"}}</p>{{end}}
{{range .R.Findings}}<section class="finding" id="{{.ID}}" data-finding="{{.ID}}">
<h3>{{.Title}}</h3>
<ul>
<li>category / severity: <code>{{.Category}}</code> / <code>{{.Severity}}</code> — {{.SeverityReason}}</li>
<li>validation / disposition: <code>{{.Validation}}</code> / <code>{{.Disposition}}</code></li>
{{range .Locations}}<li>location: <code>{{.Path}}:{{.StartLine}}-{{.EndLine}}</code></li>{{end}}
{{range .Conditions}}<li>발생 조건 (<code>{{.Status}}</code>) {{.Statement}} <code>{{join .EvidenceIDs ","}}</code></li>{{end}}
{{if .CounterevidenceIDs}}<li>반대 근거 <code>{{join .CounterevidenceIDs ","}}</code></li>{{end}}
<li>impact: {{.Impact}}</li>
{{range .Claims}}<li>claim (<code>{{.Kind}}</code>): {{.Assertion}} <code>{{join .EvidenceIDs ","}}</code></li>{{end}}
{{range .Evidence}}<li>evidence <code>{{.ID}}</code> (<code>{{.Kind}}</code>): {{.Description}} {{with .Location}}<code>{{.Path}}:{{.StartLine}}</code>{{end}} {{with .ArtifactID}}<code>{{.}}</code>{{end}} {{with .JSONPointer}}<code>{{.}}</code>{{end}}</li>{{end}}
{{range .ObservationIDs}}<li>observation <a href="#{{.}}"><code>{{.}}</code></a></li>{{end}}
<li>recommendation: {{.Recommendation.Summary}}</li>
{{range .Verification}}<li>verification: {{.}}</li>{{end}}
{{range .Limitations}}<li>limitation: {{.}}</li>{{end}}
</ul></section>
{{end}}
<h2 id="observations">{{.L.Label "observations"}}</h2>
{{if not .R.Observations}}<p>{{.L.Label "no_observations"}}</p>{{else}}<div class="scroll"><table><tr><th>id</th><th>task</th><th>rule</th><th>위치</th><th>상태</th><th>근거</th><th>메시지</th></tr>
{{$m := .Message}}{{range .R.Observations}}<tr id="{{.ID}}" data-observation="{{.ID}}"><td><code>{{.ID}}</code></td><td><code>{{.TaskKey}}</code></td><td><code>{{.RuleID}}</code></td><td>{{with .Location}}<code>{{.Path}}:{{.StartLine}}:{{.StartCol}}</code>{{else}}없음{{end}}</td><td><code>{{.LocationState}}</code></td><td><code>{{.EvidenceState}}</code></td><td><code>{{index $m .ID}}</code></td></tr>
{{end}}</table></div>{{end}}
<h2 id="executions">{{.L.Label "executions"}}</h2>
<div class="scroll"><table><tr><th>실행</th><th>상태</th><th>exit</th><th>정리</th><th>argv</th><th>cwd</th><th>stdout</th><th>stderr</th></tr>
{{range .R.Executions}}<tr><td><code>{{.ID}}</code></td><td><code>{{.Status}}</code></td><td>{{derefInt .ExitCode "없음"}}</td><td><code>{{.Cleanup}}</code></td><td><code>{{join .Argv " "}}</code></td><td><code>{{.Cwd}}</code></td><td><code>{{artref .StdoutArtifactID .StdoutTruncated}}</code></td><td><code>{{artref .StderrArtifactID .StderrTruncated}}</code></td></tr>
{{end}}</table></div>
<h2 id="artifacts">{{.L.Label "artifacts"}}</h2>
<div class="scroll"><table><tr><th>id</th><th>task</th><th>role</th><th>media</th><th>sha256</th><th>bytes</th><th>decoder</th></tr>
{{range .R.Artifacts}}<tr><td><code>{{.ID}}</code></td><td><code>{{deref .TaskKey "-"}}</code></td><td><code>{{.Role}}</code></td><td><code>{{.MediaType}}</code></td><td><code>{{.SHA256}}</code></td><td>{{i64 .SizeBytes}}</td><td><code>{{deref .Decoder "-"}}</code></td></tr>
{{end}}</table></div>
<h2 id="limitations">{{.L.Label "limitations"}}</h2>
<ul>{{range .Limitations}}<li>{{.}}</li>{{end}}</ul>
<details><summary>{{.L.Label "identity.technical"}}</summary>
<table>
<tr><th>report</th><td><code>{{.R.ID}}</code> rev <span data-field="revision">{{.R.Revision}}</span></td></tr>
<tr><th>digest</th><td><code data-field="digest">{{.DigestShort}}</code></td></tr>
<tr><th>status</th><td><code data-field="status">{{.R.Status}}</code></td></tr>
<tr><th>quality_verdict</th><td><code data-field="quality_verdict">{{.R.QualityVerdict}}</code></td></tr>
<tr><th>run</th><td><code>{{.R.RunID}}</code></td></tr>
<tr><th>attempt</th><td><code>{{.R.Attempt.ID}}</code> #{{i64 .R.Attempt.Number}} epoch {{i64 .R.Attempt.LeaseEpoch}}</td></tr>
<tr><th>snapshot</th><td><code>{{.R.Inputs.SnapshotID}}</code> manifest <code>{{.ManifestShort}}</code></td></tr>
{{with .R.Inputs.CommitSHA}}<tr><th>commit</th><td><code>{{.}}</code></td></tr>{{end}}
<tr><th>profile</th><td><code>{{.R.Inputs.Profile}}</code></td></tr>
<tr><th>created_at</th><td><code>{{.R.CreatedAt}}</code></td></tr>
<tr><th>export</th><td>{{if .R.Export.IncludeCode}}{{.L.Label "export_full"}}{{else}}{{.L.Label "export_codefree"}}{{end}}</td></tr>
</table>
<p>digest sha256: <code>{{.Digest}}</code></p>
</details>
</body>
</html>
`))

// RenderHTML renders a self-contained page: html/template escaping, no
// script, no external resource, links only to in-page fragments.
func RenderHTML(r Report, digest string, l Locale) ([]byte, error) {
	if r.hasSections() {
		return renderHTMLSections(r, digest, l)
	}
	var buf bytes.Buffer
	if err := htmlTemplate.Execute(&buf, newView(r, digest, l)); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
