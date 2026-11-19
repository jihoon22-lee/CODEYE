package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/quota"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/internal/worker/analysis/builtin"
)

// Personal mode (docs/OPERATIONS.md): `codeye start|stop|status` run one loopback
// controller and one local agent from a JSON config. Linux only.

// personalTimings are replaced in tests.
var personalTimings = struct {
	ready, enroll, controllerStop, poll time.Duration
}{ready: 30 * time.Second, enroll: 120 * time.Second, controllerStop: 40 * time.Second, poll: 250 * time.Millisecond}

// personalExecutable is the binary start launches (tests override it).
var personalExecutable = func() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func runPersonal(ctx context.Context, args []string, out, stderr io.Writer) (int, bool) {
	switch args[0] {
	case "start":
		return personalStart(ctx, args[1:], out, stderr), true
	case "stop":
		return personalStop(ctx, args[1:], out, stderr), true
	case "status":
		return personalStatus(ctx, args[1:], out, stderr), true
	case "backup":
		return personalBackup(ctx, args[1:], out, stderr), true
	case "restore":
		return personalRestore(ctx, args[1:], out, stderr), true
	}
	return 0, false
}

func knownTools() []string { return append(builtin.ToolFlags(), builtin.ToolchainFlags()...) }

// loadPersonalConfig parses --config (and the extra flags) and loads it.
func loadPersonalConfig(name string, args []string, stderr io.Writer, define func(*flag.FlagSet)) (*personal.Config, personal.Layout, bool) {
	var path string
	if !parse(name, args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&path, "config", "", "")
		if define != nil {
			define(fs)
		}
	}) {
		return nil, personal.Layout{}, false
	}
	if path == "" {
		p, err := personal.DefaultPath()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return nil, personal.Layout{}, false
		}
		path = p
	}
	cfg, err := personal.Load(path, knownTools())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, personal.Layout{}, false
	}
	return cfg, personal.Layout{Root: cfg.StateDir}, true
}

