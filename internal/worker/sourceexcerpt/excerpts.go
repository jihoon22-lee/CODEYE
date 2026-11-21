// Package sourceexcerpt defines the bounded snapshot evidence uploaded by a worker.
package sourceexcerpt

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	MediaType       = "application/vnd.codeye.source-excerpts+json"
	Version         = "0.2"
	LegacyVersion   = "0.1"
	MaxItems        = 128
	MaxBytes        = 384 << 10
	maxFile         = 512 << 10
	maxText         = 1800 // JSON-encoded bytes per item
	DiagnosticLines = 9
	ModelLines      = 50
)

type Excerpt struct {
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Text      string `json:"text"`
}

type Item struct {
	Path   string `json:"path"`
	SHA256 string `json:"blob_sha256"`
	Anchor int    `json:"anchor_line"`
	Excerpt
}

type Document struct {
	SchemaVersion string `json:"schema_version"`
	RunID         string `json:"run_id"`
	AttemptID     string `json:"attempt_id"`
	Snapshot      string `json:"snapshot_manifest_digest"`
	Items         []Item `json:"items"`
}

type Key struct {
	Path   string
	SHA256 string
	Line   int
}

func (i Item) Key() Key { return Key{i.Path, i.SHA256, i.Anchor} }

type WindowKey struct {
	Key
	Start, End int
}

func (i Item) WindowKey() WindowKey { return WindowKey{i.Key(), i.StartLine, i.EndLine} }

var moreCredentials = regexp.MustCompile(`(?i)(\bgithub_pat_[a-z0-9_]{15,}|\b(?:AKIA|ASIA)[A-Z0-9]{16}\b|\b(?:xox[baprs]-|glpat-|npm_)[a-z0-9_/-]{12,}|\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{8,}|[a-z][a-z0-9+.-]*://[^\s/@:]+:[^\s/@]+@|\b(?:api[_-]?key|password|passwd|secret|token)\s*[:=]\s*["'][^"'\r\n]{8,}["'])`)

func safeText(b []byte) bool {
	return utf8.Valid(b) && !bytes.ContainsRune(b, 0) && !record.Sensitive(b) && !moreCredentials.Match(b)
}

// SafeDiagnosticText applies the code excerpt credential screen to tool notes.
func SafeDiagnosticText(text string) bool { return safeText([]byte(text)) }

func (e Excerpt) validText(anchor int) bool {
	return e.StartLine >= 1 && anchor >= e.StartLine && anchor <= e.EndLine &&
		e.EndLine-e.StartLine == strings.Count(e.Text, "\n") &&
		strings.TrimSpace(e.Text) != "" && record.JSONLen(e.Text) <= maxText && safeText([]byte(e.Text)) &&
		strings.IndexFunc(e.Text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) < 0
}

func (e Excerpt) ValidDiagnostic(anchor int) bool {
	return e.validText(anchor) && e.EndLine-e.StartLine < DiagnosticLines
}

// ValidCitation accepts the context window of a stored report and requires a
// longer excerpt to start and end within the actual model citation.
func (e Excerpt) ValidCitation(start, end int) bool {
	return e.ValidDiagnostic(start) || e.validModel(start, end)
}

func (e Excerpt) validModel(start, end int) bool {
	return e.StartLine == start && e.EndLine <= end && e.validText(start) && e.EndLine-start < ModelLines
}

// AtCitation narrows a new forward window to one of the references that
// share its starting line. A clipped prefix can become entirely blank.
func (e Excerpt) AtCitation(start, end int) (Excerpt, bool) {
	if end < start || !e.validModel(start, e.EndLine) {
		return Excerpt{}, false
	}
	if e.EndLine > end {
		lines := strings.SplitN(e.Text, "\n", end-start+2)
		e.EndLine, e.Text = end, strings.Join(lines[:end-start+1], "\n")
	}
	return e, e.validModel(start, end)
}

