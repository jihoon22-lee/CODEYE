package jsts

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// A repository tsconfig chain is untrusted input (docs/43 §3.2). Options
// that name host paths or make tsc write are checked before any tool runs;
// the adapters refuse the task and record only option names.

var (
	// writeAlways write regardless of the adapter's command-line overrides
	// (generateTrace wrote trace.json and types.json under --noEmit,
	// measured with TypeScript 6.0.3) or have no command-line off value.
	writeAlways = []string{"generateTrace", "generateCpuProfile"}
	// emitPaths are inert under --noEmit --incremental false --composite
	// false (measured: a canary directory they named stayed empty); one
	// leaving the snapshot is still refused.
	emitPaths = []string{"outDir", "declarationDir", "outFile", "tsBuildInfoFile"}
	// readPaths and readPathLists name host paths tsc reads from.
	readPaths     = []string{"baseUrl", "rootDir"}
	readPathLists = []string{"rootDirs", "typeRoots"}
	// topLists are the top-level file selections.
	topLists = []string{"files", "include", "exclude"}
)

// isAbs reports an absolute POSIX, Windows drive or UNC path.
func isAbs(p string) bool {
	p = strings.ReplaceAll(p, `\`, "/")
	return strings.HasPrefix(p, "/") || (len(p) > 1 && p[1] == ':' && unicode.IsLetter(rune(p[0])))
}

func (c *ConfigChain) outside(option string) {
	c.Outside = true
	c.OutsideOptions = append(c.OutsideOptions, option)
}

// staticDir is the snapshot-relative directory part of a path pattern
// before its first glob segment.
func staticDir(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if strings.ContainsAny(s, "*?[{") {
			return path.Clean(strings.Join(segs[:i], "/") + "/.")
		}
	}
	return p
}

// resolve maps a config path value to a snapshot path: "${configDir}" is the
// directory of the config being compiled (TypeScript 5.5+), other relative
// values are relative to dir. false when absolute or leaving the snapshot.
func resolve(dir, configDir, v string) (string, bool) {
	v = strings.ReplaceAll(v, `\`, "/")
	if rest, ok := strings.CutPrefix(v, "${configDir}"); ok {
		dir, v = configDir, "./"+strings.TrimPrefix(rest, "/")
	}
	if isAbs(v) {
		return "", false
	}
	j := path.Clean(path.Join(dir, v))
	if j == ".." || strings.HasPrefix(j, "../") {
		return "", false
	}
	return j, true
}

func strList(raw json.RawMessage) []string {
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return []string{one}
	}
	var many []string
	_ = json.Unmarshal(raw, &many)
	return many
}

// set reports a present, non-null, non-empty, non-false option value.
func set(raw json.RawMessage) bool {
	s := strings.TrimSpace(string(raw))
	return s != "" && s != "null" && s != "false" && s != `""` && s != "[]"
}

// checkOptions records the path and write options of one config file
// (JSONC already stripped) in the chain.
func (c *ConfigChain) checkOptions(b []byte, dir, configDir string) error {
	var raw struct {
		Top             map[string]json.RawMessage
		CompilerOptions map[string]json.RawMessage `json:"compilerOptions"`
	}
	if err := json.Unmarshal(b, &raw.Top); err != nil {
		return err
	}
	if co, ok := raw.Top["compilerOptions"]; ok && set(co) {
		if err := json.Unmarshal(co, &raw.CompilerOptions); err != nil {
			return err
		}
	}
	check := func(option, base, v string) bool {
		j, ok := resolve(base, configDir, v)
		if !ok {
			return false
		}
		c.Paths = append(c.Paths, staticDir(j))
		return true
	}
	for _, name := range topLists {
		for _, v := range strList(raw.Top[name]) {
			if v != "" && !check(name, dir, v) {
				c.outside(name)
			}
		}
	}
	co := raw.CompilerOptions
	for _, name := range writeAlways {
		if set(co[name]) {
			c.WriteOptions = append(c.WriteOptions, name)
		}
	}
	for _, name := range emitPaths {
		for _, v := range strList(co[name]) {
			if v != "" && !check(name, dir, v) {
				c.WriteOptions = append(c.WriteOptions, name)
			}
		}
	}
	for _, name := range append(append([]string{}, readPaths...), readPathLists...) {
		for _, v := range strList(co[name]) {
			if v != "" && !check(name, dir, v) {
				c.outside(name)
			}
		}
	}
	// types are package names; a relative or absolute entry is a path.
	for _, v := range strList(co["types"]) {
		if (strings.HasPrefix(v, ".") || isAbs(v) || strings.HasPrefix(v, "${configDir}")) && !check("types", dir, v) {
			c.outside("types")
		}
	}
	// paths are relative to this file's baseUrl, or to its directory.
	base := dir
	if bu := strList(co["baseUrl"]); len(bu) == 1 && bu[0] != "" {
		if j, ok := resolve(dir, configDir, bu[0]); ok {
			base = j
		}
	}
	var mapping map[string][]string
	if set(co["paths"]) && json.Unmarshal(co["paths"], &mapping) == nil {
		keys := make([]string, 0, len(mapping))
		for k := range mapping {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for _, v := range mapping[k] {
				if v != "" && !check("paths", base, v) {
					c.outside("paths")
				}
			}
		}
	}
	return nil
}

// Policy reasons of a refused tsconfig (skipped_policy), shared by tsc and
// the type-aware ESLint task.
const (
	ReasonWriteOption   = "tsconfig_write_option"
	ReasonConfigOutside = "tsconfig_outside_snapshot"
)

// ChainPolicy refuses a chain that names a write option or a host path,
// or whose paths reach outside the workspace copy through a symlink; it
// returns the reason and the option names ("" when the chain may run).
func ChainPolicy(c ConfigChain, workspace string) (string, []string) {
	switch {
	case len(c.WriteOptions) > 0:
		return ReasonWriteOption, c.WriteOptions
	case c.Outside:
		return ReasonConfigOutside, c.OutsideOptions
	case PathsLeaveWorkspace(workspace, c.Paths):
		return ReasonConfigOutside, []string{"symlink"}
	}
	return "", nil
}

// CheckEffective checks the effective configuration `tsc --showConfig`
// printed for the config in snapshot directory dir, before the check runs:
// the same path and write options as the chain (paths there are relative to
// the config directory; workspace paths are redacted, hence relative), and
// the adapter's overrides in force (noEmit true, incremental and composite
// false). It returns the offending option names.
func CheckEffective(shown []byte, dir string) (write, outside []string, err error) {
	var c ConfigChain
	if err := c.checkOptions(shown, dir, dir); err != nil {
		return nil, nil, err
	}
	var raw struct {
		CompilerOptions map[string]json.RawMessage `json:"compilerOptions"`
	}
	if err := json.Unmarshal(shown, &raw); err != nil {
		return nil, nil, err
	}
	co := raw.CompilerOptions
	if strings.TrimSpace(string(co["noEmit"])) != "true" {
		c.WriteOptions = append(c.WriteOptions, "noEmit")
	}
	for _, name := range []string{"incremental", "composite"} {
		if set(co[name]) {
			c.WriteOptions = append(c.WriteOptions, name)
		}
	}
	return uniq(c.WriteOptions), uniq(c.OutsideOptions), nil
}

func uniq(list []string) []string {
	if len(list) == 0 {
		return nil
	}
	sort.Strings(list)
	return slicesCompact(list)
}

func slicesCompact(list []string) []string {
	out := list[:1]
	for _, s := range list[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// PathsLeaveWorkspace reports a chain path whose existing part resolves,
// through symlinks, outside the workspace copy. Snapshots never hold
// symlinks (link_not_followed), so this guards a changed workspace.
func PathsLeaveWorkspace(workspace string, paths []string) bool {
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return true
	}
	for _, p := range paths {
		cur := filepath.Join(workspace, filepath.FromSlash(p))
		for {
			if _, err := os.Lstat(cur); err == nil {
				break
			}
			up := filepath.Dir(cur)
			if up == cur {
				break
			}
			cur = up
		}
		r, err := filepath.EvalSymlinks(cur)
		if err != nil {
			return true
		}
		if rel, err := filepath.Rel(root, r); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return true
		}
	}
	return false
}

// OptionLimitations are the limitation codes naming the options (names
// only, never values): tsconfig_option_<snake_name>.
func OptionLimitations(names ...[]string) []string {
	var out []string
	for _, list := range names {
		for _, n := range list {
			out = append(out, "tsconfig_option_"+snake(n))
		}
	}
	return out
}

var upper = regexp.MustCompile(`[A-Z]`)

func snake(s string) string {
	return upper.ReplaceAllStringFunc(s, func(u string) string { return "_" + strings.ToLower(u) })
}

// hostPath is a quoted absolute path in a tool message.
var hostPath = regexp.MustCompile(`'(?:/|[A-Za-z]:[\\/]|\\\\)[^']*'`)

// WithholdHostPaths replaces quoted absolute paths left in a (masked)
// tool message, so a diagnostic about a host file never quotes it.
func WithholdHostPaths(s string) string {
	return hostPath.ReplaceAllString(s, "'[host path withheld]'")
}
