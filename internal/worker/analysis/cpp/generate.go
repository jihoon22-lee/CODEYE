package cpp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/govet"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/obsdoc"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/pack"
	"github.com/jihoon22-lee/CODEYE/internal/worker/executor"
)

// Compile DB generation (docs/ANALYZERS.md#compile-db-생성). When the
// snapshot has no compile_commands.json but a CMake project, one task runs
// the project's CMake configure step on the attempt's workspace copy with
// CMAKE_EXPORT_COMPILE_COMMANDS. Configure executes repository build
// configuration (CMakeLists.txt, try_compile and try_run checks), so the task
// needs the repository-code grant. The build directory is
// .codeye/compile-db/<n> in the workspace copy: snapshots never hold a
// .codeye directory, the restore between tasks keeps hidden directories, no
// analyzer treats it as source, and the headers configure writes there stay
// inside the copy the tools may read. Nothing is built. The C/C++ analyzers
// read the generated database as data through the same mapping and flag
// allowlist as a snapshot database and never run the compiler it names.
// Without the grant, cmake, or a successful configure they keep the
// reduced mode.

const (
	// GenerateTaskKey is the key of the generation task (stage build).
	GenerateTaskKey = "compile-db"
	// GenerateTimeout bounds one configure run.
	GenerateTimeout = 5 * time.Minute
	// MaxGeneratedProjects bounds the CMake projects one task configures.
	MaxGeneratedProjects = 8
	// CMakeTestedSeries is the cmake series the generation was measured
	// with (4.2.3 on Linux).
	CMakeTestedSeries = "4.2"

	generateDir = "compile-db"
	// GeneratedBuildDir is the workspace-relative parent of the build
	// directories.
	GeneratedBuildDir  = ".codeye/compile-db"
	generateRecord     = "record.json"
	generateKind       = "codeye_compile_db_generation"
	generateVersion    = "0.1"
	generateOutLimit   = 1 << 20
	maxCMakeInputs     = 256
	cmakeListsName     = "CMakeLists.txt"
	execCMakeVersion   = "cmake.version"
	execCMakeConfigure = "cmake.configure"
)

// Reasons and limitation codes of the generation.
const (
	// ReasonBuildToolNotConfigured: cmake or the build program its
	// generator needs (make, ninja) is not in the operator's tool path.
	ReasonBuildToolNotConfigured = "build_tool_not_configured"
	// ReasonGenerationFailed: the configure step did not succeed.
	ReasonGenerationFailed = "compile_db_generation_failed"
	// ReasonGeneratedDBMissing: configure succeeded but wrote no readable
	// compile_commands.json (a generator without the export, or no target).
	ReasonGeneratedDBMissing = "compile_db_not_written"

	LimCMakeNotFound         = "cmake_not_found"
	LimGeneratorNotFound     = "cmake_generator_not_found"
	LimGenerated             = "compile_db_generated"
	LimGeneratorCMake        = "compile_db_generator_cmake"
	LimGenerationPartial     = "compile_db_generation_partial"
	LimGenerationNotAllowed  = "compile_db_generation_not_allowed"
	LimGenerationFailed      = "compile_db_generation_failed"
	LimGenerationUnavailable = "compile_db_generation_unavailable"
	// LimNoBuildDescription: no compile DB and no CMake project; other
	// build descriptions (Makefile, configure, Meson) are not configured
	// to produce one.
	LimNoBuildDescription   = "compile_db_build_description_unsupported"
	LimGeneratedIncludes    = "generated_include_dirs_used"
	LimCMakeInputsNotListed = "cmake_inputs_not_enumerated"
)

var cmakeVersionLine = regexp.MustCompile(`^cmake version ([0-9]+\.[0-9]+\.[0-9]+)\S*$`)

// ParseCMakeVersion reads `cmake --version` (first line "cmake version X.Y.Z").
func ParseCMakeVersion(out []byte) (string, bool) {
	line, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n")
	m := cmakeVersionLine.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return "", false
	}
	return m[1], true
}

