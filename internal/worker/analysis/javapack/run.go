package javapack

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/anypack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
	"github.com/jihoon22-lee/CODEYE/internal/worker/javaproject"
	"github.com/jihoon22-lee/CODEYE/internal/worker/source"
)

const (
	maxFileBytes      = 2 << 20
	maxInputBytes     = 64 << 20
	maxFiles          = 4096
	pmdExecution      = "pmd.check"
	javacExecution    = "spotbugs.javac"
	spotbugsExecution = "spotbugs.check"
)

// Select rules about observed behavior; naming and stylistic choices are
// not actionable defects by themselves.
var pmdRules = []string{
	"category/java/errorprone.xml/BrokenNullCheck", "category/java/errorprone.xml/CloseResource",
	"category/java/errorprone.xml/ReturnFromFinallyBlock", "category/java/errorprone.xml/UselessOperationOnImmutable",
	"category/java/errorprone.xml/UseEqualsToCompareStrings", "category/java/errorprone.xml/ImplicitSwitchFallThrough",
	"category/java/bestpractices.xml/PreserveStackTrace", "category/java/bestpractices.xml/UnusedLocalVariable",
	"category/java/multithreading.xml/DontCallThreadRun", "category/java/multithreading.xml/UnsynchronizedStaticFormatter",
}

func (a *Analyzer) Run(ctx context.Context, job analysis.Job, task analysis.TaskSpec, runner executor.Runner) analysis.TaskResult {
	run, failure := anypack.Start(ctx, job, task.Key, a.Plan)
	if failure != nil {
		return *failure
	}
	list, err := targets(ctx, job, a.name)
	if err != nil {
		return pack.Failed(analysis.ReasonHandlerError)
	}
	i := slices.IndexFunc(list, func(t target) bool { return t.task.Spec.Key == task.Key })
	if i < 0 {
		return pack.Failed(pack.ReasonUnknownTask)
	}
	t := list[i]
	doc := a.document(job, t)
	var outcome anypack.Outcome
	selected := selectFiles(job.Snapshot.SnapshotDir, t.files)
	excluded := len(t.files) - len(selected)
	if excluded > 0 {
		doc.Coverage.FilesSkipped = excluded
		doc.Coverage.Units["java_input_excluded"] = excluded
		doc.Limitations = append(doc.Limitations, "java_input_excluded")
	}
	switch {
	case len(selected) == 0:
		outcome.Fail(domain.TaskUnsupported, anypack.ReasonNoAnalyzableFiles, obsdoc.ExitNotRun)
	case !anypack.LinkFreeFiles(run.Workspace, selected):
		outcome.Fail(domain.TaskFailed, anypack.ReasonWorkspaceLink, obsdoc.ExitNotRun)
	case len(anypack.Drift(run.Workspace, selected)) != 0:
		outcome.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitNotRun)
	case a.name == PMD:
		a.runPMD(ctx, runner, run, t, selected, &doc, &outcome)
	default:
		a.runSpotBugs(ctx, runner, run, t, selected, &doc, &outcome)
	}
	doc.Executions = anypack.ExecutionsOf(run.Executions)
	if len(doc.Executions) != 0 {
		if !anypack.LinkFreeFiles(run.Workspace, selected) {
			doc.WorkspaceDrift = anypack.Paths(selected)
		} else {
			doc.WorkspaceDrift = anypack.Drift(run.Workspace, selected)
		}
		if len(doc.WorkspaceDrift) != 0 {
			outcome.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
			for i := range doc.Observations {
				if p := doc.Observations[i].Path; p != nil && slices.Contains(doc.WorkspaceDrift, *p) {
					doc.Observations[i].Location = nil
					doc.Observations[i].LocationState = obsdoc.LocationSourceDrift
				}
			}
		}
	}
	doc.Parse.OK = !outcome.Failed()
	outcome.Apply(&doc, doc.Parse.Records > 0)
	return run.Finish(doc)
}

