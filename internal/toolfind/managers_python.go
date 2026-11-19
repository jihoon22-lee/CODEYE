package toolfind

import (
	"regexp"
	"runtime"
	"strconv"
)

// Python package managers of the dependency fetch
// (docs/ANALYZERS.md#python-의존성-받기): uv, and the Python interpreter
// whose pip installs when there is no uv (uv installs for it). They are
// agent flags (--uv, --python) and agent.tools.uv / agent.tools.python.

// pythonManagerNames are the Python package manager tools in detection
// order: python is validated after uv, since an interpreter without pip is
// still usable when uv is found.
var pythonManagerNames = []string{"uv", "python"}

// Tested series of the Python package managers: uv 0.12; Python 3.11 and
// later read pyproject.toml with the standard library (tomllib).
const (
	uvTestedSeries     = "0.12"
	pythonTestedSeries = "3.11+"
)

func pythonBin() string {
	if runtime.GOOS == "windows" {
		return "python"
	}
	return "python3"
}

// pythonManagerSpecs are looked up like executables on PATH and in the
// user directories.
var pythonManagerSpecs = map[string]spec{
	"uv":     {kindExec, "uv", "", "`curl -LsSf https://astral.sh/uv/install.sh | sh` (installs to ~/.local/bin) or set `agent.tools.uv`"},
	"python": {kindExec, pythonBin(), "", "install Python 3 with pip (Ubuntu: `sudo apt install python3 python3-pip`) or uv, or set `agent.tools.python`"},
}

var (
	uvVersionLine     = regexp.MustCompile(`^uv ([0-9]+\.[0-9]+\.[0-9]+)`)
	pythonVersionLine = regexp.MustCompile(`^Python (3)\.([0-9]+)\.([0-9]+)$`)
	pipVersionLine    = regexp.MustCompile(`^pip [0-9][0-9A-Za-z.+-]* from `)
)

// validatePythonManager validates uv and python candidates; handled is
// false for any other tool.
func (d *detector) validatePythonManager(name, path string) (outcome, bool) {
	switch name {
	case "uv":
		v, err := d.version(path, "--version")
		if err != "" {
			return outcome{reason: err, series: uvTestedSeries}, true
		}
		m := uvVersionLine.FindStringSubmatch(v)
		if m == nil {
			return outcome{reason: "version output not recognized", series: uvTestedSeries}, true
		}
		return outcome{ok: true, version: m[1], tested: seriesMatch(m[1], uvTestedSeries), series: uvTestedSeries}, true
	case "python":
		v, err := d.version(path, "--version")
		if err != "" {
			return outcome{reason: err, series: pythonTestedSeries}, true
		}
		m := pythonVersionLine.FindStringSubmatch(v)
		if m == nil {
			return outcome{reason: "version output not recognized", series: pythonTestedSeries}, true
		}
		minor, _ := strconv.Atoi(m[2])
		if minor < 8 {
			return outcome{reason: "Python 3.8 or later is needed", series: pythonTestedSeries}, true
		}
		// pip installs when there is no uv; an interpreter without pip is
		// usable only with uv.
		if pv, perr := d.version(path, "-I", "-m", "pip", "--version"); perr != "" || !pipVersionLine.MatchString(pv) {
			if t := d.get("uv"); t.Status != Found && d.ex.Tools["uv"] == "" {
				return outcome{reason: "pip is not installed for this interpreter and uv was not found", series: pythonTestedSeries}, true
			}
		}
		return outcome{ok: true, version: m[1] + "." + m[2] + "." + m[3], tested: minor >= 11, series: pythonTestedSeries}, true
	}
	return outcome{}, false
}

// seriesMatch reports v in the major.minor series s.
func seriesMatch(v, s string) bool {
	return len(v) > len(s) && v[:len(s)] == s && v[len(s)] == '.'
}
