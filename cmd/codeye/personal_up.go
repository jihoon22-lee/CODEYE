package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// `codeye up [PATH|URL]` (docs/GUIDE.md): from nothing to a signed-in
// browser. It writes a minimal configuration when there is none, runs the
// managed PostgreSQL, the controller and the agent (reusing whatever already
// runs), registers the folder or Git repository as a project and queues its
// analysis, then prints a single-use login link to that project and opens
// the browser.
// `codeye open` only issues and opens a new link.

// upRunBody is the run request of `codeye up PATH` (the web app's default).
const upRunBody = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`

func personalUp(ctx context.Context, args []string, out, stderr io.Writer) int {
	var configFlag, binDir string
	var noOpen, noProbe, offline bool
	var serveFlags personal.ServeEdit
	listenSet := false
	pull := pullOptions{wait: time.Hour}
	target, rest := splitTarget(args)
	if !parse("up", rest, stderr, func(fs *flag.FlagSet) {
		fs.Func("listen", "", func(v string) error { serveFlags.Listen, listenSet = v, true; return nil })
		fs.StringVar(&serveFlags.TLSCert, "tls-cert", "", "")
		fs.StringVar(&serveFlags.TLSKey, "tls-key", "", "")
		fs.StringVar(&serveFlags.PublicOrigin, "public-origin", "", "")
		fs.BoolVar(&serveFlags.PlainHTTP, "plain-http", false, "")
		fs.StringVar(&configFlag, "config", "", "")
		fs.StringVar(&binDir, "postgres-bin-dir", "", "")
		fs.BoolVar(&noOpen, "no-open", false, "")
		fs.BoolVar(&noProbe, "no-probe", false, "")
		fs.BoolVar(&offline, "offline", false, "")
		fs.Var(&pull.comment, "comment", "")
		fs.BoolVar(&pull.dryRunComment, "dry-run-comment", false, "")
		fs.DurationVar(&pull.wait, "comment-wait", time.Hour, "")
	}) {
		return 2
	}
	if binDir != "" {
		abs, err := filepath.Abs(binDir)
		if err != nil {
			fmt.Fprintln(stderr, "--postgres-bin-dir cannot be resolved")
			return 2
		}
		binDir = abs
	}
	if err := personal.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	path, err := personalConfigPath(configFlag)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if created, err := personal.EnsureFile(path); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	} else if created {
		fmt.Fprintln(out, "wrote a new configuration:", path)
	}
	cfg, err := personal.Load(path, knownTools())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if binDir != "" && cfg.Managed() && cfg.Database.PostgresBinDir != binDir {
		if cfg, err = personal.EditFile(path, knownTools(), personal.ConfigEdit{PostgresBinDir: binDir}); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintln(out, "saved database.postgres_bin_dir in", path)
	}
	if offline && !cfg.Offline {
		// Saved, so that a later codeye start stays offline too.
		if cfg, err = personal.EditFile(path, knownTools(), personal.ConfigEdit{Offline: true}); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintln(out, "saved \"offline\": true in", path)
	}
	if listenSet || serveFlags.TLSCert != "" || serveFlags.TLSKey != "" || serveFlags.PublicOrigin != "" || serveFlags.PlainHTTP {
		// How the service listens is saved, so that codeye start and the
		// user service serve the same address.
		if !listenSet {
			serveFlags.Listen = cfg.Listen
		}
		for _, p := range []*string{&serveFlags.TLSCert, &serveFlags.TLSKey} {
			if *p != "" {
				if abs, err := filepath.Abs(*p); err == nil {
					*p = abs
				}
			}
		}
		if cfg, err = personal.EditFile(path, knownTools(), personal.ConfigEdit{Serve: &serveFlags}); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintln(out, "saved how the service listens in", path)
	}
	if cfg.PlainHTTP {
		fmt.Fprintln(out, "plain HTTP on", cfg.Listen, "(plain_http): sign-in cookies and passwords travel unencrypted on this network")
	}
	layout := completeConfig(cfg)
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
	var proj *upTarget
	pr, isPR, perr := pullRequestTarget(cfg, target)
	if (pull.comment.set || pull.dryRunComment) && !isPR {
		fmt.Fprintln(stderr, "--comment and --dry-run-comment need a pull-request address: codeye up https://github.com/OWNER/REPO/pull/NUMBER")
		return 2
	}
	switch {
	case isPR:
		if proj, err = pr, perr; err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	case target != "" && isGitURL(target):
		if proj, err = gitTarget(cfg, target); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	case target != "":
		if proj, cfg, err = resolveTarget(path, cfg, target, out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		layout = completeConfig(cfg)
	}
	up, code := bringUp(ctx, cfg, layout, bringUpOptions{postgresBinDir: binDir, noProbe: noProbe}, out, stderr)
	if up == nil {
		return code
	}
	defer up.store.Close()
	if up.already {
		fmt.Fprintf(out, "CODEYE is already running at %s (controller pid %d, agent pid %d)\n", cfg.ServiceOrigin(), up.controllerPID, up.agentPID)
	} else {
		fmt.Fprintf(out, "CODEYE is running at %s (controller pid %d, agent pid %d); logs in %s\n", cfg.ServiceOrigin(), up.controllerPID, up.agentPID, layout.LogDir())
	}
	if cfg.Public() {
		fmt.Fprintf(out, "Other people open %s; add them under 관리 > 사용자 and send them their sign-in link.\n", cfg.ServiceOrigin())
	}
	if cfg.WebDir == "" {
		fmt.Fprintf(out, "web UI not built - run `cd web && npm ci && npm run build` next to the codeye binary (or set web_dir); API is up at %s/api/v1\n", cfg.Origin())
	}
	opCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	fragment := url.Values{}
	runID := ""
	if proj != nil {
		projectID, run, err := analyzeTarget(opCtx, up, cfg, layout, proj, pull, out)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		runID = run
		fragment.Set("project", projectID)
		fragment.Set("run", runID)
	}
	code = signIn(opCtx, up.store, up.user, cfg.ServiceOrigin(), fragment, noOpen, out, stderr)
	if code != 0 || !pull.dryRunComment {
		return code
	}
	return printCommentPreview(ctx, up.store, layout, up.user, runID, pull.wait, out, stderr)
}

func personalOpen(ctx context.Context, args []string, out, stderr io.Writer) int {
	var noOpen bool
	cfg, layout, ok := loadPersonalConfig("open", args, stderr, func(fs *flag.FlagSet) { fs.BoolVar(&noOpen, "no-open", false, "") })
	if !ok {
		return 2
	}
	if err := personal.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	state := layout.Inspect(probedListen(cfg), false)
	if !state.Running(personal.RoleController) {
		fmt.Fprintln(stderr, "CODEYE is not running; run codeye up")
		return 1
	}
	id, err := layout.ReadIdentity()
	if err != nil || id == nil {
		fmt.Fprintln(stderr, "no administrator user in", layout.IdentityFile(), "- run codeye up")
		return 1
	}
	opCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if _, err := personalDatabase(opCtx, cfg, layout, dbOptions{notices: stderr}); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	store, err := openStore(opCtx)
	if err != nil {
		fmt.Fprintln(stderr, "database unavailable:", err)
		return 1
	}
	defer store.Close()
	return signIn(opCtx, store, id.UserID, cfg.ServiceOrigin(), nil, noOpen, out, stderr)
}

// signIn issues a login link, prints it and opens the browser on it. The
// fragment carries the deep link (project, run) after the launch token.
func signIn(ctx context.Context, s *postgres.Store, user, origin string, fragment url.Values, noOpen bool, out, stderr io.Writer) int {
	link, err := issueLoginURL(ctx, s, user, origin)
	if err != nil {
		fmt.Fprintln(stderr, "no login link:", err)
		return 1
	}
	if len(fragment) > 0 {
		link += "&" + fragment.Encode()
	}
	fmt.Fprintf(out, "\nOpen CODEYE (signed in; the link works once, within %d minutes):\n  %s\n\n", int(launchTTL/time.Minute), link)
	if !noOpen {
		if opener, err := openBrowser(link); err != nil {
			fmt.Fprintln(out, "could not open a browser ("+err.Error()+"); open the link above")
		} else {
			fmt.Fprintln(out, "opened the browser with", opener)
		}
	}
	fmt.Fprintf(out, "The browser stays signed in for %d days. New link: codeye open. Stop: codeye stop.\n", int(httpapi.ServiceSessionTTL/(24*time.Hour)))
	return 0
}

// splitTarget takes the one positional argument (PATH or URL) from args,
// wherever it is among the flags.
func splitTarget(args []string) (string, []string) {
	var target string
	var rest []string
	valueFlags := map[string]bool{}
	for _, name := range []string{"config", "postgres-bin-dir", "comment-wait", "listen", "tls-cert", "tls-key", "public-origin"} {
		valueFlags["-"+name], valueFlags["--"+name] = true, true
	}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) && target == "" {
				target = args[i+1]
				rest = append(rest, args[i+2:]...)
			} else {
				rest = append(rest, args[i+1:]...)
			}
			return target, rest
		case strings.HasPrefix(a, "-"):
			rest = append(rest, a)
			if valueFlags[a] && i+1 < len(args) {
				rest = append(rest, args[i+1])
				i++
			}
		case target == "":
			target = a
		default:
			// A second positional argument is left for the flag parser to reject.
			rest = append(rest, a)
		}
	}
	return target, rest
}

// isGitURL reports whether the argument of `codeye up` names a repository
// rather than a folder: a URL, an scp-like address, or HOST/OWNER/REPO that
// is not an existing path.
func isGitURL(s string) bool {
	if strings.Contains(s, "://") || strings.HasPrefix(s, "git@") {
		return true
	}
	if _, err := os.Stat(s); err == nil {
		return false
	}
	first, _, _ := strings.Cut(s, "/")
	return strings.Count(strings.TrimSuffix(s, "/"), "/") >= 2 && strings.Contains(first, ".") && !strings.HasPrefix(s, ".")
}

// upTarget is the folder or repository `codeye up` analyzes: a folder under
// the source root at rootPath, or a repository (repoURL, branch "" for the
// default branch) of an approved Git host.
type upTarget struct {
	path, rootPath, relative string
	repoURL, branch, host    string
	// pullRequest is the pull-request number of a /pull/N address.
	pullRequest int
}

func (t *upTarget) git() bool { return t.repoURL != "" }

// shown is how messages name the target.
func (t *upTarget) shown() string {
	if t.pullRequest > 0 {
		return fmt.Sprintf("%s/pull/%d", t.repoURL, t.pullRequest)
	}
	if t.git() {
		if t.branch != "" {
			return t.repoURL + " (branch " + t.branch + ")"
		}
		return t.repoURL
	}
	return t.path
}

// repoURLOrPath names the project (not the pull request) of the target.
func (t *upTarget) repoURLOrPath() string {
	if t.pullRequest > 0 {
		return t.repoURL
	}
	return t.shown()
}

// gitTarget validates a repository argument: an HTTPS URL of a host in
// git_hosts (a /tree/BRANCH browser address names the branch).
func gitTarget(cfg *personal.Config, target string) (*upTarget, error) {
	repo, branch, err := domain.SplitRepositoryURL(target)
	if err != nil {
		if errors.Is(err, domain.ErrUnsupported) {
			return nil, errors.New("only HTTPS repository URLs are supported, for example https://github.com/OWNER/REPO")
		}
		return nil, errors.New("not a repository URL: use https://HOST/OWNER/REPO (optionally /tree/BRANCH)")
	}
	host, _ := domain.RepositoryHost(repo)
	if !slices.Contains(cfg.GitHosts, host) {
		if cfg.Offline {
			return nil, fmt.Errorf("the Git host %s is not in git_hosts (offline mode allows no host by default); add the internal Git host to git_hosts", host)
		}
		return nil, fmt.Errorf("the Git host %s is not in git_hosts (default github.com); add it to the configuration to analyze its repositories", host)
	}
	return &upTarget{repoURL: repo, branch: branch, host: host}, nil
}

// resolveTarget maps PATH to a configured source root (the root itself or a
// folder under it) or adds the folder as a new source root to the
// configuration file. It returns the (possibly changed) configuration.
func resolveTarget(configPath string, cfg *personal.Config, target string, out io.Writer) (*upTarget, *personal.Config, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return nil, nil, errors.New("the path cannot be resolved")
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, nil, fmt.Errorf("%s does not exist", abs)
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return nil, nil, fmt.Errorf("%s is not a folder", real)
	}
	for _, dir := range []string{cfg.StateDir, backupDirOf(cfg)} {
		if pathWithin(dir, real) || pathWithin(real, dir) {
			return nil, nil, fmt.Errorf("%s contains or is inside the CODEYE data directory %s; pass a project folder", real, dir)
		}
	}
	best := -1
	rel := ""
	for i, r := range cfg.SourceRoots {
		if !pathWithin(real, r.Path) || (best >= 0 && len(r.Path) <= len(cfg.SourceRoots[best].Path)) {
			continue
		}
		p, err := filepath.Rel(r.Path, real)
		if err != nil {
			continue
		}
		p = filepath.ToSlash(p)
		if domain.ValidateRelativeSourcePath(p) != nil {
			continue
		}
		best, rel = i, p
	}
	if best >= 0 {
		return &upTarget{path: real, rootPath: cfg.SourceRoots[best].Path, relative: rel}, cfg, nil
	}
	label := uniqueLabel(cfg, filepath.Base(real))
	next, err := personal.EditFile(configPath, knownTools(), personal.ConfigEdit{AddRoot: &personal.SourceRoot{Label: label, Path: real}})
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(out, "added source root %q (%s) to %s\n", label, real, configPath)
	return &upTarget{path: real, rootPath: real, relative: "."}, next, nil
}

// pathWithin reports whether p is base or below it.
func pathWithin(p, base string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// uniqueLabel is the folder name, made unique among the configured labels.
func uniqueLabel(cfg *personal.Config, base string) string {
	base = strings.TrimSpace(base)
	if base == "" || base == string(filepath.Separator) || base == "." {
		base = "folder"
	}
	if utf8.RuneCountInString(base) > 90 {
		base = string([]rune(base)[:90])
	}
	taken := map[string]bool{}
	for _, r := range cfg.SourceRoots {
		taken[r.Label] = true
	}
	label := base
	for n := 2; taken[label]; n++ {
		label = fmt.Sprintf("%s-%d", base, n)
	}
	return label
}

// analyzeTarget finds the project of the target folder or repository (or
// registers it) and queues a run, unless one is still queued or running. A
// pull request always gets a new run on the head GitHub shows now.
func analyzeTarget(ctx context.Context, up *running, cfg *personal.Config, layout personal.Layout, t *upTarget, pull pullOptions, out io.Writer) (string, string, error) {
	var src domain.Source
	var name string
	if t.git() {
		if cfg.Agent.Git == "" {
			return "", "", errors.New("git was not found on this machine (see the tool table above); install git (Ubuntu: `sudo apt install git`) or set agent.git, then run codeye up again")
		}
		p, _, err := up.store.EnsureProvider(ctx, t.host)
		if err != nil {
			return "", "", fmt.Errorf("cannot register the Git host %s: %w", t.host, err)
		}
		src = domain.Source{Kind: domain.SourceGit, ProviderID: p.ID, RepositoryURL: t.repoURL, Branch: t.branch}
		name = domain.RepositoryName(t.repoURL)
	} else {
		for _, r := range up.roots {
			if r.Path == t.rootPath {
				src = domain.Source{Kind: domain.SourceLocal, SourceRootID: r.ID, RelativePath: t.relative}
			}
		}
		if src.Kind == "" {
			return "", "", fmt.Errorf("the source root of %s is not registered", t.path)
		}
		name = filepath.Base(t.path)
	}
	var project *postgres.ProjectSummary
	for cursor := ""; project == nil; {
		page, err := up.store.ListProjects(ctx, up.user, cursor, 100)
		if err != nil {
			return "", "", fmt.Errorf("cannot list projects: %w", err)
		}
		for i, p := range page.Items {
			var have domain.Source
			if json.Unmarshal(p.Source, &have) == nil && domain.SameSource(have, src) {
				project = &page.Items[i]
				break
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	var projectID string
	if project != nil {
		projectID, name = project.ID, project.Name
		// A queued or executing run is shown instead of queueing another; a
		// cancelling or recovery_required run gets a new run beside it.
		if lr := project.LastRun; t.pullRequest == 0 && lr != nil && slices.Contains([]domain.RunStatus{domain.RunQueued, domain.RunPreparing, domain.RunRunning, domain.RunFinalizing}, lr.Status) {
			fmt.Fprintf(out, "project %q (%s): its analysis is already %s (run %s)\n", name, t.shown(), lr.Status, lr.ID)
			return projectID, lr.ID, nil
		}
	} else {
		p, err := up.store.RegisterProject(ctx, up.user, postgres.ProjectRegistration{Name: name, Source: src})
		if err != nil {
			return "", "", fmt.Errorf("cannot register the project: %w", err)
		}
		projectID, name = p.ID, p.Name
		fmt.Fprintf(out, "registered project %q (%s)\n", name, t.repoURLOrPath())
	}
	if t.pullRequest > 0 {
		run, err := queuePullRequest(ctx, up, layout, projectID, t, pull, out)
		return projectID, run, err
	}
	run, err := up.store.Enqueue(ctx, up.user, projectID, "up-"+domain.NewID(), json.RawMessage(upRunBody))
	if err != nil {
		return "", "", fmt.Errorf("cannot queue the analysis: %w", err)
	}
	fmt.Fprintf(out, "queued an analysis of %q (run %s); the browser shows its progress and the report\n", name, run.ID)
	return projectID, run.ID, nil
}

// applyDetectedTools fills the analyzer and toolchain paths the
// configuration leaves unset with the tools found on this machine, shows what
// was found and what is missing, and records the result in the state
// directory. Explicit agent settings always win; agent.auto_detect false
// turns detection off.
func applyDetectedTools(ctx context.Context, cfg *personal.Config, out io.Writer) toolfind.Result {
	res, err := prepareTools(ctx, cfg, out)
	if err != nil {
		fmt.Fprintln(out, "tool detection:", err)
	}
	return res
}

// openBrowser opens url in the desktop browser and names the program used
// (tests replace it). On WSL it uses the Windows browser.
var openBrowser = func(link string) (string, error) {
	for _, argv := range browserCommands() {
		exe, err := exec.LookPath(argv[0])
		if err != nil {
			continue
		}
		cmd := exec.Command(exe, append(argv[1:], link)...)
		if err := cmd.Start(); err != nil {
			continue
		}
		go func() { _ = cmd.Wait() }()
		return argv[0], nil
	}
	return "", errors.New("no browser opener found")
}

// browserCommands are the openers tried in order: $BROWSER, then wslview
// and explorer.exe on WSL, then xdg-open.
func browserCommands() [][]string {
	var cmds [][]string
	if b := strings.TrimSpace(os.Getenv("BROWSER")); b != "" && !strings.ContainsAny(b, " \t:%") {
		cmds = append(cmds, []string{b})
	}
	if onWSL() {
		cmds = append(cmds, []string{"wslview"}, []string{"explorer.exe"})
	}
	return append(cmds, []string{"xdg-open"})
}

func onWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	raw, err := os.ReadFile("/proc/sys/kernel/osrelease")
	return err == nil && strings.Contains(strings.ToLower(string(raw)), "microsoft")
}
