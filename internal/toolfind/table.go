package toolfind

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/tabwriter"
)

// Summary counts the tools by status.
func (r Result) Summary() (found, missing, unusable int) {
	for _, t := range r.Tools {
		switch t.Status {
		case Found:
			found++
		case NotFound:
			missing++
		default:
			unusable++
		}
	}
	return
}

// WriteTable prints the found / missing / how-to-add table. home shortens
// paths under the user's home directory ("" keeps them whole).
func WriteTable(w io.Writer, r Result, home string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TOOL\tSTATUS\tVERSION\tWHERE / HOW TO ADD")
	for _, t := range r.Tools {
		var status, version, where string
		switch t.Status {
		case Found:
			status, version = "found", t.Version
			where = tilde(home, t.Path)
			if t.Source == SourceConfig {
				where += "  (config)"
			}
			if !t.VersionTested {
				note := "untested version"
				if t.TestedSeries != "" {
					note += ", tested " + t.TestedSeries
				}
				where += "  [" + note + "]"
			}
		case Unusable:
			status, version = "unusable", "-"
			where = t.Reason
			if t.Source == SourceConfig {
				where = tilde(home, t.Path) + ": " + where + " (config)"
			}
			if t.Hint != "" {
				where += " - " + t.Hint
			}
		default:
			status, version = "missing", "-"
			where = "not found"
			if t.Reason != "" {
				where += " (" + t.Reason + ")"
			}
			if t.Hint != "" {
				where += " - " + t.Hint
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", t.Name, status, version, where)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if t := r.ToolchainPath; t != nil {
		var found, missing []string
		for _, p := range t.Programs {
			if p.Status == Found {
				found = append(found, p.Name)
			} else {
				missing = append(missing, p.Name)
			}
		}
		dirs := filepath.SplitList(t.Path)
		for i := range dirs {
			dirs[i] = tilde(home, dirs[i])
		}
		line := "toolchain path " + strings.Join(dirs, string(filepath.ListSeparator)) + ":"
		if len(found) > 0 {
			line += " " + strings.Join(found, ", ") + " found"
		}
		if len(missing) > 0 {
			line += "; " + strings.Join(missing, ", ") + " missing"
		}
		if !t.CompileDBGenerationReady() {
			line += " - C/C++ compile DB generation needs cmake, make or ninja and a C/C++ compiler in agent.toolchain_path"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	var extra []string
	if r.GoModCache != "" {
		extra = append(extra, "go module cache: "+tilde(home, r.GoModCache))
	}
	if r.PythonBothTypeCheckers {
		extra = append(extra, "python type checkers: mypy and pyright both run")
	}
	if len(extra) > 0 {
		_, err := fmt.Fprintln(w, strings.Join(extra, "; "))
		return err
	}
	return nil
}
