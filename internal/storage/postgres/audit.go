package postgres

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
)

// Audit actor kinds (audit_events.actor_kind).
const (
	ActorUser      = "user"
	ActorOperator  = "operator_cli"
	ActorNode      = "node"
	ActorSystem    = "system"
	ActorAnonymous = "anonymous"
)

// Audit actions: the closed vocabulary of audit_events.action (migration
// 0010 CHECK and OpenAPI AuditAction list the same values).
const (
	AuditLoginSucceeded        = "auth.login_succeeded"
	AuditLoginFailed           = "auth.login_failed"
	AuditLoginThrottled        = "auth.login_throttled"
	AuditLogout                = "auth.logout"
	AuditPasswordChanged       = "auth.password_changed"
	AuditPasswordChangeFailed  = "auth.password_change_failed"
	AuditPasswordSet           = "auth.password_set"
	AuditPasswordReset         = "auth.password_reset"
	AuditLoginLinkIssued       = "auth.login_link_issued"
	AuditUserCreated           = "user.created"
	AuditUserDisabled          = "user.disabled"
	AuditAdminGranted          = "user.admin_granted"
	AuditAdminRevoked          = "user.admin_revoked"
	AuditSourceRootAdded       = "source_root.added"
	AuditSourceRootGranted     = "source_root.granted"
	AuditSourceRootRevoked     = "source_root.grant_revoked"
	AuditProviderAdded         = "provider.added"
	AuditProjectRegistered     = "project.registered"
	AuditProjectRevised        = "project.revised"
	AuditMemberSet             = "project.member_set"
	AuditMemberRemoved         = "project.member_removed"
	AuditRunRequested          = "run.requested"
	AuditRunCancelRequested    = "run.cancel_requested"
	AuditReportExported        = "report.exported"
	AuditFindingReviewRecorded = "finding_review.recorded"
	AuditScheduleCreated       = "schedule.created"
	AuditScheduleUpdated       = "schedule.updated"
	AuditScheduleDeleted       = "schedule.deleted"
	AuditEnrollTokenCreated    = "node.enrollment_token_created"
	AuditEnrollTokenRevoked    = "node.enrollment_token_revoked"
	AuditNodeEnrolled          = "node.enrolled"
	AuditNodeRevoked           = "node.revoked"
	AuditPoolPolicySet         = "pool.policy_set"
	AuditSchedulerLimitsSet    = "scheduler.limits_set"
	AuditArtifactQuotaSet      = "scheduler.artifact_quota_set"
	AuditStaleRejected         = "attempt.stale_rejected"
)

// auditDetailKeys are the only members an audit detail may carry. Every
// value is a short string, a number, a boolean, null or a list of short
// strings. None of these names a password, token, secret, source text,
// prompt or server path; adding a key is a reviewed code change.
var auditDetailKeys = map[string]bool{
	"method": true, "client_address": true, "scope": true, "retry_after_seconds": true,
	"username": true, "display_name": true, "is_admin": true, "previous_is_admin": true, "must_change_password": true,
	"sessions_revoked": true, "label": true, "kind": true, "host": true, "user_id": true,
	"role": true, "previous_role": true, "name": true, "source_kind": true, "source_root_id": true, "provider_id": true,
	"revision": true, "profile": true, "scope_kind": true, "trigger": true, "schedule_id": true, "status": true,
	"format": true, "include_code": true, "fingerprint": true, "disposition": true, "finding_id": true,
	"cron": true, "timezone": true, "enabled": true, "fields": true, "pool": true, "source_root_ids": true,
	"expires_at": true, "epoch": true, "route": true, "global_max_running": true, "project_max_running": true,
	"artifact_quota_bytes": true, "min_workers": true, "max_workers": true, "policy_revision": true,
	"run_id": true, "protocol_version": true, "os": true, "arch": true,
}

const maxAuditString = 200

type requestIDKey struct{}
type systemActorKey struct{}

var requestIDForm = regexp.MustCompile(`^req_[0-9a-f]{24}$`)

// WithRequestID attaches the HTTP request ID to ctx; audit rows written under
// ctx carry it. Other values are ignored.
func WithRequestID(ctx context.Context, id string) context.Context {
	if !requestIDForm.MatchString(id) {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey{}, id)
}

// asSystem marks writes under ctx as done by the control plane itself (the
// schedule materializer) on behalf of the user it acts for.
func asSystem(ctx context.Context) context.Context {
	return context.WithValue(ctx, systemActorKey{}, true)
}

func requestIDFrom(ctx context.Context) *string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return &id
	}
	return nil
}

// Principal is who performs an account or access change: a signed-in user
// (checked to be an enabled administrator in the same transaction) or the
// operator CLI, which holds the database URL. The zero value is invalid, so
// a caller cannot fall into operator authority by omission.
type Principal struct {
	user     string
	operator bool
}

// OperatorPrincipal is the operator CLI (and personal-mode start).
func OperatorPrincipal() Principal { return Principal{operator: true} }

