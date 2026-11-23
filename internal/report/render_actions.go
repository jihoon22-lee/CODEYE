package report

import (
	"cmp"
	"html"
	"slices"
	"strconv"
	"strings"
)

// The action-first opening of the overview: what to fix and why comes first,
// the coverage ledgers follow. Everything here is read from the stored
// report; no number, severity or sentence is made up. A reader gets, in order:
//
//  1. the action cards (supported or reproduced findings on primary paths),
//  2. documents that disagree with the implementation,
//  3. notices of other sections (secret detections, environment mismatches),
//  4. candidates that need more checking and the tool diagnostics grouped by
//     analyzer and rule, both collapsed.
//
// The coverage line near the top links to the status table below so that
// missing, failed or unsupported work is never read as success.

const (
	actionCardLimit   = 100
	mismatchCardLimit = 5
	diagPlaceLimit    = 25
)

// actionRow is one labelled line of a card. Inline marks comparison text whose
// backtick pairs format code.
type actionRow struct {
	Label   string
	Text    string
	Inline  bool
	Codes   []string
	Sources []sourceCitation
}

// actionCard is a finding to act on.
type actionCard struct {
	Anchor, FindingID, Title     string
	State, ValidationCode        string
	Severity, SeverityCode, Tone string
	Location, MoreLocations      string
	Before, After                []actionRow
	Preview                      *sourcePreview
	Source                       sourceCitation
	EvidenceLink, EvidenceLabel  string
}

// mismatchCard is a document promise the implementation does not (fully) keep.
type mismatchCard struct {
	Anchor, Title, State, Status, Tone, Interpretation string
	Rows                                               []actionRow
	Link, LinkLabel                                    string
}

// candidateRow is one line of the collapsed candidate lists.
type candidateRow struct {
	Title, State, Tone, Severity, Location string
	Next                                   *actionRow
	Link, LinkLabel                        string
}

type candidateFold struct {
	ID, Label, Note string
	Rows            []candidateRow
}

// diagGroup is the diagnostics of one rule of one analyzer.
type diagGroup struct {
	Analyzer, Rule, Count, Severity, Description string
	Places                                       []string
	More                                         string
}

type diagFold struct {
	ID, Label, Note, Unplaced string
	Groups                    []diagGroup
}

// actionBlock is the opening shown before the status tables.
type actionBlock struct {
	CoverageLead, CoverageLine, CoverageLabel string

	Heading, Lead string
	Paras         []string
	Cards         []actionCard

	MismatchHeading, MismatchLead string
	Mismatches, MismatchMore      []mismatchCard
	MismatchMoreLabel             string

	NoticesHeading string
	Notices        []secItem

	Candidates, Secondary candidateFold
	Diagnostics           diagFold
}

// Empty reports an opening with nothing to show; flat layouts never have one.
func (a actionBlock) Empty() bool { return a.Heading == "" }

// actionableFindings splits the findings with a supported or reproduced
// validation and no hiding disposition into those on primary paths and those
// on secondary paths, most severe first. A model sibling of a tool finding
// counts once. The overview's code count is the length of the first list.
func (v secView) actionableFindings() (primary, secondary []Finding) {
	var actionable []Finding
	for _, finding := range v.R.Findings {
		if hiddenDisposition[finding.Disposition] || finding.Validation != ValidationSupported && finding.Validation != ValidationReproduced {
			continue
		}
		actionable = append(actionable, finding)
	}
	duplicates := duplicateReadingFindings(actionable, v.R.Observations)
	for _, finding := range actionable {
		switch {
		case duplicates[finding.ID]:
		case findingSecondary(finding):
			secondary = append(secondary, finding)
		default:
			primary = append(primary, finding)
		}
	}
	order := func(a, b Finding) int {
		if c := cmp.Compare(sevRank(b.Severity), sevRank(a.Severity)); c != 0 {
			return c
		}
		return cmp.Compare(reproducedRank(b), reproducedRank(a))
	}
	slices.SortStableFunc(primary, order)
	slices.SortStableFunc(secondary, order)
	return primary, secondary
}

func reproducedRank(f Finding) int {
	if f.Validation == ValidationReproduced {
		return 1
	}
	return 0
}

