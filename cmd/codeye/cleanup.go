package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/artifactstore"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/retention"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// `codeye cleanup` (docs/OPERATIONS.md#보존-정리): retention for old raw
// artifact contents in the controller store and old snapshot copies and
// workspaces on a node. It is a dry run unless --apply is given; the dry run
// prints exactly what --apply would remove. Reports, observations,
// manifests, runs, reviews and audit rows are kept.
//
// Where it acts:
//   - the service (default, or --config): the managed or external database,
//     <state_dir>/artifacts, <state_dir>/agent and its retired directories;
//   - a controller host: --artifact-dir with CODEYE_DATABASE_URL;
//   - a node: --agent-state-dir (no database needed).

const defaultRetention = 30 * 24 * time.Hour

// cleanupReport is the --format json document.
type cleanupReport struct {
	OlderThan     string           `json:"older_than"`
	OlderThanDays int64            `json:"older_than_days"`
	Applied       bool             `json:"applied"`
	Artifacts     *artifactCleanup `json:"artifacts"`
	NodeData      *nodeDataCleanup `json:"node_data"`
	AuditRows     *auditRowsKept   `json:"audit_rows"`
	Kept          []string         `json:"kept"`
	Notes         []string         `json:"notes"`
}

type artifactCleanup struct {
	Cutoff        time.Time        `json:"cutoff"`
	Rows          int              `json:"rows"`
	Runs          int              `json:"runs"`
	RowBytes      int64            `json:"row_bytes"`
	ByRole        map[string]int   `json:"by_role"`
	FreedContents int              `json:"freed_contents"`
	FreedBytes    int64            `json:"freed_bytes"`
	DeletedFiles  int              `json:"deleted_files"`
	KeptFiles     int              `json:"kept_files"`
	FailedFiles   int              `json:"failed_files"`
	Sample        []artifactSample `json:"sample"`
}

type artifactSample struct {
	RunID string `json:"run_id"`
	Role  string `json:"role"`
	Size  int64  `json:"bytes"`
}

type nodeDataCleanup struct {
	Entries []retention.Entry `json:"entries"`
	Bytes   int64             `json:"bytes"`
	Removed int               `json:"removed"`
	Failed  int               `json:"failed"`
}

type auditRowsKept struct {
	Older  int64  `json:"older"`
	Action string `json:"action"`
}

// parseAge reads "30d" (days) or a Go duration ("36h").
func parseAge(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 1 || n > 36500 {
			return 0, errors.New("bad days")
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func cleanupCommand(ctx context.Context, args []string, out, stderr io.Writer) int {
	var config, artifactDir, agentState, age, format string
	var apply bool
	if !parse("cleanup", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&config, "config", "", "")
		fs.StringVar(&artifactDir, "artifact-dir", "", "")
		fs.StringVar(&agentState, "agent-state-dir", "", "")
		fs.StringVar(&age, "older-than", "30d", "")
		fs.StringVar(&format, "format", "text", "")
		fs.BoolVar(&apply, "apply", false, "")
	}) || (format != "text" && format != "json") {
		return 2
	}
	olderThan, err := parseAge(age)
	if err != nil || olderThan < postgres.MinRetentionAge {
		fmt.Fprintln(stderr, "--older-than must be days (30d) or a duration of at least 1h")
		return 2
	}
	if config != "" && artifactDir != "" {
		fmt.Fprintln(stderr, "--config and --artifact-dir cannot be combined")
		return 2
	}
	for _, p := range []*string{&artifactDir, &agentState} {
		if *p != "" {
			if *p, err = filepath.Abs(*p); err != nil {
				fmt.Fprintln(stderr, "a directory cannot be resolved")
				return 2
			}
		}
	}
	rep := cleanupReport{OlderThan: olderThan.String(), OlderThanDays: int64(olderThan / (24 * time.Hour)), Applied: apply,
		Kept: []string{"reports", "observations", "source_manifest", "result_manifest", "runs", "events", "finding_reviews", "audit_events", "snapshot_records"}, Notes: []string{}}
	code := 0
	switch {
	case artifactDir != "":
		code = withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
			return cleanArtifacts(ctx, s, artifactDir, olderThan, apply, &rep, stderr)
		})
		if code == 0 && agentState != "" {
			code = cleanNode(agentState, "", olderThan, apply, &rep, stderr)
		}
	case agentState != "" && config == "":
		code = cleanNode(agentState, "", olderThan, apply, &rep, stderr)
		rep.Notes = append(rep.Notes, "no database given: controller artifacts were not checked")
	default:
		code = cleanInstance(ctx, config, agentState, olderThan, apply, &rep, stderr)
	}
	if code != 0 && rep.Artifacts == nil && rep.NodeData == nil {
		return code
	}
	if format == "json" {
		printJSON(out, rep)
	} else {
		writeCleanupText(out, rep)
	}
	return code
}