// GenerationProjects are the CMake projects generation configures for the
// job: none when no C/C++ translation unit is in scope or the snapshot has
// its own compile DB; otherwise every outermost CMakeLists.txt (no
// CMakeLists.txt in an ancestor directory) outside data directories whose
// directory holds an in-scope translation unit, shallowest first, at most
// MaxGeneratedProjects. Manifest and scope only.
func GenerationProjects(job analysis.Job) ([]string, error) {
	l, err := layoutOf(job)
	if err != nil || l.CppFiles == 0 || len(l.Sources) == 0 || len(l.DBs) > 0 {
		return nil, err
	}
	dirs := map[string]bool{}
	for p := range l.Files {
		if path.Base(p) == cmakeListsName && !pack.UnderDataDir(p) {
			dirs[path.Dir(p)] = true
		}
	}
	var out []string
	for d := range dirs {
		outer := true
		for a := d; a != "."; {
			a = path.Dir(a)
			if dirs[a] {
				outer = false
				break
			}
		}
		if !outer {
			continue
		}
		for _, s := range l.Sources {
			if pack.UnderDir(s, d) {
				out = append(out, d)
				break
			}
		}
	}
	sort.Slice(out, func(a, b int) bool {
		da, db := strings.Count(out[a], "/"), strings.Count(out[b], "/")
		if out[a] == "." || out[b] == "." {
			return out[a] == "."
		}
		if da != db {
			return da < db
		}
		return out[a] < out[b]
	})
	if len(out) > MaxGeneratedProjects {
		out = out[:MaxGeneratedProjects]
	}
	return out, nil
}

// GeneratedProject is one configured CMake project.
type GeneratedProject struct {
	// Dir is the snapshot-relative project directory.
	Dir string `json:"dir"`
	// Build is the workspace-relative build directory.
	Build string `json:"build"`
	OK    bool   `json:"ok"`
	// SHA256 and Entries describe the compile_commands.json it wrote.
	SHA256  string `json:"sha256,omitempty"`
	Entries int    `json:"entries,omitempty"`
}

// Generation is the record the generation task leaves in the attempt
// scratch for the analyzers (never uploaded: the task's executions and
// limitations carry what the report shows).
type Generation struct {
	Kind          string             `json:"kind"`
	SchemaVersion string             `json:"schema_version"`
	Snapshot      string             `json:"snapshot_manifest_digest"`
	AttemptID     string             `json:"attempt_id"`
	Status        domain.TaskStatus  `json:"task_status"`
	Reason        string             `json:"reason,omitempty"`
	Generator     string             `json:"generator"`
	Version       string             `json:"generator_version,omitempty"`
	Projects      []GeneratedProject `json:"projects"`
	// Inputs are the CMake files of the snapshot (CMakeLists.txt, *.cmake)
	// with their manifest SHA-256; nil when there are more than 256.
	Inputs      []obsdoc.ConfigFile `json:"inputs"`
	Limitations []string            `json:"limitations"`
}

func generationPath(scratch string) string {
	return filepath.Join(scratch, generateDir, generateRecord)
}

// GenerateConfig is what the generation needs from the node.
type GenerateConfig struct {
	// ToolPath is the operator's --toolchain-path list: cmake, the build
	// program of its generator and the compiler the configure step probes
	// are found there, and it is the configure run's PATH.
	ToolPath string
	Timeout  time.Duration
}

func findIn(dirs []string, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	for _, d := range dirs {
		p := filepath.Join(d, name)
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0) {
			return p
		}
	}
	return ""
}

func toolDirs(list string) []string {
	var out []string
	seen := map[string]bool{}
	for _, d := range filepath.SplitList(list) {
		if !filepath.IsAbs(d) {
			continue
		}
		d = filepath.Clean(d)
		if seen[d] {
			continue
		}
		seen[d] = true
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			out = append(out, d)
		}
	}
	return out
}

// cmakeInputs lists the CMake files of the snapshot for the rule context of
// a generated database (nil when there are more than maxCMakeInputs).
func cmakeInputs(l layout) []obsdoc.ConfigFile {
	var out []obsdoc.ConfigFile
	for p, e := range l.Files {
		if pack.UnderDataDir(p) || path.Base(p) != cmakeListsName && path.Ext(p) != ".cmake" {
			continue
		}
		out = append(out, obsdoc.ConfigFile{Path: p, SHA256: e.SHA256})
	}
	if len(out) > maxCMakeInputs {
		return nil
	}
	sort.Slice(out, func(a, b int) bool { return out[a].Path < out[b].Path })
	if out == nil {
		out = []obsdoc.ConfigFile{}
	}
	return out
}