// childEnv copies only the named variables of this process.
func childEnv(keys ...string) []string {
	var env []string
	for _, k := range keys {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func httpReady(origin string) (int, bool) {
	client := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(origin + "/api/v1/ready")
	if err != nil {
		return 0, false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	_ = resp.Body.Close()
	return resp.StatusCode, resp.StatusCode == http.StatusOK
}

// exited reports a started child's exit without blocking.
type childWatch struct{ done chan error }

func watchChild(cmd *exec.Cmd) childWatch {
	w := childWatch{done: make(chan error, 1)}
	go func() { w.done <- cmd.Wait() }()
	return w
}

func (w childWatch) exited() (bool, int) {
	select {
	case err := <-w.done:
		w.done <- err
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return true, ee.ExitCode()
		}
		return true, 0
	default:
		return false, 0
	}
}

func personalStart(ctx context.Context, args []string, out, stderr io.Writer) int {
	cfg, layout, ok := loadPersonalConfig("start", args, stderr, nil)
	if !ok {
		return 2
	}
	if err := personal.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := layout.Prepare(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	unlock, err := layout.Lock()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	state := layout.Inspect(cfg.Listen, true)
	ctrlUp, agentUp := state.Running(personal.RoleController), state.Running(personal.RoleAgent)
	if ctrlUp && agentUp {
		fmt.Fprintf(out, "CODEYE is already running (controller %s); see codeye status\n", cfg.Origin())
		return 0
	}
	if ctrlUp || agentUp {
		fmt.Fprintln(stderr, "CODEYE is partially running; run codeye stop first, then codeye start")
		return 1
	}
	for _, issue := range state.Issues {
		switch issue {
		case personal.IssueAgentWithoutRecord:
			fmt.Fprintln(stderr, "an agent that codeye start did not record holds", filepath.Join(layout.AgentDir(), "agent.lock"), "- it was not signalled; stop it first")
			return 1
		case personal.IssueListenWithoutRecord:
			fmt.Fprintln(stderr, "the listen address", cfg.Listen, "is already in use by a process codeye start did not record")
			return 1
		case personal.IssueRecordUnreadable:
			fmt.Fprintln(stderr, "a process record or lock in", layout.RunDir(), "cannot be read; inspect it before starting")
			return 1
		}
	}
	for _, p := range state.Processes {
		if p.Record != nil {
			// Exited or reused pid: the recorded process is gone; nothing is signalled.
			fmt.Fprintf(out, "removed stale %s record (pid %d %s)\n", p.Role, p.Record.PID, p.State)
			_ = layout.RemoveRecord(p.Role)
		}
	}
	dbURL := os.Getenv("CODEYE_DATABASE_URL")
	if dbURL == "" {
		fmt.Fprintln(stderr, "CODEYE_DATABASE_URL is not set (database.mode external)")
		return 1
	}
	exe, err := personalExecutable()
	if err != nil {
		fmt.Fprintln(stderr, "cannot resolve the codeye executable")
		return 1
	}
	opCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	store, err := openStore(opCtx)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer store.Close()
	st, err := store.SchemaStatus(opCtx)
	if err != nil {
		fmt.Fprintln(stderr, "cannot read database schema status:", err)
		return 1
	}
	if st.State != "current" {
		fmt.Fprintf(stderr, "database schema is %s (applied %d, this binary expects %d); back it up and run \"codeye migrate\" explicitly\n", st.State, st.Applied, st.Latest)
		return 1
	}
	user, err := personalUser(opCtx, store, layout, out)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	roots, err := reconcileRoots(opCtx, store, cfg, user, out)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := store.SetArtifactQuota(opCtx, cfg.Quota.ArtifactsBytes); err != nil {
		fmt.Fprintln(stderr, "cannot apply the artifact quota:", err)
		return 1
	}
	enroll, err := prepareEnrollment(opCtx, store, layout, roots, out)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer os.Remove(layout.EnrollToken())

	started := []*personal.Record{}
	rollback := func(why string) int {
		fmt.Fprintln(stderr, why)
		for i := len(started) - 1; i >= 0; i-- {
			r := started[i]
			if err := r.Terminate(); err == nil {
				waitGone(r, 45*time.Second)
			}
			if r.State() != personal.ProcRunning {
				_ = layout.RemoveRecord(r.Role)
			} else {
				fmt.Fprintf(stderr, "%s (pid %d) did not stop; its record is kept: run codeye stop\n", r.Role, r.PID)
			}
		}
		fmt.Fprintln(stderr, "logs:", layout.LogDir())
		return 1
	}
	ctrlArgs := []string{"serve", "--listen", cfg.Listen, "--artifact-dir", layout.ArtifactDir(), "--web-dir", cfg.WebDir}
	ctrlEnv := append(childEnv("PATH", "HOME", "LANG", "TZ"), "CODEYE_DATABASE_URL="+dbURL)
	cmd, rec, err := personal.Spawn(personal.RoleController, exe, ctrlArgs, ctrlEnv, layout.Root, layout.LogFile(personal.RoleController))
	if err != nil {
		if cmd != nil {
			_ = cmd.Wait()
		}
		return rollback(err.Error())
	}
	if err := layout.WriteRecord(rec); err != nil {
		started = append(started, &rec)
		return rollback("cannot record the controller process")
	}
	started = append(started, &rec)
	ctrlWatch := watchChild(cmd)
	deadline := time.Now().Add(personalTimings.ready)
	for {
		if done, code := ctrlWatch.exited(); done {
			return rollback(fmt.Sprintf("the controller exited during start (exit %d)", code))
		}
		if _, ok := httpReady(cfg.Origin()); ok {
			break
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return rollback("the controller did not become ready")
		}
		time.Sleep(personalTimings.poll)
	}
	agentArgs := agentArgv(cfg, layout, roots, enroll)
	cmd, arec, err := personal.Spawn(personal.RoleAgent, exe, agentArgs, childEnv("PATH", "LANG"), layout.Root, layout.LogFile(personal.RoleAgent))
	if err != nil {
		if cmd != nil {
			_ = cmd.Wait()
		}
		return rollback(err.Error())
	}
	started = append(started, &arec)
	if err := layout.WriteRecord(arec); err != nil {
		return rollback("cannot record the agent process")
	}
	agentWatch := watchChild(cmd)
	deadline = time.Now().Add(personalTimings.enroll)
	for {
		if done, code := agentWatch.exited(); done {
			hint := ""
			switch code {
			case agent.ExitEnroll:
				hint = " (enrollment rejected)"
			case agent.ExitRevoked:
				hint = " (the enrolled node is revoked)"
			case agent.ExitUsage:
				hint = " (invalid agent configuration)"
			}
			return rollback(fmt.Sprintf("the agent exited during start (exit %d%s)", code, hint))
		}
		if node, _ := layout.AgentNodeID(); node != "" {
			if ns, err := store.NodeStateByID(opCtx, node); err == nil && ns.Fresh && !ns.Revoked {
				break
			}
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return rollback("the agent did not enroll and report a heartbeat in time")
		}
		time.Sleep(personalTimings.poll)
	}
	_ = os.Remove(layout.EnrollToken())
	if err := writeLoginLink(opCtx, store, user, cfg.Origin(), layout.LoginLink(), out); err != nil {
		fmt.Fprintln(stderr, "started, but no login link:", err)
	}
	fmt.Fprintf(out, "CODEYE started: controller %s (pid %d), agent pid %d; logs in %s\n", cfg.Origin(), rec.PID, arec.PID, layout.LogDir())
	return 0
}

// personalUser returns the installation's user, creating it on first start.
func personalUser(ctx context.Context, s *postgres.Store, layout personal.Layout, out io.Writer) (string, error) {
	id, err := layout.ReadIdentity()
	if err != nil {
		return "", err
	}
	if id != nil {
		active, err := s.UserActive(ctx, id.UserID)
		if err != nil {
			return "", fmt.Errorf("cannot read the personal user: %w", err)
		}
		if !active {
			return "", errors.New("the personal user in personal.json does not exist in this database or is disabled; restore the matching backup or use a fresh state_dir")
		}
		return id.UserID, nil
	}
	user, err := s.CreateUserNamed(ctx, "personal")
	if err != nil {
		return "", fmt.Errorf("cannot create the personal user: %w", err)
	}
	if err := layout.WriteIdentity(user); err != nil {
		return "", errors.New("cannot store personal.json")
	}
	fmt.Fprintln(out, "created the personal user", user)
	return user, nil
}

// rootGrant is one configured source root resolved to its database ID.
type rootGrant struct {
	personal.SourceRoot
	ID string
}

// reconcileRoots registers missing configured source roots and grants them
// to the personal user. A label registered with another path is an error:
// source roots are immutable.
func reconcileRoots(ctx context.Context, s *postgres.Store, cfg *personal.Config, user string, out io.Writer) ([]rootGrant, error) {
	var roots []rootGrant
	for _, r := range cfg.SourceRoots {
		rec, err := s.SourceRootByLabel(ctx, r.Label)
		switch {
		case errors.Is(err, domain.ErrNotFound):
			if st, err := os.Stat(r.Path); err != nil || !st.IsDir() {
				return nil, fmt.Errorf("source root %q: %s is not an existing directory", r.Label, r.Path)
			}
			added, err := s.AddSourceRoot(ctx, r.Label, r.Path)
			if err != nil {
				return nil, fmt.Errorf("cannot register source root %q: %w", r.Label, err)
			}
			rec = postgres.SourceRootRecord{ID: added.ID, Label: added.Label, Path: r.Path}
			fmt.Fprintf(out, "registered source root %q (%s)\n", r.Label, rec.ID)
		case err != nil:
			return nil, fmt.Errorf("cannot read source root %q: %w", r.Label, err)
		case rec.Path != r.Path:
			return nil, fmt.Errorf("source root %q is registered with another path; source roots are immutable, use a new label", r.Label)
		}
		if err := s.GrantSourceRoot(ctx, rec.ID, user); err != nil {
			return nil, fmt.Errorf("cannot grant source root %q: %w", r.Label, err)
		}
		roots = append(roots, rootGrant{SourceRoot: r, ID: rec.ID})
	}
	return roots, nil
}

// prepareEnrollment decides whether the local agent must enroll. An agent
// enrolled for other source roots, for a revoked node, or for a node this
// database does not know is retired (its state directory is renamed, never
// deleted) after checking that its journal holds no unfinished work. It
// returns true when an enrollment token file was written.
func prepareEnrollment(ctx context.Context, s *postgres.Store, layout personal.Layout, roots []rootGrant, out io.Writer) (bool, error) {
	want := make([]string, 0, len(roots))
	for _, r := range roots {
		want = append(want, r.ID)
	}
	sort.Strings(want)
	node, err := layout.AgentNodeID()
	if err != nil {
		return false, err
	}
	if node != "" {
		ns, err := s.NodeStateByID(ctx, node)
		reason := ""
		switch {
		case errors.Is(err, domain.ErrNotFound):
			reason = "its node is unknown to this database"
		case err != nil:
			return false, fmt.Errorf("cannot read the local node: %w", err)
		case ns.Revoked:
			reason = "its node is revoked"
		case strings.Join(ns.SourceRootIDs, ",") != strings.Join(want, ","):
			reason = "the configured source roots changed"
		}
		if reason == "" {
			return false, nil
		}
		if problem := journalProblem(layout); problem != "" {
			return false, fmt.Errorf("the local agent must re-enroll (%s) but its journal %s; inspect %s", reason, problem, layout.AgentDir())
		}
		if err == nil && !ns.Revoked {
			if err := s.RevokeNode(ctx, node); err != nil {
				return false, fmt.Errorf("cannot revoke the previous local node: %w", err)
			}
		}
		retired := fmt.Sprintf("%s.retired-%d", layout.AgentDir(), time.Now().UnixNano())
		if err := os.Rename(layout.AgentDir(), retired); err != nil {
			return false, errors.New("cannot retire the previous agent state directory")
		}
		if err := os.Mkdir(layout.AgentDir(), 0o700); err != nil {
			return false, errors.New("cannot create the agent state directory")
		}
		fmt.Fprintf(out, "re-enrolling the local agent: %s (previous state kept in %s)\n", reason, retired)
	}
	if err := writeEnrollToken(ctx, s, "default", want, defaultEnrollTTL, layout.EnrollToken(), io.Discard); err != nil {
		return false, err
	}
	return true, nil
}

// journalProblem returns why the agent journal is not clean ("" if clean or absent).
func journalProblem(layout personal.Layout) string {
	j, err := agent.ReadJournal(filepath.Join(layout.AgentDir(), "journal.jsonl"))
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		return "cannot be read"
	}
	return journalUncertainty(j)
}

// journalUncertainty: unfinished attempts, live workers, a torn tail or an
// unconfirmed cleanup report make the agent's cleanup uncertain.
func journalUncertainty(j *agent.Journal) string {
	if j.Torn {
		return "has a torn record"
	}
	for _, w := range j.Workers {
		if !w.Exited {
			return "has a worker without an exit record"
		}
	}
	for _, a := range j.Attempts {
		if !a.Done {
			return "has an unfinished attempt"
		}
	}
	for _, p := range j.Pending {
		if p.CleanupState != "confirmed" {
			return "has an unconfirmed cleanup report"
		}
	}
	return ""
}

// agentArgv builds the local agent's argv from the config.
func agentArgv(cfg *personal.Config, layout personal.Layout, roots []rootGrant, enroll bool) []string {
	a := cfg.Agent
	name := a.Name
	if name == "" {
		name = "personal"
	}
	args := []string{"agent", "--controller", cfg.Origin(), "--state-dir", layout.AgentDir(), "--name", name,
		"--autoscale", "--max-workers", strconv.Itoa(a.MaxWorkers), "--toolchain-path", a.ToolchainPath}
	if enroll {
		args = append(args, "--enroll-token-file", layout.EnrollToken())
	}
	for _, r := range roots {
		args = append(args, "--source-root", r.ID+"="+r.Path)
		if r.RepositoryCode {
			args = append(args, "--repo-code-source-root", r.ID)
		}
		if r.ModelReview {
			args = append(args, "--model-source-root", r.ID)
		}
	}
	if a.Go != "" {
		args = append(args, "--go", a.Go)
	}
	if a.GoModCache != "" {
		args = append(args, "--go-mod-cache", a.GoModCache)
	}
	tools := make([]string, 0, len(a.Tools))
	for t := range a.Tools {
		tools = append(tools, t)
	}
	sort.Strings(tools)
	for _, t := range tools {
		args = append(args, "--"+t, a.Tools[t])
	}
	if a.PythonBothTypeCheckers {
		args = append(args, "--python-both-type-checkers")
	}
	if m := a.Model; m != nil {
		args = append(args, "--opencode", m.OpenCode, "--model", m.Name)
		if m.RelayFile != "" {
			args = append(args, "--model-relay-file", m.RelayFile)
		}
		if m.ReviewPack != "" {
			args = append(args, "--review-pack", m.ReviewPack)
		}
	}
	limits := cfg.Quota.NodeLimits()
	areas := make([]string, 0, len(limits))
	for area := range limits {
		areas = append(areas, area)
	}
	sort.Strings(areas)
	for _, area := range areas {
		args = append(args, "--storage-quota", area+"="+strconv.FormatInt(limits[area], 10))
	}
	if len(limits) > 0 {
		args = append(args, "--storage-log-dir", layout.LogDir())
	}
	return args
}

// waitGone polls until r's process is no longer running or d passes.
func waitGone(r *personal.Record, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if r.State() != personal.ProcRunning {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(personalTimings.poll)
	}
}

// --- stop --------------------------------------------------------------------

// Stop outcomes.
const (
	stopStopped          = "stopped"
	stopNotRunning       = "not_running"
	stopRecoveryRequired = "recovery_required"
	stopFailed           = "failed"
)

type procReport struct {
	Role   string `json:"role"`
	PID    int    `json:"pid,omitempty"`
	Before string `json:"state_before"`
	Action string `json:"action"`
	After  string `json:"state_after"`
}

type attemptReport struct {
	ID        string `json:"attempt_id"`
	RunID     string `json:"run_id"`
	Status    string `json:"status"`
	Cleanup   string `json:"cleanup_state"`
	RunStatus string `json:"run_status"`
}

type stopReport struct {
	Outcome      string          `json:"outcome"`
	Processes    []procReport    `json:"processes"`
	InFlight     []attemptReport `json:"in_flight_at_stop"`
	AttemptsNow  []attemptReport `json:"attempts_after_stop"`
	AgentCleanup string          `json:"agent_cleanup"`
	Notes        []string        `json:"notes"`
}

func attemptReports(in []postgres.AttemptState) []attemptReport {
	out := []attemptReport{}
	for _, a := range in {
		out = append(out, attemptReport{ID: a.ID, RunID: a.RunID, Status: a.Status, Cleanup: a.Cleanup, RunStatus: a.RunStatus})
	}
	return out
}

func personalStop(ctx context.Context, args []string, out, stderr io.Writer) int {
	var format string
	cfg, layout, ok := loadPersonalConfig("stop", args, stderr, func(fs *flag.FlagSet) { fs.StringVar(&format, "format", "text", "") })
	if !ok || (format != "text" && format != "json") {
		return 2
	}
	if err := personal.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	unlock, err := layout.Lock()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer unlock()
	rep := stopReport{InFlight: []attemptReport{}, AttemptsNow: []attemptReport{}, Notes: []string{}, AgentCleanup: "not_applicable"}
	state := layout.Inspect(cfg.Listen, true)
	for _, issue := range state.Issues {
		switch issue {
		case personal.IssueAgentWithoutRecord:
			rep.Notes = append(rep.Notes, "an agent without a start record holds the agent state lock; it was not signalled")
		case personal.IssueListenWithoutRecord:
			rep.Notes = append(rep.Notes, "the listen address is used by a process without a start record; it was not signalled")
		}
	}
	// The database is optional for stop: without it the in-flight work is
	// unknown, which the report says.
	var store *postgres.Store
	if os.Getenv("CODEYE_DATABASE_URL") != "" {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		store, err = openStore(sctx)
		cancel()
		if err != nil {
			store = nil
		}
	}
	if store != nil {
		defer store.Close()
	} else {
		rep.Notes = append(rep.Notes, "database unavailable: in-flight attempts were not read")
	}
	node, _ := layout.AgentNodeID()
	readAttempts := func(also []string) ([]postgres.AttemptState, bool) {
		if store == nil || node == "" {
			return nil, false
		}
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		a, err := store.NodeAttempts(qctx, node, also)
		return a, err == nil
	}

	agentStopped := true
	var agentRec, ctrlRec *personal.Record
	for _, p := range state.Processes {
		switch p.Role {
		case personal.RoleAgent:
			agentRec = p.Record
		case personal.RoleController:
			ctrlRec = p.Record
		}
	}
	anyRunning := false
	// 1. Drain the agent, then cancel on timeout; never kill it.
	ap := procReport{Role: personal.RoleAgent, Before: stateOf(agentRec), Action: "none"}
	if agentRec != nil {
		ap.PID = agentRec.PID
	}
	if ap.Before == personal.ProcRunning {
		anyRunning = true
		inflight, _ := readAttempts(nil)
		rep.InFlight = attemptReports(inflight)
		if format == "text" {
			fmt.Fprintf(out, "draining the agent (%d attempt(s) in flight; up to %ds)\n", len(inflight), cfg.Stop.DrainTimeoutSeconds)
		}
		ap.Action = signalAction(agentRec.Drain(), "drain_requested")
		if ap.Action == "drain_requested" && !waitGoneCtx(ctx, agentRec, time.Duration(cfg.Stop.DrainTimeoutSeconds)*time.Second) {
			if format == "text" {
				fmt.Fprintf(out, "drain timeout: cancelling running work (up to %ds)\n", cfg.Stop.CancelTimeoutSeconds)
			}
			ap.Action = signalAction(agentRec.Terminate(), "cancelled_after_drain_timeout")
			waitGoneCtx(context.Background(), agentRec, time.Duration(cfg.Stop.CancelTimeoutSeconds)*time.Second)
		}
		ap.After = agentRec.State()
		if ap.After == personal.ProcRunning {
			agentStopped = false
			rep.Notes = append(rep.Notes, "the agent is still running; it was not killed and the controller was left running so the agent can still report cleanup")
		} else {
			// The verdict comes from the agent's own journal, not its log.
			problem := journalProblem(layout)
			if problem == "" {
				rep.AgentCleanup = "confirmed"
			} else {
				rep.AgentCleanup = "uncertain"
				rep.Notes = append(rep.Notes, "agent journal "+problem)
			}
		}
	} else {
		ap.After = ap.Before
	}
	if agentStopped && agentRec != nil && ap.After != personal.ProcRunning && ap.After != personal.ProcUnknown {
		_ = layout.RemoveRecord(personal.RoleAgent)
	}
	rep.Processes = append(rep.Processes, ap)

	// 2. The controller, only after the agent is gone.
	cp := procReport{Role: personal.RoleController, Before: stateOf(ctrlRec), Action: "none"}
	if ctrlRec != nil {
		cp.PID = ctrlRec.PID
	}
	if ids := attemptIDs(rep.InFlight); agentStopped {
		if now, ok := readAttempts(ids); ok {
			rep.AttemptsNow = attemptReports(now)
		}
	}
	if cp.Before == personal.ProcRunning {
		anyRunning = true
		if agentStopped {
			cp.Action = signalAction(ctrlRec.Terminate(), "terminated")
			waitGoneCtx(context.Background(), ctrlRec, personalTimings.controllerStop)
		} else {
			cp.Action = "left_running"
		}
	}
	cp.After = stateOf(ctrlRec)
	if ctrlRec != nil && cp.After != personal.ProcRunning && cp.After != personal.ProcUnknown {
		_ = layout.RemoveRecord(personal.RoleController)
	}
	rep.Processes = append(rep.Processes, cp)

	switch {
	case !agentStopped:
		rep.Outcome = stopRecoveryRequired
	case cp.After == personal.ProcRunning:
		rep.Outcome = stopFailed
		rep.Notes = append(rep.Notes, "the controller did not stop; it was not killed")
	case rep.AgentCleanup == "uncertain":
		rep.Outcome = stopRecoveryRequired
	case activeAttempts(rep.AttemptsNow):
		rep.Outcome = stopRecoveryRequired
		rep.Notes = append(rep.Notes, "attempts of this node are still leased or recovery_required in the database")
	case !anyRunning:
		rep.Outcome = stopNotRunning
	default:
		rep.Outcome = stopStopped
	}
	if format == "json" {
		printJSON(out, rep)
	} else {
		for _, p := range rep.Processes {
			fmt.Fprintf(out, "%-10s %-12s -> %-12s action=%s\n", p.Role, p.Before, p.After, p.Action)
		}
		for _, a := range rep.AttemptsNow {
			fmt.Fprintf(out, "attempt %s run %s: %s cleanup=%s run=%s\n", a.ID, a.RunID, a.Status, a.Cleanup, a.RunStatus)
		}
		for _, n := range rep.Notes {
			fmt.Fprintln(out, "note:", n)
		}
		fmt.Fprintln(out, "outcome:", rep.Outcome)
	}
	if rep.Outcome == stopStopped || rep.Outcome == stopNotRunning {
		return 0
	}
	return 1
}

func stateOf(r *personal.Record) string {
	if r == nil {
		return personal.ProcNone
	}
	return r.State()
}

func attemptIDs(in []attemptReport) []string {
	out := []string{}
	for _, a := range in {
		out = append(out, a.ID)
	}
	return out
}

func activeAttempts(in []attemptReport) bool {
	for _, a := range in {
		if a.Status == "leased" || a.Status == "recovery_required" {
			return true
		}
	}
	return false
}

// signalAction names the result of a verified signal.
func signalAction(err error, sent string) string {
	switch {
	case err == nil:
		return sent
	case errors.Is(err, personal.ErrProcessGone):
		return "already_exited"
	case errors.Is(err, personal.ErrProcessReused):
		return "not_signalled_pid_reused"
	case errors.Is(err, personal.ErrSignalUnsupported):
		return "not_signalled_unsupported"
	}
	return "signal_failed"
}

func waitGoneCtx(ctx context.Context, r *personal.Record, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if r.State() != personal.ProcRunning {
			return true
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return false
		}
		time.Sleep(personalTimings.poll)
	}
}

// --- status ------------------------------------------------------------------

type areaStatus struct {
	Area       string `json:"area"`
	Bytes      *int64 `json:"bytes"`
	Limit      int64  `json:"limit_bytes"`
	Exceeded   bool   `json:"exceeded"`
	Incomplete bool   `json:"incomplete,omitempty"`
	// Basis is how the enforced value is measured: node_heartbeat (agent
	// walk) or database_accounting (artifact rows); doctor walks the dirs.
	Basis string `json:"basis"`
}

type statusReport struct {
	State      string          `json:"state"`
	Processes  []procReport    `json:"processes"`
	Issues     []string        `json:"issues"`
	Controller map[string]any  `json:"controller"`
	Agent      map[string]any  `json:"agent"`
	InFlight   []attemptReport `json:"in_flight"`
	Storage    []areaStatus    `json:"storage"`
}

func personalStatus(ctx context.Context, args []string, out, stderr io.Writer) int {
	var format string
	cfg, layout, ok := loadPersonalConfig("status", args, stderr, func(fs *flag.FlagSet) { fs.StringVar(&format, "format", "text", "") })
	if !ok || (format != "text" && format != "json") {
		return 2
	}
	if err := personal.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	rep := statusReport{Issues: []string{}, Controller: map[string]any{}, Agent: map[string]any{}, InFlight: []attemptReport{}, Storage: []areaStatus{}}
	// Probe the agent lock only while no start/stop runs (it takes the lock
	// for an instant).
	probe := false
	if unlock, err := layout.Lock(); err == nil {
		probe = true
		defer unlock()
	} else if errors.Is(err, personal.ErrBusy) {
		rep.Issues = append(rep.Issues, "operation_in_progress")
	}
	state := layout.Inspect(cfg.Listen, probe)
	rep.Issues = append(rep.Issues, state.Issues...)
	for _, p := range state.Processes {
		pr := procReport{Role: p.Role, Before: p.State, After: p.State, Action: "none"}
		if p.Record != nil {
			pr.PID = p.Record.PID
		}
		rep.Processes = append(rep.Processes, pr)
	}
	code, ready := httpReady(cfg.Origin())
	rep.Controller["origin"] = cfg.Origin()
	rep.Controller["ready"] = ready
	rep.Controller["http_status"] = code
	healthy := ready && state.Running(personal.RoleController) && state.Running(personal.RoleAgent)
	node, _ := layout.AgentNodeID()
	rep.Agent["node_id"] = node
	var store *postgres.Store
	if os.Getenv("CODEYE_DATABASE_URL") != "" {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		s, err := openStore(sctx)
		cancel()
		if err == nil {
			store = s
			defer store.Close()
		}
	}
	rep.Controller["database"] = store != nil
	var heartbeatFresh bool
	var nodeExceeded []string
	var artifacts *postgres.ArtifactUsage
	if store != nil {
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if node != "" {
			if ns, err := store.NodeStateByID(qctx, node); err == nil {
				heartbeatFresh = ns.Fresh && !ns.Revoked
				nodeExceeded = ns.QuotaExceeded
				rep.Agent["revoked"] = ns.Revoked
				rep.Agent["last_heartbeat_at"] = ns.LastHeartbeatAt
				rep.Agent["heartbeat_fresh"] = ns.Fresh
				rep.Agent["workers"] = ns.Workers
				rep.Agent["storage_quota_exceeded"] = ns.QuotaExceeded
			} else {
				rep.Issues = append(rep.Issues, "node_unknown_to_database")
			}
			if a, err := store.NodeAttempts(qctx, node, nil); err == nil {
				rep.InFlight = attemptReports(a)
			}
		}
		if u, err := store.ArtifactUsage(qctx); err == nil {
			artifacts = &u
		}
	} else {
		rep.Issues = append(rep.Issues, "database_unavailable")
	}
	healthy = healthy && heartbeatFresh
	l := quota.Layout{AgentStateDir: layout.AgentDir(), GoModCache: cfg.Agent.GoModCache, LogDir: layout.LogDir()}
	for _, area := range quota.NodeAreas {
		as := areaStatus{Area: area, Limit: cfg.Quota.Limit(area), Basis: "node_heartbeat"}
		if u, err := quota.Measure(l, area); err == nil {
			b := u.Bytes
			as.Bytes, as.Incomplete = &b, u.Incomplete
		}
		for _, e := range nodeExceeded {
			as.Exceeded = as.Exceeded || e == area
		}
		rep.Storage = append(rep.Storage, as)
	}
	as := areaStatus{Area: quota.Artifacts, Limit: cfg.Quota.ArtifactsBytes, Basis: "database_accounting"}
	if artifacts != nil {
		b := artifacts.Bytes
		as.Bytes = &b
		as.Exceeded = artifacts.Quota != nil && artifacts.Bytes >= *artifacts.Quota
	}
	rep.Storage = append(rep.Storage, as)
	sort.Strings(rep.Issues)
	switch {
	case healthy && len(state.Issues) == 0:
		rep.State = "running"
	case !state.Running(personal.RoleController) && !state.Running(personal.RoleAgent) && len(state.Issues) == 0:
		rep.State = "stopped"
	default:
		rep.State = "degraded"
	}
	if format == "json" {
		printJSON(out, rep)
	} else {
		fmt.Fprintln(out, "state:", rep.State)
		for _, p := range rep.Processes {
			fmt.Fprintf(out, "%-10s %-12s pid=%d\n", p.Role, p.After, p.PID)
		}
		fmt.Fprintf(out, "controller %s ready=%v database=%v\n", cfg.Origin(), ready, store != nil)
		fmt.Fprintf(out, "agent node=%s heartbeat_fresh=%v workers=%v\n", node, heartbeatFresh, rep.Agent["workers"])
		for _, a := range rep.InFlight {
			fmt.Fprintf(out, "in flight: attempt %s run %s %s cleanup=%s run=%s\n", a.ID, a.RunID, a.Status, a.Cleanup, a.RunStatus)
		}
		for _, s := range rep.Storage {
			used := "unmeasured"
			if s.Bytes != nil {
				used = strconv.FormatInt(*s.Bytes, 10)
			}
			fmt.Fprintf(out, "storage %-10s %s bytes limit=%d exceeded=%v (%s)\n", s.Area, used, s.Limit, s.Exceeded, s.Basis)
		}
		for _, i := range rep.Issues {
			fmt.Fprintln(out, "issue:", i)
		}
	}
	if rep.State == "degraded" {
		return 1
	}
	return 0
}

// personalStore opens the database named by CODEYE_DATABASE_URL.
func personalStore(ctx context.Context) (*postgres.Store, error) {
	if os.Getenv("CODEYE_DATABASE_URL") == "" {
		return nil, errors.New("CODEYE_DATABASE_URL is not set")
	}
	return openStore(ctx)
}