func cleanInstance(ctx context.Context, config, agentState string, olderThan time.Duration, apply bool, rep *cleanupReport, stderr io.Writer) int {
	args := []string{}
	if config != "" {
		args = append(args, "--config", config)
	}
	cfg, layout, ok := loadInstanceConfig("cleanup", args, stderr, nil)
	if !ok {
		return 2
	}
	if err := instance.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	unlock, err := layout.Lock()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	store, db, err := instanceStore(opCtx, cfg, layout, stderr)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer stopStartedDB(db)
	defer store.Close()
	if code := cleanArtifacts(opCtx, store, layout.ArtifactDir(), olderThan, apply, rep, stderr); code != 0 {
		return code
	}
	if agentState == "" {
		agentState = layout.AgentDir()
	}
	return cleanNode(agentState, layout.AgentDir(), olderThan, apply, rep, stderr)
}

// cleanArtifacts plans (or applies) the controller part. Files are deleted
// only after the database marks their rows removed, and only when no present
// row references the content at that moment; empty contents are left in
// place (they free nothing and are shared by many runs).
func cleanArtifacts(ctx context.Context, s *postgres.Store, dir string, olderThan time.Duration, apply bool, rep *cleanupReport, stderr io.Writer) int {
	var plan postgres.RetentionPlan
	var err error
	if apply {
		plan, err = s.ApplyRetention(ctx, olderThan)
	} else {
		plan, err = s.PlanRetention(ctx, olderThan)
	}
	if err != nil {
		fmt.Fprintln(stderr, "cannot plan artifact retention:", err)
		return 1
	}
	a := &artifactCleanup{Cutoff: plan.Cutoff.UTC(), Rows: len(plan.Artifacts), Runs: plan.Runs, RowBytes: plan.RowBytes, ByRole: map[string]int{},
		FreedContents: len(plan.FreedBlobs), FreedBytes: plan.FreedBytes, Sample: []artifactSample{}}
	for _, x := range plan.Artifacts {
		a.ByRole[x.Role]++
		if len(a.Sample) < 20 {
			a.Sample = append(a.Sample, artifactSample{RunID: x.RunID, Role: x.Role, Size: x.Size})
		}
	}
	rep.Artifacts = a
	rep.AuditRows = &auditRowsKept{Older: plan.AuditRows, Action: "kept"}
	if !apply {
		return 0
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		fmt.Fprintln(stderr, "the artifact directory cannot be read; the rows are marked removed and their files are left in place")
		rep.Notes = append(rep.Notes, "artifact directory unreadable: files not deleted")
		return 1
	}
	for _, b := range plan.FreedBlobs {
		present, err := s.ContentPresent(ctx, b.SHA256)
		if err != nil {
			a.FailedFiles++
			continue
		}
		if present || b.Size == 0 {
			a.KeptFiles++
			continue
		}
		err = os.Remove(filepath.Join(dir, filepath.FromSlash(artifactstore.Key(b.SHA256))))
		switch {
		case err == nil:
			a.DeletedFiles++
		case errors.Is(err, os.ErrNotExist):
			a.KeptFiles++
		default:
			a.FailedFiles++
		}
	}
	if a.FailedFiles > 0 {
		rep.Notes = append(rep.Notes, "some artifact files could not be deleted; their rows are marked removed and the next cleanup does not retry them")
		return 1
	}
	return 0
}

