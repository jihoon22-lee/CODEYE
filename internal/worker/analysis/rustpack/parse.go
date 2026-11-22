package rustpack

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
)

// Parser identity: `cargo ... --message-format=json`, one JSON object per line.
const (
	ParserID      = "cargo-json-v1"
	ParserVersion = "1"
)

// ---- versions ---------------------------------------------------------------

var (
	cargoVersionPattern  = regexp.MustCompile(`^cargo ([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?)(?: \(([0-9a-f]{7,40} [0-9]{4}-[0-9]{2}-[0-9]{2})\))?$`)
	clippyVersionPattern = regexp.MustCompile(`^clippy ([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?)(?: \(([0-9a-f]{7,40} [0-9]{4}-[0-9]{2}-[0-9]{2})\))?$`)
	rustcReleasePattern  = regexp.MustCompile(`^release: ([0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.]+)?)$`)
	rustcHostPattern     = regexp.MustCompile(`^host: ([a-z0-9_]+(?:-[a-z0-9_.]+){1,3})$`)
	rustcFirstPattern    = regexp.MustCompile(`^rustc [0-9]+\.[0-9]+\.[0-9]+`)
)

func firstLine(stdout []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(stdout)), "\n")
	return strings.TrimSpace(strings.TrimRight(line, "\r"))
}

func parseToolVersion(re *regexp.Regexp, stdout []byte) (string, bool) {
	m := re.FindStringSubmatch(firstLine(stdout))
	if m == nil {
		return "", false
	}
	if m[2] != "" {
		return m[1] + " (" + m[2] + ")", true
	}
	return m[1], true
}

// ParseCargoVersion reads `cargo --version`: "cargo 1.99.1 (5f94df478
// 2026-08-27)" → "1.99.1 (5f94df478 2026-08-27)". Distribution builds
// without the parenthesized build id are accepted; anything else is not.
func ParseCargoVersion(stdout []byte) (string, bool) {
	return parseToolVersion(cargoVersionPattern, stdout)
}

// ParseClippyVersion reads `cargo-clippy --version`: "clippy 0.1.99
// (58cf8f9c35 2026-10-13)".
func ParseClippyVersion(stdout []byte) (string, bool) {
	return parseToolVersion(clippyVersionPattern, stdout)
}

// Rustc is what `rustc -vV` identifies.
type Rustc struct {
	Release string // 1.99.1
	Host    string // x86_64-unknown-linux-gnu
}

// ParseRustcVV reads `rustc -vV`; both the release and the host triple are
// required (the triple pins the build target, docs/ANALYZERS.md).
func ParseRustcVV(stdout []byte) (Rustc, bool) {
	var r Rustc
	lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
	if len(lines) == 0 || !rustcFirstPattern.MatchString(strings.TrimSpace(lines[0])) {
		return Rustc{}, false
	}
	for _, l := range lines[1:] {
		l = strings.TrimRight(l, "\r")
		if m := rustcReleasePattern.FindStringSubmatch(l); m != nil {
			r.Release = m[1]
		}
		if m := rustcHostPattern.FindStringSubmatch(l); m != nil {
			r.Host = m[1]
		}
	}
	return r, r.Release != "" && r.Host != ""
}

// Tested release series: the cargo and clippy bytes pinned by the captured
// fixtures (testdata/).
const (
	TestedCargo  = "1.99"
	TestedClippy = "0.1.99"
	TestedRustc  = "1.99"
)

func seriesOf(v string, n int) string {
	v, _, _ = strings.Cut(v, " ")
	v, _, _ = strings.Cut(v, "-")
	parts := strings.Split(v, ".")
	if len(parts) < n {
		return v
	}
	return strings.Join(parts[:n], ".")
}

// CargoVersionTested reports whether a parsed cargo version is of the tested
// series. A pre-release (nightly, beta) is never tested.
func CargoVersionTested(v string) bool {
	rel, _, _ := strings.Cut(v, " ")
	return !strings.Contains(rel, "-") && seriesOf(v, 2) == TestedCargo
}

// ClippyVersionTested reports whether a parsed clippy version is tested.
func ClippyVersionTested(v string) bool {
	rel, _, _ := strings.Cut(v, " ")
	return !strings.Contains(rel, "-") && seriesOf(v, 3) == TestedClippy
}

// RustcVersionTested reports whether a rustc release is of the tested series.
func RustcVersionTested(release string) bool {
	return !strings.Contains(release, "-") && seriesOf(release, 2) == TestedRustc
}

// ---- cargo JSON stream ------------------------------------------------------

