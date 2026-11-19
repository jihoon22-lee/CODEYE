package main

import (
	"encoding/json"
	"fmt"
	"io"
	"runtime"
	"runtime/debug"

	"github.com/jihoon22-lee/CODEYE/migrations"
)

// Build identity, set by the offline pack build (scripts/offline_pack.py)
// with -ldflags -X; a plain `go build` leaves the defaults and the commit
// comes from the module's VCS stamp when there is one.
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildTime    = ""
)

// versionInfo is `codeye version --json` (docs/OPERATIONS.md#배포팩-내용).
type versionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit,omitempty"`
	BuildTime string `json:"build_time,omitempty"`
	GoVersion string `json:"go_version"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	// Migrations is the newest schema migration embedded in this binary.
	Migrations int `json:"migrations"`
}

func currentVersion() versionInfo {
	latest, _ := migrations.LatestVersion()
	v := versionInfo{Version: buildVersion, Commit: buildCommit, BuildTime: buildTime, GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, Migrations: latest}
	if v.Commit == "" {
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, s := range info.Settings {
				if s.Key == "vcs.revision" {
					v.Commit = s.Value
				}
			}
		}
	}
	return v
}

// versionCommand is `codeye version [--json]`.
func versionCommand(args []string, out, stderr io.Writer) int {
	asJSON := len(args) == 1 && args[0] == "--json"
	if len(args) > 1 || (len(args) == 1 && !asJSON) {
		fmt.Fprintln(stderr, "usage: codeye version [--json]")
		return 2
	}
	v := currentVersion()
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if enc.Encode(v) != nil {
			return 1
		}
		return 0
	}
	commit := v.Commit
	if commit == "" {
		commit = "unknown"
	}
	fmt.Fprintf(out, "codeye %s (commit %s", v.Version, commit)
	if v.BuildTime != "" {
		fmt.Fprintf(out, ", built %s", v.BuildTime)
	}
	fmt.Fprintf(out, ", %s %s/%s, schema migration %d)\n", v.GoVersion, v.OS, v.Arch, v.Migrations)
	return 0
}