// UserPrincipal is a signed-in user acting through the API.
func UserPrincipal(id string) Principal { return Principal{user: id} }

func (p Principal) valid() bool { return p.operator || validIDs(p.user) }

// auditEntry is one audit row. The actor is resolved by the writer from
// kind and the context (request ID, system marker).
type auditEntry struct {
	kind, user, node                      string
	action, targetType, targetID, project string
	detail                                map[string]any
}

// userAudit is an entry by a signed-in user, or by the control plane on the
// user's behalf when ctx is marked as system.
func userAudit(ctx context.Context, user, action string) auditEntry {
	if v, _ := ctx.Value(systemActorKey{}).(bool); v {
		return auditEntry{kind: ActorSystem, user: user, action: action}
	}
	return auditEntry{kind: ActorUser, user: user, action: action}
}

// principalAudit is an entry by an administrator or the operator CLI.
func principalAudit(by Principal, action string) auditEntry {
	if by.operator {
		return auditEntry{kind: ActorOperator, action: action}
	}
	return auditEntry{kind: ActorUser, user: by.user, action: action}
}

func (e auditEntry) target(kind, id string) auditEntry {
	e.targetType, e.targetID = kind, id
	return e
}

func (e auditEntry) in(project string) auditEntry {
	e.project = project
	return e
}

func (e auditEntry) with(detail map[string]any) auditEntry {
	e.detail = detail
	return e
}

// checkAuditDetail enforces the detail allowlist and value shapes.
func checkAuditDetail(detail map[string]any) error {
	for k, v := range detail {
		if !auditDetailKeys[k] {
			return domain.ErrInvalid
		}
		switch x := v.(type) {
		case nil, bool, int, int32, int64, float64:
		case string:
			if len(x) > maxAuditString {
				return domain.ErrInvalid
			}
		case []string:
			if len(x) > 50 {
				return domain.ErrInvalid
			}
			for _, s := range x {
				if len(s) > maxAuditString {
					return domain.ErrInvalid
				}
			}
		case time.Time:
		default:
			return domain.ErrInvalid
		}
	}
	return nil
}

// writeAudit appends e in tx. The caller's change and its audit row commit
// or roll back together; a failure here fails the change.
func writeAudit(ctx context.Context, tx pgx.Tx, e auditEntry) error {
	if err := checkAuditDetail(e.detail); err != nil {
		return err
	}
	detail := e.detail
	if detail == nil {
		detail = map[string]any{}
	}
	raw, err := json.Marshal(detail)
	if err != nil {
		return domain.ErrInvalid
	}
	opt := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	_, err = tx.Exec(ctx, `INSERT INTO audit_events(actor_kind,actor_user_id,actor_node_id,action,target_type,target_id,project_id,request_id,detail)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9::text::jsonb)`,
		e.kind, opt(e.user), opt(e.node), e.action, opt(e.targetType), opt(e.targetID), opt(e.project), requestIDFrom(ctx), string(raw))
	return err
}

// recordAudit writes e in its own transaction (events that change nothing
// else: a rejected login, a stale node write).
func (s *Store) recordAudit(ctx context.Context, e auditEntry) error {
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) { return true, writeAudit(ctx, tx, e) })
	return err
}

// RecordStaleRejection records that a node's write was refused because the
// attempt lease is not held by that worker and epoch. The refusal itself
// already happened (and rolled back); this row is its trace.
func (s *Store) RecordStaleRejection(ctx context.Context, node, attempt string, epoch int64, route string) error {
	if !validIDs(node) {
		return domain.ErrInvalid
	}
	e := auditEntry{kind: ActorNode, node: node, action: AuditStaleRejected}.with(map[string]any{"epoch": epoch, "route": route})
	if validIDs(attempt) {
		e = e.target("attempt", attempt)
	}
	return s.recordAudit(ctx, e)
}

// AuditRecord is one row of the audit read API.
type AuditRecord struct {
	ID            int64
	OccurredAt    time.Time
	ActorKind     string
	ActorUserID   *string
	ActorUsername *string
	ActorNodeID   *string
	Action        string
	TargetType    *string
	TargetID      *string
	ProjectID     *string
	RequestID     *string
	Detail        json.RawMessage
}

// AuditFilter narrows an audit read. Cursor is the NextCursor of the
// previous page; rows are newest first by row ID.
type AuditFilter struct {
	Since, Until *time.Time
	ActorUserID  string
	Action       string
	ProjectID    string
	Cursor       string
	Limit        int
}

// AuditPage is one keyset page of audit rows.
type AuditPage struct {
	Items      []AuditRecord
	NextCursor string
}

func validAuditAction(a string) bool { return auditActions[a] }

