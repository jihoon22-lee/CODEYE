package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/httpapi"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

// nodeAdminUsage documents the operator node commands (W06).
const nodeAdminUsage = `  codeye node token create --pool NAME [--source-root ID[,ID...]] [--output .codeye/enroll-token] [--ttl 10m]
  codeye node token revoke --id TOKEN_ID
  codeye node list
  codeye node revoke --id NODE_ID
  codeye scheduler set --global-max-running N
`

const defaultEnrollTTL = 10 * time.Minute

// runNodeAdmin handles the trusted operator node commands. args is the full
// argument list after the program name. ok is false for anything else (for
// example the node agent commands owned by runNode).
func runNodeAdmin(ctx context.Context, args []string, out, stderr io.Writer) (code int, ok bool) {
	if len(args) >= 2 && args[0] == "scheduler" && args[1] == "set" {
		return schedulerSet(ctx, args[2:], out, stderr), true
	}
	if len(args) < 2 || args[0] != "node" {
		return 0, false
	}
	switch {
	case args[1] == "token" && len(args) >= 3 && args[2] == "create":
		return nodeTokenCreate(ctx, args[3:], out, stderr), true
	case args[1] == "token" && len(args) >= 3 && args[2] == "revoke":
		return nodeTokenRevoke(ctx, args[3:], out, stderr), true
	case args[1] == "token":
		fmt.Fprintln(stderr, "missing node token subcommand; use codeye --help")
		return 2, true
	case args[1] == "list":
		return nodeList(ctx, args[2:], out, stderr), true
	case args[1] == "revoke":
		return nodeRevoke(ctx, args[2:], out, stderr), true
	}
	return 0, false
}

func nodeTokenCreate(ctx context.Context, args []string, out, stderr io.Writer) int {
	var pool, roots, output string
	var ttl time.Duration
	if !parse("node token create", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&pool, "pool", "", "")
		fs.StringVar(&roots, "source-root", "", "")
		fs.StringVar(&output, "output", filepath.Join(".codeye", "enroll-token"), "")
		fs.DurationVar(&ttl, "ttl", defaultEnrollTTL, "")
	}) || pool == "" || output == "" {
		fmt.Fprintln(stderr, "--pool is required")
		return 2
	}
	if ttl < time.Minute || ttl > 24*time.Hour {
		fmt.Fprintln(stderr, "--ttl must be between 1m and 24h")
		return 2
	}
	var ids []string
	if roots != "" {
		ids = strings.Split(roots, ",")
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		if err := writeEnrollToken(ctx, s, pool, ids, ttl, output, out); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	})
}

// writeEnrollToken issues a single-use enrollment token and writes it to path
// (mode 0600, same-directory rename so a symlink at path is replaced, not
// followed). The token is never printed.
func writeEnrollToken(ctx context.Context, s *postgres.Store, pool string, roots []string, ttl time.Duration, path string, out io.Writer) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return errors.New("cannot create the enrollment token directory")
	}
	if st, err := os.Lstat(path); err == nil && !st.Mode().IsRegular() {
		return errors.New("enrollment token path exists and is not a regular file")
	}
	tmp, err := os.CreateTemp(dir, ".enroll-token-*")
	if err != nil {
		return errors.New("cannot create the enrollment token file")
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, errors.ErrUnsupported) {
		_ = tmp.Close()
		return errors.New("cannot restrict enrollment token file permissions")
	}
	token := httpapi.NewToken()
	issued, err := s.IssueEnrollmentToken(ctx, pool, roots, httpapi.TokenHash(token), ttl)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("cannot issue enrollment token: %w", err)
	}
	if _, err := tmp.WriteString(token + "\n"); err != nil {
		_ = tmp.Close()
		return errors.New("cannot write the enrollment token file")
	}
	if err := tmp.Close(); err != nil {
		return errors.New("cannot write the enrollment token file")
	}
	if err := os.Rename(tmpName, path); err != nil {
		return errors.New("cannot place the enrollment token file")
	}
	printJSON(out, map[string]any{
		"token_id": issued.ID, "pool_id": issued.PoolID, "pool": issued.PoolName,
		"source_root_ids": issued.SourceRootIDs, "expires_at": issued.ExpiresAt.UTC().Format(time.RFC3339),
		"token_file": path,
	})
	return nil
}

func nodeTokenRevoke(ctx context.Context, args []string, out, stderr io.Writer) int {
	var id string
	if !parse("node token revoke", args, stderr, func(fs *flag.FlagSet) { fs.StringVar(&id, "id", "", "") }) || id == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		if err := s.RevokeEnrollmentToken(ctx, id); err != nil {
			fmt.Fprintln(stderr, "cannot revoke enrollment token:", err)
			return 1
		}
		fmt.Fprintln(out, "enrollment token revoked")
		return 0
	})
}

func nodeList(ctx context.Context, args []string, out, stderr io.Writer) int {
	if !parse("node list", args, stderr, func(*flag.FlagSet) {}) {
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		nodes, err := s.ListNodes(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "cannot list nodes:", err)
			return 1
		}
		type item struct {
			ID                 string     `json:"id"`
			Pool               string     `json:"pool"`
			Name               string     `json:"name"`
			OS                 string     `json:"os"`
			Arch               string     `json:"arch"`
			Protocol           string     `json:"protocol_version"`
			EnrolledAt         time.Time  `json:"enrolled_at"`
			RevokedAt          *time.Time `json:"revoked_at"`
			LastHeartbeatAt    *time.Time `json:"last_heartbeat_at"`
			Capabilities       []string   `json:"capabilities"`
			SourceRootIDs      []string   `json:"source_root_ids"`
			ActiveReservations int64      `json:"active_reservations"`
			Workers            int64      `json:"live_workers"`
		}
		items := make([]item, 0, len(nodes))
		for _, n := range nodes {
			items = append(items, item{n.ID, n.PoolName, n.Name, n.OS, n.Arch, n.Protocol, n.EnrolledAt.UTC(), n.RevokedAt, n.LastHeartbeatAt, n.Capabilities, n.SourceRootIDs, n.ActiveReservations, n.Workers})
		}
		printJSON(out, map[string]any{"items": items})
		return 0
	})
}

func nodeRevoke(ctx context.Context, args []string, out, stderr io.Writer) int {
	var id string
	if !parse("node revoke", args, stderr, func(fs *flag.FlagSet) { fs.StringVar(&id, "id", "", "") }) || id == "" {
		fmt.Fprintln(stderr, "--id is required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		if err := s.RevokeNode(ctx, id); err != nil {
			fmt.Fprintln(stderr, "cannot revoke node:", err)
			return 1
		}
		fmt.Fprintln(out, "node revoked; its leases can no longer be renewed and will expire into recovery_required")
		return 0
	})
}

func schedulerSet(ctx context.Context, args []string, out, stderr io.Writer) int {
	n := -1
	if !parse("scheduler set", args, stderr, func(fs *flag.FlagSet) { fs.IntVar(&n, "global-max-running", -1, "") }) || n < 0 {
		fmt.Fprintln(stderr, "--global-max-running N (0..10000) is required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		if err := s.SetGlobalMaxRunning(ctx, n); err != nil {
			fmt.Fprintln(stderr, "cannot set scheduler limit:", err)
			return 1
		}
		fmt.Fprintln(out, "scheduler limit updated")
		return 0
	})
}
