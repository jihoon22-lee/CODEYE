package testexec

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

const maxResultFile = 8 << 20

type testResults struct {
	counts    resultdoc.Counts
	cases     []resultdoc.Case
	confirmed bool
}

func (r *testResults) add(test resultdoc.Case) {
	switch test.Status {
	case resultdoc.Passed:
		r.counts.Passed++
	case resultdoc.Failed:
		r.counts.Failed++
	case resultdoc.Skipped:
		r.counts.Skipped++
	default:
		return
	}
	r.sample(test)
}

func (r *testResults) sample(test resultdoc.Case) {
	if !resultdoc.ValidName(test.Name) {
		return
	}
	if len(r.cases) < resultdoc.MaxCases {
		r.cases = append(r.cases, test)
	} else if test.Status == resultdoc.Failed {
		for i := len(r.cases) - 1; i >= 0; i-- {
			if r.cases[i].Status != resultdoc.Failed {
				r.cases[i] = test
				break
			}
		}
	}
}

func (r *testResults) merge(part testResults) {
	r.confirmed = r.confirmed || part.confirmed
	r.counts.Passed += part.counts.Passed
	r.counts.Failed += part.counts.Failed
	r.counts.Skipped += part.counts.Skipped
	for _, test := range part.cases {
		r.sample(test)
	}
}

func (r testResults) hasTests() bool {
	return r.counts.Total() > 0 && r.counts.Total() <= resultdoc.MaxCount
}

func resultArtifact(job analysis.Job, p testPlan, scratch, name string, res executor.Result, r testResults) (analysis.ArtifactFile, bool) {
	stdout, stderr := domain.Digest(res.Stdout.Data), domain.Digest(res.Stderr.Data)
	files := govet.IncludedFiles(job.Snapshot.Manifest)
	want := resultdoc.Expect{RunID: job.RunID, AttemptID: job.AttemptID, Snapshot: job.Snapshot.Digest, TaskKey: p.spec.Key,
		StdoutSHA256: stdout, StderrSHA256: stderr, Lookup: func(path string) bool { _, ok := files[path]; return ok }}
	doc := resultdoc.Document{SchemaVersion: resultdoc.Version, RunID: want.RunID, AttemptID: want.AttemptID, Snapshot: want.Snapshot,
		TaskKey: want.TaskKey, Runner: name, StdoutSHA256: stdout, StderrSHA256: stderr, Counts: r.counts, Cases: r.cases}
	body, err := resultdoc.Encode(doc, want)
	if err != nil {
		return analysis.ArtifactFile{}, false
	}
	file := filepath.Join(scratch, "test-results.json")
	if err := analysis.WriteFileExclusive(file, body); err != nil {
		return analysis.ArtifactFile{}, false
	}
	return analysis.ArtifactFile{Path: file, Role: "other", MediaType: resultdoc.MediaType}, true
}

type goTestEvent struct{ Action, Package, Test string }

func goResults(stdout []byte) (out testResults) {
	type events struct {
		started, ended int
		status         string
	}
	tests := map[goTestEvent]events{}
	packages := map[string]int{}
	for line := range bytes.SplitSeq(stdout, []byte{'\n'}) {
		var e goTestEvent
		if len(line) > 4<<10 || json.Unmarshal(line, &e) != nil || e.Package == "" {
			continue
		}
		status := mapStatus(e.Action)
		if e.Test == "" {
			if status != "" {
				packages[e.Package]++
			}
			continue
		}
		key := goTestEvent{Package: e.Package, Test: e.Test}
		s := tests[key]
		switch {
		case e.Action == "run":
			s.started++
		case status != "":
			s.ended++
			if s.started == 1 {
				s.status = status
			}
		}
		tests[key] = s
	}
	var keys []goTestEvent
	for k, s := range tests {
		if s.started == 1 && s.ended == 1 && s.status != "" && packages[k.Package] == 1 {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b goTestEvent) int {
		if order := strings.Compare(a.Package, b.Package); order != 0 {
			return order
		}
		return strings.Compare(a.Test, b.Test)
	})
	hasSameStatusChild := map[goTestEvent]bool{}
	for _, k := range keys {
		if parent, _, ok := cutLast(k.Test, "/"); ok {
			p := goTestEvent{Package: k.Package, Test: parent}
			// A parent with a different outcome can have its own assertion.
			if tests[p].status == tests[k].status {
				hasSameStatusChild[p] = true
			}
		}
	}
	for _, k := range keys {
		if !hasSameStatusChild[k] {
			out.add(resultdoc.Case{Name: k.Test, Status: tests[k].status})
		}
	}
	return out
}