func (a *Analyzer) document(job analysis.Job, t target) obsdoc.Document {
	d := a.Descriptor()
	component := obsdoc.Component{ID: a.name, Version: "1"}
	doc := obsdoc.New(d.Analyzer(), component, component, t.task.Spec.Key, job.Snapshot.Digest)
	doc.Module.Dir, doc.Patterns, doc.Coverage.FilesInScope = t.task.Module, t.task.Patterns, t.task.FilesInScope
	doc.Tool.Name, doc.Tool.Version, doc.Tool.VersionTested = a.name, anypack.Ptr(a.version), VersionTested(a.name, a.version)
	doc.RulePack = obsdoc.RulePack{ID: "codeye-java-" + a.name, Version: "1",
		RepositoryConfig: obsdoc.RepoConfigOverridden, SourceSuppressions: obsdoc.SuppressHonored, ConfigFiles: []obsdoc.ConfigFile{},
		UnreportedSuppressionRules: []string{}}
	if a.name == PMD {
		doc.RulePack.Selection = pmdRules
		doc.Limitations = append(doc.Limitations, "java_project_classpath_not_loaded")
	} else {
		doc.RulePack.Selection = []string{"-effort:max", "-low"}
		doc.Limitations = append(doc.Limitations, "java_workspace_compilation_only")
	}
	if !doc.Tool.VersionTested {
		doc.Limitations = append(doc.Limitations, "tool_version_untested")
	}
	return doc
}

func selectFiles(snapshotDir string, files []source.Entry) []source.Entry {
	var selected []source.Entry
	var total int64
	for _, f := range files {
		if len(selected) >= maxFiles || f.Size == nil || *f.Size > maxFileBytes || total+*f.Size > maxInputBytes ||
			strings.ContainsAny(f.Path, "\r\n") || anypack.ClassifyText(snapshotDir, f) != anypack.TextUTF8 {
			continue
		}
		selected = append(selected, f)
		total += *f.Size
	}
	return selected
}

func (a *Analyzer) toolSpec(run *anypack.Run, budget time.Duration, args ...string) executor.Spec {
	spec := run.Spec(a.java, a.java.Env(nil), budget, 8<<20, 1<<20, a.commandArgs(false, args...)...)
	spec.Redact = append(spec.Redact, a.jar, filepath.Dir(a.jar), filepath.Dir(filepath.Dir(a.jar)))
	return spec
}

func (a *Analyzer) runPMD(ctx context.Context, runner executor.Runner, run *anypack.Run, t target,
	files []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome) {
	paths := anypack.Paths(files)
	list, err := run.WriteFile("sources.txt", []byte(strings.Join(paths, "\n")+"\n"))
	if err != nil {
		outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitNotRun)
		return
	}
	args := []string{"check", "--file-list", list, "--rulesets", strings.Join(pmdRules, ","), "--format", "json",
		"--encoding", "UTF-8", "--no-cache", "--no-progress", "--no-fail-on-violation", "--threads", "1"}
	if t.module.Release != "" {
		args = append(args, "--use-version", "java-"+t.module.Release)
	}
	if t.module.ReleaseUnresolved || t.module.Release == "" {
		doc.Limitations = append(doc.Limitations, "java_source_version_not_confirmed")
	}
	res := run.Exec(ctx, runner, pmdExecution, a.toolSpec(run, 2*time.Minute, args...))
	outcome.Executions(run.Cancelled, res)
	if !complete(res, outcome) {
		return
	}
	parsePMD(run, files, doc, outcome, res.Stdout.Data)
	if pack.ExitCode(res) != 0 {
		outcome.Fail(domain.TaskFailed, "pmd_failed", obsdoc.ExitExecutionError)
	}
}

func complete(res *executor.Result, outcome *anypack.Outcome) bool {
	if res == nil || outcome.Failed() {
		return false
	}
	if !pack.StreamsComplete(res) {
		outcome.Fail(domain.TaskFailed, anypack.ReasonOutputTruncated, obsdoc.ExitExecutionError)
		return false
	}
	return true
}

func javacArgument(arg string) string {
	return "\"" + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(arg) + "\""
}

