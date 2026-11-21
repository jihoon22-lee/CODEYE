package review

import (
	"path"
	"slices"
	"sort"
	"strings"
	"unicode"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

type documentHeading struct {
	line, level, weight int
	skip                bool
	member              bool
	workflow            bool
}

type documentRange struct{ start, end int }

type userDocument struct {
	entry source.Entry
	lines []string
	after int
}

type documentCoverage struct {
	doc  userDocument
	gaps []documentRange
}

// fillDocumentCoverage spends spare slots on the earliest unread text of a
// file already read for the document role. Each document gets a turn before
// the next round. Appending preserves preceding excerpts during request fitting.
func (s *roleStage) fillDocumentCoverage(docs []userDocument, primary string, out []record.Excerpt, remain, limit int) []record.Excerpt {
	if len(out) >= limit || remain <= 2 {
		return out
	}
	covered := map[string][]documentRange{}
	for _, x := range out {
		if x.DirectivePath == "" {
			covered[x.Path] = append(covered[x.Path], documentRange{x.Start - 1, x.End})
		}
	}
	active := make([]documentCoverage, 0, len(docs))
	for _, doc := range docs {
		p := doc.entry.Path
		coverage := documentCoverage{doc: doc, gaps: uncoveredDocumentRanges(doc.lines, path.Ext(p), covered[p])}
		if p == primary {
			active = slices.Insert(active, 0, coverage)
		} else {
			active = append(active, coverage)
		}
	}
	for len(active) > 0 {
		next := active[:0]
		for _, coverage := range active {
			x, ok := coverage.next(s, min(roleMaxDocBytes, remain))
			if !ok {
				continue
			}
			out = append(out, x)
			next = append(next, coverage)
			remain -= record.JSONLen(x.Text)
			if len(out) >= limit || remain <= 2 {
				return out
			}
		}
		active = next
	}
	return out
}

func (c *documentCoverage) next(s *roleStage, budget int) (record.Excerpt, bool) {
	for len(c.gaps) > 0 {
		gap := &c.gaps[0]
		if gap.start >= gap.end {
			c.gaps = c.gaps[1:]
			continue
		}
		start := gap.start
		end := min(gap.end, start+roleMaxDocLines)
		lines := c.doc.lines[start:end]
		first := slices.IndexFunc(lines, substantiveDocumentLine)
		if first < 0 {
			gap.start = end
			continue
		}
		x, ok := s.boundedExcerpt(c.doc.entry, "document", "", start+1, lines, budget)
		if !ok || x.End <= start+first {
			gap.start += max(1, first)
			continue
		}
		gap.start = x.End
		return x, true
	}
	return record.Excerpt{}, false
}

func uncoveredDocumentRanges(lines []string, ext string, covered []documentRange) []documentRange {
	return documentGaps(len(lines), documentationHeadings(lines, ext), covered)
}

func documentGaps(lineCount int, headings []documentHeading, covered []documentRange) []documentRange {
	skipThrough := 0
	for i, heading := range headings {
		if !heading.skip || heading.line < skipThrough {
			continue
		}
		skipThrough = lineCount
		for _, next := range headings[i+1:] {
			if next.level <= heading.level {
				skipThrough = next.line
				break
			}
		}
		covered = append(covered, documentRange{heading.line, skipThrough})
	}
	sort.Slice(covered, func(i, j int) bool { return covered[i].start < covered[j].start })
	var gaps []documentRange
	start := 0
	for _, r := range covered {
		if start < r.start {
			gaps = append(gaps, documentRange{start, r.start})
		}
		start = max(start, r.end)
	}
	if start < lineCount {
		gaps = append(gaps, documentRange{start, lineCount})
	}
	return gaps
}

func substantiveDocumentLine(raw string) bool {
	line := strings.TrimLeft(strings.TrimSpace(raw), "-* >")
	switch {
	case strings.HasPrefix(line, "#"), strings.HasPrefix(line, "```"), strings.HasPrefix(line, "~~~"), strings.HasPrefix(line, "<!--"):
		return false
	case strings.HasPrefix(line, "!["), strings.HasPrefix(line, "[") && strings.HasSuffix(line, ")"), strings.HasPrefix(line, "https://"), strings.HasPrefix(line, "http://"):
		return false
	}
	return strings.ContainsFunc(line, unicode.IsLetter)
}

// documentationRanges partitions an already bounded continuation when it
// contains several API entries. It keeps the whole range in order and
// prefers headings with concrete behavior text when only a few cuts fit.
func documentationRanges(lines []string, ext string, start, end, slots int) []documentRange {
	ranges := []documentRange{{start, end}}
	if slots <= 1 {
		return ranges
	}
	var entries []int
	for _, heading := range documentationHeadings(lines[start:end], ext) {
		if heading.member && !heading.skip {
			entries = append(entries, start+heading.line)
		}
	}
	if len(entries) < 2 {
		return ranges
	}
	type cut struct{ line, weight int }
	cuts := make([]cut, 0, len(entries)-1)
	for i := 1; i < len(entries); i++ {
		stop := end
		if i+1 < len(entries) {
			stop = entries[i+1]
		}
		c := cut{line: entries[i]}
		for _, raw := range lines[c.line+1 : stop] {
			if strings.TrimSpace(raw) != "" {
				c.weight += 1 + documentationLineWeight(raw)
			}
		}
		if c.weight > 0 {
			cuts = append(cuts, c)
		}
	}
	if len(cuts) >= slots {
		sort.SliceStable(cuts, func(i, j int) bool { return cuts[i].weight > cuts[j].weight })
		cuts = cuts[:slots-1]
		sort.Slice(cuts, func(i, j int) bool { return cuts[i].line < cuts[j].line })
	}
	for _, c := range cuts {
		ranges[len(ranges)-1].end = c.line
		ranges = append(ranges, documentRange{c.line, end})
	}
	return ranges
}

// documentationContinuation selects one contiguous excerpt beyond the
// leading window. Headings bound the search and the score favors concrete
// behavior and examples under user-facing sections.
func documentationContinuation(lines []string, prefixEnd int, ext string, budget int) (start, end int) {
	return bestDocumentationSection(lines, prefixEnd, ext, budget, false)
}

// representativeDocumentSection uses exact source lines from a project
// README. It can start at a heading or a paragraph within an uncovered
// section. Commands in code fences do not count as product behavior.
func representativeDocumentSection(lines []string, ext string, budget int, covered []documentRange) (start, end int) {
	headings := productDocumentHeadings(lines, ext)
	byLine := make(map[int]documentHeading, len(headings))
	weights := make([]int, len(lines))
	prose := make([]bool, len(lines))
	for _, heading := range headings {
		byLine[heading.line] = heading
	}
	var fence string
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		switch {
		case fence != "":
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
		case strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~"):
			fence = line[:3]
		case substantiveDocumentLine(line) && !strings.HasPrefix(line, "|"):
			prose[i] = true
			weights[i] = documentBehaviorWeight(line)
		}
	}
	start, best := -1, 0
	for _, gap := range documentGaps(len(lines), headings, slices.Clone(covered)) {
		for at := gap.start; at < gap.end; at++ {
			heading, titled := byLine[at]
			paragraph := prose[at] && (at == gap.start || strings.TrimSpace(lines[at-1]) == "" || byLine[at-1].level > 0)
			if !titled && !paragraph {
				continue
			}
			score, used, through := 0, 0, at
			for line := at; line < min(at+roleMaxDocLines, gap.end); line++ {
				if next, ok := byLine[line]; line != at && ok && (next.level <= heading.level || !titled) {
					break
				}
				used += record.JSONLen(lines[line])
				if used > budget {
					break
				}
				score += weights[line]
				through = line + 1
			}
			// A heading whose text lies beyond the limit adds no evidence.
			for through > at+1 {
				if _, heading := byLine[through-1]; !heading && strings.TrimSpace(lines[through-1]) != "" {
					break
				}
				through--
			}
			if score >= 3 && score+heading.weight > best {
				start, end, best = at, through, score+heading.weight
			}
		}
	}
	return start, end
}

