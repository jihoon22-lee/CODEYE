package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

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
		return id.UserID, ensurePersonalAdmin(ctx, s, id.UserID)
	}
	u, err := s.CreateAccount(ctx, postgres.OperatorPrincipal(), postgres.NewAccount{DisplayName: "personal", IsAdmin: true})
	if err != nil {
		return "", fmt.Errorf("cannot create the personal user: %w", err)
	}
	user := u.ID
	if err := layout.WriteIdentity(user); err != nil {
		return "", errors.New("cannot store personal.json")
	}
	fmt.Fprintln(out, "created the personal user", user)
	return user, nil
}

// ensurePersonalAdmin makes the single personal-mode user an administrator
// (so it sees the account screens). An installation from before the role
// existed is promoted once; the change is audited as the operator.
func ensurePersonalAdmin(ctx context.Context, s *postgres.Store, user string) error {
	u, err := s.FindUser(ctx, user)
	if err != nil {
		return fmt.Errorf("cannot read the personal user: %w", err)
	}
	if u.IsAdmin {
		return nil
	}
	if _, err := s.SetAdmin(ctx, postgres.OperatorPrincipal(), user, true); err != nil {
		return fmt.Errorf("cannot make the personal user an administrator: %w", err)
	}
	return nil
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
	// The model, its credential file and the per-root model-review grants
	// (personal_model.go).
	args = append(args, modelAgentArgs(cfg, layout, roots)...)
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
