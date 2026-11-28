package review

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/review/record"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

// The closed world of one investigate task's leads
// (docs/REVIEW.md#단서-해석). A check that no other source of the project
// declares or writes a name (the one definition of a C or C++ prototype, an
// ordinary method's binding) reads every source of the language in the
// snapshot: inside or outside the run's scope and a pull request's changed
// files, on a secondary path, in a directory the review does not read as
// code, generated or not. Nothing it reads is sent; a definition found
// there is a hit resolve refuses with the file's own reason.
//
// A plain search for the name as a whole word (and, for a method, the words
// of code that may patch classes) rules out most sources before any is
// tokenized (holdsWord). A
// source the review withholds (by the secrets screen, its path or its
// content) is searched too but never tokenized or sent: holding the name,
// it leaves the question unanswered (lead_secret_withheld), as a source
// that is not UTF-8 text does (lead_read_failed). A source the snapshot
// holds no bytes of leaves every question of its language unanswered: one
// the snapshot policy left out for credentials (lead_secret_withheld), a
// link, a special file or a submodule it did not acquire (lead_read_failed;
// a submodule for every language). So do sources of the language past
// maxWorldBytes, and a question that would tokenize more than maxWorldFiles
// of them (lead_scan_limit). The snapshot's build outputs and dependency
// caches (generated_or_cache) are no project source. The task's leads share
// what the world read and found, and each gets the answer it would get
// alone. The scan limits (maxScanFiles, maxScanBytes) stay with what a lead
// reads to send.

const (
	// maxWorldBytes bounds the bytes of one language's sources the world
	// reads and searches; maxWorldFiles the sources one question tokenizes
	// (a task asks one question a lead, at most record.MaxLeads).
	maxWorldBytes = 64 << 20
	maxWorldFiles = 200
)

// leadWorld is the closed world of one investigate task.
type leadWorld struct {
	r *leadResolver
	// read reads a source's verified bytes (worldBytes); false when the
	// snapshot gives none.
	read  func(source.Entry) (string, bool, error)
	texts map[string]string
	// opaque holds, for a source a question met, why no question may
	// tokenize it ("" when one may); untokenized the sources the reader
	// could not tokenize.
	opaque      map[string]string
	untokenized map[string]bool
	sets        map[string]*worldSet
	// holding caches the sources of a language holding a word; native and
	// methods each file's answer for a name, patches each file's patching.
	holding map[[2]string]map[string]bool
	native  map[[2]string]nativeFile
	methods map[[2]string]methodFile
	patches map[string]bool
}

// worldSet is the sources of one language the world read, in manifest
// order, or why it cannot answer for the language.
type worldSet struct {
	paths  []string
	reason string
}

// nativeFile is a C or C++ source's definitions of a name (nativeExternals);
// ok is false when the source cannot be tokenized.
type nativeFile struct {
	defs []nativeDecl
	ok   bool
}

// methodFile is what a JavaScript, TypeScript or Python source holds for a
// method's name: its sites (the brackets or class blocks that may give
// instances a member of the name, jsMemberSites, pyMemberSites) and whether
// it writes such a member.
type methodFile struct {
	owners []int
	writes bool
}

// closedWorld is the resolver's world, made at its first question.
func (r *leadResolver) closedWorld() *leadWorld {
	if r.world == nil {
		read := r.worldRead
		if read == nil {
			read = r.s.worldBytes
		}
		r.world = &leadWorld{r: r, read: read, texts: map[string]string{}, opaque: map[string]string{}, untokenized: map[string]bool{},
			sets: map[string]*worldSet{}, holding: map[[2]string]map[string]bool{}, native: map[[2]string]nativeFile{}, methods: map[[2]string]methodFile{},
			patches: map[string]bool{}}
	}
	return r.world
}

// worldBytes reads the verified bytes of a source of the closed world, up to
// maxWorldBytes; false when the snapshot gives none for it. The world
// screens what it read itself (leadWorld.opaqueReason).
func (s *roleStage) worldBytes(e source.Entry) (string, bool, error) {
	if *e.Size > maxWorldBytes || domain.ValidateEvidencePath(e.Path) != nil {
		return "", false, nil
	}
	root, err := os.OpenRoot(s.j.Snapshot.SnapshotDir)
	if err != nil {
		return "", false, err
	}
	defer root.Close()
	b, err := readVerifiedWithin(root, e, maxWorldBytes)
	if err != nil {
		return "", false, err
	}
	return string(b), true, nil
}

