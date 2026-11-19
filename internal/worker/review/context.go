// Package review composes the static analyzer with bounded model tasks on the
// same immutable snapshot. Source text is data and cannot select tools or roles.
package review

import (
	"bytes"
	"errors"
	"os"
	"path"
	"slices"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const maxFiles = 6
const maxContextBytes = 12000
const maxFileBytes = 512 << 10

type Corpus struct {
	Snapshot    string    `json:"snapshot_manifest_digest"`
	Excerpts    []Excerpt `json:"excerpts"`
	Eligible    int       `json:"eligible_files"`
	Documents   int       `json:"selected_documents"`
	Limitations []string  `json:"limitations"`
}

func inScope(name string, scope domain.Scope) bool {
	if scope.Kind == "full" {
		return true
	}
	if scope.Kind != "paths" {
		return false
	}
	for _, prefix := range scope.Paths {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

// priority ranks a corpus candidate: entry points, documents, sources,
// manifests, tests. The Go, README/AGENTS and go.mod rules stay fixed; the
// other ecosystems in scope follow otherPriority (sources.go).
func priority(name string, others otherScope) (rank int, kind string) {
	base := strings.ToLower(path.Base(name))
	switch {
	case base == "readme.md" || base == "agents.md":
		return 1, "document"
	case base == "main.go":
		return 0, "code"
	case strings.HasSuffix(base, ".go") && !strings.HasSuffix(base, "_test.go"):
		return 2, "code"
	case base == "go.mod":
		return 3, "config"
	case strings.HasSuffix(base, "_test.go"):
		return 4, "test"
	default:
		return otherPriority(name, others)
	}
}

func buildCorpus(snapshot source.Snapshot, scope domain.Scope) (Corpus, error) {
	return buildScreenedCorpus(snapshot, scope, nil)
}

// buildScreenedCorpus is buildCorpus without the files the secrets screen
// withholds (screen.go); with none withheld it is buildCorpus exactly.
func buildScreenedCorpus(snapshot source.Snapshot, scope domain.Scope, withheld map[string]bool) (Corpus, error) {
	c := Corpus{Snapshot: snapshot.Digest, Excerpts: []Excerpt{}, Limitations: []string{}}
	root, err := os.OpenRoot(snapshot.SnapshotDir)
	if err != nil {
		return c, err
	}
	defer root.Close()
	type selected struct {
		entry source.Entry
		rank  int
		kind  string
	}
	var options []selected
	others := newOtherScope(snapshot.Manifest, scope)
	for _, e := range snapshot.Manifest.Entries {
		if e.Kind != "file" || e.SHA256 == "" || e.Size == nil || e.ExcludedReason != "" || !inScope(e.Path, scope) {
			continue
		}
		if excludedSource(e.Path) {
			c.Limitations = addLim(c.Limitations, limExcludedSource)
		}
		r, k := priority(e.Path, others)
		if k == "" {
			continue
		}
		options = append(options, selected{e, r, k})
	}
	c.Eligible = len(options)
	sort.Slice(options, func(i, j int) bool {
		a, b := options[i], options[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if strings.Count(a.entry.Path, "/") != strings.Count(b.entry.Path, "/") {
			return strings.Count(a.entry.Path, "/") < strings.Count(b.entry.Path, "/")
		}
		return a.entry.Path < b.entry.Path
	})
	remain := maxContextBytes
	for _, o := range options {
		if len(c.Excerpts) >= maxFiles || remain < 128 {
			break
		}
		e := o.entry
		if o.kind == "document" && c.Documents >= 2 {
			continue
		}
		if domain.ValidateEvidencePath(e.Path) != nil || *e.Size > maxFileBytes {
			continue
		}
		if withheld[e.Path] {
			c.Limitations = addLim(c.Limitations, limSecretWithheld)
			continue
		}
		b, err := readVerified(root, e)
		if err != nil {
			return c, err
		}
		if !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0 || record.Sensitive(b) {
			continue
		}
		var lines []string
		used := 0
		for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
			if len(lines) >= 50 || used+len(line)+1 > min(remainingPerFile(o.kind), remain) {
				break
			}
			lines = append(lines, line)
			used += len(line) + 1
		}
		if len(lines) == 0 || used <= 1 {
			continue
		}
		c.Excerpts = append(c.Excerpts, Excerpt{ID: record.ExcerptID(snapshot.Digest, e.Path, e.SHA256, 1, len(lines)), Path: e.Path, SHA256: e.SHA256, Start: 1, End: len(lines), Kind: o.kind, Text: strings.Join(lines, "\n")})
		remain -= used
		if o.kind == "document" {
			c.Documents++
		}
		if used < len(b) {
			c.Limitations = append(c.Limitations, "excerpt_only")
		}
	}
	if c.Documents == 0 {
		c.Limitations = append(c.Limitations, "no_document_in_selected_context")
	}
	if c.Eligible > len(c.Excerpts) {
		c.Limitations = append(c.Limitations, "files_outside_review_context")
	}
	sort.Strings(c.Limitations)
	c.Limitations = slices.Compact(c.Limitations)
	if len(c.Excerpts) == 0 {
		return c, errors.New("no_review_context")
	}
	return c, nil
}

func remainingPerFile(kind string) int {
	if kind == "document" {
		return 1200
	}
	return 3200
}