func productDocumentHeadings(lines []string, ext string) []documentHeading {
	headings := documentationHeadings(lines, ext)
	for i, heading := range headings {
		title := strings.ToLower(strings.Trim(lines[heading.line], "# \t\r"))
		switch title {
		case "building", "build instructions", "development environment", "tech stack", "technology stack", "개발 환경", "기술 스택", "문서":
			headings[i].skip = true
		}
	}
	return headings
}

const (
	compactDocSections = 2
	compactDocLines    = 16
	compactDocBytes    = 800
)

// compactDocumentCoverage takes short, independent sections from the least
// represented portions of a guide. It only spends slots still available
// before the unlinked test material.
func (s *roleStage) compactDocumentCoverage(doc userDocument, out []record.Excerpt, remain int) []record.Excerpt {
	headings := productDocumentHeadings(doc.lines, path.Ext(doc.entry.Path))
	if len(headings) < 6 {
		return out
	}
	var covered []documentRange
	for _, x := range out {
		if x.Path == doc.entry.Path && x.DirectivePath == "" {
			covered = append(covered, documentRange{x.Start - 1, x.End})
		}
	}
	for range compactDocSections {
		if len(out) >= roleMaxDocs || remain < 128 {
			break
		}
		budget := min(compactDocBytes, remain)
		best := documentRange{}
		distance := -1
		for _, gap := range documentGaps(len(doc.lines), headings, slices.Clone(covered)) {
			for i, h := range headings {
				if h.line < gap.start || h.line >= gap.end {
					continue
				}
				sectionEnd := len(doc.lines)
				if i+1 < len(headings) {
					sectionEnd = headings[i+1].line
				}
				if slices.ContainsFunc(covered, func(old documentRange) bool {
					return old.start < sectionEnd && h.line < old.end
				}) {
					continue
				}
				end := compactDocumentEnd(doc.lines, h.line, min(gap.end, sectionEnd), budget)
				if end <= h.line+1 {
					continue
				}
				separation := len(doc.lines)
				for _, old := range covered {
					separation = min(separation, max(h.line-old.end, old.start-end))
				}
				if separation > distance {
					best, distance = documentRange{h.line, end}, separation
				}
			}
		}
		if distance < 0 {
			break
		}
		window, ok := s.boundedExcerpt(doc.entry, "document", "", best.start+1, doc.lines[best.start:best.end], budget)
		if !ok || window.End != best.end {
			break
		}
		out = append(out, window)
		covered = append(covered, best)
		remain -= record.JSONLen(window.Text)
	}
	return out
}

