package doctor

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/backup"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/quota"
)

// Operations is the optional W25 section of `codeye doctor --config` (docs/OPERATIONS.md
// §6): per-area storage use against the personal quota, the newest backup's
// age and whether the start/stop state matches the running processes. It is
// absent without --config, so the W01 report is unchanged.
type Operations struct {
	Storage   []AreaStatus     `json:"storage"`
	Backup    BackupStatus     `json:"backup"`
	Processes ProcessesSummary `json:"processes"`
}

// AreaStatus is one storage area. Bytes is measured by walking the area's
// directories now (doctor), which can differ from the enforcement basis.
type AreaStatus struct {
	Area           string  `json:"area"`
	Status         string  `json:"status"`
	Bytes          *int64  `json:"bytes"`
	LimitBytes     *int64  `json:"limit_bytes"`
	AvailableBytes *uint64 `json:"filesystem_available_bytes"`
	Incomplete     bool    `json:"incomplete"`
	Enforcement    string  `json:"enforcement"`
}

// BackupStatus is the newest complete backup set under backup_dir.
type BackupStatus struct {
	Status     string     `json:"status"`
	Latest     *string    `json:"latest"`
	CreatedAt  *time.Time `json:"created_at"`
	AgeSeconds *int64     `json:"age_seconds"`
}

// ProcessesSummary compares the process records with the live processes.
type ProcessesSummary struct {
	Status     string   `json:"status"`
	Controller string   `json:"controller"`
	Agent      string   `json:"agent"`
	Issues     []string `json:"issues"`
}

// Area statuses.
const (
	AreaWithinLimit = "within_limit"
	AreaOverLimit   = "over_limit"
	AreaNoLimit     = "no_limit"
	AreaUnmeasured  = "unmeasured"
)

// IssueOperationInProgress: a start/stop/backup/restore holds the personal
// lock, so the state may be changing (same word as codeye status).
const IssueOperationInProgress = "operation_in_progress"

// backupStaleAfter: an older newest backup is reported "stale".
const backupStaleAfter = 7 * 24 * time.Hour

func operations(cfg *personal.Config, now time.Time) *Operations {
	layout := personal.Layout{Root: cfg.StateDir}
	ops := &Operations{Storage: []AreaStatus{}}
	l := quota.Layout{AgentStateDir: layout.AgentDir(), GoModCache: cfg.Agent.GoModCache, LogDir: layout.LogDir(), ArtifactDir: layout.ArtifactDir()}
	for _, area := range quota.Areas {
		a := AreaStatus{Area: area, Status: AreaNoLimit, Enforcement: "claim_refusal_node_heartbeat"}
		if area == quota.Artifacts {
			a.Enforcement = "claim_refusal_database_accounting"
		}
		if limit := cfg.Quota.Limit(area); limit > 0 {
			a.LimitBytes = &limit
		}
		_, a.AvailableBytes = resources(existingParent(areaRoot(l, area)))
		u, err := quota.Measure(l, area)
		switch {
		case err != nil:
			a.Status = AreaUnmeasured
		default:
			b := u.Bytes
			a.Bytes, a.Incomplete = &b, u.Incomplete
			if a.LimitBytes != nil {
				a.Status = AreaWithinLimit
				if b >= *a.LimitBytes {
					a.Status = AreaOverLimit
				}
			}
		}
		ops.Storage = append(ops.Storage, a)
	}
	ops.Backup = backupStatus(cfg.BackupDir, now)
	// Like codeye status: probe the agent lock (an agent without a record) only
	// while no start/stop/backup/restore holds the personal lock.
	probe, busy := false, false
	if unlock, err := layout.Lock(); err == nil {
		probe = true
		defer unlock()
	} else if errors.Is(err, personal.ErrBusy) {
		busy = true
	}
	state := layout.Inspect(cfg.Listen, probe)
	ops.Processes = ProcessesSummary{Status: "consistent", Issues: append([]string{}, state.Issues...)}
	if busy {
		ops.Processes.Issues = append(ops.Processes.Issues, IssueOperationInProgress)
	}
	for _, p := range state.Processes {
		switch p.Role {
		case personal.RoleController:
			ops.Processes.Controller = p.State
		case personal.RoleAgent:
			ops.Processes.Agent = p.State
		}
	}
	if personal.Supported() != nil {
		ops.Processes.Status = "unsupported"
	} else if len(ops.Processes.Issues) > 0 {
		ops.Processes.Status = "inconsistent"
	}
	return ops
}

func areaRoot(l quota.Layout, area string) string {
	switch area {
	case quota.Logs:
		return l.LogDir
	case quota.Artifacts:
		return l.ArtifactDir
	}
	return l.AgentStateDir
}

// existingParent returns path or its nearest existing ancestor (statfs).
func existingParent(path string) string {
	for p := path; p != ""; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if filepath.Dir(p) == p {
			break
		}
	}
	return path
}

func backupStatus(dir string, now time.Time) BackupStatus {
	if dir == "" {
		return BackupStatus{Status: "not_configured"}
	}
	// A path that is not a directory is unreadable on every OS (Windows
	// reports reading a file as a directory as "not found").
	if st, err := os.Stat(dir); err == nil && !st.IsDir() {
		return BackupStatus{Status: "unreadable"}
	}
	name, at, err := backup.Newest(dir)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return BackupStatus{Status: "none"}
	case err != nil:
		return BackupStatus{Status: "unreadable"}
	}
	age := int64(now.Sub(at) / time.Second)
	st := BackupStatus{Status: "recent", Latest: &name, CreatedAt: &at, AgeSeconds: &age}
	if now.Sub(at) > backupStaleAfter {
		st.Status = "stale"
	}
	return st
}

func (o *Operations) write(w io.Writer) error {
	if o == nil {
		return nil
	}
	num := func(p *int64) string {
		if p == nil {
			return "-"
		}
		return strconv.FormatInt(*p, 10)
	}
	for _, a := range o.Storage {
		avail := "-"
		if a.AvailableBytes != nil {
			avail = strconv.FormatUint(*a.AvailableBytes, 10)
		}
		if _, err := fmt.Fprintf(w, "storage %-10s %-13s bytes=%s limit=%s fs_available=%s incomplete=%t\n", a.Area, a.Status, num(a.Bytes), num(a.LimitBytes), avail, a.Incomplete); err != nil {
			return err
		}
	}
	latest := "-"
	if o.Backup.Latest != nil {
		latest = *o.Backup.Latest
	}
	if _, err := fmt.Fprintf(w, "backup  %-24s latest=%s age_seconds=%s\n", o.Backup.Status, latest, num(o.Backup.AgeSeconds)); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "processes %-22s controller=%s agent=%s issues=%s\n", o.Processes.Status, o.Processes.Controller, o.Processes.Agent, strings.Join(o.Processes.Issues, ","))
	return err
}
