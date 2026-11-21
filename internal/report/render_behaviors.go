package report

import (
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/sourceexcerpt"
)

type projectBehavior struct {
	Anchor, Claim, Status, Explanation, Additional string
	DocumentSource, CodeSource                     sourceCitation
	Preview                                        *sourceCitation
	Workflow                                       *workflowReadingView
	implementation                                 bool
}

type projectBehaviors struct {
	Description string
	Source      sourceCitation
	Rows        []projectBehavior
}

var purposeHeading = regexp.MustCompile(`(?i)^(overview|introduction|about(?: .+)?|what is .+|welcome to .+|개요|소개)[.!]?$`)
var usageHeading = regexp.MustCompile(`(?i)^(usage|features|quick start|quickstart|examples|how to use(?: .+)?|why .+|사용법|사용 예|주요 기능|기능|시작하기)$`)
var operationalHeading = regexp.MustCompile(`(?i)^(install|installation|building|configuration|parameters|deployment|requirements|environment variables|command-line flags|run .+|설치|환경 변수|설정|배포|요구사항)$`)
var usageFile = regexp.MustCompile(`(?i)^(readme(?:\.(?:md|markdown|rst))?|usage\.(?:md|markdown|rst)|user[-_ ]guide\.(?:md|markdown|rst))$`)
var operationalClaim = regexp.MustCompile(`(?i)\b(installation|deployment|cmake)\b|(?:설치|빌드|배포|환경 변수)|(?:경로|포트).*(?:기본값|기본으로|생략)`)
var proseEnding = regexp.MustCompile(`[.!?。！？]$`)

type behaviorDoc struct {
	evidence    SectionEvidence
	description *SectionEvidence
	usage       bool
	operational bool
}

func behaviorDocument(c SectionEvidence) behaviorDoc {
	doc := behaviorDoc{evidence: c}
	if c.Excerpt == nil || !usageFile.MatchString(path.Base(c.Path)) {
		return doc
	}
	// Prose directly below the title can introduce a README without headings.
	inPurpose, purpose, usage, fenced := c.Excerpt.StartLine <= 3, false, false, false
	for i, raw := range strings.Split(c.Excerpt.Text, "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		if strings.HasPrefix(line, "##") {
			heading := strings.TrimSpace(strings.Trim(line, "#"))
			inPurpose = purposeHeading.MatchString(heading)
			purpose = purpose || inPurpose
			usage = usage || usageHeading.MatchString(heading)
			doc.operational = doc.operational || operationalHeading.MatchString(heading)
			continue
		}
		n := c.Excerpt.StartLine + i
		if inPurpose && doc.description == nil && n >= c.StartLine && n <= c.EndLine &&
			len([]rune(line)) <= 160 && proseEnding.MatchString(line) &&
			!strings.ContainsAny(line, "`[]<>\"\\") && !strings.Contains(line, "://") && !strings.ContainsAny(line[:1], "#-+*!") {
			description := focusBehaviorEvidence(c, n, n, raw)
			doc.description = &description
		}
	}
	doc.usage = usage || !doc.operational && (purpose || doc.description != nil)
	return doc
}

// Focus an existing, visible citation on a smaller range. Preserve the host
// and snapshot identity, and only publish lines inside the cited excerpt.
func focusBehaviorEvidence(e SectionEvidence, start, end int, text string) SectionEvidence {
	e.StartLine, e.EndLine = start, end
	e.Excerpt = &sourceexcerpt.Excerpt{StartLine: start, EndLine: end, Text: text}
	return e
}

var responseCue = regexp.MustCompile(`\b(?:Status[A-Z]\w*|HTTP_[A-Z_]+|status_code)\b|\b(?:status|sendStatus|writeHead)\([2-5]\d\d\b`)
var exitCue = regexp.MustCompile(`^\s*(?:return\b|throw\b|raise\b)`)
var branchCue = regexp.MustCompile(`^\s*(?:if\b|else if\b|except\b|catch\b|}\s*else if\b)`)
var typedConditionCue = regexp.MustCompile(`\.\([\w.]+\)|\b(?:errors\.Is|instanceof|isinstance|except|catch)\b`)
var implementationOutcome = regexp.MustCompile(`^\s*(?:return|throw|raise)(?:\s+[^\s;/#}]|\()`)
var implementationBranch = regexp.MustCompile(`^\s*(?:if|elif|for|while|switch|match|except|catch)(?:\s+[\w(]|\()`)
var implementationUpdate = regexp.MustCompile(`^\s*[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)?\s*(?:\+=|-=|\*=|/=|:=)\s*\S`)

