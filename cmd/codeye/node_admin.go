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
  codeye scheduler set [--global-max-running N] [--project-max-running N] [--artifact-quota-bytes N]
  codeye pool set --name NAME [--min-workers N] [--max-workers N] [--idle-timeout-seconds S]
                  [--scale-up-cooldown-seconds S] [--scale-down-cooldown-seconds S] [--expect-revision R]
  codeye pool list
`

const defaultEnrollTTL = 10 * time.Minute

// runNodeAdmin handles the trusted operator node commands. args is the full
// argument list after the program name. ok is false for anything else (for
// example the node agent commands owned by runNode).
func runNodeAdmin(ctx context.Context, args []string, out, stderr io.Writer) (code int, ok bool) {
	if len(args) >= 2 && args[0] == "scheduler" && args[1] == "set" {
		return schedulerSet(ctx, args[2:], out, stderr), true
	}
	if len(args) >= 2 && args[0] == "pool" && args[1] == "set" {
		return poolSet(ctx, args[2:], out, stderr), true
	}
	if len(args) >= 2 && args[0] == "pool" && args[1] == "list" {
		return poolList(ctx, args[2:], out, stderr), true
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
	var global, project *int
	var artifactQuota *int64
	g, p := -1, -1
	var aq int64 = -1
	fs, ok := parseSet("scheduler set", args, stderr, func(fs *flag.FlagSet) {
		fs.IntVar(&g, "global-max-running", -1, "")
		fs.IntVar(&p, "project-max-running", -1, "")
		fs.Int64Var(&aq, "artifact-quota-bytes", -1, "")
	})
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "global-max-running":
			global = &g
		case "project-max-running":
			project = &p
		case "artifact-quota-bytes":
			artifactQuota = &aq
		}
	})
	if !ok || (global == nil && project == nil && artifactQuota == nil) || (global != nil && (g < 0 || g > 10000)) || (project != nil && (p < 1 || p > 10000)) || (artifactQuota != nil && aq < 0) {
		fmt.Fprintln(stderr, "--global-max-running N (0..10000), --project-max-running N (1..10000) and/or --artifact-quota-bytes N (0 removes the limit) is required")
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		if global != nil || project != nil {
			l, err := s.SetSchedulerLimits(ctx, global, project)
			if err != nil {
				fmt.Fprintln(stderr, "cannot set scheduler limit:", err)
				return 1
			}
			fmt.Fprintln(out, "scheduler limit updated")
			printJSON(out, map[string]any{"global_max_running": l.GlobalMaxRunning, "project_max_running": l.ProjectMaxRunning, "revision": l.Revision})
		}
		if artifactQuota != nil {
			if err := s.SetArtifactQuota(ctx, *artifactQuota); err != nil {
				fmt.Fprintln(stderr, "cannot set the artifact quota:", err)
				return 1
			}
			u, err := s.ArtifactUsage(ctx)
			if err != nil {
				fmt.Fprintln(stderr, "cannot read the artifact usage:", err)
				return 1
			}
			fmt.Fprintln(out, "artifact quota updated")
			printJSON(out, map[string]any{"artifact_quota_bytes": u.Quota, "artifact_bytes": u.Bytes})
		}
		return 0
	})
}

func poolSet(ctx context.Context, args []string, out, stderr io.Writer) int {
	var name string
	ints := map[string]*int{}
	var expect int64 = -1
	names := []string{"min-workers", "max-workers", "idle-timeout-seconds", "scale-up-cooldown-seconds", "scale-down-cooldown-seconds"}
	vals := map[string]*int{}
	fs, ok := parseSet("pool set", args, stderr, func(fs *flag.FlagSet) {
		fs.StringVar(&name, "name", "", "")
		fs.Int64Var(&expect, "expect-revision", -1, "")
		for _, n := range names {
			v := new(int)
			vals[n] = v
			fs.IntVar(v, n, 0, "")
		}
	})
	if !ok || name == "" {
		fmt.Fprintln(stderr, "--name is required; see codeye --help")
		return 2
	}
	fs.Visit(func(f *flag.Flag) {
		if v, ok := vals[f.Name]; ok {
			ints[f.Name] = v
		}
	})
	u := postgres.PoolPolicyUpdate{Name: name, MinWorkers: ints["min-workers"], MaxWorkers: ints["max-workers"], IdleTimeoutSeconds: ints["idle-timeout-seconds"],
		ScaleUpCooldownSeconds: ints["scale-up-cooldown-seconds"], ScaleDownCooldownSeconds: ints["scale-down-cooldown-seconds"]}
	if expect >= 0 {
		u.ExpectRevision = &expect
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		p, warnings, err := s.SetPoolPolicy(ctx, u)
		if err != nil {
			fmt.Fprintln(stderr, "cannot set pool policy (min<=max, max 0 only with min 0, idle 1..86400s, cooldowns 0..3600s; revision must match):", err)
			return 1
		}
		item := policyJSON(p)
		item["warnings"] = warnings
		printJSON(out, item)
		return 0
	})
}

func policyJSON(p postgres.PoolPolicy) map[string]any {
	return map[string]any{"pool_id": p.PoolID, "pool": p.PoolName, "revision": p.Revision, "min_workers": p.MinWorkers, "max_workers": p.MaxWorkers,
		"idle_timeout_seconds": p.IdleTimeoutSeconds, "scale_up_cooldown_seconds": p.ScaleUpCooldownSeconds, "scale_down_cooldown_seconds": p.ScaleDownCooldownSeconds}
}

func poolList(ctx context.Context, args []string, out, stderr io.Writer) int {
	if !parse("pool list", args, stderr, func(*flag.FlagSet) {}) {
		return 2
	}
	return withStore(ctx, stderr, func(ctx context.Context, s *postgres.Store) int {
		pools, err := s.ListPools(ctx)
		if err != nil {
			fmt.Fprintln(stderr, "cannot list pools:", err)
			return 1
		}
		items := make([]map[string]any, 0, len(pools))
		for _, p := range pools {
			item := policyJSON(p.Policy)
			for k, v := range map[string]any{"scaler_epoch": p.Epoch, "capacity_state": p.CapacityState, "reasons": p.Reasons, "desired_workers": p.DesiredWorkers,
				"live_workers": p.LiveWorkers, "busy_workers": p.BusyWorkers, "draining_workers": p.DrainingWorkers, "pending_starts": p.PendingStarts,
				"queued_demand": p.QueuedDemand, "last_scale_up_at": p.LastScaleUpAt, "last_scale_down_at": p.LastScaleDownAt, "reconciled_at": p.ReconciledAt} {
				item[k] = v
			}
			items = append(items, item)
		}
		printJSON(out, map[string]any{"items": items})
		return 0
	})
}