// pendingCandidates are the model candidates that still need checking, split
// into those on primary paths and those on secondary paths (examples and
// tests), each most severe first.
func (v secView) pendingCandidates() (primary, secondary []Finding) {
	review := v.R.CodeReview
	if review == nil {
		return nil, nil
	}
	var pending []Finding
	for _, f := range v.R.Findings {
		if !slices.Contains(review.FindingIDs, f.ID) || hiddenDisposition[f.Disposition] ||
			f.Validation != ValidationInconclusive && f.Validation != ValidationUnreviewed {
			continue
		}
		pending = append(pending, f)
	}
	duplicates := duplicateReadingFindings(pending, v.R.Observations)
	for _, f := range pending {
		switch {
		case duplicates[f.ID]:
		case findingSecondary(f):
			secondary = append(secondary, f)
		default:
			primary = append(primary, f)
		}
	}
	order := func(a, b Finding) int { return cmp.Compare(sevRank(b.Severity), sevRank(a.Severity)) }
	slices.SortStableFunc(primary, order)
	slices.SortStableFunc(secondary, order)
	return primary, secondary
}

// staticSeverities maps each observation of a tool finding to the finding's
// severity.
func staticSeverities(r Report, findings map[string]Finding) map[string]string {
	sevOf := map[string]string{}
	for _, id := range StaticFindingIDs(r) {
		if f, ok := findings[id]; ok {
			for _, o := range f.ObservationIDs {
				if sevRank(f.Severity) > sevRank(sevOf[o]) {
					sevOf[o] = f.Severity
				}
			}
		}
	}
	return sevOf
}

// coverageAttention are the overview's attention codes that say something
// was not analysed. They go to the coverage line, not to the notices.
var coverageAttention = map[string]bool{
	"required_tools_not_succeeded": true, "analysis_gaps": true, "review_scope_not_sent": true,
	"review_primary_not_sent": true, "model_review_not_performed": true,
}

// noticeAttention are findings of other sections the opening points at.
var noticeAttention = map[string]bool{"secrets_detected": true, "environment_mismatch": true, "document_conflicts": true}