// spanJSON is one rustc diagnostic span. Unknown fields are a shape error:
// the parser is pinned to the tested rustc's format.
type spanJSON struct {
	FileName           *string         `json:"file_name"`
	ByteStart          *int            `json:"byte_start"`
	ByteEnd            *int            `json:"byte_end"`
	LineStart          *int            `json:"line_start"`
	LineEnd            *int            `json:"line_end"`
	ColumnStart        *int            `json:"column_start"`
	ColumnEnd          *int            `json:"column_end"`
	IsPrimary          *bool           `json:"is_primary"`
	Text               json.RawMessage `json:"text"`
	Label              *string         `json:"label"`
	SuggestedReplace   *string         `json:"suggested_replacement"`
	SuggestedApplicity *string         `json:"suggestion_applicability"`
	Expansion          json.RawMessage `json:"expansion"`
}

type codeJSON struct {
	Code        *string `json:"code"`
	Explanation *string `json:"explanation"`
}

type diagJSON struct {
	MessageType *string    `json:"$message_type"`
	Message     *string    `json:"message"`
	Code        *codeJSON  `json:"code"`
	Level       *string    `json:"level"`
	Spans       []spanJSON `json:"spans"`
	Children    []diagJSON `json:"children"`
	Rendered    *string    `json:"rendered"`
}

// recordJSON is the union of every cargo message the parser accepts; the
// reason selects which fields must be present.
type recordJSON struct {
	Reason       *string         `json:"reason"`
	PackageID    *string         `json:"package_id"`
	ManifestPath *string         `json:"manifest_path"`
	Target       json.RawMessage `json:"target"`
	Profile      json.RawMessage `json:"profile"`
	Features     json.RawMessage `json:"features"`
	Filenames    json.RawMessage `json:"filenames"`
	Executable   json.RawMessage `json:"executable"`
	Fresh        *bool           `json:"fresh"`
	Message      *diagJSON       `json:"message"`
	LinkedLibs   json.RawMessage `json:"linked_libs"`
	LinkedPaths  json.RawMessage `json:"linked_paths"`
	Cfgs         json.RawMessage `json:"cfgs"`
	Env          json.RawMessage `json:"env"`
	OutDir       *string         `json:"out_dir"`
	Success      *bool           `json:"success"`
}

// Span is a parsed diagnostic span (1-based lines, character columns).
type Span struct {
	File                         string
	LineStart, LineEnd           int
	ColStart, ColEnd             int
	Primary                      bool
	Label                        string
	HasSuggestion, HasReplaceFix bool
}

// Diag is a parsed rustc diagnostic with its children (notes, helps).
type Diag struct {
	Level, Message, Code string
	Spans                []Span
	Children             []Diag
}

// MsgRecord is one compiler-message record with its byte range in stdout.
type MsgRecord struct {
	Index              int
	PackageID          string
	ManifestPath       string
	TargetName         string
	TargetKinds        []string
	Diag               Diag
	ByteStart, ByteEnd int64
}

// ArtifactRecord is one compiler-artifact record.
type ArtifactRecord struct {
	PackageID, ManifestPath, TargetName, SourcePath string
	Documentation                                   bool
	TargetKinds                                     []string
	Fresh                                           bool
}

// BuildScript is one build-script-executed record: evidence that a build
// script of the repository ran.
type BuildScript struct{ PackageID string }

// Stream is the parse of stdout. UnparsedBytes > 0, a DecodeError or a shape
// error means part of stdout could not be read; none may become "no findings".
type Stream struct {
	Messages      []MsgRecord
	Artifacts     []ArtifactRecord
	BuildScripts  []BuildScript
	Finished      []bool // success values of build-finished records
	Values        int
	ShapeErrors   int
	UnparsedBytes int64
	DecodeError   string
}

func (s *spanJSON) valid() bool {
	return s.FileName != nil && s.ByteStart != nil && s.ByteEnd != nil && s.LineStart != nil && s.LineEnd != nil && s.ColumnStart != nil && s.ColumnEnd != nil && s.IsPrimary != nil &&
		*s.LineStart >= 1 && *s.LineEnd >= *s.LineStart && *s.ColumnStart >= 1 && *s.ColumnEnd >= 1 && *s.ByteStart >= 0 && *s.ByteEnd >= *s.ByteStart
}

