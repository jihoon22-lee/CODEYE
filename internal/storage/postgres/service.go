package postgres

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jihoon22-lee/CODEYE/internal/domain"
	"github.com/jihoon22-lee/CODEYE/internal/nodeproto"
)

// Permissions of the shared service (docs/SECURITY.md#서버-경로-허가): who may use a
// source root, the administrator's service switches and the effective analysis
// choices a claim hands to a node.

// rootAllowedSQL is the one predicate "user $u may use source root r": a
// grant for the user, or the root is open to everyone. Registration, the
// claim re-check, the waiting check and the readable-folder lists all use it.
// $1 is the root column or parameter, $2 the user.
func rootAllowedSQL(root, user string) string {
	return "(EXISTS(SELECT 1 FROM source_root_grants g WHERE g.source_root_id=" + root + " AND g.user_id=" + user + ")" +
		" OR EXISTS(SELECT 1 FROM source_root_everyone e WHERE e.source_root_id=" + root + "))"
}

// rootAllowedLockedSQL is rootAllowedSQL taking share locks on the rows it
// reads (ordered against a concurrent revoke or close).
func rootAllowedLockedSQL(root, user string) string {
	return "(EXISTS(SELECT 1 FROM source_root_grants g WHERE g.source_root_id=" + root + " AND g.user_id=" + user + " FOR SHARE)" +
		" OR EXISTS(SELECT 1 FROM source_root_everyone e WHERE e.source_root_id=" + root + " FOR SHARE))"
}

// AddSourceRootBy registers a server folder as a source root (an
// administrator, or the operator CLI). path must be an absolute, clean,
// existing directory; label defaults to the folder name. The audit row keeps
// only the label.
func (s *Store) AddSourceRootBy(ctx context.Context, by Principal, label, path string) (SourceRootRecord, error) {
	if path == "" || len(path) > 4096 || !utf8.ValidString(path) || strings.ContainsRune(path, 0) || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return SourceRootRecord{}, domain.ErrInvalid
	}
	if strings.TrimSpace(label) == "" {
		label = filepath.Base(path)
		if label == string(filepath.Separator) || label == "." || label == "" {
			label = path
		}
	}
	clean, ok := cleanLabel(label, 100)
	if !ok {
		return SourceRootRecord{}, domain.ErrInvalid
	}
	if st, err := os.Stat(path); err != nil || !st.IsDir() {
		return SourceRootRecord{}, ErrFolderMissing
	}
	r := SourceRootRecord{ID: domain.NewID(), Label: clean, Path: path}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return false, err
		}
		var taken bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM source_roots WHERE label=$1 OR path=$2)", r.Label, path).Scan(&taken); err != nil {
			return false, err
		}
		if taken {
			return false, domain.ErrConflict
		}
		if _, err := tx.Exec(ctx, "INSERT INTO source_roots(id,label,path) VALUES($1,$2,$3)", r.ID, r.Label, path); err != nil {
			return false, err
		}
		return true, writeAudit(ctx, tx, principalAudit(by, AuditSourceRootAdded).target("source_root", r.ID).with(map[string]any{"label": r.Label}))
	})
	return r, err
}

// ErrFolderMissing: the path is not an existing directory.
var ErrFolderMissing = errors.New("the folder does not exist or is not a directory")

// SetSourceRootEveryone opens root to every user (everyone true) or closes it
// again; the per-user grants are unchanged. Setting the current value writes
// no audit row.
func (s *Store) SetSourceRootEveryone(ctx context.Context, by Principal, root string, everyone bool) error {
	if !validIDs(root) {
		return domain.ErrInvalid
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return false, err
		}
		var exists bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM source_roots WHERE id=$1)", root).Scan(&exists); err != nil {
			return false, err
		}
		if !exists {
			return false, domain.ErrNotFound
		}
		q := "DELETE FROM source_root_everyone WHERE source_root_id=$1"
		if everyone {
			q = "INSERT INTO source_root_everyone(source_root_id) VALUES($1) ON CONFLICT DO NOTHING"
		}
		tag, err := tx.Exec(ctx, q, root)
		if err != nil || tag.RowsAffected() == 0 {
			return false, err
		}
		return true, writeAudit(ctx, tx, principalAudit(by, AuditSourceRootEveryone).target("source_root", root).with(map[string]any{"everyone": everyone}))
	})
	return err
}

// AddProviderBy approves an HTTPS Git host (an administrator): github.com is
// kind github, any other host ghes. created is false when the host was
// already approved (no second row, no audit row).
func (s *Store) AddProviderBy(ctx context.Context, by Principal, host string) (Provider, bool, error) {
	host = strings.ToLower(strings.TrimSpace(host))
	if domain.ValidateHost(host) != nil {
		return Provider{}, false, domain.ErrInvalid
	}
	kind := "ghes"
	if host == "github.com" {
		kind = "github"
	}
	type result struct {
		p       Provider
		created bool
	}
	res, err := transaction(ctx, s, func(tx pgx.Tx) (result, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return result{}, err
		}
		var p Provider
		err := tx.QueryRow(ctx, "SELECT id::text,kind,host,coalesce(api_url,'') FROM providers WHERE host=$1 ORDER BY kind LIMIT 1", host).Scan(&p.ID, &p.Kind, &p.Host, &p.APIURL)
		if err == nil {
			return result{p: p}, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return result{}, err
		}
		p = Provider{ID: domain.NewID(), Kind: kind, Host: host}
		if _, err := tx.Exec(ctx, "INSERT INTO providers(id,kind,host) VALUES($1,$2,$3)", p.ID, kind, host); err != nil {
			return result{}, err
		}
		return result{p: p, created: true}, writeAudit(ctx, tx, principalAudit(by, AuditProviderAdded).target("provider", p.ID).with(map[string]any{"kind": kind, "host": truncateAudit(host)}))
	})
	return res.p, res.created, err
}

