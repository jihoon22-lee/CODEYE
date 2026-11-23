package toolfind

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultToolchainPath is the agent's --toolchain-path when the
// configuration names none (agent.toolchain_path).
const DefaultToolchainPath = "/usr/bin:/bin"

// ToolchainPath is what the agent's toolchain path offers the tasks that
// find their programs there rather than through agent.tools: the C/C++
// compile DB generation (cmake with make or ninja, and the compilers its
// configure step probes), the CMake test suites, and the C compiler and
// archiver of Cargo build scripts (docs/ANALYZERS.md#도구-경로).
type ToolchainPath struct {
	Path     string             `json:"path"`
	Programs []ToolchainProgram `json:"programs"`
}

// ToolchainProgram is one program looked up in the toolchain path. Only its
// presence is checked: nothing is executed.
type ToolchainProgram struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Path   string `json:"path,omitempty"`
	// UsedFor names the tasks that need it.
	UsedFor []string `json:"used_for"`
}

// Purposes of the toolchain-path programs.
const (
	UseCompileDBGeneration = "compile_db_generation"
	UseCMakeTests          = "cmake_tests"
	UseRustBuildScripts    = "rust_build_scripts"
)

var toolchainPrograms = []struct {
	name string
	used []string
}{
	{"cmake", []string{UseCompileDBGeneration, UseCMakeTests}},
	{"make", []string{UseCompileDBGeneration, UseCMakeTests}},
	{"ninja", []string{UseCompileDBGeneration, UseCMakeTests}},
	{"cc", []string{UseCompileDBGeneration, UseCMakeTests, UseRustBuildScripts}},
	{"c++", []string{UseCompileDBGeneration, UseCMakeTests}},
	{"ar", []string{UseRustBuildScripts}},
}

// LookToolchainPath checks the programs of list ("" is the default path;
// on Windows, where there is no default, "" checks nothing).
func LookToolchainPath(list string) *ToolchainPath {
	if list == "" {
		if runtime.GOOS == "windows" {
			return nil
		}
		list = DefaultToolchainPath
	}
	var dirs []string
	for _, d := range filepath.SplitList(list) {
		if filepath.IsAbs(d) {
			dirs = append(dirs, filepath.Clean(d))
		}
	}
	out := &ToolchainPath{Path: list, Programs: []ToolchainProgram{}}
	for _, p := range toolchainPrograms {
		prog := ToolchainProgram{Name: p.name, Status: NotFound, UsedFor: p.used}
		name := p.name
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		for _, d := range dirs {
			f := filepath.Join(d, name)
			if st, err := os.Stat(f); err == nil && st.Mode().IsRegular() && (runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0) {
				prog.Status, prog.Path = Found, f
				break
			}
		}
		out.Programs = append(out.Programs, prog)
	}
	return out
}

// Program returns the entry named name.
func (t *ToolchainPath) Program(name string) (ToolchainProgram, bool) {
	if t == nil {
		return ToolchainProgram{}, false
	}
	for _, p := range t.Programs {
		if p.Name == name {
			return p, true
		}
	}
	return ToolchainProgram{}, false
}

func (t *ToolchainPath) found(name string) bool {
	p, ok := t.Program(name)
	return ok && p.Status == Found
}

// CompileDBGenerationReady reports whether the C/C++ compile DB generation
// can run: cmake, make or ninja, and a C or C++ compiler.
func (t *ToolchainPath) CompileDBGenerationReady() bool {
	return t.found("cmake") && (t.found("make") || t.found("ninja")) && (t.found("cc") || t.found("c++"))
}