// SafePath excludes conventional credential files before opening them.
func SafePath(p string) bool {
	base := strings.ToLower(path.Base(p))
	ext := path.Ext(base)
	return domain.ValidateEvidencePath(p) == nil && safeText([]byte(p)) &&
		base != ".env" && !strings.HasPrefix(base, ".env.") && !strings.HasSuffix(base, ".env") &&
		base != ".netrc" && base != ".npmrc" && base != ".git-credentials" && !strings.HasPrefix(base, "id_") &&
		ext != ".pem" && ext != ".key" && ext != ".p12" && ext != ".pfx"
}

func Decode(body []byte, run, attempt, snapshot string, lookup func(string) (string, bool)) (Document, bool) {
	var doc Document
	if len(body) > MaxBytes || domain.DecodeStrict(body, &doc) != nil ||
		(doc.SchemaVersion != Version && doc.SchemaVersion != LegacyVersion) ||
		doc.RunID != run || doc.AttemptID != attempt || doc.Snapshot != snapshot ||
		len(doc.Items) == 0 || len(doc.Items) > MaxItems {
		return Document{}, false
	}
	legacy := doc.SchemaVersion == LegacyVersion
	seen := map[WindowKey]bool{}
	for _, i := range doc.Items {
		sha, ok := lookup(i.Path)
		key := i.WindowKey()
		if legacy {
			key.Start, key.End = 0, 0
		}
		valid := i.ValidDiagnostic(i.Anchor) || !legacy && i.validModel(i.Anchor, i.EndLine)
		if !ok || sha != i.SHA256 || !SafePath(i.Path) || !valid || seen[key] {
			return Document{}, false
		}
		seen[key] = true
	}
	return doc, true
}

// Read binds every line to bytes from the fixed snapshot, withholds a whole
// file on a credential match, and never truncates the highlighted line.
func Read(root *os.Root, entry source.Entry, anchor int) (Excerpt, bool) {
	if anchor < 1 || anchor > maxFile+1 {
		return Excerpt{}, false
	}
	start := max(anchor-3, 1)
	lines := readLines(root, entry, start, anchor+5)
	if anchor-start >= len(lines) {
		return Excerpt{}, false
	}
	end := start + len(lines) - 1
	for {
		e := Excerpt{StartLine: start, EndLine: end, Text: strings.Join(lines, "\n")}
		if e.ValidDiagnostic(anchor) {
			return e, true
		}
		switch {
		case start == end:
			return Excerpt{}, false
		case end-anchor >= anchor-start:
			lines = lines[:len(lines)-1]
			end--
		default:
			lines = lines[1:]
			start++
		}
	}
}

// ReadRange captures a contiguous, whole-line prefix of a model citation.
func ReadRange(root *os.Root, entry source.Entry, start, end int) (Excerpt, bool) {
	if start < 1 || start > maxFile+1 || end < start {
		return Excerpt{}, false
	}
	lines := readLines(root, entry, start, min(end, start+ModelLines-1))
	var e Excerpt
	for i := range lines {
		text := strings.Join(lines[:i+1], "\n")
		if record.JSONLen(text) > maxText {
			break
		}
		e = Excerpt{StartLine: start, EndLine: start + i, Text: text}
	}
	return e, e.validModel(start, end)
}

func readLines(root *os.Root, entry source.Entry, start, end int) []string {
	if entry.Kind != "file" || entry.ExcludedReason != "" || entry.Size == nil ||
		*entry.Size > maxFile || !SafePath(entry.Path) {
		return nil
	}
	st, err := root.Lstat(entry.Path)
	if err != nil || !st.Mode().IsRegular() {
		return nil
	}
	f, err := root.Open(entry.Path)
	if err != nil {
		return nil
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil || int64(len(b)) != *entry.Size || domain.Digest(b) != entry.SHA256 || !safeText(b) {
		return nil
	}
	lines := make([]string, 0, end-start+1)
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), maxFile+1)
	for line := 1; line <= end && scanner.Scan(); line++ {
		if line >= start {
			lines = append(lines, scanner.Text())
		}
	}
	return lines
}

func Encode(doc Document) ([]byte, error) { return json.Marshal(doc) }
