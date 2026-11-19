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
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// `codeye up [PATH]` (docs/GUIDE.md): from nothing to a signed-in browser.
// It writes a minimal configuration when there is none, runs the managed
// PostgreSQL, the controller and the agent (reusing whatever already runs),
// registers PATH as a project and queues its analysis, then prints a
// single-use login link to that project and opens the browser.
// `codeye open` only issues and opens a new link.

// upRunBody is the run request of `codeye up PATH` (the web app's default).
const upRunBody = `{"profile":"deep-review","source_policy":"latest_at_acquire","scope":{"kind":"full"}}`

func personalUp(ctx context.Context, args []string, out, stderr io.Writer) int {
	var configFlag, binDir string
	var noOpen bool
	target, rest := splitTarget(args)
	if !parse("up", rest, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&configFlag, "config", "", "")
		fs.StringVar(&binDir, "postgres-bin-dir", "", "")
		fs.BoolVar(&noOpen, "no-open", false, "")
	}) {
		return 2
	}
	if isGitURL(target) {
		fmt.Fprintln(stderr, "Git URLs are not wired into codeye up yet. Clone the repository and analyze the folder:")
		fmt.Fprintln(stderr, "  git clone "+target+" DIR && codeye up DIR")
		fmt.Fprintln(stderr, "A Git project can also be registered in the web UI (프로젝트 등록 → Git) when an agent with --git runs; see docs/GITHUB.md.")
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
	if target != "" {
		if proj, cfg, err = resolveTarget(path, cfg, target, out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		layout = completeConfig(cfg)
	}
	applyDetectedTools(cfg, out)
	prepareModel(cfg, out)

	up, code := bringUp(ctx, cfg, layout, bringUpOptions{postgresBinDir: binDir}, out, stderr)
	if up == nil {
		return code
	}
	defer up.store.Close()
	if up.already {
		fmt.Fprintf(out, "CODEYE is already running at %s (controller pid %d, agent pid %d)\n", cfg.Origin(), up.controllerPID, up.agentPID)
	} else {
		fmt.Fprintf(out, "CODEYE is running at %s (controller pid %d, agent pid %d); logs in %s\n", cfg.Origin(), up.controllerPID, up.agentPID, layout.LogDir())
	}
	if cfg.WebDir == "" {
		fmt.Fprintf(out, "web UI not built - run `cd web && npm ci && npm run build` next to the codeye binary (or set web_dir); API is up at %s/api/v1\n", cfg.Origin())
	}
	opCtx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	fragment := url.Values{}
	if proj != nil {
		projectID, runID, err := analyzeTarget(opCtx, up, proj, out)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		fragment.Set("project", projectID)
		fragment.Set("run", runID)
	}
	return signIn(opCtx, up.store, up.user, cfg.Origin(), fragment, noOpen, out, stderr)
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
	state := layout.Inspect(cfg.Listen, false)
	if !state.Running(personal.RoleController) {
		fmt.Fprintln(stderr, "CODEYE is not running; run codeye up")
		return 1
	}
	id, err := layout.ReadIdentity()
	if err != nil || id == nil {
		fmt.Fprintln(stderr, "no personal user in", layout.IdentityFile(), "- run codeye up")
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
	return signIn(opCtx, store, id.UserID, cfg.Origin(), nil, noOpen, out, stderr)
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
	fmt.Fprintf(out, "The browser stays signed in for %d days. New link: codeye open. Stop: codeye stop.\n", int(httpapi.PersonalSessionTTL/(24*time.Hour)))
	return 0
}

// splitTarget takes the one positional argument (PATH or URL) from args,
// wherever it is among the flags.
func splitTarget(args []string) (string, []string) {
	var target string
	var rest []string
	valueFlags := map[string]bool{"-config": true, "--config": true, "-postgres-bin-dir": true, "--postgres-bin-dir": true}
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

func isGitURL(s string) bool {
	return strings.Contains(s, "://") || strings.HasPrefix(s, "git@")
}

// upTarget is the folder `codeye up PATH` analyzes.
type upTarget struct {
	path, label, relative string
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
		return &upTarget{path: real, label: cfg.SourceRoots[best].Label, relative: rel}, cfg, nil
	}
	label := uniqueLabel(cfg, filepath.Base(real))
	next, err := personal.EditFile(configPath, knownTools(), personal.ConfigEdit{AddRoot: &personal.SourceRoot{Label: label, Path: real}})
	if err != nil {
		return nil, nil, err
	}
	fmt.Fprintf(out, "added source root %q (%s) to %s\n", label, real, configPath)
	return &upTarget{path: real, label: label, relative: "."}, next, nil
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

// analyzeTarget finds the project of the target folder (or registers it)
// and queues a run, unless one is still queued or running.
func analyzeTarget(ctx context.Context, up *running, t *upTarget, out io.Writer) (string, string, error) {
	rootID := ""
	for _, r := range up.roots {
		if r.Label == t.label {
			rootID = r.ID
		}
	}
	if rootID == "" {
		return "", "", fmt.Errorf("source root %q is not registered", t.label)
	}
	var project *postgres.ProjectSummary
	for cursor := ""; project == nil; {
		page, err := up.store.ListProjects(ctx, up.user, cursor, 100)
		if err != nil {
			return "", "", fmt.Errorf("cannot list projects: %w", err)
		}
		for i, p := range page.Items {
			var src domain.Source
			if json.Unmarshal(p.Source, &src) == nil && src.Kind == domain.SourceLocal && src.SourceRootID == rootID && src.RelativePath == t.relative {
				project = &page.Items[i]
				break
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	var projectID, name string
	if project != nil {
		projectID, name = project.ID, project.Name
		if lr := project.LastRun; lr != nil && !lr.Status.Terminal() {
			fmt.Fprintf(out, "project %q (%s): its analysis is already %s (run %s)\n", name, t.path, lr.Status, lr.ID)
			return projectID, lr.ID, nil
		}
	} else {
		name = filepath.Base(t.path)
		p, err := up.store.RegisterProject(ctx, up.user, postgres.ProjectRegistration{Name: name,
			Source: domain.Source{Kind: domain.SourceLocal, SourceRootID: rootID, RelativePath: t.relative}})
		if err != nil {
			return "", "", fmt.Errorf("cannot register the project: %w", err)
		}
		projectID, name = p.ID, p.Name
		fmt.Fprintf(out, "registered project %q (%s)\n", name, t.path)
	}
	run, err := up.store.Enqueue(ctx, up.user, projectID, "up-"+domain.NewID(), json.RawMessage(upRunBody))
	if err != nil {
		return "", "", fmt.Errorf("cannot queue the analysis: %w", err)
	}
	fmt.Fprintf(out, "queued an analysis of %q (run %s); the browser shows its progress and the report\n", name, run.ID)
	return projectID, run.ID, nil
}

// applyDetectedTools fills the analyzer and toolchain paths the
// configuration leaves empty; agent settings always win. The Go toolchain
// is taken from PATH; other tools run only when agent.tools names them.
func applyDetectedTools(cfg *personal.Config, out io.Writer) {
	if cfg.Agent.Go == "" {
		if p, err := exec.LookPath("go"); err == nil {
			if real, err := filepath.EvalSymlinks(p); err == nil && filepath.IsAbs(real) {
				cfg.Agent.Go = filepath.Clean(real)
				fmt.Fprintln(out, "Go toolchain:", cfg.Agent.Go, "(from PATH)")
			}
		}
	}
}

// prepareModel sets up the review model of the agent. The model comes from
// agent.model; without it the review stage is recorded as skipped and the
// static analysis runs.
func prepareModel(cfg *personal.Config, out io.Writer) {
	if cfg.Agent.Model == nil {
		fmt.Fprintln(out, "no review model configured (agent.model): reports hold static analysis only")
	}
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
