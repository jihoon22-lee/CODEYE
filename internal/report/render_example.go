package report

import (
	"path"
	"regexp"
	"slices"
	"strings"
)

type documentExample struct {
	Call, Output string
}

type exampleCandidate struct {
	documentExample
	name      string
	arguments []string
}

const (
	exampleString  = `(?:"[^"\\\x60\r\n\t]*"|'[^'\\\x60\r\n\t]*')`
	exampleNumber  = `-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?`
	exampleName    = `[A-Za-z_][A-Za-z0-9_]*`
	exampleJS      = `(?:` + exampleString + `|` + exampleNumber + `|true|false|null|undefined|NaN)`
	examplePython  = `(?:` + exampleString + `|` + exampleNumber + `|True|False|None)`
	pythonArgument = `(?:` + exampleName + ` *= *)?` + examplePython
)

var (
	exampleHeading      = regexp.MustCompile(`^#{1,6} `)
	exampleFence        = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})(.*)$")
	exampleJSCall       = regexp.MustCompile(`^((` + exampleName + `)\( *(` + exampleJS + `(?: *, *` + exampleJS + `)*) *\));?$`)
	exampleJSOutput     = regexp.MustCompile(`^//=> *(` + exampleJS + `)$`)
	examplePythonAssert = regexp.MustCompile(`^assert ((` + exampleName + `)\( *(` + pythonArgument + `(?: *, *` + pythonArgument + `)*) *\)) *== *(` + examplePython + `)$`)
	examplePythonImport = regexp.MustCompile(`^from ` + exampleName + ` import ` + exampleName + `$`)
	exampleArgument     = regexp.MustCompile(`(?:(` + exampleName + `) *= *)?(` + exampleJS + `|` + examplePython + `)`)
	exampleArgSeparator = regexp.MustCompile(`^(?:에|와|과|으로|로|,)? +$`)
	exampleConditionEnd = regexp.MustCompile(`^(?:(?:을|를) (?:적용하면|입력받으면|전달하면)|인 경우|일 때)$`)
)

// Read only complete Markdown fences in the cited window. Saved evidence is
// text and the examples are copied literally, without interpreting arguments.
func citedDocumentExample(statement string, evidence *SectionEvidence) *documentExample {
	if evidence == nil || evidence.Kind != "document" || evidence.Excerpt == nil ||
		!slices.Contains([]string{".md", ".markdown"}, strings.ToLower(path.Ext(evidence.Path))) {
		return nil
	}
	excerpt := evidence.Excerpt
	if excerpt.StartLine != evidence.StartLine || excerpt.EndLine > evidence.EndLine ||
		strings.Contains(strings.ToUpper(excerpt.Text), "[REDACTED]") {
		return nil
	}
	lines := strings.Split(excerpt.Text, "\n")
	// A window that starts later needs a visible document section boundary.
	if excerpt.StartLine > 1 && !exampleHeading.MatchString(lines[0]) {
		return nil
	}
	var candidates []exampleCandidate
	var fence, language string
	start := 0
	for i, line := range lines {
		marker := exampleFence.FindStringSubmatch(line)
		if marker == nil {
			continue
		}
		if fence == "" {
			fence, language, start = marker[1], strings.TrimSpace(marker[2]), i
			continue
		}
		if marker[1][0] == fence[0] && len(marker[1]) >= len(fence) && strings.TrimSpace(marker[2]) == "" {
			candidates = append(candidates, exampleFenceCandidates(language, lines[start+1:i])...)
			fence = ""
		}
	}
	var found *exampleCandidate
	for _, candidate := range candidates {
		if !candidate.matches(strings.TrimSpace(statement)) {
			continue
		}
		if found != nil || len(candidate.Call)+len(candidate.Output) > 120 {
			return nil
		}
		found = &candidate
	}
	if found == nil || slices.ContainsFunc(candidates, func(other exampleCandidate) bool {
		return other.name == found.name && slices.Equal(other.arguments, found.arguments) && other.Output != found.Output
	}) {
		return nil
	}
	return &found.documentExample
}

func exampleFenceCandidates(language string, body []string) []exampleCandidate {
	var candidates []exampleCandidate
	switch language {
	case "js", "javascript":
		if len(body) == 2 {
			call := exampleJSCall.FindStringSubmatch(body[0])
			output := exampleJSOutput.FindStringSubmatch(body[1])
			if call != nil && output != nil {
				candidates = append(candidates, exampleCandidate{documentExample{call[1], output[1]}, call[2], exampleArguments(call[3], false)})
			}
		}
	case "py", "python":
		for _, line := range body {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") || examplePythonImport.MatchString(line) {
				continue
			}
			// A quoted string, branch or arbitrary setup can change the
			// meaning of an otherwise literal-looking line in this block.
			assertion := examplePythonAssert.FindStringSubmatch(line)
			if assertion == nil {
				return nil
			}
			args := exampleArguments(assertion[3], false)
			if len(args) == 0 {
				return nil
			}
			candidates = append(candidates, exampleCandidate{documentExample{assertion[1], assertion[4]}, assertion[2], args})
		}
	}
	return candidates
}

// The candidate call grammar has already validated its separators. For prose,
// require every character between its arguments to belong to the condition.
func exampleArguments(text string, prose bool) []string {
	var positional, named []string
	names := map[string]bool{}
	end := 0
	for _, part := range exampleArgument.FindAllStringSubmatchIndex(text, -1) {
		if prose && (end == 0 && part[0] != 0 || end > 0 && !exampleArgSeparator.MatchString(text[end:part[0]])) {
			return nil
		}
		end = part[1]
		value := text[part[4]:part[5]]
		if part[2] < 0 {
			if !prose && len(named) > 0 {
				return nil
			}
			positional = append(positional, value)
			continue
		}
		name := text[part[2]:part[3]]
		if names[name] {
			return nil
		}
		names[name] = true
		named = append(named, name+"="+value)
	}
	if prose && !exampleConditionEnd.MatchString(text[end:]) {
		return nil
	}
	slices.Sort(named)
	return append(positional, named...)
}

func (e exampleCandidate) matches(statement string) bool {
	endings := []string{" " + e.Output + "을 반환한다.", " " + e.Output + "를 반환한다."}
	for _, call := range []string{e.Call, "`" + e.Call + "`"} {
		if statement == call+" returns "+e.Output+"." || slices.ContainsFunc(endings, func(end string) bool {
			return statement == call+"은"+end || statement == call+"는"+end
		}) {
			return true
		}
	}
	for _, name := range []string{e.name, "`" + e.name + "`"} {
		for _, subject := range []string{name + "은 ", name + "는 "} {
			condition, ok := strings.CutPrefix(statement, subject)
			if !ok {
				continue
			}
			for _, result := range endings {
				input, ok := strings.CutSuffix(condition, result)
				if ok && slices.Equal(exampleArguments(input, true), e.arguments) {
					return true
				}
			}
		}
	}
	return false
}
