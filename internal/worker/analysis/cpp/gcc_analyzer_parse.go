package cpp

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

type gccPosition struct {
	File string `json:"file"`
	Line int    `json:"line"`
	Col  int    `json:"byte-column"`
}

type gccLocation struct {
	Caret  *gccPosition `json:"caret,omitempty"`
	Start  *gccPosition `json:"start,omitempty"`
	Finish *gccPosition `json:"finish,omitempty"`
}

type gccEvent struct {
	Location    gccPosition `json:"location"`
	Description string      `json:"description"`
}

type gccDiagnostic struct {
	Kind      string        `json:"kind"`
	Option    string        `json:"option,omitempty"`
	Message   string        `json:"message"`
	Locations []gccLocation `json:"locations,omitempty"`
	Path      []gccEvent    `json:"path,omitempty"`
}

type gccRecord struct {
	gccDiagnostic
	span   obsdoc.ByteSpan
	events []obsdoc.ByteSpan
	index  int
}

type gccDiagnostics struct {
	records       []gccRecord
	outOfScope    int
	compileErrors int
	missing       bool
	invalid       bool
	unavailable   bool
	tooComplex    bool
	trimmed       bool
}

func (d gccDiagnostic) location() *gccPosition {
	if len(d.Locations) != 0 {
		return d.Locations[0].Caret
	}
	return nil
}

func (d gccDiagnostic) includesOutside(allowed func(string) bool) bool {
	for _, l := range d.Locations {
		for _, p := range []*gccPosition{l.Caret, l.Start, l.Finish} {
			if p != nil && !allowed(p.File) {
				return true
			}
		}
	}
	return slices.ContainsFunc(d.Path, func(e gccEvent) bool { return !allowed(e.Location.File) })
}

// GCC emits one JSON array on stderr. Persist only selected diagnostics for
// which every supplied position belongs to the acquired workspace. Invalid
// output, external diagnostics and incidental compiler text never reach the
// execution artifact. Byte offsets are calculated after this projection.
func sanitizeGCCAnalyzer(r *executor.Result, allowed func(string) bool, mask func(string) string) gccDiagnostics {
	var parsed gccDiagnostics
	data := bytes.TrimSpace(r.Stderr.Data)
	var diagnostics []gccDiagnostic
	if len(bytes.TrimSpace(r.Stdout.Data)) != 0 || len(data) == 0 || data[0] != '[' || json.Unmarshal(data, &diagnostics) != nil {
		parsed.invalid = true
		replaceStream(&r.Stderr, nil)
		replaceStream(&r.Stdout, nil)
		return parsed
	}
	replaceStream(&r.Stdout, nil)
	var out bytes.Buffer
	out.WriteByte('[')
	for _, d := range diagnostics {
		switch d.Kind {
		case "error", "fatal error":
			parsed.compileErrors++
			parsed.missing = parsed.missing || strings.Contains(d.Message, "No such file or directory")
			d.Option = ""
			if d.location() == nil {
				continue // unlocated compiler failures can contain node paths
			}
		case "warning":
			if d.Option == "-Wanalyzer-too-complex" {
				parsed.tooComplex = true
			}
			if strings.Contains(d.Message, "analyzer") && strings.Contains(d.Message, "not supported in this configuration") {
				parsed.unavailable = true
			}
			if !slices.Contains(GCCAnalyzerSelection, d.Option) {
				continue
			}
			if d.location() == nil {
				parsed.invalid = true
				continue
			}
		case "note":
			continue
		default:
			parsed.invalid = true
			continue
		}
		if d.includesOutside(allowed) {
			parsed.outOfScope++
			continue
		}
		d.Message = mask(strings.TrimSuffix(d.Message, " ["+d.Option+"]"))
		if d.Message == "" || !obsdoc.SafeText(d.Message, 64<<10) {
			parsed.invalid = true
			continue
		}
		if len(d.Path) > 64 {
			parsed.trimmed = true
			d.Path = d.Path[:64]
		}
		for i := range d.Path {
			d.Path[i].Description = mask(d.Path[i].Description)
			if !obsdoc.SafeText(d.Path[i].Description, 8192) {
				d.Path[i].Description = "(omitted by the platform)"
			}
		}
		blob, err := json.Marshal(d)
		if err != nil {
			parsed.invalid = true
			continue
		}
		if len(parsed.records) != 0 {
			out.WriteByte(',')
		}
		start := out.Len()
		out.Write(blob)
		record := gccRecord{gccDiagnostic: d, span: obsdoc.ByteSpan{Start: int64(start), End: int64(out.Len())}, index: len(parsed.records)}
		cursor := bytes.Index(blob, []byte(`"path":[`))
		if cursor >= 0 {
			for _, event := range d.Path {
				value, _ := json.Marshal(event)
				i := bytes.Index(blob[cursor:], value)
				if i < 0 {
					parsed.invalid = true
					break
				}
				at := cursor + i
				cursor = at + len(value)
				record.events = append(record.events, obsdoc.ByteSpan{Start: int64(start + at), End: int64(start + cursor)})
			}
		}
		parsed.records = append(parsed.records, record)
	}
	out.WriteString("]\n")
	replaceStream(&r.Stderr, out.Bytes())
	return parsed
}

func gccRelatedKind(message string) string {
	switch {
	case strings.Contains(message, "assuming "):
		return obsdoc.RelatedAssumption
	case strings.Contains(message, "following ") && strings.Contains(message, " branch"):
		return obsdoc.RelatedBranch
	default:
		return obsdoc.RelatedNote
	}
}
