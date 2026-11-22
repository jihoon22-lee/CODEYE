package testexec

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/testexec/resultdoc"
)

func jestResults(body []byte) (out testResults) {
	var report struct {
		TestResults []struct {
			Name             string
			AssertionResults []struct {
				Status, FullName, Title string
				Location                struct{ Line int }
			}
		}
	}
	if json.Unmarshal(body, &report) != nil {
		return out
	}
	out.confirmed = report.TestResults != nil
	for _, suite := range report.TestResults {
		for _, test := range suite.AssertionResults {
			status := mapStatus(test.Status)
			if status == "" {
				return testResults{}
			}
			name := test.FullName
			if name == "" {
				name = test.Title
			}
			out.add(resultdoc.Case{Name: name, Status: status, Path: suite.Name, Line: test.Location.Line})
		}
	}
	return out
}

type playwrightSuite struct {
	Suites []playwrightSuite
	Specs  []struct {
		Title, File string
		Line        int
		Tests       []struct {
			ProjectName, Status string
			Results             []struct{ Status string }
		}
	}
}

func playwrightResults(body []byte) (out testResults) {
	var report struct{ Suites []playwrightSuite }
	if json.Unmarshal(body, &report) != nil {
		return out
	}
	out.confirmed = report.Suites != nil
	var walk func([]playwrightSuite, int)
	walk = func(suites []playwrightSuite, level int) {
		if level > 32 {
			return
		}
		for _, suite := range suites {
			for _, spec := range suite.Specs {
				for _, test := range spec.Tests {
					if len(test.Results) != 1 {
						continue
					}
					status := mapStatus(test.Results[0].Status)
					if test.Results[0].Status == "timedOut" || test.Status == "unexpected" {
						status = resultdoc.Failed
					}
					if status == "" {
						continue
					}
					name := spec.Title
					if test.ProjectName != "" {
						name = test.ProjectName + ": " + name
					}
					out.add(resultdoc.Case{Name: name, Status: status, Path: spec.File, Line: spec.Line})
				}
			}
			walk(suite.Suites, level+1)
		}
	}
	walk(report.Suites, 0)
	return out
}

var (
	tapStatistic = regexp.MustCompile(`(?m)^# (tests|pass|fail|cancelled|skipped|todo) ([0-9]+)\r?$`)
	tapCase      = regexp.MustCompile(`(?m)^ *(ok|not ok) [1-9][0-9]* - ([^\r\n]{1,512})\r?$`)
)

func nodeResults(stdout []byte) (out testResults) {
	if !bytes.Contains(stdout, []byte("\nTAP version 13\n")) && !bytes.HasPrefix(stdout, []byte("TAP version 13\n")) {
		return out
	}
	stats := map[string]int{}
	for _, m := range tapStatistic.FindAllSubmatch(stdout, 7) {
		name := string(m[1])
		n, err := strconv.Atoi(string(m[2]))
		if _, exists := stats[name]; exists || err != nil || n > resultdoc.MaxCount {
			return testResults{}
		}
		stats[name] = n
	}
	if len(stats) != 6 || stats["tests"] != stats["pass"]+stats["fail"]+stats["cancelled"]+stats["skipped"]+stats["todo"] {
		return out
	}
	out.counts = resultdoc.Counts{Passed: stats["pass"], Failed: stats["fail"] + stats["cancelled"], Skipped: stats["skipped"] + stats["todo"]}
	out.confirmed = true
	var examples testResults
	for _, m := range tapCase.FindAllSubmatch(stdout, resultdoc.MaxCases) {
		name := string(m[2])
		status := resultdoc.Passed
		if string(m[1]) == "not ok" {
			status = resultdoc.Failed
		}
		for _, marker := range []string{" # SKIP", " # TODO"} {
			if cut, _, ok := strings.Cut(name, marker); ok {
				name, status = cut, resultdoc.Skipped
			}
		}
		examples.add(resultdoc.Case{Name: name, Status: status})
	}
	if examples.counts.Passed <= out.counts.Passed && examples.counts.Failed <= out.counts.Failed && examples.counts.Skipped <= out.counts.Skipped {
		out.cases = examples.cases
	}
	return out
}
