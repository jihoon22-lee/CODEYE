package architecture

import (
	"context"
	_ "embed"
	"encoding/json"
	"path"
	"regexp"
	"slices"
	"strings"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

//go:embed import_linter.py
var importLinterScript string

const importExecution = "import-linter.check"

var pythonModule = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z_0-9]*(?:\.[a-zA-Z_][a-zA-Z_0-9]*)*$`)

func hasImportConfiguration(job analysis.Job) bool {
	for _, name := range []string{".importlinter", "setup.cfg", "pyproject.toml"} {
		entry, body, err := configuration(job, name)
		if entry.Path == "" {
			continue
		}
		if err != nil || name == ".importlinter" {
			return true
		}
		for line := range strings.SplitSeq(string(body), "\n") {
			header := strings.TrimSpace(line)
			if strings.HasPrefix(header, "[tool.importlinter]") || strings.HasPrefix(header, "[importlinter]") {
				return true
			}
		}
	}
	return false
}

func (a *Analyzer) runImportLinter(ctx context.Context, run *anypack.Run, runner executor.Runner, stage string, files []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome) {
	candidates := map[string]source.Entry{}
	var configurationPaths []string
	for _, name := range []string{".importlinter", "setup.cfg", "pyproject.toml"} {
		e, body, err := configuration(run.Job, name)
		if err != nil {
			outcome.Fail(domain.TaskFailed, "dependency_graph_configuration_unreadable", obsdoc.ExitNotRun)
			return
		}
		if e.Path == "" {
			continue
		}
		if err := writeStage(stage, e.Path, body); err != nil {
			outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitNotRun)
			return
		}
		configurationPaths = append(configurationPaths, e.Path)
		candidates[e.Path] = e
	}
	if len(configurationPaths) == 0 {
		outcome.Fail(domain.TaskUnsupported, "import_linter_configuration_absent", obsdoc.ExitNotRun)
		return
	}
	script, err := run.WriteFile("check-contracts.py", []byte(importLinterScript))
	if err != nil {
		outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitNotRun)
		return
	}
	args := append([]string{"-I", "-B", script}, configurationPaths...)
	spec := run.Spec(a.python, a.python.Env(nil), checkTimeout, checkOutput, 1<<20, args...)
	res := runCommand(ctx, runner, run, importExecution, stage, spec, outcome)
	if res == nil {
		return
	}
	if pack.ExitCode(res) != 0 {
		outcome.Fail(domain.TaskFailed, "import_linter_failed", obsdoc.ExitExecutionError)
		return
	}
	parseImports(run, files, doc, outcome, res.Stdout.Data, candidates)
}

type importCheck struct {
	ID, Kind string
	Kept     *bool
	Warnings int
	Edges    []struct {
		Importer, Imported string
		Line               int
	}
}

func parseImports(run *anypack.Run, files []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome, body []byte, candidates map[string]source.Entry) {
	var result struct {
		Status, Configuration, Root, Version string
		Modules                              []string
		Checks                               []json.RawMessage
	}
	if err := json.Unmarshal(body, &result); err != nil {
		parseFailure(doc, outcome)
		return
	}
	switch result.Status {
	case "configuration_absent", "custom_contract_unsupported", "package_roots_unsupported",
		"package_roots_unavailable", "contract_count_unsupported", "external_contract_unsupported":
		doc.Limitations = append(doc.Limitations, "import_linter_"+result.Status)
		outcome.Fail(domain.TaskUnsupported, "import_linter_"+result.Status, obsdoc.ExitConfigurationError)
		return
	case "contract_invalid", "contract_check_failed":
		outcome.Fail(domain.TaskFailed, "import_linter_"+result.Status, obsdoc.ExitConfigurationError)
		return
	case "checked":
	default:
		parseFailure(doc, outcome)
		return
	}
	config, ok := candidates[result.Configuration]
	if !ok || result.Root != "." && result.Root != "src" || doc.Tool.Version == nil || result.Version != *doc.Tool.Version ||
		len(result.Modules) == 0 || len(result.Checks) == 0 || len(result.Checks) > 128 {
		parseFailure(doc, outcome)
		return
	}
	doc.RulePack.RepositoryConfig = obsdoc.RepoConfigHonored
	doc.RulePack.ConfigFiles = append(doc.RulePack.ConfigFiles, obsdoc.ConfigFile{Path: config.Path, SHA256: config.SHA256})
	fileSet := map[string]bool{}
	for _, f := range files {
		fileSet[f.Path] = true
	}
	modulePath := map[string]string{}
	for _, name := range result.Modules {
		if !pythonModule.MatchString(name) {
			parseFailure(doc, outcome)
			return
		}
		base := path.Join(result.Root, strings.ReplaceAll(name, ".", "/"))
		for _, candidate := range []string{base + ".py", path.Join(base, "__init__.py")} {
			if fileSet[candidate] {
				modulePath[name] = candidate
				doc.Coverage.ProcessedPaths = append(doc.Coverage.ProcessedPaths, candidate)
				break
			}
		}
	}
	slices.Sort(doc.Coverage.ProcessedPaths)
	doc.Coverage.ProcessedPaths = slices.Compact(doc.Coverage.ProcessedPaths)
	if len(doc.Coverage.ProcessedPaths) == 0 {
		parseFailure(doc, outcome)
		return
	}
	resolver := anypack.NewResolver(run.Job.Snapshot.SnapshotDir, files, nil)
	records := jsonRecords{body: body}
	digest := obsdoc.SHA256Hex(body)
	for _, raw := range result.Checks {
		start, end, found := records.offsets(raw)
		var check importCheck
		if !found || json.Unmarshal(raw, &check) != nil || check.Kept == nil || !ruleName.MatchString(check.Kind) ||
			check.Warnings < 0 || len(check.Edges) > 128 {
			parseFailure(doc, outcome)
			return
		}
		doc.Coverage.Units["import_contracts_checked"]++
		if check.Warnings > 0 {
			doc.Coverage.Units["import_contract_warnings"] += check.Warnings
		}
		if *check.Kept {
			continue
		}
		doc.Coverage.Units["import_contracts_broken"]++
		rule := check.Kind
		if ruleName.MatchString(check.ID) {
			rule = check.ID
		}
		var foundLocation bool
		for _, edge := range check.Edges {
			p := modulePath[edge.Importer]
			if p != "" && edge.Line > 0 && modulePath[edge.Imported] != "" {
				observation(doc, resolver, importExecution, digest, p, rule, edge.Line, start, end)
				foundLocation = true
				break
			}
		}
		if !foundLocation {
			doc.Limitations = append(doc.Limitations, "import_contract_witness_unmapped")
			observation(doc, resolver, importExecution, digest, "", rule, 0, start, end)
		}
	}
}