// buildActions reads the opening from the stored report.
func (v secView) buildActions() actionBlock {
	l, r := v.L, v.R
	a := actionBlock{Heading: l.Label("actions.heading")}
	findingByID := make(map[string]Finding, len(r.Findings))
	for _, f := range r.Findings {
		findingByID[f.ID] = f
	}
	anchors := v.anchorSet()
	ai := r.Coverage.AIReview
	assessed := ai.Status == AIReviewPerformed || ai.Status == AIReviewPartial
	if r.Status == StatusFailed || r.Status == StatusCancelled {
		assessed = false
	}

	primary, secondaryActions := v.actionableFindings()
	for i, f := range primary {
		if i == actionCardLimit {
			break
		}
		a.Cards = append(a.Cards, v.actionCard(f, anchors))
	}
	mismatches := v.mismatchCards(anchors)
	pending, secondaryPending := v.pendingCandidates()
	secondary := append(secondaryActions, secondaryPending...)
	slices.SortStableFunc(secondary, func(a, b Finding) int { return cmp.Compare(sevRank(b.Severity), sevRank(a.Severity)) })

	switch {
	case len(a.Cards) > 0:
		a.Lead = tf(l, "actions.lead", len(primary))
		if ai.Status == AIReviewPartial {
			a.Paras = append(a.Paras, l.Label("actions.lead_partial"))
		}
	case !assessed:
		a.Lead = l.Label("actions.not_assessed")
		if text := l.Label("opening.model." + ai.Status); ai.Status != AIReviewPerformed && text != "opening.model."+ai.Status {
			a.Paras = append(a.Paras, text)
		}
		if reason := noDecisionReason(l, ai); reason != "" && ai.Status != "not_requested" {
			a.Paras = append(a.Paras, reason)
		}
	default:
		// The existing wording for a review that accepted results and found
		// nothing to act on, with what was reviewed.
		if r.CodeReview != nil && hasCodeReviewEvidence(r.CodeReview) {
			a.Lead = l.Label("review.reading_none")
			if line := reviewScopeLine(l, r.CodeReview); line != "" {
				a.Paras = append(a.Paras, line)
			}
			if ai.Status == AIReviewPartial {
				a.Paras = append(a.Paras, l.Label("actions.none_partial"))
			}
			a.Paras = append(a.Paras, l.Label("actions.none_not_a_proof"))
		} else {
			a.Lead = l.Label("review.reading_no_evidence")
			a.Paras = append(a.Paras, l.Label("actions.not_assessed"))
		}
	}
	if len(a.Cards) == 0 {
		if n := len(pending); n > 0 {
			a.Paras = append(a.Paras, tf(l, "actions.others_candidates", n))
		}
		if n := len(secondary); n > 0 {
			a.Paras = append(a.Paras, tf(l, "actions.others_secondary", n))
		}
		if n := len(mismatches); n > 0 {
			a.Paras = append(a.Paras, tf(l, "actions.others_mismatch", n))
		}
	}
	if !r.Export.IncludeCode {
		a.Paras = append(a.Paras, l.Label("actions.lead_code_free"))
	}

	if len(mismatches) > 0 {
		a.MismatchHeading = l.Label("actions.mismatch_heading")
		a.MismatchLead = tf(l, "actions.mismatch_lead", len(mismatches))
		a.Mismatches = mismatches
		if len(mismatches) > mismatchCardLimit {
			a.Mismatches, a.MismatchMore = mismatches[:mismatchCardLimit], mismatches[mismatchCardLimit:]
			a.MismatchMoreLabel = tf(l, "actions.mismatch_more", len(a.MismatchMore))
		}
	}

	if r.Overview != nil {
		var coverage []string
		for _, x := range r.Overview.Attention {
			switch {
			case coverageAttention[x.Code]:
				coverage = append(coverage, attentionText(l, x.Code, x.Count))
			case noticeAttention[x.Code]:
				a.Notices = append(a.Notices, secItem{Text: attentionText(l, x.Code, x.Count), Link: x.Section})
			}
		}
		if text := l.Label("opening.model." + ai.Status); ai.Status != AIReviewPerformed && !slices.ContainsFunc(r.Overview.Attention, func(x Attention) bool {
			return x.Code == "model_review_not_performed"
		}) && text != "opening.model."+ai.Status {
			coverage = append(coverage, text)
		}
		if len(coverage) > 0 {
			a.CoverageLead = l.Label("actions.coverage_lead")
			a.CoverageLine = strings.Join(coverage, " ")
			a.CoverageLabel = l.Label("actions.coverage_link")
		}
	}
	if len(a.Notices) > 0 {
		a.NoticesHeading = l.Label("actions.notices_heading")
		// The anchors are section ids; drop a link to a section that is absent.
		for i := range a.Notices {
			if !anchors[a.Notices[i].Link] {
				a.Notices[i].Link = ""
			}
		}
	}

	a.Candidates = v.candidateFold("candidates", "actions.candidates", "actions.candidates_note", pending, anchors)
	a.Secondary = v.candidateFold("secondary-findings", "actions.secondary", "actions.secondary_note", secondary, anchors)
	a.Diagnostics = v.diagnosticFold(findingByID)
	return a
}

// actionCard shows one finding to act on: what happens and when, the impact,
// the stored code, how to fix and check it, and where the evidence is.
func (v secView) actionCard(f Finding, anchors map[string]bool) actionCard {
	l := v.L
	c := actionCard{
		Anchor: "action-" + f.ID, FindingID: f.ID, Title: openingText(f.Title, f.ID),
		State: lab(l, "validation", f.Validation), ValidationCode: f.Validation,
		Severity: tf(l, "actions.severity", lab(l, "severity", f.Severity)), SeverityCode: f.Severity, Tone: "ready",
	}
	if len(f.Locations) > 0 {
		loc := f.Locations[0]
		c.Location = sourceRange(loc.Path, loc.StartLine, loc.EndLine)
		if n := len(f.Locations) - 1; n > 0 {
			c.MoreLocations = tf(l, "summary.more_places", n)
		}
	}
	// The first code evidence that supports the finding, with its stored lines.
	for _, e := range f.Evidence {
		if e.Kind != "code" || e.Location == nil || slices.Contains(f.CounterevidenceIDs, e.ID) {
			continue
		}
		c.Source = v.citeEvidence(e.Kind, e.Location.Path, e.Location.StartLine, e.Location.EndLine, e.Excerpt)
		if v.R.Export.IncludeCode {
			c.Preview = c.Source.previewFor(f.Title)
		}
		if c.Location == "" {
			c.Location = c.Source.Location
		}
		break
	}
	if !v.R.Export.IncludeCode {
		// The excerpt text is not part of a code-free report.
		c.Source.Text = ""
	}
	add := func(target *[]actionRow, label, text string) {
		if text = openingText(text, ""); text != "" {
			*target = append(*target, actionRow{Label: l.Label(label), Text: text})
		}
	}
	for _, x := range f.Conditions {
		add(&c.Before, "opening.condition."+x.Status, x.Statement)
	}
	add(&c.Before, "opening.impact", f.Impact)
	add(&c.After, "opening.recommendation", f.Recommendation.Summary)
	for _, x := range f.Verification {
		add(&c.After, "opening.verification", x)
	}
	add(&c.After, "decision.finding_limitation", findingLimitation(f))
	counter := len(f.CounterevidenceIDs)
	if anchors[f.ID] {
		c.EvidenceLink = f.ID
		c.EvidenceLabel = tf(l, "actions.evidence_link", max(0, len(f.Evidence)-counter), counter)
	}
	return c
}