func (a *Analyzer) runSpotBugs(ctx context.Context, runner executor.Runner, run *anypack.Run, t target,
	files []source.Entry, doc *obsdoc.Document, outcome *anypack.Outcome) {
	compiler, err := a.compiler()
	if err != nil {
		outcome.Fail(domain.TaskUnsupported, "javac_unavailable", obsdoc.ExitNotRun)
		return
	}
	if t.module.ReleaseUnresolved {
		outcome.Fail(domain.TaskUnsupported, "java_source_version_unresolved", obsdoc.ExitNotRun)
		return
	}
	release := t.module.Release
	jdkMajor, _ := javaproject.Major(strings.Split(a.jdkVersion, ".")[0])
	if release != "" {
		n, _ := javaproject.Major(release)
		if n < 7 || jdkMajor < n {
			outcome.Fail(domain.TaskUnsupported, "java_release_unavailable", obsdoc.ExitNotRun)
			return
		}
	} else {
		doc.Limitations = append(doc.Limitations, "java_source_version_not_confirmed")
	}
	classes, errClasses := run.Mkdir("classes")
	empty, errEmpty := run.Mkdir("empty")
	if errClasses != nil || errEmpty != nil {
		outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitNotRun)
		return
	}
	jars, reason := a.auxiliaryClasspath(run, t.module, doc)
	if reason != "" {
		outcome.Fail(domain.TaskFailed, reason, obsdoc.ExitNotRun)
		return
	}
	classpath := empty
	if len(jars) > 0 {
		if slices.ContainsFunc(jars, func(p string) bool {
			return strings.ContainsRune(p, os.PathListSeparator) || strings.ContainsAny(p, "\r\n")
		}) {
			outcome.Fail(domain.TaskUnsupported, "java_classpath_path_unsupported", obsdoc.ExitNotRun)
			return
		}
		classpath = strings.Join(jars, string(os.PathListSeparator))
	}
	args := []string{"-proc:none", "-implicit:none", "-encoding", "UTF-8", "-g", "-classpath", classpath, "-sourcepath", empty, "-d", classes}
	if release != "" {
		args = append(args, "--release", release)
	}
	compileFiles := files
	mainDir := path.Join(t.module.Dir, "src/main/java")
	if slices.ContainsFunc(t.module.Files, func(f source.Entry) bool { return pack.UnderDir(f.Path, mainDir) }) {
		production := make([]source.Entry, 0, len(t.module.Files))
		for _, f := range t.module.Files {
			if pack.UnderDir(f.Path, mainDir) {
				production = append(production, f)
			}
		}
		original := len(files)
		files = slices.DeleteFunc(slices.Clone(files), func(f source.Entry) bool { return !pack.UnderDir(f.Path, mainDir) })
		if n := original - len(files); n > 0 {
			doc.Coverage.FilesSkipped += n
			doc.Coverage.Units["java_nonproduction_files_excluded"] = n
			doc.Limitations = append(doc.Limitations, "java_nonproduction_files_excluded")
		}
		if len(files) == 0 {
			outcome.Fail(domain.TaskUnsupported, "java_production_sources_unselected", obsdoc.ExitNotRun)
			return
		}
		compileFiles = selectFiles(run.Job.Snapshot.SnapshotDir, production)
		if len(compileFiles) != len(production) {
			outcome.Fail(domain.TaskUnsupported, "java_compile_input_limit", obsdoc.ExitNotRun)
			return
		}
		if !anypack.LinkFreeFiles(run.Workspace, compileFiles) || len(anypack.Drift(run.Workspace, compileFiles)) > 0 {
			outcome.Fail(domain.TaskFailed, anypack.ReasonWorkspaceDrift, obsdoc.ExitExecutionError)
			return
		}
		if len(compileFiles) > len(files) {
			doc.Coverage.Units["java_compiler_context_files"] = len(compileFiles) - len(files)
		}
	}
	// A module descriptor would make javac compile a named module: then
	// every source must be on a module source path and the classpath JARs are
	// not readable from the module (measured: "file should be on source path,
	// or on patch path for module" for every file). The classes are compiled
	// in the unnamed module instead; SpotBugs reads the same bytecode
	// (decision A70).
	descriptors := 0
	for _, file := range compileFiles {
		if path.Base(file.Path) == "module-info.java" {
			descriptors++
			continue
		}
		args = append(args, "./"+file.Path)
	}
	if descriptors == len(compileFiles) {
		outcome.Fail(domain.TaskUnsupported, "java_production_sources_unselected", obsdoc.ExitNotRun)
		return
	}
	if descriptors > 0 {
		doc.Coverage.Units["java_module_descriptors_not_compiled"] = descriptors
		doc.Limitations = append(doc.Limitations, "java_module_descriptor_not_compiled")
	}

	for i := range args {
		args[i] = javacArgument(args[i])
	}
	response, err := run.WriteFile("javac-arguments.txt", []byte(strings.Join(args, "\n")+"\n"))
	if err != nil {
		outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitNotRun)
		return
	}
	compile := run.Exec(ctx, runner, javacExecution, run.Spec(compiler, compiler.Env(nil), 2*time.Minute, 64<<10, 1<<20,
		"-J-Xmx512m", "-J-Dfile.encoding=UTF-8", "@"+response))
	outcome.Executions(run.Cancelled, compile)
	if !complete(compile, outcome) {
		return
	}
	if pack.ExitCode(compile) != 0 {
		outcome.Fail(domain.TaskFailed, "java_compilation_failed", obsdoc.ExitConfigurationError)
		return
	}
	count, err := classFiles(classes)
	if err != nil {
		outcome.Fail(domain.TaskFailed, "java_class_output_limit", obsdoc.ExitExecutionError)
		return
	}
	if count == 0 {
		outcome.Fail(domain.TaskUnsupported, "java_classes_unavailable", obsdoc.ExitConfigurationError)
		return
	}
	doc.Coverage.Units["java_compiled_classes"] = count
	spotArgs := []string{"-xml", "-effort:max", "-low", "-exitcode"}
	if len(jars) > 0 {
		list, err := run.WriteFile("spotbugs-auxiliary-classpath.txt", []byte(strings.Join(jars, "\n")+"\n"))
		if err != nil {
			outcome.Fail(domain.TaskFailed, "scratch_unavailable", obsdoc.ExitExecutionError)
			return
		}
		spotArgs = append(spotArgs, "-auxclasspathFromFile", list)
	}
	spotArgs = append(spotArgs, classes)
	res := run.Exec(ctx, runner, spotbugsExecution, a.toolSpec(run, 2*time.Minute, spotArgs...))
	outcome.Executions(run.Cancelled, res)
	if !complete(res, outcome) {
		return
	}
	context := slices.DeleteFunc(slices.Clone(compileFiles), func(f source.Entry) bool {
		return slices.ContainsFunc(files, func(g source.Entry) bool { return g.Path == f.Path })
	})
	parseSpotBugs(run, files, context, doc, outcome, res.Stdout.Data)
	code := pack.ExitCode(res)
	if code != 0 && (code != 1 || len(doc.Observations) == 0 && doc.Coverage.Units["java_findings_location_unmapped"] == 0 && doc.Coverage.Units["java_findings_outside_scope"] == 0) {
		outcome.Fail(domain.TaskFailed, "spotbugs_failed", obsdoc.ExitExecutionError)
	}
}

