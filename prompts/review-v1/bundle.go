// Package reviewpack embeds the platform's trusted instructions at build time.
package reviewpack

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

//go:embed pack.yaml *.md languages/*.md
var files embed.FS

type Selection struct {
	Text   string
	Files  []string
	Digest string
}

func For(role string) (Selection, error) {
	roles := map[string]string{"context": "01-context.md", "investigate": "02-investigate.md", "verify": "03-verify.md"}
	f, ok := roles[role]
	if !ok {
		return Selection{}, fmt.Errorf("unknown review role")
	}
	ordered := []string{"00-core.md", f, "domains.md", "languages/go.md"}
	var paragraphs []string
	h := sha256.New()
	names := append([]string{"pack.yaml"}, ordered...)
	sort.Strings(names)
	for _, name := range names {
		b, err := files.ReadFile(name)
		if err != nil {
			return Selection{}, err
		}
		fmt.Fprintf(h, "%s%c", name, 0)
		h.Write(b)
		h.Write([]byte{0})
	}
	for _, name := range ordered {
		b, err := files.ReadFile(name)
		if err != nil {
			return Selection{}, err
		}
		paragraphs = append(paragraphs, string(b))
	}
	return Selection{Files: names, Digest: hex.EncodeToString(h.Sum(nil)), Text: strings.Join(paragraphs, "\n\n")}, nil
}