// compactDocumentEnd retains complete paragraphs and fenced examples as far
// as they fit. A heading, half of a paragraph, or open code fence is not a
// representative section by itself.
func compactDocumentEnd(lines []string, start, end, budget int) int {
	used, bodyEnd := record.JSONLen(lines[start]), start
	var fence string
	hasBody := false
	listItems := 0
	for i := start + 1; i < min(end, start+compactDocLines); i++ {
		raw := strings.TrimSpace(lines[i])
		used += record.JSONLen(lines[i])
		if used > budget {
			break
		}
		switch {
		case fence != "":
			if strings.HasPrefix(raw, fence) {
				fence, bodyEnd, hasBody = "", i+1, true
			}
		case strings.HasPrefix(raw, "```") || strings.HasPrefix(raw, "~~~"):
			fence = raw[:3]
		case raw == "" && hasBody:
			bodyEnd = i
		default:
			if substantiveDocumentLine(raw) {
				if strings.HasPrefix(raw, "- ") || strings.HasPrefix(raw, "* ") {
					listItems++
					hasBody = hasBody || listItems > 1 || strings.HasSuffix(raw, ".")
				} else {
					hasBody = true
				}
			}
			if hasBody && i+1 == end {
				bodyEnd = end
			}
		}
	}
	return bodyEnd
}

func documentBehaviorWeight(line string) int {
	score := documentationLineWeight(line)
	for _, word := range witnessTerms(line) {
		switch word {
		case "supports", "supported", "unsupported", "cannot", "unless", "only", "must", "never", "except",
			"calculates", "calculated", "counts", "rejects", "retains", "preserves", "excludes", "limits",
			"경우", "제외", "제한", "허용", "보장", "합산", "계산", "유지", "아니다", "않는다":
			return score + 3
		}
	}
	return score
}

func bestDocumentationSection(lines []string, prefixEnd int, ext string, budget int, workflowOnly bool) (start, end int) {
	headings := documentationHeadings(lines, ext)
	start = -1
	best := 0
	var context []documentHeading
	for i, heading := range headings {
		for len(context) != 0 && context[len(context)-1].level >= heading.level {
			context = context[:len(context)-1]
		}
		if len(context) != 0 {
			parent := context[len(context)-1]
			heading.skip = heading.skip || parent.skip
			heading.weight = max(heading.weight, parent.weight-1)
		}
		context = append(context, heading)
		if heading.line < prefixEnd || heading.skip || (workflowOnly && !heading.workflow) || (!heading.workflow && heading.weight == 0) {
			continue
		}
		limit := min(len(lines), heading.line+roleMaxDocLines)
		for _, next := range headings[i+1:] {
			if next.line >= limit {
				break
			}
			if next.level < heading.level || next.skip || (next.level == heading.level && len(context) == 1) {
				limit = next.line
				break
			}
		}
		weight, behavior, used := 0, 0, 0
		windowEnd := heading.line
		for line := heading.line; line < limit; line++ {
			raw := lines[line]
			if len(raw) > budget {
				break
			}
			used += record.JSONLen(raw)
			if used > budget {
				break
			}
			windowEnd = line + 1
			if line > heading.line {
				strength := documentationLineWeight(raw)
				if heading.workflow && substantiveDocumentLine(raw) {
					strength++
				}
				if strength > 0 {
					weight += strength
					behavior++
				}
			}
		}
		if score := heading.weight + weight; behavior >= 2 && score > best {
			start, end, best = heading.line, windowEnd, score
		}
	}
	return start, end
}