// mismatchCards are the document comparisons that did not match, mismatches
// first. Their text is a model reading of the documents, so every card says so.
func (v secView) mismatchCards(anchors map[string]bool) []mismatchCard {
	doc := v.R.Documents
	if doc == nil {
		return nil
	}
	l := v.L
	var cards []mismatchCard
	for _, status := range []string{"mismatch", "partial"} {
		for i, c := range doc.Comparisons {
			if c.Status != status {
				continue
			}
			card := mismatchCard{
				Anchor: "mismatch-" + strconv.Itoa(i+1), Title: openingText(c.Statement, tf(l, "docs.withheld_behavior", i+1)),
				State: l.Label("decision.comparison." + status), Status: status, Tone: "attention",
				Interpretation: l.Label("insight.interpretation"),
			}
			if status == "partial" {
				card.Tone = "partial"
			}
			if text := openingText(c.Reason, ""); text != "" {
				card.Rows = append(card.Rows, actionRow{Label: l.Label("actions.observed"), Text: text, Inline: true})
			}
			if scope := displayDocumentScope(c.AppliesTo); scope != "" {
				card.Rows = append(card.Rows, actionRow{Label: l.Label("col.applies_to"), Codes: []string{scope}})
			}
			if sources := v.sourceCitations(c.DocumentEvidenceIDs, doc.Evidence); len(sources) > 0 {
				card.Rows = append(card.Rows, actionRow{Label: l.Label("actions.document_location"), Sources: sources})
			}
			if sources := v.sourceCitations(c.CodeEvidenceIDs, doc.Evidence); len(sources) > 0 {
				card.Rows = append(card.Rows, actionRow{Label: l.Label("actions.code_location"), Sources: sources})
			}
			if anchor := comparisonAnchor(i); anchors[anchor] {
				card.Link, card.LinkLabel = anchor, l.Label("actions.open_comparison")
			}
			cards = append(cards, card)
		}
	}
	return cards
}

func (v secView) candidateFold(id, label, note string, findings []Finding, anchors map[string]bool) candidateFold {
	if len(findings) == 0 {
		return candidateFold{}
	}
	l := v.L
	fold := candidateFold{ID: id, Label: tf(l, label, len(findings)), Note: l.Label(note)}
	for _, f := range findings {
		row := candidateRow{
			Title: openingText(f.Title, f.ID), State: lab(l, "validation", f.Validation), Tone: "attention",
			Severity: tf(l, "actions.severity", lab(l, "severity", f.Severity)),
		}
		if f.Validation == ValidationSupported || f.Validation == ValidationReproduced {
			row.Tone = "ready"
		}
		if len(f.Locations) > 0 {
			loc := f.Locations[0]
			row.Location = sourceRange(loc.Path, loc.StartLine, loc.EndLine)
		}
		if next := readerVerification(f.Verification); next != "" {
			row.Next = &actionRow{Label: l.Label("opening.verification"), Text: next}
		}
		if anchors[f.ID] {
			row.Link, row.LinkLabel = f.ID, l.Label("actions.open_finding")
		}
		fold.Rows = append(fold.Rows, row)
	}
	return fold
}

