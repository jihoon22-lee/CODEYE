package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/github"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

const maxWebhookBody = 2 << 20

type webhookConfig struct {
	Host         string `json:"host"`
	Repository   string `json:"repository"`
	RepositoryID int64  `json:"repository_id"`
	Actor        string `json:"actor_user_id"`
	Secret       string `json:"secret"`
	PullRequests bool   `json:"pull_requests"`
	Mentions     bool   `json:"mentions"`
}

func (s *Server) readWebhookConfig(project string) (webhookConfig, bool) {
	var cfg webhookConfig
	if s.inst == nil || s.inst.StateDir == "" || !domain.ValidID(project) {
		return cfg, false
	}
	state, err := os.OpenRoot(s.inst.StateDir)
	if err != nil {
		return cfg, false
	}
	defer state.Close()
	name := filepath.Join("secrets", "github-webhooks", project+".json")
	st, err := state.Lstat(name)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		return cfg, false
	}
	f, err := state.Open(name)
	if err != nil {
		return cfg, false
	}
	defer f.Close()
	st, err = f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0o600 {
		return cfg, false
	}
	const limit = 4 << 10
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || len(raw) > limit || domain.DecodeStrict(raw, &cfg) != nil || domain.ValidateHost(cfg.Host) != nil ||
		cfg.Host != strings.ToLower(cfg.Host) || !domain.ValidRepositoryName(cfg.Repository) || cfg.RepositoryID < 1 || !domain.ValidID(cfg.Actor) ||
		len(cfg.Secret) < 32 || len(cfg.Secret) > 256 || (!cfg.PullRequests && !cfg.Mentions) {
		return webhookConfig{}, false
	}
	return cfg, true
}

// githubWebhook verifies and durably accepts a delivery before doing any
// remote work. The JSON is reduced to the fields needed to process it.
func (s *Server) githubWebhook(q *request) {
	cfg, ok := s.readWebhookConfig(q.params[0])
	if !ok {
		writeError(q.w, q.r, http.StatusNotFound, "NOT_FOUND", "not found", false)
		return
	}
	mediaType, _, _ := mime.ParseMediaType(q.r.Header.Get("Content-Type"))
	if mediaType != "application/json" {
		writeError(q.w, q.r, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE", "content type must be application/json", false)
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(q.w, q.r.Body, maxWebhookBody))
	if err != nil {
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			writeError(q.w, q.r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "webhook body is too large", false)
		} else {
			writeError(q.w, q.r, http.StatusBadRequest, "INVALID_REQUEST", "cannot read webhook body", false)
		}
		return
	}
	if !github.VerifyWebhook(cfg.Secret, raw, q.r.Header.Get("X-Hub-Signature-256")) {
		writeError(q.w, q.r, http.StatusUnauthorized, "UNAUTHENTICATED", "webhook signature is invalid", false)
		return
	}
	canonical, err := domain.CanonicalJSON(raw)
	var e github.WebhookEvent
	if err != nil || json.Unmarshal(canonical, &e) != nil || e.Repository.ID != cfg.RepositoryID || !strings.EqualFold(e.Repository.FullName, cfg.Repository) {
		writeError(q.w, q.r, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "webhook does not match the configured repository", false)
		return
	}
	ctx, cancel := q.ctx()
	defer cancel()
	delivery := postgres.WebhookDelivery{ProjectID: q.params[0], DeliveryID: strings.ToLower(q.r.Header.Get("X-GitHub-Delivery")), Digest: domain.Digest(raw),
		Event: q.r.Header.Get("X-GitHub-Event"), ActorID: cfg.Actor, Host: cfg.Host, Repository: cfg.Repository, RepositoryID: cfg.RepositoryID, RequestID: requestID(q.r)}
	switch delivery.Event {
	case "pull_request":
		if cfg.PullRequests && (e.Action == "opened" || e.Action == "reopened" || e.Action == "synchronize" || e.Action == "ready_for_review") &&
			e.PullRequest.Head.Repo != nil && e.PullRequest.Head.Repo.ID == cfg.RepositoryID && !e.PullRequest.Draft && domain.ValidObjectID(e.PullRequest.Head.SHA) {
			delivery.PullNumber, delivery.HeadSHA = e.Number, e.PullRequest.Head.SHA
		}
	case "issue_comment":
		if cfg.Mentions && e.Action == "created" {
			client, login := s.platformGitHub(ctx, cfg.Host)
			if client == nil || client.Token == "" || login == "" {
				writeError(q.w, q.r, http.StatusServiceUnavailable, "UNAVAILABLE", "platform GitHub account is not configured", true)
				return
			}
			if e.CommentCommand(login) {
				delivery.PullNumber, delivery.CommentID, delivery.Commenter, delivery.CommenterID = e.Issue.Number, e.Comment.ID, e.Comment.User.Login, e.Comment.User.ID
			}
		}
	}
	if err := s.store.StoreWebhookDelivery(ctx, delivery); err != nil {
		s.storeError(q, err)
		return
	}
	q.w.WriteHeader(http.StatusAccepted)
}

