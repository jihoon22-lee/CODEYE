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
	MediaType = "application/vnd.codeye.source-excerpts+json"
	Version   = "0.1"
	MaxItems  = 128
	MaxBytes  = 384 << 10
	maxFile   = 512 << 10
	maxText   = 1800 // JSON-encoded bytes per item
	maxLines  = 9
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

var moreCredentials = regexp.MustCompile(`(?i)(\bgithub_pat_[a-z0-9_]{15,}|\b(?:AKIA|ASIA)[A-Z0-9]{16}\b|\b(?:xox[baprs]-|glpat-|npm_)[a-z0-9_/-]{12,}|\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]{8,}|[a-z][a-z0-9+.-]*://[^\s/@:]+:[^\s/@]+@|\b(?:api[_-]?key|password|passwd|secret|token)\s*[:=]\s*["'][^"'\r\n]{8,}["'])`)

func safeText(b []byte) bool {
	return utf8.Valid(b) && !bytes.ContainsRune(b, 0) && !record.Sensitive(b) && !moreCredentials.Match(b)
}

func (e Excerpt) Valid(anchor int) bool {
	return e.StartLine >= 1 && anchor >= e.StartLine && anchor <= e.EndLine &&
		e.EndLine-e.StartLine < maxLines && e.EndLine-e.StartLine+1 == strings.Count(e.Text, "\n")+1 &&
		strings.TrimSpace(e.Text) != "" && record.JSONLen(e.Text) <= maxText && safeText([]byte(e.Text)) &&
		strings.IndexFunc(e.Text, func(r rune) bool { return unicode.IsControl(r) && r != '\n' && r != '\t' }) < 0
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
		doc.SchemaVersion != Version || doc.RunID != run || doc.AttemptID != attempt || doc.Snapshot != snapshot ||
		len(doc.Items) == 0 || len(doc.Items) > MaxItems {
		return Document{}, false
	}
	seen := map[Key]bool{}
	for _, i := range doc.Items {
		sha, ok := lookup(i.Path)
		if !ok || sha != i.SHA256 || !SafePath(i.Path) || !i.Valid(i.Anchor) || seen[i.Key()] {
			return Document{}, false
		}
		seen[i.Key()] = true
	}
	return doc, true
}

// Read binds every line to bytes from the fixed snapshot, withholds a whole
// file on a credential match, and never truncates the highlighted line.
func Read(root *os.Root, entry source.Entry, anchor int) (Excerpt, bool) {
	if anchor < 1 || entry.Kind != "file" || entry.ExcludedReason != "" || entry.Size == nil ||
		*entry.Size > maxFile || !SafePath(entry.Path) {
		return Excerpt{}, false
	}
	st, err := root.Lstat(entry.Path)
	if err != nil || !st.Mode().IsRegular() {
		return Excerpt{}, false
	}
	f, err := root.Open(entry.Path)
	if err != nil {
		return Excerpt{}, false
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxFile+1))
	if err != nil || int64(len(b)) != *entry.Size || domain.Digest(b) != entry.SHA256 || !safeText(b) {
		return Excerpt{}, false
	}
	lines := make([]string, 0, maxLines)
	start := max(anchor-3, 1)
	scanner := bufio.NewScanner(bytes.NewReader(b))
	scanner.Buffer(make([]byte, 4096), maxFile+1)
	for line := 1; scanner.Scan() && line <= anchor+5; line++ {
		if line >= start {
			lines = append(lines, scanner.Text())
		}
	}
	if anchor-start >= len(lines) {
		return Excerpt{}, false
	}
	end := start + len(lines) - 1
	for {
		e := Excerpt{StartLine: start, EndLine: end, Text: strings.Join(lines, "\n")}
		if e.Valid(anchor) {
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

func Encode(doc Document) ([]byte, error) { return json.Marshal(doc) }
