package review

import (
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

type documentHeading struct {
	line, level, weight int
	skip                bool
	member              bool
}

type documentRange struct{ start, end int }

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
		if heading.line < prefixEnd || heading.skip || heading.weight == 0 {
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
			headings = append(headings, documentHeading{line: i, level: level, weight: weight, skip: skip, member: member})
		}
	}
	return headings
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