const maxProjectBehaviorRows = 4

func behaviorHasImplementation(e SectionEvidence) bool {
	if e.Excerpt == nil {
		return false
	}
	for i, line := range strings.Split(e.Excerpt.Text, "\n") {
		n := e.Excerpt.StartLine + i
		if n >= e.StartLine && n <= e.EndLine &&
			(implementationOutcome.MatchString(line) || implementationBranch.MatchString(line) || implementationUpdate.MatchString(line)) {
			return true
		}
	}
	return false
}

func behaviorStatusRank(status string) int {
	switch status {
	case "mismatch":
		return 0
	case "partial":
		return 1
	case "match":
		return 2
	default:
		return 3
	}
}

// Prefer small cited windows around explicit outcomes. The preview is quoted
// verbatim so this selection works without interpreting the source language.
func behaviorPreview(e SectionEvidence) (SectionEvidence, int) {
	if e.Excerpt == nil {
		return SectionEvidence{}, 0
	}
	lines := strings.Split(e.Excerpt.Text, "\n")
	var best SectionEvidence
	bestRank := 0
	for i, line := range lines {
		rank := 0
		switch {
		case responseCue.MatchString(line):
			rank = 2
		case exitCue.MatchString(line) && len(strings.Fields(line)) > 1:
			rank = 1
		}
		if rank == 0 {
			continue
		}
		start, end := i, i
		if i > 0 && branchCue.MatchString(lines[i-1]) {
			start = i - 1
			rank++
			if typedConditionCue.MatchString(lines[start]) {
				rank++
			}
			if i > 1 && strings.Contains(lines[i-2], "=") {
				start--
			}
		}
		if start == i {
			continue
		}
		if i+1 < len(lines) && exitCue.MatchString(lines[i+1]) {
			end++
		}
		startLine, endLine := e.Excerpt.StartLine+start, e.Excerpt.StartLine+end
		if rank <= bestRank || startLine < e.StartLine || endLine > e.EndLine {
			continue
		}
		text := strings.Join(lines[start:end+1], "\n")
		if len([]rune(text)) > 290 {
			continue
		}
		best, bestRank = focusBehaviorEvidence(e, startLine, endLine, text), rank
	}
	return best, bestRank
}

func (v secView) projectBehaviors() projectBehaviors {
	var result projectBehaviors
	if !v.R.Export.IncludeCode {
		return result
	}
	doc := v.R.Documents
	if doc == nil {
		return result
	}
	usage := make(map[string]behaviorDoc)
	descriptionLine := 0
	for _, e := range doc.Evidence {
		if e.Kind != "document" || !productLocation(e.Path, e.StartLine, e.EndLine) {
			continue
		}
		d := behaviorDocument(e)
		if d.description != nil && path.Dir(e.Path) == "." && strings.HasPrefix(strings.ToLower(e.Path), "readme") &&
			(descriptionLine == 0 || d.description.StartLine < descriptionLine) {
			result.Description, descriptionLine = strings.TrimSpace(d.description.Excerpt.Text), d.description.StartLine
			result.Source = v.citeSectionEvidence(*d.description)
		}
		if d.usage {
			usage[e.ID] = d
		}
	}
	seen := make(map[string]bool)
	for i, comparison := range doc.Comparisons {
		key := strengthTextKey(comparison.Statement)
		if openingText(comparison.Statement, "") == "" || seen[key] ||
			!productLocation(path.Join(comparison.AppliesTo, "scope"), 1, 1) {
			continue
		}
		var cited behaviorDoc
		for _, id := range comparison.DocumentEvidenceIDs {
			if d := usage[id]; d.evidence.ID != "" && !(d.operational && operationalClaim.MatchString(comparison.Statement)) {
				cited = d
				break
			}
		}
		if cited.evidence.ID == "" && comparison.RelatedWorkflow != nil {
			if e := bestProductEvidence(comparison.DocumentEvidenceIDs, doc.Evidence, "document"); e != nil {
				cited.evidence = *e
			}
		}
		if cited.evidence.ID == "" {
			continue
		}
		if comparison.Status != "unknown" && !hasProductSectionEvidence(comparison.CodeEvidenceIDs, doc.Evidence, "code") {
			continue
		}
		seen[key] = true
		row := projectBehavior{
			Anchor: comparisonAnchor(i), Claim: comparison.Statement, Status: comparison.Status,
			Explanation:    openingText(comparison.Reason, ""),
			DocumentSource: v.citeDecisionDocument(cited.evidence, comparison.Statement),
			Workflow:       v.workflowCitations(comparison.RelatedWorkflow, doc.Evidence),
		}
		codeRefs := comparison.CodeEvidenceIDs
		if extra := comparison.AdditionalObservation; extra != nil && openingText(extra.Reason, "") != "" && additionalPrimaryCode(comparison, doc.Evidence) != nil {
			if strengthTextKey(extra.Reason) != strengthTextKey(row.Explanation) {
				row.Additional = extra.Reason
			}
			codeRefs = append(slices.Clone(extra.CodeEvidenceIDs), codeRefs...)
		}
		if source := bestProductEvidence(codeRefs, doc.Evidence, "code"); source != nil {
			row.CodeSource = v.citeSectionEvidence(*source)
		}
		var bestRank int
		for _, e := range doc.Evidence {
			if e.Kind != "code" || !slices.Contains(codeRefs, e.ID) || !productLocation(e.Path, e.StartLine, e.EndLine) {
				continue
			}
			if row.Status == "unknown" {
				row.implementation = row.implementation || behaviorHasImplementation(e)
			}
			if excerpt, rank := behaviorPreview(e); rank > bestRank {
				source := v.citeSectionEvidence(excerpt)
				source.ActionLabel = v.L.Label("behavior.preview_source")
				row.Preview, bestRank = &source, rank
			}
		}
		result.Rows = append(result.Rows, row)
	}
	slices.SortStableFunc(result.Rows, func(a, b projectBehavior) int {
		if rank := behaviorStatusRank(a.Status) - behaviorStatusRank(b.Status); rank != 0 {
			return rank
		}
		if a.implementation == b.implementation {
			return 0
		}
		if a.implementation {
			return -1
		}
		return 1
	})
	return result
}

