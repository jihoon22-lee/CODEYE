package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// Instance is the installation the controller serves (`codeye up` always
// passes it; `codeye serve --state-dir` for a hand-started controller).
// Every method is safe on a nil Instance.
type Instance struct {
	// StateDir is the installation's state directory. The tool detection
	// (tools.json) and the setup summary (run/personal-setup.json) are read
	// from it on each request, because `codeye up` rewrites them while the
	// controller keeps running; no project may contain it. The users' GitHub
	// tokens (secrets/github-users/<user>.json) are read from it for each
	// GitHub API call.
	StateDir string
	// GitHubAPI is the GitHub REST API base; "" is github.APIBase() (tests
	// point it at a fake).
	GitHubAPI string
}

// InstanceSetupFile is the setup summary `codeye up`/`start` writes under
// <state>/run for the controller (the file name predates the shared
// service and stays for existing installations).
const InstanceSetupFile = "personal-setup.json"

// InstanceSetup is what the installation tells the web app about itself. It
// holds no credential and no key.
type InstanceSetup struct {
	SchemaVersion string `json:"schema_version"`
	// Model is the review model, nil when none is configured.
	Model *SetupModel `json:"model"`
	// Git reports a usable git executable; GitHosts the HTTPS hosts of the
	// configuration (the administrator may approve more in the web).
	Git      bool     `json:"git"`
	GitHosts []string `json:"git_hosts"`
	// BackupDir is where backups go; no project may contain it.
	BackupDir string `json:"backup_dir,omitempty"`
	// GitHub is the token discovery `codeye up` did for the administrator
	// who runs it; nil from older setups.
	GitHub *SetupGitHub `json:"github,omitempty"`
	// Offline is the configuration's offline mode: the controller makes no
	// GitHub API call (no pull-request pin, preview lookup, comment or token
	// check).
	Offline bool `json:"offline,omitempty"`
	// DependencyFetch is the local agent's dependency switch (nil from
	// older setups: unknown).
	DependencyFetch *bool `json:"dependency_fetch,omitempty"`
}

// offline reports the installation's offline mode from the setup summary.
func (p *Instance) offline() bool {
	s := p.readSetup()
	return s != nil && s.Offline
}

// SetupGitHub is the GitHub token discovery of `codeye up`: whether it is
// on, whether a token was found and whose it is. It never holds the token.
type SetupGitHub struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Token   bool   `json:"token"`
	Account string `json:"account,omitempty"`
	// Source is GH_TOKEN, GITHUB_TOKEN or gh.
	Source string `json:"source,omitempty"`
	// Reason is why no token is available, or why the account is unknown.
	Reason string `json:"reason,omitempty"`
}

// SetupModel is the configured review model.
type SetupModel struct {
	ID string `json:"id"`
	// Probe is the last `codeye model probe` of this configuration:
	// verified, failed or not_probed.
	Probe string `json:"probe"`
}

// Model probe states of SetupModel.Probe.
const (
	ProbeVerified  = "verified"
	ProbeFailed    = "failed"
	ProbeNotProbed = "not_probed"
)

// readSetup reads the setup summary; nil when there is none or it does not
// decode (the screen then shows "unknown", never a guess).
func (p *Instance) readSetup() *InstanceSetup {
	if p == nil || p.StateDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(p.StateDir, "run", InstanceSetupFile))
	if err != nil || len(raw) > 64<<10 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s InstanceSetup
	if dec.Decode(&s) != nil || s.SchemaVersion != "0.1" {
		return nil
	}
	if s.GitHosts == nil {
		s.GitHosts = []string{}
	}
	return &s
}

// setupView is GET /setup: what the signed-in user can do here and why not.
type setupView struct {
	IsAdmin bool `json:"is_admin"`
	// ServiceOrigin is the address to share with other people (the public
	// origin); null when the service listens on loopback only.
	ServiceOrigin    *string          `json:"service_origin"`
	Model            *setupModelView  `json:"model"`
	Git              *setupGitView    `json:"git"`
	ReadableFolders  []string         `json:"readable_folders"`
	AnalysisDefaults analysisDefaults `json:"analysis_defaults"`
	Available        availability     `json:"available"`
	// Tools are the detected tools; paths only for administrators.
	Tools *toolfind.Result `json:"tools"`
	// GitHub is the signed-in user's own GitHub connection (no token).
	GitHub  setupGitHubView `json:"github"`
	Offline bool            `json:"offline"`
}