// Generate runs the generation task. The caller has checked the
// repository-code grant. The record is written whatever the outcome.
func Generate(ctx context.Context, job analysis.Job, runner executor.Runner, cfg GenerateConfig) analysis.TaskResult {
	projects, err := GenerationProjects(job)
	if err != nil || len(projects) == 0 {
		return pack.Failed(analysis.ReasonHandlerError, pack.ReasonUnknownTask)
	}
	if !filepath.IsAbs(job.Scratch) || !filepath.IsAbs(job.Snapshot.WorkspaceDir) {
		return pack.Failed(analysis.ReasonHandlerError, "invalid_job_paths")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = GenerateTimeout
	}
	l, _ := layoutOf(job)
	dir := filepath.Join(job.Scratch, generateDir)
	for _, d := range []string{dir, filepath.Join(dir, "exec"), filepath.Join(dir, "tmp"), filepath.Join(dir, "probe")} {
		if err := os.Mkdir(d, 0o700); err != nil {
			return pack.Failed(analysis.ReasonHandlerError, "scratch_unavailable")
		}
	}
	rec := Generation{Kind: generateKind, SchemaVersion: generateVersion, Snapshot: job.Snapshot.Digest, AttemptID: job.AttemptID,
		Generator: "cmake", Projects: []GeneratedProject{}, Inputs: cmakeInputs(l), Limitations: []string{}}
	var executions []analysis.Execution
	finish := func(status domain.TaskStatus, reason string, lims ...string) analysis.TaskResult {
		rec.Status, rec.Reason = status, reason
		rec.Limitations = sortedUnique(append(rec.Limitations, lims...))
		res := analysis.TaskResult{Status: status, Reason: reason, Executions: executions, Limitations: rec.Limitations}
		b, err := json.Marshal(rec)
		if err != nil || analysis.WriteFileExclusive(generationPath(job.Scratch), b) != nil {
			return analysis.TaskResult{Status: domain.TaskFailed, Reason: analysis.ReasonHandlerError, Executions: executions, Limitations: []string{"record_write_failed"}}
		}
		return res
	}
	dirs := toolDirs(cfg.ToolPath)
	cmake := findIn(dirs, "cmake")
	if cmake == "" {
		return finish(domain.TaskUnsupported, ReasonBuildToolNotConfigured, LimCMakeNotFound)
	}
	generator, program := "", ""
	if p := findIn(dirs, "make"); p != "" {
		generator, program = "Unix Makefiles", p
	} else if p := findIn(dirs, "ninja"); p != "" {
		generator, program = "Ninja", p
	}
	if generator == "" {
		return finish(domain.TaskUnsupported, ReasonBuildToolNotConfigured, LimGeneratorNotFound)
	}
	workspace := job.Snapshot.WorkspaceDir
	roots := []string{workspace}
	if r, err := filepath.EvalSymlinks(workspace); err == nil && r != workspace {
		roots = append(roots, r)
	}
	redact := append(govet.RedactForms(roots), dir)
	env := map[string]string{"PATH": strings.Join(dirs, string(os.PathListSeparator)), "LANG": "C.UTF-8"}
	if runtime.GOOS == "windows" {
		env["SystemRoot"] = os.Getenv("SystemRoot")
	}
	run := func(name, root, cwd string, args []string, timeout time.Duration) (*executor.Result, string) {
		spec := executor.Spec{Executable: cmake, Args: args, WorkspaceRoot: root, Dir: cwd, ProtectedRoots: []string{job.Snapshot.SnapshotDir},
			TempParent: filepath.Join(dir, "tmp"), Env: env, Timeout: timeout, StdoutLimit: generateOutLimit, StderrLimit: generateOutLimit, Redact: redact}
		res := runner.Run(ctx, spec)
		if x, err := analysis.WriteExecution(filepath.Join(dir, "exec"), name, res); err == nil {
			executions = append(executions, x)
		}
		switch {
		case res.Cleanup == executor.CleanupUncertain:
			return &res, ReasonCleanupUncertain
		case ctx.Err() != nil || res.Status == executor.StatusCancelled:
			return &res, ReasonCancelled
		case res.Status == executor.StatusTimedOut:
			return &res, ReasonTimeout
		case res.Status == executor.StatusUnsupported:
			return &res, ReasonExecutionUnsupported
		case res.Status == executor.StatusRejected:
			return &res, ReasonExecutionRejected
		case res.Status != executor.StatusExited:
			return &res, ReasonToolStartFailed
		}
		return &res, ""
	}
	stopped := func(reason string) analysis.TaskResult {
		switch reason {
		case ReasonCancelled:
			return finish(domain.TaskCancelled, reason)
		case ReasonTimeout:
			return finish(domain.TaskTimedOut, reason)
		case ReasonExecutionUnsupported:
			return finish(domain.TaskUnsupported, reason)
		}
		return finish(domain.TaskFailed, reason)
	}
	vres, stop := run(execCMakeVersion, filepath.Join(dir, "probe"), filepath.Join(dir, "probe"), []string{"--version"}, 30*time.Second)
	if stop != "" {
		return stopped(stop)
	}
	version, ok := ParseCMakeVersion(vres.Stdout.Data)
	if !ok || pack.ExitCode(vres) != 0 {
		return finish(domain.TaskFailed, ReasonToolVersion)
	}
	rec.Version = version
	if !strings.HasPrefix(version, CMakeTestedSeries+".") {
		rec.Limitations = append(rec.Limitations, "tool_version_untested")
	}
	failed := 0
	for i, p := range projects {
		lists := govet.JoinRel(p, cmakeListsName)
		b, err := readSnapshot(job.Snapshot.SnapshotDir, lists)
		if e, listed := l.Files[lists]; err != nil || !listed || obsdoc.SHA256Hex(b) != e.SHA256 {
			return finish(domain.TaskFailed, ReasonSnapshotIntegrity)
		}
		build := path.Join(GeneratedBuildDir, strconv.Itoa(i))
		buildAbs := filepath.Join(workspace, filepath.FromSlash(build))
		if err := os.MkdirAll(buildAbs, 0o700); err != nil {
			return finish(domain.TaskFailed, analysis.ReasonHandlerError)
		}
		gp := GeneratedProject{Dir: p, Build: build}
		name := execCMakeConfigure
		if i > 0 {
			name += "." + strconv.Itoa(i)
		}
		// Configure only: the compile DB is written at generate time, so no
		// target is built. FetchContent stays disconnected and the user and
		// system package registries are not read.
		args := []string{"-S", filepath.Join(workspace, filepath.FromSlash(p)), "-B", buildAbs, "-G", generator, "-DCMAKE_MAKE_PROGRAM=" + program,
			"-DCMAKE_EXPORT_COMPILE_COMMANDS=ON", "-DFETCHCONTENT_FULLY_DISCONNECTED=ON",
			"-DCMAKE_FIND_USE_PACKAGE_REGISTRY=OFF", "-DCMAKE_FIND_USE_SYSTEM_PACKAGE_REGISTRY=OFF", "-Wno-dev", "--no-warn-unused-cli"}
		res, stop := run(name, workspace, buildAbs, args, cfg.Timeout)
		if stop != "" {
			rec.Projects = append(rec.Projects, gp)
			return stopped(stop)
		}
		if pack.ExitCode(res) == 0 && pack.StreamsComplete(res) {
			if db, err := readGenerated(filepath.Join(buildAbs, CompileDBName)); err == nil {
				if entries, err := parseDB(db); err == nil && len(entries) > 0 {
					gp.OK, gp.SHA256, gp.Entries = true, obsdoc.SHA256Hex(db), len(entries)
				}
			}
		}
		if !gp.OK {
			failed++
		}
		rec.Projects = append(rec.Projects, gp)
	}
	switch {
	case failed == len(projects):
		reason := ReasonGenerationFailed
		if allExitedZero(executions) {
			reason = ReasonGeneratedDBMissing
		}
		return finish(domain.TaskFailed, reason, LimGeneratorCMake)
	case failed > 0:
		return finish(domain.TaskFailed, ReasonGenerationFailed, LimGenerated, LimGeneratorCMake, LimGenerationPartial)
	}
	return finish(domain.TaskSucceeded, "", LimGenerated, LimGeneratorCMake)
}

