// Package reviewpack embeds the review instructions: pack.yaml, the common
// core, the language guides and one file per model role.
package reviewpack

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
)

//go:embed pack.yaml *.md languages/*.md
var files embed.FS

// Roles are the model roles of the review in plan order.
var Roles = []string{"environment", "documents", "investigate", "interpret", "leads", "triage", "verify", "contract"}

var roleFiles = map[string]string{
	"environment": "environment.md",
	"documents":   "documents.md",
	"investigate": "investigate.md",
	"interpret":   "interpret-diagnostics.md",
	"leads":       "leads.md",
	"triage":      "triage.md",
	"verify":      "verify.md",
	"contract":    "contract.md",
}

// languageFiles are the language guides by source ecosystem (the envdoc and
// obsdoc ecosystem names that pack.InScope returns); pack.yaml lists the same
// map.
var languageFiles = map[string]string{
	"cpp":        "languages/cpp.md",
	"go":         "languages/go.md",
	"java":       "languages/java.md",
	"javascript": "languages/javascript.md",
	"python":     "languages/python.md",
	"rust":       "languages/rust.md",
}

// Languages returns the ecosystems that have a language guide, sorted.
func Languages() []string {
	out := make([]string, 0, len(languageFiles))
	for eco := range languageFiles {
		out = append(out, eco)
	}
	sort.Strings(out)
	return out
}

// Selection is the instruction text of one role with the pack identity and
// the digest over pack.yaml and the selected files (sha256 over path NUL
// content NUL in UTF-8 path order).
type Selection struct {
	ID, Version string
	Text        string
	Files       []string
	Digest      string
}

// Identity returns the id and version declared in the embedded pack.yaml.
func Identity() (id, version string, err error) {
	b, err := files.ReadFile("pack.yaml")
	if err != nil {
		return "", "", err
	}
	s := bufio.NewScanner(bytes.NewReader(b))
	for s.Scan() {
		k, v, ok := strings.Cut(s.Text(), ": ")
		switch {
		case !ok:
		case k == "id":
			id = strings.TrimSpace(v)
		case k == "version":
			version = strings.TrimSpace(v)
		}
	}
	if id == "" || version == "" {
		return "", "", errors.New("pack.yaml lacks id or version")
	}
	return id, version, nil
}

// For returns the instructions of one role for a snapshot whose in-scope
// source ecosystems are ecosystems (as pack.InScope returns them; docs/ANALYZERS.md
// §2.8). Only the guides of those ecosystems are selected, after the core,
// role and domain files in ascending ecosystem order; an ecosystem without a
// guide adds nothing, so a snapshot with no supported ecosystem in scope gets
// no language guide. The digest covers pack.yaml and exactly the selected
// files.
func For(role string, ecosystems []string) (Selection, error) {
	f, ok := roleFiles[role]
	if !ok {
		return Selection{}, fmt.Errorf("unknown review role")
	}
	id, version, err := Identity()
	if err != nil {
		return Selection{}, err
	}
	ordered := []string{"00-core.md", f, "domains.md"}
	for _, eco := range Languages() {
		if slices.Contains(ecosystems, eco) {
			ordered = append(ordered, languageFiles[eco])
		}
	}
	names := append([]string{"pack.yaml"}, ordered...)
	sort.Strings(names)
	h := sha256.New()
	for _, name := range names {
		b, err := files.ReadFile(name)
		if err != nil {
			return Selection{}, err
		}
		fmt.Fprintf(h, "%s%c", name, 0)
		h.Write(b)
		h.Write([]byte{0})
	}
	var paragraphs []string
	for _, name := range ordered {
		b, err := files.ReadFile(name)
		if err != nil {
			return Selection{}, err
		}
		paragraphs = append(paragraphs, string(b))
	}
	return Selection{ID: id, Version: version, Files: names, Digest: hex.EncodeToString(h.Sum(nil)), Text: strings.Join(paragraphs, "\n\n")}, nil
}