// availability says, per thing a user may want, whether it is available to
// them and the reason when not.
type availability struct {
	GitURL            availableItem `json:"git_url"`
	LocalPath         availableItem `json:"local_path"`
	PullRequests      availableItem `json:"pull_requests"`
	GitHubToken       availableItem `json:"github_token"`
	ModelReview       availableItem `json:"model_review"`
	BuildScripts      availableItem `json:"build_scripts"`
	FetchDependencies availableItem `json:"fetch_dependencies"`
}

type availableItem struct {
	Available bool `json:"available"`
	// Reason: no_git_host, no_allowed_folder, offline, no_state_dir,
	// not_offered (the administrator's switch), model_not_configured,
	// node_disabled, unknown.
	Reason *string `json:"reason"`
}

func yes() availableItem { return availableItem{Available: true} }
func no(reason string) availableItem {
	return availableItem{Reason: &reason}
}

type setupGitHubView struct {
	// Enabled: this user can store a token here (a state directory, not
	// offline).
	Enabled bool    `json:"enabled"`
	Host    string  `json:"host"`
	Token   bool    `json:"token"`
	Account *string `json:"account"`
	// Source is web (pasted in the account screen) or, for the
	// administrator who runs the service, GH_TOKEN, GITHUB_TOKEN or gh.
	Source *string `json:"source"`
	Reason *string `json:"reason"`
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// analysisDefaults are the analysis choices a new project of this user gets,
// by source kind (the service's offer applied).
type analysisDefaults struct {
	Local domain.AnalysisChoices `json:"local"`
	Git   domain.AnalysisChoices `json:"git"`
}

type setupModelView struct {
	Configured bool    `json:"configured"`
	ID         *string `json:"id"`
	Probe      *string `json:"probe"`
}

type setupGitView struct {
	Available bool     `json:"available"`
	Hosts     []string `json:"hosts"`
}

// offered applies the service's offer to a project's default choices.
func offered(c domain.AnalysisChoices, o nodeproto.AnalysisOffer) domain.AnalysisChoices {
	c.SendSourceToModel = c.SendSourceToModel && o.ModelReview
	c.RunBuildScripts = c.RunBuildScripts && o.BuildScripts
	return c
}

// getSetup answers, for the signed-in user, what this service can do for
// them: Git hosts, the folders on the server they may analyze by path,
// their own GitHub connection, whether the model review, build scripts and
// dependency fetching are available to their projects (and why not), and
// the model and tools. Administrators also see tool paths.
func (s *Server) getSetup(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	offer, err := s.store.OfferFor(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	roots, err := s.store.GrantedRootPaths(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	provs, err := s.store.ListProviders(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	st := s.inst.readSetup()
	v := setupView{IsAdmin: q.session.IsAdmin, ServiceOrigin: optString(s.pub.origin), ReadableFolders: []string{}, Offline: st != nil && st.Offline,
		AnalysisDefaults: analysisDefaults{offered(domain.DefaultAnalysisChoices(domain.SourceLocal), offer), offered(domain.DefaultAnalysisChoices(domain.SourceGit), offer)}}
	for _, r := range roots {
		v.ReadableFolders = append(v.ReadableFolders, r.Path)
	}
	hosts := []string{}
	for _, p := range provs {
		hosts = append(hosts, p.Host)
	}
	gitExe := st == nil || st.Git
	v.Git = &setupGitView{Available: gitExe && len(hosts) > 0, Hosts: hosts}
	a := &v.Available
	a.GitURL, a.LocalPath, a.PullRequests, a.GitHubToken = yes(), yes(), yes(), yes()
	if !v.Git.Available {
		a.GitURL = no("no_git_host")
	}
	if len(roots) == 0 {
		a.LocalPath = no("no_allowed_folder")
	}
	gh := setupGitHubView{Enabled: true, Host: github.Host}
	switch {
	case v.Offline:
		a.PullRequests, a.GitHubToken = no("offline"), no("offline")
		gh.Enabled, gh.Reason = false, optString("offline_mode")
	case s.inst.tokens() == "":
		a.GitHubToken = no("no_state_dir")
		gh.Enabled, gh.Reason = false, optString("no_state_dir")
	}
	if cred := s.inst.userToken(q.session.UserID); cred != nil && !v.Offline {
		gh.Token, gh.Account, gh.Source = true, optString(cred.Login), optString(cred.Source)
	}
	v.GitHub = gh
	if st != nil {
		v.Model = &setupModelView{}
		if st.Model != nil {
			id, probe := st.Model.ID, st.Model.Probe
			v.Model = &setupModelView{Configured: true, ID: &id, Probe: &probe}
		}
	}
	switch {
	case !offer.ModelReview:
		a.ModelReview = no("not_offered")
	case v.Model == nil:
		a.ModelReview = no("unknown")
	case !v.Model.Configured:
		a.ModelReview = no("model_not_configured")
	default:
		a.ModelReview = yes()
	}
	a.BuildScripts = yes()
	if !offer.BuildScripts {
		a.BuildScripts = no("not_offered")
	}
	switch {
	case st == nil || st.DependencyFetch == nil:
		a.FetchDependencies = no("unknown")
	case !*st.DependencyFetch:
		a.FetchDependencies = no("node_disabled")
	default:
		a.FetchDependencies = yes()
	}
	if s.inst != nil && s.inst.StateDir != "" {
		if res, err := toolfind.Read(filepath.Join(s.inst.StateDir, toolfind.FileName)); err == nil {
			if !q.session.IsAdmin {
				res = withoutPaths(res)
			}
			v.Tools = &res
		}
	}
	writeJSON(q.w, http.StatusOK, v)
}

// withoutPaths is a tool detection without the machine's paths (what a
// user who is not an administrator sees).
func withoutPaths(r toolfind.Result) toolfind.Result {
	r.GoModCache, r.GoSettings, r.NpmRegistry = "", nil, ""
	tools := make([]toolfind.Tool, 0, len(r.Tools))
	for _, t := range r.Tools {
		t.Path, t.ResolvedPath, t.Source, t.Tried, t.Hint = "", "", "", nil, ""
		tools = append(tools, t)
	}
	r.Tools = tools
	return r
}

// Errors of a local_path project registration.
var (
	errFolderMissing     = errors.New("the folder does not exist or is not a directory on this server")
	errFolderOutside     = errors.New("the folder is not inside a folder this server allows you to read; use a Git address or ask the administrator to allow the folder")
	errNoAllowedFolder   = errors.New("this server allows you no folder to read; use a Git address or ask the administrator to allow a folder")
	errFolderUnsupported = errors.New("a folder name on this path is not supported (for example a ':'); ask the administrator to allow the folder itself")
)

// folderHoldsData is a project folder that contains, or lies inside, one of
// CODEYE's own data directories (database password, model key, snapshots).
type folderHoldsData struct{ folder, data string }

func (e folderHoldsData) Error() string {
	if pathWithin(e.folder, e.data) {
		return "the folder " + e.folder + " is inside CODEYE's data directory " + e.data + "; pick a project folder outside it"
	}
	return "the folder " + e.folder + " contains CODEYE's data directory " + e.data + " (database password, model key); pick a subfolder that holds the project instead"
}

// protectedDirs are the state and backup directories, resolved.
func (p *Instance) protectedDirs() []string {
	if p == nil {
		return nil
	}
	dirs := []string{p.StateDir}
	if st := p.readSetup(); st != nil {
		dirs = append(dirs, st.BackupDir)
	}
	var out []string
	for _, d := range dirs {
		if d == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(d); err == nil {
			d = r
		}
		out = append(out, filepath.Clean(d))
	}
	return out
}

// checkFolder refuses a resolved project folder that holds or lies inside
// a data directory.
func (p *Instance) checkFolder(real string) error {
	for _, dir := range p.protectedDirs() {
		if pathWithin(real, dir) || pathWithin(dir, real) {
			return folderHoldsData{folder: real, data: dir}
		}
	}
	return nil
}

// checkRootFolder refuses a source root the administrator adds inside a
// data directory (a root may contain the state directory, as the home
// directory does: projects inside it are checked one by one).
func (p *Instance) checkRootFolder(real string) error {
	for _, dir := range p.protectedDirs() {
		if pathWithin(real, dir) {
			return folderHoldsData{folder: real, data: dir}
		}
	}
	return nil
}

// checkLocalSource applies checkFolder to a local source declared by root
// and relative path. A root the caller may not use is left to the
// registration, which answers 404.
func (s *Server) checkLocalSource(q *request, src domain.Source) error {
	ctx, cancel := q.ctx()
	defer cancel()
	roots, err := s.store.GrantedRootPaths(ctx, q.session.UserID)
	if err != nil {
		return err
	}
	for _, r := range roots {
		if r.ID != src.SourceRootID {
			continue
		}
		folder := filepath.Join(r.Path, filepath.FromSlash(src.RelativePath))
		if real, err := filepath.EvalSymlinks(folder); err == nil {
			folder = real
		}
		return s.inst.checkFolder(filepath.Clean(folder))
	}
	return nil
}

// localSource maps an absolute folder path on this server onto the longest
// source root the caller may use that holds it. The path is resolved
// through symbolic links; a folder that contains or lies inside the state or
// backup directory is refused. roots are the caller's allowed roots.
//
// Only the CODEYE permission (a grant, or a root open to everyone) is
// checked. A stricter rule that maps a CODEYE account to an operating-system
// account and checks the folder's POSIX permissions for that account would
// plug in here, after the root match and before the source is returned
// (docs/SECURITY.md#서버-경로-허가); the claim-time re-check in the store would
// need the same rule.
func (s *Server) localSource(path string, roots []postgres.SourceRootRecord) (domain.Source, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return domain.Source{}, errFolderMissing
	}
	if len(roots) == 0 {
		return domain.Source{}, errNoAllowedFolder
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return domain.Source{}, errFolderMissing
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return domain.Source{}, errFolderMissing
	}
	if err := s.inst.checkFolder(real); err != nil {
		return domain.Source{}, err
	}
	var best postgres.SourceRootRecord
	bestLen, rel := -1, ""
	for _, r := range roots {
		root := r.Path
		if resolved, err := filepath.EvalSymlinks(root); err == nil {
			root = resolved
		}
		if !pathWithin(real, root) || len(root) <= bestLen {
			continue
		}
		p, err := filepath.Rel(root, real)
		if err != nil {
			continue
		}
		best, bestLen, rel = r, len(root), filepath.ToSlash(p)
	}
	if bestLen < 0 {
		return domain.Source{}, errFolderOutside
	}
	src := domain.Source{Kind: domain.SourceLocal, SourceRootID: best.ID, RelativePath: rel}
	if src.Validate() != nil {
		return domain.Source{}, errFolderUnsupported
	}
	return src, nil
}

// pathWithin reports whether p is base or below it.
func pathWithin(p, base string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// folderError writes the response of a refused local_path. readable are the
// caller's allowed folders, sent with the refusals they explain.
func (s *Server) folderError(q *request, err error, readable []string) {
	code := ""
	var holds folderHoldsData
	switch {
	case errors.As(err, &holds):
		code = "FOLDER_HOLDS_CODEYE_DATA"
	case errors.Is(err, errFolderMissing):
		code = "FOLDER_NOT_FOUND"
	case errors.Is(err, errNoAllowedFolder):
		code = "LOCAL_PATH_NOT_ALLOWED"
	case errors.Is(err, errFolderOutside):
		code = "FOLDER_NOT_READABLE"
	case errors.Is(err, errFolderUnsupported):
		code = "FOLDER_NAME_UNSUPPORTED"
	default:
		s.storeError(q, err)
		return
	}
	if readable == nil {
		readable = []string{}
	}
	writeErrorDetails(q.w, q.r, http.StatusUnprocessableEntity, code, err.Error(), false, map[string]any{"readable_folders": readable})
}

// sameSource reports whether a stored project source declares src (a
// folder or repository given again opens its project).
func sameSource(stored json.RawMessage, src domain.Source) bool {
	var have domain.Source
	return json.Unmarshal(stored, &have) == nil && domain.SameSource(have, src)
}