// strictJSON decodes raw into v rejecting unknown fields.
func strictJSON(raw []byte, v any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

// diagOf converts a decoded diagnostic; ok is false for a malformed one.
func diagOf(j *diagJSON, depth int) (Diag, bool) {
	if depth > 8 || j.Message == nil || j.Level == nil {
		return Diag{}, false
	}
	d := Diag{Level: *j.Level, Message: *j.Message}
	if j.Code != nil {
		if j.Code.Code == nil {
			return Diag{}, false
		}
		d.Code = *j.Code.Code
	}
	for i := range j.Spans {
		s := &j.Spans[i]
		if !s.valid() {
			return Diag{}, false
		}
		sp := Span{File: *s.FileName, LineStart: *s.LineStart, LineEnd: *s.LineEnd, ColStart: *s.ColumnStart, ColEnd: *s.ColumnEnd, Primary: *s.IsPrimary}
		if s.Label != nil {
			sp.Label = *s.Label
		}
		sp.HasReplaceFix = s.SuggestedReplace != nil
		d.Spans = append(d.Spans, sp)
	}
	for i := range j.Children {
		c, ok := diagOf(&j.Children[i], depth+1)
		if !ok {
			return Diag{}, false
		}
		d.Children = append(d.Children, c)
	}
	return d, true
}

// strictDiag re-decodes the diagnostic object strictly. The raw message is
// decoded twice (record level is lenient on nested objects such as target and
// profile, strict on the diagnostic and its spans).
func strictDiag(raw json.RawMessage) (*diagJSON, bool) {
	var d diagJSON
	if err := strictJSON(raw, &d); err != nil {
		return nil, false
	}
	return &d, true
}

type recordRaw struct {
	Message json.RawMessage `json:"message"`
}

type targetJSON struct {
	Kind    []string `json:"kind"`
	Name    *string  `json:"name"`
	SrcPath *string  `json:"src_path"`
	Doc     bool     `json:"doc"`
}

func targetOf(raw json.RawMessage) (name string, kinds []string, ok bool) {
	var t targetJSON
	if json.Unmarshal(raw, &t) != nil || t.Name == nil || len(t.Kind) == 0 {
		return "", nil, false
	}
	return *t.Name, t.Kind, true
}

// ParseStream reads cargo's stdout value by value with byte offsets. Every
// record is decoded strictly at the record level; a value of the wrong shape
// is counted, not dropped.
func ParseStream(stdout []byte) Stream {
	var out Stream
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		start := pack.SkipSpace(stdout, dec.InputOffset())
		if start >= int64(len(stdout)) {
			return out
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if errors.Is(err, io.EOF) {
				return out
			}
			out.DecodeError = pack.ClassifyDecodeError(err)
			out.UnparsedBytes = int64(len(stdout)) - start
			return out
		}
		end := dec.InputOffset()
		index := out.Values
		out.Values++
		var r recordJSON
		if err := strictJSON(raw, &r); err != nil || r.Reason == nil {
			out.ShapeErrors++
			continue
		}
		switch *r.Reason {
		case "compiler-message":
			var rr recordRaw
			_ = json.Unmarshal(raw, &rr)
			dj, ok := strictDiag(rr.Message)
			if !ok || r.PackageID == nil || r.ManifestPath == nil || r.Target == nil {
				out.ShapeErrors++
				continue
			}
			name, kinds, tok := targetOf(r.Target)
			diag, dok := diagOf(dj, 0)
			if !tok || !dok {
				out.ShapeErrors++
				continue
			}
			out.Messages = append(out.Messages, MsgRecord{Index: index, PackageID: *r.PackageID, ManifestPath: *r.ManifestPath, TargetName: name, TargetKinds: kinds, Diag: diag, ByteStart: start, ByteEnd: end})
		case "compiler-artifact":
			name, kinds, tok := targetOf(r.Target)
			if r.PackageID == nil || r.ManifestPath == nil || r.Fresh == nil || !tok || r.Filenames == nil || r.Profile == nil {
				out.ShapeErrors++
				continue
			}
			var target targetJSON
			json.Unmarshal(r.Target, &target)
			var sourcePath string
			if target.SrcPath != nil {
				sourcePath = *target.SrcPath
			}
			var outputs []string
			if err := json.Unmarshal(r.Filenames, &outputs); err != nil {
				out.ShapeErrors++
				continue
			}
			documented := target.Doc && slices.ContainsFunc(outputs, func(f string) bool {
				p := strings.ReplaceAll(f, "\\", "/")
				return strings.HasSuffix(p, "/index.html")
			})
			out.Artifacts = append(out.Artifacts, ArtifactRecord{Documentation: documented, SourcePath: sourcePath, PackageID: *r.PackageID, ManifestPath: *r.ManifestPath, TargetName: name, TargetKinds: kinds, Fresh: *r.Fresh})
		case "build-script-executed":
			if r.PackageID == nil || r.OutDir == nil {
				out.ShapeErrors++
				continue
			}
			out.BuildScripts = append(out.BuildScripts, BuildScript{PackageID: *r.PackageID})
		case "build-finished":
			if r.Success == nil {
				out.ShapeErrors++
				continue
			}
			out.Finished = append(out.Finished, *r.Success)
		default:
			// A reason of a cargo this parser was not pinned to.
			out.ShapeErrors++
		}
	}
}

