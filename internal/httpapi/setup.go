package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
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
	// (tools.json) and the setup summary (run/setup.json) are read
	// from it on each request, because `codeye up` rewrites them while the
	// controller keeps running. The controller also reads the platform GitHub
	// credentials from the state directory for each GitHub API call.
	StateDir     string
	OpenCode     OpenCodeConnection
	RuntimePaths RuntimePathManager
	// GitHubAPI is the GitHub REST API base; "" is github.APIBase() (tests
	// point it at a fake).
	GitHubAPI string
	// Offline policy passed at controller startup is independent of the setup summary.
	Offline   bool
	GitHosts  []string
	CABundle  string
	protected []string
}

// InstanceSetupFile is the setup summary `codeye up`/`start` writes under
// <state>/run for the controller.
const InstanceSetupFile = "setup.json"

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
	// GitHub decodes older setup summaries.
	GitHub            *SetupGitHub  `json:"github,omitempty"`
	GitHubConnections []SetupGitHub `json:"github_connections,omitempty"`
	Offline           bool          `json:"offline,omitempty"`
	CABundle          string        `json:"ca_bundle,omitempty"`
	// DependencyFetch is the local agent's dependency switch (nil from
	// older setups: unknown).
	DependencyFetch *bool `json:"dependency_fetch,omitempty"`
}

func (p *Instance) platform() github.Platform {
	platform := github.Platform{PublicAPI: github.APIBase()}
	if p == nil {
		return platform
	}
	platform.CredentialFile = github.PlatformCredentialFile(p.StateDir)
	if p.GitHubAPI != "" {
		platform.PublicAPI = p.GitHubAPI
	}
	if p.Offline {
		platform.Offline, platform.Hosts, platform.CABundle = true, p.GitHosts, p.CABundle
	} else if setup := p.readSetup(); setup != nil {
		platform.Offline, platform.Hosts, platform.CABundle = setup.Offline, setup.GitHosts, setup.CABundle
	}
	return platform
}

func (p *Instance) gitSourceContext(ctx context.Context) context.Context {
	platform := p.platform()
	if platform.Offline {
		return postgres.WithOfflineGitHosts(ctx, platform.Hosts)
	}
	return ctx
}

// SetupGitHub is the host's platform account status. It holds no credential.
type SetupGitHub struct {
	Enabled bool   `json:"enabled"`
	Host    string `json:"host"`
	Token   bool   `json:"token"`
	Account string `json:"account,omitempty"`
	// Source decodes older setup summaries.
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
	Tools             *toolfind.Result `json:"tools"`
	GitHubConnections []SetupGitHub    `json:"github_connections"`
	Offline           bool             `json:"offline"`
}

// availability says, per thing a user may want, whether it is available to
// them and the reason when not.
type availability struct {
	GitURL            availableItem `json:"git_url"`
	LocalPath         availableItem `json:"local_path"`
	PullRequests      availableItem `json:"pull_requests"`
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
	provs, err := s.store.ListProviders(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	st := s.inst.readSetup()
	platform := s.inst.platform()
	v := setupView{IsAdmin: q.session.IsAdmin, ServiceOrigin: optString(s.Origin()), ReadableFolders: []string{}, GitHubConnections: []SetupGitHub{}, Offline: platform.Offline,
		AnalysisDefaults: analysisDefaults{offered(domain.DefaultAnalysisChoices(domain.SourceLocal), offer), offered(domain.DefaultAnalysisChoices(domain.SourceGit), offer)}}
	hosts := []string{}
	for _, p := range provs {
		if github.HostAllowed(p.Host, platform.Offline, platform.Hosts) {
			hosts = append(hosts, p.Host)
		}
	}
	gitExe := st == nil || st.Git
	v.Git = &setupGitView{Available: gitExe && len(hosts) > 0, Hosts: hosts}
	a := &v.Available
	a.GitURL, a.LocalPath, a.PullRequests = yes(), yes(), yes()
	if !v.Git.Available {
		a.GitURL, a.PullRequests = no("no_git_host"), no("no_git_host")
	}
	if st != nil {
		v.GitHubConnections = append(v.GitHubConnections, st.GitHubConnections...)
		v.Model = &setupModelView{}
		if st.Model != nil {
			id, probe := st.Model.ID, st.Model.Probe
			v.Model = &setupModelView{Configured: true, ID: &id, Probe: &probe}
		}
	}
	if s.inst != nil && s.inst.OpenCode != nil {
		current, err := s.inst.OpenCode.Get()
		if err != nil {
			v.Model = nil
		} else {
			v.Model = &setupModelView{Configured: current.Configured, ID: current.EffectiveModel, Probe: current.Probe}
		}
	}
	if v.Model != nil && !q.session.IsAdmin {
		v.Model.ID, v.Model.Probe = nil, nil
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
	errFolderMissing    = errors.New("the folder does not exist or is not a directory on this server")
	errFolderUnreadable = errors.New("the service account cannot read this folder")
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
	dirs := append([]string{p.StateDir}, p.protected...)
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

// checkLocalSource applies the folder check to the legacy root-relative form.
func (s *Server) checkLocalSource(q *request, src domain.Source) error {
	ctx, cancel := q.ctx()
	defer cancel()
	root, err := s.store.SourceRootPath(ctx, src.SourceRootID)
	if err != nil {
		return err
	}
	folder := filepath.Join(root, filepath.FromSlash(src.RelativePath))
	if resolved, err := filepath.EvalSymlinks(folder); err == nil {
		folder = resolved
	}
	return s.inst.checkFolder(folder)
}

// resolveFolder checks readability as the controller's service account.
func (p *Instance) resolveFolder(path string) (string, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return "", errFolderMissing
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if errors.Is(err, os.ErrPermission) {
		return "", errFolderUnreadable
	}
	if err != nil {
		return "", errFolderMissing
	}
	if err := p.checkFolder(real); err != nil {
		return "", err
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return "", errFolderMissing
	}
	folder, err := os.Open(real)
	if err != nil {
		return "", errFolderUnreadable
	}
	defer folder.Close()
	if _, err := folder.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return "", errFolderUnreadable
	}
	return real, nil
}

// pathWithin reports whether p is base or below it.
func pathWithin(p, base string) bool {
	rel, err := filepath.Rel(base, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// folderError writes a source-path refusal.
func (s *Server) folderError(q *request, err error) {
	code := ""
	var holds folderHoldsData
	switch {
	case errors.As(err, &holds):
		code = "FOLDER_HOLDS_CODEYE_DATA"
	case errors.Is(err, errFolderMissing):
		code = "FOLDER_NOT_FOUND"
	case errors.Is(err, errFolderUnreadable):
		code = "FOLDER_NOT_READABLE"
	default:
		s.storeError(q, err)
		return
	}
	writeError(q.w, q.r, http.StatusUnprocessableEntity, code, err.Error(), false)
}

// sameSource reports whether a stored project source declares src (a
// folder or repository given again opens its project).
func sameSource(stored json.RawMessage, src domain.Source) bool {
	var have domain.Source
	return json.Unmarshal(stored, &have) == nil && domain.SameSource(have, src)
}
