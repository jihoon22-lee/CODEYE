package httpapi

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// The shared service: a user's own GitHub connection (/account/github) and
// what the administrator manages in the web instead of the command line
// (folders and who may read them, Git hosts, the service switches).

// accountGitHubView is GET /account/github. It never holds the token.
type accountGitHubView struct {
	Host string `json:"host"`
	// Connected: a token is stored for this user.
	Connected bool    `json:"connected"`
	Account   *string `json:"account"`
	// Source is web (pasted here) or, for the administrator who runs the
	// service, GH_TOKEN, GITHUB_TOKEN or gh (found by `codeye up`).
	Source *string `json:"source"`
	// Storable: a token can be stored here (a state directory, not offline).
	Storable bool `json:"storable"`
	// Reason is why not: offline_mode or no_state_dir.
	Reason *string `json:"reason"`
}

func (s *Server) accountGitHubView(user string) accountGitHubView {
	v := accountGitHubView{Host: github.Host, Storable: true}
	switch {
	case s.inst.offline():
		v.Storable, v.Reason = false, optString("offline_mode")
		return v
	case s.inst.tokens() == "":
		v.Storable, v.Reason = false, optString("no_state_dir")
		return v
	}
	if cred := s.inst.userToken(user); cred != nil {
		v.Connected, v.Account, v.Source = true, optString(cred.Login), optString(cred.Source)
	}
	return v
}

func (s *Server) getAccountGitHub(q *request) {
	writeJSON(q.w, http.StatusOK, s.accountGitHubView(q.session.UserID))
}

