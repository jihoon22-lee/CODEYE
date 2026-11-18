// Package reviewpackr1 embeds the R1 candidate review pack (W15 phase 2). It
// is selected only by the node operator (codeye agent --review-pack
// review-r1); review-v1 stays the default and is not affected by this pack.
package reviewpackr1

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
)

//go:embed pack.yaml *.md languages/*.md
var files embed.FS

// Roles are the R1 model roles in plan order.
var Roles = []string{"environment", "documents", "investigate", "interpret", "verify"}

var roleFiles = map[string]string{
	"environment": "environment.md",
	"documents":   "documents.md",
	"investigate": "investigate.md",
	"interpret":   "interpret-diagnostics.md",
	"verify":      "verify.md",
}

// Selection is the instruction text of one role with the pack identity and
// the digest over pack.yaml and the selected files (review-v1 algorithm:
// sha256 over path NUL content NUL in UTF-8 path order).
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

// For returns the instructions of one R1 role.
func For(role string) (Selection, error) {
	f, ok := roleFiles[role]
	if !ok {
		return Selection{}, fmt.Errorf("unknown review role")
	}
	id, version, err := Identity()
	if err != nil {
		return Selection{}, err
	}
	ordered := []string{"00-core.md", f, "domains.md", "languages/go.md"}
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
