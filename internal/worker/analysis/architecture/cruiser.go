package architecture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const cruiserExecution = "dependency-cruiser.check"

var managedCruiserRules = []byte(`{"forbidden":[{"name":"no-circular-dependencies","severity":"warn","from":{},"to":{"circular":true}}]}`)
var staticCruiserOptions = map[string]bool{"includeOnly": true, "exclude": true, "doNotFollow": true, "tsPreCompilationDeps": true}

func cruiserRules(body []byte) ([]byte, error) {
	if _, err := domain.CanonicalJSON(body); err != nil {
		return nil, err
	}
	var rules map[string]json.RawMessage
	if err := json.Unmarshal(body, &rules); err != nil || rules == nil {
		return nil, fmt.Errorf("expected a JSON rules object")
	}
	n := 0
	for k, v := range rules {
		switch k {
		case "forbidden", "required":
			var list []struct{ Name string }
			if err := json.Unmarshal(v, &list); err != nil || len(list) > 128 {
				return nil, fmt.Errorf("invalid rule list")
			}
			for _, rule := range list {
				if !ruleName.MatchString(rule.Name) {
					return nil, fmt.Errorf("invalid dependency rule name")
				}
			}
			n += len(list)
		case "allowed":
			var list []json.RawMessage
			if err := json.Unmarshal(v, &list); err != nil || len(list) > 128 {
				return nil, fmt.Errorf("invalid allow list")
			}
			n += len(list)
		case "allowedSeverity":
			var severity string
			if err := json.Unmarshal(v, &severity); err != nil {
				return nil, err
			}
		case "options":
			var options map[string]json.RawMessage
			if err := json.Unmarshal(v, &options); err != nil {
				return nil, err
			}
			for option := range options {
				if !staticCruiserOptions[option] {
					return nil, fmt.Errorf("unsupported dependency resolution option")
				}
			}
			if selected, ok := options["doNotFollow"]; ok {
				// The execution always blocks traversal outside staged sources.
				// Keep declared exclusions that operate inside that boundary.
				var own struct{ Path string }
				if err := json.Unmarshal(selected, &own); err != nil || own.Path == "" || len(own.Path) > 4096 {
					return nil, fmt.Errorf("unsupported doNotFollow options")
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(selected, &fields); err != nil || len(fields) != 1 {
					return nil, fmt.Errorf("unsupported doNotFollow options")
				}
				delete(options, "doNotFollow")
				b, _ := json.Marshal(map[string]string{"path": "^(?:\\.\\.(?:/|$)|node_modules(?:/|$))|(?:" + own.Path + ")"})
				options["doNotFollow"] = b
			}
			b, _ := json.Marshal(options)
			rules[k] = b
		default:
			return nil, fmt.Errorf("unsupported dependency rules property")
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("no dependency rules declared")
	}
	if _, found := rules["options"]; !found {
		rules["options"] = json.RawMessage(`{}`)
	}
	var options map[string]json.RawMessage
	_ = json.Unmarshal(rules["options"], &options)
	if options == nil {
		options = map[string]json.RawMessage{}
	}
	if _, ok := options["doNotFollow"]; !ok {
		options["doNotFollow"] = json.RawMessage(`{"path":"^(?:\\.\\.(?:/|$)|node_modules(?:/|$))"}`)
	}
	rules["options"], _ = json.Marshal(options)
	return json.Marshal(rules)
}

func (a *Analyzer) runCruiser(ctx context.Context, run *anypack.Run, runner executor.Runner, stage string, files []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome) {
	e, body, err := configuration(run.Job, ".dependency-cruiser.json", ".dependency-cruiser.js", ".dependency-cruiser.cjs", ".dependency-cruiser.mjs", ".dependency-cruiser.ts", ".dependency-cruiser.cts", ".dependency-cruiser.mts")
	if err != nil {
		outcome.Fail(domain.TaskFailed, "dependency_graph_configuration_unreadable", obsdoc.ExitNotRun)
		return
	}
	if e.Path == "" {
		body = managedCruiserRules
		doc.RulePack.Selection = append(doc.RulePack.Selection, "no-circular-dependencies")
	} else if e.Path != ".dependency-cruiser.json" {
		outcome.Fail(domain.TaskUnsupported, "dependency_cruiser_executable_configuration", obsdoc.ExitNotRun)
		return
	} else {
		doc.RulePack.RepositoryConfig = obsdoc.RepoConfigHonored
		doc.RulePack.ConfigFiles = append(doc.RulePack.ConfigFiles, obsdoc.ConfigFile{Path: e.Path, SHA256: e.SHA256})
	}
	rules, err := cruiserRules(body)
	if err != nil {
		outcome.Fail(domain.TaskUnsupported, "dependency_cruiser_configuration_unsupported", obsdoc.ExitNotRun)
		return
	}
	config, err := run.WriteFile("dependency-rules.json", rules)
	if err != nil {
		outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitNotRun)
		return
	}
	toolRoot := filepath.Dir(filepath.Dir(filepath.Dir(a.tool.Resolved)))
	if filepath.Base(toolRoot) != "node_modules" {
		outcome.Fail(domain.TaskUnsupported, "dependency_cruiser_layout_unsupported", obsdoc.ExitNotRun)
		return
	}
	// The Node permission model confines reading to the operator tool tree
	// and the staged analysis inputs. No child processes, addons or writes.
	argv := []string{"--permission", "--allow-fs-read=" + toolRoot, "--allow-fs-read=" + run.Dir, a.tool.Executable,
		"--config", config, "--output-type", "json", "--no-cache", "--no-ignore-known", "--", "."}
	env := a.tool.Env(map[string]string{"PREFIX": run.Dir, "npm_config_prefix": run.Dir})
	node, err := anypack.NewTool(a.tool.Interpreter.Executable, nil)
	if err != nil {
		outcome.Fail(domain.TaskUnsupported, pack.ReasonToolUnavailable, obsdoc.ExitNotRun)
		return
	}
	spec := run.Spec(node, env, checkTimeout, checkOutput, 1<<20, argv...)
	spec.Redact = append(spec.Redact, a.tool.Resolved, toolRoot)
	res := runCommand(ctx, runner, run, cruiserExecution, stage, spec, outcome)
	if res == nil {
		return
	}
	if pack.ExitCode(res) != 0 {
		outcome.Fail(domain.TaskFailed, "dependency_cruiser_failed", obsdoc.ExitExecutionError)
		return
	}
	doc.Limitations = append(doc.Limitations, "dependency_resolution_project_sources_only")
	parseCruiser(run, files, doc, outcome, res.Stdout.Data)
}

type cruiseModule struct {
	Source        string
	Dependencies  []struct{ CouldNotResolve bool }
	CouldNotParse bool
}

type cruiseViolation struct {
	From, To, UnresolvedTo, Type string
	Rule                         struct{ Name string }
}

func parseCruiser(run *anypack.Run, files []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome, body []byte) {
	var result struct {
		Modules []cruiseModule
		Summary struct {
			Violations   []json.RawMessage
			TotalCruised int
		}
	}
	if err := json.Unmarshal(body, &result); err != nil || result.Modules == nil || result.Summary.Violations == nil ||
		result.Summary.TotalCruised != len(result.Modules) {
		parseFailure(doc, outcome)
		return
	}
	if len(result.Modules) == 0 {
		outcome.Fail(domain.TaskUnsupported, "dependency_cruiser_no_parsed_sources", obsdoc.ExitConfigurationError)
		return
	}
	selected := map[string]source.Entry{}
	seen := map[string]bool{}
	for _, f := range files {
		selected[f.Path] = f
	}
	for _, m := range result.Modules {
		if _, ok := selected[m.Source]; !ok || seen[m.Source] {
			parseFailure(doc, outcome)
			return
		}
		seen[m.Source] = true
		if m.CouldNotParse {
			outcome.Fail(domain.TaskFailed, "dependency_cruiser_source_unparsed", obsdoc.ExitConfigurationError)
			continue
		}
		doc.Coverage.ProcessedPaths = append(doc.Coverage.ProcessedPaths, m.Source)
		for _, dep := range m.Dependencies {
			if dep.CouldNotResolve {
				doc.Coverage.Units["dependency_unresolved"]++
			}
		}
	}
	if len(seen) != len(files) {
		doc.Limitations = append(doc.Limitations, "dependency_sources_omitted")
	}
	resolver := anypack.NewResolver(run.Job.Snapshot.SnapshotDir, files, nil)
	records := jsonRecords{body: body}
	digest := obsdoc.SHA256Hex(body)
	for _, raw := range result.Summary.Violations {
		start, end, ok := records.offsets(raw)
		var v cruiseViolation
		if !ok || json.Unmarshal(raw, &v) != nil || !ruleName.MatchString(v.Rule.Name) {
			parseFailure(doc, outcome)
			return
		}
		if !seen[v.From] || (v.To != "" && v.To != v.From && !seen[v.To]) {
			doc.Coverage.Units["dependency_violation_unmapped"]++
			continue
		}
		line := cruiseLocation(run.Job.Snapshot.SnapshotDir, selected[v.From], v.UnresolvedTo)
		observation(doc, resolver, cruiserExecution, digest, v.From, v.Rule.Name, line, start, end)
	}
}

// A path reported by the cruiser is not a line citation. Link only a
// distinctive import/require literal with one occurrence in the file.
func cruiseLocation(snapshot string, f source.Entry, target string) int {
	if target == "" || strings.ContainsAny(target, "'\x22\r\n") {
		return 0
	}
	root, err := os.OpenRoot(snapshot)
	if err != nil {
		return 0
	}
	defer root.Close()
	src, err := readEntry(root, f, maxSourceFile)
	if err != nil {
		return 0
	}
	q := regexp.QuoteMeta(target)
	pattern := regexp.MustCompile(`(?:\bfrom\s*|\bimport\s*(?:\(\s*)?|\brequire\s*\(\s*)["']` + q + `["']`)
	position := pattern.FindAllIndex(src, 2)
	if len(position) != 1 {
		return 0
	}
	return 1 + bytes.Count(src[:position[0][0]], []byte("\n"))
}