func documentationHeadings(lines []string, ext string) []documentHeading {
	var headings []documentHeading
	var fence string
	rst := strings.EqualFold(ext, ".rst")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if len(line) < 2 || len(line) > 180 {
			continue
		}
		switch {
		case fence != "":
			if strings.HasPrefix(line, fence) {
				fence = ""
			}
			continue
		case strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~"):
			fence = line[:3]
			continue
		case len(raw)-len(strings.TrimLeft(raw, " ")) >= 4 || strings.HasPrefix(raw, "\t"):
			continue
		}
		level := 0
		title := line
		for level < len(line) && line[level] == '#' {
			level++
		}
		switch {
		case level > 0:
			if level > 6 || level == len(line) || (line[level] != ' ' && line[level] != '\t') {
				continue
			}
			title = line[level:]
		case i+1 < len(lines) && !strings.ContainsAny(line[:1], "#[-+*>|@") && (i == 0 || strings.TrimSpace(lines[i-1]) == ""):
			underline := strings.TrimSpace(lines[i+1])
			if len(underline) < 3 || strings.Trim(underline, underline[:1]) != "" {
				continue
			}
			switch underline[0] {
			case '=':
				level = 1
			case '-':
				level = 2
			case '~', '^', '"':
				if rst {
					level = 3
				}
			}
		}
		if level > 0 {
			weight, skip := documentationHeadingWeight(title)
			member := !strings.Contains(title, "://") && len(documentedMembers(title, true)) > 0
			headings = append(headings, documentHeading{line: i, level: level, weight: weight, skip: skip, member: member, workflow: workflowHeading(title)})
		}
	}
	return headings
}

func workflowHeading(title string) bool {
	var previous string
	for _, word := range witnessTerms(title) {
		switch word {
		case "workflow", "workflows", "사용흐름", "작업흐름":
			return true
		}
		switch previous + " " + word {
		case "use case", "use cases", "user journey", "user journeys", "user flow", "user flows", "user roles",
			"functional requirements", "사용 사례", "사용자 흐름", "사용자 역할", "작업 흐름", "기능 요구", "기능 요구사항":
			return true
		}
		previous = word
	}
	return false
}

func documentationHeadingWeight(title string) (weight int, skip bool) {
	for _, word := range witnessTerms(title) {
		switch word {
		case "installation", "install", "sponsors", "sponsor", "funding", "donate", "donations",
			"changelog", "license", "licence", "contents", "toc", "contributing", "contributors",
			"credits", "acknowledgements", "badge", "badges", "ci", "설치", "목차", "후원", "라이선스", "변경이력":
			return 0, true
		case "api", "reference", "인터페이스":
			weight = max(weight, 10)
		case "caveats", "limitations", "constraints", "compatibility", "restrictions", "제약", "제한", "지원":
			weight = max(weight, 10)
		case "usage", "options", "arguments", "parameters", "commands", "examples", "example", "behavior", "behaviour",
			"configuration", "errors", "returns", "사용법", "옵션", "명령", "예제", "동작", "설정", "오류", "반환":
			weight = max(weight, 8)
		}
	}
	return weight, false
}

func documentationLineWeight(raw string) int {
	line := strings.TrimSpace(raw)
	if len(line) < 6 || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") || strings.HasPrefix(line, "!") ||
		strings.HasPrefix(line, "http") || strings.HasPrefix(line, "<") || strings.HasPrefix(line, "```") {
		return 0
	}
	score := 0
	if strings.Contains(line, "`") || strings.Contains(line, "(") || strings.Contains(line, "--") {
		score++
	}
	for _, word := range witnessTerms(line) {
		switch word {
		case "return", "returns", "returned", "default", "optional", "parameter", "parameters", "argument", "arguments",
			"option", "options", "result", "error", "errors", "raises", "throws", "when", "while", "if",
			"반환", "기본값", "인자", "오류", "입력", "결과", "옵션":
			return score + 2
		}
	}
	return score
}
