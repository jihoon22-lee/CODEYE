package jsts

import (
	"encoding/json"
	"errors"
	"path"
	"sort"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// tsconfig is JSONC data. CODEYE reads it, never executes it: compiler
// plugins listed in it are ignored by tsc (measured, docs/43 §3).

// StripJSONC removes comments and trailing commas outside strings, the two
// JSONC extensions TypeScript accepts in tsconfig files.
func StripJSONC(b []byte) []byte { return stripTrailingCommas(stripComments(b)) }

// stripComments drops // and /* */ comments outside JSON strings.
func stripComments(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inStr:
			out = append(out, c)
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
			out = append(out, c)
		case c == '/' && i+1 < len(b) && b[i+1] == '/':
			for i < len(b) && b[i] != '\n' {
				i++
			}
			if i < len(b) {
				out = append(out, '\n')
			}
		case c == '/' && i+1 < len(b) && b[i+1] == '*':
			i += 2
			for i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/') {
				i++
			}
			i++
			out = append(out, ' ')
		default:
			out = append(out, c)
		}
	}
	return out
}

func stripTrailingCommas(b []byte) []byte {
	out := make([]byte, 0, len(b))
	inStr, esc := false, false
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case inStr:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == ',':
			j := i + 1
			for j < len(b) && (b[j] == ' ' || b[j] == '\t' || b[j] == '\n' || b[j] == '\r') {
				j++
			}
			if j < len(b) && (b[j] == '}' || b[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

// ConfigChain is the tsconfig files a check reads, followed as data: the
// selected config, its local "extends" chain and the configs of its
// "references". Package extends (resolved from node_modules, never in a
// snapshot) are listed in Packages; tsc reports them unresolved.
type ConfigChain struct {
	// Files are snapshot paths with their manifest SHA-256, path order.
	Files []obsdoc.ConfigFile
	// References are the snapshot paths of the referenced configs of the
	// selected config, in declaration order.
	References []string
	// Packages are package-name extends specifiers.
	Packages []string
	// Outside reports an extends or reference that leaves the snapshot
	// (absolute or escaping path); the tool would read a file CODEYE cannot
	// bind to the manifest.
	Outside bool
	// Unreadable reports a config whose bytes are not the manifest's or that
	// is not JSONC (tsc reports the syntax error itself).
	Unreadable bool
}

// ErrIntegrity means a config file in the manifest could not be read with
// its manifest SHA-256 from the snapshot.
var ErrIntegrity = errors.New("tsconfig does not match the snapshot manifest")

// ReadChain follows the tsconfig at snapshot path cfg. It never reads
// outside the snapshot directory.
func ReadChain(snapshotDir string, files map[string]source.Entry, cfg string) (ConfigChain, error) {
	var c ConfigChain
	seen := map[string]bool{}
	var walk func(p string, depth int, top bool) error
	walk = func(p string, depth int, top bool) error {
		if seen[p] || depth > 16 {
			return nil
		}
		seen[p] = true
		e, ok := files[p]
		if !ok {
			// A missing local file: tsc reports it (TS5083/TS6053).
			return nil
		}
		b, err := govet.ReadSnapshotFile(snapshotDir, p)
		if err != nil || obsdoc.SHA256Hex(b) != e.SHA256 {
			return ErrIntegrity
		}
		c.Files = append(c.Files, obsdoc.ConfigFile{Path: p, SHA256: e.SHA256})
		var raw struct {
			Extends    json.RawMessage `json:"extends"`
			References []struct {
				Path string `json:"path"`
			} `json:"references"`
		}
		if json.Unmarshal(StripJSONC(b), &raw) != nil {
			c.Unreadable = true
			return nil
		}
		var ext []string
		var one string
		if len(raw.Extends) > 0 {
			if json.Unmarshal(raw.Extends, &one) == nil {
				ext = []string{one}
			} else {
				_ = json.Unmarshal(raw.Extends, &ext)
			}
		}
		dir := path.Dir(p)
		for _, x := range ext {
			x = strings.ReplaceAll(x, `\`, "/")
			switch {
			case x == "":
			case strings.HasPrefix(x, "/") || (len(x) > 1 && x[1] == ':'):
				c.Outside = true
			case strings.HasPrefix(x, "./") || strings.HasPrefix(x, "../"):
				target, ok := local(dir, x)
				if !ok {
					c.Outside = true
					continue
				}
				if _, in := files[target]; !in && !strings.HasSuffix(target, ".json") {
					target += ".json"
				}
				if err := walk(target, depth+1, false); err != nil {
					return err
				}
			default:
				c.Packages = append(c.Packages, x)
			}
		}
		for _, r := range raw.References {
			x := strings.ReplaceAll(r.Path, `\`, "/")
			if x == "" {
				continue
			}
			if strings.HasPrefix(x, "/") || (len(x) > 1 && x[1] == ':') {
				c.Outside = true
				continue
			}
			target, ok := local(dir, x)
			if !ok {
				c.Outside = true
				continue
			}
			if _, isFile := files[target]; !isFile || !strings.HasSuffix(target, ".json") {
				target = Join(target, "tsconfig.json")
			}
			if top {
				c.References = append(c.References, target)
			}
			if err := walk(target, depth+1, false); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(cfg, 0, true); err != nil {
		return ConfigChain{}, err
	}
	sort.Slice(c.Files, func(i, j int) bool { return c.Files[i].Path < c.Files[j].Path })
	sort.Strings(c.Packages)
	return c, nil
}

// local resolves a relative reference against a snapshot directory; false
// when it leaves the snapshot.
func local(dir, rel string) (string, bool) {
	j := path.Clean(path.Join(dir, rel))
	if j == ".." || strings.HasPrefix(j, "../") || strings.HasPrefix(j, "/") {
		return "", false
	}
	return j, true
}

// MergeConfigFiles unions config file lists (path order, unique).
func MergeConfigFiles(lists ...[]obsdoc.ConfigFile) []obsdoc.ConfigFile {
	seen := map[string]bool{}
	out := []obsdoc.ConfigFile{}
	for _, l := range lists {
		for _, f := range l {
			if !seen[f.Path] {
				seen[f.Path] = true
				out = append(out, f)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}
