package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// WebhookDelivery stores the identity of an authenticated repository event.
// An event that requires no run has PullNumber zero.
type WebhookDelivery struct {
	ProjectID, DeliveryID, Digest, Event, ActorID string
	RepositoryID                                  int64
	Repository, Host, HeadSHA, Commenter          string
	CommentID, CommenterID                        int64
	PullNumber, Attempts                          int
	RequestID                                     string
}

var webhookEventName = regexp.MustCompile(`^[a-z][a-z_]{0,79}$`)

// StoreWebhookDelivery durably accepts a delivery. A repeated delivery with
// the original bytes succeeds and a collision returns ErrConflict.
func (s *Store) StoreWebhookDelivery(ctx context.Context, d WebhookDelivery) error {
	if !validIDs(d.ProjectID, d.DeliveryID, d.ActorID) || !hexDigest.MatchString(d.Digest) || !webhookEventName.MatchString(d.Event) ||
		d.RepositoryID < 1 || !domain.ValidRepositoryName(d.Repository) || domain.ValidateHost(d.Host) != nil ||
		d.PullNumber < 0 || d.PullNumber > domain.MaxPullRequestNumber || d.CommentID < 0 || d.CommenterID < 0 || len(d.Commenter) > 100 ||
		(d.HeadSHA != "" && !domain.ValidObjectID(d.HeadSHA)) || !requestIDForm.MatchString(d.RequestID) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		var event, digest string
		err := tx.QueryRow(ctx, `INSERT INTO github_webhook_deliveries(project_id,delivery_id,digest,event,actor_id,repository_id,repository,source_host,
			pull_number,head_sha,comment_id,commenter,commenter_id,request_id,status) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,
			CASE WHEN $9=0 THEN 'ignored' ELSE 'pending' END) ON CONFLICT DO NOTHING RETURNING event,digest`,
			d.ProjectID, d.DeliveryID, d.Digest, d.Event, d.ActorID, d.RepositoryID, d.Repository, d.Host,
			d.PullNumber, d.HeadSHA, d.CommentID, d.Commenter, d.CommenterID, d.RequestID).Scan(&event, &digest)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `SELECT event,digest FROM github_webhook_deliveries WHERE project_id=$1 AND delivery_id=$2`, d.ProjectID, d.DeliveryID).Scan(&event, &digest)
		}
		if err == nil && (event != d.Event || digest != d.Digest) {
			err = domain.ErrConflict
		}
		return err == nil, err
	})
	return err
}

const webhookMaxAttempts = 5

// Spaces make these keys distinct from the visible-ASCII keys of browser requests.
const webhookRunKeyPrefix = "github webhook "

// ClaimWebhookDelivery leases one pending delivery for a bounded processing
// attempt. The incremented attempt count fences a processor whose lease lapses.
func (s *Store) ClaimWebhookDelivery(ctx context.Context) (WebhookDelivery, error) {
	_, err := s.pool.Exec(ctx, `UPDATE github_webhook_deliveries SET status='failed',reason='attempts_exhausted'
		WHERE (project_id,delivery_id) IN (SELECT project_id,delivery_id FROM github_webhook_deliveries WHERE status='pending'
		AND attempts >= $1 AND next_at <= clock_timestamp() LIMIT 100 FOR UPDATE SKIP LOCKED)`, webhookMaxAttempts)
	if err != nil {
		return WebhookDelivery{}, classify(err)
	}
	var d WebhookDelivery
	err = s.pool.QueryRow(ctx, `WITH candidate AS (SELECT project_id,delivery_id FROM github_webhook_deliveries
		WHERE status='pending' AND attempts < $1 AND next_at <= clock_timestamp() ORDER BY next_at,project_id,delivery_id LIMIT 1 FOR UPDATE SKIP LOCKED)
		UPDATE github_webhook_deliveries w SET attempts=w.attempts+1,next_at=clock_timestamp()+interval '1 minute' FROM candidate c
		WHERE w.project_id=c.project_id AND w.delivery_id=c.delivery_id
		RETURNING w.project_id::text,w.delivery_id::text,w.digest,w.event,w.actor_id::text,w.repository_id,w.repository,w.source_host,
		w.pull_number,w.head_sha,w.comment_id,w.commenter,w.commenter_id,w.request_id,w.attempts`, webhookMaxAttempts).Scan(&d.ProjectID, &d.DeliveryID, &d.Digest,
		&d.Event, &d.ActorID, &d.RepositoryID, &d.Repository, &d.Host, &d.PullNumber, &d.HeadSHA, &d.CommentID, &d.Commenter, &d.CommenterID, &d.RequestID, &d.Attempts)
	return d, classify(err)
}

