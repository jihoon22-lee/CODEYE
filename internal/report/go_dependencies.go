package report

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/gograph"
)

func validateGoGraphs(r Report) error {
	if r.Metrics == nil || len(r.Metrics.GoDependencies) == 0 {
		return nil
	}
	version, _ := strconv.Atoi(r.Builder.Version)
	if version < AnalysisEvidenceSince {
		return invalidf("Go package graph requires its analysis evidence builder")
	}
	artifacts := map[string]Artifact{}
	graphs := map[string]bool{}
	lists := map[string]string{}
	seen := map[string]bool{}
	for _, a := range r.Artifacts {
		artifacts[a.ID] = a
		if a.MediaType == gograph.MediaType && a.Role == "other" && deref(a.Decoder) == gograph.MediaType+";schema_version="+gograph.Version {
			graphs[deref(a.TaskKey)] = true
		}
	}
	for _, e := range r.Executions {
		if e.Name == "go.list" {
			lists[e.TaskKey] = artifacts[deref(e.StdoutArtifactID)].SHA256
		}
	}
	for _, d := range r.Metrics.GoDependencies {
		if seen[d.TaskKey] || d.Snapshot != r.Inputs.ManifestSHA256 || !graphs[d.TaskKey] || d.ListSHA == "" || d.ListSHA != lists[d.TaskKey] {
			return invalidf("Go package graph is not bound to its published execution")
		}
		if err := gograph.Validate(d, nil); err != nil {
			return invalidf("%v", err)
		}
		seen[d.TaskKey] = true
	}
	return nil
}

func (v secView) goDependencyParts(detail bool) []secPart {
	l := v.L
	var parts []secPart
	for _, d := range v.R.Metrics.GoDependencies {
		head := l.Label("graph.title")
		context := d.Module
		if d.Dir != "." {
			context += " · " + d.Dir
		}
		lines := []string{context + " · " + d.Platform + " · " + l.Label("graph.scope."+d.Scope),
			tf(l, "graph.coverage", len(d.Nodes), d.ParsedFiles, d.IgnoredFiles),
			l.Label("graph.status." + d.Status)}
		if d.UnlistedFiles != nil {
			lines[1] += " · " + tf(l, "graph.unlisted_files", *d.UnlistedFiles)
		}
		if d.IncompletePackages+d.UnparsedFiles+d.UnresolvedImports > 0 {
			lines = append(lines, tf(l, "graph.gaps", d.IncompletePackages, d.UnparsedFiles, d.UnresolvedImports))
		}
		if len(d.Limitations) > 0 {
			reasons := make([]string, 0, len(d.Limitations))
			for _, code := range d.Limitations {
				reasons = append(reasons, l.Label("graph.limit."+code))
			}
			lines = append(lines, strings.Join(reasons, " · "))
		}
		part := secPart{Heading: head, Paras: lines}
		in, out, tests := map[string][]gograph.Edge{}, map[string][]gograph.Edge{}, map[string][]gograph.Edge{}
		for _, edge := range d.Edges {
			if edge.Kind == gograph.Production {
				in[edge.To] = append(in[edge.To], edge)
				out[edge.From] = append(out[edge.From], edge)
			} else {
				tests[edge.To] = append(tests[edge.To], edge)
			}
		}
		if cycle := gograph.Cycle(d); len(cycle) > 0 {
			p := secPart{Heading: l.Label("graph.cycle")}
			for _, e := range cycle {
				n := v.graphEdgeNote(e, l.Label("graph.production"))
				p.List = append(p.List, secItem{Text: n.Text, Sources: []sourceCitation{*n.Source}})
			}
			part.Parts = append(part.Parts, p)
		} else if d.Status == "complete" {
			part.Paras = append(part.Paras, l.Label("graph.no_cycle"))
		}
		if detail && len(d.Nodes) > 0 {
			t := &secTable{Head: []string{l.Label("graph.package"), l.Label("graph.incoming"), l.Label("graph.outgoing"), l.Label("graph.tests")}, DetailGroup: "go-graph-detail"}
			for n, pkg := range d.Nodes {
				name := pkg.Import
				t.Rows = append(t.Rows, []secCell{code(name), num(len(in[name])), num(len(out[name])), num(len(distinctTestConsumers(tests[name])))})
				row := secTableDetail{ID: fmt.Sprintf("go-package-%s-%d", d.TaskKey, n+1), Label: name, Text: l.Label("graph.candidate_scope")}
				for _, edge := range in[name] {
					n := v.graphEdgeNote(edge, l.Label("graph.incoming"))
					n.Text = strings.TrimPrefix(edge.From, d.Module+"/")
					row.Notes = append(row.Notes, n)
				}
				for _, edge := range out[name] {
					n := v.graphEdgeNote(edge, l.Label("graph.outgoing"))
					n.Text = strings.TrimPrefix(edge.To, d.Module+"/")
					row.Notes = append(row.Notes, n)
				}
				for _, edge := range tests[name] {
					n := v.graphEdgeNote(edge, l.Label("graph."+edge.Kind))
					n.Text = strings.TrimPrefix(edge.From, d.Module+"/")
					row.Notes = append(row.Notes, n)
				}
				for _, edge := range gograph.Indirect(in, name) {
					row.Notes = append(row.Notes, v.graphEdgeNote(edge, l.Label("graph.indirect")))
				}
				t.Details = append(t.Details, row)
			}
			part.Table = t
		}
		parts = append(parts, part)
	}
	return parts
}

func (v secView) graphEdgeNote(edge gograph.Edge, label string) inspectionNote {
	place := &ObservationLocation{Path: edge.Witness.Path, StartLine: edge.Witness.Line, EndLine: edge.Witness.Line}
	source := v.inspectionCitation(place, nil)
	return inspectionNote{Label: label, Text: edge.From + " → " + edge.To, Source: &source}
}

func distinctTestConsumers(edges []gograph.Edge) map[string]bool {
	out := make(map[string]bool)
	for _, edge := range edges {
		out[edge.From] = true
	}
	return out
}
