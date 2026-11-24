package opengrep

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
)

// fixtureRoot holds the rule pack fixtures: per language a positive file
// per rule (each expected finding marked "expect: <rule>" on its line) and
// a "_safe" negative file that must yield nothing.
const fixtureRoot = "../../../../fixtures/taint"

// suppressedFiles are in-source suppressions of the rule pack's languages:
// each marked line holds a flow the rule finds and a nosem comment, so the
// SARIF result is reported with an inSource suppression.
var suppressedFiles = map[string]string{
	"suppressed/app.py": `import os
from flask import request


def run():
    os.system(request.args["cmd"])  # nosemgrep: codeye.python.command-injection
    os.system(request.args["cmd"])  # nosem
    os.system(request.args["cmd"])  # nosemgrep: some.other.rule
`,
}

// suppressedWant are the suppressed places of suppressedFiles; the
// unsuppressed line (another rule named) is an active finding.
var suppressedWant = []string{"suppressed/app.py:6:codeye.python.command-injection", "suppressed/app.py:7:codeye.python.command-injection"}
var suppressedActive = []string{"suppressed/app.py:8:codeye.python.command-injection"}

func fixtureFiles(t testing.TB) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(fixtureRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(fixtureRoot, p)
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		files[filepath.ToSlash(rel)] = b
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for p, s := range suppressedFiles {
		files[p] = []byte(s)
	}
	return files
}

var expectMarker = regexp.MustCompile(`expect: (codeye\.[a-z]+\.[a-z-]+)`)

// expectedPlaces are path:line:rule of every marker, sorted.
func expectedPlaces(files map[string][]byte) []string {
	var out []string
	for p, b := range files {
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range expectMarker.FindAllStringSubmatch(line, -1) {
				out = append(out, p+":"+strconv.Itoa(i+1)+":"+m[1])
			}
		}
	}
	out = append(out, suppressedActive...)
	sort.Strings(out)
	return out
}

// places splits the observations into active and suppressed path:line:rule.
func places(doc obsdoc.Document) (active, suppressed []string) {
	for _, o := range doc.Observations {
		p := "?"
		if o.Location != nil {
			p = o.Location.Path + ":" + strconv.Itoa(o.Location.StartLine) + ":" + o.RuleID
		}
		if o.Suppressed {
			suppressed = append(suppressed, p)
		} else {
			active = append(active, p)
		}
	}
	sort.Strings(active)
	sort.Strings(suppressed)
	return active, suppressed
}

// checkObservations asserts the shape every observation of the pack has:
// resolved byte locations, security category, a tool flow from a source to
// a sink with fixed step texts, and a described rule with a CWE.
func checkObservations(t *testing.T, doc obsdoc.Document) {
	t.Helper()
	rules := map[string]obsdoc.Rule{}
	for _, r := range doc.Rules {
		rules[r.ID] = r
	}
	for _, o := range doc.Observations {
		if o.LocationState != obsdoc.LocationResolved || o.Location.ColumnUnit != "byte" || o.Category != "security" || o.RawRef.JSONPointer == "" {
			t.Fatalf("observation %+v", o)
		}
		if r, ok := rules[o.RuleID]; !ok || len(r.CWE) == 0 || r.ShortDescription == "" {
			t.Fatalf("rule of %s not described: %+v", o.RuleID, r)
		}
		if len(o.Related) < 2 {
			t.Fatalf("%s:%d: no tool flow", o.Location.Path, o.Location.StartLine)
		}
		first, last := o.Related[0], o.Related[len(o.Related)-1]
		if first.Kind != obsdoc.RelatedFlowStep || last.Kind != obsdoc.RelatedFlowStep || !strings.HasSuffix(first.RawMessage, ": source") || !strings.HasSuffix(last.RawMessage, ": sink") {
			t.Fatalf("flow of %s:%d: %q .. %q", o.Location.Path, o.Location.StartLine, first.RawMessage, last.RawMessage)
		}
		if last.Location == nil || last.Location.Path != o.Location.Path {
			t.Fatalf("sink step not in the file: %+v", last)
		}
	}
}
