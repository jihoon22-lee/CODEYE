// Package faildoc binds a Go test failure to its captured event and declaration.
package faildoc

import (
	"encoding/json"
	"errors"
	"go/token"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	MediaType      = "application/vnd.codeye.go-test-failures+json"
	Version        = "0.1"
	MaxBytes       = 256 << 10
	MaxStdoutBytes = 8 << 20
	MaxFailures    = 64
	MaxEventBytes  = 4 << 10
)

var (
	invalid    = errors.New("invalid Go test failure evidence")
	hex64      = regexp.MustCompile(`^[0-9a-f]{64}$`)
	goTaskKey  = regexp.MustCompile(`^test\.go(?:\.m[0-9]{2,})?$`)
	importPath = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._~+/-]{0,511}$`)
)

type Document struct {
	SchemaVersion string    `json:"schema_version"`
	RunID         string    `json:"run_id"`
	AttemptID     string    `json:"attempt_id"`
	Snapshot      string    `json:"snapshot_manifest_digest"`
	TaskKey       string    `json:"task_key"`
	StdoutSHA256  string    `json:"stdout_sha256"`
	Failures      []Failure `json:"failures"`
}

type Failure struct {
	Package    string `json:"package"`
	Test       string `json:"test"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	BlobSHA256 string `json:"blob_sha256"`
	ByteStart  int    `json:"byte_start"`
	ByteEnd    int    `json:"byte_end"`
}

type Expect struct {
	RunID, AttemptID, Snapshot, TaskKey, ModuleDir, StdoutSHA256 string
	Lookup                                                       func(string) (string, bool)
}

func GoTask(key string) bool { return goTaskKey.MatchString(key) }

// TestName allows a Go top-level test function name, with no subtest label.
func TestName(name string) bool {
	suffix, ok := strings.CutPrefix(name, "Test")
	first, _ := utf8.DecodeRuneInString(suffix)
	return ok && len(name) <= 128 && token.IsIdentifier(name) && !unicode.IsLower(first)
}

func validPackage(name string) bool {
	return importPath.MatchString(name) && domain.ValidateEvidencePath(name) == nil
}

func Encode(doc Document, want Expect) ([]byte, error) {
	if err := doc.validate(want); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(doc)
	if err != nil || len(raw) > MaxBytes {
		return nil, invalid
	}
	return domain.CanonicalJSON(raw)
}

func Decode(body []byte, want Expect) (Document, error) {
	var doc Document
	if len(body) == 0 || len(body) > MaxBytes || domain.DecodeStrict(body, &doc) != nil {
		return Document{}, invalid
	}
	if err := doc.validate(want); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (doc Document) validate(want Expect) error {
	if doc.SchemaVersion != Version || !domain.ValidID(doc.RunID) || !domain.ValidID(doc.AttemptID) || !hex64.MatchString(doc.Snapshot) ||
		!GoTask(doc.TaskKey) || !hex64.MatchString(doc.StdoutSHA256) || want.Lookup == nil ||
		doc.RunID != want.RunID || doc.AttemptID != want.AttemptID || doc.Snapshot != want.Snapshot || doc.TaskKey != want.TaskKey ||
		doc.StdoutSHA256 != want.StdoutSHA256 || (want.ModuleDir != "." && domain.ValidateEvidencePath(want.ModuleDir) != nil) ||
		len(doc.Failures) == 0 || len(doc.Failures) > MaxFailures {
		return invalid
	}
	seen := map[testKey]bool{}
	end := 0
	for _, f := range doc.Failures {
		key := testKey{f.Package, f.Test}
		if seen[key] || !validPackage(f.Package) || !TestName(f.Test) || f.Line < 1 ||
			domain.ValidateEvidencePath(f.Path) != nil || !strings.HasSuffix(f.Path, "_test.go") || !hex64.MatchString(f.BlobSHA256) ||
			(want.ModuleDir != "." && !strings.HasPrefix(f.Path, want.ModuleDir+"/")) ||
			f.ByteStart < end || f.ByteEnd <= f.ByteStart || f.ByteEnd > MaxStdoutBytes || f.ByteEnd-f.ByteStart > MaxEventBytes {
			return invalid
		}
		if sha, ok := want.Lookup(f.Path); !ok || sha != f.BlobSHA256 {
			return invalid
		}
		seen[key], end = true, f.ByteEnd
	}
	return nil
}
