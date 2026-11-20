package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/hostaddr"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

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
	host := strings.ToLower(strings.TrimSpace(in.Host))
	platform := s.inst.platform()
	if platform.Offline && domain.ValidateHost(host) == nil && !github.HostAllowed(host, true, platform.Hosts) {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "SOURCE_UNSUPPORTED", "offline mode allows only configured internal Git hosts", false)
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
	// ServiceAddresses are all the addresses the service answers on, for
	// people to pick the one their network reaches.
	ServiceAddresses []hostaddr.Address `json:"service_addresses"`
}

func (s *Server) settingsViewOf(st postgres.ServiceSettings) settingsView {
	return settingsView{OthersModelReview: st.OthersModelReview, OthersBuildScripts: st.OthersBuildScripts, UpdatedAt: st.UpdatedAt, ServiceOrigin: optString(s.Origin()), ServiceAddresses: s.Addresses()}
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
