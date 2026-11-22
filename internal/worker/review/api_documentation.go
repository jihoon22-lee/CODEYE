package review

import (
	"path"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/discovery/envdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	maxDocumentAPIBytes = 64 << 10
	maxAPISourceFiles   = 32
	maxAPISourceBytes   = 512 << 10
	rustAPISampled      = "rust_public_api_partially_sampled"
)

type documentedAPISources struct {
	stage       *roleStage
	catalog     *sourceCalls
	content     map[string]string
	files       int
	bytes       int64
	readLimited bool
}

func (s *roleStage) documentedAPISources(entries map[string]source.Entry) *documentedAPISources {
	r := &documentedAPISources{stage: s, content: map[string]string{}}
	r.catalog = &sourceCalls{sourceCallCatalog: &sourceCallCatalog{files: entries}, read: r.read}
	return r
}

func (r *documentedAPISources) read(entry source.Entry) (string, bool, error) {
	if text, cached := r.content[entry.Path]; cached {
		return text, text != "", nil
	}
	if err := r.stage.ctx.Err(); err != nil {
		return "", false, err
	}
	if entry.Size == nil || !inScope(entry.Path, normalizedScope(r.stage.j.Scope)) || r.stage.h.secondary().Match(entry.Path) != "" {
		return "", false, nil
	}
	if r.files >= maxAPISourceFiles || *entry.Size > maxAPISourceBytes-r.bytes {
		r.readLimited = true
		return "", false, nil
	}
	r.files++
	r.bytes += *entry.Size
	text, ok, err := r.stage.readSource(entry)
	if !ok || record.GeneratedHeader([]byte(text)) {
		text = ""
	}
	if err == nil {
		r.content[entry.Path] = text
	}
	return text, ok && text != "", err
}

func (r *documentedAPISources) target(p string) bool {
	return record.SourceKind(p) == "code" &&
		(r.stage.j.PullRequest == nil || slices.Contains(r.stage.j.PullRequest.Changed, p))
}

func (r *documentedAPISources) excerpts(page envdoc.Doc, content string, budget, limit int) ([]record.Excerpt, bool, error) {
	if limit <= 0 || len(content) > maxDocumentAPIBytes || r.stage.h.secondary().Match(page.Path) != "" {
		return nil, false, nil
	}
	var out []record.Excerpt
	add := func(x record.Excerpt) {
		if x.ValidDirectivePath() && x.DocumentScope() == page.AppliesTo && !slices.ContainsFunc(out, func(old record.Excerpt) bool { return old.ID == x.ID }) {
			out = append(out, x)
			budget -= record.JSONLen(x.Text)
		}
	}
	for _, target := range documentedPythonTargets(page.Path, content) {
		if len(out) >= limit || budget < 128 {
			break
		}
		entry, object, text, err := pythonDocumentedObject(r.catalog, page.AppliesTo, target)
		if err != nil {
			return nil, false, err
		}
		if entry.Path != "" && r.target(entry.Path) {
			if excerpt, ok := r.stage.pythonDocExcerpt(entry, page.Path, text, object, min(roleMaxDocBytes, budget)); ok {
				add(excerpt)
			}
		}
	}
	partial := false
	if len(out) < limit && budget >= 128 && record.MarkdownDirectivePath(page.Path) {
		uses := documentedRustTargets(content)
		fallback := len(uses) == 0 && page.Class == envdoc.ClassReadme
		if fallback {
			var err error
			uses, partial, err = rustCrateReexports(r.catalog, page.AppliesTo)
			if err != nil {
				return nil, false, err
			}
		}
		for _, use := range uses {
			if len(out) >= limit || budget < 128 {
				partial = partial || fallback
				break
			}
			entry, symbol, text, err := documentedRustObject(r.catalog, page.AppliesTo, use)
			if err != nil {
				return nil, false, err
			}
			if entry.Path != "" && r.target(entry.Path) {
				if excerpt, ok := r.stage.rustDocExcerpt(entry, page.Path, text, symbol, min(roleMaxDocBytes, budget)); ok {
					add(excerpt)
				}
			}
		}
		partial = partial || fallback && r.readLimited
	}
	return out, partial, nil
}

type apiFence struct {
	start, end int // inclusive, physical line numbers of the delimiters
	lang, text string
}

// apiFences recognizes complete, non-quoted Markdown examples. Inline text,
// HTML comments and nested documentation-language blocks are not code.
func apiFences(content string) []apiFence {
	var out []apiFence
	var fence, lang string
	var start int
	lines := strings.Split(content, "\n")
	comment := false
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if fence != "" {
			if strings.HasPrefix(line, fence) && strings.Trim(line, fence[:1]) == "" {
				out = append(out, apiFence{start + 1, i + 1, lang, strings.Join(lines[start+1:i], "\n")})
				fence = ""
			}
			continue
		}
		if comment || strings.Contains(line, "<!--") {
			comment = !strings.Contains(line, "-->")
			continue
		}
		if strings.HasPrefix(raw, "\t") || len(raw)-len(strings.TrimLeft(raw, " ")) > 3 ||
			(!strings.HasPrefix(line, "```") && !strings.HasPrefix(line, "~~~")) {
			continue
		}
		fence = line[:len(line)-len(strings.TrimLeft(line, line[:1]))]
		lang, _, _ = strings.Cut(strings.TrimSpace(line[len(fence):]), " ")
		start = i
	}
	return out
}

func productionAPISource(entry source.Entry, scope, ext string) bool {
	return entry.Path != "" && path.Ext(entry.Path) == ext && record.SourceKind(entry.Path) == "code" && record.ScopeUnder(entry.Path, scope)
}