func mapStatus(s string) string {
	switch s {
	case "pass", "passed", "ok":
		return resultdoc.Passed
	case "fail", "failed", "FAILED":
		return resultdoc.Failed
	case "skip", "skipped", "ignored", "pending", "todo", "disabled":
		return resultdoc.Skipped
	}
	return ""
}

func cutLast(s, sep string) (string, string, bool) {
	i := strings.LastIndex(s, sep)
	if i == -1 {
		return s, "", false
	}
	return s[:i], s[i+len(sep):], true
}

var (
	unittestRun  = regexp.MustCompile(`(?m)^Ran ([0-9]+) tests? in [0-9.]+s\r?$`)
	unittestEnd  = regexp.MustCompile(`(?m)^(?:OK|FAILED)(?: \(([^\r\n()]*)\))?\r?$`)
	unittestCase = regexp.MustCompile(`(?m)^([^\r\n]+?) \(([A-Za-z0-9_.$]+)\)(?:\r?\n[^\r\n]*)? \.\.\. (ok|FAIL|ERROR|skipped(?: '[^\r\n]*')?|expected failure|unexpected success)\r?$`)
	cargoSummary = regexp.MustCompile(`(?m)^test result: (?:ok|FAILED)\. ([0-9]+) passed; ([0-9]+) failed; ([0-9]+) ignored;.*$`)
	cargoCase    = regexp.MustCompile(`(?m)^test (.{1,512}) \.\.\. (ok|FAILED|ignored)(?:, [^\r\n]*)?\r?$`)
)

func unittestResults(stderr []byte) (out testResults) {
	run, end := unittestRun.FindAllSubmatch(stderr, 2), unittestEnd.FindAllSubmatch(stderr, 2)
	if len(run) != 1 || len(end) != 1 {
		return out
	}
	total, err := strconv.Atoi(string(run[0][1]))
	if err != nil || total < 1 || total > resultdoc.MaxCount {
		return out
	}
	details := strings.TrimSpace(string(end[0][1]))
	if details != "" {
		seen := map[string]bool{}
		for _, field := range strings.Split(details, ", ") {
			key, raw, ok := strings.Cut(field, "=")
			n, err := strconv.Atoi(raw)
			if !ok || err != nil || n < 0 || n > total || seen[key] {
				return testResults{}
			}
			seen[key] = true
			switch key {
			case "failures", "errors", "unexpected successes":
				out.counts.Failed += n
			case "skipped", "expected failures":
				out.counts.Skipped += n
			default:
				return testResults{}
			}
		}
	}
	out.counts.Passed = total - out.counts.Failed - out.counts.Skipped
	if out.counts.Passed < 0 || strings.HasPrefix(string(end[0][0]), "FAILED") != (out.counts.Failed > 0) {
		return testResults{}
	}
	var examples testResults
	for _, m := range unittestCase.FindAllSubmatch(stderr, resultdoc.MaxCases) {
		status := resultdoc.Failed
		switch {
		case string(m[3]) == "ok":
			status = resultdoc.Passed
		case bytes.HasPrefix(m[3], []byte("skipped")) || string(m[3]) == "expected failure":
			status = resultdoc.Skipped
		}
		examples.add(resultdoc.Case{Name: string(m[2]), Status: status})
	}
	if examples.counts.Passed <= out.counts.Passed && examples.counts.Failed <= out.counts.Failed && examples.counts.Skipped <= out.counts.Skipped {
		out.cases = examples.cases
	}
	return out
}

