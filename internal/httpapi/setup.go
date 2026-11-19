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
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
	"github.com/jihoon22-lee/CODEYE/internal/toolfind"
)

// Personal configures the controller of `codeye up`/`codeye start` (one user
// on their own machine).
type Personal struct {
	// StateDir is the installation's state directory. The tool detection
	// (tools.json) and the setup summary (run/personal-setup.json) are read
	// from it on each request, because `codeye up` rewrites them while the
	// controller keeps running; no project may contain it.
	StateDir string
}

// PersonalSetupFile is the setup summary `codeye up`/`start` writes under
// <state>/run for the controller.
const PersonalSetupFile = "personal-setup.json"

// PersonalSetup is what personal mode tells the web app about itself. It
// holds no credential and no key.
type PersonalSetup struct {
	SchemaVersion string `json:"schema_version"`
	// Model is the review model, nil when none is configured.
	Model *SetupModel `json:"model"`
	// Git reports a usable git executable; GitHosts the HTTPS hosts projects
	// may be fetched from.
	Git      bool     `json:"git"`
	GitHosts []string `json:"git_hosts"`
	// BackupDir is where backups go; no project may contain it.
	BackupDir string `json:"backup_dir,omitempty"`
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
func (p *Personal) readSetup() *PersonalSetup {
	if p == nil || p.StateDir == "" {
		return nil
	}
	raw, err := os.ReadFile(filepath.Join(p.StateDir, "run", PersonalSetupFile))
	if err != nil || len(raw) > 64<<10 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s PersonalSetup
	if dec.Decode(&s) != nil || s.SchemaVersion != "0.1" {
		return nil
	}
	if s.GitHosts == nil {
		s.GitHosts = []string{}
	}
	return &s
}

// setupView is GET /setup.
type setupView struct {
	// Mode is personal (codeye up) or server.
	Mode             string                 `json:"mode"`
	Model            *setupModelView        `json:"model"`
	Git              *setupGitView          `json:"git"`
	ReadableFolders  []string               `json:"readable_folders"`
	AnalysisDefaults domain.AnalysisChoices `json:"analysis_defaults"`
	Tools            *toolfind.Result       `json:"tools"`
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

// getSetup answers what this installation can do for the first-run screen:
// in personal mode the model, Git, the folders the user can analyze by path
// and the detected tools; a server-mode controller knows none of these
// (nodes report their own) and says so with nulls.
func (s *Server) getSetup(q *request) {
	v := setupView{Mode: "server", ReadableFolders: []string{}, AnalysisDefaults: domain.DefaultAnalysisChoices}
	if s.personal == nil {
		writeJSON(q.w, http.StatusOK, v)
		return
	}
	v.Mode = "personal"
	v.Model = &setupModelView{}
	if st := s.personal.readSetup(); st != nil {
		if st.Model != nil {
			id, probe := st.Model.ID, st.Model.Probe
			v.Model = &setupModelView{Configured: true, ID: &id, Probe: &probe}
		}
		v.Git = &setupGitView{Available: st.Git, Hosts: st.GitHosts}
	}
	if s.personal.StateDir != "" {
		if res, err := toolfind.Read(filepath.Join(s.personal.StateDir, toolfind.FileName)); err == nil {
			v.Tools = &res
		}
	}
	ctx, cancel := q.ctx()
	defer cancel()
	roots, err := s.store.GrantedRootPaths(ctx, q.session.UserID)
	if err != nil {
		s.storeError(q, err)
		return
	}
	for _, r := range roots {
		v.ReadableFolders = append(v.ReadableFolders, r.Path)
	}
	writeJSON(q.w, http.StatusOK, v)
}

// Errors of a local_path project registration (personal mode).
var (
	errFolderMissing     = errors.New("the folder does not exist or is not a directory")
	errFolderOutside     = errors.New("outside readable folders - run `codeye up <path>` in a terminal to add it")
	errFolderHoldsData   = errors.New("the folder contains or is inside CODEYE's own data directory; pick a project folder")
	errFolderUnsupported = errors.New("a folder name on this path is not supported (for example a ':'); run `codeye up <path>` to add the folder itself")
)

// localSource maps an absolute folder path onto the longest granted source
// root that holds it (personal mode). The path is resolved through symbolic
// links; a folder that contains or lies inside the state or backup directory
// is refused.
func (s *Server) localSource(q *request, path string) (domain.Source, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return domain.Source{}, errFolderMissing
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		return domain.Source{}, errFolderMissing
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return domain.Source{}, errFolderMissing
	}
	protected := []string{s.personal.StateDir}
	if st := s.personal.readSetup(); st != nil {
		protected = append(protected, st.BackupDir)
	}
	for _, dir := range protected {
		if dir == "" {
			continue
		}
		if r, err := filepath.EvalSymlinks(dir); err == nil {
			dir = r
		}
		if pathWithin(real, dir) || pathWithin(dir, real) {
			return domain.Source{}, errFolderHoldsData
		}
	}
	ctx, cancel := q.ctx()
	defer cancel()
	roots, err := s.store.GrantedRootPaths(ctx, q.session.UserID)
	if err != nil {
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

// folderError writes the response of a refused local_path.
func (s *Server) folderError(q *request, err error) {
	code := ""
	switch {
	case errors.Is(err, errFolderMissing):
		code = "FOLDER_NOT_FOUND"
	case errors.Is(err, errFolderOutside):
		code = "FOLDER_NOT_READABLE"
	case errors.Is(err, errFolderHoldsData):
		code = "FOLDER_HOLDS_CODEYE_DATA"
	case errors.Is(err, errFolderUnsupported):
		code = "FOLDER_NAME_UNSUPPORTED"
	default:
		s.storeError(q, err)
		return
	}
	writeError(q.w, q.r, http.StatusUnprocessableEntity, code, err.Error(), false)
}

// sameSource reports whether a stored project source declares src
// (personal mode reuses the project of a folder or repository given again).
func sameSource(stored json.RawMessage, src domain.Source) bool {
	var have domain.Source
	return json.Unmarshal(stored, &have) == nil && domain.SameSource(have, src)
}