// diagnosticFold groups the tool diagnostics by analyzer and rule with the
// rule's description and where it was reported.
func (v secView) diagnosticFold(findings map[string]Finding) diagFold {
	l := v.L
	obs := make(map[string]Observation, len(v.R.Observations))
	for _, o := range v.R.Observations {
		obs[o.ID] = o
	}
	sevOf := staticSeverities(v.R, findings)
	var merged []ruleGroup
	index := map[string]int{}
	for _, lang := range regularStaticLanguages(v.R) {
		for _, g := range v.staticGroups(lang, obs, sevOf) {
			key := g.Analyzer + "\x00" + g.Rule
			if at, ok := index[key]; ok {
				merged[at].Count += g.Count
				merged[at].Primary = append(merged[at].Primary, g.Primary...)
				merged[at].Secondary = append(merged[at].Secondary, g.Secondary...)
				continue
			}
			index[key] = len(merged)
			merged = append(merged, g)
		}
	}
	if len(merged) == 0 {
		return diagFold{}
	}
	total, rules, tools := 0, len(merged), map[string]bool{}
	fold := diagFold{ID: "tool-diagnostics", Note: l.Label("actions.diagnostics_note")}
	unplaced := false
	for _, g := range merged {
		total += g.Count
		tools[g.Analyzer] = true
		places := append(slices.Clone(g.Primary), g.Secondary...)
		group := diagGroup{Analyzer: g.Analyzer, Rule: g.Rule, Count: tf(l, "actions.diag_count", g.Count), Severity: g.Severity, Description: g.Message}
		for _, p := range places {
			unplaced = unplaced || p == l0(l)
		}
		if len(places) > diagPlaceLimit {
			group.More = tf(l, "actions.diag_more", len(places)-diagPlaceLimit)
			places = places[:diagPlaceLimit]
		}
		group.Places = places
		fold.Groups = append(fold.Groups, group)
	}
	fold.Label = tf(l, "actions.diagnostics", total, rules, len(tools))
	if unplaced {
		fold.Unplaced = l.Label("actions.diagnostics_unplaced")
	}
	return fold
}

// anchorSet is every id a link inside the report can reach: sections, parts,
// finding cards and rows, and the document comparison rows.
func (v secView) anchorSet() map[string]bool {
	ids := map[string]bool{"appendix": true, "coverage": true}
	var visit func([]secPart)
	visit = func(parts []secPart) {
		for _, p := range parts {
			if p.Anchor != "" {
				ids[p.Anchor] = true
			}
			for _, c := range p.Cards {
				ids[c.ID] = true
			}
			if t := p.Table; t != nil {
				for _, id := range t.RowIDs {
					ids[id] = true
				}
				for _, id := range t.ObservationIDs {
					ids[id] = true
				}
			}
			for _, f := range p.Findings {
				ids[f.ID] = true
			}
			if p.Reading != nil {
				for _, g := range p.Reading.Groups {
					ids[g.ID] = true
					for _, c := range g.Categories {
						for _, row := range c.Rows {
							ids[row.ID] = true
							for _, alias := range row.Aliases {
								ids[alias] = true
							}
						}
					}
				}
			}
			visit(p.Parts)
		}
	}
	for _, s := range v.Sections {
		ids[s.ID] = true
		visit(s.Parts)
	}
	visit(v.Appendix)
	visit(v.OverviewDetail)
	delete(ids, "")
	return ids
}

// --- Markdown ----------------------------------------------------------------

// mdSummary is the text of a <summary> line. A line that starts with
// <details> is a raw HTML block: backslash escapes would show, so the text is
// HTML-escaped and the characters that open links or code are numeric entities.
func mdSummary(s string) string {
	s = html.EscapeString(oneLine(s))
	return strings.NewReplacer("[", "&#91;", "]", "&#93;", "(", "&#40;", ")", "&#41;", "!", "&#33;", "`", "&#96;", "\\", "&#92;").Replace(s)
}

// mdCoverageLine is the sentence under the status banner.
func mdCoverageLine(p func(...string), a actionBlock) {
	if a.CoverageLine == "" {
		return
	}
	p("> **", mdText(a.CoverageLead), "** ", mdText(a.CoverageLine), " [", mdText(a.CoverageLabel), "](#coverage)")
	p()
}

func mdActionRow(p func(...string), row actionRow) {
	line := "- **" + mdText(row.Label) + "**"
	if row.Text != "" {
		if row.Inline {
			line += " " + mdInlineComparison(row.Text)
		} else {
			line += " " + mdText(row.Text)
		}
	}
	for _, c := range row.Codes {
		line += " " + mdCode(c)
	}
	p(line)
	if len(row.Sources) == 0 {
		return
	}
	p()
	mdCitations(func(s ...string) {
		for _, line := range strings.Split(strings.Join(s, ""), "\n") {
			if line == "" {
				p()
			} else {
				p("  ", line)
			}
		}
	}, row.Sources)
}

