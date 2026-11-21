package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/agent"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/instance"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

func cmdStart(ctx context.Context, args []string, out, stderr io.Writer) int {
	cfg, layout, ok := loadInstanceConfig("start", args, stderr, nil)
	if !ok {
		return 2
	}
	if err := instance.Supported(); err != nil {
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
	up, code := bringUp(ctx, cfg, layout, bringUpOptions{}, out, stderr)
	if up == nil {
		return code
	}
	defer up.store.Close()
	if up.already {
		fmt.Fprintf(out, "CODEYE is already running (controller %s); see codeye status\n", cfg.Origin())
		return 0
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := writeLoginLink(lctx, up.store, up.user, cfg.ServiceOrigin(), layout.LoginLink(), out); err != nil {
		fmt.Fprintln(stderr, "started, but no login link:", err)
	}
	fmt.Fprintf(out, "CODEYE started: controller %s (pid %d), agent pid %d; logs in %s\n", cfg.Origin(), up.controllerPID, up.agentPID, layout.LogDir())
	return 0
}

// bringUpOptions are the inputs of bringUp beyond the configuration.
type bringUpOptions struct {
	// postgresBinDir is `codeye up --postgres-bin-dir`.
	postgresBinDir string
	// noProbe is `codeye up --no-probe`: the model connection is not tried.
	noProbe bool
}

// running is a started or reused service installation.
type running struct {
	store         *postgres.Store
	db            *instanceDB
	user          string
	controllerPID int
	agentPID      int
	// already: both processes were running with the configured arguments;
	// nothing was started.
	already bool
}

// bringUp is the shared core of `codeye start` and `codeye up`; the caller
// holds the service lock. It starts the database (managed mode), brings the
// schema up to date (managed mode migrates by itself), registers the service's
// user and the source roots, then starts whichever of the controller and the
// agent is not running. A running agent whose argv no longer matches the
// configuration is drained (running work finishes) and started again. On
// failure everything this call started is stopped again and the exit code is
// returned with a nil result.
func bringUp(ctx context.Context, cfg *instance.Config, layout instance.Layout, o bringUpOptions, out, stderr io.Writer) (*running, int) {
	// Detected tools and the model complete the configuration before the
	// agent argv is built and compared with a running agent's.
	applyDetectedTools(ctx, cfg, out)
	model := setupInstanceModel(ctx, cfg, layout, o.noProbe, out)
	opCtx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	db, err := instanceDatabase(opCtx, cfg, layout, dbOptions{start: true, flagBinDir: o.postgresBinDir, notices: out})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, 1
	}
	stopDB := func() {
		if db.started {
			_ = db.stop(context.Background())
		}
	}
	state := layout.Inspect(probedListen(cfg), true)
	ctrlUp, agentUp := state.Running(instance.RoleController), state.Running(instance.RoleAgent)
	if !ctrlUp && !cfg.ListenExplicit() && !addrFree(cfg.Listen) {
		// The default address moves to the next free port; stop, status
		// and open read it from the controller record.
		free := freeListen()
		if free == "" {
			fmt.Fprintf(stderr, "ports %d-%d on 127.0.0.1 are all in use; set listen in the configuration\n", defaultPort, lastAutoPort)
			stopDB()
			return nil, 1
		}
		fmt.Fprintf(out, "%s is in use by another program; using %s\n", cfg.Listen, free)
		cfg.Listen = free
	}
	for _, issue := range state.Issues {
		switch issue {
		case instance.IssueAgentWithoutRecord:
			fmt.Fprintln(stderr, "an agent that codeye start did not record holds", filepath.Join(layout.AgentDir(), "agent.lock"), "- it was not signalled; stop it first")
			stopDB()
			return nil, 1
		case instance.IssueListenWithoutRecord:
			fmt.Fprintln(stderr, "the listen address", cfg.Listen, "is already in use by a process codeye start did not record")
			stopDB()
			return nil, 1
		case instance.IssueRecordUnreadable:
			fmt.Fprintln(stderr, "a process record or lock in", layout.RunDir(), "cannot be read; inspect it before starting")
			stopDB()
			return nil, 1
		}
	}
	for _, p := range state.Processes {
		if p.Record != nil && p.State != instance.ProcRunning {
			// Exited or reused pid: the recorded process is gone; nothing is signalled.
			fmt.Fprintf(out, "removed stale %s record (pid %d %s)\n", p.Role, p.Record.PID, p.State)
			_ = layout.RemoveRecord(p.Role)
		}
	}
	exe, err := instanceExecutable()
	if err != nil {
		fmt.Fprintln(stderr, "cannot resolve the codeye executable")
		stopDB()
		return nil, 1
	}
	store, err := openStore(opCtx)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		stopDB()
		return nil, 1
	}
	fail := func(msg ...any) (*running, int) {
		fmt.Fprintln(stderr, msg...)
		store.Close()
		if !state.Running(instance.RoleController) && !state.Running(instance.RoleAgent) {
			stopDB()
		}
		return nil, 1
	}
	if err := ensureSchema(opCtx, store, cfg, db, out); err != nil {
		return fail(err)
	}
	user, err := adminUser(opCtx, store, layout, out)
	if err != nil {
		return fail(err)
	}
	roots, err := reconcileRoots(opCtx, store, cfg, out)
	if err != nil {
		return fail(err)
	}
	if err := store.SetArtifactQuota(opCtx, cfg.Quota.ArtifactsBytes); err != nil {
		return fail("cannot apply the artifact quota:", err)
	}
	if err := ensureGitProviders(opCtx, store, cfg, out); err != nil {
		return fail(err)
	}
	gh, err := configurePlatformGitHub(opCtx, store, cfg, layout, out)
	if err != nil {
		return fail("cannot configure platform GitHub access:", err)
	}
	if err := ensurePoolWorkers(opCtx, store, cfg); err != nil {
		return fail(err)
	}
	if err := writeInstanceSetup(cfg, layout, model, gh); err != nil {
		return fail("cannot write installation setup:", err)
	}
	res := &running{store: store, db: db, user: user}
	var agentRec, ctrlRec *instance.Record
	for _, p := range state.Processes {
		if p.State == instance.ProcRunning {
			if p.Role == instance.RoleAgent {
				agentRec = p.Record
			} else {
				ctrlRec = p.Record
			}
		}
	}
	desired := agentArgv(cfg, layout, roots, false)
	if agentUp && agentRec.Args != nil && !slices.Equal(agentRec.Args, desired) {
		fmt.Fprintf(out, "restarting the agent: its configuration changed (running work finishes first, up to %ds)\n", cfg.Stop.DrainTimeoutSeconds)
		if err := agentRec.Drain(); err != nil && !errors.Is(err, instance.ErrProcessGone) {
			return fail("cannot ask the agent to drain:", signalAction(err, "drain_requested"))
		}
		if !waitGoneCtx(ctx, agentRec, time.Duration(cfg.Stop.DrainTimeoutSeconds)*time.Second) {
			return fail("the agent is still finishing its work and takes no new work; run the same command again when codeye status shows it idle")
		}
		_ = layout.RemoveRecord(instance.RoleAgent)
		agentUp, agentRec = false, nil
	}
	// The controller restarts when its listeners or offline Git policy change.
	// Agents with unchanged configuration reconnect and keep running their work.
	ctrlListen := cfg.Listen
	if ctrlUp && !cfg.ListenExplicit() && ctrlRec.Listen != "" {
		ctrlListen = ctrlRec.Listen
	}
	ctrlArgs := append([]string{"serve", "--session-ttl", httpapi.ServiceSessionTTL.String(), "--state-dir", layout.Root, "--listen", ctrlListen, "--artifact-dir", layout.ArtifactDir(), "--web-dir", cfg.WebDir}, controllerListenArgs(cfg)...)
	if path := cfg.FilePath(); path != "" {
		ctrlArgs = append(ctrlArgs, "--instance-config", path)
	}
	if cfg.Offline {
		ctrlArgs = append(ctrlArgs, "--offline")
		for _, host := range cfg.GitHosts {
			ctrlArgs = append(ctrlArgs, "--git-host", host)
		}
	}
	if cfg.CABundle != "" {
		ctrlArgs = append(ctrlArgs, "--ca-bundle", cfg.CABundle)
	}
	if ctrlUp && ctrlRec.Args != nil && !slices.Equal(ctrlRec.Args, ctrlArgs) {
		fmt.Fprintln(out, "restarting the controller: its configuration changed")
		if err := ctrlRec.Terminate(); err != nil && !errors.Is(err, instance.ErrProcessGone) {
			return fail("cannot stop the controller:", signalAction(err, "terminate_requested"))
		}
		if !waitGoneCtx(ctx, ctrlRec, 45*time.Second) {
			return fail("the controller did not stop; run codeye stop, then codeye up again")
		}
		_ = layout.RemoveRecord(instance.RoleController)
		ctrlUp, ctrlRec = false, nil
	}
	if ctrlUp && agentUp {
		res.already = true
		res.controllerPID, res.agentPID = ctrlRec.PID, agentRec.PID
		return res, 0
	}
	if ctrlRec != nil {
		res.controllerPID = ctrlRec.PID
	}
	enroll := false
	if !agentUp {
		if enroll, err = prepareEnrollment(opCtx, store, layout, roots, out); err != nil {
			return fail(err)
		}
	} else {
		res.agentPID = agentRec.PID
	}
	defer os.Remove(layout.EnrollToken())

	started := []*instance.Record{}
	rollback := func(why string) (*running, int) {
		fmt.Fprintln(stderr, why)
		for i := len(started) - 1; i >= 0; i-- {
			r := started[i]
			if err := r.Terminate(); err == nil {
				waitGone(r, 45*time.Second)
			}
			if r.State() != instance.ProcRunning {
				_ = layout.RemoveRecord(r.Role)
			} else {
				fmt.Fprintf(stderr, "%s (pid %d) did not stop; its record is kept: run codeye stop\n", r.Role, r.PID)
			}
		}
		fmt.Fprintln(stderr, "logs:", layout.LogDir())
		store.Close()
		if !ctrlUp && !agentUp {
			stopDB()
		}
		return nil, 1
	}
	if !ctrlUp {
		ctrlEnv := append(childEnv(append([]string{"PATH", "HOME", "LANG", "TZ", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME"}, fixtureControllerEnv...)...), "CODEYE_DATABASE_URL="+os.Getenv("CODEYE_DATABASE_URL"))
		cmd, rec, err := instance.Spawn(instance.RoleController, exe, ctrlArgs, ctrlEnv, layout.Root, layout.LogFile(instance.RoleController))
		if err != nil {
			if cmd != nil {
				_ = cmd.Wait()
			}
			return rollback(err.Error())
		}
		rec.Listen, rec.Args = ctrlListen, ctrlArgs
		started = append(started, &rec)
		if err := layout.WriteRecord(rec); err != nil {
			return rollback("cannot record the controller process")
		}
		ctrlWatch := watchChild(cmd)
		deadline := time.Now().Add(instanceTimings.ready)
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
			time.Sleep(instanceTimings.poll)
		}
		res.controllerPID = rec.PID
	}
	if !agentUp {
		cmd, arec, err := instance.Spawn(instance.RoleAgent, exe, agentArgv(cfg, layout, roots, enroll), childEnv(append([]string{"PATH", "LANG", "HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME"}, fixtureAgentEnv...)...), layout.Root, layout.LogFile(instance.RoleAgent))
		if err != nil {
			if cmd != nil {
				_ = cmd.Wait()
			}
			return rollback(err.Error())
		}
		arec.Args = desired
		started = append(started, &arec)
		if err := layout.WriteRecord(arec); err != nil {
			return rollback("cannot record the agent process")
		}
		agentWatch := watchChild(cmd)
		deadline := time.Now().Add(instanceTimings.enroll)
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
			time.Sleep(instanceTimings.poll)
		}
		res.agentPID = arec.PID
	}
	return res, 0
}

// controllerListenArgs are the controller flags of a service other machines
// reach: TLS or plain HTTP with the public origin, and the loopback address
// the local agent and the commands here use.
func controllerListenArgs(cfg *instance.Config) []string {
	var args []string
	switch {
	case cfg.TLS():
		args = append(args, "--tls-cert", cfg.TLSCert, "--tls-key", cfg.TLSKey)
	case cfg.PlainHTTP:
		args = append(args, "--plain-http")
	}
	for _, o := range cfg.PublicOrigins {
		args = append(args, "--public-origin", o)
	}
	if cfg.NeedsLocalListen() {
		args = append(args, "--local-listen", cfg.InternalListen())
	}
	return args
}

// ensureGitProviders registers the configured Git hosts as providers, so
// projects can be added by repository URL with nothing else set up.
func ensureGitProviders(ctx context.Context, s *postgres.Store, cfg *instance.Config, out io.Writer) error {
	for _, h := range cfg.GitHosts {
		if !github.HostAllowed(h, cfg.Offline, cfg.GitHosts) {
			continue
		}
		_, created, err := s.EnsureProvider(ctx, h)
		if err != nil {
			return fmt.Errorf("cannot register the Git host %s: %w", h, err)
		}
		if created {
			fmt.Fprintf(out, "registered the Git host %s\n", h)
		}
	}
	return nil
}

// ensurePoolWorkers lets the controller run as many attempts at once as the
// local agent may start (agent.max_workers): the pool's max_workers follows
// the configuration. An unchanged value appends no policy revision.
func ensurePoolWorkers(ctx context.Context, s *postgres.Store, cfg *instance.Config) error {
	n := cfg.Agent.MaxWorkers
	if _, _, err := s.SetPoolPolicy(ctx, postgres.PoolPolicyUpdate{Name: "default", MaxWorkers: &n}); err != nil {
		return fmt.Errorf("cannot set the worker pool size: %w", err)
	}
	return nil
}

// writeInstanceSetup records for the controller what the web app shows on
// the first screen: the model, Git, the GitHub account and the backup
// directory (no key, no token).
func writeInstanceSetup(cfg *instance.Config, layout instance.Layout, m *instance.ResolvedModel, gh []httpapi.SetupGitHub) error {
	deps := cfg.Agent.DependenciesEnabled()
	st := httpapi.InstanceSetup{SchemaVersion: "0.1", Git: cfg.Agent.Git != "", GitHosts: append([]string{}, cfg.GitHosts...), BackupDir: backupDirOf(cfg),
		GitHubConnections: gh, Offline: cfg.Offline, CABundle: cfg.CABundle, DependencyFetch: &deps}
	if m != nil {
		st.Model = &httpapi.SetupModel{ID: m.ID, Probe: instance.ModelProbeResult(layout, m)}
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return layout.WriteRunFile(httpapi.InstanceSetupFile, append(raw, '\n'))
}

// Listen ports tried when the default is busy and listen is not configured.
const (
	defaultPort  = 7331
	lastAutoPort = 7340
)

// probedListen is the address Inspect checks for a process without a
// record. A default address that another program uses is no problem (start
// and up move to the next free port), so only a configured one is probed.
func probedListen(cfg *instance.Config) string {
	if cfg.ListenExplicit() {
		return cfg.Listen
	}
	return ""
}

// addrFree reports whether a listener can bind addr now.
func addrFree(addr string) bool {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

// freeListen returns the first free loopback address on 7331..7340 ("" when none).
func freeListen() string {
	for p := defaultPort; p <= lastAutoPort; p++ {
		if instance.PortFree("127.0.0.1", p) {
			return "127.0.0.1:" + strconv.Itoa(p)
		}
	}
	return ""
}

// instanceUser returns the installation's user, creating it on first start.
func adminUser(ctx context.Context, s *postgres.Store, layout instance.Layout, out io.Writer) (string, error) {
	id, err := layout.ReadIdentity()
	if err != nil {
		return "", err
	}
	if id != nil {
		active, err := s.UserActive(ctx, id.UserID)
		if err != nil {
			return "", fmt.Errorf("cannot read the administrator user: %w", err)
		}
		if !active {
			return "", errors.New("the administrator user in admin.json does not exist in this database or is disabled; restore the matching backup or use a fresh state_dir")
		}
		return id.UserID, ensureAdmin(ctx, s, id.UserID)
	}
	u, err := s.CreateAccount(ctx, postgres.OperatorPrincipal(), postgres.NewAccount{DisplayName: "admin", IsAdmin: true})
	if err != nil {
		return "", fmt.Errorf("cannot create the administrator user: %w", err)
	}
	user := u.ID
	if err := layout.WriteIdentity(user); err != nil {
		return "", errors.New("cannot store admin.json")
	}
	fmt.Fprintln(out, "created the administrator user", user)
	return user, nil
}

// ensureAdmin makes the single service user an administrator
// (so it sees the account screens). An installation from before the role
// existed is promoted once; the change is audited as the operator.
func ensureAdmin(ctx context.Context, s *postgres.Store, user string) error {
	u, err := s.FindUser(ctx, user)
	if err != nil {
		return fmt.Errorf("cannot read the administrator user: %w", err)
	}
	if u.IsAdmin {
		return nil
	}
	if _, err := s.SetAdmin(ctx, postgres.OperatorPrincipal(), user, true); err != nil {
		return fmt.Errorf("cannot make the configured user an administrator: %w", err)
	}
	return nil
}

// sourceRootMapping is one configured source root resolved to its database ID.
type sourceRootMapping struct {
	instance.SourceRoot
	ID string
}

// reconcileRoots registers configured node mappings. A label registered
// with another path is an error:
// source roots are immutable.
func reconcileRoots(ctx context.Context, s *postgres.Store, cfg *instance.Config, out io.Writer) ([]sourceRootMapping, error) {
	var roots []sourceRootMapping
	for i := range cfg.SourceRoots {
		r := cfg.SourceRoots[i]
		rec, err := s.SourceRootByLabel(ctx, r.Label)
		for n := 2; r.Implicit && err == nil && rec.Path != r.Path; n++ {
			// The implicit home root keeps the first free label whose
			// registered path is the home directory (the home directory moved).
			r.Label = fmt.Sprintf("home-%d", n)
			cfg.SourceRoots[i].Label = r.Label
			rec, err = s.SourceRootByLabel(ctx, r.Label)
		}
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
		default:
			if st, err := os.Stat(r.Path); err != nil || !st.IsDir() {
				fmt.Fprintf(out, "note: source root %q (%s) is not an existing directory; its projects cannot be analyzed until it is back (or remove it from source_roots)\n", r.Label, r.Path)
			}
		}
		roots = append(roots, sourceRootMapping{SourceRoot: r, ID: rec.ID})
	}
	return roots, nil
}

// prepareEnrollment decides whether the local agent must enroll. An agent
// enrolled for other source roots, for a revoked node, or for a node this
// database does not know is retired (its state directory is renamed, never
// deleted) after checking that its journal holds no unfinished work. It
// returns true when an enrollment token file was written.
func prepareEnrollment(ctx context.Context, s *postgres.Store, layout instance.Layout, roots []sourceRootMapping, out io.Writer) (bool, error) {
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
func agentArgv(cfg *instance.Config, layout instance.Layout, roots []sourceRootMapping, enroll bool) []string {
	a := cfg.Agent
	name := a.Name
	if name == "" {
		name = "local"
	}
	args := []string{"agent", "--controller", cfg.Origin(), "--state-dir", layout.AgentDir(), "--name", name,
		"--autoscale", "--max-workers", strconv.Itoa(a.MaxWorkers), "--toolchain-path", a.ToolchainPath,
		// A local node takes work even when no analyzer is installed:
		// the tasks of missing tools are recorded as unsupported.
		"--advertise-without-baseline"}
	if enroll {
		args = append(args, "--enroll-token-file", layout.EnrollToken())
	}
	for _, r := range roots {
		args = append(args, "--source-root", r.ID+"="+r.Path)
	}
	// Each project's own choices, as far as the service offers them to its
	// owner, decide whether its source goes to the model and whether its
	// build scripts run, for folders and Git alike. The agent runs on the
	// controller's machine: folders and Git hosts the administrator adds in
	// the web are served without restarting it.
	args = append(args, "--project-analysis-policy", "--controller-sources")
	// A project folder must not hold CODEYE's own data, however it was
	// registered.
	args = append(args, "--protected-dir", layout.Root, "--protected-dir", backupDirOf(cfg))
	if cfg.Offline {
		args = append(args, "--offline")
	}
	if cfg.CABundle != "" {
		args = append(args, "--ca-bundle", cfg.CABundle)
	}
	if a.Git != "" {
		args = append(args, "--git", a.Git)
		for _, h := range cfg.GitHosts {
			if github.HostAllowed(h, cfg.Offline, cfg.GitHosts) {
				args = append(args, "--git-host", h)
			}
		}
		args = append(args, "--git-credential-file", github.PlatformCredentialFile(layout.Root))
	}
	if a.Go != "" {
		args = append(args, "--go", a.Go)
	}
	if a.GoModCache != "" {
		args = append(args, "--go-mod-cache", a.GoModCache)
	}
	// Dependencies are fetched unless the configuration turns that off;
	// each project's own choice still decides for its runs.
	if a.DependenciesEnabled() {
		args = append(args, "--dependency-fetch", "--dependency-cache-dir", a.DependencyCache(layout))
		if d := a.Dependencies; d != nil {
			for _, f := range []struct{ flag, value string }{{"--go-proxy", d.GoProxy}, {"--go-sumdb", d.GoSumDB}, {"--go-private", d.GoPrivate}, {"--go-noproxy", d.GoNoProxy}, {"--go-nosumdb", d.GoNoSumDB}} {
				if f.value != "" {
					args = append(args, f.flag, f.value)
				}
			}
			if d.TimeoutSeconds > 0 {
				args = append(args, "--dependency-timeout", strconv.Itoa(d.TimeoutSeconds)+"s")
			}
			if d.TotalTimeoutSeconds > 0 {
				args = append(args, "--dependency-total-timeout", strconv.Itoa(d.TotalTimeoutSeconds)+"s")
			}
			if d.MaxDownloadBytes > 0 {
				args = append(args, "--dependency-max-bytes", strconv.FormatInt(d.MaxDownloadBytes, 10))
			}
			if d.NpmRegistry != "" {
				args = append(args, "--npm-registry", d.NpmRegistry)
			}
			if d.PipIndexURL != "" {
				args = append(args, "--pip-index-url", d.PipIndexURL)
			}
		}
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
	// The model and its credential file (instance_model.go).
	args = append(args, modelAgentArgs(cfg, layout)...)
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
func waitGone(r *instance.Record, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if r.State() != instance.ProcRunning {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(instanceTimings.poll)
	}
}
