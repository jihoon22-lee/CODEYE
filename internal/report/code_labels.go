package report

import (
	_ "embed"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// codeLabelsJSON is the one catalog of reason, limitation and coverage
// counter texts. The web report imports the same file
// (web/src/locales/codeLabels.ts), so a code reads the same in the rendered
// HTML, the Markdown and the web screen (docs/DECISIONS.md A72).
//
//go:embed reportui/code_labels_ko.json
var codeLabelsJSON []byte

type codeLabels struct {
	// Reasons are task reason and limitation codes.
	Reasons map[string]string `json:"reasons"`
	// ModelReasons are role-level codes whose words (result_missing,
	// not_succeeded) would be ambiguous among the analyzer reasons; they are
	// tried after Reasons.
	ModelReasons map[string]string `json:"model_reasons"`
	// Upstream words the codes upstream_<role>_<state>, which name the role
	// of an earlier review stage and how it ended; {role} stands for the role.
	Upstream map[string]string `json:"upstream"`
	// Units are the keys of a tool row's coverage counters. A key with no unit
	// text falls back to its reason text.
	Units map[string]string `json:"units"`
}

var koreanCodeLabels = func() codeLabels {
	var c codeLabels
	if err := json.Unmarshal(codeLabelsJSON, &c); err != nil {
		panic(err)
	}
	return c
}()

// CodeLabel is the text of a code ("code" or "code:detail"). ok is false for a
// code with no text, which is then shown as is.
func (l Locale) CodeLabel(c string) (string, bool) {
	base, detail, _ := strings.Cut(c, ":")
	t, ok := l.Codes[base]
	if !ok {
		t, ok = l.ModelReasons[base]
	}
	if !ok {
		t, ok = l.upstreamText(base)
	}
	if !ok {
		return c, false
	}
	if detail != "" {
		return t + " (" + detail + ")", true
	}
	return t, true
}

// upstreamText words upstream_<role>_<state>, built from parts and so not a
// catalog key of its own.
func (l Locale) upstreamText(code string) (string, bool) {
	rest, ok := strings.CutPrefix(code, "upstream_")
	if !ok {
		return "", false
	}
	for state, text := range l.Upstream {
		if role, ok := strings.CutSuffix(rest, "_"+state); ok && role != "" {
			return strings.ReplaceAll(text, "{role}", role), true
		}
	}
	return "", false
}

// Unit is the text of a coverage counter key; unknown keys are shown as is.
func (l Locale) Unit(key string) string {
	if t, ok := l.Units[key]; ok {
		return t
	}
	if t, ok := l.Codes[key]; ok {
		return t
	}
	return key
}

// unitCounts lists a tool row's coverage counters as "text: n", ordered by
// key.
func (l Locale) unitCounts(units map[string]int) []string {
	keys := make([]string, 0, len(units))
	for k := range units {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, l.Unit(k)+": "+strconv.Itoa(units[k]))
	}
	return out
}