// cleanNode plans (or applies) the node part for one agent state
// directory. Workspaces of attempts still open in the journal are kept.
func cleanNode(stateDir, retiredOf string, olderThan time.Duration, apply bool, rep *cleanupReport, stderr io.Writer) int {
	active := map[string]bool{}
	j, err := agent.ReadJournal(filepath.Join(stateDir, "journal.jsonl"))
	switch {
	case err == nil:
		for id, at := range j.Attempts {
			if !at.Done {
				active[id] = true
			}
		}
	case !errors.Is(err, os.ErrNotExist):
		fmt.Fprintln(stderr, "the agent journal cannot be read; node data was not checked")
		rep.Notes = append(rep.Notes, "agent journal unreadable: node data not checked")
		return 1
	}
	cutoff := time.Now().Add(-olderThan)
	entries, err := retention.ScanAgent(stateDir, cutoff, active)
	if err != nil {
		fmt.Fprintln(stderr, "cannot read the agent data directory")
		return 1
	}
	if retiredOf != "" {
		retired, err := retention.ScanRetired(retiredOf, cutoff)
		if err != nil {
			fmt.Fprintln(stderr, "cannot read the state directory")
			return 1
		}
		entries = append(entries, retired...)
	}
	// A running attempt works inside its snapshot copy (acquire-*/workspace)
	// and the journal does not name that directory: while the agent runs (or
	// when that cannot be told), snapshot copies are left alone.
	if running, err := instance.AgentStateLocked(stateDir); running || err != nil {
		kept := entries[:0]
		for _, e := range entries {
			if e.Kind != retention.KindSnapshot {
				kept = append(kept, e)
			}
		}
		if len(kept) < len(entries) {
			rep.Notes = append(rep.Notes, "the agent is running (or its lock cannot be checked): snapshot copies are kept; stop the agent to remove them")
		}
		entries = kept
	}
	retention.Sort(entries)
	n := &nodeDataCleanup{Entries: entries}
	if n.Entries == nil {
		n.Entries = []retention.Entry{}
	}
	for _, e := range entries {
		n.Bytes += e.Bytes
	}
	rep.NodeData = n
	if !apply {
		return 0
	}
	for _, e := range entries {
		if err := retention.Remove(e); err != nil {
			n.Failed++
		} else {
			n.Removed++
		}
	}
	if n.Failed > 0 {
		rep.Notes = append(rep.Notes, "some node directories could not be removed")
		return 1
	}
	return 0
}

func writeCleanupText(out io.Writer, r cleanupReport) {
	verb := "would remove"
	if r.Applied {
		verb = "removed"
	}
	fmt.Fprintf(out, "retention: older than %s (%d days)%s\n", r.OlderThan, r.OlderThanDays, map[bool]string{true: "", false: " - dry run, nothing changed (add --apply)"}[r.Applied])
	if a := r.Artifacts; a != nil {
		roles := make([]string, 0, len(a.ByRole))
		for k, v := range a.ByRole {
			roles = append(roles, fmt.Sprintf("%s %d", k, v))
		}
		sort.Strings(roles)
		fmt.Fprintf(out, "  raw artifacts: %s %d contents of %d runs (%d bytes; %d files freed, %d bytes) [%s]\n", verb, a.Rows, a.Runs, a.RowBytes, a.FreedContents, a.FreedBytes, strings.Join(roles, ", "))
		if r.Applied {
			fmt.Fprintf(out, "    files deleted %d, kept (still referenced or empty) %d, failed %d\n", a.DeletedFiles, a.KeptFiles, a.FailedFiles)
		}
	}
	if n := r.NodeData; n != nil {
		fmt.Fprintf(out, "  node data: %s %d directories (%d bytes)\n", verb, len(n.Entries), n.Bytes)
		for _, e := range n.Entries {
			fmt.Fprintf(out, "    %-13s %s (%s, %d bytes)\n", e.Kind, e.Path, e.ModTime.Format("2006-01-02"), e.Bytes)
		}
	}
	if r.AuditRows != nil {
		fmt.Fprintf(out, "  audit rows older than the cutoff: %d (kept: the audit log is append-only)\n", r.AuditRows.Older)
	}
	fmt.Fprintf(out, "  kept: %s\n", strings.Join(r.Kept, ", "))
	for _, n := range r.Notes {
		fmt.Fprintln(out, "  note:", n)
	}
}
