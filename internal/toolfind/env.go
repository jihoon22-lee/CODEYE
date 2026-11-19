package toolfind

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ToolPrefixRel is the documented CODEYE tool prefix below the user's home
// (docs/ANALYZERS.md#도구-찾기): bin, cpp/bin and go/bin hold executables and
// node/node_modules holds the npm tools.
const ToolPrefixRel = ".local/share/codeye-tools"

// Env is the environment detection looks in. Everything that depends on the
// machine is a field, so tests build a fake home.
type Env struct {
	Home string
	// Path is the PATH directory list.
	Path []string
	// SystemDirs are fixed directories searched for executables after the
	// per-user ones (for example /usr/local/go/bin).
	SystemDirs []string
	// ToolsDir is the CODEYE tool prefix; "" means Home/ToolPrefixRel.
	ToolsDir string
	// RustupHome is the rustup home; "" means Home/.rustup.
	RustupHome string
	// SystemModuleDirs are fixed node_modules directories searched for the npm
	// tools in addition to the tool prefix and the node installation.
	SystemModuleDirs []string
	// Getenv reads the environment; nil uses the process environment.
	Getenv func(string) string
}

// SystemEnv is the environment of this process.
func SystemEnv() Env {
	home, _ := os.UserHomeDir()
	e := Env{Home: home, Path: filepath.SplitList(os.Getenv("PATH")), Getenv: os.Getenv}
	if runtime.GOOS != "windows" {
		e.SystemDirs = []string{"/usr/local/go/bin", "/usr/local/bin", "/usr/bin", "/bin", "/snap/bin"}
		e.SystemModuleDirs = []string{"/usr/local/lib/node_modules", "/usr/lib/node_modules"}
	}
	return e
}

func (e Env) getenv(k string) string {
	if e.Getenv != nil {
		return e.Getenv(k)
	}
	return os.Getenv(k)
}

func (e Env) toolsDir() string {
	if e.ToolsDir != "" {
		return e.ToolsDir
	}
	if e.Home == "" {
		return ""
	}
	return filepath.Join(e.Home, filepath.FromSlash(ToolPrefixRel))
}

func (e Env) rustupHome() string {
	switch {
	case e.RustupHome != "":
		return e.RustupHome
	case e.getenv("RUSTUP_HOME") != "":
		return e.getenv("RUSTUP_HOME")
	case e.Home != "":
		return filepath.Join(e.Home, ".rustup")
	}
	return ""
}

// dir is a searched directory with the label shown as "found in".
type dir struct{ path, label string }

// execDirs are the directories searched for executables, in order: the
// user's PATH, then the per-user install directories, the system ones and the
// CODEYE tool prefix.
func (e Env) execDirs() []dir {
	var out []dir
	seen := map[string]bool{}
	add := func(path, label string) {
		if path == "" || !filepath.IsAbs(path) {
			return
		}
		path = filepath.Clean(path)
		if seen[path] {
			return
		}
		seen[path] = true
		out = append(out, dir{path, label})
	}
	for _, p := range e.Path {
		add(p, SourcePath)
	}
	if e.Home != "" {
		add(filepath.Join(e.Home, ".local", "bin"), "~/.local/bin")
		add(filepath.Join(e.Home, "go", "bin"), "~/go/bin")
	}
	for _, p := range e.SystemDirs {
		add(p, p)
	}
	if e.Home != "" {
		add(filepath.Join(e.Home, ".opencode", "bin"), "~/.opencode/bin")
	}
	if t := e.toolsDir(); t != "" {
		add(filepath.Join(t, "bin"), "codeye-tools/bin")
		add(filepath.Join(t, "cpp", "bin"), "codeye-tools/cpp/bin")
		add(filepath.Join(t, "go", "bin"), "codeye-tools/go/bin")
	}
	return out
}

// moduleDirs are the node_modules directories searched for the npm tools:
// the tool prefix, the installation of the node that was found and the
// system ones.
func (e Env) moduleDirs(nodeExe string) []dir {
	var out []dir
	seen := map[string]bool{}
	add := func(path, label string) {
		if path == "" || !filepath.IsAbs(path) {
			return
		}
		path = filepath.Clean(path)
		if !seen[path] {
			seen[path] = true
			out = append(out, dir{path, label})
		}
	}
	if t := e.toolsDir(); t != "" {
		add(filepath.Join(t, "node", "node_modules"), "codeye-tools/node")
		add(filepath.Join(t, "node_modules"), "codeye-tools")
	}
	if nodeExe != "" {
		if real, err := filepath.EvalSymlinks(nodeExe); err == nil {
			// <prefix>/bin/node with global modules in <prefix>/lib/node_modules.
			add(filepath.Join(filepath.Dir(filepath.Dir(real)), "lib", "node_modules"), "node installation")
		}
	}
	if e.Home != "" {
		add(filepath.Join(e.Home, ".npm-global", "lib", "node_modules"), "~/.npm-global")
	}
	for _, p := range e.SystemModuleDirs {
		add(p, p)
	}
	return out
}

// exeNames are the file names an executable may have on this platform.
func exeNames(name string) []string {
	if runtime.GOOS == "windows" {
		return []string{name + ".exe", name}
	}
	return []string{name}
}

// isExecutable reports a regular, executable file (following symlinks).
func isExecutable(p string) bool {
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	return runtime.GOOS == "windows" || st.Mode().Perm()&0o111 != 0
}

func isRegular(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

// tilde shortens a path under home for messages and labels.
func tilde(home, p string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+string(filepath.Separator))) {
		return "~" + strings.TrimPrefix(p, home)
	}
	return p
}
