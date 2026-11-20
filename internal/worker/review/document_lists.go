package review

import (
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
)

var markdownItem = regexp.MustCompile(`^([-+*]|[0-9]{1,9}[.)])([\t ]+|$)`)

// markdownListRanges identifies item lines and indented continuations of
// contiguous lists. Nested items belong to their outer list. Fenced examples
// are opaque, even if they happen to contain bullets or numbered lines.
func markdownListRanges(lines []string) []documentRange {
	var ranges []documentRange
	var current documentRange
	var baseIndent, contentIndent int
	var marker byte
	var fence string
	fencedList := false
	finish := func() {
		if current.end > current.start {
			ranges = append(ranges, current)
			current = documentRange{}
		}
	}
	for i, raw := range lines {
		line := strings.TrimLeft(raw, " \t")
		indent := documentIndent(raw[:len(raw)-len(line)])
		if fence != "" {
			if fencedList {
				current.end = i + 1
			}
			if strings.HasPrefix(line, fence) && strings.Trim(line[len(fence):], fence[:1]+" \t\r") == "" {
				fence = ""
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			fence = line[:len(line)-len(strings.TrimLeft(line, line[:1]))]
			fencedList = current.end != 0 && indent >= contentIndent
			if fencedList {
				current.end = i + 1
			} else {
				finish()
			}
			continue
		}
		if m := markdownItem.FindStringSubmatch(line); m != nil && !thematicListBreak(line, m[1]) {
			kind := m[1][len(m[1])-1]
			if current.end != 0 && (indent < baseIndent || (indent == baseIndent && kind != marker)) {
				finish()
			}
			if current.end == 0 {
				if indent >= 4 {
					continue
				}
				current.start, baseIndent, marker = i, indent, kind
			}
			if indent == baseIndent {
				contentIndent = indent + len(m[1]) + max(1, documentIndent(m[2]))
			}
			current.end = i + 1
			continue
		}
		if current.end != 0 && indent >= contentIndent {
			current.end = i + 1
		} else {
			finish()
		}
	}
	finish()
	return ranges
}

func documentIndent(spaces string) int {
	n := 0
	for _, r := range spaces {
		if r == '\t' {
			n += 4 - n%4
		} else {
			n++
		}
	}
	return n
}

func thematicListBreak(line, marker string) bool {
	return (marker == "-" || marker == "*") && strings.Count(line, marker) >= 3 && strings.Trim(line, marker+" \t\r") == ""
}

// incompleteDocumentList checks a document window against the final request,
// where adjacent windows can supply the missing part of the same list.
func incompleteDocumentList(x record.Excerpt, sent []record.Excerpt, lists []documentRange) bool {
	if x.DirectivePath != "" {
		return false
	}
	for _, list := range lists {
		if x.End <= list.start || x.Start-1 >= list.end || (x.Start-1 <= list.start && x.End >= list.end) {
			continue
		}
		covered := 0
		for _, part := range sent {
			if part.Path == x.Path && part.DirectivePath == "" {
				covered += max(0, min(part.End, list.end)-max(part.Start-1, list.start))
			}
		}
		if covered < list.end-list.start {
			return true
		}
	}
	return false
}