var auditActions = map[string]bool{
	AuditLoginSucceeded: true, AuditLoginFailed: true, AuditLoginThrottled: true, AuditLogout: true,
	AuditPasswordChanged: true, AuditPasswordChangeFailed: true, AuditPasswordSet: true, AuditPasswordReset: true,
	AuditLoginLinkIssued: true, AuditUserCreated: true, AuditUserDisabled: true, AuditAdminGranted: true,
	AuditAdminRevoked: true, AuditSourceRootAdded: true, AuditSourceRootGranted: true, AuditSourceRootRevoked: true,
	AuditProviderAdded: true, AuditProjectRegistered: true, AuditProjectRevised: true, AuditMemberSet: true,
	AuditMemberRemoved: true, AuditRunRequested: true, AuditRunCancelRequested: true, AuditReportExported: true,
	AuditFindingReviewRecorded: true, AuditScheduleCreated: true, AuditScheduleUpdated: true, AuditScheduleDeleted: true,
	AuditEnrollTokenCreated: true, AuditEnrollTokenRevoked: true, AuditNodeEnrolled: true, AuditNodeRevoked: true,
	AuditPoolPolicySet: true, AuditSchedulerLimitsSet: true, AuditArtifactQuotaSet: true, AuditStaleRejected: true,
}

// AuditActions lists the closed action vocabulary.
func AuditActions() []string {
	out := make([]string, 0, len(auditActions))
	for a := range auditActions {
		out = append(out, a)
	}
	return out
}

func (f AuditFilter) check() (after int64, ok bool) {
	if f.Limit < 1 || f.Limit > 200 || (f.ActorUserID != "" && !validIDs(f.ActorUserID)) ||
		(f.ProjectID != "" && !validIDs(f.ProjectID)) || (f.Action != "" && !validAuditAction(f.Action)) {
		return 0, false
	}
	if f.Cursor != "" {
		n, err := strconv.ParseInt(f.Cursor, 10, 64)
		if err != nil || n < 1 || strconv.FormatInt(n, 10) != f.Cursor {
			return 0, false
		}
		after = n
	}
	return after, true
}

const auditSelect = `SELECT a.id,a.occurred_at,a.actor_kind,a.actor_user_id::text,u.username,a.actor_node_id::text,a.action,
	a.target_type,a.target_id,a.project_id::text,a.request_id,a.detail
	FROM audit_events a LEFT JOIN users u ON u.id=a.actor_user_id
	WHERE ($1::bigint=0 OR a.id<$1) AND ($2::timestamptz IS NULL OR a.occurred_at>=$2) AND ($3::timestamptz IS NULL OR a.occurred_at<$3)
	AND ($4::uuid IS NULL OR a.actor_user_id=$4) AND ($5::text IS NULL OR a.action=$5) AND ($6::uuid IS NULL OR a.project_id=$6)
	ORDER BY a.id DESC LIMIT $7`

func readAudit(ctx context.Context, tx pgx.Tx, after int64, f AuditFilter) (AuditPage, error) {
	opt := func(s string) any {
		if s == "" {
			return nil
		}
		return s
	}
	rows, err := tx.Query(ctx, auditSelect, after, f.Since, f.Until, opt(f.ActorUserID), opt(f.Action), opt(f.ProjectID), f.Limit+1)
	if err != nil {
		return AuditPage{}, err
	}
	defer rows.Close()
	page := AuditPage{Items: []AuditRecord{}}
	for rows.Next() {
		var r AuditRecord
		if err := rows.Scan(&r.ID, &r.OccurredAt, &r.ActorKind, &r.ActorUserID, &r.ActorUsername, &r.ActorNodeID, &r.Action,
			&r.TargetType, &r.TargetID, &r.ProjectID, &r.RequestID, &r.Detail); err != nil {
			return AuditPage{}, err
		}
		if len(page.Items) == f.Limit {
			page.NextCursor = strconv.FormatInt(page.Items[len(page.Items)-1].ID, 10)
			break
		}
		r.OccurredAt = r.OccurredAt.UTC()
		page.Items = append(page.Items, r)
	}
	rows.Close()
	return page, rows.Err()
}

// ListAudit is the administrator's audit read over every row.
func (s *Store) ListAudit(ctx context.Context, actor string, f AuditFilter) (AuditPage, error) {
	after, ok := f.check()
	if !ok || !validIDs(actor) {
		return AuditPage{}, domain.ErrInvalid
	}
	return transaction(ctx, s, func(tx pgx.Tx) (AuditPage, error) {
		if err := requireAdmin(ctx, tx, UserPrincipal(actor)); err != nil {
			return AuditPage{}, err
		}
		return readAudit(ctx, tx, after, f)
	})
}

// ListProjectAudit is a project manager's read of that project's rows only.
func (s *Store) ListProjectAudit(ctx context.Context, actor, project string, f AuditFilter) (AuditPage, error) {
	after, ok := f.check()
	if !ok || !validIDs(actor, project) || (f.ProjectID != "" && f.ProjectID != project) {
		return AuditPage{}, domain.ErrInvalid
	}
	f.ProjectID = project
	return transaction(ctx, s, func(tx pgx.Tx) (AuditPage, error) {
		if err := requireProject(ctx, tx, actor, project, true, false); err != nil {
			return AuditPage{}, err
		}
		return readAudit(ctx, tx, after, f)
	})
}
