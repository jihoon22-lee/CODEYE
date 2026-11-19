package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/jihoon22-lee/CODEYE/internal/personal"
	"github.com/jihoon22-lee/CODEYE/internal/quota"
	"github.com/jihoon22-lee/CODEYE/internal/storage/postgres"
)

type areaStatus struct {
	Area       string `json:"area"`
	Bytes      *int64 `json:"bytes"`
	Limit      int64  `json:"limit_bytes"`
	Exceeded   bool   `json:"exceeded"`
	Incomplete bool   `json:"incomplete,omitempty"`
	// Basis is how the enforced value is measured: node_heartbeat (agent
	// walk) or database_accounting (artifact rows); doctor walks the dirs.
	Basis string `json:"basis"`
}

type statusReport struct {
	State      string          `json:"state"`
	Processes  []procReport    `json:"processes"`
	Issues     []string        `json:"issues"`
	Controller map[string]any  `json:"controller"`
	Agent      map[string]any  `json:"agent"`
	InFlight   []attemptReport `json:"in_flight"`
	Storage    []areaStatus    `json:"storage"`
}

func personalStatus(ctx context.Context, args []string, out, stderr io.Writer) int {
	var format string
	cfg, layout, ok := loadPersonalConfig("status", args, stderr, func(fs *flag.FlagSet) { fs.StringVar(&format, "format", "text", "") })
	if !ok || (format != "text" && format != "json") {
		return 2
	}
	if err := personal.Supported(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	rep := statusReport{Issues: []string{}, Controller: map[string]any{}, Agent: map[string]any{}, InFlight: []attemptReport{}, Storage: []areaStatus{}}
	// Probe the agent lock only while no start/stop runs (it takes the lock
	// for an instant).
	probe := false
	if unlock, err := layout.Lock(); err == nil {
		probe = true
		defer unlock()
	} else if errors.Is(err, personal.ErrBusy) {
		rep.Issues = append(rep.Issues, "operation_in_progress")
	}
	state := layout.Inspect(cfg.Listen, probe)
	rep.Issues = append(rep.Issues, state.Issues...)
	for _, p := range state.Processes {
		pr := procReport{Role: p.Role, Before: p.State, After: p.State, Action: "none"}
		if p.Record != nil {
			pr.PID = p.Record.PID
		}
		rep.Processes = append(rep.Processes, pr)
	}
	code, ready := httpReady(cfg.Origin())
	rep.Controller["origin"] = cfg.Origin()
	rep.Controller["ready"] = ready
	rep.Controller["http_status"] = code
	healthy := ready && state.Running(personal.RoleController) && state.Running(personal.RoleAgent)
	node, _ := layout.AgentNodeID()
	rep.Agent["node_id"] = node
	var store *postgres.Store
	if os.Getenv("CODEYE_DATABASE_URL") != "" {
		sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		s, err := openStore(sctx)
		cancel()
		if err == nil {
			store = s
			defer store.Close()
		}
	}
	rep.Controller["database"] = store != nil
	var heartbeatFresh bool
	var nodeExceeded []string
	var artifacts *postgres.ArtifactUsage
	if store != nil {
		qctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if node != "" {
			if ns, err := store.NodeStateByID(qctx, node); err == nil {
				heartbeatFresh = ns.Fresh && !ns.Revoked
				nodeExceeded = ns.QuotaExceeded
				rep.Agent["revoked"] = ns.Revoked
				rep.Agent["last_heartbeat_at"] = ns.LastHeartbeatAt
				rep.Agent["heartbeat_fresh"] = ns.Fresh
				rep.Agent["workers"] = ns.Workers
				rep.Agent["storage_quota_exceeded"] = ns.QuotaExceeded
			} else {
				rep.Issues = append(rep.Issues, "node_unknown_to_database")
			}
			if a, err := store.NodeAttempts(qctx, node, nil); err == nil {
				rep.InFlight = attemptReports(a)
			}
		}
		if u, err := store.ArtifactUsage(qctx); err == nil {
			artifacts = &u
		}
	} else {
		rep.Issues = append(rep.Issues, "database_unavailable")
	}
	healthy = healthy && heartbeatFresh
	l := quota.Layout{AgentStateDir: layout.AgentDir(), GoModCache: cfg.Agent.GoModCache, LogDir: layout.LogDir()}
	for _, area := range quota.NodeAreas {
		as := areaStatus{Area: area, Limit: cfg.Quota.Limit(area), Basis: "node_heartbeat"}
		if u, err := quota.Measure(l, area); err == nil {
			b := u.Bytes
			as.Bytes, as.Incomplete = &b, u.Incomplete
		}
		for _, e := range nodeExceeded {
			as.Exceeded = as.Exceeded || e == area
		}
		rep.Storage = append(rep.Storage, as)
	}
	as := areaStatus{Area: quota.Artifacts, Limit: cfg.Quota.ArtifactsBytes, Basis: "database_accounting"}
	if artifacts != nil {
		b := artifacts.Bytes
		as.Bytes = &b
		as.Exceeded = artifacts.Quota != nil && artifacts.Bytes >= *artifacts.Quota
	}
	rep.Storage = append(rep.Storage, as)
	sort.Strings(rep.Issues)
	switch {
	case healthy && len(state.Issues) == 0:
		rep.State = "running"
	case !state.Running(personal.RoleController) && !state.Running(personal.RoleAgent) && len(state.Issues) == 0:
		rep.State = "stopped"
	default:
		rep.State = "degraded"
	}
	if format == "json" {
		printJSON(out, rep)
	} else {
		fmt.Fprintln(out, "state:", rep.State)
		for _, p := range rep.Processes {
			fmt.Fprintf(out, "%-10s %-12s pid=%d\n", p.Role, p.After, p.PID)
		}
		fmt.Fprintf(out, "controller %s ready=%v database=%v\n", cfg.Origin(), ready, store != nil)
		fmt.Fprintf(out, "agent node=%s heartbeat_fresh=%v workers=%v\n", node, heartbeatFresh, rep.Agent["workers"])
		for _, a := range rep.InFlight {
			fmt.Fprintf(out, "in flight: attempt %s run %s %s cleanup=%s run=%s\n", a.ID, a.RunID, a.Status, a.Cleanup, a.RunStatus)
		}
		for _, s := range rep.Storage {
			used := "unmeasured"
			if s.Bytes != nil {
				used = strconv.FormatInt(*s.Bytes, 10)
			}
			fmt.Fprintf(out, "storage %-10s %s bytes limit=%d exceeded=%v (%s)\n", s.Area, used, s.Limit, s.Exceeded, s.Basis)
		}
		for _, i := range rep.Issues {
			fmt.Fprintln(out, "issue:", i)
		}
	}
	if rep.State == "degraded" {
		return 1
	}
	return 0
}