// worldSource reports a source of the world's language lang: C files for
// c, C and C++ files for cpp (nativeSource), else the files lang's lead
// resolver reads (javascript for JavaScript and TypeScript, python).
func worldSource(p, lang string) bool {
	switch lang {
	case "c", "cpp":
		return nativeSource(p, lang)
	}
	return leadDefinitionLanguage(p) == lang
}

// sources reads the sources of lang. Which sources give no bytes or pass
// the byte bound is decided before any is read.
func (w *leadWorld) sources(lang string) (*worldSet, error) {
	if s, ok := w.sets[lang]; ok {
		return s, nil
	}
	s := &worldSet{}
	var read []source.Entry
	var total int64
	withheld, unread := false, false
	for _, p := range w.r.catalog.ordered {
		e := w.r.catalog.files[p]
		switch {
		case e.Kind == "submodule":
			// Its sources were not acquired: any language's may be there.
			unread = true
		case !worldSource(p, lang) || e.Kind == "directory" || e.ExcludedReason == "generated_or_cache":
		case e.ExcludedReason == "credential_directory" || e.ExcludedReason == "secret_file_policy":
			withheld = true
		case e.Kind != "file" || e.Size == nil || e.SHA256 == "" || e.ExcludedReason != "":
			unread = true
		default:
			total += *e.Size
			read = append(read, e)
		}
	}
	switch {
	case withheld:
		s.reason = record.LeadWithheld
	case unread:
		s.reason = record.LeadReadFailed
	case total > maxWorldBytes:
		s.reason = record.LeadScanLimit
	}
	for _, e := range read {
		if s.reason != "" {
			break
		}
		if err := w.r.s.ctx.Err(); err != nil {
			return nil, err
		}
		if _, ok := w.texts[e.Path]; !ok {
			text, ok, err := w.read(e)
			switch {
			case err != nil:
				return nil, err
			case !ok:
				s.reason = record.LeadReadFailed
				continue
			}
			w.texts[e.Path] = text
		}
		s.paths = append(s.paths, e.Path)
	}
	if s.reason != "" {
		s.paths = nil
	}
	w.sets[lang] = s
	return s, nil
}

// holdingName is the set of sources of lang whose bytes hold word
// (holdsWord), else the reason the world cannot answer for lang.
func (w *leadWorld) holdingName(lang, word string) (map[string]bool, string, error) {
	s, err := w.sources(lang)
	if err != nil {
		return nil, "", err
	}
	if s.reason != "" {
		return nil, s.reason, nil
	}
	key := [2]string{lang, word}
	found, ok := w.holding[key]
	if !ok {
		found = map[string]bool{}
		for _, p := range s.paths {
			if holdsWord(w.texts[p], word) {
				found[p] = true
			}
		}
		w.holding[key] = found
	}
	return found, "", nil
}

// holdsWord reports text holding word where a token of it may stand: no
// ASCII letter or underscore just before it and no ASCII letter, digit or
// underscore just after it where word begins or ends with one (a longer
// name holds it there; a digit before it may end a number, as in 0x1).
// Every token the lead readers take for word is such an occurrence.
func holdsWord(text, word string) bool {
	for i := 0; i <= len(text); {
		k := strings.Index(text[i:], word)
		if k < 0 {
			return false
		}
		k += i
		end := k + len(word)
		before := k == 0 || !isIdentByte(word[0]) || !isIdentByte(text[k-1]) || text[k-1] >= '0' && text[k-1] <= '9'
		if before && (end == len(text) || !isIdentByte(word[len(word)-1]) || !isIdentByte(text[end])) {
			return true
		}
		i = k + 1
	}
	return false
}

// opaqueReason is why no question may tokenize the world's source p, or
// "": lead_secret_withheld when the review withholds it (the secrets
// screen, its path, its content), lead_read_failed when it is not UTF-8
// text. It is decided only for a source a question needs.
func (w *leadWorld) opaqueReason(p string) string {
	if why, ok := w.opaque[p]; ok {
		return why
	}
	b := []byte(w.texts[p])
	var why string
	switch {
	case w.r.s.secretFiles()[p] || record.Sensitive([]byte(p)) || w.r.s.sensitiveBlob(w.r.catalog.files[p], b):
		why = record.LeadWithheld
	case !utf8.Valid(b) || bytes.IndexByte(b, 0) >= 0:
		why = record.LeadReadFailed
	}
	w.opaque[p] = why
	return why
}

// opaqueAmong is why a question needing the world's sources paths cannot
// tokenize one of them (lead_secret_withheld before lead_read_failed), or
// "".
func (w *leadWorld) opaqueAmong(paths []string) string {
	reason := ""
	for _, p := range paths {
		switch w.opaqueReason(p) {
		case record.LeadWithheld:
			return record.LeadWithheld
		case record.LeadReadFailed:
			reason = record.LeadReadFailed
		}
	}
	return reason
}

