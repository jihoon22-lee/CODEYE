// Package resultdoc binds observed test cases to one test execution.
package resultdoc

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	MediaType = "application/vnd.codeye.test-results+json"
	Version   = "0.1"
	MaxBytes  = 128 << 10
	MaxCases  = 128
	MaxCount  = 1_000_000
	Passed    = "passed"
	Failed    = "failed"
	Skipped   = "skipped"
)

var (
	invalid = errors.New("invalid test result evidence")
	digest  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	taskKey = regexp.MustCompile(`^test\.[a-z0-9-]+(?:\.m[0-9]{2,})?$`)
)

type Counts struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

func (c Counts) Total() int { return c.Passed + c.Failed + c.Skipped }

type Case struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
	Line   int    `json:"line,omitempty"`
}

type Document struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	AttemptID     string `json:"attempt_id"`
	Snapshot      string `json:"snapshot_manifest_digest"`
	TaskKey       string `json:"task_key"`
	Runner        string `json:"runner"`
	StdoutSHA256  string `json:"stdout_sha256"`
	StderrSHA256  string `json:"stderr_sha256"`
	Counts        Counts `json:"counts"`
	Cases         []Case `json:"cases,omitempty"`
}

type Expect struct {
	RunID, AttemptID, Snapshot, TaskKey, StdoutSHA256, StderrSHA256 string
	Lookup                                                          func(path string) bool
}

func ValidName(name string) bool {
	return name != "" && len(name) <= 512 && utf8.ValidString(name) &&
		strings.TrimSpace(name) == name && !strings.ContainsFunc(name, unicode.IsControl)
}

func Encode(doc Document, want Expect) ([]byte, error) {
	if err := doc.validate(want); err != nil {
		return nil, err
	}
	body, err := json.Marshal(doc)
	if err != nil || len(body) > MaxBytes {
		return nil, invalid
	}
	return domain.CanonicalJSON(body)
}

func Decode(body []byte, want Expect) (Document, error) {
	var doc Document
	if len(body) == 0 || len(body) > MaxBytes || domain.DecodeStrict(body, &doc) != nil || doc.validate(want) != nil {
		return Document{}, invalid
	}
	return doc, nil
}

func (doc Document) validate(want Expect) error {
	if doc.SchemaVersion != Version || !domain.ValidID(doc.RunID) || !domain.ValidID(doc.AttemptID) || !digest.MatchString(doc.Snapshot) ||
		!taskKey.MatchString(doc.TaskKey) || !digest.MatchString(doc.StdoutSHA256) || !digest.MatchString(doc.StderrSHA256) ||
		doc.RunID != want.RunID || doc.AttemptID != want.AttemptID || doc.Snapshot != want.Snapshot || doc.TaskKey != want.TaskKey ||
		doc.StdoutSHA256 != want.StdoutSHA256 || doc.StderrSHA256 != want.StderrSHA256 {
		return invalid
	}
	return ValidateCases(doc.Runner, doc.Counts, doc.Cases, want.Lookup)
}

func ValidateCases(runner string, c Counts, cases []Case, lookup func(path string) bool) error {
	if !validRunner(runner) || len(cases) > MaxCases ||
		c.Passed < 0 || c.Passed > MaxCount || c.Failed < 0 || c.Failed > MaxCount || c.Skipped < 0 || c.Skipped > MaxCount ||
		c.Total() == 0 || c.Total() > MaxCount {
		return invalid
	}
	for _, test := range cases {
		if !ValidName(test.Name) || (test.Path == "") != (test.Line == 0) || test.Line < 0 ||
			(test.Path != "" && (domain.ValidateEvidencePath(test.Path) != nil || lookup != nil && !lookup(test.Path))) {
			return invalid
		}
		switch test.Status {
		case Passed:
			c.Passed--
		case Failed:
			c.Failed--
		case Skipped:
			c.Skipped--
		default:
			return invalid
		}
	}
	if c.Passed < 0 || c.Failed < 0 || c.Skipped < 0 {
		return invalid
	}
	return nil
}

func validRunner(name string) bool {
	switch name {
	case "go", "unittest", "pytest", "jest", "vitest", "node-test", "playwright", "cargo", "nextest", "ctest", "maven", "gradle":
		return true
	}
	return false
}