func (v secView) PriorityDecision() bool {
	d := v.Decision
	return d.Tone == "action" || d.Action != nil || d.State == v.L.Label("decision.comparison.mismatch") ||
		d.State == v.L.Label("decision.comparison.partial")
}

func (v *secView) buildProjectOpening() {
	v.Behaviors = v.projectBehaviors()
	v.Decision = v.projectDecision()
	if v.PriorityDecision() && v.Decision.Tone != "action" {
		v.Behaviors.Rows = slices.DeleteFunc(v.Behaviors.Rows, func(row projectBehavior) bool {
			return row.Anchor == v.Decision.ComparisonAnchor
		})
	}
	v.Behaviors.Rows = v.Behaviors.Rows[:min(len(v.Behaviors.Rows), maxProjectBehaviorRows)]
	if slices.ContainsFunc(v.Behaviors.Rows, func(row projectBehavior) bool {
		return row.Claim == v.Decision.ComparisonExample
	}) {
		v.Decision.ComparisonExample = ""
	}
}

func mdBehaviors(p func(...string), behavior projectBehaviors, l Locale) {
	if len(behavior.Rows) == 0 && behavior.Description == "" {
		return
	}
	heading := "behavior.heading"
	if len(behavior.Rows) == 0 {
		heading = "behavior.project_intro"
	}
	p("### ", mdText(l.Label(heading)))
	p()
	if behavior.Description != "" {
		p(mdText(behavior.Description))
		source := behavior.Source
		source.Text = ""
		mdCitations(p, []sourceCitation{source})
		p()
	}
	for _, row := range behavior.Rows {
		p("**", mdInlineComparison(row.Claim), "**")
		p()
		p(mdText(l.Label("behavior.documented")), " · ", mdText(l.Label("decision.comparison."+row.Status)))
		p()
		source := row.DocumentSource
		source.Text, source.Example = "", nil
		mdCitations(p, []sourceCitation{source})
		if row.Preview != nil {
			mdCitations(p, []sourceCitation{*row.Preview})
		}
		if row.Additional != "" {
			p("**", mdText(l.Label("decision.additional_observation")), "** ", mdInlineComparison(row.Additional))
		}
		if row.Explanation != "" {
			p("**", mdText(l.Label("behavior.recorded_comparison")), "** ", mdInlineComparison(row.Explanation))
		}
		if row.Preview == nil && row.CodeSource.Location != "" {
			code := row.CodeSource
			code.Text = ""
			mdCitations(p, []sourceCitation{code})
		}
		p("[", mdText(l.Label("behavior.open_comparison")), "](#", row.Anchor, ")")
		p()
		mdWorkflowCitations(p, row.Workflow)
	}
}