// PackageName returns the package name of a cargo package id in either
// format: "path+file:///dir/name#0.1.0" (name is the last path segment) or
// "path+file:///dir#name@0.1.0".
func PackageName(id string) string {
	base, frag, hasFrag := strings.Cut(id, "#")
	if hasFrag {
		if name, _, ok := strings.Cut(frag, "@"); ok && name != "" {
			return name
		}
	}
	base = strings.TrimRight(base, "/")
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	return base
}

// ---- stderr -----------------------------------------------------------------

// StderrFacts are the stable stderr patterns that explain a failed cargo run.
// Cargo prints progress and warnings there too; those carry no fact and are
// never an error by themselves (the exit status and build-finished decide).
type StderrFacts struct {
	Toolchain       bool // rustc too old/new, edition or feature gate, missing std
	LockMissing     bool
	LockOutdated    bool
	Dependency      bool
	Linker          bool
	ManifestInvalid bool
	BuildScript     bool
	CompileError    bool
	Lines           int
	Other           int
}

var (
	errorLine = regexp.MustCompile(`^error(\[E[0-9]{4}\])?: `)
)

// ClassifyStderr reads cargo's stderr. Only cargo's own error lines count: they
// start in column 0 with "error". Everything else is not evidence of anything:
// "warning:" lines (a build script can print `cargo:warning=...` with any
// text), and the indented stderr of a failed build script or tool. The caller
// applies the facts to a run that exited non-zero only.
func ClassifyStderr(stderr []byte) StderrFacts {
	var f StderrFacts
	afterError := false
	for _, l := range strings.Split(string(stderr), "\n") {
		l = strings.TrimRight(l, "\r")
		if strings.TrimSpace(l) == "" {
			continue
		}
		f.Lines++
		if !strings.HasPrefix(l, "error") {
			// TOML syntax errors point into the manifest on the next line:
			// " --> Cargo.toml:1:9".
			if afterError && strings.HasPrefix(l, " --> ") && strings.Contains(l, "Cargo.toml:") {
				f.ManifestInvalid = true
			}
			afterError = false
			continue
		}
		afterError = true
		switch {
		case strings.Contains(l, "cannot create the lock file") && strings.Contains(l, "--locked"):
			f.LockMissing = true
		case strings.Contains(l, "needs to be updated but --locked was passed"), strings.Contains(l, "cannot update the lock file") && strings.Contains(l, "--locked"):
			f.LockOutdated = true
		case strings.Contains(l, "no matching package named"), strings.Contains(l, "failed to select a version for"), strings.Contains(l, "failed to get `"),
			strings.Contains(l, "failed to load source for dependency"), strings.Contains(l, "unable to get packages from source"),
			strings.Contains(l, "attempting to make an HTTP request, but --offline was specified"), strings.Contains(l, "failed to download"),
			strings.Contains(l, "is not available offline"):
			f.Dependency = true
		case strings.Contains(l, "linker `") && strings.Contains(l, "not found"):
			f.Linker = true
		case strings.Contains(l, "failed to parse manifest"), strings.Contains(l, "could not find `Cargo.toml`"), strings.Contains(l, "failed to load manifest for"):
			f.ManifestInvalid = true
		case strings.Contains(l, "failed to run custom build command"):
			f.BuildScript = true
		case strings.Contains(l, "requires rustc ") && strings.Contains(l, "or newer"), strings.Contains(l, "is not supported by the following packages"),
			strings.Contains(l, "can't find crate for `std`"), strings.Contains(l, "can't find crate for `core`"):
			f.Toolchain = true
		case strings.HasPrefix(l, "error: could not compile "):
			f.CompileError = true
		case errorLine.MatchString(l):
			f.Other++
		}
	}
	return f
}

// ---- dep-info ---------------------------------------------------------------

// ParseDepInfo reads a rustc dep-info (.d) file and returns the source files
// it lists: the phony "<path>:" lines that follow the main rule. Spaces in a
// path are escaped with a backslash.
func ParseDepInfo(b []byte) []string {
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimRight(l, "\r")
		if l == "" || strings.HasPrefix(l, "#") || !strings.HasSuffix(l, ":") {
			continue
		}
		p := strings.TrimSuffix(l, ":")
		p = strings.ReplaceAll(p, `\ `, " ")
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