// putAccountGitHub (PUT /account/github {token}) stores the signed-in
// user's own github.com token. GitHub is asked whose token it is: a token
// GitHub refuses is not stored; when GitHub cannot be reached the token is
// stored with the account unknown. The token is never logged, returned or
// written to the audit row.
func (s *Server) putAccountGitHub(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if domain.DecodeStrict(body, &in) != nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"token": string}`, false)
		return
	}
	token := strings.TrimSpace(in.Token)
	if !github.ValidToken(token) {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "the token must be one line of 8 to 512 printable characters", false)
		return
	}
	if v := s.accountGitHubView(q.session.UserID); !v.Storable {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "GITHUB_TOKEN_UNAVAILABLE", "this service cannot store GitHub tokens (offline mode, or no state directory)", false)
		return
	}
	idCtx, cancel := context.WithTimeout(q.r.Context(), 15*time.Second)
	user, err := github.New(s.inst.apiBase(), token).User(idCtx)
	cancel()
	login := ""
	switch kind := github.KindOf(err); {
	case err == nil:
		login = user.Login
	case kind == github.KindUnauthorized:
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "GITHUB_TOKEN_REFUSED", "GitHub refused this token; it was not stored", false)
		return
	}
	if err := s.inst.tokens().Write(q.session.UserID, github.Credential{Host: github.Host, Token: token, Source: github.TokenFromWeb, Login: login}); err != nil {
		s.log.Warn("github token store failed", "request_id", requestID(q.r))
		writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "the token could not be stored", true)
		return
	}
	ctx, cancelAudit := q.ctx()
	defer cancelAudit()
	if err := s.store.RecordGitHubToken(ctx, q.session.UserID, true, github.Host, login); err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, s.accountGitHubView(q.session.UserID))
}

// deleteAccountGitHub removes the signed-in user's token (204; also when
// none was stored). Their projects then reach public repositories only.
func (s *Server) deleteAccountGitHub(q *request) {
	if err := s.inst.tokens().Remove(q.session.UserID); err != nil {
		writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "the token could not be removed", true)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.RecordGitHubToken(ctx, q.session.UserID, false, github.Host, ""); err != nil {
		s.storeError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}

// POST /admin/source-roots {path, label?, everyone?}: the administrator
// allows a folder on this server (for example an NFS mount). It must exist
// and must not lie inside CODEYE's data directories. everyone opens it to
// every user at once; otherwise it is granted to users one by one.
func (s *Server) adminAddSourceRoot(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Path     string  `json:"path"`
		Label    *string `json:"label"`
		Everyone bool    `json:"everyone"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Path == "" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"path": string, "label"?: string, "everyone"?: boolean}`, false)
		return
	}
	path := in.Path
	if !filepath.IsAbs(path) {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "FOLDER_NOT_FOUND", "the path must be an absolute folder on this server", false)
		return
	}
	path = filepath.Clean(path)
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "FOLDER_NOT_FOUND", "the folder does not exist on this server or is not a directory", false)
		return
	}
	if err := s.inst.checkRootFolder(path); err != nil {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "FOLDER_HOLDS_CODEYE_DATA", err.Error(), false)
		return
	}
	label := ""
	if in.Label != nil {
		label = *in.Label
	}
	ctx, cancel := q.ctx()
	defer cancel()
	r, err := s.store.AddSourceRootBy(ctx, q.principal(), label, path)
	switch {
	case errors.Is(err, postgres.ErrFolderMissing):
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "FOLDER_NOT_FOUND", "the folder does not exist on this server or is not a directory", false)
		return
	case errors.Is(err, domain.ErrConflict):
		writeError(q.w, q.r, http.StatusConflict, "CONFLICT", "a folder with this path or label is already allowed", false)
		return
	case err != nil:
		s.storeError(q, err)
		return
	}
	if in.Everyone {
		if err := s.store.SetSourceRootEveryone(ctx, q.principal(), r.ID, true); err != nil {
			s.storeError(q, err)
			return
		}
	}
	q.w.Header().Set("Location", apiPrefix+"/admin/source-roots/"+r.ID)
	writeJSON(q.w, http.StatusCreated, sourceRootItem{ID: r.ID, Label: r.Label, Path: r.Path, Everyone: in.Everyone, CreatedAt: time.Now().UTC().Truncate(time.Second), Grants: []sourceRootGrantItem{}})
}

// PUT /admin/source-roots/{id}/everyone {everyone}: open the folder to every
// user, or close it again (per-user grants stay).
func (s *Server) adminSetSourceRootEveryone(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Everyone *bool `json:"everyone"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Everyone == nil {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"everyone": boolean}`, false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	if err := s.store.SetSourceRootEveryone(ctx, q.principal(), q.params[0], *in.Everyone); err != nil {
		s.storeError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusNoContent)
}

// POST /admin/providers {host}: approve an HTTPS Git host (github.com or a
// GitHub Enterprise Server host). 201 when added, 200 when it already was.
func (s *Server) adminAddProvider(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		Host string `json:"host"`
	}
	if domain.DecodeStrict(body, &in) != nil || in.Host == "" {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"host": string}`, false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	p, created, err := s.store.AddProviderBy(ctx, q.principal(), in.Host)
	if errors.Is(err, domain.ErrInvalid) {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "the host must be a DNS name with an optional port (for example github.example.com)", false)
		return
	}
	if err != nil {
		s.storeError(q, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(q.w, status, map[string]any{"id": p.ID, "kind": p.Kind, "host": p.Host, "api_url": optString(p.APIURL)})
}

// settingsView is GET/PUT /admin/settings.
type settingsView struct {
	OthersModelReview  bool      `json:"others_model_review"`
	OthersBuildScripts bool      `json:"others_build_scripts"`
	UpdatedAt          time.Time `json:"updated_at"`
	// ServiceOrigin is the address to share (null: loopback only).
	ServiceOrigin *string `json:"service_origin"`
}

func (s *Server) settingsViewOf(st postgres.ServiceSettings) settingsView {
	return settingsView{OthersModelReview: st.OthersModelReview, OthersBuildScripts: st.OthersBuildScripts, UpdatedAt: st.UpdatedAt, ServiceOrigin: optString(s.pub.origin)}
}

func (s *Server) adminGetSettings(q *request) {
	ctx, cancel := q.ctx()
	defer cancel()
	st, err := s.store.GetServiceSettings(ctx)
	if err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, s.settingsViewOf(st))
}

// PUT /admin/settings {others_model_review?, others_build_scripts?}. The
// administrators' own projects are not affected by either switch.
func (s *Server) adminPutSettings(q *request) {
	body, ok := q.readBody()
	if !ok {
		return
	}
	var in struct {
		OthersModelReview  *bool `json:"others_model_review"`
		OthersBuildScripts *bool `json:"others_build_scripts"`
	}
	if domain.DecodeStrict(body, &in) != nil || (in.OthersModelReview == nil && in.OthersBuildScripts == nil) {
		writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", `body must be {"others_model_review"?: boolean, "others_build_scripts"?: boolean} with at least one member`, false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	st, err := s.store.SetServiceSettings(ctx, q.principal(), postgres.ServiceSettingsPatch{OthersModelReview: in.OthersModelReview, OthersBuildScripts: in.OthersBuildScripts})
	if err != nil {
		s.storeError(q, err)
		return
	}
	writeJSON(q.w, http.StatusOK, s.settingsViewOf(st))
}