// analysisOffer is what the service offers the runs user starts: an
// enabled administrator's runs get everything, other users' runs follow the
// service switches (whoever owns the project).
func analysisOffer(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, user string) (nodeproto.AnalysisOffer, error) {
	var admin bool
	if err := q.QueryRow(ctx, "SELECT is_admin AND disabled_at IS NULL FROM users WHERE id=$1", user).Scan(&admin); err != nil {
		return nodeproto.AnalysisOffer{}, err
	}
	if admin {
		return nodeproto.AnalysisOffer{ModelReview: true, BuildScripts: true}, nil
	}
	st, err := readServiceSettings(ctx, q)
	if err != nil {
		return nodeproto.AnalysisOffer{}, err
	}
	return nodeproto.AnalysisOffer{ModelReview: st.OthersModelReview, BuildScripts: st.OthersBuildScripts}, nil
}

// OfferFor is analysisOffer for the runs user would start (GET /setup).
func (s *Store) OfferFor(ctx context.Context, user string) (nodeproto.AnalysisOffer, error) {
	if !validIDs(user) {
		return nodeproto.AnalysisOffer{}, domain.ErrInvalid
	}
	o, err := analysisOffer(ctx, s.pool, user)
	return o, classify(err)
}

// RecordGitHubToken writes the audit row of a user storing (set) or
// removing their own GitHub token. The token itself lives in a file only the
// service account reads; the row names the host and the account, never the
// token.
func (s *Store) RecordGitHubToken(ctx context.Context, user string, set bool, host, login string) error {
	if !validIDs(user) {
		return domain.ErrInvalid
	}
	action := AuditGitHubTokenRemoved
	detail := map[string]any{"host": truncateAudit(host)}
	if set {
		action = AuditGitHubTokenSet
		if login != "" {
			detail["login"] = truncateAudit(login)
		}
	}
	_, err := transaction(ctx, s, func(tx pgx.Tx) (bool, error) {
		return true, writeAudit(ctx, tx, userAudit(ctx, user, action).target("user", user).with(detail))
	})
	return err
}

// ServiceSettings are the administrator's service-wide switches.
type ServiceSettings struct {
	// OthersModelReview: runs started by a user who is not an
	// administrator may send source to the model (default true).
	OthersModelReview bool
	// OthersBuildScripts: runs started by a user who is not an
	// administrator may run their build scripts as the service account
	// (default false).
	OthersBuildScripts bool
	UpdatedAt          time.Time
}

// ServiceSettingsPatch changes the given switches (nil keeps a value).
type ServiceSettingsPatch struct {
	OthersModelReview, OthersBuildScripts *bool
}

func readServiceSettings(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}) (ServiceSettings, error) {
	var st ServiceSettings
	err := q.QueryRow(ctx, "SELECT others_model_review,others_build_scripts,updated_at FROM service_settings WHERE id=1").Scan(&st.OthersModelReview, &st.OthersBuildScripts, &st.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ServiceSettings{OthersModelReview: true}, nil
	}
	st.UpdatedAt = st.UpdatedAt.UTC()
	return st, err
}

// GetServiceSettings reads the switches (any signed-in user may know them:
// they say what is available to whom).
func (s *Store) GetServiceSettings(ctx context.Context) (ServiceSettings, error) {
	st, err := readServiceSettings(ctx, s.pool)
	return st, classify(err)
}

// SetServiceSettings applies p (an administrator). An unchanged value writes
// no audit row.
func (s *Store) SetServiceSettings(ctx context.Context, by Principal, p ServiceSettingsPatch) (ServiceSettings, error) {
	return transaction(ctx, s, func(tx pgx.Tx) (ServiceSettings, error) {
		if err := requireAdmin(ctx, tx, by); err != nil {
			return ServiceSettings{}, err
		}
		cur, err := readServiceSettings(ctx, tx)
		if err != nil {
			return cur, err
		}
		next := cur
		detail := map[string]any{}
		if p.OthersModelReview != nil && *p.OthersModelReview != cur.OthersModelReview {
			next.OthersModelReview = *p.OthersModelReview
			detail["others_model_review"] = next.OthersModelReview
		}
		if p.OthersBuildScripts != nil && *p.OthersBuildScripts != cur.OthersBuildScripts {
			next.OthersBuildScripts = *p.OthersBuildScripts
			detail["others_build_scripts"] = next.OthersBuildScripts
		}
		if len(detail) == 0 {
			return cur, nil
		}
		var updater any
		if !by.operator {
			updater = by.user
		}
		if err := tx.QueryRow(ctx, `INSERT INTO service_settings(id,others_model_review,others_build_scripts,updated_at,updated_by) VALUES(1,$1,$2,clock_timestamp(),$3)
			ON CONFLICT(id) DO UPDATE SET others_model_review=excluded.others_model_review,others_build_scripts=excluded.others_build_scripts,updated_at=excluded.updated_at,updated_by=excluded.updated_by
			RETURNING updated_at`, next.OthersModelReview, next.OthersBuildScripts, updater).Scan(&next.UpdatedAt); err != nil {
			return cur, err
		}
		next.UpdatedAt = next.UpdatedAt.UTC()
		return next, writeAudit(ctx, tx, principalAudit(by, AuditServiceSettingsSet).target("service", "settings").with(detail))
	})
}