func (s *Server) webhookLoop(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		for range 8 {
			if ctx.Err() != nil || !s.processWebhook(ctx) {
				break
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Server) processWebhook(ctx context.Context) bool {
	claimCtx, cancel := context.WithTimeout(ctx, storeTimeout)
	d, err := s.store.ClaimWebhookDelivery(claimCtx)
	cancel()
	if err != nil {
		if !errors.Is(err, domain.ErrNotFound) && ctx.Err() == nil {
			s.log.Warn("GitHub delivery claim failed", "error_class", errorClass(err))
		}
		return false
	}
	processCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	reason, retry := s.handleWebhookDelivery(processCtx, d)
	cancel()
	if reason != "" && ctx.Err() == nil {
		finishCtx, cancel := context.WithTimeout(ctx, storeTimeout)
		defer cancel()
		if err := s.store.FinishWebhookDelivery(finishCtx, d, reason, retry); err != nil {
			s.log.Warn("GitHub delivery result save failed", "error_class", errorClass(err))
		}
	}
	return true
}

func (s *Server) handleWebhookDelivery(ctx context.Context, d postgres.WebhookDelivery) (reason string, retry bool) {
	cfg, ok := s.readWebhookConfig(d.ProjectID)
	if !ok || cfg.Actor != d.ActorID || cfg.RepositoryID != d.RepositoryID || cfg.Host != d.Host || !strings.EqualFold(cfg.Repository, d.Repository) ||
		(d.Event == "pull_request" && !cfg.PullRequests) || (d.Event == "issue_comment" && !cfg.Mentions) {
		return "configuration_changed", false
	}
	project, err := s.store.GetProjectSummary(ctx, d.ActorID, d.ProjectID)
	if err != nil {
		return webhookStoreFailure(err)
	}
	if project.Role == string(domain.RoleViewer) {
		return "project_access_denied", false
	}
	src, err := domain.ParseSource(project.Source)
	host, hostErr := domain.RepositoryHost(src.RepositoryURL)
	if err != nil || src.Kind != domain.SourceGit || hostErr != nil || host != d.Host || !strings.EqualFold(domain.RepositoryPath(src.RepositoryURL), d.Repository) {
		return "project_source_changed", false
	}
	client, login := s.platformGitHub(ctx, host)
	if client == nil || client.Token == "" {
		return "platform_github_unavailable", true
	}
	if d.Commenter != "" {
		if login == "" || strings.EqualFold(d.Commenter, login) {
			return "comment_author_denied", false
		}
		allowed, err := client.CanRequestReview(ctx, d.Repository, d.Commenter, d.CommenterID)
		if err != nil {
			return webhookProviderFailure(err)
		}
		if !allowed {
			return "comment_author_denied", false
		}
	}
	pin, allowed, err := github.ResolveWebhook(ctx, client, host, d.Repository, d.RepositoryID, d.PullNumber, d.HeadSHA, time.Now())
	if err != nil {
		return webhookProviderFailure(err)
	}
	if !allowed {
		return "pull_request_ineligible", false
	}
	pin.Comment = project.Analysis.CommentOnPullRequest
	if err := s.store.EnqueueWebhookPullRequest(ctx, d, pin); err != nil {
		return webhookStoreFailure(err)
	}
	return "", false
}

func webhookStoreFailure(err error) (string, bool) {
	if errors.Is(err, domain.ErrForbidden) || errors.Is(err, domain.ErrNotFound) {
		return "project_access_denied", false
	}
	if errors.Is(err, domain.ErrInvalid) {
		return "project_source_changed", false
	}
	return "storage_unavailable", true
}

func webhookProviderFailure(err error) (string, bool) {
	reason := github.KindOf(err)
	return reason, reason == github.KindUnavailable || reason == github.KindRateLimited
}