func classFiles(dir string) (int, error) {
	count := 0
	var size int64
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		count++
		size += info.Size()
		if !info.Mode().IsRegular() || filepath.Ext(p) != ".class" || count > maxFiles*4 || size > maxInputBytes {
			return fmt.Errorf("unsupported compiler output")
		}
		return nil
	})
	return count, err
}

func observation(doc *obsdoc.Document, resolver *anypack.Resolver, file, rule, category, exec, digest string, line, end int, start, stop int64, ordinals map[string]int) {
	path, location, state := resolver.Resolve(file, line, end, 0, 0)
	msg := doc.Tool.Name + " reported " + rule
	pos := strconv.Itoa(line) + "-" + strconv.Itoa(end)
	key := file + "\x00" + rule + "\x00" + pos
	ord := ordinals[key]
	ordinals[key]++
	doc.Observations = append(doc.Observations, obsdoc.Observation{
		ID: obsdoc.ObservationID(doc.Analyzer.ID, doc.Module.Dir, file, rule, pos, pos, msg, ord), RuleID: rule,
		Category: category, RawMessage: msg, Path: path, Location: location, LocationState: state,
		Related: []obsdoc.Related{}, Unit: file,
		RawRef: obsdoc.RawRef{Execution: exec, Stream: "stdout", StreamSHA256: digest, RecordIndex: doc.Parse.Records, ByteStart: start, ByteEnd: stop},
	})
	doc.Parse.Records++
}

func terminalParseFailure(doc *obsdoc.Document, outcome *anypack.Outcome) {
	doc.Parse.ShapeErrors++
	outcome.Fail(domain.TaskFailed, anypack.ReasonOutputUnparsed, obsdoc.ExitExecutionError)
}