// FinishWebhookDelivery records why an event was ignored, or schedules a
// retry of an upstream or storage error.
func (s *Store) FinishWebhookDelivery(ctx context.Context, d WebhookDelivery, reason string, retry bool) error {
	delay := time.Duration(d.Attempts) * 30 * time.Second
	_, err := s.pool.Exec(ctx, `UPDATE github_webhook_deliveries SET reason=$4,
		status=CASE WHEN NOT $5::boolean THEN 'ignored' WHEN attempts >= $7 THEN 'failed' ELSE 'pending' END,
		next_at=clock_timestamp()+$6*interval '1 millisecond' WHERE project_id=$1 AND delivery_id=$2 AND attempts=$3 AND status='pending'`,
		d.ProjectID, d.DeliveryID, d.Attempts, reason, retry, delay.Milliseconds(), webhookMaxAttempts)
	return classify(err)
}

// EnqueueWebhookPullRequest enqueues a pinned same-repository PR and marks
// the delivery complete in the same transaction. The ordinary enqueue path
// checks the configured actor's current project role and source.
func (s *Store) EnqueueWebhookPullRequest(ctx context.Context, d WebhookDelivery, pin domain.PullRequest) error {
	stored, err := pin.Marshal()
	if err != nil || pin.Host != d.Host || pin.Number != d.PullNumber || !strings.EqualFold(pin.Repository, d.Repository) ||
		!strings.EqualFold(pin.HeadRepository, d.Repository) || (d.HeadSHA != "" && pin.HeadSHA != d.HeadSHA) {
		return domain.ErrInvalid
	}
	key := fmt.Sprintf("%spr-%d-%d-%s", webhookRunKeyPrefix, d.RepositoryID, d.PullNumber, pin.HeadSHA)
	if d.CommentID > 0 {
		key = fmt.Sprintf("%scomment-%d-%d", webhookRunKeyPrefix, d.RepositoryID, d.CommentID)
	}
	request := domain.RunRequest{Profile: "deep-review", Scope: domain.Scope{Kind: "full"}, SourcePolicy: "latest_at_acquire",
		PullRequest: &domain.PullRequestRef{Number: d.PullNumber}}
	raw, err := json.Marshal(request)
	if err != nil {
		return domain.ErrInvalid
	}
	body, err := domain.CanonicalJSON(raw)
	if err != nil {
		return domain.ErrInvalid
	}
	ctx = WithRequestID(ctx, d.RequestID)
	_, err = transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		var actor string
		err := tx.QueryRow(ctx, `SELECT actor_id::text FROM github_webhook_deliveries WHERE project_id=$1 AND delivery_id=$2 AND attempts=$3
			AND status='pending' AND next_at>clock_timestamp() FOR UPDATE`, d.ProjectID, d.DeliveryID, d.Attempts).Scan(&actor)
		if err != nil {
			return false, err
		}
		run, err := enqueueTx(ctx, tx, actor, d.ProjectID, key, body, &RunOrigin{Webhook: true}, stored)
		if err != nil {
			return false, err
		}
		_, err = tx.Exec(ctx, `UPDATE github_webhook_deliveries SET status='enqueued',reason='',run_id=$3 WHERE project_id=$1 AND delivery_id=$2`,
			d.ProjectID, d.DeliveryID, run.ID)
		return err == nil, err
	})
	return err
}
