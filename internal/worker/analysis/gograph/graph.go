// Package gograph records direct imports of source files selected by one
// module's existing go list execution.
package gograph

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

const (
	MediaType    = "application/vnd.codeye.go-package-graph+json"
	Version      = "0.1"
	MaxBytes     = 4 << 20
	MaxPackages  = 1024
	MaxFiles     = 12000
	MaxBytesRead = 64 << 20
	MaxFileBytes = 1 << 20
	MaxEdges     = 12000
	Production   = "production"
	InternalTest = "internal_test"
	ExternalTest = "external_test"
)

type Document struct {
	Version            string   `json:"schema_version"`
	TaskKey            string   `json:"task_key"`
	Snapshot           string   `json:"snapshot_manifest_digest"`
	ListSHA            string   `json:"go_list_sha256"`
	Module             string   `json:"module"`
	Dir                string   `json:"module_dir"`
	Platform           string   `json:"platform"`
	Scope              string   `json:"scope"`
	Status             string   `json:"status"`
	ParsedFiles        int      `json:"parsed_files"`
	IgnoredFiles       int      `json:"ignored_variant_files"`
	UnlistedFiles      *int     `json:"unlisted_files,omitempty"`
	IncompletePackages int      `json:"incomplete_packages"`
	UnresolvedImports  int      `json:"unresolved_internal_imports"`
	UnparsedFiles      int      `json:"unparsed_files"`
	Limitations        []string `json:"limitations"`
	Nodes              []Node   `json:"packages"`
	Edges              []Edge   `json:"edges"`
}

type Node struct {
	Import string `json:"import_path"`
	Dir    string `json:"directory"`
}

type Edge struct {
	From    string  `json:"from"`
	To      string  `json:"to"`
	Kind    string  `json:"kind"`
	Witness Witness `json:"witness"`
}

type Witness struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	SHA  string `json:"blob_sha256"`
}

// ListedPackage contains only snapshot-relative files named by go list.
type ListedPackage struct {
	Node
	Production, InternalTest, ExternalTest []string
	Ignored                                int
	Incomplete                             bool
}

type ReadFile func(string) ([]byte, string, bool)

func Create(ctx context.Context, d Document, packages []ListedPackage, read ReadFile) Document {
	d.Version = Version
	d.Nodes, d.Edges = []Node{}, []Edge{}
	nodes := map[string]bool{}
	missing := map[[3]string]bool{}
	edges := map[[3]string]bool{}
	limited := len(packages) > MaxPackages
	if limited {
		packages = packages[:MaxPackages]
	}
	for _, pkg := range packages {
		d.Nodes = append(d.Nodes, pkg.Node)
		nodes[pkg.Import] = true
		d.IgnoredFiles += pkg.Ignored
		if pkg.Incomplete {
			d.IncompletePackages++
		}
	}
	readBytes, readFiles := 0, 0
files:
	for _, pkg := range packages {
		for _, group := range []struct {
			kind  string
			files []string
		}{{Production, pkg.Production}, {InternalTest, pkg.InternalTest}, {ExternalTest, pkg.ExternalTest}} {
			for _, p := range group.files {
				if ctx.Err() != nil || readFiles >= MaxFiles || readBytes > MaxBytesRead-MaxFileBytes || len(edges) >= MaxEdges {
					limited = true
					break files
				}
				readFiles++
				body, sha, ok := read(p)
				if !ok {
					d.UnparsedFiles++
					continue
				}
				readBytes += len(body)
				fset := token.NewFileSet()
				f, err := parser.ParseFile(fset, p, body, parser.ImportsOnly)
				if err != nil {
					d.UnparsedFiles++
					continue
				}
				d.ParsedFiles++
				for _, spec := range f.Imports {
					target, err := strconv.Unquote(spec.Path.Value)
					if err != nil || !underModule(target, d.Module) {
						continue
					}
					key := [3]string{pkg.Import, target, group.kind}
					if !nodes[target] {
						if len(missing) < MaxEdges {
							missing[key] = true
						} else {
							limited = true
						}
						continue
					}
					if edges[key] {
						continue
					}
					if len(edges) >= MaxEdges {
						limited = true
						continue
					}
					line := fset.PositionFor(spec.Path.Pos(), false).Line
					edges[key] = true
					d.Edges = append(d.Edges, Edge{pkg.Import, target, group.kind, Witness{p, line, sha}})
				}
			}
		}
	}
	d.UnresolvedImports = len(missing)
	for _, note := range []struct {
		ok   bool
		code string
	}{{limited, "graph_collection_limited"}, {d.IncompletePackages > 0, "go_list_packages_incomplete"}, {d.UnparsedFiles > 0, "go_import_files_unreadable"},
		{d.UnresolvedImports > 0, "go_internal_targets_unlisted"}, {d.UnlistedFiles != nil && *d.UnlistedFiles > 0, "go_candidate_files_unlisted"}, {d.Scope == "selected_packages", "go_package_scope_limited"}} {
		if note.ok {
			d.Limitations = append(d.Limitations, note.code)
		}
	}
	d.Limitations = append([]string{}, d.Limitations...)
	slices.Sort(d.Limitations)
	d.Limitations = slices.Compact(d.Limitations)
	d.Status = "complete"
	if len(d.Nodes) == 0 || d.ParsedFiles == 0 {
		d.Status = "unavailable"
	} else if len(d.Limitations) > 0 {
		d.Status = "partial"
	}
	slices.SortFunc(d.Edges, compareEdges)
	return d
}