func cargoResults(stdout []byte) (out testResults) {
	var confirmed testResults
	for _, m := range cargoSummary.FindAllSubmatch(stdout, -1) {
		passed, e1 := strconv.Atoi(string(m[1]))
		failed, e2 := strconv.Atoi(string(m[2]))
		skipped, e3 := strconv.Atoi(string(m[3]))
		if e1 != nil || e2 != nil || e3 != nil || max(passed, failed, skipped) > resultdoc.MaxCount {
			return testResults{}
		}
		out.merge(testResults{counts: resultdoc.Counts{Passed: passed, Failed: failed, Skipped: skipped}})
	}
	for _, m := range cargoCase.FindAllSubmatch(stdout, resultdoc.MaxCases) {
		confirmed.add(resultdoc.Case{Name: string(m[1]), Status: mapStatus(string(m[2]))})
	}
	if confirmed.counts.Passed <= out.counts.Passed && confirmed.counts.Failed <= out.counts.Failed && confirmed.counts.Skipped <= out.counts.Skipped {
		out.cases = confirmed.cases
	}
	return out
}

type junitCase struct {
	Name    string    `xml:"name,attr"`
	Class   string    `xml:"classname,attr"`
	Failure *struct{} `xml:"failure"`
	Error   *struct{} `xml:"error"`
	Skipped *struct{} `xml:"skipped"`
	Status  string    `xml:"status,attr"`
	Path    string    `xml:"file,attr"`
	Line    string    `xml:"line,attr"`
}

func xmlResults(body []byte, runner string) (out testResults, valid bool) {
	if len(body) == 0 || len(body) > maxResultFile {
		return out, false
	}
	dec := xml.NewDecoder(bytes.NewReader(body))
	var stack []string
	root := false
	for {
		t, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil || len(stack) > 32 {
			return testResults{}, false
		}
		switch v := t.(type) {
		case xml.StartElement:
			name := v.Name.Local
			if len(stack) == 0 {
				if root || name != "testsuites" && name != "testsuite" {
					return testResults{}, false
				}
				root = true
			}
			if name == "testcase" && len(stack) > 0 && stack[len(stack)-1] == "testsuite" {
				var c junitCase
				if err := dec.DecodeElement(&c, &v); err != nil || strings.TrimSpace(c.Name) == "" {
					return testResults{}, false
				}
				status := resultdoc.Passed
				switch {
				case c.Failure != nil || c.Error != nil:
					status = resultdoc.Failed
				case c.Skipped != nil || c.Status == "notrun" || c.Status == "disabled" || c.Status == "skipped":
					status = resultdoc.Skipped
				}
				name := c.Name
				if c.Class != "" && c.Class != c.Name {
					name = c.Class + "." + name
				}
				test := resultdoc.Case{Name: name, Status: status}
				if runner == "pytest" {
					line, err := strconv.Atoi(c.Line)
					if c.Error != nil && (err != nil || c.Class == "") {
						continue // collection error, so no test case ran
					}
					if err == nil && line >= 0 && line < 1_000_000 {
						test.Path, test.Line = c.Path, line+1
					}
				}
				out.add(test)
				if out.counts.Total() > resultdoc.MaxCount {
					return testResults{}, false
				}
				continue
			}
			stack = append(stack, name)
		case xml.EndElement:
			stack = stack[:len(stack)-1]
		case xml.Directive:
			return testResults{}, false
		}
	}
	out.confirmed = root && len(stack) == 0
	return out, out.confirmed
}

func readTestReport(root *os.Root, relative string) ([]byte, bool) {
	fi, err := root.Lstat(relative)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() < 1 || fi.Size() > maxResultFile {
		return nil, false
	}
	f, err := root.Open(relative)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, maxResultFile+1))
	return body, err == nil && len(body) <= maxResultFile
}

func gatherResults(p testPlan, key, scratch string, res executor.Result) (name string, results testResults) {
	name = p.runner
	switch key {
	case "go":
		return "go", goResults(res.Stdout.Data)
	case "rust":
		if name == "" {
			name = "cargo"
		}
		return name, cargoResults(res.Stdout.Data)
	case "python":
		if name == "" || name == "unittest" {
			return "unittest", unittestResults(res.Stderr.Data)
		}
	}
	if name == "node-test" {
		return name, nodeResults(res.Stdout.Data)
	}
	root, err := os.OpenRoot(scratch)
	if err != nil {
		return name, results
	}
	defer root.Close()
	body, ok := readTestReport(root, "framework-report")
	if !ok {
		return name, results
	}
	switch name {
	case "pytest", "ctest", "nextest":
		results, _ = xmlResults(body, name)
	case "jest", "vitest":
		results = jestResults(body)
	case "playwright":
		results = playwrightResults(body)
	}
	return name, results
}