func allExitedZero(xs []analysis.Execution) bool {
	for _, x := range xs {
		if x.Result.ExitCode == nil || *x.Result.ExitCode != 0 {
			return false
		}
	}
	return true
}

func readGenerated(p string) ([]byte, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > maxCompileDBBytes {
		return nil, errors.New("compile DB unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxCompileDBBytes+1))
	if err != nil || len(b) > maxCompileDBBytes {
		return nil, errors.New("compile DB unavailable")
	}
	return b, nil
}

func sortedUnique(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// dbSource is the compile DB input of a C/C++ analyzer task: the snapshot's
// databases, the generated ones, or none (reduced mode / not run).
type dbSource struct {
	Generated   bool
	DBs         []string
	Data        map[string][]byte
	ConfigFiles []obsdoc.ConfigFile
	ConfigKnown bool
	Version     string
	// Limitations explain the source (or why there is none).
	Limitations []string
}

// compileDBs picks the analyzer input: the snapshot's compile DBs first,
// otherwise what this attempt's generation task wrote. fail is an integrity
// failure reason ("" when usable).
func compileDBs(job analysis.Job, l layout) (dbSource, string) {
	if len(l.DBs) > 0 {
		data, files, ok := readDBs(job, l)
		if !ok {
			return dbSource{}, ReasonSnapshotIntegrity
		}
		return dbSource{DBs: l.DBs, Data: data, ConfigFiles: files, ConfigKnown: true}, ""
	}
	projects, _ := GenerationProjects(job)
	if len(projects) == 0 {
		return dbSource{Limitations: []string{LimNoBuildDescription}}, ""
	}
	b, err := os.ReadFile(generationPath(job.Scratch))
	if err != nil {
		// The task was not planned (no usable C/C++ analyzer, or a handler
		// chain without it) or did not get to write its record.
		return dbSource{Limitations: []string{LimGenerationUnavailable}}, ""
	}
	var rec Generation
	if json.Unmarshal(b, &rec) != nil || rec.Kind != generateKind || rec.Snapshot != job.Snapshot.Digest || rec.AttemptID != job.AttemptID {
		return dbSource{Limitations: []string{LimGenerationUnavailable}}, ""
	}
	src := dbSource{Generated: true, Data: map[string][]byte{}, Version: rec.Version}
	for _, p := range rec.Projects {
		if !p.OK || domain.ValidateEvidencePath(p.Build) != nil || !underGeneratedBuild(p.Build+"/x") {
			continue
		}
		db, err := readGenerated(filepath.Join(job.Snapshot.WorkspaceDir, filepath.FromSlash(p.Build), CompileDBName))
		if err != nil || obsdoc.SHA256Hex(db) != p.SHA256 {
			return dbSource{}, ReasonSnapshotIntegrity
		}
		key := p.Build + "/" + CompileDBName
		src.DBs = append(src.DBs, key)
		src.Data[key] = db
	}
	if len(src.DBs) == 0 {
		lim := LimGenerationFailed
		switch {
		case slices.Contains(rec.Limitations, LimGenerationNotAllowed):
			lim = LimGenerationNotAllowed
		case rec.Status != domain.TaskFailed:
			// cmake or its build program missing, cancelled, timed out.
			lim = LimGenerationUnavailable
		}
		return dbSource{Limitations: []string{lim}}, ""
	}
	src.Limitations = []string{LimGenerated, LimGeneratorCMake}
	if rec.Status != domain.TaskSucceeded {
		src.Limitations = append(src.Limitations, LimGenerationPartial)
	}
	src.ConfigFiles, src.ConfigKnown = rec.Inputs, rec.Inputs != nil
	if !src.ConfigKnown {
		src.Limitations = append(src.Limitations, LimCMakeInputsNotListed)
	}
	return src, ""
}

// apply records the source on an envelope (after applyCompileDB).
func (src dbSource) apply(doc *obsdoc.Document) {
	doc.Limitations = append(doc.Limitations, src.Limitations...)
	if !src.Generated {
		return
	}
	doc.Target.Properties["compile_db_source"] = "generated"
	doc.Target.Properties["compile_db_generator"] = "cmake"
	if src.Version != "" {
		doc.Target.Properties["compile_db_generator_version"] = src.Version
		doc.RulePack.Selection = append(doc.RulePack.Selection, "compile-db=cmake-"+src.Version)
	}
	if !src.ConfigKnown {
		doc.RulePack.ConfigFiles = nil
	}
}

// underGeneratedBuild reports a workspace path below the generated build
// directories (configured headers of a generated compile DB).
func underGeneratedBuild(rel string) bool { return strings.HasPrefix(rel, GeneratedBuildDir+"/") }
