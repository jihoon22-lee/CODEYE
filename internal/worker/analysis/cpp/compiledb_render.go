package cpp

import (
	"bytes"
	"encoding/json"
	"errors"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// RenderedDriver is the driver name the tools see: "clang++" for C++
// (a C++ driver name, a C++ extension or -x c++), otherwise "clang". The
// entry's own driver name is recorded but never rendered: its prefix or
// suffix would select a target, a driver mode or configuration files.
func (u Unit) RenderedDriver() string {
	lang := ""
	for _, a := range u.Args {
		if a.Flag == "-x" {
			lang = a.Value
		}
	}
	if strings.HasPrefix(lang, "c++") || (lang == "" && (cxxDriver(u.Driver) || cxxExt[strings.ToLower(path.Ext(u.Rel))])) {
		return "clang++"
	}
	return "clang"
}

// Render returns the argv of a unit for the workspace: driver, kept
// arguments with absolute workspace paths, then the main file. Every
// positional (the main file, separate include paths) is absolute, so no
// repository path can be read as an option.
func (u Unit) Render(workspace string) []string {
	abs := func(rel string) string {
		if rel == "." {
			return workspace
		}
		return filepath.Join(workspace, filepath.FromSlash(rel))
	}
	out := []string{u.RenderedDriver()}
	for _, a := range u.Args {
		switch {
		case a.Path != "" && a.Joined:
			out = append(out, a.Flag+abs(a.Path))
		case a.Path != "":
			out = append(out, a.Flag, abs(a.Path))
		case a.Value != "":
			out = append(out, a.Flag, a.Value)
		default:
			out = append(out, a.Flag)
		}
	}
	return append(out, abs(u.Rel))
}

// File is the absolute workspace path of the unit's main file.
func (u Unit) File(workspace string) string {
	return filepath.Join(workspace, filepath.FromSlash(u.Rel))
}

// RenderDB writes the CODEYE compile DB the tools read: one entry per unit,
// directory = workspace root, every path absolute in the workspace.
func RenderDB(workspace string, units []Unit) ([]byte, error) {
	type out struct {
		Directory string   `json:"directory"`
		File      string   `json:"file"`
		Arguments []string `json:"arguments"`
	}
	if !filepath.IsAbs(workspace) {
		return nil, errors.New("workspace must be absolute")
	}
	list := make([]out, 0, len(units))
	for _, u := range units {
		if !u.safe() {
			return nil, errors.New("unsafe compile DB unit")
		}
		argv := u.Render(workspace)
		for _, a := range argv[1:] {
			if strings.HasPrefix(a, "@") || (!strings.HasPrefix(a, "-") && !filepath.IsAbs(a) && !keepLanguages[a]) {
				return nil, errors.New("unsafe compile DB argument")
			}
		}
		list = append(list, out{Directory: workspace, File: u.File(workspace), Arguments: argv})
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(list); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Std summarizes the -std values of the units ("c99,c++17"), for target
// properties.
func Std(units []Unit) string {
	set := map[string]bool{}
	for _, u := range units {
		for _, a := range u.Args {
			if strings.HasPrefix(a.Flag, "-std=") {
				set[a.Flag[5:]] = true
			}
		}
	}
	var out []string
	for s := range set {
		out = append(out, s)
	}
	sort.Strings(out)
	s := strings.Join(out, ",")
	if len(s) > 256 || slices.ContainsFunc(out, func(v string) bool { return strings.ContainsAny(v, "/\\") }) {
		return ""
	}
	return s
}