func mdCandidateFold(p func(...string), fold candidateFold) {
	if len(fold.Rows) == 0 {
		return
	}
	mdReadingAnchor(p, fold.ID)
	p("<details><summary>", mdSummary(fold.Label), "</summary>")
	p()
	p(mdText(fold.Note))
	p()
	for _, row := range fold.Rows {
		line := "- **" + mdText(row.Title) + "** · " + mdText(row.State+" · "+row.Severity)
		if row.Location != "" {
			line += " · " + mdCode(row.Location)
		}
		p(line)
		if row.Next != nil {
			p("  - **", mdText(row.Next.Label), "** ", mdText(row.Next.Text))
		}
		if row.Link != "" {
			p("  - [", mdText(row.LinkLabel), "](#", row.Link, ")")
		}
	}
	p()
	p("</details>")
	p()
}

func mdDiagnosticFold(p func(...string), fold diagFold) {
	if len(fold.Groups) == 0 {
		return
	}
	mdReadingAnchor(p, fold.ID)
	p("<details><summary>", mdSummary(fold.Label), "</summary>")
	p()
	p(mdText(fold.Note))
	p()
	if fold.Unplaced != "" {
		p(mdText(fold.Unplaced))
		p()
	}
	for _, g := range fold.Groups {
		summary := g.Analyzer + " " + g.Rule + " · " + g.Count
		if g.Severity != "" {
			summary += " · " + g.Severity
		}
		if g.Description != "" {
			summary += " — " + g.Description
		}
		p("<details><summary>", mdSummary(summary), "</summary>")
		p()
		for _, place := range g.Places {
			p("- ", mdCode(place))
		}
		if g.More != "" {
			p("- ", mdText(g.More))
		}
		p()
		p("</details>")
		p()
	}
	p("</details>")
	p()
}

// mdActionBlock prints the opening. The caller adds the status table after it.
func mdActionBlock(p func(...string), a actionBlock, l Locale) {
	if a.Empty() {
		return
	}
	mdReadingAnchor(p, "actions")
	p("### ", mdText(a.Heading))
	p()
	p(mdText(a.Lead))
	p()
	for _, text := range a.Paras {
		p(mdText(text))
		p()
	}
	for _, c := range a.Cards {
		mdReadingAnchor(p, c.Anchor)
		p("#### ", mdText(c.Title))
		p()
		p("**", mdText(c.State), "** · ", mdText(c.Severity))
		p()
		for _, row := range c.Before {
			mdActionRow(p, row)
		}
		if len(c.Before) > 0 {
			p()
		}
		if c.Source.Location != "" {
			mdCitations(p, []sourceCitation{c.Source})
		} else if c.Location != "" {
			p(mdCode(c.Location))
			p()
		}
		for _, row := range c.After {
			mdActionRow(p, row)
		}
		p()
		if c.EvidenceLink != "" {
			p("[", mdText(c.EvidenceLabel), "](#", c.EvidenceLink, ")")
			p()
		}
	}
	if a.MismatchHeading != "" {
		p("### ", mdText(a.MismatchHeading))
		p()
		p(mdText(a.MismatchLead))
		p()
		mdMismatchCards(p, a.Mismatches)
		if len(a.MismatchMore) > 0 {
			p("<details><summary>", mdSummary(a.MismatchMoreLabel), "</summary>")
			p()
			mdMismatchCards(p, a.MismatchMore)
			p("</details>")
			p()
		}
	}
	if len(a.Notices) > 0 {
		p("### ", mdText(a.NoticesHeading))
		p()
		for _, it := range a.Notices {
			if it.Link != "" {
				p("- [", mdText(it.Text), "](#", it.Link, ")")
			} else {
				p("- ", mdText(it.Text))
			}
		}
		p()
	}
	mdCandidateFold(p, a.Candidates)
	mdCandidateFold(p, a.Secondary)
	mdDiagnosticFold(p, a.Diagnostics)
}

func mdMismatchCards(p func(...string), cards []mismatchCard) {
	for _, c := range cards {
		mdReadingAnchor(p, c.Anchor)
		p("#### ", mdText(c.Title))
		p()
		p("**", mdText(c.State), "** · ", mdText(c.Interpretation))
		p()
		for _, row := range c.Rows {
			mdActionRow(p, row)
		}
		p()
		if c.Link != "" {
			p("[", mdText(c.LinkLabel), "](#", c.Link, ")")
			p()
		}
	}
}