// text is the world's source p as the lead's scan reads it.
func (w *leadWorld) text(p string) string {
	return strings.TrimSuffix(w.texts[p], "\n")
}

// nativeDefinitions are the definitions of name at namespace scope (not
// static, not conditional, no variable) the world's C or C++ source p
// holds; false when p cannot be tokenized.
func (w *leadWorld) nativeDefinitions(p, name string) ([]nativeDecl, bool) {
	key := [2]string{p, name}
	if f, ok := w.native[key]; ok {
		return f.defs, f.ok
	}
	var f nativeFile
	if u := newNativeUnit(p, w.text(p)); u != nil {
		f.ok = true
		for _, d := range u.decls(name) {
			if d.body && !d.static && !d.value && !d.conditional {
				f.defs = append(f.defs, d)
			}
		}
	}
	w.native[key] = f
	return f.defs, f.ok
}

// methodAnswer is whether the world leaves the member name of the class
// whose body (JavaScript, its "{") or block (Python, its name token) is
// owner in the anchor's file p the only member of the name an instance may
// have: "" when it does, else lead_ambiguous (a write of a member of the
// name, a source that may patch classes otherwise, or in a source whose
// declarations count a member of the name another class declares; with
// owner -1 every one counts), lead_definition_not_found (a source holding
// the name or a patching word the reader cannot tokenize) or the reason
// the world cannot answer. declarations nil counts none.
func (w *leadWorld) methodAnswer(lang, p, name string, owner int, declarations func(string) bool) (string, error) {
	named, reason, err := w.holdingName(lang, name)
	if err != nil || reason != "" {
		return reason, err
	}
	words := jsPatchWords
	if lang == "python" {
		words = pyPatchWords
	}
	patching := map[string]bool{}
	for _, word := range words {
		found, _, err := w.holdingName(lang, word)
		if err != nil {
			return "", err
		}
		for q := range found {
			patching[q] = true
		}
	}
	var candidates []string
	for _, q := range w.sets[lang].paths {
		if named[q] || patching[q] {
			candidates = append(candidates, q)
		}
	}
	if len(candidates) > maxWorldFiles {
		return record.LeadScanLimit, nil
	}
	if why := w.opaqueAmong(candidates); why != "" {
		return why, nil
	}
	for _, q := range candidates {
		patches, ok := w.patching(lang, q)
		switch {
		case !ok:
			return record.LeadDefinitionMissing, nil
		case patches:
			return record.LeadAmbiguous, nil
		case !named[q]:
			continue
		}
		f, ok := w.method(lang, q, name)
		switch {
		case !ok:
			return record.LeadDefinitionMissing, nil
		case f.writes, declarations != nil && declarations(q) && slices.ContainsFunc(f.owners, func(o int) bool { return q != p || o != owner }):
			return record.LeadAmbiguous, nil
		}
	}
	return "", nil
}

// tokens is the world's source p tokenized, or nil when the reader cannot
// tokenize it.
func (w *leadWorld) tokens(p string) []callToken {
	if w.untokenized[p] {
		return nil
	}
	tokens := leadTokens(p, w.text(p))
	if tokens == nil {
		w.untokenized[p] = true
	}
	return tokens
}

// patching reports a source of lang that may give classes members no
// member of a name shows (jsPatches, pyPatches); false ok when it cannot be
// tokenized.
func (w *leadWorld) patching(lang, p string) (patches, ok bool) {
	if v, seen := w.patches[p]; seen {
		return v, true
	}
	tokens := w.tokens(p)
	if tokens == nil {
		return false, false
	}
	if lang == "python" {
		patches = pyPatches(tokens)
	} else {
		patches = jsPatches(tokens)
	}
	w.patches[p] = patches
	return patches, true
}

// method reads what the world's source p of lang holds for the method name;
// false when it cannot be tokenized.
func (w *leadWorld) method(lang, p, name string) (methodFile, bool) {
	key := [2]string{p, name}
	if f, ok := w.methods[key]; ok {
		return f, true
	}
	tokens := w.tokens(p)
	if tokens == nil {
		return methodFile{}, false
	}
	var f methodFile
	if lang == "python" {
		f.owners, f.writes = pyMemberSites(tokens, pythonBlocks(tokens), name)
	} else {
		f.owners, f.writes = jsMemberSites(tokens, name)
	}
	w.methods[key] = f
	return f, true
}
