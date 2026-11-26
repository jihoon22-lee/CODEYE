package javapack

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

var javaRule = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{0,127}$`)

type pmdFile struct {
	Filename   string
	Violations []json.RawMessage
}

type pmdViolation struct {
	Rule, Ruleset      string
	BeginLine, EndLine int
}

func parsePMD(run *anypack.Run, files []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome, body []byte) {
	var report struct {
		Files                                 []json.RawMessage
		PMDVersion                            string
		ProcessingErrors, ConfigurationErrors []json.RawMessage
	}
	if err := json.Unmarshal(body, &report); err != nil || report.Files == nil || report.PMDVersion == "" ||
		doc.Tool.Version == nil || report.PMDVersion != *doc.Tool.Version || report.ProcessingErrors == nil || report.ConfigurationErrors == nil {
		terminalParseFailure(doc, outcome)
		return
	}
	if len(report.ProcessingErrors)+len(report.ConfigurationErrors) != 0 {
		doc.Coverage.Units["java_analysis_errors"] = len(report.ProcessingErrors) + len(report.ConfigurationErrors)
		outcome.Fail(domain.TaskFailed, "pmd_processing_errors", obsdoc.ExitConfigurationError)
	}
	known := map[string]bool{}
	for _, file := range files {
		known[file.Path] = true
	}
	resolver := anypack.NewResolver(run.Job.Snapshot.SnapshotDir, files, nil)
	digest := obsdoc.SHA256Hex(body)
	ordinals := map[string]int{}
	offset := 0
	for _, rawFile := range report.Files {
		var file pmdFile
		start := bytes.Index(body[offset:], rawFile)
		if start < 0 || json.Unmarshal(rawFile, &file) != nil || len(file.Violations) == 0 {
			terminalParseFailure(doc, outcome)
			return
		}
		start += offset
		offset = start + len(rawFile)
		rel, valid := run.RelPath(file.Filename)
		if !valid || !known[rel] {
			terminalParseFailure(doc, outcome)
			return
		}
		inside := 0
		for _, raw := range file.Violations {
			at := bytes.Index(rawFile[inside:], raw)
			var violation pmdViolation
			if at < 0 || json.Unmarshal(raw, &violation) != nil || !javaRule.MatchString(violation.Rule) ||
				violation.BeginLine < 1 || violation.EndLine < violation.BeginLine {
				terminalParseFailure(doc, outcome)
				return
			}
			at += inside
			inside = at + len(raw)
			category := "correctness"
			switch violation.Ruleset {
			case "Multithreading":
				category = "concurrency"
			case "Best Practices":
				category = "maintainability"
			}
			observation(doc, resolver, rel, violation.Rule, category, pmdExecution, digest, violation.BeginLine,
				violation.EndLine, int64(start+at), int64(start+inside), ordinals)
		}
	}
	doc.Coverage.ProcessedPaths = anypack.Paths(files)
}

type spotLine struct {
	Name       string `xml:"sourcefile,attr"`
	SourcePath string `xml:"sourcepath,attr"`
	Start      int    `xml:"start,attr"`
	End        int    `xml:"end,attr"`
}

type spotBug struct {
	Type     string     `xml:"type,attr"`
	Category string     `xml:"category,attr"`
	Lines    []spotLine `xml:"SourceLine"`
	// The class, method and field the bug is about carry their own
	// source lines; only their source path is read (to tell a report in a
	// compiler context file from one without a location). Referenced types
	// are not the bug's place and are not read.
	Classes []spotMember `xml:"Class"`
	Methods []spotMember `xml:"Method"`
	Fields  []spotMember `xml:"Field"`
}

type spotMember struct {
	Lines []spotLine `xml:"SourceLine"`
}

// sourcePaths are the source paths of every line of the bug.
func (b spotBug) sourcePaths() []string {
	var out []string
	for _, l := range b.Lines {
		out = append(out, l.SourcePath)
	}
	for _, members := range [][]spotMember{b.Classes, b.Methods, b.Fields} {
		for _, m := range members {
			for _, l := range m.Lines {
				out = append(out, l.SourcePath)
			}
		}
	}
	return out
}

type spotSummary struct {
	Classes  int `xml:"total_classes,attr"`
	Bugs     int `xml:"total_bugs,attr"`
	Packages []struct {
		Name    string `xml:"package,attr"`
		Classes []struct {
			File string `xml:"sourceFile,attr"`
		} `xml:"ClassStats"`
	} `xml:"PackageStats"`
}

func spotSourceNames(snapshotDir string, files []source.Entry) map[string]string {
	names := make(map[string]string, len(files))
	root, err := os.OpenRoot(snapshotDir)
	if err != nil {
		return names
	}
	defer root.Close()
	for _, file := range files {
		body, err := root.ReadFile(file.Path)
		if err != nil || int64(len(body)) != *file.Size || obsdoc.SHA256Hex(body) != file.SHA256 {
			continue
		}
		name := javaproject.SourceName(file.Path, body)
		if _, exists := names[name]; exists {
			names[name] = ""
		} else {
			names[name] = file.Path
		}
	}
	return names
}

func selectedSource(names map[string]string, name string) string {
	name = strings.TrimPrefix(strings.ReplaceAll(name, `\`, "/"), "./")
	if domain.ValidateEvidencePath(name) != nil || !strings.HasSuffix(name, ".java") {
		return ""
	}
	return names[name]
}

// parseSpotBugs reads the XML report. files are the selected sources;
// context are the other sources compiled with them, whose reports are outside
// the selected scope and counted, never located.
func parseSpotBugs(run *anypack.Run, files, context []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome, body []byte) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	var opened, closed, errorSeen bool
	var summary spotSummary
	var seen int
	digest := obsdoc.SHA256Hex(body)
	resolver := anypack.NewResolver(run.Job.Snapshot.SnapshotDir, files, nil)
	ordinals := map[string]int{}
	names := spotSourceNames(run.Job.Snapshot.SnapshotDir, files)
	contextNames := spotSourceNames(run.Job.Snapshot.SnapshotDir, context)
	outside := 0
	for {
		offset := dec.InputOffset()
		token, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			terminalParseFailure(doc, outcome)
			return
		}
		switch t := token.(type) {
		case xml.StartElement:
			if !opened {
				if t.Name.Local != "BugCollection" || !spotVersionMatches(t, doc.Tool.Version) {
					terminalParseFailure(doc, outcome)
					return
				}
				opened = true
				continue
			}
			switch t.Name.Local {
			case "BugInstance":
				var bug spotBug
				if dec.DecodeElement(&bug, &t) != nil || !javaRule.MatchString(bug.Type) {
					terminalParseFailure(doc, outcome)
					return
				}
				seen++
				category := "correctness"
				switch bug.Category {
				case "MT_CORRECTNESS":
					category = "concurrency"
				case "PERFORMANCE":
					category = "performance"
				case "SECURITY":
					category = "security"
				case "STYLE":
					category = "maintainability"
				}
				located := false
				for _, line := range bug.Lines {
					p := selectedSource(names, line.SourcePath)
					if p != "" && line.Start > 0 && line.End >= line.Start {
						observation(doc, resolver, p, bug.Type, category, spotbugsExecution, digest, line.Start, line.End, offset, dec.InputOffset(), ordinals)
						located = true
						break
					}
				}
				if paths := bug.sourcePaths(); !located && !slices.ContainsFunc(paths, func(sp string) bool { return selectedSource(names, sp) != "" }) &&
					slices.ContainsFunc(paths, func(sp string) bool { return selectedSource(contextNames, sp) != "" }) {
					outside++
				}
			case "Errors":
				var info struct {
					Errors  int `xml:"errors,attr"`
					Missing int `xml:"missingClasses,attr"`
				}
				if errorSeen || dec.DecodeElement(&info, &t) != nil || info.Errors < 0 || info.Missing < 0 {
					terminalParseFailure(doc, outcome)
					return
				}
				errorSeen = true
				if info.Errors > 0 || info.Missing > 0 {
					doc.Coverage.Units["java_analysis_errors"] = info.Errors + info.Missing
					outcome.Fail(domain.TaskFailed, "spotbugs_analysis_errors", obsdoc.ExitConfigurationError)
				}
			case "FindBugsSummary":
				if summary.Classes != 0 || dec.DecodeElement(&summary, &t) != nil {
					terminalParseFailure(doc, outcome)
					return
				}
			default:
				if err := dec.Skip(); err != nil {
					terminalParseFailure(doc, outcome)
					return
				}
			}
		case xml.EndElement:
			if !opened || closed || t.Name.Local != "BugCollection" {
				terminalParseFailure(doc, outcome)
				return
			}
			closed = true
		}
	}
	if !closed || !errorSeen || summary.Classes < 1 || summary.Bugs != seen {
		terminalParseFailure(doc, outcome)
		return
	}
	if outside > 0 {
		doc.Coverage.Units["java_findings_outside_scope"] = outside
		doc.Limitations = append(doc.Limitations, "observations_outside_scope_omitted")
	}
	if n := seen - len(doc.Observations) - outside; n > 0 {
		doc.Coverage.Units["java_findings_location_unmapped"] = n
		doc.Limitations = append(doc.Limitations, "java_findings_location_unmapped")
	}
	processed := map[string]bool{}
	for _, pkg := range summary.Packages {
		for _, c := range pkg.Classes {
			p := selectedSource(names, path.Join(strings.ReplaceAll(pkg.Name, ".", "/"), c.File))
			if p != "" {
				processed[p] = true
			}
		}
	}
	for _, f := range files {
		if processed[f.Path] {
			doc.Coverage.ProcessedPaths = append(doc.Coverage.ProcessedPaths, f.Path)
		}
	}
	if len(processed) < len(files) {
		doc.Limitations = append(doc.Limitations, "java_sources_without_mapped_classes")
	}
	if len(processed) == 0 {
		outcome.Fail(domain.TaskFailed, "java_classes_source_unmapped", obsdoc.ExitConfigurationError)
	}
}

func spotVersionMatches(start xml.StartElement, version *string) bool {
	for _, attribute := range start.Attr {
		if attribute.Name.Local == "version" {
			return version != nil && attribute.Value == *version
		}
	}
	return false
}
