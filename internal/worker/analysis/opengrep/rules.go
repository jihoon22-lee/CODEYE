package opengrep

import (
	"embed"
	"path"
	"regexp"
	"sort"
	"strings"
)

// rulesFS holds the CODEYE rule pack codeye-taint: one Opengrep rule file
// per language, written for CODEYE from the language and framework APIs.
//
//go:embed rules/*.yaml
var rulesFS embed.FS

// Language is one rule file and the snapshot files it applies to.
type Language struct {
	Name       string   // rule file base name and the rule ID segment
	Extensions []string // file extensions (lower case) given to Opengrep
	Rules      []string // rule IDs of the file, sorted
	File       []byte
}

// languageExtensions maps a rule file to the files it scans. A language
// without a rule file in the pack is not scanned.
var languageExtensions = map[string][]string{
	"python":     {".py"},
	"javascript": {".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx", ".mts", ".cts"},
	"go":         {".go"},
	"java":       {".java"},
	"c":          {".c", ".h", ".cc", ".cpp", ".cxx", ".c++", ".hh", ".hpp", ".hxx", ".h++"},
	"rust":       {".rs"},
}

var ruleIDLine = regexp.MustCompile(`(?m)^  - id: (codeye\.[a-z]+\.[a-z-]+)\s*$`)

// Languages lists the rule files of the pack in name order.
func Languages() []Language {
	entries, err := rulesFS.ReadDir("rules")
	if err != nil {
		panic(err)
	}
	var out []Language
	for _, e := range entries {
		name := strings.TrimSuffix(e.Name(), ".yaml")
		ext, ok := languageExtensions[name]
		if !ok {
			panic("opengrep: rule file without a language: " + e.Name())
		}
		b, err := rulesFS.ReadFile(path.Join("rules", e.Name()))
		if err != nil {
			panic(err)
		}
		l := Language{Name: name, Extensions: ext, File: b}
		for _, m := range ruleIDLine.FindAllStringSubmatch(string(b), -1) {
			if !strings.HasPrefix(m[1], "codeye."+name+".") {
				panic("opengrep: rule " + m[1] + " is not in its language file")
			}
			l.Rules = append(l.Rules, m[1])
		}
		sort.Strings(l.Rules)
		out = append(out, l)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Name < out[b].Name })
	return out
}

// LanguageOf returns the rule language of a snapshot path ("" when no rule
// file applies).
func LanguageOf(langs []Language, p string) string {
	ext := strings.ToLower(path.Ext(p))
	for _, l := range langs {
		for _, e := range l.Extensions {
			if e == ext {
				return l.Name
			}
		}
	}
	return ""
}

// RuleIDs lists every rule of the pack, sorted.
func RuleIDs() []string {
	var out []string
	for _, l := range Languages() {
		out = append(out, l.Rules...)
	}
	sort.Strings(out)
	return out
}