func underModule(p, module string) bool { return p == module || strings.HasPrefix(p, module+"/") }

func validImport(value string) bool {
	return value != "" && len(value) <= 512 && value != "." && !strings.HasPrefix(value, "/") && path.Clean(value) == value &&
		strings.IndexAny(value, ":@\\\"'`") < 0 && strings.IndexFunc(value, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

func compareEdges(a, b Edge) int {
	return cmp.Or(cmp.Compare(a.From, b.From), cmp.Compare(a.To, b.To), cmp.Compare(a.Kind, b.Kind))
}

func Validate(d Document, lookup func(string) (string, bool)) error {
	if d.Version != Version || d.Scope != "all_module_packages" && d.Scope != "selected_packages" ||
		d.Status != "complete" && d.Status != "partial" && d.Status != "unavailable" ||
		!validImport(d.Module) || d.Dir != "." && domain.ValidateEvidencePath(d.Dir) != nil ||
		d.Platform == "" || len(d.Platform) > 80 || domain.ValidateEvidencePath(d.Platform) != nil ||
		len(d.Nodes) > MaxPackages || len(d.Edges) > MaxEdges || d.ParsedFiles < 0 || d.ParsedFiles > MaxFiles ||
		d.IgnoredFiles < 0 || d.IncompletePackages < 0 || d.IncompletePackages > len(d.Nodes) || d.UnresolvedImports < 0 || d.UnresolvedImports > MaxEdges ||
		d.UnlistedFiles != nil && (*d.UnlistedFiles < 0 || slices.Contains(d.Limitations, "go_list_incomplete") ||
			(*d.UnlistedFiles > 0) != slices.Contains(d.Limitations, "go_candidate_files_unlisted")) ||
		d.UnlistedFiles == nil && slices.Contains(d.Limitations, "go_candidate_files_unlisted") ||
		d.UnparsedFiles < 0 || d.UnparsedFiles+d.ParsedFiles > MaxFiles || len(d.Limitations) > 16 ||
		d.Limitations == nil || d.Nodes == nil || d.Edges == nil ||
		(d.Status == "unavailable") != (len(d.Nodes) == 0 || d.ParsedFiles == 0) ||
		d.Status == "complete" && (len(d.Limitations) != 0 || d.Scope != "all_module_packages" || d.UnresolvedImports+d.UnparsedFiles+d.IncompletePackages > 0) ||
		d.Status == "partial" && len(d.Limitations) == 0 {
		return fmt.Errorf("invalid Go package graph")
	}
	for i, limit := range d.Limitations {
		switch limit {
		case "graph_collection_limited", "go_list_packages_incomplete", "go_import_files_unreadable",
			"go_internal_targets_unlisted", "go_candidate_files_unlisted", "go_package_scope_limited", "go_list_incomplete", "go_list_rows_unverified", "go_workspace_drift":
		default:
			return fmt.Errorf("invalid Go package graph limitation")
		}
		if i > 0 && d.Limitations[i-1] >= limit {
			return fmt.Errorf("invalid Go package graph limitation order")
		}
	}
	nodes := map[string]string{}
	for i, n := range d.Nodes {
		if !validImport(n.Import) || !underModule(n.Import, d.Module) ||
			n.Dir != "." && domain.ValidateEvidencePath(n.Dir) != nil || d.Dir != "." && n.Dir != d.Dir && !strings.HasPrefix(n.Dir, d.Dir+"/") ||
			i > 0 && d.Nodes[i-1].Import >= n.Import {
			return fmt.Errorf("invalid graph package")
		}
		expected := d.Dir
		if n.Import != d.Module {
			expected = path.Join(d.Dir, strings.TrimPrefix(n.Import, d.Module+"/"))
		}
		if expected != n.Dir {
			return fmt.Errorf("graph package directory and import path disagree")
		}
		nodes[n.Import] = n.Dir
	}
	for i, e := range d.Edges {
		w := e.Witness
		if nodes[e.From] == "" || nodes[e.To] == "" || !slices.Contains([]string{Production, InternalTest, ExternalTest}, e.Kind) ||
			domain.ValidateEvidencePath(w.Path) != nil || path.Dir(w.Path) != nodes[e.From] || !strings.HasSuffix(w.Path, ".go") ||
			strings.HasSuffix(w.Path, "_test.go") == (e.Kind == Production) || w.Line < 1 || !digest(w.SHA) ||
			i > 0 && compareEdges(d.Edges[i-1], e) >= 0 {
			return fmt.Errorf("invalid graph import edge")
		}
		if lookup != nil {
			if sha, ok := lookup(w.Path); !ok || sha != w.SHA {
				return fmt.Errorf("graph witness is outside its snapshot")
			}
		}
	}
	return nil
}

func digest(s string) bool {
	return len(s) == 64 && strings.IndexFunc(s, func(r rune) bool { return (r < '0' || r > '9') && (r < 'a' || r > 'f') }) < 0
}

func Decode(body []byte, task, snapshot, listSHA string, lookup func(string) (string, bool)) (Document, error) {
	d := Document{}
	if len(body) == 0 || len(body) > MaxBytes {
		return d, fmt.Errorf("Go graph artifact size")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&d); err != nil {
		return d, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF || !digest(snapshot) || !digest(listSHA) ||
		d.TaskKey != task || d.Snapshot != snapshot || d.ListSHA != listSHA {
		return d, fmt.Errorf("Go graph execution binding")
	}
	return d, Validate(d, lookup)
}

// Cycle gives one deterministic directed production cycle from this snapshot
// and configuration, as exact direct import edges.
func Cycle(d Document) []Edge {
	out := map[string][]Edge{}
	for _, e := range d.Edges {
		if e.Kind == Production {
			out[e.From] = append(out[e.From], e)
		}
	}
	mark := map[string]int{}
	var route, cycle []Edge
	var visit func(string) bool
	visit = func(node string) bool {
		mark[node] = 1
		for _, edge := range out[node] {
			switch mark[edge.To] {
			case 0:
				route = append(route, edge)
				if visit(edge.To) {
					return true
				}
				route = route[:len(route)-1]
			case 1:
				index := slices.IndexFunc(route, func(e Edge) bool { return e.From == edge.To })
				if index < 0 {
					index = len(route)
				}
				cycle = append(slices.Clone(route[index:]), edge)
				return true
			}
		}
		mark[node] = 2
		return false
	}
	for _, n := range d.Nodes {
		if mark[n.Import] == 0 && visit(n.Import) {
			break
		}
	}
	return cycle
}

// Indirect returns one two-hop import witness from an indirect consumer.
func Indirect(incoming map[string][]Edge, focus string) []Edge {
	direct := map[string]bool{focus: true}
	for _, edge := range incoming[focus] {
		direct[edge.From] = true
	}
	for _, last := range incoming[focus] {
		for _, first := range incoming[last.From] {
			if !direct[first.From] {
				return []Edge{first, last}
			}
		}
	}
	return nil
}
